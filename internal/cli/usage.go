package cli

import (
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/nono/scut/internal/exit"
)

func skipInUsage(c *cobra.Command) bool {
	return c.Name() == "usage" || c.Name() == "help" || c.Name() == "completion" || c.Hidden
}

// newUsage returns the usage command of a root or a group. It reads the
// cobra tree, so the text cannot drift from the commands.
func newUsage(root bool) *cobra.Command {
	c := &cobra.Command{
		Use:   "usage",
		Short: "List the commands and flags",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if root {
				return writeRootUsage(cmd.OutOrStdout(), cmd.Parent())
			}
			return writeNounUsage(cmd.OutOrStdout(), cmd.Parent())
		},
	}
	if !root {
		c.Short = "List the commands and flags of this group"
	}
	return c
}

func writeRootUsage(w io.Writer, root *cobra.Command) error {
	var b strings.Builder
	for _, c := range root.Commands() {
		if !skipInUsage(c) {
			fmt.Fprintf(&b, "%s  %s\n", c.Name(), c.Short)
		}
	}
	b.WriteString("Run scut <noun> usage for the commands of a noun.\n")
	_, err := io.WriteString(w, b.String())
	return err
}

func writeNounUsage(w io.Writer, noun *cobra.Command) error {
	var b strings.Builder
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		for _, s := range c.Commands() {
			switch {
			case skipInUsage(s):
			case s.HasSubCommands():
				walk(s)
			default:
				writeLeafUsage(&b, s)
			}
		}
	}
	walk(noun)
	_, err := io.WriteString(w, b.String())
	return err
}

// writeLeafUsage writes the usage line and the flags of a command with no
// subcommands.
func writeLeafUsage(b *strings.Builder, s *cobra.Command) {
	use := strings.TrimPrefix(s.CommandPath(), s.Root().Name()+" ")
	if _, args, ok := strings.Cut(s.Use, " "); ok {
		use += " " + args
	}
	fmt.Fprintf(b, "%s  %s\n", use, s.Short)
	s.LocalFlags().VisitAll(func(f *pflag.Flag) {
		// cobra adds --help only to the command that runs.
		if f.Hidden || f.Name == "help" {
			return
		}
		name := "--" + f.Name
		if f.Shorthand != "" {
			name = "-" + f.Shorthand + ", " + name
		}
		fmt.Fprintf(b, "  %s %s  %s\n", name, f.Value.Type(), f.Usage)
	})
}

// usageAfter writes the usage of cmd when its run failed with a usage error
// and its only argument is "usage", as in "scut story create usage". Where
// "usage" is a valid argument, such as a search query, the command runs as
// usual. It reports whether it wrote the usage.
func usageAfter(cmd *cobra.Command, err error) bool {
	var e *exit.Error
	if cmd.HasSubCommands() || !errors.As(err, &e) || e.Code != exit.Usage || !slices.Equal(cmd.Flags().Args(), []string{"usage"}) {
		return false
	}
	var b strings.Builder
	writeLeafUsage(&b, cmd)
	_, werr := io.WriteString(cmd.OutOrStdout(), b.String())
	return werr == nil
}
