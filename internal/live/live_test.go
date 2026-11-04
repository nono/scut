//go:build live

// Package live holds a read-only smoke test against the real Shortcut API.
// It builds the real binary and runs it with a temp cache and an empty config.
package live

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const storyName = "Example story with one attachment"

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

func TestLive(t *testing.T) {
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
	run := func(args ...string) []byte {
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
		if err := json.Unmarshal(run("whoami"), &v); err != nil {
			t.Fatal(err)
		}
		if v.Workspace != "example-ws" {
			t.Errorf("workspace %q", v.Workspace)
		}
	})
	t.Run("story view", func(t *testing.T) {
		var v struct {
			ID   int64
			Name string
		}
		if err := json.Unmarshal(run("story", "view", "4966"), &v); err != nil {
			t.Fatal(err)
		}
		if v.ID != 4966 || v.Name != storyName {
			t.Errorf("got %d %q", v.ID, v.Name)
		}
	})
	t.Run("story search", func(t *testing.T) {
		var v []json.RawMessage
		if err := json.Unmarshal(run("story", "search", "id:4966"), &v); err != nil {
			t.Fatal(err)
		}
		if len(v) != 1 {
			t.Errorf("%d items, want 1", len(v))
		}
	})
	t.Run("story files", func(t *testing.T) {
		dir := t.TempDir()
		run("story", "files", "4966", "--out", dir)
		fi, err := os.Stat(filepath.Join(dir, "example_attachment.txt"))
		if err != nil {
			t.Fatal(err)
		}
		if fi.Size() != 9692 {
			t.Errorf("size %d, want 9692", fi.Size())
		}
	})
	t.Run("story branch", func(t *testing.T) {
		var v struct{ Branch string }
		if err := json.Unmarshal(run("story", "branch", "4966"), &v); err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(v.Branch, "bug/a test story/") {
			t.Errorf("branch %q", v.Branch)
		}
	})
}
