package cli

import (
	"net/url"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/nono/scut/internal/ref"
)

func newStoryComments(env Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:         "comments <story>",
		Short:       "List the comments of a story, oldest first",
		Args:        cobra.ExactArgs(1),
		Annotations: map[string]string{OpsAnnotation: "listStoryComments,listMembers"},
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
			raw, stop := listPages(cmd, d, "stories/"+strconv.FormatInt(id, 10)+"/comments", q, limit)
			if raw == nil && stop != nil {
				return stop
			}
			out, err := shapeComments(raw, d.Resolver)
			if err != nil {
				return err
			}
			if err := d.Out(out); err != nil {
				return err
			}
			return stop // a list cut short: the items read, then the error
		})(cmd, args)
	}
	return cmd
}
