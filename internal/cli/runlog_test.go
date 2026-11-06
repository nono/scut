package cli_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/nono/scut/internal/cli"
	"github.com/nono/scut/internal/testgit"
	"github.com/nono/scut/internal/transport"
)

const marker = "zqmarkerzq"

// anyRoute answers each API call with an empty list.
func anyRoute() map[string]http.HandlerFunc {
	return map[string]http.HandlerFunc{"/": jsonHandler(`{"entities":[]}`)}
}

func logPath(h *harness) string {
	return filepath.Join(h.env.Getenv("HOME"), ".local", "state", "scut", "scut.log")
}

func logText(t *testing.T, h *harness) string {
	t.Helper()
	b, err := os.ReadFile(logPath(h))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func logLines(t *testing.T, h *harness) []map[string]any {
	t.Helper()
	var out []map[string]any
	for l := range strings.Lines(logText(t, h)) {
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("not JSON: %q: %v", l, err)
		}
		out = append(out, m)
	}
	return out
}

type closedPipe struct{}

func (closedPipe) Write([]byte) (int, error) {
	return 0, fmt.Errorf("write /dev/stdout: %w", syscall.EPIPE)
}

// A reader that closes the pipe, as head does, must not lose the line. The run
// keeps the exit code of a process that SIGPIPE stops, and prints no error.
func TestRunLogBrokenPipe(t *testing.T) {
	h := newHarness(t, nil, "", nil)
	var errb strings.Builder
	if code := cli.Execute([]string{"spec", "getStory"}, closedPipe{}, &errb, h.env); code != 141 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if errb.Len() != 0 {
		t.Errorf("stderr = %q", errb.String())
	}
	l := lastLine(t, h)
	if l["exit"] != 141.0 || !strings.Contains(l["error"].(string), "broken pipe") {
		t.Errorf("line = %v", l)
	}
}

func lastLine(t *testing.T, h *harness) map[string]any {
	t.Helper()
	l := logLines(t, h)
	if len(l) == 0 {
		t.Fatal("the log is empty")
	}
	return l[len(l)-1]
}

func TestRunLogSuccess(t *testing.T) {
	h := newHarness(t, map[string]http.HandlerFunc{
		"GET /api/v4/example-ws/stories/1": jsonHandler(`{"entity":{"id":1}}`),
	}, "", map[string]string{"SCUT_TOKEN": token, "CLAUDE_CODE_SESSION_ID": "s1"})
	code, stdout, stderr := h.run("story", "view", "1", "--fields", "id")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	l := lastLine(t, h)
	for k, want := range map[string]any{
		"cmd": "story view", "exit": 0.0, "session": "s1", "args_n": 1.0, "out_bytes": float64(len(stdout)),
	} {
		if l[k] != want {
			t.Errorf("%s = %v, want %v", k, l[k], want)
		}
	}
	if !reflect.DeepEqual(l["ids"], []any{1.0}) {
		t.Errorf("ids = %v", l["ids"])
	}
	if !reflect.DeepEqual(l["flags"], map[string]any{"fields": "id"}) {
		t.Errorf("flags must hold only what the user set: %v", l["flags"])
	}
	if l["v"] != transport.Version || l["cwd"] != h.env.Dir {
		t.Errorf("v = %v, cwd = %v", l["v"], l["cwd"])
	}
	e := l["http"].([]any)[0].(map[string]any)
	if e["p"] != "/api/v4/example-ws/stories/1" || e["s"] != 200.0 || e["m"] != "GET" {
		t.Errorf("http = %v", e)
	}
}

