package cli_test

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nono/scut/internal/testapi"
)

func listBody(items ...any) string {
	if items == nil {
		items = []any{} // the API sends [], never null
	}
	b, _ := json.Marshal(map[string]any{
		"current_page": 1, "current_items": len(items), "total_items": len(items), "total_pages": 1, "entities": items,
	})
	return string(b)
}

func nestedList(items ...any) map[string]any {
	return map[string]any{"current_items": len(items), "total_items": len(items), "entities": items}
}

func lookupRoutes() map[string]http.HandlerFunc {
	values := []any{map[string]any{"id": "0f0f0f0f-08aa-4af3-b3d0-d03dc9e0846c", "value": "Back end"}}
	for i := 1; i < 11; i++ {
		values = append(values, map[string]any{"id": "v" + string(rune('a'+i)), "value": "Area " + string(rune('A'+i))})
	}
	return map[string]http.HandlerFunc{
		"GET /api/v4/acme/custom-fields": jsonHandler(listBody(map[string]any{
			"id": "0f0f0f0f-08b2-4eb1-b29f-703a73481c90", "name": "Technical Area", "field_type": "enum", "values": values,
		})),
		"GET /api/v4/acme/teams": jsonHandler(listBody(map[string]any{
			"id": "t1", "name": "Software Dev", "mention_name": "software-dev",
			"default_workflow": map[string]any{"id": 5, "name": "Standard"},
			"workflows":        nestedList(),
		})),
		"GET /api/v4/acme/members": jsonHandler(listBody(
			map[string]any{"id": "u1", "name": "Bruno", "mention_name": "bruno", "email_address": "b@example.com"},
			map[string]any{"id": "u2", "name": "Gone", "mention_name": "gone", "email_address": nil, "disabled": true},
		)),
		"GET /api/v4/acme/workflows": jsonHandler(listBody(
			map[string]any{"id": 5, "name": "Standard", "default_workflow_state": map[string]any{"id": 7, "name": "To Do"}, "workflow_states": nestedList(
				map[string]any{"id": 7, "name": "To Do"}, map[string]any{"id": 8, "name": "Done"})},
			map[string]any{"id": 6, "name": "Kanban", "default_workflow_state": map[string]any{"id": 9, "name": "Ready"}, "workflow_states": nestedList(
				map[string]any{"id": 9, "name": "Ready"})},
		)),
	}
}

func lookupHarness(t *testing.T) *harness {
	return newHarness(t, lookupRoutes(), "", map[string]string{"SCUT_TOKEN": "sct_rw_acme_s3cret"})
}

func decodeList(t *testing.T, s string) []map[string]any {
	t.Helper()
	var l []map[string]any
	if err := json.Unmarshal([]byte(s), &l); err != nil {
		t.Fatalf("not a JSON array: %q: %v", s, err)
	}
	return l
}

func TestFieldValues(t *testing.T) {
	code, out, errs := lookupHarness(t).run("field", "values", "Technical Area")
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	l := decodeList(t, out)
	if len(l) != 11 {
		t.Fatalf("%d values, want 11", len(l))
	}
	if l[0]["id"] != "0f0f0f0f-08aa-4af3-b3d0-d03dc9e0846c" || l[0]["name"] != "Back end" || len(l[0]) != 2 {
		t.Errorf("first value %v", l[0])
	}
}

func TestFieldValuesUnknown(t *testing.T) {
	code, _, errs := lookupHarness(t).run("field", "values", "Nope")
	if code != 3 {
		t.Fatalf("code %d, want 3; stderr %s", code, errs)
	}
	if e := decode(t, errs); e["valid"] == nil {
		t.Errorf("no valid in %s", errs)
	}
}

func TestFieldList(t *testing.T) {
	code, out, errs := lookupHarness(t).run("field", "list")
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	l := decodeList(t, out)
	if len(l) != 1 || l[0]["name"] != "Technical Area" || l[0]["type"] != "enum" {
		t.Errorf("out %s", out)
	}
}

