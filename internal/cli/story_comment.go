package cli

import (
	"encoding/json"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/nono/scut/internal/exit"
	"github.com/nono/scut/internal/ref"
)

func newStoryComment(env Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:         "comment <story>",
		Short:       "Add a comment to a story",
		Args:        cobra.ExactArgs(1),
		Annotations: map[string]string{OpsAnnotation: "createStoryComment,listMembers"},
	}
	bodyFile := cmd.Flags().String("body-file", "", "read the comment text from this file; - is stdin")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if *bodyFile == "" {
			return exit.Usagef("--body-file is required")
		}
		id, err := ref.Story(args[0])
		if err != nil {
			return err
		}
		text, err := readText(*bodyFile, env.Stdin)
		if err != nil {
			return err
		}
		return withDeps(env, func(cmd *cobra.Command, _ []string, d *deps) error {
			raw, err := d.Doer.Raw(cmd.Context(), "POST", "stories/"+strconv.FormatInt(id, 10)+"/comments", nil,
				map[string]string{"text": text})
			if err != nil {
				return err
			}
			cs, err := shapeComments([]json.RawMessage{raw}, d.Resolver)
			if err != nil {
				return err
			}
			if len(cs) == 0 {
				return &exit.Error{Code: exit.Other, Msg: "no comment in the reply"}
			}
			return d.Out(cs[0])
		})(cmd, args)
	}
	return cmd
}
