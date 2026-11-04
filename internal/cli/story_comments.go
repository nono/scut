package cli

import (
	"net/url"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/nono/scut/internal/exit"
	"github.com/nono/scut/internal/ref"
)

func newStoryComments(env Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:         "comments <story>",
		Short:       "List the comments of a story, oldest first",
		Args:        oneArg,
		Annotations: map[string]string{opsAnnotation: "listStoryComments"},
	}
	limitFlag := cmd.Flags().String("limit", strconv.Itoa(defaultLimit), "maximum number of comments, or all")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		limit, err := parseLimit(*limitFlag)
		if err != nil {
			return err
		}
		id, err := ref.Story(args[0])
		if err != nil {
			return err
		}
		return withDeps(env, func(cmd *cobra.Command, _ []string, d *deps) error {
			q := url.Values{"order_dir": {"asc"}}
			raw, err := listPages(cmd, d, "stories/"+strconv.FormatInt(id, 10)+"/comments", q, limit)
			if err != nil {
				return err
			}
			out, err := shapeComments(raw, d.Resolver)
			if err != nil {
				return err
			}
			return d.Out(out)
		})(cmd, args)
	}
	return cmd
}

// oneArg requires exactly one argument, as a usage error (exit 2).
func oneArg(cmd *cobra.Command, args []string) error {
	if len(args) != 1 {
		return exit.Usagef("%q takes one argument: %s", cmd.CommandPath(), cmd.Use)
	}
	return nil
}
