package cli_test

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/nono/scut/internal/testapi"
)

func TestCommentCreate(t *testing.T) {
	var body []byte
	h := newHarness(t, map[string]http.HandlerFunc{
		"POST /api/v4/acme/stories/5818/comments": func(w http.ResponseWriter, r *http.Request) {
			body, _ = io.ReadAll(r.Body)
			jsonHandler(jsonOf(map[string]any{"entity": comment(9, "from file\n", false)}))(w, r)
		},
		"GET /api/v4/acme/members": membersRoute(),
	}, "", searchEnv)
	f := filepath.Join(t.TempDir(), "c.md")
	if err := os.WriteFile(f, []byte("from file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, errs := h.run("story", "comment", "sc-5818", "--body-file", f)
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil || got["text"] != "from file\n" || len(got) != 1 {
		t.Errorf("body %q", body)
	}
	m := decode(t, out)
	if m["id"] != float64(9) || m["text"] != "from file\n" || m["author"].(map[string]any)["mention_name"] != "testuser" {
		t.Errorf("stdout %s", out)
	}
}

func TestCommentStdinAndErrors(t *testing.T) {
	h := newHarness(t, nil, " \n", searchEnv)
	if code, _, _ := h.run("story", "comment", "5818", "--body-file", "-"); code != 2 {
		t.Errorf("empty: code %d", code)
	}
	if code, _, _ := h.run("story", "comment", "5818"); code != 2 {
		t.Errorf("no flag: code %d", code)
	}
	if code, _, _ := h.run("story", "comment", "--body-file", "-"); code != 2 {
		t.Errorf("no story: code %d", code)
	}
}

func TestCommentContract(t *testing.T) {
	h := newHarness(t, nil, "hello\n", searchEnv)
	h.env.APIBase = testapi.Prism(t)
	code, out, errs := h.run("story", "comment", "5818", "--body-file", "-")
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	if _, ok := decode(t, out)["id"]; !ok {
		t.Errorf("no id: %s", out)
	}
}
