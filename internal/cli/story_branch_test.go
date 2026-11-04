package cli_test

import (
	"net/http"
	"strings"
	"testing"
)

const branchPath = "/api/v4/acme/stories/5818"

func branchRun(t *testing.T, body string) (int, string, string) {
	t.Helper()
	var c calls
	h := newHarness(t, map[string]http.HandlerFunc{
		"GET " + branchPath: c.wrap(jsonHandler(body)),
	}, "", searchEnv)
	code, out, errs := h.run("story", "branch", "5818")
	if code == 0 || code == 3 {
		if got := c.param(branchPath, "fields"); got != "formatted_vcs_branch_name" {
			t.Errorf("fields %q", got)
		}
	}
	return code, out, errs
}

func TestBranchNull(t *testing.T) {
	code, out, errs := branchRun(t, `{"id":5818,"formatted_vcs_branch_name":null}`)
	if code != 3 {
		t.Fatalf("code %d, stdout %s, stderr %s", code, out, errs)
	}
	if !strings.Contains(errs, "no branch name format") {
		t.Errorf("stderr %s", errs)
	}
}

func TestBranchOK(t *testing.T) {
	code, out, errs := branchRun(t, `{"id":5818,"formatted_vcs_branch_name":"nono/sc-5818/fix-it"}`)
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	m := decode(t, out)
	if m["story"] != float64(5818) || m["branch"] != "nono/sc-5818/fix-it" {
		t.Errorf("stdout %s", out)
	}
}

func TestBranchStripTrailing(t *testing.T) {
	for in, want := range map[string]string{"a/sc-1/x-": "a/sc-1/x", "a/sc-1/": "a/sc-1", "a/b": "a/b"} {
		code, out, errs := branchRun(t, `{"id":5818,"formatted_vcs_branch_name":"`+in+`"}`)
		if code != 0 {
			t.Fatalf("%q: code %d, stderr %s", in, code, errs)
		}
		if got := decode(t, out)["branch"]; got != want {
			t.Errorf("%q: branch %v, want %q", in, got, want)
		}
	}
}
