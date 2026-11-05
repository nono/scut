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
	"github.com/nono/scut/internal/testgit"
)

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

func TestOpenFromLinkedWorktreeSubdir(t *testing.T) {
	_, main := testgit.Repos(t)
	other := filepath.Join(filepath.Dir(main), "other")
	testgit.Git(t, main, "worktree", "add", "-q", "--detach", other)
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
	_, main := testgit.Repos(t)
	testgit.Git(t, main, "switch", "-q", "-c", "bug/sc-1234/x")
	want := []gitwt.Match{{Branch: "bug/sc-1234/x", Dir: main, Source: gitwt.Worktree}}
	if got := find(t, open(t, main), 1234); !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v", got)
	}
}

func TestFindSkipsDetached(t *testing.T) {
	_, main := testgit.Repos(t)
	testgit.Git(t, main, "worktree", "add", "-q", "--detach", filepath.Join(filepath.Dir(main), "d"))
	if got := find(t, open(t, main), 1234); got != nil {
		t.Errorf("got %+v", got)
	}
}

func TestFindPrunable(t *testing.T) {
	_, main := testgit.Repos(t)
	dir := filepath.Join(filepath.Dir(main), "1234")
	testgit.Git(t, main, "worktree", "add", "-q", "-b", "bug/sc-1234/x", dir)
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	want := []gitwt.Match{{Branch: "bug/sc-1234/x", Dir: dir, Source: gitwt.Worktree, Prunable: true}}
	if got := find(t, open(t, main), 1234); !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v", got)
	}
}

func TestFindLocal(t *testing.T) {
	_, main := testgit.Repos(t)
	testgit.Git(t, main, "branch", "bug/sc-1234/x")
	want := []gitwt.Match{{Branch: "bug/sc-1234/x", Source: gitwt.Local}}
	if got := find(t, open(t, main), 1234); !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v", got)
	}
}

func TestFindRemote(t *testing.T) {
	origin, main := testgit.Repos(t)
	testgit.Push(t, origin, "bug/sc-1234/x")
	testgit.Git(t, main, "fetch", "-q", "origin")
	want := []gitwt.Match{{Branch: "bug/sc-1234/x", Source: gitwt.Remote}}
	if got := find(t, open(t, main), 1234); !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v", got)
	}
}

func TestFindLocalAndRemoteSameName(t *testing.T) {
	_, main := testgit.Repos(t)
	testgit.Git(t, main, "branch", "bug/sc-1234/x")
	testgit.Git(t, main, "push", "-q", "origin", "bug/sc-1234/x")
	want := []gitwt.Match{{Branch: "bug/sc-1234/x", Source: gitwt.Local}}
	if got := find(t, open(t, main), 1234); !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v", got)
	}
}

func TestFindTwoLocal(t *testing.T) {
	_, main := testgit.Repos(t)
	testgit.Git(t, main, "branch", "bug/sc-1234/b")
	testgit.Git(t, main, "branch", "bug/sc-1234/a")
	want := []gitwt.Match{{Branch: "bug/sc-1234/a", Source: gitwt.Local}, {Branch: "bug/sc-1234/b", Source: gitwt.Local}}
	if got := find(t, open(t, main), 1234); !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v", got)
	}
}

func TestFindSourceOrder(t *testing.T) {
	origin, main := testgit.Repos(t)
	testgit.Push(t, origin, "bug/sc-1234/b")
	testgit.Git(t, main, "fetch", "-q", "origin")
	testgit.Git(t, main, "branch", "bug/sc-1234/a")
	want := []gitwt.Match{{Branch: "bug/sc-1234/a", Source: gitwt.Local}}
	if got := find(t, open(t, main), 1234); !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v", got)
	}
}

