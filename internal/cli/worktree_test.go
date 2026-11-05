package cli_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nono/scut/internal/cli"
	"github.com/nono/scut/internal/testapi"
	"github.com/nono/scut/internal/testgit"
)

const wtStory = "/api/v4/acme/stories/1234"

// gitRepos makes a bare origin and a clone of it with one commit, and returns
// the path of the clone.
func gitRepos(t *testing.T) string {
	t.Helper()
	_, main := testgit.Repos(t)
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
	if b := testgit.Git(t, dir, "branch", "--show-current"); b != "bug/sc-1234/x" {
		t.Errorf("branch %q", b)
	}
	if got := c.param(wtStory, "fields"); got != "formatted_vcs_branch_name" {
		t.Errorf("fields %q", got)
	}
}

func TestWorktreeLocalNoToken(t *testing.T) {
	h, main := wtHarness(t, nil, "", nil)
	testgit.Git(t, main, "branch", "bug/sc-1234/x")
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
	testgit.Git(t, main, "branch", "bug/sc-1234/x")
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
	testgit.Git(t, main, "branch", "bug/sc-1234/a")
	testgit.Git(t, main, "branch", "bug/sc-1234/b")
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
	testgit.Git(t, main, "branch", "bug/sc-1234/x")
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
	testgit.Git(t, main, "worktree", "add", "-q", "-b", "bug/sc-1234/x", dir)
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
	return startHarnessH(t, c, storyHandler(func() int { return stateID }, owners), jsonHandler(listBody(states...)), patchStatus, patch)
}

// storyHandler serves story 1234 in workflow 5, with the state that state gives
// at each call, and owners.
func storyHandler(state func() int, owners []string) http.HandlerFunc {
	ows := make([]any, len(owners))
	for i, o := range owners {
		ows[i] = map[string]any{"id": o}
	}
	return func(w http.ResponseWriter, r *http.Request) {
		jsonHandler(jsonOf(map[string]any{"id": 1234, "formatted_vcs_branch_name": "bug/sc-1234/x",
			"workflow": map[string]any{"id": 5}, "workflow_state": map[string]any{"id": state()}, "owners": nestedList(ows...)}))(w, r)
	}
}

// startHarnessH is startHarness with the story and states handlers given.
func startHarnessH(t *testing.T, c *calls, storyH, statesH http.HandlerFunc, patchStatus int, patch *map[string]any) (*harness, string) {
	t.Helper()
	h, main := wtHarness(t, c, `"bug/sc-1234/x"`, wtToken)
	story := map[string]any{"id": 1234}
	h.env.APIBase = testapi.Fake(t, map[string]http.HandlerFunc{
		"GET " + wtStory:                      c.wrap(storyH),
		"GET /api/v4/acme/workflows/5/states": c.wrap(statesH),
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
	wt, _ := decode(t, errs)["worktree"].(map[string]any)
	if wt["branch"] != "bug/sc-1234/x" {
		t.Errorf("stderr %s", errs)
	}
	if _, ok := wt["started"]; ok {
		t.Errorf("worktree has started: %s", errs)
	}
}

// The output format must not depend on whether the run called the API: a
// config file output setting is not read on the path with no API call.
func TestWorktreeOutputSameOnBothPaths(t *testing.T) {
	h, _ := wtHarness(t, &calls{}, `"bug/sc-1234/x"`, wtToken)
	cfg := filepath.Join(h.env.Getenv("XDG_CONFIG_HOME"), "scut")
	if err := os.MkdirAll(cfg, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg, "config.toml"), []byte("output = \"human\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for i := range 2 {
		code, out, errs := h.run("worktree", "1234")
		if code != 0 {
			t.Fatalf("run %d: code %d, stderr %s", i, code, errs)
		}
		decode(t, out)
	}
}

// No fetch when a branch exists: an origin that cannot be reached must not matter.
func TestWorktreeLocalNoFetch(t *testing.T) {
	h, main := wtHarness(t, nil, "", nil)
	testgit.Git(t, main, "branch", "bug/sc-1234/x")
	testgit.Git(t, main, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "gone.git"))
	if code, _, errs := h.run("worktree", "1234"); code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
}

func TestWorktreeFoundAfterFetch(t *testing.T) {
	h, main := wtHarness(t, nil, "", nil)
	testgit.Push(t, testgit.Git(t, main, "remote", "get-url", "origin"), "bug/sc-1234/x")
	code, out, errs := h.run("worktree", "1234")
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	if m := decode(t, out); m["from"] != "remote" || m["branch"] != "bug/sc-1234/x" {
		t.Errorf("out %s", out)
	}
}

func TestWorktreeStartBacklog(t *testing.T) {
	var c calls
	var patch map[string]any
	states := append(wfStates(), map[string]any{"id": 6, "type": "backlog", "position": 0})
	h, _ := startHarness(t, &c, 6, []string{"u1"}, states, 200, &patch)
	code, out, errs := h.run("worktree", "1234", "--start")
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	if got := jsonOf(patch); got != `{"workflow_state_id":9}` {
		t.Errorf("patch %s", got)
	}
	if m := decode(t, out); m["started"] != true {
		t.Errorf("out %s", out)
	}
}

// The started state can be on a later page of the states.
func TestWorktreeStartStatesPaged(t *testing.T) {
	var c calls
	var patch map[string]any
	pages := func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("cursor") == "" {
			jsonHandler(jsonOf(map[string]any{"entities": []any{map[string]any{"id": 7, "type": "unstarted", "position": 0}},
				"next_page_url": "https://api.app.shortcut.com/api/v4/acme/workflows/5/states?cursor=p2"}))(w, r)
			return
		}
		jsonHandler(listBody(map[string]any{"id": 9, "type": "started", "position": 1}))(w, r)
	}
	h, _ := startHarnessH(t, &c, storyHandler(func() int { return 7 }, []string{"u1"}), pages, 200, &patch)
	code, _, errs := h.run("worktree", "1234", "--start")
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	if got := jsonOf(patch); got != `{"workflow_state_id":9}` {
		t.Errorf("patch %s", got)
	}
}

