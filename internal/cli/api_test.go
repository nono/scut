package cli_test

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

type recorder struct {
	mu   sync.Mutex
	reqs []string
	body string
}

func (r *recorder) handler(resp string) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		b, _ := io.ReadAll(req.Body)
		r.mu.Lock()
		r.reqs = append(r.reqs, req.Method+" "+req.URL.Path)
		r.body = string(b)
		r.mu.Unlock()
		jsonHandler(resp)(w, req)
	}
}

func tokenVars() map[string]string { return map[string]string{"SCUT_TOKEN": token} }

func TestAPIDeleteNeedsYes(t *testing.T) {
	rec := &recorder{}
	h := newHarness(t, map[string]http.HandlerFunc{"/": rec.handler(`{}`)}, "", tokenVars())
	code, _, errs := h.run("api", "-X", "DELETE", "/story-links/1")
	if code != 2 {
		t.Fatalf("code %d, want 2; stderr %s", code, errs)
	}
	if len(rec.reqs) != 0 {
		t.Errorf("requests %v", rec.reqs)
	}
	if code, _, errs := h.run("api", "-X", "DELETE", "--yes", "/story-links/1"); code != 0 {
		t.Fatalf("with --yes: code %d, stderr %s", code, errs)
	}
	if len(rec.reqs) != 1 || rec.reqs[0] != "DELETE /api/v4/example-ws/story-links/1" {
		t.Errorf("requests %v", rec.reqs)
	}
}

func TestAPIPath(t *testing.T) {
	rec := &recorder{}
	h := newHarness(t, map[string]http.HandlerFunc{"/": rec.handler(`{"entity":{"id":5409}}`)}, "", tokenVars())
	code, out, errs := h.run("api", "/stories/5409")
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	if m := decode(t, out); m["id"] != float64(5409) {
		t.Errorf("out %s", out)
	}
	if code, _, errs := h.run("api", "/api/v4/whoami"); code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	want := []string{"GET /api/v4/example-ws/stories/5409", "GET /api/v4/whoami"}
	if strings.Join(rec.reqs, "|") != strings.Join(want, "|") {
		t.Errorf("requests %v, want %v", rec.reqs, want)
	}
}

func TestAPIFields(t *testing.T) {
	rec := &recorder{}
	h := newHarness(t, map[string]http.HandlerFunc{"/": rec.handler(`{"entity":{"id":1,"name":"x"}}`)}, "", tokenVars())
	code, out, errs := h.run("api", "/stories", "-f", "name=x", "-f", "estimate=3", "-f", "done=true",
		"-f", "none=null", "-f", "text=007", "-f", "s=\"q\"")
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	if len(rec.reqs) != 1 || rec.reqs[0] != "POST /api/v4/example-ws/stories" {
		t.Errorf("requests %v", rec.reqs)
	}
	b := decode(t, rec.body)
	if b["name"] != "x" || b["estimate"] != float64(3) || b["done"] != true || b["none"] != nil || b["text"] != "007" || b["s"] != `"q"` {
		t.Errorf("body %s", rec.body)
	}
	if m := decode(t, out); m["name"] != "x" || m["id"] != float64(1) || len(m) != 2 {
		t.Errorf("out %s", out)
	}
}

func TestAPIFieldsBadPair(t *testing.T) {
	h := newHarness(t, map[string]http.HandlerFunc{}, "", tokenVars())
	if code, _, _ := h.run("api", "/stories", "-f", "noequals"); code != 2 {
		t.Fatalf("code %d, want 2", code)
	}
}

func TestAPIInput(t *testing.T) {
	rec := &recorder{}
	h := newHarness(t, map[string]http.HandlerFunc{"/": rec.handler(`[{"id":1},{"id":2}]`)}, `{"name":"y"}`, tokenVars())
	code, out, errs := h.run("api", "/stories", "--input", "-")
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	if rec.reqs[0] != "POST /api/v4/example-ws/stories" || rec.body != `{"name":"y"}` {
		t.Errorf("requests %v body %s", rec.reqs, rec.body)
	}
	if !strings.HasPrefix(out, "[") {
		t.Errorf("out %s", out)
	}

	f := filepath.Join(t.TempDir(), "b.json")
	if err := os.WriteFile(f, []byte(`{"name":"z"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _, errs := h.run("api", "-X", "PUT", "/stories/1", "--input", f); code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	if rec.reqs[1] != "PUT /api/v4/example-ws/stories/1" || rec.body != `{"name":"z"}` {
		t.Errorf("requests %v body %s", rec.reqs, rec.body)
	}
}

func TestAPIInputConflicts(t *testing.T) {
	rec := &recorder{}
	h := newHarness(t, map[string]http.HandlerFunc{"/": rec.handler(`{}`)}, `{}`, tokenVars())
	if code, _, _ := h.run("api", "/stories", "--input", "-", "-f", "a=b"); code != 2 {
		t.Fatalf("code %d, want 2", code)
	}
	if code, _, _ := h.run("api", "/stories", "--input", "-"); code != 0 {
		t.Fatalf("valid input: code %d", code)
	}
	h.env.Stdin = strings.NewReader(`{bad`)
	if code, _, _ := h.run("api", "/stories", "--input", "-"); code != 2 {
		t.Fatalf("bad JSON: code %d, want 2", code)
	}
	if len(rec.reqs) != 1 {
		t.Errorf("requests %v", rec.reqs)
	}
}

func TestAPIBadMethod(t *testing.T) {
	h := newHarness(t, map[string]http.HandlerFunc{}, "", tokenVars())
	if code, _, _ := h.run("api", "-X", "FETCH", "/stories"); code != 2 {
		t.Fatalf("code %d, want 2", code)
	}
}

// A usage error exits 2 before the token is read, so it does not hide behind an auth error.
func TestAPIUsageNeedsNoToken(t *testing.T) {
	h := newHarness(t, map[string]http.HandlerFunc{}, `{bad`, nil)
	for _, args := range [][]string{
		{"api", "-X", "FETCH", "/stories"},
		{"api", "-X", "delete", "/story-links/1"},
		{"api", "/stories", "-f", "nokey"},
		{"api", "/stories", "--input", "-"},
		{"api", "/stories", "--input", "-", "-f", "a=b"},
	} {
		if code, _, errs := h.run(args...); code != 2 {
			t.Errorf("%v: code %d, want 2; stderr %s", args, code, errs)
		}
	}
}

func TestAPINotFound(t *testing.T) {
	h := newHarness(t, map[string]http.HandlerFunc{
		"/": func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) },
	}, "", tokenVars())
	if code, _, _ := h.run("api", "/stories/1"); code != 3 {
		t.Fatalf("code %d, want 3", code)
	}
}
