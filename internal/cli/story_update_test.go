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

const (
	updPath    = "/api/v4/acme/stories/5818"
	areaField  = "0f0f0f0f-08b2-4eb1-b29f-703a73481c90"
	areaAPI    = "0f0f0f0f-044e-4240-8de7-9a49d9f3048b"
	updateAuth = "sct_rw_acme_s3cret"
)

// updateHarness serves the lookup routes plus a story PATCH that records the
// body. extra overrides or adds routes.
func updateHarness(t *testing.T, c *calls, body *map[string]any, extra map[string]http.HandlerFunc) *harness {
	t.Helper()
	routes := lookupRoutes()
	routes["GET /api/v4/acme/custom-fields"] = jsonHandler(listBody(map[string]any{
		"id": areaField, "name": "Technical Area", "field_type": "enum",
		"values": []any{map[string]any{"id": areaAPI, "value": "API"}},
	}))
	routes["PATCH "+updPath] = c.wrap(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		*body = nil
		_ = json.Unmarshal(b, body)
		jsonHandler(jsonOf(map[string]any{"entity": storyFixture(t)}))(w, r)
	})
	for k, v := range extra {
		routes[k] = v
	}
	return newHarness(t, routes, "", map[string]string{"SCUT_TOKEN": updateAuth})
}

func TestUpdateOnlyGivenFields(t *testing.T) {
	var c calls
	var body map[string]any
	h := updateHarness(t, &c, &body, nil)
	code, out, errs := h.run("story", "update", "5818", "--name", "x")
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	if jsonOf(body) != `{"name":"x"}` {
		t.Errorf("body %s", jsonOf(body))
	}
	if c.count("/api/v4/acme/stories/5818/comments") != 0 {
		t.Errorf("comment call: %v", c.paths)
	}
	m := decode(t, out)
	if m["id"] != float64(5818) || m["description"] == nil {
		t.Errorf("out %s", out)
	}
}

func TestUpdateOwnersAddRemove(t *testing.T) {
	var c calls
	var body map[string]any
	h := updateHarness(t, &c, &body, nil)
	code, _, errs := h.run("story", "update", "5818", "--add-owner", "bruno", "--add-owner", "00000000-0000-4000-8000-000000000009", "--remove-owner", "gone")
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	if jsonOf(body) != `{"owner_ids_add":["u1","00000000-0000-4000-8000-000000000009"],"owner_ids_remove":["u2"]}` {
		t.Errorf("body %s", jsonOf(body))
	}
	if _, ok := body["owner_ids"]; ok {
		t.Errorf("owner_ids sent")
	}
}

func TestUpdateRequesterEpicArchive(t *testing.T) {
	var c calls
	var body map[string]any
	h := updateHarness(t, &c, &body, map[string]http.HandlerFunc{
		"GET /api/v4/acme/epics": jsonHandler(listBody(map[string]any{"id": 12, "name": "Epic"})),
	})
	code, _, errs := h.run("story", "update", "5818", "--requester", "bruno", "--epic", "12", "--archive")
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	if jsonOf(body) != `{"archived":true,"epic_id":12,"requester_id":"u1"}` {
		t.Errorf("body %s", jsonOf(body))
	}
	code, _, errs = h.run("story", "update", "5818", "--unarchive")
	if code != 0 || jsonOf(body) != `{"archived":false}` {
		t.Errorf("code %d, body %s, stderr %s", code, jsonOf(body), errs)
	}
}

func TestUpdateFieldAdd(t *testing.T) {
	var c calls
	var body map[string]any
	h := updateHarness(t, &c, &body, nil)
	code, _, errs := h.run("story", "update", "5818", "--field", "Technical Area=API")
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	want := `{"custom_field_values_add":[{"field_id":"` + areaField + `","value_id":"` + areaAPI + `"}]}`
	if jsonOf(body) != want {
		t.Errorf("body %s, want %s", jsonOf(body), want)
	}
}

func TestUpdateFieldBadForm(t *testing.T) {
	var c calls
	var body map[string]any
	h := updateHarness(t, &c, &body, nil)
	if code, _, _ := h.run("story", "update", "5818", "--field", "Technical Area"); code != 2 {
		t.Errorf("code %d, want 2", code)
	}
	if c.total() != 0 {
		t.Errorf("calls %v", c.paths)
	}
}

