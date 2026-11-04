package cli_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"

	"github.com/nono/scut/internal/testapi"
)

const testMember = "00000000-0000-4000-8000-000000000001"

// storyFixture returns the "entity" of testdata/story_5818.json as a map.
func storyFixture(t *testing.T) map[string]any {
	t.Helper()
	b, err := os.ReadFile("testdata/story_5818.json")
	if err != nil {
		t.Fatal(err)
	}
	var env struct {
		Entity map[string]any `json:"entity"`
	}
	if err := json.Unmarshal(b, &env); err != nil {
		t.Fatal(err)
	}
	return env.Entity
}

func jsonOf(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func comment(id int, text any, deleted bool) map[string]any {
	return map[string]any{
		"id": id, "text": text, "deleted": deleted, "created_at": "2026-11-04T10:30:00Z",
		"author": map[string]any{"entity_type": "member:slim", "id": testMember, "name": "Test User"},
	}
}

type calls struct {
	mu    sync.Mutex
	paths []string
	query map[string]string
}

func (c *calls) count(path string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, p := range c.paths {
		if p == path {
			n++
		}
	}
	return n
}

func (c *calls) total() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.paths)
}

func (c *calls) wrap(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c.mu.Lock()
		c.paths = append(c.paths, r.URL.Path)
		if c.query == nil {
			c.query = map[string]string{}
		}
		c.query[r.URL.Path] = r.URL.RawQuery
		c.mu.Unlock()
		h(w, r)
	}
}

func (c *calls) param(path, key string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return strings.Join(mustQuery(c.query[path])[key], ",")
}

func mustQuery(raw string) url.Values {
	v, _ := url.ParseQuery(raw)
	return v
}

func viewHarness(t *testing.T, c *calls, story map[string]any, comments []any) *harness {
	t.Helper()
	routes := map[string]http.HandlerFunc{
		"GET /api/v4/acme/stories/5818":          c.wrap(jsonHandler(jsonOf(map[string]any{"entity": story}))),
		"GET /api/v4/acme/stories/5818/comments": c.wrap(jsonHandler(listBody(comments...))),
		"GET /api/v4/acme/members": jsonHandler(listBody(
			map[string]any{"id": testMember, "name": "Test User", "mention_name": "testuser"})),
	}
	return newHarness(t, routes, "", map[string]string{"SCUT_TOKEN": "sct_rw_acme_s3cret"})
}

func TestViewOneStoryTwoCalls(t *testing.T) {
	var c calls
	h := viewHarness(t, &c, storyFixture(t), []any{comment(1, "hello", false)})
	code, out, errs := h.run("story", "view", "5818")
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	if n := c.total(); n != 2 {
		t.Errorf("%d story calls, want 2: %v", n, c.paths)
	}
	if got := c.param("/api/v4/acme/stories/5818/comments", "order_dir"); got != "desc" {
		t.Errorf("order_dir %q", got)
	}
	if got := c.param("/api/v4/acme/stories/5818/comments", "limit"); got != "10" {
		t.Errorf("limit %q", got)
	}
	m := decode(t, out)
	if m["id"] != float64(5818) || m["name"] != "scut API test 1 (delete me)" || m["type"] != "chore" {
		t.Errorf("basics: %s", out)
	}
	if s, _ := m["state"].(map[string]any); s["id"] != float64(500000007) || s["name"] != "To Do" {
		t.Errorf("state: %v", m["state"])
	}
	owners, _ := m["owners"].([]any)
	if len(owners) != 1 || owners[0].(map[string]any)["mention_name"] != "testuser" || owners[0].(map[string]any)["id"] != testMember {
		t.Errorf("owners: %v", m["owners"])
	}
	if r, _ := m["requester"].(map[string]any); r["mention_name"] != "testuser" {
		t.Errorf("requester: %v", m["requester"])
	}
	if f, _ := m["fields"].(map[string]any); f["Technical Area"] != "API" {
		t.Errorf("fields: %v", m["fields"])
	}
	if m["app_url"] != "https://app.shortcut.com/acme/story/5818" || m["description"] == nil {
		t.Errorf("url/description: %s", out)
	}
	cs, _ := m["comments"].([]any)
	if len(cs) != 1 {
		t.Fatalf("comments: %v", m["comments"])
	}
	c0 := cs[0].(map[string]any)
	if c0["text"] != "hello" || c0["created_at"] != "2026-11-04T10:30:00Z" || c0["author"].(map[string]any)["mention_name"] != "testuser" {
		t.Errorf("comment: %v", c0)
	}
	for _, k := range []string{"labels_total", "owners_total", "links_total"} {
		if _, ok := m[k]; ok {
			t.Errorf("unexpected %s", k)
		}
	}
}

