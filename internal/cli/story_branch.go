package cli

import (
	"encoding/json"
	"net/url"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/nono/scut/internal/exit"
)

func newStoryBranch(env Env) *cobra.Command {
	return &cobra.Command{
		Use:         "branch [<story>]",
		Short:       "Print the suggested git branch name of a story",
		Args:        cobra.MaximumNArgs(1),
		Annotations: map[string]string{OpsAnnotation: "getStory"},
		RunE: func(cmd *cobra.Command, args []string) error {
			ids, err := storyIDs(env, args)
			if err != nil {
				return err
			}
			return withDeps(env, func(cmd *cobra.Command, _ []string, d *deps) error {
				raw, err := d.Doer.Raw(cmd.Context(), "GET", "stories/"+strconv.FormatInt(ids[0], 10),
					url.Values{"fields": {"formatted_vcs_branch_name"}}, nil)
				if err != nil {
					return err
				}
				var s struct {
					Branch *string `json:"formatted_vcs_branch_name"`
				}
				if err := json.Unmarshal(raw, &s); err != nil {
					return &exit.Error{Code: exit.Other, Msg: "bad story response"}
				}
				if s.Branch == nil {
					return exit.NotFoundf("the workspace has no branch name format")
				}
				return d.Out(map[string]any{
					"story":  ids[0],
					"branch": strings.TrimRight(*s.Branch, "-/"),
				})
			})(cmd, args)
		},
	}
}
