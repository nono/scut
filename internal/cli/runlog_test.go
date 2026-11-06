package cli_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/nono/scut/internal/cli"
	"github.com/nono/scut/internal/runlog"
)

const marker = "zqmarkerzq"

var withToken = map[string]string{"SCUT_TOKEN": token}

// anyRoute answers each API call with an empty list.
func anyRoute() map[string]http.HandlerFunc {
	return map[string]http.HandlerFunc{"/": jsonHandler(`{"entities":[]}`)}
}

func logPath(h *harness) string {
	return filepath.Join(h.env.Getenv("HOME"), ".local", "state", "scut", "scut.log")
}

func logLines(t *testing.T, h *harness) []map[string]any {
	t.Helper()
	b, err := os.ReadFile(logPath(h))
	if err != nil {
		t.Fatal(err)
	}
	var out []map[string]any
	for l := range strings.Lines(string(b)) {
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("not JSON: %q: %v", l, err)
		}
		out = append(out, m)
	}
	return out
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
	if l["flags"].(map[string]any)["fields"] != "id" {
		t.Errorf("flags = %v", l["flags"])
	}
	e := l["http"].([]any)[0].(map[string]any)
	if e["p"] != "/api/v4/example-ws/stories/1" || e["s"] != 200.0 || e["m"] != "GET" {
		t.Errorf("http = %v", e)
	}
}

// A search query or a name in ID form is not an ID.
func TestRunLogIDsOnlyForIDCommands(t *testing.T) {
	for _, args := range [][]string{
		{"story", "search", "20261106"},
		{"epic", "search", "20261106"},
		{"doc", "search", "20261106"},
		{"doc", "view", "20261106"},
		{"spec", "20261106"},
	} {
		h := newHarness(t, anyRoute(), "", withToken)
		h.run(args...)
		if l := lastLine(t, h); l["ids"] != nil || l["args_n"] != float64(len(args)-len(strings.Fields(l["cmd"].(string)))) {
			t.Errorf("%v: %v", args, l)
		}
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
	}}, "", withToken)
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
	h := newHarness(t, anyRoute(), "", withToken)
	h.run("api", "stories/search?query="+marker, "-X", "GET")
	h.run("api", "stories/some-text")
	b, err := os.ReadFile(logPath(h))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), marker) {
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
	}}, "", withToken)
	h.run("team", "list")
	h.run("api", "stories/"+token)
	h.run("team", "list", "--token-file", "/nonexistent/"+token)
	h.run("api", "-f", "x")
	b, err := os.ReadFile(logPath(h))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "s3cret") {
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
	if b, _ := os.ReadFile(logPath(h)); strings.Contains(string(b), "s3cret") {
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
		wantCode, wantOut, wantErr := newHarness(t, anyRoute(), "", withToken).run(args...)
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

// TestRunLogNoInputText covers each command and flag, also a new one. The error
// fields can quote an input by design. So can the path of scut api.
func TestRunLogNoInputText(t *testing.T) {
	check := func(t *testing.T, args []string, drop ...string) map[string]any {
		t.Helper()
		h := newHarness(t, anyRoute(), "", withToken)
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
	reached := map[string]bool{}
	for _, c := range leaves(cli.NewRoot(newHarness(t, nil, "", nil).env)) {
		path := strings.Fields(c.CommandPath())[1:]
		var drop []string
		if c.Name() == "api" {
			drop = []string{"http"}
		}
		l := check(t, append(slices.Clone(path), marker), drop...)
		_, reached[strings.Join(path, " ")] = l["http"]

		seen := map[string]bool{}
		visit := func(f *pflag.Flag) {
			if seen[f.Name] || f.Name == "help" || f.Value.Type() == "bool" || runlog.Logged(f.Name) {
				return
			}
			seen[f.Name] = true
			check(t, append(slices.Clone(path), "1", "--"+f.Name, marker))
		}
		c.Flags().VisitAll(visit)
		c.InheritedFlags().VisitAll(visit)
		if !seen["token-file"] {
			t.Errorf("%v: the walk did not reach the global flags", path)
		}
	}
	for _, name := range []string{"story search", "epic search", "doc search"} {
		if !reached[name] {
			t.Errorf("%s did not reach HTTP, so the check of http is empty", name)
		}
	}
}
