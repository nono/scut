package cli_test

import (
	"fmt"
	"net/http"
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

// pagedHandler serves pages of 25 stories; page n links to page n+1 until last.
func pagedHandler(t *testing.T, c *calls, pages int) http.HandlerFunc {
	return c.wrap(func(w http.ResponseWriter, r *http.Request) {
		page := 1
		if cur := r.URL.Query().Get("cursor"); cur != "" {
			_, _ = fmt.Sscanf(cur, "p%d", &page)
		}
		items := make([]any, 25)
		for i := range items {
			s := storyFixture(t)
			s["id"] = page*100 + i
			items[i] = s
		}
		body := listBody(items...)
		if page < pages {
			body = strings.TrimSuffix(body, "}") +
				fmt.Sprintf(`,"next_page_url":"/api/v4/acme/stories/search?query=x&cursor=p%d"}`, page+1)
		}
		jsonHandler(body)(w, r)
	})
}

func TestSearchLimitAll(t *testing.T) {
	var c calls
	h := newHarness(t, map[string]http.HandlerFunc{
		"GET " + searchPath:        pagedHandler(t, &c, 3),
		"GET /api/v4/acme/members": membersRoute(),
	}, "", searchEnv)
	code, out, errs := h.run("story", "search", "x", "--limit", "all")
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	if n := len(decodeList(t, out)); n != 75 {
		t.Errorf("%d items, want 75", n)
	}
	if got := c.count(searchPath); got != 3 {
		t.Errorf("%d calls", got)
	}
	if got := c.param(searchPath, "limit"); got != "100" {
		t.Errorf("limit %q", got)
	}
}

func TestSearchLimitStops(t *testing.T) {
	var c calls
	h := newHarness(t, map[string]http.HandlerFunc{
		"GET " + searchPath:        pagedHandler(t, &c, 3),
		"GET /api/v4/acme/members": membersRoute(),
	}, "", searchEnv)
	code, out, errs := h.run("story", "search", "x", "--limit", "30")
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	if n := len(decodeList(t, out)); n != 30 {
		t.Errorf("%d items, want 30", n)
	}
	if got := c.count(searchPath); got != 2 {
		t.Errorf("%d calls, want 2", got)
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
