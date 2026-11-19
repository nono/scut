package cli_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/spf13/cobra"

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

// "<command> usage", with no other argument or flag, prints the usage of
// each command before the run: no token, no API call. With a flag, the
// command runs as usual and its errors stay.
func TestUsageAfterCommand(t *testing.T) {
	h := newHarness(t, nil, "", nil)
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		for _, s := range c.Commands() {
			if s.HasSubCommands() {
				walk(s)
				continue
			}
			if s.Hidden || s.Name() == "help" || s.Name() == "usage" {
				continue
			}
			path := strings.TrimPrefix(s.CommandPath(), "scut ")
			code, out, errs := h.run(append(strings.Fields(path), "usage")...)
			if code != 0 || !strings.HasPrefix(out, path) || !strings.Contains(out, s.Short) || strings.Contains(out, "--help") {
				t.Errorf("%s usage: code %d, stdout %q, stderr %s", path, code, out, errs)
			}
		}
	}
	walk(cli.NewRoot(emptyEnv()))
	code, out, _ := h.run("story", "create", "usage")
	if code != 0 || !strings.Contains(out, "--name string") || strings.Contains(out, "story view") {
		t.Errorf("story create usage: code %d, stdout %q", code, out)
	}
	for _, tc := range []struct {
		args []string
		code int
		err  string
	}{
		{[]string{"story", "search", "usage", "--limit", "0"}, 2, "--limit takes"},
		{[]string{"story", "search", "usage", "--bogus"}, 2, "unknown flag"},
		{[]string{"story", "create", "usage", "--type", "zzz"}, 2, "usage"},
		{[]string{"story", "search", "usage", "--limit", "5"}, 4, "no token"},
		{[]string{"--human", "whoami", "usage"}, 2, "takes no arguments"},
	} {
		code, out, errs := h.run(tc.args...)
		if code != tc.code || out != "" || !strings.Contains(errs, tc.err) {
			t.Errorf("%v: code %d, stdout %q, stderr %s", tc.args, code, out, errs)
		}
	}
}
