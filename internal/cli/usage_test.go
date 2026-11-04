package cli_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/nono/scut/internal/cli"
)

func TestUsageSmall(t *testing.T) {
	// No token, and a bad output setting: usage must not load the settings.
	h := newHarness(t, nil, "", map[string]string{"SCUT_OUTPUT": "bogus"})
	var out, errb bytes.Buffer
	root := storyRoot(h.env)
	root.SetOut(&out)
	if code := cli.RunContext(context.Background(), root, []string{"usage"}, &errb); code != 0 {
		t.Fatalf("code %d, stderr %s", code, errb.String())
	}
	if n := len(out.String()); n > 1000 {
		t.Errorf("%d characters, want at most 1000", n)
	}
	for _, noun := range []string{"auth", "whoami", "api", "story", "Run scut <noun> usage for the commands of a noun."} {
		if !strings.Contains(out.String(), noun) {
			t.Errorf("missing %q in %q", noun, out.String())
		}
	}
	if strings.Contains(out.String(), "completion") || strings.Contains(out.String(), "help ") {
		t.Errorf("noise in %q", out.String())
	}
}

func TestNounUsage(t *testing.T) {
	h := newHarness(t, nil, "", map[string]string{"SCUT_OUTPUT": "bogus"})
	var out, errb bytes.Buffer
	root := storyRoot(h.env)
	root.SetOut(&out)
	if code := cli.RunContext(context.Background(), root, []string{"story", "usage"}, &errb); code != 0 {
		t.Fatalf("code %d, stderr %s", code, errb.String())
	}
	for _, want := range []string{"story create", "Create a story", "--name", "string", "story name", "--estimate", "int", "story points"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q in %q", want, out.String())
		}
	}
	if strings.Contains(out.String(), "story usage") {
		t.Errorf("usage lists itself: %q", out.String())
	}
}

func TestAuthUsage(t *testing.T) {
	h := newHarness(t, nil, "", nil)
	code, out, errs := h.run("auth", "usage")
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	if !strings.Contains(out, "auth login") || !strings.Contains(out, "auth status") {
		t.Errorf("out %q", out)
	}
}

func TestGroupAloneNeedsNoToken(t *testing.T) {
	h := newHarness(t, nil, "", map[string]string{"SCUT_OUTPUT": "bogus"})
	var out, errb bytes.Buffer
	root := storyRoot(h.env)
	root.SetOut(&out)
	code := cli.RunContext(context.Background(), root, []string{"story"}, &errb)
	if code != 2 || !strings.Contains(errb.String(), "needs a subcommand") {
		t.Fatalf("code %d, stderr %s", code, errb.String())
	}
}

func TestOpsAnnotations(t *testing.T) {
	root := cli.NewRoot(emptyEnv())
	want := map[string]string{"whoami": "getWhoami", "api": "*", "auth login": "getWhoami", "auth status": "getWhoami"}
	for path, ops := range want {
		cmd, _, err := root.Find(strings.Fields(path))
		if err != nil || cmd.Annotations["scut/ops"] != ops {
			t.Errorf("%s: %v %v", path, err, cmd.Annotations)
		}
	}
}
