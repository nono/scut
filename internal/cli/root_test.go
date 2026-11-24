package cli_test

import (
	"bytes"
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/nono/scut/internal/cli"
	"github.com/nono/scut/internal/exit"
	"github.com/nono/scut/internal/transport"
)

func emptyEnv() cli.Env {
	return cli.Env{Getenv: func(string) string { return "" }, Dir: "."}
}

func TestHelpNeedsNoToken(t *testing.T) {
	var out, errb bytes.Buffer
	if code := cli.Execute([]string{"--help"}, &out, &errb, emptyEnv()); code != 0 {
		t.Fatalf("code %d, want 0", code)
	}
	if errb.Len() != 0 {
		t.Errorf("stderr not empty: %q", errb.String())
	}
}

func TestGroupWithoutSubcommand(t *testing.T) {
	var out, errb bytes.Buffer
	root := cli.NewRoot(emptyEnv())
	root.AddCommand(cli.Group("story", "stories"))
	root.SetOut(&out)
	if code := cli.RunContext(context.Background(), root, []string{"story"}, &errb); code != 2 {
		t.Fatalf("code %d, want 2", code)
	}
	if !strings.Contains(errb.String(), `"error"`) {
		t.Errorf("stderr: %q", errb.String())
	}
}

// usageError runs args on root and returns the JSON error; it fails unless the code is 2.
func usageError(t *testing.T, root *cobra.Command, args ...string) map[string]any {
	t.Helper()
	var out, errb bytes.Buffer
	root.SetOut(&out)
	if code := cli.RunContext(context.Background(), root, args, &errb); code != 2 {
		t.Fatalf("%v: code %d, want 2; stderr %s", args, code, errb.String())
	}
	return decode(t, errb.String())
}

func hasValid(e map[string]any, want ...string) bool {
	valid, _ := e["valid"].([]any)
	for _, w := range want {
		if !slices.Contains(valid, any(w)) {
			return false
		}
	}
	return true
}

// Errors from the command tree name the fix: the valid subcommands or flags, and how to get help.
func TestUsageErrorsNameTheFix(t *testing.T) {
	for _, c := range []struct {
		args       []string
		valid      []string
		suggestion string
	}{
		{[]string{"stroy"}, []string{"story", "epic"}, `did you mean "story"?`},
		{[]string{"--nope"}, []string{"--human", "--version"}, `see "scut --help"`},
		{[]string{"--profile"}, nil, `see "scut --help"`},
		{[]string{"story", "veiw"}, []string{"view", "comment"}, `did you mean "view"?`},
		{[]string{"story", "nothing-like-it"}, []string{"view"}, `run "scut story usage"`},
		{[]string{"story"}, []string{"view", "update"}, `run "scut story usage"`},
		{[]string{"nothing-like-it"}, []string{"story", "epic"}, `run "scut usage"`},
		// A story reference after the story group: story view.
		{[]string{"story", "1234"}, []string{"view"}, `did you mean "scut story view 1234"?`},
		{[]string{"story", "sc-1234", "--fields", "name"}, []string{"view"}, `did you mean "scut story view sc-1234"?`},
		// A verb of a noun: each noun command with that verb.
		{[]string{"search", "foo"}, []string{"story"}, `did you mean "scut doc search", "scut epic search" or "scut story search"?`},
		{[]string{"get", "1234"}, []string{"story"}, `did you mean "scut story view"?`},
		{[]string{"show"}, []string{"story"}, `did you mean "scut story view"?`},
		{[]string{"story", "search"}, nil, "usage: scut story search <query>"},
		{[]string{"workflow", "states", "a", "b"}, nil, "usage: scut workflow states [<workflow>]"},
		{[]string{"story", "view", "1", "--feilds", "id"}, []string{"--fields", "--comments", "--human"}, `see "scut story view --help"`},
		{[]string{"story", "view", "1", "-z"}, []string{"--fields"}, `see "scut story view --help"`},
		{[]string{"spec", "Story", "--depth", "x"}, nil, `see "scut spec --help"`},
		// A leaf that takes no arguments: extra ones are a wrong count, not a command.
		{[]string{"whoami", "extra"}, nil, "usage: scut whoami"},
		{[]string{"team", "list", "x"}, nil, "usage: scut team list"},
		// An Args check that returns its own usage error still gets the usage line.
		{[]string{"story", "link", "1", "blocks"}, nil, "usage: scut story link <story> <verb> <story> [<verb> <story>]..."},
		// A mistyped subcommand before a flag is an unknown command, not an unknown flag of the group.
		{[]string{"story", "veiw", "1", "--fields", "name"}, []string{"view"}, `did you mean "view"?`},
		{[]string{"story", "--yes"}, []string{"--human"}, `see "scut story --help"`},
		// An empty name matches every name by prefix: no guess.
		{[]string{"story", ""}, []string{"view"}, `run "scut story usage"`},
	} {
		e := usageError(t, cli.NewRoot(emptyEnv()), c.args...)
		if !hasValid(e, c.valid...) {
			t.Errorf("%v: valid %v, want %v", c.args, e["valid"], c.valid)
		}
		if c.valid == nil && e["valid"] != nil {
			t.Errorf("%v: valid %v, want none", c.args, e["valid"])
		}
		if e["suggestion"] != c.suggestion {
			t.Errorf("%v: suggestion %q, want %q", c.args, e["suggestion"], c.suggestion)
		}
	}
}

