package cli_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/nono/scut/internal/cli"
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

func TestArgCountIs2(t *testing.T) {
	h := newHarness(t, nil, "", map[string]string{"SCUT_TOKEN": "sct_rw_acme_s3cret"})
	for _, args := range [][]string{{"field", "values"}, {"api"}, {"story", "search"}, {"field", "values", "a", "b"}} {
		if code, _, errs := h.run(args...); code != 2 {
			t.Errorf("%v: code %d, stderr %s", args, code, errs)
		}
	}
}
