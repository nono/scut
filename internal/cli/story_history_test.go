package cli_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nono/scut/internal/testapi"
)

const (
	historyPath     = "/api/v4/acme/stories/5818/history"
	manyHistoryPath = "/api/v4/acme/stories/history"
	unknownMember   = "00000000-0000-4000-8000-0000000000aa"
)

func storySlim(id int) map[string]any {
	return map[string]any{"entity_type": "story:slim", "id": id, "name": "Story " + strconv.Itoa(id),
		"app_url": "https://app.shortcut.com/acme/story/" + strconv.Itoa(id), "uri": "https://api.app.shortcut.com/x"}
}

func memberSlim(id string) map[string]any {
	return map[string]any{"entity_type": "member:slim", "id": id, "name": "Test User", "uri": "https://api.app.shortcut.com/x"}
}

func stateSlim(id int, name string) map[string]any {
	return map[string]any{"entity_type": "workflow-state:slim", "id": id, "name": name,
		"uri": "https://api.app.shortcut.com/x", "workflow_name": "Standard"}
}

// change is a StoryHistoryChange.
func change(story int, attr, action string, actor any, removes, adds []any) map[string]any {
	return map[string]any{"tx_id": "00000000-0000-4000-8000-0000000000bb", "timestamp": "2026-11-17T14:34:59.372Z",
		"entity": storySlim(story), "attribute": attr, "action": action, "actor": actor, "removes": removes, "adds": adds}
}

// historyBody is a StoryHistoryResponseWrapper. next is the next_page_url.
func historyBody(next string, changes ...any) string {
	if changes == nil {
		changes = []any{}
	}
	m := map[string]any{"$schema": "https://api.app.shortcut.com/api/v4/schemas/story-history", "changes": changes,
		"current_items": len(changes), "current_page": 1, "total_items": len(changes), "total_pages": 1}
	if next != "" {
		m["next_page_url"] = next
	}
	return jsonOf(m)
}

func stateChange(story int) map[string]any {
	return change(story, "workflow_state", "update", memberSlim(testMember),
		[]any{stateSlim(7, "To Do")}, []any{stateSlim(8, "Done")})
}

func historyHarness(t *testing.T, routes map[string]http.HandlerFunc) *harness {
	t.Helper()
	routes["GET /api/v4/acme/members"] = membersRoute()
	return newHarness(t, routes, "", searchEnv)
}

func TestHistoryShape(t *testing.T) {
	link := map[string]any{"entity_type": "story-link:slim", "id": 50, "verb": "blocks", "uri": "https://api.app.shortcut.com/x",
		"subject": storySlim(5818), "object": storySlim(9)}
	commit := map[string]any{"entity_type": "commit:slim", "id": json.Number("9007199254740993"), "message": "[sc-5818] Fix",
		"uri": "https://api.app.shortcut.com/x"}
	vcs := map[string]any{"entity_type": "vcs-identity:slim", "id": 77, "name": "octocat"}
	var c calls
	h := historyHarness(t, map[string]http.HandlerFunc{
		"GET " + historyPath: c.wrap(jsonHandler(historyBody("",
			stateChange(5818),
			change(5818, "owners", "update", memberSlim(unknownMember), []any{}, []any{memberSlim(testMember)}),
			change(5818, "story_links", "create", nil, []any{}, []any{link}),
			change(5818, "commits", "push", vcs, []any{}, []any{commit}),
			change(5818, "estimate", "update", memberSlim(testMember), []any{2}, []any{3}),
		))),
	})
	code, out, errs := h.run("story", "history", "sc-5818")
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	ts := `"timestamp":"2026-11-17T14:34:59.372Z"`
	me := `{"id":"` + testMember + `","mention_name":"testuser"}`
	sameJSON(t, out, `[
		{`+ts+`,"actor":`+me+`,"action":"update","attribute":"workflow_state",
			"removes":[{"id":7,"name":"To Do"}],"adds":[{"id":8,"name":"Done"}]},
		{`+ts+`,"actor":{"id":"`+unknownMember+`","mention_name":null,"name":"Test User"},"action":"update","attribute":"owners","adds":[`+me+`]},
		{`+ts+`,"action":"create","attribute":"story_links",
			"adds":[{"id":50,"verb":"blocks","subject":{"id":5818,"name":"Story 5818"},"object":{"id":9,"name":"Story 9"}}]},
		{`+ts+`,"actor":{"id":77,"name":"octocat"},"action":"push","attribute":"commits",
			"adds":[{"id":9007199254740993,"message":"[sc-5818] Fix"}]},
		{`+ts+`,"actor":`+me+`,"action":"update","attribute":"estimate","removes":[2],"adds":[3]}
	]`)
	for k, want := range map[string]string{"order_dir": "asc", "limit": "25", "fields": "", "ids": ""} {
		if got := c.param(historyPath, k); got != want {
			t.Errorf("%s %q, want %q", k, got, want)
		}
	}
}

