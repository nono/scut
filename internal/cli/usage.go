package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
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

// usageAfter writes the usage of the command that args name when the last
// argument is "usage" and there is no other argument or flag, as in
// "scut story create usage". It runs before the command, so it needs no
// token. It returns the command, or nil when args are not such a request.
func usageAfter(root *cobra.Command, args []string) *cobra.Command {
	n := len(args)
	if n < 2 || args[n-1] != "usage" {
		return nil
	}
	// A group has a usage subcommand. A flag, or another word, leaves rest
	// not empty.
	cmd, rest, err := root.Find(args[:n-1])
	if err != nil || len(rest) > 0 || cmd.HasSubCommands() {
		return nil
	}
	var b strings.Builder
	writeLeafUsage(&b, cmd)
	if _, err := io.WriteString(cmd.OutOrStdout(), b.String()); err != nil {
		return nil
	}
	return cmd
}
