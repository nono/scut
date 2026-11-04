package ref_test

import (
	"errors"
	"os"
	"os/exec"
	"testing"

	"github.com/nono/scut/internal/exit"
	"github.com/nono/scut/internal/ref"
)

func TestStoryRef(t *testing.T) {
	for in, want := range map[string]int64{
		"sc-5409": 5409,
		"5409":    5409,
		"https://app.shortcut.com/example-ws/story/5409/some-slug": 5409,
		"https://app.shortcut.com/acme/story/12":                         12,
		"bug/sc-5409/fix-wave":                                           5409,
		"user42/sc-17-x":                                                 17,
	} {
		got, err := ref.Story(in)
		if err != nil || got != want {
			t.Errorf("%q: got %d, %v; want %d", in, got, err, want)
		}
	}
}

func TestStoryRefBad(t *testing.T) {
	for _, in := range []string{"abc", "sc-", "54a", "", "https://app.shortcut.com/acme/epic/5", "99999999999999999999"} {
		_, err := ref.Story(in)
		var e *exit.Error
		if !errors.As(err, &e) || e.Code != exit.Usage {
			t.Errorf("%q: got %v, want usage error", in, err)
		}
	}
}

func TestFromGitOutsideRepo(t *testing.T) {
	_, err := ref.FromGit(t.TempDir())
	var e *exit.Error
	if !errors.As(err, &e) || e.Code != exit.Usage || e.Msg != "give a story ID" {
		t.Errorf("got %v", err)
	}
}

// gitRepo makes a repository in a temp dir with one empty commit on branch.
func gitRepo(t *testing.T, branch string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found")
	}
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", branch)
	git(t, dir, "-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false",
		"commit", "-q", "--allow-empty", "--no-verify", "-m", "x")
	return dir
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

func TestFromGitBranch(t *testing.T) {
	id, err := ref.FromGit(gitRepo(t, "bug/sc-5409/x"))
	if err != nil || id != 5409 {
		t.Errorf("got %d, %v; want 5409", id, err)
	}
}

func TestFromGitDetached(t *testing.T) {
	dir := gitRepo(t, "bug/sc-5409/x")
	git(t, dir, "checkout", "-q", "--detach")
	_, err := ref.FromGit(dir)
	var e *exit.Error
	if !errors.As(err, &e) || e.Code != exit.Usage || e.Msg != "give a story ID" {
		t.Errorf("got %v", err)
	}
}
