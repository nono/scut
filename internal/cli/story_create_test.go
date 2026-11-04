package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/nono/scut/internal/cli"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nono/scut/internal/testapi"
)

const (
	createPath = "/api/v4/acme/stories"
	uuidV4     = `^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`
)

// createFake records the body of each create and answers it from a script.
type createFake struct {
	mu     sync.Mutex
	bodies []map[string]any
	// statuses[i] is the status of create number i; 201 when out of range.
	statuses   []int
	retryAfter string   // Retry-After header of each non-201 answer
	lists      []string // list response bodies, one per list call; empty list when out of range
	nList      int
}

func (f *createFake) create(t *testing.T, c *calls) http.HandlerFunc {
	return c.wrap(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		f.mu.Lock()
		i := len(f.bodies)
		f.bodies = append(f.bodies, m)
		status := 201
		if i < len(f.statuses) {
			status = f.statuses[i]
		}
		f.mu.Unlock()
		if status != 201 {
			w.Header().Set("Content-Type", "application/json")
			if f.retryAfter != "" {
				w.Header().Set("Retry-After", f.retryAfter)
			}
			w.WriteHeader(status)
			_, _ = io.WriteString(w, `{"message":"boom"}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(201)
		_, _ = io.WriteString(w, jsonOf(map[string]any{"entity": storyFixture(t)}))
	})
}

func (f *createFake) list(c *calls) http.HandlerFunc {
	return c.wrap(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		i := f.nList
		f.nList++
		f.mu.Unlock()
		body := listBody()
		if i < len(f.lists) {
			body = f.lists[i]
		}
		jsonHandler(body)(w, r)
	})
}

func (f *createFake) externalID(i int) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if i >= len(f.bodies) {
		return ""
	}
	s, _ := f.bodies[i]["external_id"].(string)
	return s
}

func createHarness(t *testing.T, f *createFake, c *calls, extra map[string]http.HandlerFunc) *harness {
	t.Helper()
	routes := lookupRoutes()
	routes["GET /api/v4/acme/labels"] = jsonHandler(listBody(map[string]any{"id": 31, "name": "bug-hunt"}))
	routes["GET /api/v4/acme/epics"] = jsonHandler(listBody(map[string]any{"id": 12, "name": "Epic"}))
	routes["GET /api/v4/acme/custom-fields"] = jsonHandler(listBody(
		map[string]any{"id": areaField, "name": "Technical Area", "field_type": "enum",
			"values": []any{map[string]any{"id": areaAPI, "value": "API"}}},
		map[string]any{"id": "f2", "name": "Squad", "field_type": "enum",
			"values": []any{map[string]any{"id": "v-red", "value": "Red"}, map[string]any{"id": "v-blue", "value": "Blue"}}},
	))
	routes["GET /api/v4/whoami"] = jsonHandler(whoamiBody)
	routes["POST "+createPath] = f.create(t, c)
	routes["GET "+createPath] = f.list(c)
	routes["GET "+createPath+"/5818"] = c.wrap(jsonHandler(jsonOf(map[string]any{"entity": storyFixture(t)})))
	for k, v := range extra {
		routes[k] = v
	}
	h := newHarness(t, routes, "", map[string]string{"SCUT_TOKEN": updateAuth})
	h.env.Sleep = func(time.Duration) {}
	return h
}

func TestCreateOneCall(t *testing.T) {
	var c calls
	f := &createFake{}
	h := createHarness(t, f, &c, nil)
	desc := filepath.Join(t.TempDir(), "d.md")
	if err := os.WriteFile(desc, []byte("body text\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, errs := h.run("story", "create", "--name", "New thing", "--type", "bug", "--team", "Software Dev",
		"--state", "To Do", "--epic", "12", "--parent", "5000", "--owner", "bruno", "--owner", "@me",
		"--requester", "bruno", "--label", "bug-hunt", "--estimate", "3", "--field", "Technical Area=API",
		"--description-file", desc, "--link", "blocked-by 4000", "--link", "blocks sc-4001",
		"--external-link", "https://example.com/a")
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	if c.count(createPath) != 1 || len(f.bodies) != 1 {
		t.Fatalf("calls %v", c.paths)
	}
	b := f.bodies[0]
	want := map[string]string{
		"name": `"New thing"`, "story_type": `"bug"`, "team_id": `"t1"`, "workflow_state_id": `7`, "epic_id": `12`,
		"parent_story_id": `5000`, "requester_id": `"u1"`, "label_ids": `[31]`, "estimate": `3`,
		"description": `"body text\n"`, "external_links": `["https://example.com/a"]`,
		"custom_field_values": `[{"field_id":"` + areaField + `","value_id":"` + areaAPI + `"}]`,
		"story_links":         `[{"subject_story_id":4000,"verb":"blocks"},{"object_story_id":4001,"verb":"blocks"}]`,
	}
	for k, v := range want {
		if got := jsonOf(b[k]); got != v {
			t.Errorf("%s = %s, want %s", k, got, v)
		}
	}
	owners, _ := b["owner_ids"].([]any)
	if len(owners) != 2 || owners[0] != "u1" {
		t.Errorf("owner_ids %v", b["owner_ids"])
	}
	if id, _ := b["external_id"].(string); !regexp.MustCompile(uuidV4).MatchString(id) {
		t.Errorf("external_id %q", id)
	}
	m := decode(t, out)
	if m["id"] != float64(5818) || m["description"] == nil {
		t.Errorf("out %s", out)
	}
}

func TestCreateDefaults(t *testing.T) {
	var c calls
	f := &createFake{}
	h := createHarness(t, f, &c, nil)
	write := func(p, s string) {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(h.env.Dir, ".git", "HEAD"), "")
	write(filepath.Join(h.env.Dir, ".scut.toml"), `team = "Software Dev"
state = "To Do"
owner = "bruno"
requester = "bruno"

[fields]
"Technical Area" = "API"
Squad = "Red"
`)
	code, _, errs := h.run("story", "create", "--name", "Plain", "--field", "Squad=Blue")
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	b := f.bodies[0]
	if b["team_id"] != "t1" || jsonOf(b["workflow_state_id"]) != "7" || b["requester_id"] != "u1" ||
		jsonOf(b["owner_ids"]) != `["u1"]` || b["story_type"] != "feature" {
		t.Errorf("body %s", jsonOf(b))
	}
	// The flag wins over the config for Squad; the config gives Technical Area.
	want := `[{"field_id":"f2","value_id":"v-blue"},{"field_id":"` + areaField + `","value_id":"` + areaAPI + `"}]`
	if got := jsonOf(b["custom_field_values"]); got != want {
		t.Errorf("custom_field_values %s, want %s", got, want)
	}
}

func listWith(ext string) string {
	return listBody(map[string]any{"id": 5818, "external_id": ext, "name": "x", "app_url": "https://app/5818"})
}

func TestCreateRetryFindsExisting(t *testing.T) {
	var c calls
	f := &createFake{statuses: []int{504}}
	h := createHarness(t, f, &c, map[string]http.HandlerFunc{
		"GET " + createPath: c.wrap(func(w http.ResponseWriter, r *http.Request) {
			jsonHandler(listWith(f.externalID(0)))(w, r)
		}),
	})
	code, out, errs := h.run("story", "create", "--name", "x", "--state", "To Do")
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	if f.count() != 1 {
		t.Errorf("%d creates, want 1", f.count())
	}
	if decode(t, out)["id"] != float64(5818) || c.count(createPath+"/5818") != 1 {
		t.Errorf("out %s, calls %v", out, c.paths)
	}
	q := c.param(createPath, "filter")
	if q != "include_archived" || c.param(createPath, "order_by") != "created_at" || c.param(createPath, "order_dir") != "desc" ||
		c.param(createPath, "limit") != "25" || c.param(createPath, "fields") != "id,external_id,name,app_url" {
		t.Errorf("list query %q", c.query[createPath])
	}
}

func (f *createFake) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.bodies)
}

func TestCreateRetryCreates(t *testing.T) {
	var c calls
	f := &createFake{statuses: []int{504, 201}}
	h := createHarness(t, f, &c, nil)
	code, _, errs := h.run("story", "create", "--name", "x", "--state", "To Do")
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	if f.count() != 2 || f.nList != 1 {
		t.Fatalf("creates %d, lists %d", f.count(), f.nList)
	}
	if f.externalID(0) == "" || f.externalID(0) != f.externalID(1) {
		t.Errorf("external ids %q, %q", f.externalID(0), f.externalID(1))
	}
}

func TestCreateGivesUp(t *testing.T) {
	var c calls
	f := &createFake{statuses: []int{504, 504, 504, 504, 504}}
	h := createHarness(t, f, &c, nil)
	code, _, errs := h.run("story", "create", "--name", "x", "--state", "To Do")
	if code != 5 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	e := decode(t, errs)
	sug, _ := e["suggestion"].(string)
	if !strings.Contains(sug, "may exist") || !strings.Contains(sug, f.externalID(0)) {
		t.Errorf("suggestion %q", sug)
	}
	if f.count() != 4 || f.nList != 4 {
		t.Errorf("%d creates, %d lists, want 4 and 4", f.count(), f.nList)
	}
}

// After the last try, one more find runs; when it finds the story, exit 0.
func TestCreateGivesUpFindsLast(t *testing.T) {
	var c calls
	f := &createFake{statuses: []int{504, 504, 504, 504}}
	h := createHarness(t, f, &c, map[string]http.HandlerFunc{
		"GET " + createPath: c.wrap(func(w http.ResponseWriter, r *http.Request) {
			f.mu.Lock()
			f.nList++
			f.mu.Unlock()
			body := listBody()
			if f.count() == 4 { // the story shows up only after the last create
				body = listWith(f.externalID(0))
			}
			jsonHandler(body)(w, r)
		}),
	})
	code, out, errs := h.run("story", "create", "--name", "x", "--state", "To Do")
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	if f.count() != 4 || f.nList != 4 || decode(t, out)["id"] != float64(5818) || c.count(createPath+"/5818") != 1 {
		t.Errorf("%d creates, %d lists, out %s", f.count(), f.nList, out)
	}
}

func TestCreateNoRetryOn4xx(t *testing.T) {
	var c calls
	f := &createFake{statuses: []int{422, 201}}
	h := createHarness(t, f, &c, nil)
	code, _, errs := h.run("story", "create", "--name", "x", "--state", "To Do")
	if code == 0 || code == 5 || f.count() != 1 || f.nList != 0 {
		t.Errorf("code %d, creates %d, lists %d, stderr %s", code, f.count(), f.nList, errs)
	}
}

func TestCreateBudget(t *testing.T) {
	var c calls
	f := &createFake{statuses: []int{504, 201}}
	h := createHarness(t, f, &c, nil)
	now := time.Unix(1000, 0)
	h.env.Now = func() time.Time { now = now.Add(25 * time.Second); return now }
	code, _, errs := h.run("story", "create", "--name", "x", "--state", "To Do")
	if code != 5 || f.count() != 1 || f.nList != 1 {
		t.Errorf("code %d, creates %d, lists %d, stderr %s", code, f.count(), f.nList, errs)
	}
	if !strings.Contains(errs, f.externalID(0)) {
		t.Errorf("stderr %s", errs)
	}
}

// When the list call fails, the create must not run again: its result is unknown.
func TestCreateNoRecreateWhenFindFails(t *testing.T) {
	var c calls
	f := &createFake{statuses: []int{504, 201}}
	h := createHarness(t, f, &c, map[string]http.HandlerFunc{
		"GET " + createPath: c.wrap(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
		}),
	})
	code, _, errs := h.run("story", "create", "--name", "x", "--state", "To Do")
	if code != 5 || f.count() != 1 {
		t.Errorf("code %d, creates %d, stderr %s", code, f.count(), errs)
	}
}

func TestCreateUnknownLabel(t *testing.T) {
	var c calls
	f := &createFake{}
	h := createHarness(t, f, &c, nil)
	code, _, errs := h.run("story", "create", "--name", "x", "--state", "To Do", "--label", "nope")
	if code != 3 || c.count(createPath) != 0 {
		t.Errorf("code %d, calls %v, stderr %s", code, c.paths, errs)
	}
}

func TestCreateUsageErrors(t *testing.T) {
	var c calls
	f := &createFake{}
	h := createHarness(t, f, &c, nil)
	for _, args := range [][]string{
		{"story", "create"},
		{"story", "create", "--name", "x", "--type", "epic"},
		{"story", "create", "--name", "x", "--link", "blocks"},
		{"story", "create", "--name", "x", "--link", "eats 12"},
		{"story", "create", "--name", "x", "--workflow", "Standard"},
		{"story", "create", "--name", "x", "--field", "Squad"},
	} {
		if code, _, _ := h.run(args...); code != 2 {
			t.Errorf("%v: code %d, want 2", args, code)
		}
	}
	if c.total() != 0 {
		t.Errorf("calls %v", c.paths)
	}
}

func TestCreateNoState(t *testing.T) {
	var c calls
	f := &createFake{}
	h := createHarness(t, f, &c, nil)
	if code, _, _ := h.run("story", "create", "--name", "x"); code != 2 || c.count(createPath) != 0 {
		t.Errorf("code %d, calls %v", code, c.paths)
	}
}

func TestCreateAmbiguousState(t *testing.T) {
	var c calls
	f := &createFake{}
	h := createHarness(t, f, &c, map[string]http.HandlerFunc{"GET /api/v4/acme/workflows": twoToDo()})
	if code, _, _ := h.run("story", "create", "--name", "x", "--state", "To Do"); code != 2 || c.count(createPath) != 0 {
		t.Errorf("code %d, calls %v", code, c.paths)
	}
}

func TestCreateContract(t *testing.T) {
	h := newHarness(t, nil, "", map[string]string{"SCUT_TOKEN": updateAuth})
	h.env.APIBase = testapi.Prism(t)
	h.env.Sleep = func(time.Duration) {}
	code, out, errs := h.run("story", "create", "--name", "x", "--state", "string")
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	if _, ok := decode(t, out)["id"]; !ok {
		t.Errorf("no id: %s", out)
	}
}

// The list call may take long. No create starts after the budget.
func TestCreateBudgetAfterList(t *testing.T) {
	var c calls
	var offset atomic.Int64
	f := &createFake{statuses: []int{504, 201}}
	h := createHarness(t, f, &c, map[string]http.HandlerFunc{
		"GET " + createPath: c.wrap(func(w http.ResponseWriter, r *http.Request) {
			offset.Add(int64(25 * time.Second))
			jsonHandler(listBody())(w, r)
		}),
	})
	base := time.Unix(1000, 0)
	h.env.Now = func() time.Time { return base.Add(time.Duration(offset.Load())) }
	code, _, errs := h.run("story", "create", "--name", "x", "--state", "To Do")
	if code != 5 || f.count() != 1 || !strings.Contains(errs, f.externalID(0)) {
		t.Errorf("code %d, creates %d, stderr %s", code, f.count(), errs)
	}
}

func TestCreateRetryAfter(t *testing.T) {
	var c calls
	f := &createFake{statuses: []int{429, 201}, retryAfter: "2"}
	h := createHarness(t, f, &c, nil)
	var slept []time.Duration
	h.env.Sleep = func(d time.Duration) { slept = append(slept, d) }
	code, _, errs := h.run("story", "create", "--name", "x", "--state", "To Do")
	if code != 0 || len(slept) != 1 || slept[0] != 2*time.Second {
		t.Fatalf("code %d, slept %v, stderr %s", code, slept, errs)
	}
	var order []string
	for _, p := range c.paths {
		if p == createPath {
			order = append(order, p)
		}
	}
	if len(order) != 3 || f.count() != 2 || f.nList != 1 {
		t.Errorf("paths %v, creates %d, lists %d", c.paths, f.count(), f.nList)
	}
}

func TestCreateNoRetryOn500(t *testing.T) {
	var c calls
	f := &createFake{statuses: []int{500, 201}}
	h := createHarness(t, f, &c, nil)
	code, _, errs := h.run("story", "create", "--name", "x", "--state", "To Do")
	if code != 5 || f.count() != 1 || f.nList != 0 {
		t.Errorf("code %d, creates %d, lists %d", code, f.count(), f.nList)
	}
	if !strings.Contains(errs, "may exist") || !strings.Contains(errs, f.externalID(0)) {
		t.Errorf("stderr %s", errs)
	}
}

func TestCreateCancelled(t *testing.T) {
	var c calls
	f := &createFake{statuses: []int{503}}
	h := createHarness(t, f, &c, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h.env.Sleep = func(time.Duration) { cancel() }
	var errb bytes.Buffer
	code := cli.RunContext(ctx, cli.NewRoot(h.env), []string{"story", "create", "--name", "x", "--state", "To Do"}, &errb)
	if code != 130 || f.count() != 1 || !strings.Contains(errb.String(), f.externalID(0)) {
		t.Errorf("code %d, creates %d, stderr %s", code, f.count(), errb.String())
	}
}

func TestCreateShapeFails(t *testing.T) {
	var c calls
	f := &createFake{}
	h := createHarness(t, f, &c, map[string]http.HandlerFunc{
		"GET /api/v4/acme/members": func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(500) },
	})
	code, out, errs := h.run("story", "create", "--name", "x", "--state", "To Do")
	if code == 0 {
		t.Fatalf("code 0, stdout %s", out)
	}
	m := decode(t, out)
	if m["id"] != float64(5818) || m["app_url"] == nil {
		t.Errorf("stdout %s", out)
	}
	if !strings.Contains(errs, "the story was created: ") {
		t.Errorf("stderr %s", errs)
	}
}