// pflag stops at a bad flag, so the arguments after it are not known.
func TestRunLogBadFlagHasNoArgCount(t *testing.T) {
	for _, args := range [][]string{
		{"story", "view", "--nope", "1"},
		{"story", "view", "1", "--nope", "2"},
		{"story", "veiw", "--nope", "1"},
	} {
		h := newHarness(t, anyRoute(), "", tokenVars())
		h.run(args...)
		if l := lastLine(t, h); l["exit"] != 2.0 || l["args_n"] != nil {
			t.Errorf("%v: %v", args, l)
		}
	}
	h := newHarness(t, anyRoute(), "", tokenVars())
	h.run("story", "view", "1", "2", "--limit", "x")
	if l := lastLine(t, h); l["args_n"] != nil {
		t.Errorf("unknown flag of a known command: %v", l)
	}
	h = newHarness(t, anyRoute(), "", tokenVars())
	h.run("story", "search")
	if l := lastLine(t, h); l["args_n"] != 0.0 {
		t.Errorf("a count error keeps the count: %v", l)
	}
}

// The transport of the log must not change the text of a timeout.
func TestFileTimeoutSameTextWithLog(t *testing.T) {
	stop := make(chan struct{})
	host := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-stop }))
	defer host.Close()
	defer close(stop)
	got := map[string]bool{}
	for _, mode := range []string{"off", "on"} {
		for range 6 {
			h := newHarness(t, map[string]http.HandlerFunc{
				"GET " + filesPath: jsonHandler(listBody(map[string]any{
					"id": 1, "name": "a.txt", "content_type": "text/plain", "size": 2, "url": host.URL + "/f/1",
				})),
			}, "", map[string]string{"SCUT_TOKEN": "sct_rw_acme_s3cret", "SCUT_LOG": mode})
			h.env.FileClient = host.Client()
			h.env.FileTimeout = 20 * time.Millisecond
			h.env.FileHosts = []string{"127.0.0.1"}
			h.env.Sleep = func(time.Duration) {}
			code, _, stderr := h.run("story", "files", "5818", "--out", t.TempDir())
			if code != 5 {
				t.Fatalf("exit %d: %s", code, stderr)
			}
			got[stderr] = true
		}
	}
	want := `{"error":"download file 1: context deadline exceeded"}` + "\n"
	if len(got) != 1 || !got[want] {
		t.Errorf("error texts: %v, want only %q", got, want)
	}
}

func TestRunLogRetries(t *testing.T) {
	var n atomic.Int32
	h := newHarness(t, map[string]http.HandlerFunc{"/": func(w http.ResponseWriter, r *http.Request) {
		if n.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		jsonHandler(`{"entities":[]}`)(w, r)
	}}, "", tokenVars())
	h.env.Sleep = func(time.Duration) {}
	if code, _, stderr := h.run("team", "list"); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	es := lastLine(t, h)["http"].([]any)
	if len(es) != 2 || es[0].(map[string]any)["s"] != 503.0 || es[1].(map[string]any)["s"] != 200.0 {
		t.Errorf("http = %v", es)
	}
}

func TestRunLogBeforeCommand(t *testing.T) {
	for _, c := range []struct {
		args    []string
		cmd     string
		exit    float64
		inError string
	}{
		{[]string{"story", "veiw", "1", "--fields", "x"}, "story", 2, "veiw"},
		{[]string{"story", "view", "1", "--nope"}, "story view", 2, "nope"},
		{[]string{"team", "list"}, "team list", 4, ""},
		{[]string{"--help"}, "", 0, ""},
	} {
		t.Run(strings.Join(c.args, " "), func(t *testing.T) {
			h := newHarness(t, anyRoute(), "", nil)
			h.run(c.args...)
			l := lastLine(t, h)
			if l["cmd"] != c.cmd || l["exit"] != c.exit {
				t.Errorf("line = %v", l)
			}
			if _, ok := l["error"]; ok != (c.exit != 0) {
				t.Errorf("error field: %v", l)
			}
			if c.inError != "" && !strings.Contains(l["error"].(string), c.inError) {
				t.Errorf("error = %v", l["error"])
			}
			if _, ok := l["http"]; ok {
				t.Errorf("http with no call: %v", l)
			}
		})
	}
}

