package cli

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/nono/scut/internal/exit"
	"github.com/nono/scut/internal/ref"
)

var storyTypes = []string{"feature", "chore", "bug"}

// linkSpec is one --link "<verb> <story>".
type linkSpec struct {
	api   string
	swap  bool
	other int64
}

func parseLinkSpecs(list []string) ([]linkSpec, error) {
	var out []linkSpec
	for _, l := range list {
		p := strings.Fields(l)
		if len(p) != 2 {
			return nil, exit.Usagef("--link takes \"<verb> <story>\", not %q", l)
		}
		api, swap, err := linkVerb(p[0])
		if err != nil {
			return nil, err
		}
		id, err := ref.Story(p[1])
		if err != nil {
			return nil, err
		}
		out = append(out, linkSpec{api, swap, id})
	}
	return out, nil
}

func newStoryCreate(env Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:         "create",
		Short:       "Create a story",
		Args:        cobra.NoArgs,
		Annotations: map[string]string{OpsAnnotation: "createStory,listStories,getStory,listTeams,listWorkflows,searchEpics,listMembers,getWhoami,listLabels,listCustomFields"},
	}
	f := cmd.Flags()
	name := f.String("name", "", "story name (required)")
	typ := f.String("type", "feature", "feature, chore or bug")
	team := f.String("team", "", "team name; default: config")
	workflow := f.String("workflow", "", "workflow of --state; default: config, else the default workflow of the team")
	state := f.String("state", "", "workflow state name; default: config")
	epic := f.String("epic", "", "epic ID or name")
	parent := f.String("parent", "", "parent story")
	owners := f.StringArray("owner", nil, "owner (@me, mention name, email or UUID); repeatable; default: config")
	requester := f.String("requester", "", "requester; default: config")
	labels := f.StringArray("label", nil, "label name; repeatable")
	estimate := f.Int("estimate", 0, "estimate in points")
	fields := f.StringArray("field", nil, "set a custom field, Name=Value; repeatable; overrides the config")
	descFile := f.String("description-file", "", "read the description from this file; - is stdin")
	links := f.StringArray("link", nil, "link, \"<verb> <story>\"; verbs: blocks, blocked-by, relates-to, duplicates, duplicated-by")
	extLinks := f.StringArray("external-link", nil, "external link URL; repeatable")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		changed := cmd.Flags().Changed
		if strings.TrimSpace(*name) == "" {
			return exit.Usagef("--name is required")
		}
		if !slices.Contains(storyTypes, *typ) {
			return &exit.Error{Code: exit.Usage, Msg: "--type takes feature, chore or bug, not " + `"` + *typ + `"`, Valid: storyTypes}
		}
		specs, err := parseFieldSpecs(*fields)
		if err != nil {
			return err
		}
		linkList, err := parseLinkSpecs(*links)
		if err != nil {
			return err
		}
		body := map[string]any{"name": *name, "story_type": *typ}
		if changed("description-file") {
			if body["description"], err = readText(*descFile, env.Stdin); err != nil {
				return err
			}
		}
		if changed("parent") {
			if body["parent_story_id"], err = ref.Story(*parent); err != nil {
				return err
			}
		}
		if changed("estimate") {
			body["estimate"] = *estimate
		}
		if len(*extLinks) > 0 {
			var u []string
			for _, l := range *extLinks {
				if !slices.Contains(u, l) {
					u = append(u, l)
				}
			}
			body["external_links"] = u
		}
		if len(linkList) > 0 {
			ls := make([]map[string]any, 0, len(linkList))
			for _, l := range linkList {
				m := map[string]any{"verb": l.api}
				if l.swap {
					m["subject_story_id"] = l.other
				} else {
					m["object_story_id"] = l.other
				}
				ls = append(ls, m)
			}
			body["story_links"] = ls
		}
		return withDeps(env, func(cmd *cobra.Command, _ []string, d *deps) error {
			ctx := cmd.Context()
			r := d.Resolver
			s := d.Settings
			// explain names the config source of a value that does not resolve.
			explain := func(key string, flagSet bool, err error) error {
				if flagSet {
					return err
				}
				return s.Explain(key, err)
			}
			stateName := first(*state, s.State)
			if stateName == "" {
				return exit.Usagef("give --state or set state in the config")
			}
			// Resolve every name before the first write.
			teamName := first(*team, s.Team)
			var teamID string
			if teamName != "" {
				if teamID, err = r.Team(teamName); err != nil {
					return explain("team", *team != "", err)
				}
				body["team_id"] = teamID
			}
			var wf int64
			if wfName := first(*workflow, s.Workflow); wfName != "" {
				if wf, err = r.Workflow(wfName); err != nil {
					return explain("workflow", *workflow != "", err)
				}
			}
			if body["workflow_state_id"], err = r.State(stateName, wf, teamID); err != nil {
				return explain("state", *state != "", err)
			}
			if changed("epic") {
				if body["epic_id"], err = r.Epic(*epic); err != nil {
					return err
				}
			}
			ownerNames := *owners
			if len(ownerNames) == 0 && s.Owner != "" {
				ownerNames = []string{s.Owner}
			}
			if len(ownerNames) > 0 {
				if body["owner_ids"], err = r.Members(ownerNames); err != nil {
					return explain("owner", len(*owners) > 0, err)
				}
			}
			if rq := first(*requester, s.Requester); rq != "" {
				if body["requester_id"], err = r.Member(rq); err != nil {
					return explain("requester", *requester != "", err)
				}
			}
			if len(*labels) > 0 {
				if body["label_ids"], err = r.Labels(*labels); err != nil {
					return err
				}
			}
			// A --field flag overrides the config value of the same field.
			vals := map[string]string{}
			for k, v := range s.Fields {
				vals[k] = v
			}
			for _, sp := range specs {
				vals[sp.name] = sp.value
			}
			if len(vals) > 0 {
				names := make([]string, 0, len(vals))
				for k := range vals {
					names = append(names, k)
				}
				slices.Sort(names)
				cfv := make([]map[string]string, 0, len(names))
				for _, n := range names {
					fid, vid, err := r.Field(n, vals[n])
					if err != nil {
						return explain("fields."+n, slices.ContainsFunc(specs, func(sp fieldSpec) bool { return sp.name == n }), err)
					}
					cfv = append(cfv, map[string]string{"field_id": fid, "value_id": vid})
				}
				body["custom_field_values"] = cfv
			}
			body["external_id"] = newExternalID()
			raw, err := createStoryRetry(ctx, d, body)
			if raw == nil {
				return err
			}
			if err == nil {
				var out StoryOut
				if out, err = shapeStory(raw, r, shapeOpts{Description: true}); err == nil {
					return d.Out(out)
				}
			}
			// The story exists: give its ID although the output failed.
			var c struct {
				ID     int64  `json:"id"`
				AppURL string `json:"app_url"`
			}
			_ = json.Unmarshal(raw, &c)
			if oerr := d.Out(map[string]any{"id": c.ID, "app_url": c.AppURL}); oerr != nil {
				return oerr
			}
			return created(err, c.AppURL)
		})(cmd, args)
	}
	return cmd
}

// first returns the first non-empty string.
func first(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}

// created adds to err the advice that the story exists.
func created(err error, appURL string) error {
	var e *exit.Error
	if !errors.As(err, &e) {
		e = &exit.Error{Code: exit.Other, Msg: err.Error()}
	}
	c := *e
	c.Suggestion = "the story was created: " + appURL
	return &c
}
