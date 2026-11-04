package cli

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/nono/scut/internal/cache"
	"github.com/nono/scut/internal/exit"
)

// decodeAll unmarshals each raw entity into a T.
func decodeAll[T any](raw []json.RawMessage, what string) ([]T, error) {
	out := make([]T, 0, len(raw))
	for _, b := range raw {
		var v T
		if err := json.Unmarshal(b, &v); err != nil {
			return nil, &exit.Error{Code: exit.Other, Msg: fmt.Sprintf("bad %s data", what)}
		}
		out = append(out, v)
	}
	return out, nil
}

type rawID = json.RawMessage

func listCmd(env Env, short, op string, run func(d *deps) (any, error)) *cobra.Command {
	return &cobra.Command{
		Use:         "list",
		Short:       short,
		Args:        noArgs,
		Annotations: map[string]string{opsAnnotation: op},
		RunE: withDeps(env, func(_ *cobra.Command, _ []string, d *deps) error {
			v, err := run(d)
			if err != nil {
				return err
			}
			return d.Out(v)
		}),
	}
}

func newTeamGroup(env Env) *cobra.Command {
	g := Group("team", "List teams")
	g.AddCommand(listCmd(env, "List teams", "listTeams", func(d *deps) (any, error) {
		raw, err := d.Resolver.Teams()
		if err != nil {
			return nil, err
		}
		type team struct {
			ID          rawID  `json:"id"`
			Name        string `json:"name"`
			MentionName string `json:"mention_name"`
		}
		return decodeAll[team](raw, "team")
	}))
	return g
}

func newMemberGroup(env Env) *cobra.Command {
	g := Group("member", "List members")
	g.AddCommand(listCmd(env, "List members", "listMembers", func(d *deps) (any, error) {
		raw, err := d.Resolver.MemberList()
		if err != nil {
			return nil, err
		}
		type in struct {
			ID           rawID   `json:"id"`
			Name         string  `json:"name"`
			MentionName  string  `json:"mention_name"`
			EmailAddress *string `json:"email_address"`
			Disabled     bool    `json:"disabled"`
		}
		type member struct {
			ID          rawID   `json:"id"`
			Name        string  `json:"name"`
			MentionName string  `json:"mention_name"`
			Email       *string `json:"email"`
			Disabled    bool    `json:"disabled,omitempty"`
		}
		ms, err := decodeAll[in](raw, "member")
		if err != nil {
			return nil, err
		}
		out := make([]member, 0, len(ms))
		for _, m := range ms {
			out = append(out, member{m.ID, m.Name, m.MentionName, m.EmailAddress, m.Disabled})
		}
		return out, nil
	}))
	return g
}

type workflowEntity struct {
	ID                   rawID  `json:"id"`
	Name                 string `json:"name"`
	DefaultWorkflowState *struct {
		ID   rawID  `json:"id"`
		Name string `json:"name"`
	} `json:"default_workflow_state"`
	WorkflowStates struct {
		Entities []struct {
			ID   rawID  `json:"id"`
			Name string `json:"name"`
		} `json:"entities"`
	} `json:"workflow_states"`
}

func newWorkflowGroup(env Env) *cobra.Command {
	g := Group("workflow", "List workflows and their states")
	g.AddCommand(listCmd(env, "List workflows", "listWorkflows", func(d *deps) (any, error) {
		raw, err := d.Resolver.Workflows()
		if err != nil {
			return nil, err
		}
		ws, err := decodeAll[workflowEntity](raw, "workflow")
		if err != nil {
			return nil, err
		}
		type idNameRaw struct {
			ID   rawID  `json:"id"`
			Name string `json:"name"`
		}
		type workflow struct {
			ID           rawID      `json:"id"`
			Name         string     `json:"name"`
			DefaultState *idNameRaw `json:"default_state"`
		}
		out := make([]workflow, 0, len(ws))
		for _, w := range ws {
			x := workflow{ID: w.ID, Name: w.Name}
			if s := w.DefaultWorkflowState; s != nil {
				x.DefaultState = &idNameRaw{s.ID, s.Name}
			}
			out = append(out, x)
		}
		return out, nil
	}))
	states := &cobra.Command{
		Use:         "states [<workflow>]",
		Short:       "List the states of a workflow, or of all",
		Args:        cobra.MaximumNArgs(1),
		Annotations: map[string]string{opsAnnotation: "listWorkflows"},
		RunE: withDeps(env, func(_ *cobra.Command, args []string, d *deps) error {
			if len(args) == 1 { // resolves the name: refresh once, then exit 3 with valid
				if _, err := d.Resolver.Workflow(args[0]); err != nil {
					return err
				}
			}
			raw, err := d.Resolver.Workflows()
			if err != nil {
				return err
			}
			ws, err := decodeAll[workflowEntity](raw, "workflow")
			if err != nil {
				return err
			}
			type state struct {
				ID       rawID  `json:"id"`
				Name     string `json:"name"`
				Workflow string `json:"workflow"`
			}
			out := []state{}
			for _, w := range ws {
				if len(args) == 1 && w.Name != args[0] {
					continue
				}
				for _, s := range w.WorkflowStates.Entities {
					out = append(out, state{s.ID, s.Name, w.Name})
				}
			}
			return d.Out(out)
		}),
	}
	g.AddCommand(states)
	return g
}

func newFieldGroup(env Env) *cobra.Command {
	g := Group("field", "List custom fields and their values")
	g.AddCommand(listCmd(env, "List custom fields", "listCustomFields", func(d *deps) (any, error) {
		raw, err := d.Resolver.Fields()
		if err != nil {
			return nil, err
		}
		type field struct {
			ID   rawID  `json:"id"`
			Name string `json:"name"`
			Type string `json:"field_type"`
		}
		in, err := decodeAll[field](raw, "field")
		if err != nil {
			return nil, err
		}
		type out struct {
			ID   rawID  `json:"id"`
			Name string `json:"name"`
			Type string `json:"type"`
		}
		res := make([]out, 0, len(in))
		for _, f := range in {
			res = append(res, out(f))
		}
		return res, nil
	}))
	g.AddCommand(&cobra.Command{
		Use:         "values <field>",
		Short:       "List the values of a custom field",
		Args:        cobra.ExactArgs(1),
		Annotations: map[string]string{opsAnnotation: "listCustomFields"},
		RunE: withDeps(env, func(_ *cobra.Command, args []string, d *deps) error {
			vs, err := d.Resolver.FieldValues(args[0])
			if err != nil {
				return err
			}
			return d.Out(vs)
		}),
	})
	return g
}

func newCacheGroup(env Env) *cobra.Command {
	g := Group("cache", "Manage the local cache")
	g.AddCommand(&cobra.Command{
		Use:   "clear",
		Short: "Remove the cache of this workspace",
		Args:  noArgs,
		RunE: withDeps(env, func(_ *cobra.Command, _ []string, d *deps) error {
			dir := d.Settings.CacheDir
			if err := cache.New(dir, cacheTTL, time.Now).Clear(); err != nil {
				return err
			}
			return d.Out(map[string]string{"cleared": dir})
		}),
	})
	return g
}