func TestRunLogAPIPath(t *testing.T) {
	h := newHarness(t, anyRoute(), "", tokenVars())
	h.run("api", "stories/search?query="+marker, "-X", "GET")
	h.run("api", "stories/some-text")
	if b := logText(t, h); strings.Contains(b, marker) {
		t.Errorf("the query is in the log: %s", b)
	}
	lines := logLines(t, h)
	if p := lines[0]["http"].([]any)[0].(map[string]any)["p"]; p != "/api/v4/example-ws/stories/search" {
		t.Errorf("p = %v", p)
	}
	if p := lines[1]["http"].([]any)[0].(map[string]any)["p"]; p != "/api/v4/example-ws/stories/some-text" {
		t.Errorf("p = %v", p)
	}
}

func TestRunLogNoToken(t *testing.T) {
	h := newHarness(t, map[string]http.HandlerFunc{"/": func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = fmt.Fprintf(w, `{"message":"bad %s"}`, token)
	}}, "", tokenVars())
	h.run("team", "list")
	h.run("api", "stories/"+token)
	h.run("team", "list", "--token-file", "/nonexistent/"+token)
	h.run("api", "stories/1", "-f", token) // a usage error that quotes the token
	b := logText(t, h)
	if strings.Contains(b, "s3cret") {
		t.Errorf("the token is in the log: %s", b)
	}
	if len(logLines(t, h)) != 4 {
		t.Errorf("log = %s", b)
	}
}

func TestRunLogFile(t *testing.T) {
	h := filesHarness(t, []fileSpec{{id: 7, name: "a-name.txt", body: "hi", size: -1}}, 0, nil)
	dir := t.TempDir()
	if code, _, stderr := h.run("story", "files", "5818", "--out", dir); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	es := lastLine(t, h)["http"].([]any)
	e := es[len(es)-1].(map[string]any)
	if e["h"] != "127.0.0.1" || e["s"] != 200.0 {
		t.Errorf("entry = %v", e)
	}
	if _, ok := e["p"]; ok {
		t.Errorf("a file entry has a path: %v", e)
	}
}

func TestRunLogAuthLogin(t *testing.T) {
	h := newHarness(t, map[string]http.HandlerFunc{
		"GET /api/v4/whoami": jsonHandler(whoamiBody),
	}, token+"\n", nil)
	if code, _, stderr := h.run("auth", "login"); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	e := lastLine(t, h)["http"].([]any)[0].(map[string]any)
	if e["p"] != "/api/v4/whoami" {
		t.Errorf("entry = %v", e)
	}
	if b := logText(t, h); strings.Contains(b, "s3cret") {
		t.Errorf("the token is in the log: %s", b)
	}
}

func TestRunLogOff(t *testing.T) {
	h := newHarness(t, anyRoute(), "", map[string]string{"SCUT_TOKEN": token, "SCUT_LOG": "off"})
	h.run("team", "list")
	if _, err := os.Stat(filepath.Dir(logPath(h))); !os.IsNotExist(err) {
		t.Errorf("log directory: %v", err)
	}
}

func TestRunLogFailureHasNoEffect(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	dirState := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dirState, "scut", "scut.log"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"team", "list"}, {"story", "veiw"}} {
		wantCode, wantOut, wantErr := newHarness(t, anyRoute(), "", tokenVars()).run(args...)
		for name, state := range map[string]string{"state is a file": file, "log is a directory": dirState} {
			h := newHarness(t, anyRoute(), "", map[string]string{"SCUT_TOKEN": token, "XDG_STATE_HOME": state})
			code, out, errb := h.run(args...)
			if code != wantCode || out != wantOut || errb != wantErr {
				t.Errorf("%s: %v: got %d %q %q, want %d %q %q", name, args, code, out, errb, wantCode, wantOut, wantErr)
			}
		}
	}
}

// leaves returns each command with no subcommands.
func leaves(c *cobra.Command) []*cobra.Command {
	if !c.HasSubCommands() {
		return []*cobra.Command{c}
	}
	var out []*cobra.Command
	for _, sub := range c.Commands() {
		out = append(out, leaves(sub)...)
	}
	return out
}

// loggedFlags has a copy of the allowlist of the log, so a flag added there
// must also be added here, and the walk checks each other flag.
var loggedFlags = map[string]bool{
	"limit": true, "comments": true, "fields": true, "format": true, "depth": true,
	"method": true, "state": true, "workflow": true, "team": true, "type": true,
}

