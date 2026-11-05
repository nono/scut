package gitwt_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/nono/scut/internal/exit"
	"github.com/nono/scut/internal/gitwt"
)

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// newRepos makes a bare origin and a clone of it at <tmp>/main, with one commit on main.
func newRepos(t *testing.T) (origin, main string) {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	tmp, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	origin = filepath.Join(tmp, "origin.git")
	main = filepath.Join(tmp, "main")
	git(t, tmp, "init", "-q", "--bare", "-b", "main", origin)
	git(t, tmp, "clone", "-q", origin, main)
	git(t, main, "commit", "-q", "--allow-empty", "-m", "init")
	git(t, main, "push", "-q", "origin", "main")
	git(t, main, "remote", "set-head", "origin", "main")
	return origin, main
}

// pushFromOther pushes branch to origin from a second clone.
func pushFromOther(t *testing.T, origin, branch string) {
	t.Helper()
	other := filepath.Join(t.TempDir(), "other")
	git(t, filepath.Dir(other), "clone", "-q", origin, other)
	git(t, other, "switch", "-q", "-c", branch)
	git(t, other, "push", "-q", "origin", branch)
}

func open(t *testing.T, dir string) gitwt.Repo {
	t.Helper()
	r, err := gitwt.Open(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func find(t *testing.T, r gitwt.Repo, id int64) []gitwt.Match {
	t.Helper()
	m, err := r.Find(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestHasID(t *testing.T) {
	for _, c := range []struct {
		b    string
		want bool
	}{{"bug/sc-12/x", true}, {"bug/sc-123/x", false}, {"sc-12", true}, {"main", false}} {
		if got := gitwt.HasID(c.b, 12); got != c.want {
			t.Errorf("HasID(%q, 12) = %v", c.b, got)
		}
	}
}

func TestOpenFromLinkedWorktreeSubdir(t *testing.T) {
	_, main := newRepos(t)
	other := filepath.Join(filepath.Dir(main), "other")
	git(t, main, "worktree", "add", "-q", "--detach", other)
	if err := os.Mkdir(filepath.Join(other, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	r := open(t, filepath.Join(other, "sub"))
	if r.Main != main {
		t.Errorf("Main %q, want %q", r.Main, main)
	}
	if got := r.DefaultDir(1234); got != filepath.Join(filepath.Dir(main), "1234") {
		t.Errorf("DefaultDir %q", got)
	}
}

func TestOpenOutsideRepo(t *testing.T) {
	t.Setenv("GIT_CEILING_DIRECTORIES", os.TempDir())
	_, err := gitwt.Open(context.Background(), t.TempDir())
	var e *exit.Error
	if !errors.As(err, &e) || e.Code != exit.Usage {
		t.Fatalf("err %v", err)
	}
}

func TestFindWorktreeInMain(t *testing.T) {
	_, main := newRepos(t)
	git(t, main, "switch", "-q", "-c", "bug/sc-1234/x")
	want := []gitwt.Match{{Branch: "bug/sc-1234/x", Dir: main, Source: gitwt.Worktree}}
	if got := find(t, open(t, main), 1234); !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v", got)
	}
}

func TestFindSkipsDetached(t *testing.T) {
	_, main := newRepos(t)
	git(t, main, "worktree", "add", "-q", "--detach", filepath.Join(filepath.Dir(main), "d"))
	if got := find(t, open(t, main), 1234); got != nil {
		t.Errorf("got %+v", got)
	}
}

func TestFindPrunable(t *testing.T) {
	_, main := newRepos(t)
	dir := filepath.Join(filepath.Dir(main), "1234")
	git(t, main, "worktree", "add", "-q", "-b", "bug/sc-1234/x", dir)
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	want := []gitwt.Match{{Branch: "bug/sc-1234/x", Dir: dir, Source: gitwt.Worktree, Prunable: true}}
	if got := find(t, open(t, main), 1234); !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v", got)
	}
}

func TestFindLocal(t *testing.T) {
	_, main := newRepos(t)
	git(t, main, "branch", "bug/sc-1234/x")
	want := []gitwt.Match{{Branch: "bug/sc-1234/x", Source: gitwt.Local}}
	if got := find(t, open(t, main), 1234); !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v", got)
	}
}

func TestFindRemote(t *testing.T) {
	origin, main := newRepos(t)
	pushFromOther(t, origin, "bug/sc-1234/x")
	git(t, main, "fetch", "-q", "origin")
	want := []gitwt.Match{{Branch: "bug/sc-1234/x", Source: gitwt.Remote}}
	if got := find(t, open(t, main), 1234); !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v", got)
	}
}

func TestFindLocalAndRemoteSameName(t *testing.T) {
	_, main := newRepos(t)
	git(t, main, "branch", "bug/sc-1234/x")
	git(t, main, "push", "-q", "origin", "bug/sc-1234/x")
	want := []gitwt.Match{{Branch: "bug/sc-1234/x", Source: gitwt.Local}}
	if got := find(t, open(t, main), 1234); !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v", got)
	}
}

func TestFindTwoLocal(t *testing.T) {
	_, main := newRepos(t)
	git(t, main, "branch", "bug/sc-1234/b")
	git(t, main, "branch", "bug/sc-1234/a")
	want := []gitwt.Match{{Branch: "bug/sc-1234/a", Source: gitwt.Local}, {Branch: "bug/sc-1234/b", Source: gitwt.Local}}
	if got := find(t, open(t, main), 1234); !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v", got)
	}
}

