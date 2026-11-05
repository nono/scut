package cli

import (
	"encoding/json"
	"net/url"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/nono/scut/internal/exit"
	"github.com/nono/scut/internal/ref"
)

// fieldSpec is one --field "Name=Value".
type fieldSpec struct{ name, value string }

func parseFieldSpecs(list []string) ([]fieldSpec, error) {
	var out []fieldSpec
	for _, f := range list {
		name, value, ok := strings.Cut(f, "=")
		if !ok || name == "" || value == "" {
			return nil, exit.Usagef("--field takes Name=Value, not %q", f)
		}
		out = append(out, fieldSpec{name, value})
	}
	return out, nil
}

func newStoryUpdate(env Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:         "update <story>",
		Short:       "Change a story",
		Args:        cobra.ExactArgs(1),
		Annotations: map[string]string{OpsAnnotation: "updateStory,getStory,listWorkflows,searchEpics,listMembers,getWhoami,listCustomFields"},
	}
	f := cmd.Flags()
	name := f.String("name", "", "new name")
	state := f.String("state", "", "workflow state name")
	workflow := f.String("workflow", "", "workflow of --state; default: the workflow of the story")
	epic := f.String("epic", "", "epic ID or name")
	requester := f.String("requester", "", "requester (@me, mention name, email or UUID)")
	addOwner := f.StringArray("add-owner", nil, "add an owner; repeatable")
	removeOwner := f.StringArray("remove-owner", nil, "remove an owner; repeatable")
	fields := f.StringArray("field", nil, "set a custom field, Name=Value; repeatable")
	descFile := f.String("description-file", "", "read the description from this file; - is stdin")
	archive := f.Bool("archive", false, "archive the story")
	unarchive := f.Bool("unarchive", false, "unarchive the story")
	parent := f.String("parent", "", "make the story a sub-task of this story")
	noParent := f.Bool("no-parent", false, "make the story a normal story, not a sub-task")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		changed := cmd.Flags().Changed
		if *archive && *unarchive {
			return exit.Usagef("--archive and --unarchive conflict")
		}
		if changed("parent") && *noParent {
			return exit.Usagef("--parent and --no-parent conflict")
		}
		if changed("workflow") && !changed("state") {
			return exit.Usagef("--workflow needs --state")
		}
		given := *archive || *unarchive || *noParent
		for _, n := range []string{"name", "state", "epic", "requester", "add-owner", "remove-owner", "field", "description-file", "parent"} {
			given = given || changed(n)
		}
		if !given {
			return exit.Usagef("nothing to change: give at least one flag")
		}
		id, err := ref.Story(args[0])
		if err != nil {
			return err
		}
		specs, err := parseFieldSpecs(*fields)
		if err != nil {
			return err
		}
		body := map[string]any{}
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
		if changed("parent") {
			if body["parent_story_id"], err = ref.Story(*parent); err != nil {
				return err
			}
		}
		if *noParent {
			body["parent_story_id"] = nil
		}
		return withDeps(env, func(cmd *cobra.Command, _ []string, d *deps) error {
			ctx := cmd.Context()
			r := d.Resolver
			path := "stories/" + strconv.FormatInt(id, 10)
			if changed("state") {
				var wf int64
				if changed("workflow") {
					if wf, err = r.Workflow(*workflow); err != nil {
						return err
					}
				} else {
					raw, err := d.Doer.Raw(ctx, "GET", path, url.Values{"fields": {"workflow"}}, nil)
					if err != nil {
						return err
					}
					var s struct {
						Workflow *struct {
							ID int64 `json:"id"`
						} `json:"workflow"`
					}
					if err := json.Unmarshal(raw, &s); err != nil {
						return &exit.Error{Code: exit.Other, Msg: "bad story data: " + err.Error()}
					}
					if s.Workflow != nil {
						wf = s.Workflow.ID
					}
				}
				sid, err := r.State(*state, wf, "")
				if err != nil {
					return err
				}
				body["workflow_state_id"] = sid
			}
			if changed("epic") {
				eid, err := r.Epic(*epic)
				if err != nil {
					return err
				}
				body["epic_id"] = eid
			}
			if changed("requester") {
				if body["requester_id"], err = r.Member(*requester); err != nil {
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
			if len(specs) > 0 {
				vals := make([]map[string]string, 0, len(specs))
				for _, s := range specs {
					fid, vid, err := r.Field(s.name, s.value)
					if err != nil {
						return err
					}
					vals = append(vals, map[string]string{"field_id": fid, "value_id": vid})
				}
				body["custom_field_values_add"] = vals
			}
			raw, err := d.Doer.Raw(ctx, "PATCH", path, nil, body)
			if err != nil {
				return err
			}
			out, err := shapeStory(raw, r, shapeOpts{Description: true})
			if err != nil {
				return err
			}
			return d.Out(out)
		})(cmd, args)
	}
	return cmd
}