// idCommands has a copy of the commands whose arguments the log keeps in ids.
var idCommands = map[string]bool{
	"story view": true, "story update": true, "story comment": true, "story comments": true,
	"story link": true, "story unlink": true, "story relink": true, "story subtasks": true,
	"story branch": true, "story files": true,
	"epic view": true, "epic stories": true, "epic comment": true,
	"worktree": true,
}

// walkCase is what a command needs to reach HTTP when one positional argument
// of "1" per required place is not sufficient. The positional arguments come
// first, npos of them.
type walkCase struct {
	args    []string
	npos    int
	stdin   string
	method  string // a method that the run must use
	harness func(t *testing.T, stdin string) *harness
}

func walkCases(t *testing.T) map[string]walkCase {
	create := func(t *testing.T, _ string) *harness { return createHarness(t, &createFake{}, &calls{}, nil) }
	update := func(t *testing.T, _ string) *harness {
		var body map[string]any
		return updateHarness(t, &calls{}, &body, nil)
	}
	repo := func(t *testing.T, stdin string) *harness {
		h := newHarness(t, anyRoute(), stdin, tokenVars())
		_, h.env.Dir = testgit.Repos(t)
		return h
	}
	return map[string]walkCase{
		"api":           {args: []string{"stories/1"}, npos: 1},
		"auth login":    {stdin: token + "\n"},
		"epic comment":  {args: []string{"1", "--body-file", "-"}, npos: 1, stdin: "x", method: "POST"},
		"story comment": {args: []string{"1", "--body-file", "-"}, npos: 1, stdin: "x", method: "POST"},
		"story create": {args: []string{"--name", "n", "--type", "bug", "--team", "Software Dev", "--state", "To Do"},
			method: "POST", harness: create},
		"story files":  {args: []string{"1", "--out", t.TempDir()}, npos: 1},
		"story link":   {args: []string{"1", "blocks", "2"}, npos: 3, method: "POST"},
		"story relink": {args: []string{"1", "blocks", "2", "relates-to"}, npos: 4},
		"story unlink": {args: []string{"1", "blocks", "2"}, npos: 3},
		"story update": {args: []string{"5818", "--archive"}, npos: 1, method: "PATCH", harness: update},
		"worktree":     {args: []string{"1"}, npos: 1, harness: repo},
	}
}

// walkBase returns the case of c: its entry in cases, or the smallest count of
// positional arguments, from 1, that c accepts.
func walkBase(t *testing.T, c *cobra.Command, cases map[string]walkCase, name string) walkCase {
	t.Helper()
	wc, ok := cases[name]
	if !ok {
		// A command can take no argument and then need a git branch, so 0 comes last.
		for _, n := range []int{1, 2, 3, 4, 0} {
			if c.ValidateArgs(slices.Repeat([]string{"1"}, n)) == nil {
				wc.args, wc.npos = slices.Repeat([]string{"1"}, n), n
				break
			}
		}
	}
	if wc.harness == nil {
		wc.harness = func(t *testing.T, stdin string) *harness {
			return newHarness(t, anyRoute(), stdin, tokenVars())
		}
	}
	return wc
}

