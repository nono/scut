package cli_test

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nono/scut/internal/testapi"
)

const (
	testDoc     = "1b4e28ba-2fa1-41d2-883f-0016d3cca427"
	testDocPath = "/api/v4/acme/documents/" + testDoc
	// testDocURL is a doc URL of the web app: base64 of `"Doc":#uuid "<testDoc>"`.
	testDocURL = "https://app.shortcut.com/acme/write/IkRvYyI6I3V1aWQgIjFiNGUyOGJhLTJmYTEtNDFkMi04ODNmLTAwMTZkM2NjYTQyNyI="
)

func docFixture() map[string]any {
	return map[string]any{
		"id": testDoc, "title": "Picking spec", "archived": false, "entity_type": "doc",
		"creator":    map[string]any{"entity_type": "member:slim", "id": testMember, "name": "Test User", "uri": "https://x/m"},
		"created_at": "2026-09-24T14:52:50Z", "updated_at": "2026-09-25T08:00:00Z",
		"followers": nestedList(),
		"uri":       "https://api.app.shortcut.com/api/v4/acme/documents/" + testDoc,
	}
}

// docRoute serves the doc fixture, with content in the asked format. As the
// live API, it sends only the keys in fields when fields is set.
func docRoute(content *string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		d := docFixture()
		q := r.URL.Query()
		if f := q.Get("content_format"); f != "" {
			d["content_"+f] = content
		}
		if f := q.Get("fields"); f != "" {
			keep := map[string]any{}
			for _, k := range strings.Split(f, ",") {
				if v, ok := d[k]; ok {
					keep[k] = v
				}
			}
			d = keep
		}
		jsonHandler(jsonOf(map[string]any{"entity": d}))(w, r)
	}
}

func ptr(s string) *string { return &s }

func TestDocView(t *testing.T) {
	var c calls
	h := newHarness(t, map[string]http.HandlerFunc{
		"GET " + testDocPath:       c.wrap(docRoute(ptr("# Spec\n\nText"))),
		"GET /api/v4/acme/members": membersRoute(),
	}, "", searchEnv)
	code, out, errs := h.run("doc", "view", testDocURL)
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	m := decode(t, out)
	cr, _ := m["creator"].(map[string]any)
	if m["id"] != testDoc || m["title"] != "Picking spec" || m["archived"] != false ||
		cr["mention_name"] != "testuser" || m["created_at"] != "2026-09-24T14:52:50Z" ||
		m["updated_at"] != "2026-09-25T08:00:00Z" || m["content_markdown"] != "# Spec\n\nText" {
		t.Errorf("stdout %s", out)
	}
	for _, k := range []string{"content_html", "followers", "uri", "content_file"} {
		if _, ok := m[k]; ok {
			t.Errorf("%s in output: %s", k, out)
		}
	}
	// The API leaves out the content when content_format is not sent.
	if got := c.param(testDocPath, "content_format"); got != "markdown" {
		t.Errorf("content_format %q", got)
	}
}

func TestDocViewHTML(t *testing.T) {
	h := newHarness(t, map[string]http.HandlerFunc{
		"GET " + testDocPath:       docRoute(ptr("<h1>Spec</h1>")),
		"GET /api/v4/acme/members": membersRoute(),
	}, "", searchEnv)
	code, out, errs := h.run("doc", "view", testDoc, "--format", "html")
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	m := decode(t, out)
	if m["content_html"] != "<h1>Spec</h1>" {
		t.Errorf("stdout %s", out)
	}
	if _, ok := m["content_markdown"]; ok {
		t.Errorf("content_markdown in output: %s", out)
	}
}

// An empty doc has null content: the key is still there, so the output does
// not look like a doc read with no content.
func TestDocViewNullContent(t *testing.T) {
	h := newHarness(t, map[string]http.HandlerFunc{
		"GET " + testDocPath:       docRoute(nil),
		"GET /api/v4/acme/members": membersRoute(),
	}, "", searchEnv)
	code, out, errs := h.run("doc", "view", testDoc)
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	if m := decode(t, out); m["content_markdown"] != "" {
		t.Errorf("stdout %s", out)
	}
}

// A reply with no content key, or with no doc, is an error: it must not
// replace the --out file with an empty one.
func TestDocViewNoContentInReply(t *testing.T) {
	for name, body := range map[string]string{
		"no content key": jsonOf(map[string]any{"entity": docFixture()}),
		"null entity":    `{"entity":null}`,
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, map[string]http.HandlerFunc{
				"GET " + testDocPath:       jsonHandler(body),
				"GET /api/v4/acme/members": membersRoute(),
			}, "", searchEnv)
			path := filepath.Join(t.TempDir(), "spec.md")
			if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
				t.Fatal(err)
			}
			code, out, errs := h.run("doc", "view", testDoc, "--out", path)
			if code != 1 || out != "" || errs == "" {
				t.Errorf("code %d, stdout %q, stderr %s", code, out, errs)
			}
			if b, _ := os.ReadFile(path); string(b) != "old" {
				t.Errorf("file %q", b)
			}
		})
	}
}