func TestUpdateNothing(t *testing.T) {
	h := newHarness(t, nil, "", nil)
	if code, _, _ := h.run("story", "update", "5818"); code != 2 {
		t.Errorf("code %d, want 2", code)
	}
}

func TestUpdateArchiveConflict(t *testing.T) {
	h := newHarness(t, nil, "", nil)
	if code, _, _ := h.run("story", "update", "5818", "--archive", "--unarchive"); code != 2 {
		t.Errorf("code %d, want 2", code)
	}
}

func TestUpdateBadNameNoPatch(t *testing.T) {
	var c calls
	var body map[string]any
	h := updateHarness(t, &c, &body, nil)
	code, _, errs := h.run("story", "update", "5818", "--name", "x", "--add-owner", "nobody")
	if code != 3 || c.count(updPath) != 0 {
		t.Errorf("code %d, calls %v, stderr %s", code, c.paths, errs)
	}
	if !strings.Contains(errs, "nobody") {
		t.Errorf("stderr %s", errs)
	}
}

func TestUpdateDescriptionFile(t *testing.T) {
	var c calls
	var body map[string]any
	h := updateHarness(t, &c, &body, nil)
	f := filepath.Join(t.TempDir(), "d.md")
	if err := os.WriteFile(f, []byte("new text\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, _, errs := h.run("story", "update", "5818", "--description-file", f)
	if code != 0 || body["description"] != "new text\n" || len(body) != 1 {
		t.Errorf("code %d, body %s, stderr %s", code, jsonOf(body), errs)
	}
}

func stateWorkflowGet(c *calls, wf int) http.HandlerFunc {
	return c.wrap(jsonHandler(jsonOf(map[string]any{"entity": map[string]any{
		"id": 5818, "workflow": map[string]any{"id": wf, "name": "W"}}})))
}

func twoToDo() http.HandlerFunc {
	return jsonHandler(listBody(
		map[string]any{"id": 5, "name": "Standard", "workflow_states": nestedList(map[string]any{"id": 7, "name": "To Do"})},
		map[string]any{"id": 6, "name": "Kanban", "workflow_states": nestedList(map[string]any{"id": 9, "name": "To Do"})},
	))
}

func TestUpdateStateUsesStoryWorkflow(t *testing.T) {
	var c, g calls
	var body map[string]any
	h := updateHarness(t, &c, &body, map[string]http.HandlerFunc{
		"GET " + updPath:             stateWorkflowGet(&g, 6),
		"GET /api/v4/acme/workflows": twoToDo(),
	})
	code, _, errs := h.run("story", "update", "5818", "--state", "To Do")
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	if jsonOf(body) != `{"workflow_state_id":9}` {
		t.Errorf("body %s", jsonOf(body))
	}
	if g.count(updPath) != 1 || g.param(updPath, "fields") != "workflow" {
		t.Errorf("get calls %v, fields %q", g.paths, g.param(updPath, "fields"))
	}
}

func TestUpdateStateWithWorkflowNoGet(t *testing.T) {
	var c, g calls
	var body map[string]any
	h := updateHarness(t, &c, &body, map[string]http.HandlerFunc{
		"GET " + updPath:             stateWorkflowGet(&g, 6),
		"GET /api/v4/acme/workflows": twoToDo(),
	})
	code, _, errs := h.run("story", "update", "5818", "--state", "To Do", "--workflow", "Standard")
	if code != 0 || jsonOf(body) != `{"workflow_state_id":7}` || g.total() != 0 {
		t.Errorf("code %d, body %s, gets %v, stderr %s", code, jsonOf(body), g.paths, errs)
	}
}

func TestUpdateContract(t *testing.T) {
	h := newHarness(t, nil, "", map[string]string{"SCUT_TOKEN": updateAuth})
	h.env.APIBase = testapi.Prism(t)
	code, out, errs := h.run("story", "update", "5818", "--name", "x", "--archive")
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	if _, ok := decode(t, out)["id"]; !ok {
		t.Errorf("no id: %s", out)
	}
}