func TestFetchFindsNewRemote(t *testing.T) {
	origin, main := testgit.Repos(t)
	testgit.Push(t, origin, "bug/sc-1234/x")
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

// A new branch starts at origin/HEAD, else at origin/main.
func TestAddNewBase(t *testing.T) {
	origin, main := testgit.Repos(t)
	other := filepath.Join(t.TempDir(), "other")
	testgit.Git(t, filepath.Dir(other), "clone", "-q", origin, other)
	testgit.Git(t, other, "switch", "-q", "-c", "dev")
	testgit.Git(t, other, "commit", "-q", "--allow-empty", "-m", "dev")
	testgit.Git(t, other, "push", "-q", "origin", "dev")
	testgit.Git(t, main, "fetch", "-q", "origin")
	testgit.Git(t, main, "remote", "set-head", "origin", "dev")
	r := open(t, main)
	add := func(name string) string {
		dir := filepath.Join(filepath.Dir(main), name)
		if err := r.Add(context.Background(), dir, gitwt.Match{Branch: "bug/sc-1/" + name, Source: gitwt.New}); err != nil {
			t.Fatal(err)
		}
		return testgit.Git(t, dir, "rev-parse", "HEAD")
	}
	if got, want := add("head"), testgit.Git(t, main, "rev-parse", "origin/dev"); got != want {
		t.Errorf("with origin/HEAD: %s, want origin/dev %s", got, want)
	}
	testgit.Git(t, main, "remote", "set-head", "origin", "-d")
	if got, want := add("fallback"), testgit.Git(t, main, "rev-parse", "origin/main"); got != want {
		t.Errorf("without origin/HEAD: %s, want origin/main %s", got, want)
	}
}

func TestAdd(t *testing.T) {
	origin, main := testgit.Repos(t)
	testgit.Git(t, main, "branch", "bug/sc-1/l")
	testgit.Push(t, origin, "bug/sc-2/r")
	testgit.Git(t, main, "fetch", "-q", "origin")
	r := open(t, main)
	for _, m := range []gitwt.Match{
		{Branch: "bug/sc-1/l", Source: gitwt.Local},
		{Branch: "bug/sc-2/r", Source: gitwt.Remote},
		{Branch: "bug/sc-3/n", Source: gitwt.New},
	} {
		dir := filepath.Join(filepath.Dir(main), string(m.Source))
		if err := r.Add(context.Background(), dir, m); err != nil {
			t.Fatalf("%s: %v", m.Source, err)
		}
		if got := testgit.Git(t, dir, "branch", "--show-current"); got != m.Branch {
			t.Errorf("%s: branch %q", m.Source, got)
		}
	}
	if up := testgit.Git(t, filepath.Join(filepath.Dir(main), "remote"), "rev-parse", "--abbrev-ref", "@{upstream}"); up != "origin/bug/sc-2/r" {
		t.Errorf("upstream %q", up)
	}
	// A new branch must not track its base: git pull would merge main, and
	// git push would fail or push to main, by push.default.
	if out, err := exec.Command("git", "-C", filepath.Join(filepath.Dir(main), "new"), "rev-parse", "--abbrev-ref", "@{upstream}").CombinedOutput(); err == nil {
		t.Errorf("new branch has upstream %s", out)
	}
}

func TestGitErrorHasStderr(t *testing.T) {
	_, main := testgit.Repos(t)
	file := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	err := open(t, main).Add(context.Background(), filepath.Join(file, "wt"), gitwt.Match{Branch: "bug/sc-1/x", Source: gitwt.New})
	var e *exit.Error
	if !errors.As(err, &e) || e.Code != exit.Other || !strings.Contains(e.Msg, "git worktree add") || !strings.Contains(e.Msg, "Not a directory") {
		t.Fatalf("err %v", err)
	}
}

// A git that cannot run is a git failure (exit 1), not "not in a git repository".
func TestOpenGitMissing(t *testing.T) {
	_, main := testgit.Repos(t)
	t.Setenv("PATH", t.TempDir())
	_, err := gitwt.Open(context.Background(), main)
	var e *exit.Error
	if !errors.As(err, &e) || e.Code != exit.Other {
		t.Fatalf("err %v", err)
	}
}

// A cancelled git run is a failure, not "not in a git repository".
func TestOpenCancelled(t *testing.T) {
	_, main := testgit.Repos(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := gitwt.Open(ctx, main)
	var e *exit.Error
	if !errors.As(err, &e) || e.Code != exit.Other {
		t.Fatalf("err %v", err)
	}
}

func TestFindUnicodeSpace(t *testing.T) {
	_, main := testgit.Repos(t)
	testgit.Git(t, main, "branch", "bug/sc-1234/a\u00a0b")
	want := []gitwt.Match{{Branch: "bug/sc-1234/a\u00a0b", Source: gitwt.Local}}
	if got := find(t, open(t, main), 1234); !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v", got)
	}
}

func TestFindNewlinePath(t *testing.T) {
	_, main := testgit.Repos(t)
	dir := filepath.Join(filepath.Dir(main), "a\nb")
	testgit.Git(t, main, "worktree", "add", "-q", "-b", "bug/sc-1234/x", dir)
	want := []gitwt.Match{{Branch: "bug/sc-1234/x", Dir: dir, Source: gitwt.Worktree}}
	if got := find(t, open(t, main), 1234); !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v", got)
	}
}

