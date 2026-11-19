package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/nono/scut/internal/exit"
	"github.com/nono/scut/internal/transport"
)

// HistoryOut is one change of a story. Story is set only when the command
// asks for several stories, and Attribute only when --fields does not name a
// single attribute: else they repeat the request.
type HistoryOut struct {
	Timestamp string `json:"timestamp"`
	Story     int64  `json:"story,omitempty"`
	Actor     any    `json:"actor,omitempty"`
	Action    string `json:"action,omitempty"`
	Attribute string `json:"attribute,omitempty"`
	Removes   []any  `json:"removes,omitempty"`
	Adds      []any  `json:"adds,omitempty"`
}

type historyIn struct {
	Timestamp string `json:"timestamp"`
	Entity    struct {
		ID int64 `json:"id"`
	} `json:"entity"`
	Actor     any    `json:"actor"`
	Action    string `json:"action"`
	Attribute string `json:"attribute"`
	Removes   []any  `json:"removes"`
	Adds      []any  `json:"adds"`
}

func newStoryHistory(env Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "history [<story>…]",
		Short: "List the changes of stories, oldest first; default story from the git branch",
		Long: `List the changes of stories, oldest first. With several stories, the
changes of all stories are in one list, in time order.
--fields takes API attribute names (workflow_state, owners, epic, …) and the
story view names state, type, tasks, links and fields.`,
		Annotations: map[string]string{OpsAnnotation: "getStoryHistory,getMultipleStoriesHistory,listStories,listMembers", IDArgsAnnotation: "sc-"},
	}
	fieldsFlag := cmd.Flags().String("fields", "", "comma-separated attributes to show the changes of, for example workflow_state,owners")
	limitFlag := cmd.Flags().String("limit", strconv.Itoa(defaultLimit), "maximum number of changes, or all")
	descFlag := cmd.Flags().Bool("desc", false, "newest first")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		limit, err := parseLimit(*limitFlag)
		if err != nil {
			return err
		}
		ids, err := storyIDs(env, args)
		if err != nil {
			return err
		}
		uniq := uniqueIDs(ids)
		if len(uniq) > maxPageSize {
			return exit.Usagef("story history takes at most %d stories, not %d", maxPageSize, len(uniq))
		}
		attrs := historyAttributes(*fieldsFlag)
		q := url.Values{"order_dir": {"asc"}, "limit": {strconv.Itoa(pageSize(limit))}}
		if *descFlag {
			q.Set("order_dir", "desc")
		}
		if len(attrs) > 0 {
			q.Set("fields", strings.Join(attrs, ","))
		}
		path := "stories/" + strconv.FormatInt(uniq[0], 10) + "/history"
		if len(uniq) > 1 {
			path = "stories/history"
			q.Set("ids", joinIDs(uniq, ","))
		}
		return withDeps(env, func(cmd *cobra.Command, _ []string, d *deps) error {
			ctx := cmd.Context()
			raw, stop := transport.PageQueryKey(ctx, d.Doer, path, q, "changes", limit)
			if raw == nil && stop != nil {
				return stop
			}
			out, seen, err := shapeHistory(raw, d, len(uniq) > 1, len(attrs) != 1)
			if err != nil {
				return err
			}
			if err := d.Out(out); err != nil {
				return err
			}
			if stop != nil || len(uniq) == 1 { // getStoryHistory gives 404 for an unknown story
				return stop
			}
			return checkStories(ctx, d, idsNotIn(uniq, seen))
		})(cmd, args)
	}
	return cmd
}

// historyAttributes maps a --fields list to API attribute names. The API
// checks the names: it gives the valid list on a bad one.
func historyAttributes(list string) []string {
	var out []string
	for _, f := range strings.Split(list, ",") {
		if f = strings.TrimSpace(f); f != "" && !slices.Contains(out, apiName(f)) {
			out = append(out, apiName(f))
		}
	}
	return out
}

