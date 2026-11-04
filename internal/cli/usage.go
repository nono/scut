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
			if skipInUsage(s) {
				continue
			}
			path := strings.TrimPrefix(s.CommandPath(), s.Root().Name()+" ")
			use := path
			if _, args, ok := strings.Cut(s.Use, " "); ok {
				use += " " + args
			}
			if s.HasSubCommands() {
				walk(s)
				continue
			}
			fmt.Fprintf(&b, "%s  %s\n", use, s.Short)
			s.LocalFlags().VisitAll(func(f *pflag.Flag) {
				if f.Hidden {
					return
				}
				name := "--" + f.Name
				if f.Shorthand != "" {
					name = "-" + f.Shorthand + ", " + name
				}
				fmt.Fprintf(&b, "  %s %s  %s\n", name, f.Value.Type(), f.Usage)
			})
		}
	}
	walk(noun)
	_, err := io.WriteString(w, b.String())
	return err
}
