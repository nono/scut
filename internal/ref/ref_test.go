package ref_test

import (
	"errors"
	"testing"

	"github.com/nono/scut/internal/exit"
	"github.com/nono/scut/internal/ref"
	"github.com/nono/scut/internal/testgit"
)

func TestStoryRef(t *testing.T) {
	for in, want := range map[string]int64{
		"sc-5409": 5409,
		"5409":    5409,
		"https://app.shortcut.com/example-ws/story/5409/some-slug": 5409,
		"https://app.shortcut.com/acme/story/12":                   12,
		"bug/sc-5409/fix-wave":                                     5409,
		"user42/sc-17-x":                                           17,
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

func TestFromGitBranch(t *testing.T) {
	id, err := ref.FromGit(testgit.Repo(t, "bug/sc-5409/x"))
	if err != nil || id != 5409 {
		t.Errorf("got %d, %v; want 5409", id, err)
	}
}

func TestFromGitDetached(t *testing.T) {
	dir := testgit.Repo(t, "bug/sc-5409/x")
	testgit.Git(t, dir, "checkout", "-q", "--detach")
	_, err := ref.FromGit(dir)
	var e *exit.Error
	if !errors.As(err, &e) || e.Code != exit.Usage || e.Msg != "give a story ID" {
		t.Errorf("got %v", err)
	}
}
