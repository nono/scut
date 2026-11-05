package cli_test

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nono/scut/internal/testapi"
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

// startHarness serves story 1234 in workflow 5 with state stateID and owners,
// the states of workflow 5, whoami (member u1) and a PATCH that records its
// body. patchStatus is the status of the PATCH.
func startHarness(t *testing.T, c *calls, stateID int, owners []string, states []any, patchStatus int, patch *map[string]any) (*harness, string) {
	t.Helper()
	h, main := wtHarness(t, c, `"bug/sc-1234/x"`, wtToken)
	ows := make([]any, len(owners))
	for i, o := range owners {
		ows[i] = map[string]any{"id": o}
	}
	story := map[string]any{"id": 1234, "formatted_vcs_branch_name": "bug/sc-1234/x",
		"workflow": map[string]any{"id": 5}, "workflow_state": map[string]any{"id": stateID}, "owners": nestedList(ows...)}
	h.env.APIBase = testapi.Fake(t, map[string]http.HandlerFunc{
		"GET " + wtStory:                      c.wrap(jsonHandler(jsonOf(story))),
		"GET /api/v4/acme/workflows/5/states": c.wrap(jsonHandler(listBody(states...))),
		"GET /api/v4/whoami":                  c.wrap(jsonHandler(whoamiBody)),
		"PATCH " + wtStory: c.wrap(func(w http.ResponseWriter, r *http.Request) {
			b, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(b, patch)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(patchStatus)
			_, _ = io.WriteString(w, jsonOf(map[string]any{"entity": story}))
		}),
	})
	h.env.Sleep = func(time.Duration) {}
	return h, main
}

func wfStates() []any {
	return []any{
		map[string]any{"id": 7, "type": "unstarted", "position": 0},
		map[string]any{"id": 8, "type": "started", "position": 2},
		map[string]any{"id": 9, "type": "started", "position": 1},
		map[string]any{"id": 10, "type": "done", "position": 3},
	}
}

func TestWorktreeStartUnstarted(t *testing.T) {
	var c calls
	var patch map[string]any
	h, _ := startHarness(t, &c, 7, []string{}, wfStates(), 200, &patch)
	code, out, errs := h.run("worktree", "1234", "--start")
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	if got := jsonOf(patch); got != `{"owner_ids_add":["u1"],"workflow_state_id":9}` {
		t.Errorf("patch %s", got)
	}
	if m := decode(t, out); m["started"] != true || m["from"] != "new" {
		t.Errorf("out %s", out)
	}
}

func TestWorktreeStartAlreadyStarted(t *testing.T) {
	var c calls
	var patch map[string]any
	h, _ := startHarness(t, &c, 8, []string{"u1"}, wfStates(), 200, &patch)
	code, out, errs := h.run("worktree", "1234", "--start")
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	if c.count(wtStory) != 2 || patch != nil {
		t.Errorf("calls %v, patch %v", c.paths, patch)
	}
	if m := decode(t, out); m["started"] != false {
		t.Errorf("out %s", out)
	}
}

func TestWorktreeStartDoneOwnerMissing(t *testing.T) {
	var c calls
	var patch map[string]any
	h, _ := startHarness(t, &c, 10, []string{"u2"}, wfStates(), 200, &patch)
	code, out, errs := h.run("worktree", "1234", "--start")
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	if got := jsonOf(patch); got != `{"owner_ids_add":["u1"]}` {
		t.Errorf("patch %s", got)
	}
	if m := decode(t, out); m["started"] != false {
		t.Errorf("out %s", out)
	}
}

func TestWorktreeStartNoStartedState(t *testing.T) {
	var c calls
	var patch map[string]any
	h, main := startHarness(t, &c, 7, []string{}, []any{map[string]any{"id": 7, "type": "unstarted", "position": 0}}, 200, &patch)
	code, _, errs := h.run("worktree", "1234", "--start")
	if code != 3 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	dir := filepath.Join(filepath.Dir(main), "1234")
	wt, _ := decode(t, errs)["worktree"].(map[string]any)
	if wt["dir"] != dir {
		t.Errorf("stderr %s", errs)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("worktree gone: %v", err)
	}
}

func TestWorktreeStartAPIDown(t *testing.T) {
	var c calls
	var patch map[string]any
	h, _ := startHarness(t, &c, 7, []string{}, wfStates(), 503, &patch)
	code, _, errs := h.run("worktree", "1234", "--start")
	if code != 5 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	if wt, _ := decode(t, errs)["worktree"].(map[string]any); wt["branch"] != "bug/sc-1234/x" {
		t.Errorf("stderr %s", errs)
	}
}
