package cli_test

import (
	"net/http"
	"os/exec"
	"strings"
	"testing"

	"github.com/nono/scut/internal/testapi"
)

const subPath = "/api/v4/acme/stories/5428/sub-task-stories"

func TestSubtasksPositionOrder(t *testing.T) {
	var c calls
	h := newHarness(t, map[string]http.HandlerFunc{
		"GET " + subPath:           c.wrap(jsonHandler(listBody(storyFixture(t)))),
		"GET /api/v4/acme/members": membersRoute(),
	}, "", searchEnv)
	code, out, errs := h.run("story", "subtasks", "sc-5428")
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	if got := c.param(subPath, "order_by"); got != "position" {
		t.Errorf("order_by %q", got)
	}
	if got := c.param(subPath, "order_dir"); got != "asc" {
		t.Errorf("order_dir %q", got)
	}
	if got := c.param(subPath, "limit"); got != "25" {
		t.Errorf("limit %q", got)
	}
	if f := c.param(subPath, "fields"); f == "" || strings.Contains(f, "description") || strings.Contains(f, "comments") {
		t.Errorf("fields %q", f)
	}
	l := decodeList(t, out)
	if _, ok := l[0]["description"]; ok || len(l) != 1 || l[0]["id"] != float64(5818) {
		t.Errorf("stdout %s", out)
	}
	_, help, _ := newHarness(t, nil, "", nil).run("story", "subtasks", "--help")
	if !strings.Contains(help, "archived stories are left out") {
		t.Errorf("help: %s", help)
	}
}

func TestSubtasksFieldsAndAll(t *testing.T) {
	var c calls
	h := newHarness(t, map[string]http.HandlerFunc{
		"GET " + subPath:           pagedHandler(t, &c, 250),
		"GET /api/v4/acme/members": membersRoute(),
	}, "", searchEnv)
	code, out, errs := h.run("story", "subtasks", "5428", "--limit", "all", "--fields", "name")
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	if n := len(decodeList(t, out)); n != 250 || c.count(subPath) != 3 {
		t.Errorf("%d items, %d requests", n, c.count(subPath))
	}
	if got := c.firstParam(subPath, "fields"); got != "id,name" {
		t.Errorf("fields %q", got)
	}
}

func TestSubtasksFromBranch(t *testing.T) {
	var c calls
	h := newHarness(t, map[string]http.HandlerFunc{
		"GET " + subPath:           c.wrap(jsonHandler(listBody(storyFixture(t)))),
		"GET /api/v4/acme/members": membersRoute(),
	}, "", searchEnv)
	for _, args := range [][]string{{"init", "-q"}, {"checkout", "-q", "-b", "chore/sc-5428/x"},
		{"-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "--allow-empty", "-m", "x"}} {
		if out, err := exec.Command("git", append([]string{"-C", h.env.Dir}, args...)...).CombinedOutput(); err != nil {
			t.Skipf("git: %v %s", err, out)
		}
	}
	if code, _, errs := h.run("story", "subtasks"); code != 0 || c.total() != 1 {
		t.Errorf("code %d, calls %v, stderr %s", code, c.paths, errs)
	}
}

func TestSubtasksNotFound(t *testing.T) {
	h := newHarness(t, map[string]http.HandlerFunc{
		"GET " + subPath: func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"message":"Resource does not exist."}`))
		},
	}, "", searchEnv)
	code, out, errs := h.run("story", "subtasks", "5428")
	if code != 3 || out != "" || !strings.Contains(errs, `"status":404`) {
		t.Errorf("code %d, stdout %s, stderr %s", code, out, errs)
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