func TestHistoryFlags(t *testing.T) {
	for _, tc := range []struct {
		args                 []string
		fields, order, limit string
		attribute            bool
	}{
		{[]string{"--fields", "state, owners,workflow_state", "--desc", "--limit", "5"}, "workflow_state,owners", "desc", "5", true},
		{[]string{"--fields", "workflow_state", "--limit", "all"}, "workflow_state", "asc", "100", false},
		{[]string{"--fields", "tasks,links,type,fields"}, "checklist_items,story_links,story_type,custom_field_values", "asc", "25", true},
	} {
		var c calls
		h := historyHarness(t, map[string]http.HandlerFunc{"GET " + historyPath: c.wrap(jsonHandler(historyBody("", stateChange(5818))))})
		code, out, errs := h.run(append([]string{"story", "history", "5818"}, tc.args...)...)
		if code != 0 {
			t.Fatalf("%v: code %d, stderr %s", tc.args, code, errs)
		}
		for k, want := range map[string]string{"fields": tc.fields, "order_dir": tc.order, "limit": tc.limit} {
			if got := c.param(historyPath, k); got != want {
				t.Errorf("%v: %s %q, want %q", tc.args, k, got, want)
			}
		}
		l := decodeList(t, out)
		if _, ok := l[0]["attribute"]; ok != tc.attribute {
			t.Errorf("%v: attribute shown %v: %s", tc.args, ok, out)
		}
		if _, ok := l[0]["story"]; ok {
			t.Errorf("%v: story shown for one story: %s", tc.args, out)
		}
	}
}

func TestHistoryPaging(t *testing.T) {
	for _, tc := range []struct {
		limit        string
		items, calls int
	}{{"all", 250, 3}, {"120", 120, 2}, {"25", 25, 1}, {"300", 250, 3}} {
		var c calls
		h := historyHarness(t, map[string]http.HandlerFunc{"GET " + historyPath: paged(&c, 250, func(i int) any {
			return change(5818, "estimate", "update", nil, []any{}, []any{i})
		}, historyBody)})
		code, out, errs := h.run("story", "history", "5818", "--limit", tc.limit)
		if code != 0 {
			t.Fatalf("--limit %s: code %d, stderr %s", tc.limit, code, errs)
		}
		l := decodeList(t, out)
		if len(l) != tc.items || c.count(historyPath) != tc.calls {
			t.Errorf("--limit %s: %d items, %d calls; want %d, %d", tc.limit, len(l), c.count(historyPath), tc.items, tc.calls)
		}
		if last := l[len(l)-1]["adds"].([]any)[0]; last != float64(tc.items-1) {
			t.Errorf("--limit %s: last item %v", tc.limit, last)
		}
	}
}

