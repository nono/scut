package cli_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/nono/scut/internal/testapi"
)

const bulkPath = "/api/v4/acme/stories/bulk"

// bulkAPI is a fake API for story update on several stories.
type bulkAPI struct {
	t        *testing.T
	mu       sync.Mutex
	workflow map[int64]int // story ID to its workflow; a story not in it does not exist
	reads    []url.Values  // query of each GET /stories
	writes   []bulkWrite   // each PATCH /stories/bulk
	failCall int           // 1-based PATCH call that fails with 422; 0: none
	drop     int64         // story left out of each reply
	noParent bool          // the reply ignores parent_story_id
}

type bulkWrite struct {
	limit   string
	stories []map[string]any
}

func newBulkAPI(t *testing.T, ids ...int64) *bulkAPI {
	a := &bulkAPI{t: t, workflow: map[int64]int{}}
	for _, id := range ids {
		a.workflow[id] = 5
	}
	return a
}

func (a *bulkAPI) read(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	a.reads = append(a.reads, r.URL.Query())
	var found []any
	for _, s := range strings.Split(r.URL.Query().Get("ids"), ",") {
		id, _ := strconv.ParseInt(s, 10, 64)
		if wf, ok := a.workflow[id]; ok {
			found = append(found, map[string]any{"id": id, "workflow": map[string]any{"id": wf, "name": "W"}})
		}
	}
	a.mu.Unlock()
	jsonHandler(listBody(found...))(w, r)
}