// commit makes a commit that changes file f to s in dir.
func commit(t *testing.T, dir, s string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "f"), []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
	testgit.Git(t, dir, "add", "f")
	testgit.Git(t, dir, "commit", "-q", "-m", s)
}

// A worktree in a rebase is detached; its branch is in rebase-merge/head-name.
func TestFindRebase(t *testing.T) {
	_, main := testgit.Repos(t)
	dir := filepath.Join(filepath.Dir(main), "1234")
	testgit.Git(t, main, "worktree", "add", "-q", "-b", "bug/sc-1234/x", dir)
	commit(t, dir, "a")
	testgit.Git(t, main, "branch", "other", "main")
	testgit.Git(t, main, "worktree", "add", "-q", filepath.Join(filepath.Dir(main), "o"), "other")
	commit(t, filepath.Join(filepath.Dir(main), "o"), "b")
	cmd := exec.Command("git", "-C", dir, "-c", "user.name=t", "-c", "user.email=t@example.com", "rebase", "other")
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("rebase did not stop: %s", out)
	}
	want := []gitwt.Match{{Branch: "bug/sc-1234/x", Dir: dir, Source: gitwt.Worktree}}
	if got := find(t, open(t, main), 1234); !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v", got)
	}
}

// A worktree in a bisect is detached; its branch is in BISECT_START.
func TestFindBisect(t *testing.T) {
	_, main := testgit.Repos(t)
	dir := filepath.Join(filepath.Dir(main), "1234")
	testgit.Git(t, main, "worktree", "add", "-q", "-b", "bug/sc-1234/x", dir)
	commit(t, dir, "a")
	commit(t, dir, "b")
	commit(t, dir, "c")
	testgit.Git(t, dir, "bisect", "start", "HEAD", "HEAD~3")
	want := []gitwt.Match{{Branch: "bug/sc-1234/x", Dir: dir, Source: gitwt.Worktree}}
	if got := find(t, open(t, main), 1234); !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v", got)
	}
}

// git runs in the C locale, so scut can read its messages in any locale.
func TestRunCLocale(t *testing.T) {
	real, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	_, main := testgit.Repos(t)
	bin, log := t.TempDir(), filepath.Join(t.TempDir(), "log")
	script := "#!/bin/sh\necho \"$LC_ALL\" >> " + log + "\nexec " + real + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("LC_ALL", "fr_FR.UTF-8")
	open(t, main)
	b, _ := os.ReadFile(log)
	for _, l := range strings.Fields(string(b)) {
		if l != "C" {
			t.Errorf("git ran with LC_ALL=%q", l)
		}
	}
	if len(b) == 0 {
		t.Errorf("git did not run")
	}
}