func TestDocViewOut(t *testing.T) {
	h := newHarness(t, map[string]http.HandlerFunc{
		"GET " + testDocPath:       docRoute(ptr("# Spec\n")),
		"GET /api/v4/acme/members": membersRoute(),
	}, "", searchEnv)
	dir := t.TempDir()
	path := filepath.Join(dir, "spec.md")
	if err := os.WriteFile(path, []byte("old text that is longer"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, errs := h.run("doc", "view", testDoc, "--out", path)
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	m := decode(t, out)
	if m["content_file"] != path || m["title"] != "Picking spec" {
		t.Errorf("stdout %s", out)
	}
	if _, ok := m["content_markdown"]; ok {
		t.Errorf("content in output: %s", out)
	}
	if b, err := os.ReadFile(path); err != nil || string(b) != "# Spec\n" {
		t.Errorf("file %q, %v", b, err)
	}
}

func TestDocViewOutError(t *testing.T) {
	h := newHarness(t, map[string]http.HandlerFunc{
		"GET " + testDocPath:       docRoute(ptr("# Spec\n")),
		"GET /api/v4/acme/members": membersRoute(),
	}, "", searchEnv)
	code, out, errs := h.run("doc", "view", testDoc, "--out", filepath.Join(t.TempDir(), "no", "spec.md"))
	if code != 1 || out != "" || !strings.Contains(errs, "spec.md") {
		t.Errorf("code %d, stdout %q, stderr %s", code, out, errs)
	}
}

func TestDocViewByTitle(t *testing.T) {
	var c calls
	h := newHarness(t, map[string]http.HandlerFunc{
		"GET /api/v4/acme/documents/search": c.wrap(jsonHandler(listBody(docFixture()))),
		"GET " + testDocPath:                docRoute(ptr("x")),
		"GET /api/v4/acme/members":          membersRoute(),
	}, "", searchEnv)
	if code, _, errs := h.run("doc", "view", "Picking spec"); code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	if got := c.param("/api/v4/acme/documents/search", "query"); got != "Picking spec" {
		t.Errorf("query %q", got)
	}
}

func TestDocViewErrors(t *testing.T) {
	h := newHarness(t, map[string]http.HandlerFunc{
		"GET " + testDocPath: func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"message":"not found"}`))
		},
	}, "", searchEnv)
	for _, c := range []struct {
		args []string
		code int
	}{
		{[]string{"doc", "view", testDoc}, 3},
		{[]string{"doc", "view", testDoc, "--format", "pdf"}, 2},
		{[]string{"doc", "view", "https://app.shortcut.com/acme/write/bm90IGEgZG9j"}, 2},
		{[]string{"doc", "view"}, 2},
		{[]string{"doc", "search"}, 2},
		{[]string{"doc", "search", "x", "--limit", "0"}, 2},
	} {
		if code, out, errs := h.run(c.args...); code != c.code || out != "" {
			t.Errorf("%v: code %d, stdout %q, stderr %s; want code %d", c.args, code, out, errs, c.code)
		}
	}
}

func TestDocSearch(t *testing.T) {
	var c calls
	h := newHarness(t, map[string]http.HandlerFunc{
		"GET /api/v4/acme/documents/search": c.wrap(jsonHandler(listBody(docFixture()))),
		"GET /api/v4/acme/members":          membersRoute(),
	}, "", searchEnv)
	code, out, errs := h.run("doc", "search", "picking")
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	if got := c.param("/api/v4/acme/documents/search", "query"); got != "picking" {
		t.Errorf("query %q", got)
	}
	if got := c.param("/api/v4/acme/documents/search", "fields"); got != "id,title,archived,creator,created_at,updated_at" {
		t.Errorf("fields %q", got)
	}
	l := decodeList(t, out)
	if len(l) != 1 || l[0]["id"] != testDoc || l[0]["title"] != "Picking spec" {
		t.Errorf("stdout %s", out)
	}
	if _, ok := l[0]["content_markdown"]; ok {
		t.Errorf("content in search output: %s", out)
	}
}

func TestDocHuman(t *testing.T) {
	h := newHarness(t, map[string]http.HandlerFunc{
		"GET " + testDocPath:       docRoute(ptr("# Spec\n\nText")),
		"GET /api/v4/acme/members": membersRoute(),
	}, "", searchEnv)
	code, out, errs := h.run("doc", "view", testDoc, "--human")
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	want := "Picking spec (doc " + testDoc + ")\nCreator: @testuser\nUpdated: 2026-09-25T08:00:00Z\n\n# Spec\n\nText\n"
	if out != want {
		t.Errorf("stdout %q, want %q", out, want)
	}
}

func TestDocContract(t *testing.T) {
	h := newHarness(t, nil, "", searchEnv)
	h.env.APIBase = testapi.Prism(t)
	for _, args := range [][]string{{"doc", "view", testDoc}, {"doc", "view", testDoc, "--format", "html"}, {"doc", "search", "x"}} {
		if code, _, errs := h.run(args...); code != 0 {
			t.Errorf("%v: code %d, stderr %s", args, code, errs)
		}
	}
}