func TestTeamList(t *testing.T) {
	code, out, errs := lookupHarness(t).run("team", "list")
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	l := decodeList(t, out)
	if len(l) != 1 || l[0]["id"] != "t1" || l[0]["name"] != "Software Dev" || l[0]["mention_name"] != "software-dev" || len(l[0]) != 3 {
		t.Errorf("out %s", out)
	}
}

func TestMemberList(t *testing.T) {
	code, out, errs := lookupHarness(t).run("member", "list")
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	l := decodeList(t, out)
	if len(l) != 2 || l[0]["email"] != "b@example.com" || l[0]["mention_name"] != "bruno" {
		t.Fatalf("out %s", out)
	}
	if l[1]["disabled"] != true || l[0]["disabled"] != nil {
		t.Errorf("disabled flags wrong: %s", out)
	}
}

func TestWorkflowList(t *testing.T) {
	code, out, errs := lookupHarness(t).run("workflow", "list")
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	l := decodeList(t, out)
	ds, _ := l[0]["default_state"].(map[string]any)
	if len(l) != 2 || l[0]["name"] != "Standard" || ds["id"] != float64(7) || ds["name"] != "To Do" {
		t.Errorf("out %s", out)
	}
}

func TestWorkflowStatesAll(t *testing.T) {
	code, out, errs := lookupHarness(t).run("workflow", "states")
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	l := decodeList(t, out)
	if len(l) != 3 {
		t.Fatalf("%d states, want 3: %s", len(l), out)
	}
	if l[0]["workflow"] != "Standard" || l[2]["workflow"] != "Kanban" || l[2]["name"] != "Ready" {
		t.Errorf("out %s", out)
	}
}

func TestWorkflowStatesOne(t *testing.T) {
	h := lookupHarness(t)
	code, out, errs := h.run("workflow", "states", "Kanban")
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	if l := decodeList(t, out); len(l) != 1 || l[0]["id"] != float64(9) {
		t.Errorf("out %s", out)
	}
	code, _, errs = h.run("workflow", "states", "Nope")
	if code != 3 || decode(t, errs)["valid"] == nil {
		t.Errorf("code %d, stderr %s", code, errs)
	}
}

func TestCacheClear(t *testing.T) {
	h := lookupHarness(t)
	if code, _, errs := h.run("team", "list"); code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	dir := filepath.Join(h.env.Getenv("XDG_CACHE_HOME"), "scut", "acme")
	if m, _ := filepath.Glob(filepath.Join(dir, "*.json")); len(m) == 0 {
		t.Fatalf("no cache files in %s", dir)
	}
	code, out, errs := h.run("cache", "clear")
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	if decode(t, out)["cleared"] != dir {
		t.Errorf("out %s", out)
	}
	// Only the marker of the clear is left.
	if ents, err := os.ReadDir(dir); err != nil || len(ents) != 1 || ents[0].Name() != "cleared-at" {
		t.Errorf("cache dir after clear: %v, %v", ents, err)
	}
}

func TestCacheClearNeedsToken(t *testing.T) {
	h := newHarness(t, nil, "", nil)
	if code, _, _ := h.run("cache", "clear"); code != 4 {
		t.Errorf("code %d, want 4", code)
	}
}

func TestLookupContract(t *testing.T) {
	h := newHarness(t, nil, "", map[string]string{"SCUT_TOKEN": "sct_rw_acme_s3cret"})
	h.env.APIBase = testapi.Prism(t)
	run := func(args ...string) string {
		t.Helper()
		code, out, errs := h.run(args...)
		if code != 0 {
			t.Fatalf("%s: code %d, stderr %s", strings.Join(args, " "), code, errs)
		}
		return out
	}
	for _, c := range [][]string{{"team", "list"}, {"member", "list"}, {"workflow", "states"}} {
		run(c...)
	}
	if fields := decodeList(t, run("field", "list")); len(fields) > 0 {
		name, _ := fields[0]["name"].(string)
		run("field", "values", name)
	}
	if ws := decodeList(t, run("workflow", "list")); len(ws) > 0 {
		name, _ := ws[0]["name"].(string)
		run("workflow", "states", name)
	}
	run("cache", "clear")
}
