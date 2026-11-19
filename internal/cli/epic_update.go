package cli

import (
	"encoding/json"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/nono/scut/internal/exit"
)

func newEpicUpdate(env Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:         "update <epic>",
		Short:       "Change an epic, by ID or exact name",
		Args:        cobra.ExactArgs(1),
		Annotations: map[string]string{OpsAnnotation: "updateEpic,searchEpics,listEpicWorkflowStates,listMembers,getWhoami", IDArgsAnnotation: ""},
	}
	f := cmd.Flags()
	name := f.String("name", "", "new name")
	state := f.String("state", "", "epic state name")
	addOwner := f.StringArray("add-owner", nil, "add an owner; repeatable")
	removeOwner := f.StringArray("remove-owner", nil, "remove an owner; repeatable")
	descFile := f.String("description-file", "", "read the description from this file; - is stdin")
	archive := f.Bool("archive", false, "archive the epic")
	unarchive := f.Bool("unarchive", false, "unarchive the epic")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		changed := cmd.Flags().Changed
		if *archive && *unarchive {
			return exit.Usagef("--archive and --unarchive conflict")
		}
		given := *archive || *unarchive
		for _, n := range []string{"name", "state", "add-owner", "remove-owner", "description-file"} {
			given = given || changed(n)
		}
		if !given {
			return exit.Usagef("nothing to change: give at least one flag")
		}
		body := map[string]any{}
		var err error
		if changed("description-file") {
			if body["description"], err = readText(*descFile, env.Stdin); err != nil {
				return err
			}
		}
		if changed("name") {
			body["name"] = *name
		}
		if *archive || *unarchive {
			body["archived"] = *archive
		}
		return withDeps(env, func(cmd *cobra.Command, _ []string, d *deps) error {
			r := d.Resolver
			id, err := r.Epic(args[0])
			if err != nil {
				return err
			}
			if changed("state") {
				if body["epic_state_id"], err = r.EpicState(*state); err != nil {
					return err
				}
			}
			if changed("add-owner") {
				if body["owner_ids_add"], err = r.Members(*addOwner); err != nil {
					return err
				}
			}
			if changed("remove-owner") {
				if body["owner_ids_remove"], err = r.Members(*removeOwner); err != nil {
					return err
				}
			}
			raw, err := d.Doer.Raw(cmd.Context(), "PATCH", "epics/"+strconv.FormatInt(id, 10), nil, body)
			if err != nil {
				return err
			}
			l, err := shapeEpics(d, []json.RawMessage{raw}, true)
			if err != nil {
				return err
			}
			return d.Out(l[0])
		})(cmd, args)
	}
	return cmd
}
