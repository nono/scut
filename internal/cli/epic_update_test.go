package cli_test

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nono/scut/internal/testapi"
)

const epicPath = "/api/v4/acme/epics/5387"

// epicUpdateHarness serves the lookup routes, whoami, the epic states, the
// epic search and an epic PATCH that records the body in *body.
func epicUpdateHarness(t *testing.T, c *calls, body *map[string]any, stdin string) *harness {
	t.Helper()
	routes := lookupRoutes()
	routes["GET /api/v4/whoami"] = jsonHandler(whoamiBody)
	routes["GET /api/v4/acme/epic-workflow/states"] = jsonHandler(listBody(
		map[string]any{"id": 500000010, "name": "To Do"}, map[string]any{"id": 500000011, "name": "In Progress"}))
	routes["GET /api/v4/acme/epics/search"] = jsonHandler(listBody(epicFixture()))
	routes["PATCH "+epicPath] = c.wrap(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		*body = nil
		_ = json.Unmarshal(b, body)
		jsonHandler(jsonOf(map[string]any{"entity": epicFixture()}))(w, r)
	})
	return newHarness(t, routes, stdin, map[string]string{"SCUT_TOKEN": updateAuth})
}

func TestEpicUpdateOwnerDescription(t *testing.T) {
	var c calls
	var body map[string]any
	h := epicUpdateHarness(t, &c, &body, "")
	f := filepath.Join(t.TempDir(), "d.md")
	if err := os.WriteFile(f, []byte("new text\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, errs := h.run("epic", "update", "5387", "--add-owner", "@me", "--description-file", f)
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	if jsonOf(body) != `{"description":"new text\n","owner_ids_add":["u1"]}` {
		t.Errorf("body %s", jsonOf(body))
	}
	m := decode(t, out)
	if m["id"] != float64(5387) || m["description"] != "Epic text" {
		t.Errorf("stdout %s", out)
	}
}

func TestEpicUpdateAllFlags(t *testing.T) {
	var c calls
	var body map[string]any
	h := epicUpdateHarness(t, &c, &body, "from stdin")
	code, _, errs := h.run("epic", "update", "Big epic", "--name", "New", "--state", "In Progress",
		"--remove-owner", "gone", "--remove-owner", "b@example.com", "--description-file", "-", "--archive")
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	want := `{"archived":true,"description":"from stdin","epic_state_id":500000011,"name":"New","owner_ids_remove":["u2","u1"]}`
	if jsonOf(body) != want {
		t.Errorf("body %s, want %s", jsonOf(body), want)
	}
	code, _, errs = h.run("epic", "update", "5387", "--unarchive")
	if code != 0 || jsonOf(body) != `{"archived":false}` {
		t.Errorf("code %d, body %s, stderr %s", code, jsonOf(body), errs)
	}
}

func TestEpicUpdateBadNameNoPatch(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--add-owner", "nobody"}, "nobody"},
		{[]string{"--state", "in progress"}, "In Progress"},
	} {
		var c calls
		var body map[string]any
		h := epicUpdateHarness(t, &c, &body, "")
		code, _, errs := h.run(append([]string{"epic", "update", "5387", "--name", "x"}, tc.args...)...)
		if code != 3 || c.count(epicPath) != 0 || !strings.Contains(errs, tc.want) {
			t.Errorf("%v: code %d, calls %v, stderr %s", tc.args, code, c.paths, errs)
		}
	}
}

func TestEpicUpdateUsage(t *testing.T) {
	for _, tc := range []struct {
		args  []string
		stdin string
	}{
		{[]string{"5387"}, ""},
		{[]string{"5387", "--archive", "--unarchive"}, ""},
		{[]string{"5387", "--description-file", "-"}, " \n"},
		{[]string{"5387", "5388", "--name", "x"}, ""},
		{[]string{" ", "--name", "x"}, ""},
	} {
		var c calls
		var body map[string]any
		h := epicUpdateHarness(t, &c, &body, tc.stdin)
		if code, _, errs := h.run(append([]string{"epic", "update"}, tc.args...)...); code != 2 || c.count(epicPath) != 0 {
			t.Errorf("%v: code %d, calls %v, stderr %s", tc.args, code, c.paths, errs)
		}
	}
}

func TestEpicUpdateContract(t *testing.T) {
	h := newHarness(t, nil, "", map[string]string{"SCUT_TOKEN": updateAuth})
	h.env.APIBase = testapi.Prism(t)
	f := filepath.Join(t.TempDir(), "d.md")
	if err := os.WriteFile(f, []byte("text\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"--add-owner", "@me", "--description-file", f},
		// Prism gives "string" as the name of each epic state.
		{"--name", "x", "--archive", "--state", "string", "--remove-owner", "00000000-0000-4000-8000-0000000000ab"},
	} {
		code, out, errs := h.run(append([]string{"epic", "update", "5387"}, args...)...)
		if code != 0 {
			t.Fatalf("%v: code %d, stderr %s", args, code, errs)
		}
		if _, ok := decode(t, out)["id"]; !ok {
			t.Errorf("%v: no id: %s", args, out)
		}
	}
}