func TestViewMemberNotCached(t *testing.T) {
	var c calls
	h := viewHarness(t, &c, storyFixture(t), nil)
	h.env.APIBase = testapi.Fake(t, map[string]http.HandlerFunc{
		"GET /api/v4/acme/stories/5818":          c.wrap(jsonHandler(jsonOf(map[string]any{"entity": storyFixture(t)}))),
		"GET /api/v4/acme/stories/5818/comments": c.wrap(jsonHandler(listBody())),
		"GET /api/v4/acme/members":               jsonHandler(listBody()),
	})
	code, out, errs := h.run("story", "view", "5818")
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	owners := decode(t, out)["owners"].([]any)
	o := owners[0].(map[string]any)
	if v, ok := o["mention_name"]; !ok || v != nil {
		t.Errorf("owner %v, want mention_name null", o)
	}
}

func TestViewFieldsNoComments(t *testing.T) {
	var c calls
	story := map[string]any{
		"id": 5818, "name": "N",
		"workflow_state": map[string]any{"id": 7, "name": "To Do", "workflow_name": "Standard"},
	}
	h := viewHarness(t, &c, story, nil)
	code, out, errs := h.run("story", "view", "5818", "--fields", "name,state")
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	if n := c.total(); n != 1 {
		t.Errorf("%d calls, want 1: %v", n, c.paths)
	}
	if got := c.param("/api/v4/acme/stories/5818", "fields"); got != "id,name,workflow_state" {
		t.Errorf("fields %q", got)
	}
	m := decode(t, out)
	if len(m) != 3 || m["name"] != "N" || m["state"].(map[string]any)["name"] != "To Do" {
		t.Errorf("out %s", out)
	}
}

func TestViewFieldsMapping(t *testing.T) {
	var c calls
	h := viewHarness(t, &c, map[string]any{"id": 5818}, nil)
	code, _, errs := h.run("story", "view", "5818", "--fields", "id,type,tasks,fields,links,estimate", "--comments", "0")
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	want := "id,story_type,checklist_items,custom_field_values,story_links,estimate"
	if got := c.param("/api/v4/acme/stories/5818", "fields"); got != want {
		t.Errorf("fields %q, want %q", got, want)
	}
}

func TestViewFieldsComments(t *testing.T) {
	var c calls
	h := viewHarness(t, &c, map[string]any{"id": 5818}, []any{comment(1, "hi", false)})
	code, out, errs := h.run("story", "view", "5818", "--fields", "comments")
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	if c.count("/api/v4/acme/stories/5818/comments") != 1 || len(decode(t, out)["comments"].([]any)) != 1 {
		t.Errorf("out %s, calls %v", out, c.paths)
	}
}

func TestViewCommentsZero(t *testing.T) {
	var c calls
	h := viewHarness(t, &c, storyFixture(t), nil)
	code, out, errs := h.run("story", "view", "5818", "--comments", "0")
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	if c.total() != 1 {
		t.Errorf("calls %v", c.paths)
	}
	if _, ok := decode(t, out)["comments"]; ok {
		t.Errorf("comments in %s", out)
	}
}

