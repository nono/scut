package cli_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/nono/scut/internal/testapi"
)

func epicFixture() map[string]any {
	return map[string]any{
		"id": 5387, "name": "Big epic", "description": "Epic text", "archived": false,
		"epic_state": map[string]any{"id": 7, "name": "In Progress", "type": "started"},
		"owners":     nestedList(map[string]any{"id": testMember, "name": "Test User"}),
		"app_url":    "https://app.shortcut.com/acme/epic/5387",
	}
}

func TestEpicView(t *testing.T) {
	h := newHarness(t, map[string]http.HandlerFunc{
		"GET /api/v4/acme/epics/5387": jsonHandler(jsonOf(map[string]any{"entity": epicFixture()})),
		"GET /api/v4/acme/members":    membersRoute(),
	}, "", searchEnv)
	code, out, errs := h.run("epic", "view", "5387")
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	m := decode(t, out)
	st, _ := m["state"].(map[string]any)
	owners, _ := m["owners"].([]any)
	if m["id"] != float64(5387) || m["name"] != "Big epic" || m["description"] != "Epic text" ||
		st["name"] != "In Progress" || len(owners) != 1 ||
		owners[0].(map[string]any)["mention_name"] != "testuser" ||
		m["app_url"] != "https://app.shortcut.com/acme/epic/5387" {
		t.Errorf("stdout %s", out)
	}
}

func TestEpicViewByName(t *testing.T) {
	var c calls
	h := newHarness(t, map[string]http.HandlerFunc{
		"GET /api/v4/acme/epics/search": c.wrap(jsonHandler(listBody(epicFixture()))),
		"GET /api/v4/acme/epics/5387":   jsonHandler(jsonOf(map[string]any{"entity": epicFixture()})),
		"GET /api/v4/acme/members":      membersRoute(),
	}, "", searchEnv)
	if code, _, errs := h.run("epic", "view", "Big epic"); code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	if got := c.param("/api/v4/acme/epics/search", "query"); got != `"Big epic"` {
		t.Errorf("query %q", got)
	}
}

func TestEpicSearch(t *testing.T) {
	var c calls
	e := epicFixture()
	h := newHarness(t, map[string]http.HandlerFunc{
		"GET /api/v4/acme/epics/search": c.wrap(jsonHandler(listBody(e))),
		"GET /api/v4/acme/members":      membersRoute(),
	}, "", searchEnv)
	code, out, errs := h.run("epic", "search", "state:started")
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	if got := c.param("/api/v4/acme/epics/search", "query"); got != "state:started" {
		t.Errorf("query %q", got)
	}
	l := decodeList(t, out)
	if len(l) != 1 || l[0]["id"] != float64(5387) {
		t.Errorf("stdout %s", out)
	}
	if _, ok := l[0]["description"]; ok {
		t.Errorf("description in search output: %s", out)
	}
	if code, _, _ := newHarness(t, nil, "", nil).run("epic", "search"); code != 2 {
		t.Errorf("no query: code %d", code)
	}
}

func TestEpicStoriesPositionOrder(t *testing.T) {
	var c calls
	p := "/api/v4/acme/epics/5387/stories"
	h := newHarness(t, map[string]http.HandlerFunc{
		"GET " + p:                 c.wrap(jsonHandler(listBody(storyFixture(t)))),
		"GET /api/v4/acme/members": membersRoute(),
	}, "", searchEnv)
	code, out, errs := h.run("epic", "stories", "5387")
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	if got := c.param(p, "order_by"); got != "position" {
		t.Errorf("order_by %q", got)
	}
	if got := c.param(p, "order_dir"); got != "asc" {
		t.Errorf("order_dir %q", got)
	}
	if got := c.param(p, "limit"); got != "25" {
		t.Errorf("limit %q", got)
	}
	if f := c.param(p, "fields"); f == "" || strings.Contains(f, "description") || strings.Contains(f, "comments") {
		t.Errorf("fields %q", f)
	}
	l := decodeList(t, out)
	if _, ok := l[0]["description"]; ok || len(l) != 1 {
		t.Errorf("stdout %s", out)
	}
	_, help, _ := newHarness(t, nil, "", nil).run("epic", "stories", "--help")
	if !strings.Contains(help, "archived stories are left out") {
		t.Errorf("help: %s", help)
	}
}

func TestEpicStoriesFieldsAndAll(t *testing.T) {
	var c calls
	p := "/api/v4/acme/epics/5387/stories"
	h := newHarness(t, map[string]http.HandlerFunc{
		"GET " + p:                 pagedHandler(t, &c, 2),
		"GET /api/v4/acme/members": membersRoute(),
	}, "", searchEnv)
	code, out, errs := h.run("epic", "stories", "5387", "--limit", "all", "--fields", "name")
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	if n := len(decodeList(t, out)); n != 50 {
		t.Errorf("%d items", n)
	}
	if got := c.firstParam(p, "fields"); got != "id,name" {
		t.Errorf("fields %q", got)
	}
}

func TestEpicCommentStdin(t *testing.T) {
	var body []byte
	h := newHarness(t, map[string]http.HandlerFunc{
		"POST /api/v4/acme/epics/5387/comments": func(w http.ResponseWriter, r *http.Request) {
			body = make([]byte, r.ContentLength)
			_, _ = r.Body.Read(body)
			jsonHandler(jsonOf(map[string]any{"entity": comment(9, "hi there", false)}))(w, r)
		},
		"GET /api/v4/acme/members": membersRoute(),
	}, "hi there\n", searchEnv)
	code, out, errs := h.run("epic", "comment", "5387", "--body-file", "-")
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil || got["text"] != "hi there\n" || len(got) != 1 {
		t.Errorf("body %q", body)
	}
	if m := decode(t, out); m["id"] != float64(9) {
		t.Errorf("stdout %s", out)
	}
}

func TestEpicCommentEmpty(t *testing.T) {
	h := newHarness(t, nil, "  \n", searchEnv)
	if code, _, _ := h.run("epic", "comment", "5387", "--body-file", "-"); code != 2 {
		t.Errorf("empty: code %d", code)
	}
	if code, _, _ := h.run("epic", "comment", "5387"); code != 2 {
		t.Errorf("no flag: code %d", code)
	}
}

func TestEpicNoOwners(t *testing.T) {
	e := epicFixture()
	delete(e, "owners")
	h := newHarness(t, map[string]http.HandlerFunc{
		"GET /api/v4/acme/epics/5387": jsonHandler(jsonOf(map[string]any{"entity": e})),
	}, "", searchEnv)
	code, out, errs := h.run("epic", "view", "5387")
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	m := decode(t, out)
	if _, ok := m["owners"]; ok || m["owners_total"] != nil {
		t.Errorf("stdout %s", out)
	}
}

func TestEpicContract(t *testing.T) {
	h := newHarness(t, nil, "text", searchEnv)
	h.env.APIBase = testapi.Prism(t)
	for _, args := range [][]string{{"epic", "view", "5387"}, {"epic", "search", "x"}, {"epic", "stories", "5387"}, {"epic", "comment", "5387", "--body-file", "-"}} {
		h.env.Stdin = strings.NewReader("text")
		if code, _, errs := h.run(args...); code != 0 {
			t.Errorf("%v: code %d, stderr %s", args, code, errs)
		}
	}
}
