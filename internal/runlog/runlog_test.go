package runlog

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/nono/scut/internal/exit"
)

const testUUID = "00000000-0000-4000-8000-0000000000aa"

var testStart = time.Date(2026, 11, 6, 10, 15, 30, 123e6, time.FixedZone("x", 3600))

func envOf(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func newRec(t *testing.T, vars map[string]string) *Recorder {
	t.Helper()
	m := map[string]string{"HOME": t.TempDir()}
	for k, v := range vars {
		m[k] = v
	}
	calls := 0
	r := Start(envOf(m), func() time.Time {
		calls++
		return testStart.Add(time.Duration(calls-1) * 250 * time.Millisecond)
	})
	if r == nil {
		t.Fatal("no recorder")
	}
	return r
}

func decodeLine(t *testing.T, b []byte) map[string]any {
	t.Helper()
	if len(b) == 0 || b[len(b)-1] != '\n' {
		t.Fatalf("line must end in a newline: %q", b)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("not JSON: %q: %v", b, err)
	}
	return m
}

func TestStart(t *testing.T) {
	now := time.Now
	for name, get := range map[string]func(string) string{
		"nil getenv": nil,
		"off":        envOf(map[string]string{"SCUT_LOG": "off", "HOME": "/h", "XDG_STATE_HOME": "/s"}),
		"no dirs":    envOf(nil),
	} {
		if r := Start(get, now); r != nil {
			t.Errorf("%s: got a recorder", name)
		}
	}
	if r := Start(envOf(map[string]string{"HOME": "/h", "XDG_STATE_HOME": "/s"}), now); r.path != "/s/scut/scut.log" {
		t.Errorf("XDG path = %q", r.path)
	}
	if r := Start(envOf(map[string]string{"HOME": "/h"}), now); r.path != "/h/.local/state/scut/scut.log" {
		t.Errorf("HOME path = %q", r.path)
	}
	// The XDG specification says to ignore a relative value.
	if r := Start(envOf(map[string]string{"HOME": "/h", "XDG_STATE_HOME": "rel"}), now); r.path != "/h/.local/state/scut/scut.log" {
		t.Errorf("relative XDG path = %q", r.path)
	}
	for _, get := range []func(string) string{
		envOf(map[string]string{"XDG_STATE_HOME": "rel"}),
		envOf(map[string]string{"HOME": "rel"}),
	} {
		if r := Start(get, now); r != nil {
			t.Errorf("relative path = %q", r.path)
		}
	}
}

func TestNilRecorder(t *testing.T) {
	var r *Recorder
	base := http.DefaultTransport
	if r.API(base) != base || r.Files(base) != base {
		t.Error("a nil recorder must return the base")
	}
	r.Finish(Result{}) // must not panic
}

func TestEncodeIDs(t *testing.T) {
	r := newRec(t, nil)
	m := decodeLine(t, r.encode(Result{IDArgs: true, Args: []string{
		"sc-12", "13", "007", "99999999999999999999", testUUID, "some text", "misc-12",
		"https://app.shortcut.com/example-ws/story/14/a-title",
	}}))
	want := []any{12.0, 13.0, 7.0, testUUID}
	if !reflect.DeepEqual(m["ids"], want) {
		t.Errorf("ids = %v, want %v", m["ids"], want)
	}
	if m["args_n"] != 8.0 {
		t.Errorf("args_n = %v", m["args_n"])
	}
}

func TestEncodeIDsLimit(t *testing.T) {
	r := newRec(t, nil)
	args := make([]string, 150)
	for i := range args {
		args[i] = strconv.Itoa(i + 1)
	}
	m := decodeLine(t, r.encode(Result{IDArgs: true, Args: args}))
	if ids := m["ids"].([]any); len(ids) != 100 {
		t.Errorf("len(ids) = %d", len(ids))
	}
	if m["args_n"] != 150.0 {
		t.Errorf("args_n = %v", m["args_n"])
	}
}

func TestEncodeNoIDArgs(t *testing.T) {
	m := decodeLine(t, newRec(t, nil).encode(Result{Args: []string{"1234", testUUID}}))
	if _, ok := m["ids"]; ok {
		t.Errorf("ids = %v", m["ids"])
	}
	if m["args_n"] != 2.0 {
		t.Errorf("args_n = %v", m["args_n"])
	}
}

func TestEncodeFlags(t *testing.T) {
	r := newRec(t, nil)
	m := decodeLine(t, r.encode(Result{Flags: map[string]string{"state": "Done", "name": "secret", "token-file": "/x"}}))
	want := map[string]any{"state": "Done", "name": "", "token-file": ""}
	if !reflect.DeepEqual(m["flags"], want) {
		t.Errorf("flags = %v, want %v", m["flags"], want)
	}
	m = decodeLine(t, r.encode(Result{Flags: map[string]string{"state": strings.Repeat("x", 300)}}))
	if v := m["flags"].(map[string]any)["state"].(string); len(v) != 200 {
		t.Errorf("len(state) = %d", len(v))
	}
}

func TestEncodeAllowlist(t *testing.T) {
	for _, f := range []string{"limit", "comments", "fields", "format", "depth", "method", "state", "workflow", "team", "type"} {
		if got := flags(map[string]string{f: "v"})[f]; got != "v" {
			t.Errorf("flag %s: value %q", f, got)
		}
	}
}

func TestEncodeError(t *testing.T) {
	r := newRec(t, nil)
	m := decodeLine(t, r.encode(Result{Err: &exit.Error{Msg: "bad", API: json.RawMessage(`{"message":"no"}`)}}))
	if m["error"] != "bad" || m["api"] != `{"message":"no"}` {
		t.Errorf("exit.Error: %v", m)
	}
	m = decodeLine(t, r.encode(Result{Err: fmt.Errorf("wrap: %w", errors.New("plain"))}))
	if m["error"] != "wrap: plain" {
		t.Errorf("plain: %v", m)
	}
	if _, ok := m["api"]; ok {
		t.Errorf("api with no API body: %v", m)
	}
	m = decodeLine(t, r.encode(Result{}))
	if _, ok := m["error"]; ok {
		t.Errorf("error on success: %v", m)
	}
	m = decodeLine(t, r.encode(Result{Err: errors.New(strings.Repeat("x", 5000))}))
	if v := m["error"].(string); len(v) != 1024 {
		t.Errorf("len(error) = %d", len(v))
	}
}

func TestEncodeCutUTF8(t *testing.T) {
	r := newRec(t, nil)
	for name, msg := range map[string]string{
		"cut in a character": strings.Repeat("é", 2000),
		"invalid bytes":      "a\xff\xfeb",
	} {
		b := r.encode(Result{Err: errors.New(msg)})
		m := decodeLine(t, b)
		v := m["error"].(string)
		if !utf8.ValidString(v) || strings.Contains(v, "�") || len(v) > 1024 {
			t.Errorf("%s: error = %q", name, v)
		}
	}
}

func TestEncodeScrubsToken(t *testing.T) {
	const tok = "sct_rw_example-ws_s3cret"
	r := newRec(t, nil)
	base := roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: http.NoBody}, nil
	})
	trip(t, r.API(base), "https://example.test/x/"+tok)
	for name, res := range map[string]Result{
		"error": {Err: errors.New("bad " + tok)},
		"dir":   {Dir: "/x/" + tok},
		"flag":  {Flags: map[string]string{"state": tok}},
		"cut":   {Err: errors.New(strings.Repeat("x", 1024-len(tok)+8) + tok)},
	} {
		b := r.encode(res)
		if strings.Contains(string(b), "s3cret") || !strings.Contains(string(b), "***") {
			t.Errorf("%s: %s", name, b)
		}
		decodeLine(t, b)
	}
}

