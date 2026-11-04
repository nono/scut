package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/nono/scut/internal/exit"
	"github.com/nono/scut/internal/transport"
)

const fileTimeout = 60 * time.Second

// errRedirect is the failure of CheckRedirect. A retry does not heal it.
var errRedirect = errors.New("too many redirects")

// defaultFileHosts may receive the token.
var defaultFileHosts = []string{"media.app.shortcut.com", "api.app.shortcut.com"}

// tokenAllowed reports whether the token may go to u: https and a listed host.
func tokenAllowed(u *url.URL, hosts []string) bool {
	return u.Scheme == "https" && slices.Contains(hosts, u.Hostname())
}

// fileClient returns a copy of c (or a new client) for the file host. It has
// a timeout and never sends Authorization. It sends Shortcut-Token on a
// redirect only to an allowed target.
func fileClient(c *http.Client, hosts []string) *http.Client {
	n := http.Client{}
	if c != nil {
		n = *c
	}
	if n.Timeout == 0 {
		n.Timeout = fileTimeout
	}
	n.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errRedirect
		}
		req.Header.Del("Authorization")
		if !tokenAllowed(req.URL, hosts) {
			req.Header.Del("Shortcut-Token")
		}
		return nil
	}
	return &n
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
		Annotations: map[string]string{OpsAnnotation: "listStoryFiles"},
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
		out, err := checkOutDir(*outFlag)
		if err != nil {
			return err
		}
		return withDeps(env, func(cmd *cobra.Command, _ []string, d *deps) error {
			ctx := cmd.Context()
			items, err := transport.PageQuery(ctx, d.Doer, "stories/"+strconv.FormatInt(ids[0], 10)+"/files", url.Values{}, -1)
			if err != nil {
				return err
			}
			hosts := env.FileHosts
			if hosts == nil {
				hosts = defaultFileHosts
			}
			client := fileClient(env.FileClient, hosts)
			if len(items) > 0 {
				if err := os.MkdirAll(out, 0o755); err != nil {
					return &exit.Error{Code: exit.Other, Msg: err.Error()}
				}
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
				p, err := downloadRetry(ctx, d, client, hosts, f, out, name)
				if err != nil {
					failure = err
					break
				}
				done = append(done, storyFile{ID: f.ID, Name: filepath.Base(p), Path: p, Size: f.Size, ContentType: f.ContentType})
			}
			if err := d.Out(done); err != nil {
				return err
			}
			return failure
		})(cmd, args)
	}
	return cmd
}

// checkOutDir returns the absolute path of dir. It fails if dir exists and is
// not a directory. It does not create dir.
func checkOutDir(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	fi, err := os.Stat(abs)
	switch {
	case err == nil && !fi.IsDir():
		return "", exit.Usagef("--out %q is not a directory", dir)
	case err == nil, errors.Is(err, os.ErrNotExist):
		return abs, nil
	default:
		return "", &exit.Error{Code: exit.Other, Msg: err.Error()}
	}
}

// cleanName returns the last path element of a file name from the API.
func cleanName(name string, id int64) string {
	base := path.Base(strings.ReplaceAll(name, "\\", "/"))
	if base == "." || base == ".." || base == "/" || base == "" {
		return "file-" + strconv.FormatInt(id, 10)
	}
	return base
}

// altName returns the name to try after n taken names: name, then
// <base>-<id><ext>, then <base>-<id>-<n><ext>.
func altName(name string, id int64, n int) string {
	if n == 0 {
		return name
	}
	ext := path.Ext(name)
	base := strings.TrimSuffix(name, ext) + "-" + strconv.FormatInt(id, 10)
	if n == 1 {
		return base + ext
	}
	return base + "-" + strconv.Itoa(n) + ext
}

// uniqueName adds the file ID before the extension if name is taken.
func uniqueName(name string, id int64, used map[string]bool) string {
	n := 0
	for used[altName(name, id, n)] {
		n++
	}
	name = altName(name, id, n)
	used[name] = true
	return name
}

// link is os.Link; tests replace it.
var link = os.Link

