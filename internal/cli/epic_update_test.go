package cli_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
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
		_ = json.Unmarshal(b, body)
		jsonHandler(jsonOf(map[string]any{"entity": epicFixture()}))(w, r)
	})
	return newHarness(t, routes, stdin, map[string]string{"SCUT_TOKEN": updateAuth})
}

func TestEpicUpdateAllFlags(t *testing.T) {
	var c calls
	var body map[string]any
	h := epicUpdateHarness(t, &c, &body, "from stdin")
	code, out, errs := h.run("epic", "update", "Big epic", "--name", "New", "--state", "In Progress",
		"--add-owner", "@me", "--remove-owner", "gone", "--description-file", "-", "--archive")
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	want := `{"archived":true,"description":"from stdin","epic_state_id":500000011,"name":"New","owner_ids_add":["u1"],"owner_ids_remove":["u2"]}`
	if jsonOf(body) != want {
		t.Errorf("body %s, want %s", jsonOf(body), want)
	}
	if m := decode(t, out); m["id"] != float64(5387) || m["description"] != "Epic text" {
		t.Errorf("stdout %s", out)
	}
}

// Each flag alone is a change, and sends only its key.
func TestEpicUpdateOneFlag(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--name", "New"}, `{"name":"New"}`},
		{[]string{"--state", "To Do"}, `{"epic_state_id":500000010}`},
		{[]string{"--add-owner", "bruno"}, `{"owner_ids_add":["u1"]}`},
		{[]string{"--remove-owner", "gone"}, `{"owner_ids_remove":["u2"]}`},
		{[]string{"--description-file", "-"}, `{"description":"from stdin"}`},
		{[]string{"--archive"}, `{"archived":true}`},
		{[]string{"--unarchive"}, `{"archived":false}`},
	} {
		var c calls
		var body map[string]any
		h := epicUpdateHarness(t, &c, &body, "from stdin")
		code, _, errs := h.run(append([]string{"epic", "update", "5387"}, tc.args...)...)
		if code != 0 || jsonOf(body) != tc.want {
			t.Errorf("%v: code %d, body %s, stderr %s", tc.args, code, jsonOf(body), errs)
		}
	}
}

func TestEpicUpdateBadNameNoPatch(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"5387", "--add-owner", "nobody"}, "nobody"},
		{[]string{"5387", "--remove-owner", "nobody"}, "nobody"},
		{[]string{"5387", "--state", "in progress"}, "In Progress"},
		{[]string{"No such epic"}, "Big epic"},
	} {
		var c calls
		var body map[string]any
		h := epicUpdateHarness(t, &c, &body, "")
		code, _, errs := h.run(append(append([]string{"epic", "update"}, tc.args...), "--name", "x")...)
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
		{[]string{"5387", "--description-file", "/nonexistent/d.md"}, ""},
		{[]string{"5387", "5388", "--name", "x"}, ""},
	} {
		var c calls
		var body map[string]any
		h := epicUpdateHarness(t, &c, &body, tc.stdin)
		if code, _, errs := h.run(append([]string{"epic", "update"}, tc.args...)...); code != 2 || c.count(epicPath) != 0 {
			t.Errorf("%v: code %d, calls %v, stderr %s", tc.args, code, c.paths, errs)
		}
	}
}
