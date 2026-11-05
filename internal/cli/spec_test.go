package cli_test

import (
	"encoding/json"
	"strings"
	"testing"
)

// scut spec needs no token and no API.
func TestSpecOperation(t *testing.T) {
	h := newHarness(t, nil, "", nil)
	code, out, errOut := h.run("spec", "getStory", "--depth", "1")
	if code != 0 {
		t.Fatalf("code %d: %s", code, errOut)
	}
	var got struct {
		Method, Path string
		Parameters   map[string]string
		Response     map[string]any
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if got.Method != "GET" || got.Path != "stories/{story-public-id}" ||
		got.Parameters["story-public-id"] != "path integer(int64)" || got.Response["entity"] != "$Story" {
		t.Errorf("got %s", out)
	}
}

func TestSpecSchema(t *testing.T) {
	h := newHarness(t, nil, "", nil)
	code, out, errOut := h.run("spec", "WorkflowStateSlim")
	if code != 0 || !strings.Contains(out, `"schema": "WorkflowStateSlim"`) {
		t.Errorf("code %d: %s %s", code, out, errOut)
	}
}

func TestSpecErrors(t *testing.T) {
	h := newHarness(t, nil, "", nil)
	for _, c := range []struct {
		args []string
		code int
		msg  string
	}{
		{[]string{"spec", "noSuchThing"}, 3, "noSuchThing"},
		{[]string{"spec", "getStory", "--depth", "-1"}, 2, "--depth"},
		{[]string{"spec"}, 2, "arg"},
	} {
		code, _, errOut := h.run(c.args...)
		if code != c.code || !strings.Contains(errOut, c.msg) {
			t.Errorf("%v: code %d, %s", c.args, code, errOut)
		}
	}
}
