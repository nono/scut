package cli_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const filesPath = "/api/v4/acme/stories/5818/files"

type fileSpec struct {
	id   int
	name string
	body string
	size int // -1: len(body)
}

// filesHarness serves the file list and the files. gotHdr records the headers
// of the last file request.
func filesHarness(t *testing.T, specs []fileSpec, status int, gotHdr *http.Header) *harness {
	t.Helper()
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if gotHdr != nil {
			*gotHdr = r.Header.Clone()
		}
		var id int
		_, _ = fmt.Sscanf(r.URL.Path, "/f/%d", &id)
		if status != 0 {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`<?xml version="1.0"?><Error><Code>InvalidArgument</Code></Error>`))
			return
		}
		for _, s := range specs {
			if s.id == id {
				_, _ = w.Write([]byte(s.body))
			}
		}
	}))
	t.Cleanup(host.Close)
	var items []any
	for _, s := range specs {
		size := s.size
		if size < 0 {
			size = len(s.body)
		}
		items = append(items, map[string]any{
			"id": s.id, "name": s.name, "content_type": "text/plain", "size": size,
			"url": fmt.Sprintf("%s/f/%d", host.URL, s.id),
		})
	}
	return newHarness(t, map[string]http.HandlerFunc{
		"GET " + filesPath: jsonHandler(listBody(items...)),
	}, "", searchEnv)
}

func TestFilesHeader(t *testing.T) {
	var hdr http.Header
	h := filesHarness(t, []fileSpec{{1, "a.txt", "hello", -1}}, 0, &hdr)
	out := t.TempDir()
	code, stdout, errs := h.run("story", "files", "5818", "--out", out)
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	if hdr.Get("Shortcut-Token") != "sct_rw_acme_s3cret" {
		t.Errorf("headers %v", hdr)
	}
	if hdr.Get("Authorization") != "" {
		t.Errorf("Authorization %q", hdr.Get("Authorization"))
	}
	var l []map[string]any
	if err := json.Unmarshal([]byte(stdout), &l); err != nil || len(l) != 1 {
		t.Fatalf("stdout %s: %v", stdout, err)
	}
	abs, _ := filepath.Abs(filepath.Join(out, "a.txt"))
	if l[0]["path"] != abs || l[0]["id"] != float64(1) || l[0]["size"] != float64(5) || l[0]["content_type"] != "text/plain" {
		t.Errorf("entry %v", l[0])
	}
	if b, _ := os.ReadFile(abs); string(b) != "hello" {
		t.Errorf("content %q", b)
	}
}

func TestFilesNone(t *testing.T) {
	h := filesHarness(t, nil, 0, nil)
	code, stdout, errs := h.run("story", "files", "5818", "--out", t.TempDir())
	if code != 0 || strings.TrimSpace(stdout) != "[]" {
		t.Errorf("code %d, stdout %q, stderr %s", code, stdout, errs)
	}
}

func entries(t *testing.T, dir string) []string {
	t.Helper()
	es, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var n []string
	for _, e := range es {
		n = append(n, e.Name())
	}
	return n
}

func TestFilesErrorLeavesNoFile(t *testing.T) {
	h := filesHarness(t, []fileSpec{{1, "a.txt", "hello", -1}}, 400, nil)
	out := t.TempDir()
	code, stdout, errs := h.run("story", "files", "5818", "--out", out)
	if code != 1 {
		t.Fatalf("code %d, stdout %s, stderr %s", code, stdout, errs)
	}
	if n := entries(t, out); len(n) != 0 {
		t.Errorf("files %v", n)
	}
	if strings.Contains(errs, "s3cret") {
		t.Errorf("token in %s", errs)
	}
	if strings.TrimSpace(stdout) != "[]" {
		t.Errorf("stdout %q", stdout)
	}
}

func TestFilesServerError(t *testing.T) {
	h := filesHarness(t, []fileSpec{{1, "a.txt", "hello", -1}}, 503, nil)
	if code, _, errs := h.run("story", "files", "5818", "--out", t.TempDir()); code != 5 {
		t.Errorf("code %d, stderr %s", code, errs)
	}
}

func TestFilesShortBody(t *testing.T) {
	h := filesHarness(t, []fileSpec{{1, "a.txt", "hello", 99}}, 0, nil)
	out := t.TempDir()
	code, _, errs := h.run("story", "files", "5818", "--out", out)
	if code != 1 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	if n := entries(t, out); len(n) != 0 {
		t.Errorf("files %v", n)
	}
}

func TestFilesStopsAtFirstFailure(t *testing.T) {
	h := filesHarness(t, []fileSpec{{1, "a.txt", "hello", -1}, {2, "b.txt", "xy", 50}, {3, "c.txt", "z", -1}}, 0, nil)
	out := t.TempDir()
	code, stdout, errs := h.run("story", "files", "5818", "--out", out)
	if code != 1 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	var l []map[string]any
	if err := json.Unmarshal([]byte(stdout), &l); err != nil || len(l) != 1 || l[0]["id"] != float64(1) {
		t.Errorf("stdout %s: %v", stdout, err)
	}
	if n := entries(t, out); len(n) != 1 || n[0] != "a.txt" {
		t.Errorf("files %v", n)
	}
}

func TestFilesNameClean(t *testing.T) {
	h := filesHarness(t, []fileSpec{
		{1, "../../etc/x", "1", -1}, {2, "a.txt", "2", -1}, {3, "a.txt", "3", -1}, {4, "..", "4", -1},
	}, 0, nil)
	out := filepath.Join(t.TempDir(), "sub")
	code, _, errs := h.run("story", "files", "5818", "--out", out)
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	got := entries(t, out)
	want := []string{"a-3.txt", "a.txt", "file-4", "x"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("files %v, want %v", got, want)
	}
}

func TestFilesOutMissing(t *testing.T) {
	h := newHarness(t, nil, "", nil)
	if code, _, errs := h.run("story", "files", "5818"); code != 2 {
		t.Errorf("code %d, stderr %s", code, errs)
	}
}

func TestFilesOutNotDir(t *testing.T) {
	h := filesHarness(t, []fileSpec{{1, "a.txt", "1", -1}}, 0, nil)
	f := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(f, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if code, _, errs := h.run("story", "files", "5818", "--out", f); code != 2 {
		t.Errorf("code %d, stderr %s", code, errs)
	}
}

func TestFilesRedirectDropsToken(t *testing.T) {
	var hdr http.Header
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hdr = r.Header.Clone()
		_, _ = w.Write([]byte("hi"))
	}))
	t.Cleanup(other.Close)
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, strings.Replace(other.URL, "127.0.0.1", "localhost", 1), http.StatusFound)
	}))
	t.Cleanup(host.Close)
	h := newHarness(t, map[string]http.HandlerFunc{
		"GET " + filesPath: jsonHandler(listBody(map[string]any{
			"id": 1, "name": "a.txt", "content_type": "text/plain", "size": 2, "url": host.URL,
		})),
	}, "", searchEnv)
	code, _, errs := h.run("story", "files", "5818", "--out", t.TempDir())
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	if hdr.Get("Shortcut-Token") != "" || hdr.Get("Authorization") != "" {
		t.Errorf("headers after redirect %v", hdr)
	}
}