// TestRunLogNoInputText covers each command, each place of a positional
// argument and each non-bool flag outside the allowlist, also a new one. A
// command that calls the API must reach HTTP, so the check of http is not empty.
// The error fields can quote an input by design. So can the path of scut api.
func TestRunLogNoInputText(t *testing.T) {
	cases := walkCases(t)
	check := func(t *testing.T, h *harness, args []string, drop ...string) map[string]any {
		t.Helper()
		h.run(args...)
		l := lastLine(t, h)
		rest := map[string]any{}
		for k, v := range l {
			if k != "error" && k != "api" && !slices.Contains(drop, k) {
				rest[k] = v
			}
		}
		b, err := json.Marshal(rest)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), marker) {
			t.Errorf("%v: %s", args, b)
		}
		return l
	}
	all := cli.NewRoot(newHarness(t, nil, "", nil).env)
	seenID := map[string]bool{}
	for _, c := range leaves(all) {
		path := strings.Fields(c.CommandPath())[1:]
		name := strings.Join(path, " ")
		wc := walkBase(t, c, cases, name)
		line := func(args []string) []string { return append(slices.Clone(path), args...) }

		l := check(t, wc.harness(t, wc.stdin), line(wc.args))
		if _, ok := l["http"]; !ok && len(cli.Ops(c)) > 0 {
			t.Errorf("%s did not reach HTTP, so the check of http is empty: %v", name, l)
		}
		if wc.method != "" && !usesMethod(l, wc.method) {
			t.Errorf("%s did not send %s: %v", name, wc.method, l)
		}

		for i := range wc.npos {
			args := slices.Clone(wc.args)
			args[i] = marker
			var drop []string
			if name == "api" {
				drop = []string{"http"}
			}
			check(t, wc.harness(t, wc.stdin), line(args), drop...)
		}

		seen := map[string]bool{}
		visit := func(f *pflag.Flag) {
			if seen[f.Name] || f.Name == "help" || f.Value.Type() == "bool" || loggedFlags[f.Name] {
				return
			}
			seen[f.Name] = true
			// In the form Name=Value, so that a flag of that form gets further.
			check(t, wc.harness(t, wc.stdin), line(append(slices.Clone(wc.args), "--"+f.Name, marker+"="+marker)))
		}
		c.Flags().VisitAll(visit)
		c.InheritedFlags().VisitAll(visit)
		if !seen["token-file"] {
			t.Errorf("%v: the walk did not reach the global flags", path)
		}

		// A number is an ID only for a command that takes IDs.
		h := newHarness(t, anyRoute(), "", tokenVars())
		h.run(line([]string{"20261106"})...)
		_, hasIDs := lastLine(t, h)["ids"]
		if hasIDs != idCommands[name] {
			t.Errorf("%s: ids in the line = %v", name, hasIDs)
		}
		seenID[name] = hasIDs
	}
	for name := range idCommands {
		if !seenID[name] {
			t.Errorf("%s is in idCommands and is not a command that logs IDs", name)
		}
	}
}

func usesMethod(l map[string]any, method string) bool {
	es, _ := l["http"].([]any)
	return slices.ContainsFunc(es, func(e any) bool { return e.(map[string]any)["m"] == method })
}

// The query is in no field, also when the search fails.
func TestRunLogSearchQuery(t *testing.T) {
	for name, h := range map[string]http.HandlerFunc{
		"200": jsonHandler(`{"data":[]}`),
		"503": func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) },
		"400": func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusBadRequest) },
	} {
		t.Run(name, func(t *testing.T) {
			hn := newHarness(t, map[string]http.HandlerFunc{"/": h}, "", tokenVars())
			hn.env.Sleep = func(time.Duration) {}
			for _, args := range [][]string{{"story", "search", marker}, {"epic", "search", marker}, {"doc", "search", marker}} {
				hn.run(args...)
			}
			if b := logText(t, hn); strings.Contains(b, marker) {
				t.Errorf("the query is in the log: %s", b)
			}
			if n := len(logLines(t, hn)); n != 3 {
				t.Errorf("%d lines", n)
			}
		})
	}
}

// The signal reaches a run through Execute. The handler is in place when the
// request arrives, so the signal cancels the run and does not stop the test.
func TestRunLogSIGINT(t *testing.T) {
	h := newHarness(t, map[string]http.HandlerFunc{"/": func(_ http.ResponseWriter, r *http.Request) {
		_ = syscall.Kill(syscall.Getpid(), syscall.SIGINT)
		<-r.Context().Done()
	}}, "", tokenVars())
	if code, _, errb := h.run("team", "list"); code != 130 {
		t.Fatalf("exit %d: %s", code, errb)
	}
	if l := lastLine(t, h); l["exit"] != 130.0 {
		t.Errorf("line = %v", l)
	}
}
