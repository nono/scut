package cli_test

import (
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const wtStory = "/api/v4/acme/stories/1234"

func gitT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// gitRepos makes a bare origin and a clone at <tmp>/main with one commit. It
// returns the path of the clone.
func gitRepos(t *testing.T) string {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	tmp, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	origin, main := filepath.Join(tmp, "origin.git"), filepath.Join(tmp, "main")
	gitT(t, tmp, "init", "-q", "--bare", "-b", "main", origin)
	gitT(t, tmp, "clone", "-q", origin, main)
	gitT(t, main, "commit", "-q", "--allow-empty", "-m", "init")
	gitT(t, main, "push", "-q", "origin", "main")
	gitT(t, main, "remote", "set-head", "origin", "main")
	return main
}

// wtHarness is a harness with a git repo as the current directory. branch is
// the formatted_vcs_branch_name of story 1234 ("" for no story route).
func wtHarness(t *testing.T, c *calls, branch string, vars map[string]string) (*harness, string) {
	t.Helper()
	main := gitRepos(t)
	var routes map[string]http.HandlerFunc
	if branch != "" {
		routes = map[string]http.HandlerFunc{
			"GET " + wtStory: c.wrap(jsonHandler(`{"id":1234,"formatted_vcs_branch_name":` + branch + `}`)),
		}
	}
	h := newHarness(t, routes, "", vars)
	h.env.Dir = main
	return h, main
}

var wtToken = map[string]string{"SCUT_TOKEN": "sct_rw_acme_s3cret"}

func TestWorktreeNew(t *testing.T) {
	var c calls
	h, main := wtHarness(t, &c, `"bug/sc-1234/x-"`, wtToken)
	code, out, errs := h.run("worktree", "1234")
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	dir := filepath.Join(filepath.Dir(main), "1234")
	want := `{"branch":"bug/sc-1234/x","created":true,"dir":"` + dir + `","from":"new","story":1234}`
	if got := jsonOf(decode(t, out)); got != want {
		t.Errorf("out %s", got)
	}
	if b := gitT(t, dir, "branch", "--show-current"); b != "bug/sc-1234/x" {
		t.Errorf("branch %q", b)
	}
	if got := c.param(wtStory, "fields"); got != "formatted_vcs_branch_name" {
		t.Errorf("fields %q", got)
	}
}

func TestWorktreeLocalNoToken(t *testing.T) {
	h, main := wtHarness(t, nil, "", nil)
	gitT(t, main, "branch", "bug/sc-1234/x")
	code, out, errs := h.run("worktree", "sc-1234")
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	if m := decode(t, out); m["from"] != "local" || m["created"] != true {
		t.Errorf("out %s", out)
	}
}

func TestWorktreeExisting(t *testing.T) {
	var c calls
	h, main := wtHarness(t, &c, `"bug/sc-1234/x"`, wtToken)
	if code, _, errs := h.run("worktree", "1234"); code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	n := c.total()
	code, out, errs := h.run("worktree", "1234")
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	m := decode(t, out)
	if m["from"] != "worktree" || m["created"] != false || m["dir"] != filepath.Join(filepath.Dir(main), "1234") {
		t.Errorf("out %s", out)
	}
	if c.total() != n {
		t.Errorf("second run made requests: %v", c.paths)
	}
}

func TestWorktreeRelativeDir(t *testing.T) {
	h, main := wtHarness(t, nil, "", nil)
	gitT(t, main, "branch", "bug/sc-1234/x")
	code, out, errs := h.run("worktree", "1234", "--dir", "../x")
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	if m := decode(t, out); m["dir"] != filepath.Join(filepath.Dir(main), "x") {
		t.Errorf("out %s", out)
	}
}

func TestWorktreeTwoMatches(t *testing.T) {
	h, main := wtHarness(t, nil, "", nil)
	gitT(t, main, "branch", "bug/sc-1234/a")
	gitT(t, main, "branch", "bug/sc-1234/b")
	code, _, errs := h.run("worktree", "1234")
	if code != 2 || !strings.Contains(errs, `"valid":["bug/sc-1234/a","bug/sc-1234/b"]`) {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	if code, _, errs := h.run("worktree", "1234", "--branch", "nope"); code != 2 {
		t.Fatalf("nope: code %d, stderr %s", code, errs)
	}
	code, out, errs := h.run("worktree", "1234", "--branch", "bug/sc-1234/b")
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	if m := decode(t, out); m["branch"] != "bug/sc-1234/b" {
		t.Errorf("out %s", out)
	}
}

func TestWorktreeBranchFlagNew(t *testing.T) {
	h, _ := wtHarness(t, nil, "", nil)
	if code, _, errs := h.run("worktree", "1234", "--branch", "feature/y"); code != 2 {
		t.Fatalf("no id: code %d, stderr %s", code, errs)
	}
	code, out, errs := h.run("worktree", "1234", "--branch", "feature/sc-1234/y")
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	if m := decode(t, out); m["from"] != "new" || m["branch"] != "feature/sc-1234/y" {
		t.Errorf("out %s", out)
	}
}

func TestWorktreeNullBranchName(t *testing.T) {
	h, _ := wtHarness(t, &calls{}, `null`, wtToken)
	if code, _, errs := h.run("worktree", "1234"); code != 3 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	h, _ = wtHarness(t, &calls{}, `"feature/x"`, wtToken)
	code, _, errs := h.run("worktree", "1234")
	if code != 3 || !strings.Contains(errs, "--branch") {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
}

func TestWorktreeDirInUse(t *testing.T) {
	h, main := wtHarness(t, nil, "", nil)
	gitT(t, main, "branch", "bug/sc-1234/x")
	dir := filepath.Join(filepath.Dir(main), "1234")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if code, _, errs := h.run("worktree", "1234"); code != 2 || !strings.Contains(errs, "in use") {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	if es, _ := os.ReadDir(dir); len(es) != 0 {
		t.Errorf("dir changed: %v", es)
	}
}

func TestWorktreePrunable(t *testing.T) {
	h, main := wtHarness(t, nil, "", nil)
	dir := filepath.Join(filepath.Dir(main), "1234")
	gitT(t, main, "worktree", "add", "-q", "-b", "bug/sc-1234/x", dir)
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	code, _, errs := h.run("worktree", "1234")
	if code != 2 || !strings.Contains(errs, "worktree prune") {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
}

func TestWorktreeOutsideRepo(t *testing.T) {
	t.Setenv("GIT_CEILING_DIRECTORIES", os.TempDir())
	h := newHarness(t, nil, "", nil)
	if code, _, errs := h.run("worktree", "1234"); code != 2 || !strings.Contains(errs, "not in a git repository") {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
}
