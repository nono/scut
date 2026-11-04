package cli

import (
	"context"
	"encoding/json"

	"github.com/spf13/cobra"

	"github.com/nono/scut/internal/exit"
	"github.com/nono/scut/internal/transport"
)

const opsAnnotation = "scut/ops"

type idName struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type whoamiEntity struct {
	Authorization struct {
		Scopes []string `json:"scopes"`
	} `json:"authorization"`
	Member struct {
		ID          string `json:"id"`
		MentionName string `json:"mention_name"`
		Name        string `json:"name"`
	} `json:"member"`
	Workspace struct {
		Slug string `json:"slug"`
	} `json:"workspace"`
	DefaultWorkflow      *idName `json:"default_workflow"`
	DefaultWorkflowState *idName `json:"default_workflow_state"`
}

func getWhoami(ctx context.Context, d *transport.Doer) (*whoamiEntity, error) {
	b, err := d.Raw(ctx, "GET", "/api/v4/whoami", nil, nil)
	if err != nil {
		return nil, err
	}
	var w whoamiEntity
	if json.Unmarshal(b, &w) != nil || w.Member.ID == "" {
		return nil, &exit.Error{Code: exit.Other, Msg: "whoami has no member"}
	}
	return &w, nil
}

type whoamiOut struct {
	ID                   string   `json:"id"`
	MentionName          string   `json:"mention_name"`
	Name                 string   `json:"name"`
	Workspace            string   `json:"workspace"`
	Scopes               []string `json:"scopes"`
	DefaultWorkflow      *idName  `json:"default_workflow"`
	DefaultWorkflowState *idName  `json:"default_workflow_state"`
}

func newWhoami(env Env) *cobra.Command {
	return &cobra.Command{
		Use:         "whoami",
		Short:       "Show the member and workspace of the token",
		Args:        noArgs,
		Annotations: map[string]string{opsAnnotation: "getWhoami"},
		RunE: withDeps(env, func(cmd *cobra.Command, _ []string, d *deps) error {
			w, err := getWhoami(cmd.Context(), d.Doer)
			if err != nil {
				return err
			}
			ws := w.Workspace.Slug
			if ws == "" {
				ws = d.Settings.Workspace
			}
			scopes := w.Authorization.Scopes
			if scopes == nil {
				scopes = []string{}
			}
			return d.Out(whoamiOut{
				ID: w.Member.ID, MentionName: w.Member.MentionName, Name: w.Member.Name,
				Workspace: ws, Scopes: scopes,
				DefaultWorkflow: w.DefaultWorkflow, DefaultWorkflowState: w.DefaultWorkflowState,
			})
		}),
	}
}