func TestFindSourceOrder(t *testing.T) {
	origin, main := newRepos(t)
	pushFromOther(t, origin, "bug/sc-1234/b")
	git(t, main, "fetch", "-q", "origin")
	git(t, main, "branch", "bug/sc-1234/a")
	want := []gitwt.Match{{Branch: "bug/sc-1234/a", Source: gitwt.Local}}
	if got := find(t, open(t, main), 1234); !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v", got)
	}
}

func TestFetchFindsNewRemote(t *testing.T) {
	origin, main := newRepos(t)
	pushFromOther(t, origin, "bug/sc-1234/x")
	r := open(t, main)
	if got := find(t, r, 1234); got != nil {
		t.Fatalf("before fetch: %+v", got)
	}
	if err := r.Fetch(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := []gitwt.Match{{Branch: "bug/sc-1234/x", Source: gitwt.Remote}}
	if got := find(t, r, 1234); !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v", got)
	}
}

func TestBase(t *testing.T) {
	_, main := newRepos(t)
	r := open(t, main)
	if b, err := r.Base(context.Background()); err != nil || b != "origin/HEAD" {
		t.Errorf("base %q, %v", b, err)
	}
	git(t, main, "remote", "set-head", "origin", "-d")
	if b, err := r.Base(context.Background()); err != nil || b != "origin/main" {
		t.Errorf("base %q, %v", b, err)
	}
}

func TestAdd(t *testing.T) {
	origin, main := newRepos(t)
	git(t, main, "branch", "bug/sc-1/l")
	pushFromOther(t, origin, "bug/sc-2/r")
	git(t, main, "fetch", "-q", "origin")
	r := open(t, main)
	for _, m := range []gitwt.Match{
		{Branch: "bug/sc-1/l", Source: gitwt.Local},
		{Branch: "bug/sc-2/r", Source: gitwt.Remote},
		{Branch: "bug/sc-3/n", Source: gitwt.New},
	} {
		dir := filepath.Join(filepath.Dir(main), string(m.Source))
		if err := r.Add(context.Background(), dir, m, "origin/HEAD"); err != nil {
			t.Fatalf("%s: %v", m.Source, err)
		}
		if got := git(t, dir, "branch", "--show-current"); got != m.Branch {
			t.Errorf("%s: branch %q", m.Source, got)
		}
	}
	if up := git(t, filepath.Join(filepath.Dir(main), "remote"), "rev-parse", "--abbrev-ref", "@{upstream}"); up != "origin/bug/sc-2/r" {
		t.Errorf("upstream %q", up)
	}
	// A new branch must not track its base: git pull and git push would use main.
	if out, err := exec.Command("git", "-C", filepath.Join(filepath.Dir(main), "new"), "rev-parse", "--abbrev-ref", "@{upstream}").CombinedOutput(); err == nil {
		t.Errorf("new branch has upstream %s", out)
	}
}

func TestGitErrorHasStderr(t *testing.T) {
	_, main := newRepos(t)
	file := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	err := open(t, main).Add(context.Background(), filepath.Join(file, "wt"), gitwt.Match{Branch: "bug/sc-1/x", Source: gitwt.New}, "origin/HEAD")
	var e *exit.Error
	if !errors.As(err, &e) || e.Code != exit.Other || !strings.Contains(e.Msg, "git worktree add") {
		t.Fatalf("err %v", err)
	}
}