func TestEncodeScrubsTransportError(t *testing.T) {
	r := newRec(t, nil)
	base := roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("x sct_rw_example-ws_s3cret y")
	})
	resp, err := r.API(base).RoundTrip(httptest.NewRequest(http.MethodGet, "https://example.test/x", nil))
	if err == nil {
		_ = resp.Body.Close()
		t.Fatal("the error must reach the caller")
	}
	if b := r.encode(Result{}); strings.Contains(string(b), "s3cret") {
		t.Errorf("the token is in the line: %s", b)
	}
}

// ParseToken accepts any characters in the secret.
func TestEncodeScrubsAnySecret(t *testing.T) {
	for _, secret := range []string{"abc.def+ghi/jkl", `abc:def'ghi~jkl`} {
		b := newRec(t, nil).encode(Result{Err: errors.New("bad sct_rw_example-ws_" + secret + " end")})
		if strings.Contains(string(b), "jkl") || !strings.Contains(string(b), "*** end") {
			t.Errorf("%s: %s", secret, b)
		}
		decodeLine(t, b)
	}
	// A quote in the text ends the match and leaves valid JSON.
	decodeLine(t, newRec(t, nil).encode(Result{Err: errors.New(`"sct_rw_example-ws_s3cret" end`)}))
}

func TestEncodeShape(t *testing.T) {
	r := newRec(t, map[string]string{"CLAUDE_CODE_SESSION_ID": testUUID})
	m := decodeLine(t, r.encode(Result{Version: "v1", Dir: "/w", Cmd: "story view", OutBytes: 42, Exit: 3}))
	for k, want := range map[string]any{
		"t": "2026-11-06T09:15:30.123Z", "v": "v1", "session": testUUID, "cwd": "/w",
		"cmd": "story view", "ms": 250.0, "out_bytes": 42.0, "exit": 3.0, "args_n": 0.0,
	} {
		if m[k] != want {
			t.Errorf("%s = %v, want %v", k, m[k], want)
		}
	}
	m = decodeLine(t, newRec(t, nil).encode(Result{}))
	if _, ok := m["session"]; ok {
		t.Errorf("session with no variable: %v", m)
	}
	for _, k := range []string{"cmd", "args_n", "ms", "out_bytes", "exit"} {
		if _, ok := m[k]; !ok {
			t.Errorf("%s must always be present: %v", k, m)
		}
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func trip(t *testing.T, rt http.RoundTripper, url string) {
	t.Helper()
	resp, err := rt.RoundTrip(httptest.NewRequest(http.MethodGet, url, nil))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
}

func TestAPITransport(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	defer srv.Close()
	r := newRec(t, nil)
	c := http.Client{Transport: r.API(nil)}
	resp, err := c.Get(srv.URL + "/api/v4/example-ws/stories/search?query=secret")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	b := r.encode(Result{})
	if strings.Contains(string(b), "secret") {
		t.Errorf("the query is in the line: %s", b)
	}
	e := decodeLine(t, b)["http"].([]any)[0].(map[string]any)
	if e["m"] != "GET" || e["p"] != "/api/v4/example-ws/stories/search" || e["s"] != 418.0 {
		t.Errorf("entry = %v", e)
	}
	if e["ms"] != 250.0 { // the clock of newRec moves 250 ms for each call
		t.Errorf("ms = %v", e["ms"])
	}
	if _, ok := e["h"]; ok {
		t.Errorf("API entry has a host: %v", e)
	}
}

func TestTransportError(t *testing.T) {
	r := newRec(t, nil)
	base := roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New(strings.Repeat("x", 500))
	})
	req := httptest.NewRequest(http.MethodGet, "https://example.test/x", nil)
	resp, err := r.API(base).RoundTrip(req)
	if err == nil {
		_ = resp.Body.Close()
		t.Fatal("the error must reach the caller")
	}
	e := decodeLine(t, r.encode(Result{}))["http"].([]any)[0].(map[string]any)
	if e["s"] != 0.0 || len(e["err"].(string)) != 200 {
		t.Errorf("entry = %v", e)
	}
}

