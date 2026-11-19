package cli_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nono/scut/internal/cli"
	"github.com/nono/scut/internal/testapi"
	"github.com/nono/scut/internal/testgit"
)

const wtStory = "/api/v4/acme/stories/1234"

// wtHarness is a harness with a git repo as the current directory. branch is
// the formatted_vcs_branch_name of story 1234 ("" for no story route).
func wtHarness(t *testing.T, c *calls, branch string, vars map[string]string) (*harness, string) {
	t.Helper()
	_, main := testgit.Repos(t)
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
	for _, args := range [][]string{{"worktree", "1234"}, {"worktree", "1234", "--start"}, {"worktree", "1234", "--branch", "nope"}} {
		code, _, errs := h.run(args...)
		e := decode(t, errs)
		if code != 2 || jsonOf(e["valid"]) != `["bug/sc-1234/a","bug/sc-1234/b"]` || !strings.Contains(e["suggestion"].(string), "--branch <name>") {
			t.Fatalf("%v: code %d, stderr %s", args, code, errs)
		}
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

// A null branch name and a name with no sc-<id> are exit 3, with no
// worktree. With --start, the name comes from the read during the fetch.
func TestWorktreeNullBranchName(t *testing.T) {
	for _, tc := range []struct{ name, branch, msg string }{
		{"null", `null`, "no branch name format"},
		{"no id", `"feature/x"`, "--branch <name>, with sc-1234"},
	} {
		for _, args := range [][]string{{"worktree", "1234"}, {"worktree", "1234", "--start"}} {
			t.Run(tc.name+" "+strings.Join(args[2:], ""), func(t *testing.T) {
				var patch map[string]any
				story := jsonHandler(`{"id":1234,"formatted_vcs_branch_name":` + tc.branch + `,"workflow":{"id":5}}`)
				h, main := startHarnessH(t, &calls{}, story, jsonHandler(listBody(wfStates()...)), 200, &patch)
				code, _, errs := h.run(args...)
				if code != 3 || !strings.Contains(errs, tc.msg) {
					t.Fatalf("code %d, stderr %s", code, errs)
				}
				if _, has := decode(t, errs)["worktree"]; has {
					t.Errorf("stderr %s", errs)
				}
				if _, err := os.Stat(filepath.Join(filepath.Dir(main), "1234")); err == nil {
					t.Errorf("worktree made")
				}
			})
		}
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

// A worktree with no directory is exit 2, and --start changes nothing.
func TestWorktreePrunable(t *testing.T) {
	for _, args := range [][]string{{"worktree", "1234"}, {"worktree", "1234", "--start"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var patch map[string]any
			h, main := startHarness(t, &calls{}, 7, nil, wfStates(), 200, &patch)
			dir := filepath.Join(filepath.Dir(main), "1234")
			testgit.Git(t, main, "worktree", "add", "-q", "-b", "bug/sc-1234/x", dir)
			if err := os.RemoveAll(dir); err != nil {
				t.Fatal(err)
			}
			code, _, errs := h.run(args...)
			if code != 2 || !strings.Contains(errs, "worktree prune") {
				t.Fatalf("code %d, stderr %s", code, errs)
			}
			if patch != nil {
				t.Errorf("patch %v", patch)
			}
		})
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
	h, main := wtHarness(t, c, "", wtToken)
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
	// One read during the fetch, one after the git part. The states and @me
	// are read during the fetch, so no re-read comes before the PATCH.
	if n := c.count(wtStory); n != 3 { // with the PATCH
		t.Errorf("calls %v", c.paths)
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

// breakOrigin makes the origin of main a path with no repository, so a fetch fails.
func breakOrigin(t *testing.T, main string) {
	t.Helper()
	testgit.Git(t, main, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "gone.git"))
}

// No fetch when a branch exists: an origin that cannot be reached must not matter.
func TestWorktreeLocalNoFetch(t *testing.T) {
	h, main := wtHarness(t, nil, "", nil)
	testgit.Git(t, main, "branch", "bug/sc-1234/x")
	breakOrigin(t, main)
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

// A story that another writer moved to done after the first read is not moved
// back. With a local branch there is no fetch, so the states and @me are read
// after the story, and the state is read again before the PATCH.
func TestWorktreeStartRecheck(t *testing.T) {
	var c calls
	var patch map[string]any
	n := 0
	state := func() int {
		n++
		if n <= 1 {
			return 7
		}
		return 10
	}
	h, main := startHarnessH(t, &c, storyHandler(state, nil), jsonHandler(listBody(wfStates()...)), 200, &patch)
	testgit.Git(t, main, "branch", "bug/sc-1234/x")
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
	for _, tc := range []struct {
		name, env string
		args      []string
	}{{"flag", "", []string{"--human"}}, {"env", "human", nil}} {
		t.Run(tc.name, func(t *testing.T) {
			h, main := wtHarness(t, nil, "", map[string]string{"SCUT_OUTPUT": tc.env})
			testgit.Git(t, main, "branch", "bug/sc-1234/x")
			code, out, errs := h.run(append([]string{"worktree", "1234"}, tc.args...)...)
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
		if r.URL.Query().Get("fields") == "workflow,workflow_state,owners" { // the read after the git part
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
	// The worktree was made: the error JSON names it, so a caller can open it.
	if wt, _ := decode(t, errb.String())["worktree"].(map[string]any); wt["branch"] != "bug/sc-1234/x" {
		t.Errorf("stderr %s", errb.String())
	}
}

// A branch that another run makes during the story read is used: no second branch.
func TestWorktreeBranchMadeDuringLookup(t *testing.T) {
	h, main := wtHarness(t, nil, "", wtToken)
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
	h, main := wtHarness(t, nil, "", nil)
	testgit.Git(t, main, "branch", "bug/sc-1234/x")
	dir := filepath.Join(filepath.Dir(main), "1234")
	log := testgit.WrapGit(t, `case "$*" in *"worktree add"*) if [ -d '`+dir+`' ]; then echo made >> "$LOG"; fi;; esac`)
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

// With --start, the story read runs while git fetches: the fetch waits for
// the read, and the read waits for the fetch. Steps in sequence time out.
func TestWorktreeStartReadsDuringFetch(t *testing.T) {
	log := testgit.WrapGit(t, `case "$*" in *" fetch "*)
	: > "$LOG.fetch"
	i=0; while [ ! -e "$LOG.read" ] && [ $i -lt 100 ]; do sleep 0.05; i=$((i+1)); done
	[ -e "$LOG.read" ] && echo overlap >> "$LOG";; esac`)
	var c calls
	var patch map[string]any
	story := storyHandler(func() int { return 7 }, nil)
	h, _ := startHarnessH(t, &c, func(w http.ResponseWriter, r *http.Request) {
		for i := 0; i < 100; i++ {
			if _, err := os.Stat(log + ".fetch"); err == nil {
				_ = os.WriteFile(log+".read", nil, 0o644)
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		story(w, r)
	}, jsonHandler(listBody(wfStates()...)), 200, &patch)
	if code, _, errs := h.run("worktree", "1234", "--start"); code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	if b, _ := os.ReadFile(log); string(b) != "overlap\n" {
		t.Errorf("the story read and the fetch did not run at the same time: log %q", b)
	}
}

// The decision uses the state after the git part, as with the steps in
// sequence: a story that another writer moves to unstarted during the fetch
// is started.
func TestWorktreeStartStateChangedDuringFetch(t *testing.T) {
	var c calls
	var patch map[string]any
	n := 0
	state := func() int {
		n++
		if n <= 1 { // the read during the fetch
			return 10
		}
		return 7
	}
	h, _ := startHarnessH(t, &c, storyHandler(state, []string{"u1"}), jsonHandler(listBody(wfStates()...)), 200, &patch)
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

// startRoutes serves story 1234 with story, the states of workflows 5 and 6,
// whoami with me, and a PATCH that records its body.
func startRoutes(t *testing.T, story, me http.HandlerFunc, patch *map[string]any) *harness {
	t.Helper()
	h, _ := wtHarness(t, nil, "", wtToken)
	states6 := listBody(map[string]any{"id": 17, "type": "unstarted", "position": 0},
		map[string]any{"id": 18, "type": "started", "position": 1})
	h.env.APIBase = testapi.Fake(t, map[string]http.HandlerFunc{
		"GET " + wtStory:                      story,
		"GET /api/v4/acme/workflows/5/states": jsonHandler(listBody(wfStates()...)),
		"GET /api/v4/acme/workflows/6/states": jsonHandler(states6),
		"GET /api/v4/whoami":                  me,
		"PATCH " + wtStory: func(w http.ResponseWriter, r *http.Request) {
			b, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(b, patch)
			jsonHandler(`{"id":1234}`)(w, r)
		},
	})
	h.env.Sleep = func(time.Duration) {}
	return h
}

// A story that moves to another workflow during the fetch is judged by the
// states of its new workflow.
func TestWorktreeStartWorkflowChangedDuringFetch(t *testing.T) {
	var patch map[string]any
	n := 0
	story := func(w http.ResponseWriter, r *http.Request) {
		n++
		wf, state := 5, 7
		if n > 1 { // after the read during the fetch
			wf, state = 6, 17
		}
		jsonHandler(jsonOf(map[string]any{"id": 1234, "formatted_vcs_branch_name": "bug/sc-1234/x",
			"workflow": map[string]any{"id": wf}, "workflow_state": map[string]any{"id": state},
			"owners": nestedList(map[string]any{"id": "u1"})}))(w, r)
	}
	h := startRoutes(t, story, jsonHandler(whoamiBody), &patch)
	code, _, errs := h.run("worktree", "1234", "--start")
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	if got := jsonOf(patch); got != `{"workflow_state_id":18}` {
		t.Errorf("patch %s", got)
	}
}

// When @me is read again after the story read, the state is read again before
// the PATCH: a story that another writer moved meanwhile is not moved back.
func TestWorktreeStartMeReadAgain(t *testing.T) {
	var patch map[string]any
	n := 0
	state := func() int {
		n++
		if n <= 2 { // the read during the fetch and the read after git
			return 7
		}
		return 10
	}
	sh := storyHandler(state, nil)
	me := func(w http.ResponseWriter, r *http.Request) {
		if n < 2 { // whoami fails during the fetch
			http.Error(w, `{"message":"down"}`, http.StatusServiceUnavailable)
			return
		}
		jsonHandler(whoamiBody)(w, r)
	}
	h := startRoutes(t, sh, me, &patch)
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

// A git error does not wait for the reads: the run cancels them.
func TestWorktreeStartGitErrorCancelsReads(t *testing.T) {
	var c calls
	var patch map[string]any
	hang := func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }
	h, main := startHarnessH(t, &c, hang, hang, 200, &patch)
	breakOrigin(t, main)
	t0 := time.Now()
	code, _, errs := h.run("worktree", "1234", "--start")
	if code != 1 || !strings.Contains(errs, "git fetch") {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	if d := time.Since(t0); d > 10*time.Second {
		t.Errorf("the run took %v", d)
	}
}

// blockingKeyring blocks each Get until the test ends, as a keyring that
// waits for an unlock prompt.
type blockingKeyring struct{ end chan struct{} }

func (k blockingKeyring) Get(string, string) (string, error) { <-k.end; return "", nil }
func (k blockingKeyring) Set(string, string, string) error   { return nil }

// A git error does not wait for the keyring read of the token.
func TestWorktreeStartGitErrorSkipsKeyring(t *testing.T) {
	h, main := wtHarness(t, nil, "", nil)
	k := blockingKeyring{make(chan struct{})}
	t.Cleanup(func() { close(k.end) })
	h.env.Keyring = k
	breakOrigin(t, main)
	done := make(chan int, 1)
	go func() {
		code, _, _ := h.run("worktree", "1234", "--start")
		done <- code
	}()
	select {
	case code := <-done:
		if code != 1 {
			t.Errorf("code %d", code)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the run waits for the keyring")
	}
}

// The concurrent steps fail as the steps in sequence did: a git error comes
// first, a failed story read comes without the worktree, and a failed later
// read comes with the worktree.
func TestWorktreeStartErrorOrder(t *testing.T) {
	notFound := func(w http.ResponseWriter, r *http.Request) { http.Error(w, `{"message":"no"}`, http.StatusNotFound) }
	down := func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"down"}`, http.StatusServiceUnavailable)
	}
	story := storyHandler(func() int { return 7 }, nil)
	states := jsonHandler(listBody(wfStates()...))
	for _, tc := range []struct {
		name          string
		noOrigin      bool
		story, states http.HandlerFunc
		code          int
		msg           string
		worktree      bool
	}{
		{"fetch and story fail", true, notFound, states, 1, "git fetch", false},
		{"fetch fails", true, story, states, 1, "git fetch", false},
		{"story fails", false, notFound, states, 3, "", false},
		{"states fail", false, story, down, 5, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var c calls
			var patch map[string]any
			h, main := startHarnessH(t, &c, tc.story, tc.states, 200, &patch)
			if tc.noOrigin {
				breakOrigin(t, main)
			}
			code, _, errs := h.run("worktree", "1234", "--start")
			if code != tc.code || !strings.Contains(errs, tc.msg) {
				t.Fatalf("code %d, stderr %s", code, errs)
			}
			_, has := decode(t, errs)["worktree"]
			_, err := os.Stat(filepath.Join(filepath.Dir(main), "1234"))
			if has != tc.worktree || (err == nil) != tc.worktree {
				t.Errorf("worktree in error %v, dir error %v: %s", has, err, errs)
			}
			if patch != nil {
				t.Errorf("patch %v", patch)
			}
		})
	}
}
