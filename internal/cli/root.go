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
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/nono/scut/internal/config"
	"github.com/nono/scut/internal/exit"
	"github.com/nono/scut/internal/runlog"
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
	Rec          *runlog.Recorder             // the record of this run, set by Execute; nil: no record
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
		var e *exit.Error
		if ne := noArgs(cmd, a); errors.As(ne, &e) {
			e.BadFlag = true
		}
		return e
	}
	e := exit.Usagef("%s", err)
	e.Suggestion = seeHelp(cmd)
	e.BadFlag = true
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
	// With a handler, a write to a closed stdout returns EPIPE and does not
	// stop the process, so the run can write its log line.
	signal.Notify(make(chan os.Signal, 1), syscall.SIGPIPE)
	return execute(ctx, args, stdout, stderr, env)
}

// idCommands are the commands whose positional arguments are IDs or story
// references. Another command can get a search query or a name in the form of
// an ID, so the log keeps no IDs for it.
var idCommands = map[string]bool{
	"story view": true, "story update": true, "story comment": true, "story comments": true,
	"story link": true, "story unlink": true, "story relink": true, "story subtasks": true,
	"story branch": true, "story files": true,
	"epic view": true, "epic stories": true, "epic comment": true,
	"worktree": true,
}

// execute runs scut and writes the line of the run to the log.
func execute(ctx context.Context, args []string, stdout, stderr io.Writer, env Env) int {
	now := env.Now
	if now == nil {
		now = time.Now
	}
	env.Rec = runlog.Start(env.Getenv, now)
	out := &countWriter{w: stdout}
	root := NewRoot(env)
	root.SetOut(out)
	code, cmd, err := run(ctx, root, args, out.errWriter(stderr))
	if out.broken != nil {
		code, err = int(exit.BrokenPipe), out.broken
	}
	raw := map[string]string{}
	cmd.Flags().Visit(func(f *pflag.Flag) { raw[f.Name] = f.Value.String() })
	name := strings.TrimPrefix(strings.TrimPrefix(cmd.CommandPath(), root.Name()), " ")
	var ee *exit.Error
	env.Rec.Finish(runlog.Result{
		Version:  transport.Version,
		Dir:      env.Dir,
		Cmd:      name,
		Args:     cmd.Flags().Args(),
		IDArgs:   idCommands[name],
		ArgsCut:  errors.As(err, &ee) && ee.BadFlag,
		Flags:    raw,
		OutBytes: out.n,
		Exit:     code,
		Err:      err,
	})
	return code
}

// countWriter counts the bytes that a command writes to stdout, and keeps the
// error of a write to a closed pipe.
type countWriter struct {
	w      io.Writer
	n      int64
	broken error
}

func (c *countWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	if errors.Is(err, syscall.EPIPE) {
		c.broken = err
	}
	return n, err
}

// errWriter returns stderr with no output after a write to a closed stdout:
// such a run prints no error, as a process that SIGPIPE stops.
func (c *countWriter) errWriter(stderr io.Writer) io.Writer {
	return writerFunc(func(p []byte) (int, error) {
		if c.broken != nil {
			return len(p), nil
		}
		return stderr.Write(p)
	})
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

// RunContext runs root with args and ctx. It writes any error as JSON to stderr
// and returns the exit code. A cancelled context gives 130, with no output
// unless the command returns an *exit.Error with code 130: then it is written,
// so the command can say what it did before the cancel.
func RunContext(ctx context.Context, root *cobra.Command, args []string, stderr io.Writer) int {
	code, _, _ := run(ctx, root, args, stderr)
	return code
}

// run is RunContext that also returns the command that ran and its error.
func run(ctx context.Context, root *cobra.Command, args []string, stderr io.Writer) (int, *cobra.Command, error) {
	root.SetArgs(args)
	root.SetErr(stderr)
	cmd, err := root.ExecuteContextC(ctx)
	if err == nil {
		return int(exit.OK), cmd, nil
	}
	if ctx.Err() != nil {
		var e *exit.Error
		if errors.As(err, &e) && e.Code == exit.Cancelled {
			return int(exit.Write(stderr, err)), cmd, err
		}
		return int(exit.Cancelled), cmd, err
	}
	return int(exit.Write(stderr, err)), cmd, err
}