// A flag with a known set of values gives the values and an example when its
// value is missing or not valid.
func TestKnownValuesNameTheFix(t *testing.T) {
	types := []string{"feature", "chore", "bug"}
	formats := []string{"markdown", "html"}
	methods := []string{"GET", "POST", "PUT", "PATCH", "DELETE"}
	for _, c := range []struct {
		args    []string
		valid   []string
		example string
	}{
		{[]string{"story", "create", "--name", "n", "--type", "epic"}, types, "--type feature"},
		{[]string{"doc", "view", "x", "--format", "pdf"}, formats, "--format markdown"},
		{[]string{"api", "/x", "-X"}, methods, "--method GET"},
		{[]string{"api", "/x", "-X", "--human"}, methods, "--method GET"},
		{[]string{"api", "/x", "-X", "HEAD"}, methods, "--method GET"},
		{[]string{"story", "search", "q", "--limit", "0"}, []string{"<number>", "all"}, "--limit all"},
		{[]string{"story", "view", "1", "--comments", "some"}, []string{"<number>", "all", "none"}, "--comments all"},
		// A missing value takes the next local flag as the value.
		{[]string{"story", "create", "--type", "--name=n"}, types, "--type feature"},
		{[]string{"story", "search", "--limit", "--fields", "name", "q"}, []string{"<number>", "all"}, "--limit all"},
	} {
		e := usageError(t, cli.NewRoot(emptyEnv()), c.args...)
		if jsonOf(e["valid"]) != jsonOf(c.valid) || e["suggestion"] != fmt.Sprintf("for example %q", c.example) {
			t.Errorf("%v: %v", c.args, e)
		}
	}
}

// A value that starts with - and is not a flag is a bad value, not a missing one.
func TestDashValueIsAValue(t *testing.T) {
	e := usageError(t, cli.NewRoot(emptyEnv()), "story", "search", "q", "--limit", "-1")
	if e["error"] != `--limit takes a number above 0 or "all", not "-1"` {
		t.Errorf("%v", e)
	}
}

// Each flag with a known set of values gives them when its value is missing,
// also when pflag takes the next flag as the value.
func TestMissingValueOfEachKnownSetFlag(t *testing.T) {
	want := map[string]struct {
		values  []string
		example string
	}{
		"limit":    {[]string{"<number>", "all"}, "all"},
		"comments": {[]string{"<number>", "all", "none"}, "all"},
		"type":     {[]string{"feature", "chore", "bug"}, "feature"},
		"format":   {[]string{"markdown", "html"}, "markdown"},
		"method":   {[]string{"GET", "POST", "PUT", "PATCH", "DELETE"}, "GET"},
	}
	seen := map[string]bool{}
	for _, c := range leaves(cli.NewRoot(emptyEnv())) {
		path := strings.Fields(c.CommandPath())[1:]
		c.LocalFlags().VisitAll(func(f *pflag.Flag) {
			w, ok := want[f.Name]
			if !ok {
				return
			}
			seen[f.Name] = true
			example := fmt.Sprintf("for example %q", "--"+f.Name+" "+w.example)
			for _, next := range [][]string{nil, {"--human"}, {"--profile=x"}, {"-h"}} {
				args := append(append(slices.Clone(path), "--"+f.Name), next...)
				if e := usageError(t, cli.NewRoot(emptyEnv()), args...); e["error"] != "flag needs an argument: --"+f.Name || jsonOf(e["valid"]) != jsonOf(w.values) || e["suggestion"] != example {
					t.Errorf("%v: %v", args, e)
				}
			}
		})
	}
	for name := range want {
		if !seen[name] {
			t.Errorf("no --%s flag", name)
		}
	}
}

// A missing --fields value gives the same names as an unknown field.
func TestMissingFieldsValue(t *testing.T) {
	known := usageError(t, cli.NewRoot(emptyEnv()), "story", "view", "1", "--fields", "nmae")["valid"]
	for _, args := range [][]string{
		{"story", "view", "1", "--fields"}, {"story", "search", "q", "--fields"}, {"story", "subtasks", "1", "--fields"}, {"epic", "stories", "1", "--fields"},
		// Only --comments needs a single story.
		{"story", "view", "1", "2", "--fields"},
		{"story", "view", "1", "--fields", "nmae"},
	} {
		e := usageError(t, cli.NewRoot(emptyEnv()), args...)
		if jsonOf(e["valid"]) != jsonOf(known) || e["suggestion"] != `for example "--fields app_url"` {
			t.Errorf("%v: %v", args, e)
		}
	}
}

