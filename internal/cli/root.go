// Package cli builds the scut command tree and runs it.
package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"os/signal"

	"github.com/spf13/cobra"

	"github.com/nono/scut/internal/config"
	"github.com/nono/scut/internal/exit"
)

// Env holds the process inputs that commands read, so tests can replace them.
type Env struct {
	Getenv     func(string) string
	Dir        string
	Stdin      io.Reader
	StdinIsTTY bool
	Keyring    config.Keyring // nil: no keyring
	APIBase    string         // empty: the real API
}

// OSEnv returns the Env of the running process.
func OSEnv() Env {
	dir, _ := os.Getwd()
	fi, err := os.Stdin.Stat()
	tty := err == nil && fi.Mode()&os.ModeCharDevice != 0
	return Env{Getenv: os.Getenv, Dir: dir, Stdin: os.Stdin, StdinIsTTY: tty, Keyring: config.OSKeyring{}}
}

// Group returns a command that only holds subcommands. Running it alone is a usage error.
func Group(use, short string) *cobra.Command {
	g := &cobra.Command{
		Use:   use,
		Short: short,
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return exit.Usagef("%q needs a subcommand; see \"%s --help\"", cmd.CommandPath(), cmd.CommandPath())
		},
	}
	g.AddCommand(newUsage(false))
	return g
}

func noArgs(cmd *cobra.Command, args []string) error {
	if len(args) > 0 {
		return exit.Usagef("unknown command %q for %q", args[0], cmd.CommandPath())
	}
	return nil
}

// NewRoot returns the root command with all subcommands.
func NewRoot(env Env) *cobra.Command {
	root := &cobra.Command{
		Use:           "scut",
		Short:         "Command line client for the Shortcut REST API v4",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return exit.Usagef("%s", err)
	})
	root.CompletionOptions.DisableDefaultCmd = true
	pf := root.PersistentFlags()
	pf.Bool("human", false, "print text instead of JSON")
	pf.String("token-file", "", "read the token from this file")
	pf.String("profile", "", "settings and token profile (default \"default\")")
	root.AddCommand(newUsage(true), newAuth(env), newWhoami(env), newAPI(env),
		newTeamGroup(env), newMemberGroup(env), newWorkflowGroup(env), newFieldGroup(env), newCacheGroup(env),
		storyCmd(env))
	return root
}

// Execute runs scut with args and returns the exit code.
// SIGINT cancels the command context.
func Execute(args []string, stdout, stderr io.Writer, env Env) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	root := NewRoot(env)
	root.SetOut(stdout)
	return RunContext(ctx, root, args, stderr)
}

// RunContext runs root with args and ctx. It writes any error as JSON to stderr
// and returns the exit code. A cancelled context gives 130.
func RunContext(ctx context.Context, root *cobra.Command, args []string, stderr io.Writer) int {
	root.SetArgs(args)
	root.SetErr(stderr)
	err := root.ExecuteContext(ctx)
	if err == nil {
		return int(exit.OK)
	}
	if ctx.Err() != nil && (errors.Is(err, context.Canceled) || errors.Is(err, ctx.Err())) {
		return int(exit.Cancelled)
	}
	return int(exit.Write(stderr, err))
}
