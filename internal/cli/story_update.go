package cli

import (
	"context"
	"encoding/json"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/nono/scut/internal/exit"
	"github.com/nono/scut/internal/ref"
	"github.com/nono/scut/internal/resolve"
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

// updateFlags are the flags of story update, with what RunE derives from them.
type updateFlags struct {
	changed func(name string) bool // cmd.Flags().Changed
	specs   []fieldSpec            // from fields

	name, state, workflow, epic, requester, descFile, parent string
	addOwner, removeOwner, fields                            []string
	archive, unarchive, noParent                             bool
}

func newStoryUpdate(env Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "update <story>…",
		Short: "Change one story, or make the same change to several",
		Long: "Change one story, or make the same change to several stories in one call per 100 stories.\n" +
			"With several stories, the output is a list, and --name and --description-file are not allowed.",
		Args:        cobra.MinimumNArgs(1),
		Annotations: map[string]string{OpsAnnotation: "updateStory,bulkUpdateStories,getStory,listStories,listWorkflows,searchEpics,listMembers,getWhoami,listCustomFields", IDArgsAnnotation: "sc-"},
	}
	var u updateFlags
	f := cmd.Flags()
	f.StringVar(&u.name, "name", "", "new name; a single story only")
	f.StringVar(&u.state, "state", "", "workflow state name")
	f.StringVar(&u.workflow, "workflow", "", "workflow of --state; default: the workflow of each story")
	f.StringVar(&u.epic, "epic", "", "epic ID or name")
	f.StringVar(&u.requester, "requester", "", "requester (@me, mention name, email or UUID)")
	f.StringArrayVar(&u.addOwner, "add-owner", nil, "add an owner; repeatable")
	f.StringArrayVar(&u.removeOwner, "remove-owner", nil, "remove an owner; repeatable")
	f.StringArrayVar(&u.fields, "field", nil, "set a custom field, Name=Value; repeatable")
	f.StringVar(&u.descFile, "description-file", "", "read the description from this file; - is stdin; a single story only")
	f.BoolVar(&u.archive, "archive", false, "archive the story")
	f.BoolVar(&u.unarchive, "unarchive", false, "unarchive the story")
	f.StringVar(&u.parent, "parent", "", "make the story a sub-task of this story")
	f.BoolVar(&u.noParent, "no-parent", false, "make the story a normal story, not a sub-task")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		changed := cmd.Flags().Changed
		if u.archive && u.unarchive {
			return exit.Usagef("--archive and --unarchive conflict")
		}
		if changed("parent") && u.noParent {
			return exit.Usagef("--parent and --no-parent conflict")
		}
		if changed("workflow") && !changed("state") {
			return exit.Usagef("--workflow needs --state")
		}
		given := u.archive || u.unarchive || u.noParent
		for _, n := range []string{"name", "state", "epic", "requester", "add-owner", "remove-owner", "field", "description-file", "parent"} {
			given = given || changed(n)
		}
		if !given {
			return exit.Usagef("nothing to change: give at least one flag")
		}
		ids, err := storyIDs(env, args) // args is not empty, so not from git
		if err != nil {
			return err
		}
		ids = uniqueIDs(ids)
		if len(ids) > 1 && (changed("name") || changed("description-file")) {
			return exit.Usagef("--name and --description-file need a single story")
		}
		u.changed = changed
		if u.specs, err = parseFieldSpecs(u.fields); err != nil {
			return err
		}
		body := map[string]any{}
		if changed("description-file") {
			if body["description"], err = readText(u.descFile, env.Stdin); err != nil {
				return err
			}
		}
		if changed("name") {
			body["name"] = u.name
		}
		if u.archive || u.unarchive {
			body["archived"] = u.archive
		}
		if changed("parent") {
			pid, err := ref.Story(u.parent)
			if err != nil {
				return err
			}
			if slices.Contains(ids, pid) {
				return exit.Usagef("--parent %d is one of the stories: a story cannot be its own parent", pid)
			}
			body["parent_story_id"] = pid
		}
		if u.noParent {
			body["parent_story_id"] = nil
		}
		return withDeps(env, func(cmd *cobra.Command, _ []string, d *deps) error {
			if len(ids) > 1 {
				return updateMany(cmd.Context(), d, ids, u, body)
			}
			return updateOne(cmd.Context(), d, ids[0], u, body)
		})(cmd, args)
	}
	return cmd
}

