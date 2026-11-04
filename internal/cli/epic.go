package cli

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/nono/scut/internal/exit"
)

// EpicOut is the compact epic shape. Description is only set by epic view.
type EpicOut struct {
	ID          int64           `json:"id"`
	Name        string          `json:"name"`
	State       *idName         `json:"state,omitempty"`
	Description *string         `json:"description,omitempty"`
	Owners      []memberOut     `json:"owners,omitzero"`
	OwnersTotal int             `json:"owners_total,omitempty"`
	Stats       json.RawMessage `json:"stats,omitempty"`
	AppURL      string          `json:"app_url,omitempty"`
}

type epicIn struct {
	ID          int64             `json:"id"`
	Name        string            `json:"name"`
	State       *idName           `json:"epic_state"`
	Description *string           `json:"description"`
	Owners      *nested[memberIn] `json:"owners"`
	Stats       json.RawMessage   `json:"stats"`
	AppURL      string            `json:"app_url"`
}

// Human is the text form: name, state, URL, description.
func (e EpicOut) Human() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s (epic %d)\n", e.Name, e.ID)
	if e.State != nil {
		fmt.Fprintf(&b, "State: %s\n", e.State.Name)
	}
	if len(e.Owners) > 0 {
		var o []string
		for _, m := range e.Owners {
			o = append(o, m.label())
		}
		fmt.Fprintf(&b, "Owners: %s\n", strings.Join(o, ", "))
	}
	if e.AppURL != "" {
		fmt.Fprintf(&b, "%s\n", e.AppURL)
	}
	if e.Description != nil && *e.Description != "" {
		fmt.Fprintf(&b, "\n%s\n", *e.Description)
	}
	return strings.TrimRight(b.String(), "\n")
}

type epicList []EpicOut

// Human joins the text forms of the epics.
func (l epicList) Human() string {
	parts := make([]string, len(l))
	for i, e := range l {
		parts[i] = e.Human()
	}
	return strings.Join(parts, "\n\n")
}

// shapeEpics converts Epic entities to the compact shape.
func shapeEpics(d *deps, raw []json.RawMessage, description bool) (epicList, error) {
	eps, err := decodeAll[epicIn](raw, "epic")
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, e := range eps {
		if e.Owners != nil {
			for _, o := range e.Owners.Entities {
				ids = append(ids, o.ID)
			}
		}
	}
	nm, err := memberNames(d.Resolver, ids)
	if err != nil {
		return nil, err
	}
	out := make(epicList, 0, len(eps))
	for _, e := range eps {
		o := EpicOut{ID: e.ID, Name: e.Name, State: e.State, Stats: e.Stats, AppURL: e.AppURL, OwnersTotal: e.Owners.total()}
		if description {
			o.Description = e.Description
		}
		if e.Owners != nil {
			o.Owners = []memberOut{}
			for _, m := range e.Owners.Entities {
				o.Owners = append(o.Owners, *nm.member(&m))
			}
		}
		out = append(out, o)
	}
	return out, nil
}

func newEpicGroup(env Env) *cobra.Command {
	g := Group("epic", "View and search epics, list their stories")
	g.AddCommand(newEpicView(env), newEpicSearch(env), newEpicStories(env), newEpicComment(env))
	return g
}

func newEpicView(env Env) *cobra.Command {
	return &cobra.Command{
		Use:         "view <epic>",
		Short:       "Show an epic, by ID or exact name",
		Args:        cobra.ExactArgs(1),
		Annotations: map[string]string{OpsAnnotation: "getEpic,searchEpics"},
		RunE: withDeps(env, func(cmd *cobra.Command, args []string, d *deps) error {
			id, err := d.Resolver.Epic(args[0])
			if err != nil {
				return err
			}
			raw, err := d.Doer.Raw(cmd.Context(), "GET", "epics/"+strconv.FormatInt(id, 10), nil, nil)
			if err != nil {
				return err
			}
			l, err := shapeEpics(d, []json.RawMessage{raw}, true)
			if err != nil {
				return err
			}
			return d.Out(l[0])
		}),
	}
}

func newEpicSearch(env Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:         "search <query>",
		Short:       "Search epics",
		Long:        "Search epics with a Shortcut search query, sent as is.\n\nThe search index is eventually consistent: search can lag; use epic view for current data.",
		Args:        cobra.ExactArgs(1),
		Annotations: map[string]string{OpsAnnotation: "searchEpics"},
	}
	limitFlag := cmd.Flags().String("limit", strconv.Itoa(defaultLimit), "maximum number of epics, or all")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		limit, err := parseLimit(*limitFlag)
		if err != nil {
			return err
		}
		return withDeps(env, func(cmd *cobra.Command, _ []string, d *deps) error {
			raw, err := listPages(cmd, d, "epics/search", url.Values{"query": {args[0]}}, limit)
			if err != nil {
				return err
			}
			out, err := shapeEpics(d, raw, false)
			if err != nil {
				return err
			}
			return d.Out(out)
		})(cmd, args)
	}
	return cmd
}

func newEpicStories(env Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:         "stories <epic>",
		Short:       "List the stories of an epic, in position order",
		Long:        "List the stories of an epic, in position order. The epic is an ID or an exact name.\nThe API default applies: archived stories are left out.",
		Args:        cobra.ExactArgs(1),
		Annotations: map[string]string{OpsAnnotation: "listEpicStories,searchEpics"},
	}
	limitFlag := cmd.Flags().String("limit", strconv.Itoa(defaultLimit), "maximum number of stories, or all")
	fieldsFlag := cmd.Flags().String("fields", "", "comma-separated fields to show")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		limit, err := parseLimit(*limitFlag)
		if err != nil {
			return err
		}
		fields, err := listStoryFields(*fieldsFlag)
		if err != nil {
			return err
		}
		return withDeps(env, func(cmd *cobra.Command, _ []string, d *deps) error {
			id, err := d.Resolver.Epic(args[0])
			if err != nil {
				return err
			}
			q := url.Values{"order_by": {"position"}, "order_dir": {"asc"}}
			fieldsQuery(q, fields)
			raw, err := listPages(cmd, d, "epics/"+strconv.FormatInt(id, 10)+"/stories", q, limit)
			if err != nil {
				return err
			}
			out, err := shapeStories(d, raw)
			if err != nil {
				return err
			}
			return d.Out(out)
		})(cmd, args)
	}
	return cmd
}

func newEpicComment(env Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:         "comment <epic>",
		Short:       "Add a comment to an epic",
		Args:        cobra.ExactArgs(1),
		Annotations: map[string]string{OpsAnnotation: "createEpicComment,searchEpics"},
	}
	bodyFile := cmd.Flags().String("body-file", "", "read the comment text from this file; - is stdin")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if *bodyFile == "" {
			return exit.Usagef("--body-file is required")
		}
		text, err := readText(*bodyFile, env.Stdin)
		if err != nil {
			return err
		}
		return withDeps(env, func(cmd *cobra.Command, _ []string, d *deps) error {
			id, err := d.Resolver.Epic(args[0])
			if err != nil {
				return err
			}
			raw, err := d.Doer.Raw(cmd.Context(), "POST", "epics/"+strconv.FormatInt(id, 10)+"/comments", nil,
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