func TestViewCommentsAll(t *testing.T) {
	var c calls
	var pages int
	routes := map[string]http.HandlerFunc{
		"GET /api/v4/acme/stories/5818": c.wrap(jsonHandler(jsonOf(map[string]any{"entity": storyFixture(t)}))),
		"GET /api/v4/acme/stories/5818/comments": func(w http.ResponseWriter, r *http.Request) {
			pages++
			w.Header().Set("Content-Type", "application/json")
			if r.URL.Query().Get("cursor") == "" {
				_, _ = w.Write([]byte(`{"entities":[` + jsonOf(comment(1, "a", false)) + `],"next_page_url":"http://x/api/v4/acme/stories/5818/comments?cursor=c2"}`))
				return
			}
			_, _ = w.Write([]byte(`{"entities":[` + jsonOf(comment(2, "b", false)) + `]}`))
		},
		"GET /api/v4/acme/members": jsonHandler(listBody()),
	}
	h := newHarness(t, routes, "", map[string]string{"SCUT_TOKEN": "sct_rw_acme_s3cret"})
	code, out, errs := h.run("story", "view", "5818", "--comments", "all")
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	if cs := decode(t, out)["comments"].([]any); len(cs) != 2 || pages != 2 {
		t.Errorf("%d comments, %d pages: %s", len(cs), pages, out)
	}
}

func TestViewBadComments(t *testing.T) {
	h := newHarness(t, nil, "", nil)
	for _, v := range []string{"-1", "some", "1.5"} {
		if code, _, _ := h.run("story", "view", "5818", "--comments", v); code != 2 {
			t.Errorf("--comments %s: code %d, want 2", v, code)
		}
	}
}

func TestViewMany(t *testing.T) {
	var c calls
	s1, s2 := storyFixture(t), storyFixture(t)
	s2["id"] = 5819
	routes := map[string]http.HandlerFunc{
		"GET /api/v4/acme/stories": c.wrap(jsonHandler(listBody(s1, s2))),
		"GET /api/v4/acme/members": jsonHandler(listBody()),
	}
	h := newHarness(t, routes, "", map[string]string{"SCUT_TOKEN": "sct_rw_acme_s3cret"})
	code, out, errs := h.run("story", "view", "5818", "sc-5819")
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	if c.total() != 1 || c.param("/api/v4/acme/stories", "ids") != "5818,5819" {
		t.Errorf("calls %v, ids %q", c.paths, c.param("/api/v4/acme/stories", "ids"))
	}
	l := decodeList(t, out)
	if len(l) != 2 || l[1]["id"] != float64(5819) {
		t.Errorf("out %s", out)
	}
}

func TestViewTotal(t *testing.T) {
	var c calls
	story := storyFixture(t)
	story["story_links"] = map[string]any{"current_items": 2, "total_items": 5, "entities": []any{
		map[string]any{"id": 1, "verb": "blocks", "subject": map[string]any{"id": 5818, "name": "a"}, "object": map[string]any{"id": 9, "name": "b"}},
		map[string]any{"id": 2, "verb": "relates to", "subject": map[string]any{"id": 5818}, "object": map[string]any{"id": 10}},
	}}
	story["checklist_items"] = nestedList(map[string]any{"id": 3, "description": "do it", "completed": true})
	story["branches"] = nestedList(map[string]any{"id": 4, "name": "b1"})
	story["pull_requests"] = nestedList(map[string]any{"id": 6, "name": "pr1"})
	story["labels"] = nestedList(map[string]any{"id": 7, "name": "bug"})
	story["epic"] = map[string]any{"id": 12, "name": "Epic", "app_url": "u"}
	h := viewHarness(t, &c, story, nil)
	code, out, errs := h.run("story", "view", "5818")
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	m := decode(t, out)
	if m["links_total"] != float64(5) {
		t.Errorf("links_total: %s", out)
	}
	links := m["links"].([]any)
	l0 := links[0].(map[string]any)
	if len(links) != 2 || l0["verb"] != "blocks" || l0["subject"] != float64(5818) || l0["object"] != float64(9) {
		t.Errorf("links: %v", links)
	}
	if got := jsonOf(m["tasks"]); got != `[{"complete":true,"description":"do it","id":3}]` {
		t.Errorf("tasks: %s", got)
	}
	if got := jsonOf(m["branches"]); got != `[{"id":4,"name":"b1"}]` {
		t.Errorf("branches: %s", got)
	}
	if got := jsonOf(m["pull_requests"]); got != `[{"id":6,"name":"pr1"}]` {
		t.Errorf("pull_requests: %s", got)
	}
	if got := jsonOf(m["labels"]); got != `["bug"]` {
		t.Errorf("labels: %s", got)
	}
	if got := jsonOf(m["epic"]); got != `{"id":12,"name":"Epic"}` {
		t.Errorf("epic: %s", got)
	}
}

