//go:build live

// Package live holds a read-only smoke test against the real Shortcut API.
// It builds the real binary and runs it with a temp cache and an empty config.
//
// The repository is public, so the workspace, the story and the expected
// values come from environment variables, never from the code:
//
//	SCUT_LIVE_STORY        a story ID (required, else the test skips)
//	SCUT_LIVE_WORKSPACE    the expected workspace slug
//	SCUT_LIVE_STORY_NAME   the expected name of the story
//	SCUT_LIVE_FILE         the name of a file attached to the story
//	SCUT_LIVE_FILE_SIZE    its size in bytes
//	SCUT_LIVE_BRANCH       the expected prefix of the branch name
//	SCUT_LIVE_DOC_URL      the web app URL of a doc that is not empty
//	SCUT_LIVE_DOC_TITLE    its title
//
// A check whose variable is not set is skipped.
package live

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func token(t *testing.T) string {
	t.Helper()
	if v := strings.TrimSpace(os.Getenv("SCUT_TOKEN")); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err == nil {
		if b, err := os.ReadFile(filepath.Join(home, ".config", "shortcut-v4-token")); err == nil {
			if v := strings.TrimSpace(string(b)); v != "" {
				return v
			}
		}
	}
	t.Skip("no token: set SCUT_TOKEN or write ~/.config/shortcut-v4-token")
	return ""
}

// want returns the value of the env variable name, or skips t when it is not set.
func want(t *testing.T, name string) string {
	t.Helper()
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		t.Skipf("%s is not set", name)
	}
	return v
}

func TestLive(t *testing.T) {
	story := want(t, "SCUT_LIVE_STORY")
	id, err := strconv.ParseInt(story, 10, 64)
	if err != nil {
		t.Fatalf("SCUT_LIVE_STORY %q is not a story ID", story)
	}
	tok := token(t)
	bin := filepath.Join(t.TempDir(), "scut")
	build := exec.Command("go", "build", "-o", bin, "github.com/nono/scut/cmd/scut")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	env := append(os.Environ(),
		"SCUT_TOKEN="+tok,
		"XDG_CACHE_HOME="+t.TempDir(),
		"XDG_CONFIG_HOME="+t.TempDir(),
		"SCUT_OUTPUT=json",
	)
	run := func(t *testing.T, args ...string) []byte {
		t.Helper()
		cmd := exec.Command(bin, args...)
		cmd.Env = env
		cmd.Dir = t.TempDir() // no .scut.toml above it
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("scut %s: %v\nstderr: %s", strings.Join(args, " "), err, stderr.String())
		}
		return stdout.Bytes()
	}

	t.Run("whoami", func(t *testing.T) {
		var v struct{ Workspace string }
		if err := json.Unmarshal(run(t, "whoami"), &v); err != nil {
			t.Fatal(err)
		}
		if ws := want(t, "SCUT_LIVE_WORKSPACE"); v.Workspace != ws {
			t.Errorf("workspace %q, want %q", v.Workspace, ws)
		}
	})
	t.Run("story view", func(t *testing.T) {
		var v struct {
			ID   int64
			Name string
		}
		if err := json.Unmarshal(run(t, "story", "view", story), &v); err != nil {
			t.Fatal(err)
		}
		if v.ID != id {
			t.Errorf("id %d, want %d", v.ID, id)
		}
		if name := want(t, "SCUT_LIVE_STORY_NAME"); v.Name != name {
			t.Errorf("name %q, want %q", v.Name, name)
		}
	})
	t.Run("story search", func(t *testing.T) {
		var v []json.RawMessage
		if err := json.Unmarshal(run(t, "story", "search", "id:"+story), &v); err != nil {
			t.Fatal(err)
		}
		if len(v) != 1 {
			t.Errorf("%d items, want 1", len(v))
		}
	})
	t.Run("story files", func(t *testing.T) {
		file := want(t, "SCUT_LIVE_FILE")
		size, err := strconv.ParseInt(want(t, "SCUT_LIVE_FILE_SIZE"), 10, 64)
		if err != nil {
			t.Fatal("SCUT_LIVE_FILE_SIZE is not a number")
		}
		dir := t.TempDir()
		run(t, "story", "files", story, "--out", dir)
		fi, err := os.Stat(filepath.Join(dir, file))
		if err != nil {
			t.Fatal(err)
		}
		if fi.Size() != size {
			t.Errorf("size %d, want %d", fi.Size(), size)
		}
	})
	t.Run("story branch", func(t *testing.T) {
		var v struct{ Branch string }
		if err := json.Unmarshal(run(t, "story", "branch", story), &v); err != nil {
			t.Fatal(err)
		}
		if p := want(t, "SCUT_LIVE_BRANCH"); !strings.HasPrefix(v.Branch, p) {
			t.Errorf("branch %q, want prefix %q", v.Branch, p)
		}
	})
	t.Run("doc view and search", func(t *testing.T) {
		docURL, title := want(t, "SCUT_LIVE_DOC_URL"), want(t, "SCUT_LIVE_DOC_TITLE")
		path := filepath.Join(t.TempDir(), "doc.md")
		var out struct {
			ID          string `json:"id"`
			Title       string `json:"title"`
			ContentFile string `json:"content_file"`
		}
		if err := json.Unmarshal(run(t, "doc", "view", docURL, "--out", path), &out); err != nil {
			t.Fatal(err)
		}
		if out.Title != title || out.ContentFile != path {
			t.Errorf("title %q, content_file %q; want %q, %q", out.Title, out.ContentFile, title, path)
		}
		if fi, err := os.Stat(path); err != nil || fi.Size() == 0 {
			t.Errorf("content file: %v", err)
		}
		type doc struct {
			ID string `json:"id"`
		}
		var found []doc
		if err := json.Unmarshal(run(t, "doc", "search", title, "--limit", "all"), &found); err != nil {
			t.Fatal(err)
		}
		if !slices.ContainsFunc(found, func(f doc) bool { return f.ID == out.ID }) {
			t.Errorf("doc search %q: no doc %s in %d results", title, out.ID, len(found))
		}
	})
}