// A story that another writer moved to done after the first read is not moved back.
func TestWorktreeStartRecheck(t *testing.T) {
	var c calls
	var patch map[string]any
	n := 0
	state := func() int {
		n++
		if n <= 2 { // the branch name read and the first --start read
			return 7
		}
		return 10
	}
	h, _ := startHarnessH(t, &c, storyHandler(state, nil), jsonHandler(listBody(wfStates()...)), 200, &patch)
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

func TestWorktreeHuman(t *testing.T) {
	for name, run := range map[string]func(h *harness) (int, string, string){
		"flag": func(h *harness) (int, string, string) { return h.run("worktree", "1234", "--human") },
		"env":  func(h *harness) (int, string, string) { return h.run("worktree", "1234") },
	} {
		t.Run(name, func(t *testing.T) {
			h, main := wtHarness(t, nil, "", map[string]string{"SCUT_OUTPUT": map[string]string{"flag": "", "env": "human"}[name]})
			testgit.Git(t, main, "branch", "bug/sc-1234/x")
			code, out, errs := run(h)
			if code != 0 {
				t.Fatalf("code %d, stderr %s", code, errs)
			}
			if want := filepath.Join(filepath.Dir(main), "1234") + "\nbug/sc-1234/x\n"; out != want {
				t.Errorf("out %q, want %q", out, want)
			}
		})
	}
}

// An existing worktree is returned at its own directory, whatever --dir says.
func TestWorktreeExistingElsewhere(t *testing.T) {
	h, main := wtHarness(t, nil, "", nil)
	dir := filepath.Join(filepath.Dir(main), "elsewhere")
	testgit.Git(t, main, "worktree", "add", "-q", "-b", "bug/sc-1234/x", dir)
	code, out, errs := h.run("worktree", "1234", "--dir", "../other")
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	if m := decode(t, out); m["dir"] != dir || m["from"] != "worktree" || m["created"] != false {
		t.Errorf("out %s", out)
	}
}

func TestWorktreeCancelledGit(t *testing.T) {
	h, _ := wtHarness(t, nil, "", nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var errb strings.Builder
	if code := cli.RunContext(ctx, cli.NewRoot(h.env), []string{"worktree", "1234"}, &errb); code != 130 {
		t.Fatalf("code %d, stderr %s", code, errb.String())
	}
}

func TestWorktreeCancelledStart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var c calls
	var patch map[string]any
	story := storyHandler(func() int { return 7 }, nil)
	h, _ := startHarnessH(t, &c, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Query().Get("fields"), "workflow_state") {
			cancel()
			<-r.Context().Done()
			return
		}
		story(w, r)
	}, jsonHandler(listBody(wfStates()...)), 200, &patch)
	var errb strings.Builder
	if code := cli.RunContext(ctx, cli.NewRoot(h.env), []string{"worktree", "1234", "--start"}, &errb); code != 130 {
		t.Fatalf("code %d, stderr %s", code, errb.String())
	}
}

// A branch that another run makes during the story read is used: no second branch.
func TestWorktreeBranchMadeDuringLookup(t *testing.T) {
	var main string
	h, m := wtHarness(t, nil, "", wtToken)
	main = m
	h.env.APIBase = testapi.Fake(t, map[string]http.HandlerFunc{
		"GET " + wtStory: func(w http.ResponseWriter, r *http.Request) {
			testgit.Git(t, main, "branch", "bug/sc-1234/other")
			jsonHandler(`{"id":1234,"formatted_vcs_branch_name":"bug/sc-1234/suggested"}`)(w, r)
		},
	})
	code, out, errs := h.run("worktree", "1234")
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	if m := decode(t, out); m["branch"] != "bug/sc-1234/other" || m["from"] != "local" {
		t.Errorf("out %s", out)
	}
	if got := testgit.Git(t, main, "branch", "--list", "bug/sc-1234/*"); strings.Contains(got, "suggested") {
		t.Errorf("second branch: %s", got)
	}
}

// scut makes the directory before git runs, so a directory that appears
// between the check and git worktree add cannot be one that scut did not make.
func TestWorktreeMakesDirBeforeGit(t *testing.T) {
	real, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	h, main := wtHarness(t, nil, "", nil)
	testgit.Git(t, main, "branch", "bug/sc-1234/x")
	dir := filepath.Join(filepath.Dir(main), "1234")
	bin, log := t.TempDir(), filepath.Join(t.TempDir(), "log")
	script := "#!/bin/sh\ncase \"$*\" in *\"worktree add\"*) if [ -d " + dir + " ]; then echo made >> " + log + "; fi;; esac\nexec " + real + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	if code, _, errs := h.run("worktree", "1234"); code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	if b, _ := os.ReadFile(log); string(b) != "made\n" {
		t.Errorf("dir did not exist when git ran: log %q", b)
	}
}

// When git fails after scut made the directory, scut removes it.
func TestWorktreeAddFailRemovesDir(t *testing.T) {
	h, main := wtHarness(t, nil, "", nil)
	code, _, errs := h.run("worktree", "1234", "--branch", "bug/sc-1234/a..b")
	if code != 1 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	if _, err := os.Lstat(filepath.Join(filepath.Dir(main), "1234")); err == nil {
		t.Errorf("dir left after the failure")
	}
}
