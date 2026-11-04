package cli_test

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/nono/scut/internal/testapi"
)

const searchPath = "/api/v4/acme/stories/search"

var searchEnv = map[string]string{"SCUT_TOKEN": "sct_rw_acme_s3cret"}

func membersRoute() http.HandlerFunc {
	return jsonHandler(listBody(map[string]any{"id": testMember, "name": "Test User", "mention_name": "testuser"}))
}

func TestSearchQueryUnchanged(t *testing.T) {
	var c calls
	h := newHarness(t, map[string]http.HandlerFunc{
		"GET " + searchPath:        c.wrap(jsonHandler(listBody(storyFixture(t)))),
		"GET /api/v4/acme/members": membersRoute(),
	}, "", searchEnv)
	code, out, errs := h.run("story", "search", "!is:done epic:5387")
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	if got := c.param(searchPath, "query"); got != "!is:done epic:5387" {
		t.Errorf("query %q", got)
	}
	if l := decodeList(t, out); len(l) != 1 || l[0]["id"] != float64(5818) {
		t.Errorf("stdout %s", out)
	}
}

func TestSearchNoQuery(t *testing.T) {
	h := newHarness(t, nil, "", nil)
	if code, _, errs := h.run("story", "search"); code != 2 {
		t.Errorf("code %d, stderr %s", code, errs)
	}
	if code, _, errs := h.run("story", "search", "x", "--limit", "0"); code != 2 {
		t.Errorf("limit 0: code %d, stderr %s", code, errs)
	}
	if code, _, errs := h.run("story", "search", "x", "--fields", "bogus"); code != 2 {
		t.Errorf("bad field: code %d, stderr %s", code, errs)
	}
}

func TestSearchNoDescription(t *testing.T) {
	var c calls
	h := newHarness(t, map[string]http.HandlerFunc{
		"GET " + searchPath:        c.wrap(jsonHandler(listBody(storyFixture(t)))),
		"GET /api/v4/acme/members": membersRoute(),
	}, "", searchEnv)
	code, out, errs := h.run("story", "search", "x")
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	l := decodeList(t, out)
	if _, ok := l[0]["description"]; ok {
		t.Errorf("description in %s", out)
	}
	if _, ok := l[0]["comments"]; ok {
		t.Errorf("comments in %s", out)
	}
	f := c.param(searchPath, "fields")
	if f == "" || strings.Contains(f, "description") || strings.Contains(f, "comments") || !strings.Contains(f, "workflow_state") {
		t.Errorf("fields %q", f)
	}
	if strings.Count(f, "custom_field_values") != 1 {
		t.Errorf("fields %q", f)
	}
	if _, ok := l[0]["field_ids"]; !ok {
		t.Errorf("no field_ids in %s", out)
	}
	if got := c.param(searchPath, "limit"); got != "25" {
		t.Errorf("limit %q", got)
	}
}

func TestSearchFieldsGiven(t *testing.T) {
	var c calls
	h := newHarness(t, map[string]http.HandlerFunc{
		"GET " + searchPath:        c.wrap(jsonHandler(listBody(storyFixture(t)))),
		"GET /api/v4/acme/members": membersRoute(),
	}, "", searchEnv)
	if code, _, errs := h.run("story", "search", "x", "--fields", "name,state", "--limit", "3"); code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	if got := c.param(searchPath, "fields"); got != "id,name,workflow_state" {
		t.Errorf("fields %q", got)
	}
	if got := c.param(searchPath, "limit"); got != "3" {
		t.Errorf("limit %q", got)
	}
}

func TestSearchFieldsDescriptionComments(t *testing.T) {
	h := newHarness(t, map[string]http.HandlerFunc{
		"GET " + searchPath:        jsonHandler(listBody(storyFixture(t))),
		"GET /api/v4/acme/members": membersRoute(),
	}, "", searchEnv)
	code, out, errs := h.run("story", "search", "x", "--fields", "name,description,comments")
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	l := decodeList(t, out)
	if _, ok := l[0]["description"].(string); !ok {
		t.Errorf("no description in %s", out)
	}
	if _, ok := l[0]["comments"].([]any); !ok {
		t.Errorf("no comments in %s", out)
	}
	code, out, errs = h.run("story", "search", "x", "--fields", "name,description")
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	l = decodeList(t, out)
	if _, ok := l[0]["description"].(string); !ok {
		t.Errorf("no description in %s", out)
	}
	if _, ok := l[0]["comments"]; ok {
		t.Errorf("comments not asked for, in %s", out)
	}
}

// pagedHandler serves pages of 25 stories; page n links to page n+1 until last.
// pagedHandler serves total stories in pages of the limit parameter of the
// first call (the API default of 10 without it), as the API does.
func pagedHandler(t *testing.T, c *calls, total int) http.HandlerFunc {
	return c.wrap(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Has("cursor") && len(q) > 1 {
			w.WriteHeader(422)
			_, _ = fmt.Fprint(w, `{"message":"Other parameters can't be used with cursor."}`)
			return
		}
		offset, size := 0, 10
		if cur := q.Get("cursor"); cur != "" {
			_, _ = fmt.Sscanf(cur, "o%ds%d", &offset, &size)
		} else if l := q.Get("limit"); l != "" {
			size, _ = strconv.Atoi(l)
		}
		if size < 1 || size > 100 {
			w.WriteHeader(400)
			return
		}
		var items []any
		for i := offset; i < min(total, offset+size); i++ {
			s := storyFixture(t)
			s["id"] = 1000 + i
			items = append(items, s)
		}
		body := listBody(items...)
		if offset+size < total {
			body = strings.TrimSuffix(body, "}") +
				fmt.Sprintf(`,"next_page_url":"/api/v4/acme/stories/search?cursor=o%ds%d"}`, offset+size, size)
		}
		jsonHandler(body)(w, r)
	})
}