func TestViewDeletedComment(t *testing.T) {
	var c calls
	h := viewHarness(t, &c, storyFixture(t), []any{comment(8, nil, true)})
	code, out, errs := h.run("story", "view", "5818")
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	if got := jsonOf(decode(t, out)["comments"]); got != `[{"deleted":true,"id":8}]` {
		t.Errorf("comments: %s", got)
	}
}

func TestViewFromBranch(t *testing.T) {
	var c calls
	h := viewHarness(t, &c, storyFixture(t), nil)
	for _, args := range [][]string{{"init", "-q"}, {"checkout", "-q", "-b", "chore/sc-5818/x"},
		{"-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "--allow-empty", "-m", "x"}} {
		if out, err := exec.Command("git", append([]string{"-C", h.env.Dir}, args...)...).CombinedOutput(); err != nil {
			t.Skipf("git: %v %s", err, out)
		}
	}
	code, out, errs := h.run("story", "view")
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	if decode(t, out)["id"] != float64(5818) {
		t.Errorf("out %s", out)
	}
}

func TestViewNoArgNoBranch(t *testing.T) {
	h := newHarness(t, nil, "", nil)
	if code, _, _ := h.run("story", "view"); code != 2 {
		t.Errorf("code %d, want 2", code)
	}
}

func TestViewNotFound(t *testing.T) {
	routes := map[string]http.HandlerFunc{
		"GET /api/v4/acme/stories/5818": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"message":"Resource does not exist."}`))
		},
		"GET /api/v4/acme/members": jsonHandler(listBody()),
	}
	h := newHarness(t, routes, "", map[string]string{"SCUT_TOKEN": "sct_rw_acme_s3cret"})
	if code, _, _ := h.run("story", "view", "5818"); code != 3 {
		t.Errorf("code %d, want 3", code)
	}
}

func TestViewHuman(t *testing.T) {
	var c calls
	h := viewHarness(t, &c, storyFixture(t), nil)
	code, out, errs := h.run("--human", "story", "view", "5818", "--comments", "0")
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	for _, want := range []string{"scut API test 1 (delete me)", "To Do", "testuser", "https://app.shortcut.com/acme/story/5818", "Test story for the scut CLI"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %q", want, out)
		}
	}
}

func TestViewContract(t *testing.T) {
	h := newHarness(t, nil, "", map[string]string{"SCUT_TOKEN": "sct_rw_acme_s3cret"})
	h.env.APIBase = testapi.Prism(t)
	code, out, errs := h.run("story", "view", "5818")
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	if _, ok := decode(t, out)["id"]; !ok {
		t.Errorf("no id: %s", out)
	}
	code, out, errs = h.run("story", "view", "5818", "5819")
	if code != 0 {
		t.Fatalf("many: code %d, stderr %s", code, errs)
	}
	decodeList(t, out)
}
