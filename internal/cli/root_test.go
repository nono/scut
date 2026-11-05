package cli_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/spf13/cobra"

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

func TestUnknownCommandIs2(t *testing.T) {
	var out, errb bytes.Buffer
	if code := cli.Execute([]string{"story"}, &out, &errb, emptyEnv()); code != 2 {
		t.Fatalf("code %d, want 2", code)
	}
	if !strings.Contains(errb.String(), `"error"`) {
		t.Errorf("stderr: %q", errb.String())
	}
}

func TestBadFlagIs2(t *testing.T) {
	var out, errb bytes.Buffer
	if code := cli.Execute([]string{"--nope"}, &out, &errb, emptyEnv()); code != 2 {
		t.Fatalf("code %d, want 2", code)
	}
}

// usageError runs args and returns the JSON error; it fails unless the code is 2.
func usageError(t *testing.T, args ...string) map[string]any {
	t.Helper()
	var out, errb bytes.Buffer
	if code := cli.Execute(args, &out, &errb, emptyEnv()); code != 2 {
		t.Fatalf("%v: code %d, want 2; stderr %s", args, code, errb.String())
	}
	return decode(t, errb.String())
}

func hasValid(e map[string]any, want ...string) bool {
	valid, _ := e["valid"].([]any)
	for _, w := range want {
		found := false
		for _, v := range valid {
			found = found || v == w
		}
		if !found {
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
		{[]string{"story", "veiw"}, []string{"view", "comment"}, `did you mean "view"?`},
		{[]string{"story", "nothing-like-it"}, []string{"view"}, `see "scut story --help"`},
		{[]string{"story"}, []string{"view", "update"}, `see "scut story --help"`},
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
		{[]string{"story", "--yes"}, []string{"view", "update"}, `see "scut story --help"`},
		// An empty name matches every name by prefix: no guess.
		{[]string{"story", ""}, []string{"view"}, `see "scut story --help"`},
	} {
		e := usageError(t, c.args...)
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

// Hidden commands and the help command are not in valid.
func TestUnknownCommandValidLeavesOutHelp(t *testing.T) {
	e := usageError(t, "nothing-like-it")
	if hasValid(e, "help") {
		t.Errorf("valid %v has help", e["valid"])
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
