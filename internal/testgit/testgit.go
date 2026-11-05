// Package testgit makes git repositories for tests.
package testgit

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Git runs git in dir with a test identity and no user or system config, and
// returns its trimmed output. An error fails t.
func Git(t testing.TB, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@example.com",
		"-c", "commit.gpgsign=false"}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// isolate skips t when git is missing, and gives git no user or system config
// for the rest of t, also for the code under test.
func isolate(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found")
	}
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
}

// Repo makes a repository with one empty commit on branch, and returns its path.
func Repo(t *testing.T, branch string) string {
	t.Helper()
	isolate(t)
	dir := t.TempDir()
	Git(t, dir, "init", "-q", "-b", branch)
	Git(t, dir, "commit", "-q", "--allow-empty", "--no-verify", "-m", "x")
	return dir
}

// Repos makes a bare origin and a clone of it at <tmp>/main, with one commit
// on main and origin/HEAD set. The paths have no symbolic links.
func Repos(t *testing.T) (origin, main string) {
	t.Helper()
	isolate(t)
	tmp, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	origin = filepath.Join(tmp, "origin.git")
	main = filepath.Join(tmp, "main")
	Git(t, tmp, "init", "-q", "--bare", "-b", "main", origin)
	Git(t, tmp, "clone", "-q", origin, main)
	Git(t, main, "commit", "-q", "--allow-empty", "-m", "init")
	Git(t, main, "push", "-q", "origin", "main")
	Git(t, main, "remote", "set-head", "origin", "main")
	return origin, main
}

// WrapGit puts a git first on the PATH for the rest of t: it runs prelude, a
// shell text, then the real git with the same arguments. In prelude, $LOG is
// a file that WrapGit returns, for prelude to write to.
func WrapGit(t *testing.T, prelude string) (log string) {
	t.Helper()
	real, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not found")
	}
	bin := t.TempDir()
	log = filepath.Join(t.TempDir(), "log")
	script := "#!/bin/sh\nLOG='" + log + "'\n" + prelude + "\nexec '" + real + "' \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return log
}

// Push pushes a new branch to origin from a second clone.
func Push(t *testing.T, origin, branch string) {
	t.Helper()
	other := filepath.Join(t.TempDir(), "other")
	Git(t, filepath.Dir(other), "clone", "-q", origin, other)
	Git(t, other, "switch", "-q", "-c", branch)
	Git(t, other, "push", "-q", "origin", branch)
}