// resolveCommon adds to body the names that resolve the same for each story:
// epic, requester, owners and custom fields.
func resolveCommon(r *resolve.R, u updateFlags, body map[string]any) error {
	var err error
	changed := u.changed
	if changed("epic") {
		if body["epic_id"], err = r.Epic(u.epic); err != nil {
			return err
		}
	}
	if changed("requester") {
		if body["requester_id"], err = r.Member(u.requester); err != nil {
			return err
		}
	}
	if changed("add-owner") {
		if body["owner_ids_add"], err = r.Members(u.addOwner); err != nil {
			return err
		}
	}
	if changed("remove-owner") {
		if body["owner_ids_remove"], err = r.Members(u.removeOwner); err != nil {
			return err
		}
	}
	if len(u.specs) > 0 {
		vals := make([]map[string]string, 0, len(u.specs))
		for _, s := range u.specs {
			fid, vid, err := r.Field(s.name, s.value)
			if err != nil {
				return err
			}
			vals = append(vals, map[string]string{"field_id": fid, "value_id": vid})
		}
		body["custom_field_values_add"] = vals
	}
	return nil
}

func updateOne(ctx context.Context, d *deps, id int64, u updateFlags, body map[string]any) error {
	r := d.Resolver
	path := "stories/" + strconv.FormatInt(id, 10)
	if u.changed("state") {
		var wf int64 // --workflow wins in resolveStates
		if !u.changed("workflow") {
			raw, err := d.Doer.Raw(ctx, "GET", path, url.Values{"fields": {"workflow"}}, nil)
			if err != nil {
				return err
			}
			if _, wf, err = storyWorkflow(raw); err != nil {
				return err
			}
		}
		states, err := resolveStates(r, u, []int64{id}, map[int64]int64{id: wf})
		if err != nil {
			return err
		}
		body["workflow_state_id"] = states[id]
	}
	if err := resolveCommon(r, u, body); err != nil {
		return err
	}
	raw, err := d.Doer.Raw(ctx, "PATCH", path, nil, body)
	if err != nil {
		return err
	}
	if err := checkParent(raw, body); err != nil {
		return err
	}
	out, err := shapeStory(raw, r, shapeOpts{Description: true})
	if err != nil {
		return err
	}
	return d.Out(out)
}

// storyWorkflow reads the ID of a story and of its workflow (0 for none).
func storyWorkflow(raw json.RawMessage) (id, workflow int64, err error) {
	var s struct {
		ID       int64 `json:"id"`
		Workflow *struct {
			ID int64 `json:"id"`
		} `json:"workflow"`
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		return 0, 0, &exit.Error{Code: exit.Other, Msg: "bad story data: " + err.Error()}
	}
	if s.Workflow != nil {
		workflow = s.Workflow.ID
	}
	return s.ID, workflow, nil
}

// checkParent fails when body sets parent_story_id (an int64, or nil for
// none) and the story in raw, the reply to body, has another parent. The API
// ignores parent_story_id when the Sub-task feature of the workspace is off.
func checkParent(raw []byte, body map[string]any) error {
	want, ok := body["parent_story_id"]
	if !ok {
		return nil
	}
	var s map[string]json.RawMessage
	var parent *struct {
		ID int64 `json:"id"`
	}
	err := json.Unmarshal(raw, &s)
	if p, ok := s["parent_story"]; err == nil && ok {
		err = json.Unmarshal(p, &parent)
	} else if err == nil {
		// The Story schema requires no field: do not take a missing key as no parent.
		return &exit.Error{Code: exit.Other, Msg: "the reply has no parent_story, so scut cannot check the parent." +
			" The write is saved. Read the story with scut story view --fields parent_story."}
	}
	if err != nil {
		return &exit.Error{Code: exit.Other, Msg: "bad story data: " + err.Error()}
	}
	var got any
	if parent != nil {
		got = parent.ID
	}
	if got == want {
		return nil
	}
	has := "has no parent"
	if parent != nil {
		has = "has parent " + strconv.FormatInt(parent.ID, 10)
	}
	return &exit.Error{Code: exit.Other, Msg: "the API did not set the parent: the story " + has +
		". The rest of the write is saved. Is the Sub-task feature of the workspace on?"}
}
