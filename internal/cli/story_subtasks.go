package cli

import (
	"strconv"

	"github.com/spf13/cobra"
)

func newStorySubtasks(env Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:         "subtasks [<story>]",
		Short:       "List the sub-tasks of a story, in position order",
		Long:        "List the sub-tasks of a story, in position order.\nThe API default applies: archived stories are left out.",
		Args:        cobra.MaximumNArgs(1),
		Annotations: map[string]string{OpsAnnotation: "listStorySubTaskStories,listMembers", IDArgsAnnotation: "sc-"},
	}
	limitFlag := cmd.Flags().String("limit", strconv.Itoa(defaultLimit), "maximum number of stories, or all")
	fieldsFlag := cmd.Flags().String("fields", "", fieldsUsage)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		limit, err := parseLimit(*limitFlag)
		if err != nil {
			return err
		}
		fields, err := listStoryFields(*fieldsFlag)
		if err != nil {
			return err
		}
		ids, err := storyIDs(env, args)
		if err != nil {
			return err
		}
		return withDeps(env, func(cmd *cobra.Command, _ []string, d *deps) error {
			return listStories(cmd, d, "stories/"+strconv.FormatInt(ids[0], 10)+"/sub-task-stories", positionOrder(), limit, *fieldsFlag, fields)
		})(cmd, args)
	}
	return cmd
}
