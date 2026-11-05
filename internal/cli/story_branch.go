package cli

import (
	"context"
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
				b, err := branchName(cmd.Context(), d, ids[0])
				if err != nil {
					return err
				}
				return d.Out(map[string]any{"story": ids[0], "branch": b})
			})(cmd, args)
		},
	}
}

// branchName returns the branch name that Shortcut suggests for story id,
// with no trailing "-" or "/". No branch name format gives exit 3.
func branchName(ctx context.Context, d *deps, id int64) (string, error) {
	raw, err := d.Doer.Raw(ctx, "GET", "stories/"+strconv.FormatInt(id, 10),
		url.Values{"fields": {"formatted_vcs_branch_name"}}, nil)
	if err != nil {
		return "", err
	}
	var s struct {
		Branch *string `json:"formatted_vcs_branch_name"`
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", &exit.Error{Code: exit.Other, Msg: "bad story response"}
	}
	if s.Branch == nil {
		return "", exit.NotFoundf("the workspace has no branch name format")
	}
	return strings.TrimRight(*s.Branch, "-/"), nil
}