func TestHistoryMany(t *testing.T) {
	var hc, sc calls
	found := storyFixture(t)
	found["id"] = 5819
	h := historyHarness(t, map[string]http.HandlerFunc{
		"GET " + manyHistoryPath:   hc.wrap(jsonHandler(historyBody("", stateChange(5818)))),
		"GET /api/v4/acme/stories": sc.wrap(jsonHandler(listBody(found))),
	})
	code, out, errs := h.run("story", "history", "5818", "sc-5819", "5818", "99999", "--desc")
	if code != 3 || !strings.Contains(errs, "no story 99999") {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	for k, want := range map[string]string{"ids": "5818,5819,99999", "order_dir": "desc", "limit": "25"} {
		if got := hc.param(manyHistoryPath, k); got != want {
			t.Errorf("history %s %q, want %q", k, got, want)
		}
	}
	for k, want := range map[string]string{"ids": "5819,99999", "filter": "include_archived", "fields": "id"} {
		if got := sc.param("/api/v4/acme/stories", k); got != want {
			t.Errorf("stories %s %q, want %q", k, got, want)
		}
	}
	if l := decodeList(t, out); len(l) != 1 || l[0]["story"] != float64(5818) {
		t.Errorf("stdout %s", out)
	}
}

// The rules apply at each level: an entity with no name loses app_url, and the
// member in its nested array gets its mention name. The nested member is the
// only member of the response, so its lookup comes from the nested walk.
func TestHistoryNested(t *testing.T) {
	nested := map[string]any{"entity_type": "x:slim", "id": 60, "uri": "https://api.app.shortcut.com/x", "app_url": "https://app.shortcut.com/x",
		"items": []any{memberSlim(testMember), map[string]any{"entity_type": "y:slim", "id": 61, "uri": "https://api.app.shortcut.com/x"}}}
	h := historyHarness(t, map[string]http.HandlerFunc{
		"GET " + historyPath: jsonHandler(historyBody("", change(5818, "future", "update", nil, []any{}, []any{nested}))),
	})
	code, out, errs := h.run("story", "history", "5818")
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	sameJSON(t, out, `[{"timestamp":"2026-11-17T14:34:59.372Z","action":"update","attribute":"future",
		"adds":[{"id":60,"items":[{"id":"`+testMember+`","mention_name":"testuser"},{"id":61}]}]}]`)
}

// A paging stop prints the changes read, skips the check of the stories, and
// fails.
func TestHistoryPagingStop(t *testing.T) {
	var sc calls
	h := historyHarness(t, map[string]http.HandlerFunc{
		"GET " + manyHistoryPath: jsonHandler(historyBody(
			"https://api.app.shortcut.com"+manyHistoryPath+"?cursor=same", stateChange(5818))),
		"GET /api/v4/acme/stories": sc.wrap(jsonHandler(listBody())),
	})
	code, out, errs := h.run("story", "history", "5818", "5819", "--limit", "all")
	if code != 1 || !strings.Contains(errs, "the cursor repeats") || sc.total() != 0 {
		t.Errorf("code %d, %d checks, stderr %s", code, sc.total(), errs)
	}
	if l := decodeList(t, out); len(l) != 2 || l[0]["story"] != float64(5818) {
		t.Errorf("stdout %s", out)
	}
}

// A failed check of the stories comes after the changes, with its exit code.
func TestHistoryCheckFails(t *testing.T) {
	h := historyHarness(t, map[string]http.HandlerFunc{
		"GET " + manyHistoryPath: jsonHandler(historyBody("", stateChange(5818))),
		"GET /api/v4/acme/stories": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(503)
			_, _ = w.Write([]byte(`{"message":"unavailable"}`))
		},
	})
	h.env.Sleep = func(time.Duration) {}
	code, out, errs := h.run("story", "history", "5818", "5819")
	if code != 5 {
		t.Errorf("code %d, stderr %s", code, errs)
	}
	if l := decodeList(t, out); len(l) != 1 {
		t.Errorf("stdout %s", out)
	}
}

// When each story has a change, no call checks that the stories exist.
func TestHistoryManyAllFound(t *testing.T) {
	var sc calls
	h := historyHarness(t, map[string]http.HandlerFunc{
		"GET " + manyHistoryPath:   jsonHandler(historyBody("", stateChange(5818), stateChange(5819))),
		"GET /api/v4/acme/stories": sc.wrap(jsonHandler(listBody())),
	})
	code, out, errs := h.run("story", "history", "5818", "5819")
	if code != 0 || sc.total() != 0 || len(decodeList(t, out)) != 2 {
		t.Errorf("code %d, %d checks, stderr %s, stdout %s", code, sc.total(), errs, out)
	}
}