// checkStories fails with exit 3 if a story of ids does not exist.
// getMultipleStoriesHistory gives no error for an unknown story, so a story
// with no change in the output can be a wrong ID.
func checkStories(ctx context.Context, d *deps, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	q := url.Values{"ids": {joinIDs(ids, ",")}, "filter": {"include_archived"}, "fields": {"id"}, "limit": {strconv.Itoa(len(ids))}}
	raw, err := transport.PageQuery(ctx, d.Doer, "stories", q, -1)
	if err != nil {
		return err
	}
	found := map[int64]bool{}
	for _, r := range raw {
		var s struct {
			ID int64 `json:"id"`
		}
		if err := json.Unmarshal(r, &s); err != nil {
			return &exit.Error{Code: exit.Other, Msg: fmt.Sprintf("bad story data: %v", err)}
		}
		found[s.ID] = true
	}
	if missing := idsNotIn(ids, found); len(missing) > 0 {
		return exit.NotFoundf("no story %s", joinIDs(missing, ", "))
	}
	return nil
}

// shapeHistory converts StoryHistoryChange items to the compact shape. It
// also returns the stories that have a change.
func shapeHistory(raw []json.RawMessage, d *deps, withStory, withAttribute bool) ([]HistoryOut, map[int64]bool, error) {
	in := make([]historyIn, 0, len(raw))
	var memberIDs []string
	for _, r := range raw {
		var c historyIn
		dec := json.NewDecoder(bytes.NewReader(r))
		dec.UseNumber() // keep big IDs exact
		if err := dec.Decode(&c); err != nil {
			return nil, nil, &exit.Error{Code: exit.Other, Msg: fmt.Sprintf("bad history data: %v", err)}
		}
		for _, v := range append(append([]any{c.Actor}, c.Adds...), c.Removes...) {
			memberIDs = append(memberIDs, slimMembers(v)...)
		}
		in = append(in, c)
	}
	nm, err := memberNames(d.Resolver, memberIDs)
	if err != nil {
		return nil, nil, err
	}
	seen := map[int64]bool{}
	out := make([]HistoryOut, 0, len(in))
	for _, c := range in {
		seen[c.Entity.ID] = true
		h := HistoryOut{Timestamp: c.Timestamp, Actor: nm.slim(c.Actor), Action: c.Action,
			Removes: nm.slims(c.Removes), Adds: nm.slims(c.Adds)}
		if withStory {
			h.Story = c.Entity.ID
		}
		if withAttribute {
			h.Attribute = c.Attribute
		}
		out = append(out, h)
	}
	return out, seen, nil
}

// slimMembers returns the IDs of the member:slim objects in v.
func slimMembers(v any) []string {
	var ids []string
	switch x := v.(type) {
	case map[string]any:
		if x["entity_type"] == "member:slim" {
			if id, ok := x["id"].(string); ok {
				ids = append(ids, id)
			}
		}
		for _, e := range x {
			ids = append(ids, slimMembers(e)...)
		}
	case []any:
		for _, e := range x {
			ids = append(ids, slimMembers(e)...)
		}
	}
	return ids
}

func (n names) slims(vs []any) []any {
	out := make([]any, 0, len(vs))
	for _, v := range vs {
		out = append(out, n.slim(v))
	}
	return out
}

// historyMember is a member in the history. Name is the name that the API
// sent. It is set only when the cache has no mention name, so that the
// member is not only an ID.
type historyMember struct {
	memberOut
	Name string `json:"name,omitempty"`
}

// slim makes a slim entity of the history compact, as story view shows it: a
// member is {id, mention_name}, plus name when the cache does not have the
// member. An entity with a name (state, epic, team, label, branch) is
// {id, name}. Other entities lose entity_type, uri and app_url. Other values
// stay as they are.
func (n names) slim(v any) any {
	switch x := v.(type) {
	case map[string]any:
		if x["entity_type"] == "member:slim" {
			id, _ := x["id"].(string)
			m := historyMember{memberOut: *n.member(&memberIn{ID: id})}
			if m.MentionName == nil {
				m.Name, _ = x["name"].(string)
			}
			return m
		}
		if name, ok := x["name"]; ok {
			return map[string]any{"id": x["id"], "name": name}
		}
		out := map[string]any{}
		for k, e := range x {
			if k != "entity_type" && k != "uri" && k != "app_url" {
				out[k] = n.slim(e)
			}
		}
		return out
	case []any:
		return n.slims(x)
	}
	return v
}