// place links tmp to a free name in dir, from name on (see altName), then
// removes tmp. A link never replaces a file, so two runs cannot replace each
// other's file either. If the file system has no hard links, it copies tmp
// to a new file in place of the link, which never replaces a file either. It
// returns the path.
func place(tmp, dir, name string, id int64) (string, error) {
	copying := false
	for n := 0; ; n++ {
		dest := filepath.Join(dir, altName(name, id, n))
		var err error
		if !copying {
			err = link(tmp, dest)
			copying = err != nil && !errors.Is(err, fs.ErrExist)
		}
		if copying {
			err = copyNew(tmp, dest)
		}
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		_ = os.Remove(tmp)
		return dest, nil
	}
}

// copyNew copies src to dest, a new file with mode 0644, and syncs it. It
// fails with fs.ErrExist if dest exists.
func copyNew(src, dest string) (err error) {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := out.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			_ = os.Remove(dest)
		}
	}()
	if _, err = io.Copy(out, in); err != nil {
		return err
	}
	if err = out.Chmod(0o644); err != nil { // the umask can remove bits
		return err
	}
	return out.Sync()
}

// downloadRetry runs download with transport.Retry: after a 429, a 502, a
// 503, a 504 or a lost connection it tries again, with the same backoff and
// try limit as a call. No new try starts after 20 s from the first, but each
// try has the 60 s limit of the file client (fileTimeout), not the time left
// in the 20 s: a large file needs more than 20 s for one try. Each try writes
// a new temp file.
func downloadRetry(ctx context.Context, d *deps, c *http.Client, hosts []string, f apiFile, dir, name string) (string, error) {
	var p string
	err := transport.Retry(ctx, d.Clock, fmt.Sprintf("download file %d", f.ID), func(int, time.Duration) error {
		var err error
		p, err = download(ctx, c, hosts, d.Settings.Token, f, dir, name)
		return err
	})
	return p, err
}

// download writes one file through a temp file in dir, then links it to a
// free name from name on. It returns the path.
func download(ctx context.Context, c *http.Client, hosts []string, token string, f apiFile, dir, name string) (dest string, err error) {
	scrub := func(s string) string { return transport.Scrub(s, token) }
	netErr := func(err error) error {
		if ctx.Err() != nil {
			return fmt.Errorf("download file %d: %w", f.ID, ctx.Err())
		}
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		code := exit.Unavailable
		if transport.IsTLS(err) || errors.Is(err, errRedirect) { // a retry does not heal it
			code = exit.Other
		}
		return &exit.Error{Code: code, Msg: scrub(fmt.Sprintf("download file %d: %v", f.ID, err))}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.URL, nil)
	if err != nil {
		return "", &exit.Error{Code: exit.Other, Msg: scrub(fmt.Sprintf("file %d: bad url", f.ID))}
	}
	if !tokenAllowed(req.URL, hosts) {
		return "", &exit.Error{Code: exit.Other, Msg: "file host not allowed: " + req.URL.Hostname()}
	}
	req.Header.Set("Shortcut-Token", token)
	resp, err := c.Do(req)
	if err != nil {
		return "", netErr(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", &exit.Error{Code: transport.CodeFor(resp.StatusCode), Status: resp.StatusCode, RetryAfter: transport.RetryAfter(resp),
			Msg: fmt.Sprintf("download file %d: status %d", f.ID, resp.StatusCode)}
	}
	tmp, err := os.CreateTemp(dir, ".scut-*")
	if err != nil {
		return "", &exit.Error{Code: exit.Other, Msg: err.Error()}
	}
	defer func() {
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
		}
	}()
	n, err := io.Copy(tmp, resp.Body)
	if err != nil {
		return "", netErr(err)
	}
	if n != f.Size {
		return "", &exit.Error{Code: exit.Other, Msg: fmt.Sprintf("download file %d: got %d bytes, expected %d", f.ID, n, f.Size)}
	}
	if err := tmp.Close(); err != nil {
		return "", &exit.Error{Code: exit.Other, Msg: err.Error()}
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return "", &exit.Error{Code: exit.Other, Msg: err.Error()}
	}
	if dest, err = place(tmp.Name(), dir, name, f.ID); err != nil {
		return "", &exit.Error{Code: exit.Other, Msg: err.Error()}
	}
	return dest, nil
}
