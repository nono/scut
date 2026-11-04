package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/nono/scut/internal/exit"
	"github.com/nono/scut/internal/transport"
)

const fileTimeout = 60 * time.Second

// shortcutHosts may receive the token on a redirect.
var shortcutHosts = map[string]bool{"media.app.shortcut.com": true, "api.app.shortcut.com": true}

// newFileClient returns the client for the file host. It never sends
// Authorization, and it sends Shortcut-Token only to Shortcut hosts.
func newFileClient() *http.Client {
	return &http.Client{
		Timeout: fileTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("too many redirects")
			}
			req.Header.Del("Authorization")
			if !shortcutHosts[req.URL.Hostname()] {
				req.Header.Del("Shortcut-Token")
			}
			return nil
		},
	}
}

type storyFile struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Path        string `json:"path"`
	Size        int64  `json:"size"`
	ContentType string `json:"content_type"`
}

type apiFile struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Size        int64  `json:"size"`
	ContentType string `json:"content_type"`
	URL         string `json:"url"`
}

func newStoryFiles(env Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:         "files [<story>] --out <dir>",
		Short:       "Download the files attached to a story",
		Args:        cobra.MaximumNArgs(1),
		Annotations: map[string]string{opsAnnotation: "listStoryFiles"},
	}
	outFlag := cmd.Flags().String("out", "", "directory to write the files to")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if *outFlag == "" {
			return exit.Usagef("--out is required")
		}
		ids, err := storyIDs(env, args)
		if err != nil {
			return err
		}
		out, err := prepareOutDir(*outFlag)
		if err != nil {
			return err
		}
		return withDeps(env, func(cmd *cobra.Command, _ []string, d *deps) error {
			ctx := cmd.Context()
			items, err := transport.PageQuery(ctx, d.Doer, "stories/"+strconv.FormatInt(ids[0], 10)+"/files", url.Values{}, -1)
			if err != nil {
				return err
			}
			client := env.FileClient
			if client == nil {
				client = newFileClient()
			}
			done := []storyFile{}
			used := map[string]bool{}
			var failure error
			for _, it := range items {
				var f apiFile
				if err := json.Unmarshal(it, &f); err != nil {
					failure = &exit.Error{Code: exit.Other, Msg: "bad file entry"}
					break
				}
				name := uniqueName(cleanName(f.Name, f.ID), f.ID, used)
				p := filepath.Join(out, name)
				if err := download(ctx, client, d.Settings.Token, f, out, p); err != nil {
					failure = err
					break
				}
				done = append(done, storyFile{ID: f.ID, Name: name, Path: p, Size: f.Size, ContentType: f.ContentType})
			}
			if err := d.Out(done); err != nil {
				return err
			}
			return failure
		})(cmd, args)
	}
	return cmd
}

// prepareOutDir creates dir if needed and returns its absolute path.
func prepareOutDir(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	fi, err := os.Stat(abs)
	switch {
	case err == nil && !fi.IsDir():
		return "", exit.Usagef("--out %q is not a directory", dir)
	case err == nil:
	case errors.Is(err, os.ErrNotExist):
		if err := os.MkdirAll(abs, 0o755); err != nil {
			return "", &exit.Error{Code: exit.Other, Msg: err.Error()}
		}
	default:
		return "", &exit.Error{Code: exit.Other, Msg: err.Error()}
	}
	return abs, nil
}

// cleanName returns the last path element of a file name from the API.
func cleanName(name string, id int64) string {
	base := path.Base(strings.ReplaceAll(name, "\\", "/"))
	if base == "." || base == ".." || base == "/" || base == "" {
		return "file-" + strconv.FormatInt(id, 10)
	}
	return base
}

// uniqueName adds the file ID before the extension if name is taken.
func uniqueName(name string, id int64, used map[string]bool) string {
	if used[name] {
		ext := path.Ext(name)
		name = strings.TrimSuffix(name, ext) + "-" + strconv.FormatInt(id, 10) + ext
	}
	used[name] = true
	return name
}

// download writes one file through a temp file in dir, then renames it to dest.
func download(ctx context.Context, c *http.Client, token string, f apiFile, dir, dest string) (err error) {
	scrub := func(s string) string {
		if token != "" {
			s = strings.ReplaceAll(s, token, "***")
		}
		return s
	}
	netErr := func(err error) error {
		if ctx.Err() != nil {
			return fmt.Errorf("download file %d: %w", f.ID, ctx.Err())
		}
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return &exit.Error{Code: exit.Unavailable, Msg: scrub(fmt.Sprintf("download file %d: %v", f.ID, err))}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.URL, nil)
	if err != nil {
		return &exit.Error{Code: exit.Other, Msg: scrub(fmt.Sprintf("file %d: bad url", f.ID))}
	}
	req.Header.Set("Shortcut-Token", token)
	resp, err := c.Do(req)
	if err != nil {
		return netErr(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		code := exit.Other
		if resp.StatusCode >= 500 {
			code = exit.Unavailable
		}
		return &exit.Error{Code: code, Status: resp.StatusCode, Msg: fmt.Sprintf("download file %d: status %d", f.ID, resp.StatusCode)}
	}
	tmp, err := os.CreateTemp(dir, ".scut-*")
	if err != nil {
		return &exit.Error{Code: exit.Other, Msg: err.Error()}
	}
	defer func() {
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
		}
	}()
	n, err := io.Copy(tmp, resp.Body)
	if err != nil {
		return netErr(err)
	}
	if n != f.Size {
		return &exit.Error{Code: exit.Other, Msg: fmt.Sprintf("download file %d: got %d bytes, expected %d", f.ID, n, f.Size)}
	}
	if err := tmp.Close(); err != nil {
		return &exit.Error{Code: exit.Other, Msg: err.Error()}
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return &exit.Error{Code: exit.Other, Msg: err.Error()}
	}
	if err := os.Rename(tmp.Name(), dest); err != nil {
		return &exit.Error{Code: exit.Other, Msg: err.Error()}
	}
	return nil
}