func TestHistoryFromBranch(t *testing.T) {
	var c calls
	h := historyHarness(t, map[string]http.HandlerFunc{"GET " + historyPath: c.wrap(jsonHandler(historyBody("")))})
	gitBranch(t, h.env.Dir, "chore/sc-5818/x")
	code, out, errs := h.run("story", "history")
	if code != 0 || c.count(historyPath) != 1 || strings.TrimSpace(out) != "[]" {
		t.Errorf("code %d, stderr %s, stdout %s", code, errs, out)
	}
}

func TestHistoryNotFound(t *testing.T) {
	h := historyHarness(t, map[string]http.HandlerFunc{
		"GET " + historyPath: func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"message":"Resource not found."}`))
		},
	})
	if code, _, errs := h.run("story", "history", "5818"); code != 3 {
		t.Errorf("code %d, stderr %s", code, errs)
	}
}

func TestHistoryBadForm(t *testing.T) {
	many := []string{}
	for i := range 101 {
		many = append(many, strconv.Itoa(1000+i))
	}
	h := newHarness(t, nil, "", searchEnv)
	for _, args := range [][]string{{"x"}, {"1", "--limit", "0"}, {"1", "--limit", "some"}, {}, many} {
		if code, _, errs := h.run(append([]string{"story", "history"}, args...)...); code != 2 {
			t.Errorf("%.40v: code %d, stderr %s", args, code, errs)
		}
	}
}

// prismPager proxies to Prism. It adds a cursor to the first page, so the
// command asks for a second page, and Prism checks that request too.
func prismPager(t *testing.T, c *calls) http.HandlerFunc {
	prism, err := url.Parse(testapi.Prism(t))
	if err != nil {
		t.Fatal(err)
	}
	p := httputil.NewSingleHostReverseProxy(prism)
	p.ModifyResponse = func(resp *http.Response) error {
		if resp.StatusCode != 200 || resp.Request.URL.Query().Has("cursor") {
			return nil
		}
		b, err := io.ReadAll(resp.Body)
		if err != nil {
			return err
		}
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			return err
		}
		m["next_page_url"] = resp.Request.URL.Path + "?cursor=page2"
		b, _ = json.Marshal(m)
		resp.Body = io.NopCloser(bytes.NewReader(b))
		resp.ContentLength = int64(len(b))
		resp.Header.Set("Content-Length", strconv.Itoa(len(b)))
		return nil
	}
	return c.wrap(p.ServeHTTP)
}

func TestHistoryContract(t *testing.T) {
	var c calls
	pager := prismPager(t, &c)
	h := newHarness(t, map[string]http.HandlerFunc{
		"GET " + historyPath:     pager,
		"GET " + manyHistoryPath: pager,
		"GET /api/v4/acme/":      pager,
	}, "", searchEnv)
	code, out, errs := h.run("story", "history", "5818", "--fields", "workflow_state,owners", "--desc", "--limit", "all")
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	if n := len(decodeList(t, out)); n < 2 || c.count(historyPath) != 2 || c.param(historyPath, "cursor") != "page2" {
		t.Errorf("%d items, %d calls, last query %q", n, c.count(historyPath), c.query[historyPath])
	}
	// Prism makes up null entities and stories, so exit 3 (missing stories) is fine.
	code, out, errs = h.run("story", "history", "5818", "5819", "--limit", "all")
	if code != 0 && code != 3 {
		t.Fatalf("many: code %d, stderr %s", code, errs)
	}
	if n := len(decodeList(t, out)); n < 2 || c.count(manyHistoryPath) != 2 {
		t.Errorf("many: %d items, %d calls", n, c.count(manyHistoryPath))
	}
}