// The generic suggestion of a group, run "<group> usage", runs.
func TestEachGroupHasUsage(t *testing.T) {
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		if !c.HasSubCommands() {
			return
		}
		h := newHarness(t, nil, "", nil)
		args := append(slices.Clone(strings.Fields(c.CommandPath())[1:]), "usage")
		if code, out, errs := h.run(args...); code != 0 || out == "" {
			t.Errorf("%v: code %d, stderr %s", args, code, errs)
		}
		for _, s := range c.Commands() {
			walk(s)
		}
	}
	walk(cli.NewRoot(emptyEnv()))
}

// help on a topic that does not exist is the unknown-command error, not the root help.
func TestHelpUnknownTopic(t *testing.T) {
	for _, c := range []struct {
		args       []string
		valid      string
		suggestion string
	}{
		{[]string{"help", "stroy"}, "story", `did you mean "story"?`},
		{[]string{"help", "story", "veiw"}, "view", `did you mean "view"?`},
	} {
		e := usageError(t, cli.NewRoot(emptyEnv()), c.args...)
		if !hasValid(e, c.valid) || e["suggestion"] != c.suggestion {
			t.Errorf("%v: %v", c.args, e)
		}
	}
	// Words after a leaf are its arguments, as in the command line the user wants to run.
	h := newHarness(t, nil, "", nil)
	for _, args := range [][]string{{"help"}, {"help", "story"}, {"help", "story", "view"}, {"help", "story", "view", "1"}, {"help", "api", "/stories"}} {
		if code, out, errs := h.run(args...); code != 0 || out == "" || errs != "" {
			t.Errorf("%v: code %d, stdout %d bytes, stderr %q", args, code, len(out), errs)
		}
	}
}

// Hidden commands, hidden flags and the help command are not in valid.
func TestValidLeavesOutHidden(t *testing.T) {
	root := cli.NewRoot(emptyEnv())
	root.AddCommand(&cobra.Command{Use: "secret", Hidden: true, Run: func(*cobra.Command, []string) {}})
	leaf := &cobra.Command{Use: "leaf", Run: func(*cobra.Command, []string) {}}
	leaf.Flags().Bool("shown", false, "")
	leaf.Flags().Bool("unseen", false, "")
	_ = leaf.Flags().MarkHidden("unseen")
	root.AddCommand(leaf)
	e := usageError(t, root, "nothing-like-it")
	if !hasValid(e, "leaf") || hasValid(e, "secret") || hasValid(e, "help") {
		t.Errorf("commands: valid %v", e["valid"])
	}
	e = usageError(t, root, "leaf", "--nope")
	if !hasValid(e, "--shown") || hasValid(e, "--unseen") {
		t.Errorf("flags: valid %v", e["valid"])
	}
}

func TestCancelledIs130(t *testing.T) {
	var errb bytes.Buffer
	root := cli.NewRoot(emptyEnv())
	root.AddCommand(&cobra.Command{
		Use: "wait",
		RunE: func(cmd *cobra.Command, _ []string) error {
			<-cmd.Context().Done()
			return cmd.Context().Err()
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if code := cli.RunContext(ctx, root, []string{"wait"}, &errb); code != 130 {
		t.Fatalf("code %d, want 130", code)
	}
}

// After a cancel, an *exit.Error with code 130 is written, so the command can
// say what it did; any other error gives 130 with no output.
func TestCancelledWritesCancelledError(t *testing.T) {
	for name, c := range map[string]struct {
		err  error
		want string
	}{
		"cancelled": {&exit.Error{Code: exit.Cancelled, Msg: "made x"}, `{"error":"made x"}` + "\n"},
		"other":     {&exit.Error{Code: exit.Other, Msg: "git: signal: killed"}, ""},
	} {
		t.Run(name, func(t *testing.T) {
			var errb bytes.Buffer
			root := cli.NewRoot(emptyEnv())
			root.AddCommand(&cobra.Command{Use: "fail", RunE: func(*cobra.Command, []string) error { return c.err }})
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if code := cli.RunContext(ctx, root, []string{"fail"}, &errb); code != 130 || errb.String() != c.want {
				t.Errorf("code %d, stderr %q", code, errb.String())
			}
		})
	}
}

func TestArgCountIs2(t *testing.T) {
	h := newHarness(t, nil, "", map[string]string{"SCUT_TOKEN": "sct_rw_acme_s3cret"})
	for _, args := range [][]string{{"field", "values"}, {"api"}, {"story", "search"}, {"field", "values", "a", "b"}} {
		if code, _, errs := h.run(args...); code != 2 {
			t.Errorf("%v: code %d, stderr %s", args, code, errs)
		}
	}
}

func TestVersionNeedsNoToken(t *testing.T) {
	old := transport.Version
	transport.Version = "v1.2.3"
	defer func() { transport.Version = old }()
	var out, errb bytes.Buffer
	if code := cli.Execute([]string{"--version"}, &out, &errb, emptyEnv()); code != 0 {
		t.Fatalf("code %d, want 0; stderr %q", code, errb.String())
	}
	if !strings.Contains(out.String(), "v1.2.3") {
		t.Errorf("stdout: %q", out.String())
	}
}