func TestSearchLimitAll(t *testing.T) {
	var c calls
	h := newHarness(t, map[string]http.HandlerFunc{
		"GET " + searchPath:        pagedHandler(t, &c, 250),
		"GET /api/v4/acme/members": membersRoute(),
	}, "", searchEnv)
	code, out, errs := h.run("story", "search", "x", "--limit", "all")
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	if n := len(decodeList(t, out)); n != 250 {
		t.Errorf("%d items, want 250", n)
	}
	if got := c.count(searchPath); got != 3 {
		t.Errorf("%d calls", got)
	}
	if got := c.firstParam(searchPath, "limit"); got != "100" {
		t.Errorf("limit %q", got)
	}
}

// A repeated cursor stops the list: stdout has the items read, stderr the error.
func TestSearchCursorRepeats(t *testing.T) {
	body := strings.TrimSuffix(listBody(storyFixture(t)), "}") + `,"next_page_url":"/api/v4/acme/stories/search?cursor=same"}`
	h := newHarness(t, map[string]http.HandlerFunc{
		"GET " + searchPath:        jsonHandler(body),
		"GET /api/v4/acme/members": membersRoute(),
	}, "", searchEnv)
	code, out, errs := h.run("story", "search", "x", "--limit", "all")
	if code != 1 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	if n := len(decodeList(t, out)); n != 2 {
		t.Errorf("%d items, want 2", n)
	}
	if e := decode(t, errs); e["error"] != "stopped after 2 pages: the cursor repeats" {
		t.Errorf("stderr %s", errs)
	}
}

// A page holds up to 100 items: --limit N makes ceil(N/100) calls.
func TestSearchLimitStops(t *testing.T) {
	for _, tc := range []struct {
		limit       string
		items, call int
		size        string
	}{{"30", 30, 1, "30"}, {"100", 100, 1, "100"}, {"150", 150, 2, "100"}} {
		var c calls
		h := newHarness(t, map[string]http.HandlerFunc{
			"GET " + searchPath:        pagedHandler(t, &c, 300),
			"GET /api/v4/acme/members": membersRoute(),
		}, "", searchEnv)
		code, out, errs := h.run("story", "search", "x", "--limit", tc.limit)
		if code != 0 {
			t.Fatalf("code %d, stderr %s", code, errs)
		}
		if n := len(decodeList(t, out)); n != tc.items || c.count(searchPath) != tc.call || c.firstParam(searchPath, "limit") != tc.size {
			t.Errorf("--limit %s: %d items, %d calls, limit %q", tc.limit, n, c.count(searchPath), c.firstParam(searchPath, "limit"))
		}
	}
}

func TestCommentsPaging(t *testing.T) {
	var c calls
	p := "/api/v4/acme/stories/5818/comments"
	h := newHarness(t, map[string]http.HandlerFunc{
		"GET " + p:                 pagedHandler(t, &c, 150),
		"GET /api/v4/acme/members": membersRoute(),
	}, "", searchEnv)
	code, out, errs := h.run("story", "comments", "5818", "--limit", "all")
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	if n := len(decodeList(t, out)); n != 150 || c.count(p) != 2 {
		t.Errorf("%d items, %d calls, want 150 and 2", n, c.count(p))
	}
}

func TestSearchHelpLag(t *testing.T) {
	h := newHarness(t, nil, "", nil)
	_, out, _ := h.run("story", "search", "--help")
	if !strings.Contains(out, "search can lag; use story view for current data") {
		t.Errorf("help: %s", out)
	}
}

func TestCommentsAuthorDate(t *testing.T) {
	var c calls
	h := newHarness(t, map[string]http.HandlerFunc{
		"GET /api/v4/acme/stories/5818/comments": c.wrap(jsonHandler(listBody(
			comment(1, "hello", false), map[string]any{"id": 2, "deleted": true}))),
		"GET /api/v4/acme/members": membersRoute(),
	}, "", searchEnv)
	code, out, errs := h.run("story", "comments", "5818")
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	l := decodeList(t, out)
	if len(l) != 2 {
		t.Fatalf("stdout %s", out)
	}
	a, _ := l[0]["author"].(map[string]any)
	if a["mention_name"] != "testuser" || a["id"] != testMember || l[0]["created_at"] != "2026-11-04T10:30:00Z" || l[0]["text"] != "hello" {
		t.Errorf("comment: %v", l[0])
	}
	if l[1]["deleted"] != true || l[1]["id"] != float64(2) {
		t.Errorf("deleted: %v", l[1])
	}
	if got := c.param("/api/v4/acme/stories/5818/comments", "limit"); got != "25" {
		t.Errorf("limit %q", got)
	}
}

func TestCommentsBadStory(t *testing.T) {
	h := newHarness(t, nil, "", nil)
	if code, _, _ := h.run("story", "comments", "nope"); code != 2 {
		t.Errorf("code %d", code)
	}
}

func TestSearchContract(t *testing.T) {
	h := newHarness(t, nil, "", searchEnv)
	h.env.APIBase = testapi.Prism(t)
	code, out, errs := h.run("story", "search", "epic:1")
	if code != 0 {
		t.Fatalf("search: code %d, stderr %s", code, errs)
	}
	decodeList(t, out)
	code, out, errs = h.run("story", "comments", "5818")
	if code != 0 {
		t.Fatalf("comments: code %d, stderr %s", code, errs)
	}
	decodeList(t, out)
}
