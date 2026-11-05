// Package cli builds the scut command tree and runs it.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/nono/scut/internal/config"
	"github.com/nono/scut/internal/exit"
	"github.com/nono/scut/internal/transport"
)

// Env holds the process inputs that commands read, so tests can replace them.
type Env struct {
	Getenv       func(string) string
	Dir          string
	Stdin        io.Reader
	StdinIsTTY   bool
	StdinFd      int                          // file descriptor of Stdin, for ReadPassword
	ReadPassword func(fd int) ([]byte, error) // nil: term.ReadPassword
	Keyring      config.Keyring               // nil: no keyring
	APIBase      string                       // empty: the real API
	Sleep        func(time.Duration)          // nil: wait on a timer; tests set a no-op
	Now          func() time.Time             // nil: time.Now
	FileClient   *http.Client                 // nil: a new client for the file host
	FileHosts    []string                     // hosts that get the file token; nil: the Shortcut hosts
}

// OSEnv returns the Env of the running process.
func OSEnv() Env {
	dir, _ := os.Getwd()
	fi, err := os.Stdin.Stat()
	tty := err == nil && fi.Mode()&os.ModeCharDevice != 0
	return Env{Getenv: os.Getenv, Dir: dir, Stdin: os.Stdin, StdinIsTTY: tty, StdinFd: int(os.Stdin.Fd()), Keyring: config.OSKeyring{}}
}

// Group returns a command that only holds subcommands. Running it alone is a usage error.
func Group(use, short string) *cobra.Command {
	g := &cobra.Command{
		Use:   use,
		Short: short,
		Args:  noArgs,
		// The cobra default, which only its own error path sets.
		SuggestionsMinimumDistance: 2,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return groupError(cmd, fmt.Sprintf("%q needs a subcommand", cmd.CommandPath()))
		},
	}
	g.AddCommand(newUsage(false))
	return g
}

// noArgs is the Args check of a command that takes no arguments. For a group,
// an argument is an unknown subcommand. For a leaf, it is a wrong count, and
// usageArgs adds the usage line.
func noArgs(cmd *cobra.Command, args []string) error {
	if len(args) > 0 && !cmd.HasSubCommands() {
		return exit.Usagef("%q takes no arguments, received %d", cmd.CommandPath(), len(args))
	}
	if len(args) > 0 {
		e := groupError(cmd, fmt.Sprintf("unknown command %q for %q", args[0], cmd.CommandPath()))
		// An empty name is a prefix of each name: make no guess.
		if s := cmd.SuggestionsFor(args[0]); len(s) > 0 && strings.TrimSpace(args[0]) != "" {
			e.Suggestion = fmt.Sprintf("did you mean %q?", s[0])
		}
		return e
	}
	return nil
}

// groupError is a usage error of a group: valid has its subcommands.
func groupError(cmd *cobra.Command, msg string) *exit.Error {
	return &exit.Error{Code: exit.Usage, Msg: msg, Valid: subcommandNames(cmd), Suggestion: seeHelp(cmd)}
}

// subcommandNames returns the names of the subcommands that a user can run.
func subcommandNames(cmd *cobra.Command) []string {
	var out []string
	for _, c := range cmd.Commands() {
		if c.IsAvailableCommand() {
			out = append(out, c.Name())
		}
	}
	return out
}

func seeHelp(cmd *cobra.Command) string {
	return fmt.Sprintf("see %q", cmd.CommandPath()+" --help")
}

// flagError is a flag parse error, with the flags of cmd in valid when the flag is unknown.
// On a group, cobra parses the flags before it looks for the subcommand: an
// argument before the flag is an unknown subcommand.
func flagError(cmd *cobra.Command, err error) error {
	if a := cmd.Flags().Args(); cmd.HasSubCommands() && len(a) > 0 {
		return noArgs(cmd, a)
	}
	e := exit.Usagef("%s", err)
	e.Suggestion = seeHelp(cmd)
	if ne := (*pflag.NotExistError)(nil); errors.As(err, &ne) {
		cmd.Flags().VisitAll(func(f *pflag.Flag) {
			if !f.Hidden {
				e.Valid = append(e.Valid, "--"+f.Name)
			}
		})
	}
	return e
}

// newHelp replaces the cobra help command, which prints the root help and
// exits 0 for a topic that does not exist. Here that is the unknown-command error.
func newHelp() *cobra.Command {
	return &cobra.Command{
		Use:   "help [command]",
		Short: "Help about any command",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Find fails only when the root has no Args check, and the root has noArgs.
			target, rest, _ := cmd.Root().Find(args)
			// Words after a leaf are its arguments: show its help.
			if len(rest) > 0 && target.HasSubCommands() {
				return noArgs(target, rest)
			}
			return target.Help()
		},
	}
}

// NewRoot returns the root command with all subcommands.
func NewRoot(env Env) *cobra.Command {
	root := &cobra.Command{
		Use:           "scut",
		Short:         "Command line client for the Shortcut REST API v4",
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       transport.Version,
		Args:          noArgs,
		// The cobra default, which only its own error path sets.
		SuggestionsMinimumDistance: 2,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	root.SetFlagErrorFunc(flagError)
	root.SetHelpCommand(newHelp())
	root.CompletionOptions.DisableDefaultCmd = true
	pf := root.PersistentFlags()
	pf.Bool("human", false, "print text instead of JSON")
	pf.String("token-file", "", "read the token from this file")
	pf.String("profile", "", "settings and token profile (default \"default\")")
	root.AddCommand(newUsage(true), newAuth(env), newWhoami(env), newAPI(env),
		newTeamGroup(env), newMemberGroup(env), newWorkflowGroup(env), newFieldGroup(env), newCacheGroup(env),
		storyCmd(env), newEpicGroup(env), newDocGroup(env), newWorktree(env), newSpec())
	usageArgs(root)
	return root
}

// usageArgs wraps the Args check of each command: a positional-argument error
// exits 2, with the usage line as the suggestion. An *exit.Error keeps its code,
// and a usage error with no suggestion gets the usage line.
func usageArgs(c *cobra.Command) {
	if old := c.Args; old != nil {
		c.Args = func(cmd *cobra.Command, args []string) error {
			err := old(cmd, args)
			if err == nil {
				return nil
			}
			var ee *exit.Error
			if !errors.As(err, &ee) {
				ee = exit.Usagef("%s", err)
			}
			if ee.Code == exit.Usage && ee.Suggestion == "" {
				ee.Suggestion = "usage: " + strings.TrimSuffix(cmd.UseLine(), " [flags]")
			}
			return ee
		}
	}
	for _, sub := range c.Commands() {
		usageArgs(sub)
	}
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
// and returns the exit code. A cancelled context gives 130, with no output
// unless the command returns an *exit.Error with code 130: then it is written,
// so the command can say what it did before the cancel.
func RunContext(ctx context.Context, root *cobra.Command, args []string, stderr io.Writer) int {
	root.SetArgs(args)
	root.SetErr(stderr)
	err := root.ExecuteContext(ctx)
	if err == nil {
		return int(exit.OK)
	}
	if ctx.Err() != nil {
		var e *exit.Error
		if errors.As(err, &e) && e.Code == exit.Cancelled {
			return int(exit.Write(stderr, err))
		}
		return int(exit.Cancelled)
	}
	return int(exit.Write(stderr, err))
}
