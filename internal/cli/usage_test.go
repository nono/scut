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

// "<command> usage" prints the usage of the command when "usage" is not a
// valid argument of it: then the command fails with a usage error. Where
// "usage" is valid (a search query, a name, a path), the command runs as usual.
func TestUsageAfterCommand(t *testing.T) {
	h := newHarness(t, nil, "", nil)
	printed := map[string]bool{}
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
			switch code {
			case 0:
				if !strings.HasPrefix(out, path) || !strings.Contains(out, s.Short) || strings.Contains(out, "--help") {
					t.Errorf("%s usage: stdout %q", path, out)
				}
				printed[path] = true
			case 2:
				t.Errorf("%s usage: code 2, stderr %s", path, errs)
			}
		}
	}
	walk(cli.NewRoot(emptyEnv()))
	for _, p := range []string{"story create", "story view", "story update", "worktree", "whoami", "auth login"} {
		if !printed[p] {
			t.Errorf("%s usage: no usage", p)
		}
	}
	for _, p := range []string{"story search", "api"} {
		if printed[p] {
			t.Errorf("%s usage: usage printed, want a run with the argument", p)
		}
	}
	code, out, _ := h.run("story", "create", "usage")
	if code != 0 || !strings.Contains(out, "--name string") || strings.Contains(out, "story view") {
		t.Errorf("story create usage: code %d, stdout %q", code, out)
	}
}
