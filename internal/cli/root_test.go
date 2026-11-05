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