func (a *bulkAPI) write(w http.ResponseWriter, r *http.Request) {
	b, _ := io.ReadAll(r.Body)
	var body struct {
		Stories []map[string]any `json:"stories"`
	}
	if err := json.Unmarshal(b, &body); err != nil {
		a.t.Errorf("bulk body: %v", err)
	}
	a.mu.Lock()
	a.writes = append(a.writes, bulkWrite{r.URL.Query().Get("limit"), body.Stories})
	n := len(a.writes)
	a.mu.Unlock()
	if n == a.failCall {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"message":"bad story"}`))
		return
	}
	var out []any
	for _, item := range body.Stories {
		id := int64(item["id"].(float64))
		if id == a.drop {
			continue
		}
		s := storyFixture(a.t)
		s["id"] = id
		if p, ok := item["parent_story_id"]; ok && p != nil && !a.noParent {
			s["parent_story"] = map[string]any{"id": p, "name": "Parent"}
		}
		out = append(out, s)
	}
	jsonHandler(listBody(out...))(w, r)
}

func (a *bulkAPI) harness(extra map[string]http.HandlerFunc) *harness {
	routes := lookupRoutes()
	routes["GET /api/v4/acme/stories"] = a.read
	routes["PATCH "+bulkPath] = a.write
	for k, v := range extra {
		routes[k] = v
	}
	return newHarness(a.t, routes, "", map[string]string{"SCUT_TOKEN": updateAuth})
}

func (a *bulkAPI) ids(write int) []int64 {
	var out []int64
	for _, s := range a.writes[write].stories {
		out = append(out, int64(s["id"].(float64)))
	}
	return out
}

func outIDs(t *testing.T, out string) []int64 {
	t.Helper()
	var list []map[string]any
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		t.Fatalf("not a list: %q", out)
	}
	ids := make([]int64, 0, len(list))
	for _, s := range list {
		ids = append(ids, int64(s["id"].(float64)))
	}
	return ids
}

func idRange(from, n int64) []int64 {
	out := make([]int64, 0, n)
	for i := range n {
		out = append(out, from+i)
	}
	return out
}

func idArgs(ids []int64) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, strconv.FormatInt(id, 10))
	}
	return out
}

// The sc-5428 case: several sub-tasks in one command, with one read and one write.
func TestBulkUpdateParent(t *testing.T) {
	a := newBulkAPI(t, 5429, 5430, 5431)
	h := a.harness(nil)
	code, out, errs := h.run("story", "update", "5429", "sc-5430", "5431", "--parent", "5428")
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	if len(a.reads) != 1 || len(a.writes) != 1 {
		t.Fatalf("reads %d, writes %d", len(a.reads), len(a.writes))
	}
	if got := jsonOf(a.writes[0].stories); got != `[{"id":5429,"parent_story_id":5428},{"id":5430,"parent_story_id":5428},{"id":5431,"parent_story_id":5428}]` {
		t.Errorf("body %s", got)
	}
	if a.writes[0].limit != "3" {
		t.Errorf("limit %q", a.writes[0].limit)
	}
	if q := a.reads[0]; q.Get("ids") != "5429,5430,5431" || q.Get("filter") != "include_archived" || q.Get("fields") != "id,workflow" {
		t.Errorf("read %v", q)
	}
	if got := outIDs(t, out); jsonOf(got) != "[5429,5430,5431]" {
		t.Errorf("out ids %v", got)
	}
	if strings.Contains(out, `"description"`) {
		t.Errorf("a list has no description: %s", out)
	}
}

func TestBulkUpdateRepeatsAndOrder(t *testing.T) {
	a := newBulkAPI(t, 5429, 5430)
	h := a.harness(nil)
	code, out, errs := h.run("story", "update", "5430", "5429", "5430", "--archive")
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	if got := jsonOf(a.ids(0)); got != "[5430,5429]" {
		t.Errorf("write ids %s", got)
	}
	if got := outIDs(t, out); jsonOf(got) != "[5430,5429]" {
		t.Errorf("out ids %v", got)
	}
}

// One story after repeats are removed takes the path of one story.
func TestBulkUpdateOneStoryAfterRepeats(t *testing.T) {
	var c calls
	var body map[string]any
	h := updateHarness(t, &c, &body, nil)
	code, out, errs := h.run("story", "update", "5818", "sc-5818", "--name", "x")
	if code != 0 || jsonOf(body) != `{"name":"x"}` {
		t.Fatalf("code %d, body %s, stderr %s", code, jsonOf(body), errs)
	}
	if decode(t, out)["id"] != float64(5818) {
		t.Errorf("out %s", out)
	}
}

func TestBulkUpdateChunks(t *testing.T) {
	ids := idRange(6000, 150)
	a := newBulkAPI(t, ids...)
	h := a.harness(nil)
	code, out, errs := h.run(append([]string{"story", "update", "--add-owner", "bruno"}, idArgs(ids)...)...)
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	if len(a.reads) != 2 || len(a.writes) != 2 {
		t.Fatalf("reads %d, writes %d", len(a.reads), len(a.writes))
	}
	if len(a.writes[0].stories) != 100 || len(a.writes[1].stories) != 50 || a.writes[0].limit != "100" || a.writes[1].limit != "50" {
		t.Errorf("chunks %d (limit %s), %d (limit %s)", len(a.writes[0].stories), a.writes[0].limit, len(a.writes[1].stories), a.writes[1].limit)
	}
	if jsonOf(append(a.ids(0), a.ids(1)...)) != jsonOf(ids) {
		t.Error("write order differs from the argument order")
	}
	if _, ok := a.writes[1].stories[0]["owner_ids_add"]; !ok {
		t.Errorf("item %v", a.writes[1].stories[0])
	}
	if got := outIDs(t, out); len(got) != 150 {
		t.Errorf("out has %d stories", len(got))
	}
}

func TestBulkUpdateStatePerWorkflow(t *testing.T) {
	a := newBulkAPI(t, 5429, 5430)
	a.workflow[5430] = 6
	h := a.harness(map[string]http.HandlerFunc{"GET /api/v4/acme/workflows": twoToDo()})
	code, _, errs := h.run("story", "update", "5429", "5430", "--state", "To Do")
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	if got := jsonOf(a.writes[0].stories); got != `[{"id":5429,"workflow_state_id":7},{"id":5430,"workflow_state_id":9}]` {
		t.Errorf("body %s", got)
	}
	code, _, errs = h.run("story", "update", "5429", "5430", "--state", "To Do", "--workflow", "Kanban")
	if code != 0 || jsonOf(a.writes[1].stories) != `[{"id":5429,"workflow_state_id":9},{"id":5430,"workflow_state_id":9}]` {
		t.Errorf("--workflow: code %d, body %s, stderr %s", code, jsonOf(a.writes[1].stories), errs)
	}
}

// No story is changed when a story or a name does not resolve.
func TestBulkUpdateNoWriteOnResolveError(t *testing.T) {
	a := newBulkAPI(t, 5429)
	a.workflow[5431] = 7 // a workflow with no "To Do"
	h := a.harness(map[string]http.HandlerFunc{"GET /api/v4/acme/workflows": twoToDo()})
	code, _, errs := h.run("story", "update", "5429", "5430", "--archive")
	if code != 3 || !strings.Contains(errs, "5430") {
		t.Errorf("missing story: code %d, stderr %s", code, errs)
	}
	code, _, errs = h.run("story", "update", "5429", "5431", "--state", "To Do")
	if code != 3 {
		t.Errorf("state not in a workflow: code %d, stderr %s", code, errs)
	}
	code, _, errs = h.run("story", "update", "5429", "5431", "--epic", "No such epic")
	if code != 3 {
		t.Errorf("bad epic: code %d, stderr %s", code, errs)
	}
	if len(a.writes) != 0 {
		t.Errorf("writes %d", len(a.writes))
	}
}

func TestBulkUpdateFailedChunk(t *testing.T) {
	ids := idRange(6000, 250)
	a := newBulkAPI(t, ids...)
	a.failCall = 2
	h := a.harness(nil)
	code, out, errs := h.run(append([]string{"story", "update", "--archive"}, idArgs(ids)...)...)
	if code == 0 {
		t.Fatal("code 0")
	}
	if got := outIDs(t, out); len(got) != 100 || got[0] != 6000 {
		t.Errorf("stdout has %d stories, want the first chunk", len(got))
	}
	if len(a.writes) != 2 {
		t.Errorf("writes %d: the chunk after the failed one must not be sent", len(a.writes))
	}
	e := decode(t, errs)
	s, _ := e["suggestion"].(string)
	if !strings.Contains(s, "may be partly changed") || !strings.Contains(s, "scut story view 6100 6101") || !strings.Contains(s, " 6199") || strings.Contains(s, "6200") {
		t.Errorf("suggestion %q", s)
	}
	if e["status"] != float64(422) || e["api"] == nil {
		t.Errorf("error %v", e)
	}
}

func TestBulkUpdateReplyLeavesOutStory(t *testing.T) {
	a := newBulkAPI(t, 5429, 5430)
	a.drop = 5430
	h := a.harness(nil)
	code, _, errs := h.run("story", "update", "5429", "5430", "--archive")
	if code != 1 || !strings.Contains(errs, "5430") || !strings.Contains(errs, "saved") {
		t.Errorf("code %d, stderr %s", code, errs)
	}
}

func TestBulkUpdateParentIgnored(t *testing.T) {
	a := newBulkAPI(t, 5429, 5430)
	a.noParent = true
	h := a.harness(nil)
	code, _, errs := h.run("story", "update", "5429", "5430", "--parent", "5428")
	if code != 1 || !strings.Contains(errs, "the API did not set the parent") || !strings.Contains(errs, "5429") {
		t.Errorf("code %d, stderr %s", code, errs)
	}
}

// Flags that fit one story only are usage errors, before the token is read.
func TestBulkUpdateUsage(t *testing.T) {
	h := newHarness(t, map[string]http.HandlerFunc{}, "", nil)
	for _, args := range [][]string{
		{"5429", "5430", "--name", "x"},
		{"5429", "5430", "--description-file", "-"},
		{"5429", "5428", "--parent", "5428"},
		{"5429", "5430"},
	} {
		if code, _, errs := h.run(append([]string{"story", "update"}, args...)...); code != 2 {
			t.Errorf("%v: code %d, stderr %s", args, code, errs)
		}
	}
}

// Prism checks the bulk body against the spec. The read goes to the fake API,
// since Prism makes up the IDs of the stories it lists.
func TestBulkUpdateContract(t *testing.T) {
	prism, err := url.Parse(testapi.Prism(t))
	if err != nil {
		t.Fatal(err)
	}
	a := newBulkAPI(t, 5429, 5430)
	h := a.harness(map[string]http.HandlerFunc{
		"PATCH " + bulkPath: httputil.NewSingleHostReverseProxy(prism).ServeHTTP,
		// The spec wants UUIDs for owner IDs.
		"GET /api/v4/acme/members": jsonHandler(listBody(map[string]any{
			"id": "00000000-0000-4000-8000-0000000000aa", "name": "Test User", "mention_name": "test-user"})),
		"GET /api/v4/acme/workflows": twoToDo(),
	})
	// Prism's reply lists made-up stories, so the reply check fails. A body
	// that Prism rejects gives another error.
	code, _, errs := h.run("story", "update", "5429", "5430", "--archive", "--add-owner", "test-user",
		"--field", "Technical Area=Back end", "--state", "To Do", "--parent", "5428")
	if code != 1 || !strings.Contains(errs, "the reply does not list") {
		t.Errorf("code %d, stderr %s", code, errs)
	}
}