func TestTransportLimit(t *testing.T) {
	r := newRec(t, nil)
	base := roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: http.NoBody}, nil
	})
	rt := r.API(base)
	for range 150 {
		trip(t, rt, "https://example.test/x")
	}
	m := decodeLine(t, r.encode(Result{}))
	if n := len(m["http"].([]any)); n != 100 {
		t.Errorf("len(http) = %d", n)
	}
	if m["http_n"] != 150.0 {
		t.Errorf("http_n = %v", m["http_n"])
	}
	if _, ok := decodeLine(t, newRec(t, nil).encode(Result{}))["http_n"]; ok {
		t.Error("http_n must be absent with no cut")
	}
}

func TestFilesTransport(t *testing.T) {
	r := newRec(t, nil)
	base := roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: http.NoBody}, nil
	})
	trip(t, r.Files(base), "https://files.example.test/a/name.png?x=secret")
	b := r.encode(Result{})
	if s := string(b); strings.Contains(s, "name.png") || strings.Contains(s, "secret") {
		t.Errorf("file text in the line: %s", b)
	}
	e := decodeLine(t, b)["http"].([]any)[0].(map[string]any)
	if e["h"] != "files.example.test" {
		t.Errorf("entry = %v", e)
	}
	if _, ok := e["p"]; ok {
		t.Errorf("file entry has a path: %v", e)
	}
}
