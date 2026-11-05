package cli_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/nono/scut/internal/testapi"
)

const subPath = "/api/v4/acme/stories/5428/sub-task-stories"

// orderedLists are the commands that list stories in position order.
var orderedLists = []struct {
	args []string
	path string
}{
	{[]string{"epic", "stories", "5387"}, "/api/v4/acme/epics/5387/stories"},
	{[]string{"story", "subtasks", "sc-5428"}, subPath},
}

func TestOrderedListsPositionOrder(t *testing.T) {
	for _, tc := range orderedLists {
		var c calls
		h := newHarness(t, map[string]http.HandlerFunc{
			"GET " + tc.path:           c.wrap(jsonHandler(listBody(storyFixture(t)))),
			"GET /api/v4/acme/members": membersRoute(),
		}, "", searchEnv)
		code, out, errs := h.run(tc.args...)
		if code != 0 {
			t.Fatalf("%v: code %d, stderr %s", tc.args, code, errs)
		}
		for k, want := range map[string]string{"order_by": "position", "order_dir": "asc", "limit": "25"} {
			if got := c.param(tc.path, k); got != want {
				t.Errorf("%v: %s %q", tc.args, k, got)
			}
		}
		if f := c.param(tc.path, "fields"); f == "" || strings.Contains(f, "description") || strings.Contains(f, "comments") {
			t.Errorf("%v: fields %q", tc.args, f)
		}
		l := decodeList(t, out)
		if _, ok := l[0]["description"]; ok || len(l) != 1 || l[0]["id"] != float64(5818) {
			t.Errorf("%v: stdout %s", tc.args, out)
		}
		_, help, _ := newHarness(t, nil, "", nil).run(append(tc.args[:2:2], "--help")...)
		if !strings.Contains(help, "archived stories are left out") {
			t.Errorf("%v: help: %s", tc.args, help)
		}
	}
}

func TestOrderedListsFieldsAndAll(t *testing.T) {
	for _, tc := range orderedLists {
		var c calls
		h := newHarness(t, map[string]http.HandlerFunc{
			"GET " + tc.path:           pagedHandler(t, &c, 250),
			"GET /api/v4/acme/members": membersRoute(),
		}, "", searchEnv)
		code, out, errs := h.run(append(tc.args, "--limit", "all", "--fields", "name")...)
		if code != 0 {
			t.Fatalf("%v: code %d, stderr %s", tc.args, code, errs)
		}
		if n := len(decodeList(t, out)); n != 250 || c.count(tc.path) != 3 {
			t.Errorf("%v: %d items, %d requests", tc.args, n, c.count(tc.path))
		}
		if got := c.firstParam(tc.path, "fields"); got != "id,name" {
			t.Errorf("%v: fields %q", tc.args, got)
		}
	}
}

func TestOrderedListsNotFound(t *testing.T) {
	for _, tc := range orderedLists {
		h := newHarness(t, map[string]http.HandlerFunc{
			"GET " + tc.path: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(404)
				_, _ = w.Write([]byte(`{"message":"Resource does not exist."}`))
			},
		}, "", searchEnv)
		code, out, errs := h.run(tc.args...)
		if code != 3 || out != "" || !strings.Contains(errs, `"status":404`) {
			t.Errorf("%v: code %d, stdout %s, stderr %s", tc.args, code, out, errs)
		}
	}
}

// An error on a later page fails with no output (transport.Page).
func TestOrderedListsLaterPageError(t *testing.T) {
	for _, tc := range orderedLists {
		var c calls
		paged := pagedHandler(t, &c, 250)
		h := newHarness(t, map[string]http.HandlerFunc{
			"GET " + tc.path: func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Has("cursor") {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(500)
					_, _ = w.Write([]byte(`{"message":"boom"}`))
					return
				}
				paged(w, r)
			},
			"GET /api/v4/acme/members": membersRoute(),
		}, "", searchEnv)
		code, out, errs := h.run(append(tc.args, "--limit", "all")...)
		if code == 0 || out != "" || c.count(tc.path) != 1 || !strings.Contains(errs, `"status":500`) {
			t.Errorf("%v: code %d, stdout %s, stderr %s", tc.args, code, out, errs)
		}
	}
}

func TestSubtasksFromBranch(t *testing.T) {
	var c calls
	h := newHarness(t, map[string]http.HandlerFunc{
		"GET " + subPath:           c.wrap(jsonHandler(listBody(storyFixture(t)))),
		"GET /api/v4/acme/members": membersRoute(),
	}, "", searchEnv)
	gitBranch(t, h.env.Dir, "chore/sc-5428/x")
	if code, _, errs := h.run("story", "subtasks"); code != 0 || c.total() != 1 {
		t.Errorf("code %d, calls %v, stderr %s", code, c.paths, errs)
	}
}

func TestSubtasksBadForm(t *testing.T) {
	h := newHarness(t, nil, "", nil)
	for _, args := range [][]string{{"x"}, {"1", "2"}, {"1", "--limit", "0"}, {}} {
		if code, _, _ := h.run(append([]string{"story", "subtasks"}, args...)...); code != 2 {
			t.Errorf("%v: code %d, want 2", args, code)
		}
	}
}

func TestSubtasksContract(t *testing.T) {
	h := newHarness(t, nil, "", searchEnv)
	h.env.APIBase = testapi.Prism(t)
	if code, _, errs := h.run("story", "subtasks", "5428"); code != 0 {
		t.Errorf("code %d, stderr %s", code, errs)
	}
}
