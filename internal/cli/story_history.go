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

// historyIn is a StoryHistoryChange. It has no "story" key, so Story stays 0.
type historyIn struct {
	HistoryOut
	Entity struct {
		ID int64 `json:"id"`
	} `json:"entity"`
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
	limitFlag := valuesFlag(cmd.Flags(), "limit", "", strconv.Itoa(defaultLimit), "maximum number of changes, or all", limitValues)
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
		q := url.Values{"order_dir": {"asc"}}
		if *descFlag {
			q.Set("order_dir", "desc")
		}
		fieldsQuery(q, attrs)
		many := len(uniq) > 1
		path := "stories/" + strconv.FormatInt(uniq[0], 10) + "/history"
		if many {
			path = "stories/history"
			q.Set("ids", joinIDs(uniq, ","))
		}
		return withDeps(env, func(cmd *cobra.Command, _ []string, d *deps) error {
			var seen map[int64]bool
			err := printPagesKey(cmd, d, path, q, "changes", limit, func(raw []json.RawMessage) ([]HistoryOut, error) {
				out, s, err := shapeHistory(raw, d, many, len(attrs) != 1)
				seen = s
				return out, err
			})
			// A list cut short returns its error here, so the check is skipped.
			// getStoryHistory gives 404 for an unknown story.
			if err != nil || !many {
				return err
			}
			return checkStories(cmd.Context(), d, idsNotIn(uniq, seen))
		})(cmd, args)
	}
	return cmd
}

// historyAttributes maps a --fields list to API attribute names. The API
// checks the names: it gives the valid list on a bad one.
func historyAttributes(list string) []string {
	var out []string
	for _, f := range strings.Split(list, ",") {
		if f = apiName(strings.TrimSpace(f)); f != "" && !slices.Contains(out, f) {
			out = append(out, f)
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
	q := storiesQuery(ids)
	q.Set("fields", "id")
	raw, err := transport.PageQuery(ctx, d.Doer, "stories", q, -1)
	if err != nil {
		return err
	}
	ss, err := decodeAll[struct {
		ID int64 `json:"id"`
	}](raw, "story")
	if err != nil {
		return err
	}
	found := map[int64]bool{}
	for _, s := range ss {
		found[s.ID] = true
	}
	return noStory(ids, found)
}

// historyMember is a member in the history. Name is the name that the API
// sent. It is kept only when the cache has no mention name, so that the
// member is not only an ID.
type historyMember struct {
	memberOut
	Name string `json:"name,omitempty"`
}

// shapeHistory converts StoryHistoryChange items to the compact shape. It
// also returns the stories that have a change.
func shapeHistory(raw []json.RawMessage, d *deps, withStory, withAttribute bool) ([]HistoryOut, map[int64]bool, error) {
	var members memberRefs
	seen := map[int64]bool{}
	out := make([]HistoryOut, 0, len(raw))
	for _, r := range raw {
		var c historyIn
		dec := json.NewDecoder(bytes.NewReader(r))
		dec.UseNumber() // keep big IDs exact
		if err := dec.Decode(&c); err != nil {
			return nil, nil, &exit.Error{Code: exit.Other, Msg: fmt.Sprintf("bad history data: %v", err)}
		}
		seen[c.Entity.ID] = true
		h := c.HistoryOut
		h.Actor, h.Removes, h.Adds = members.slim(h.Actor), members.slims(h.Removes), members.slims(h.Adds)
		if withStory {
			h.Story = c.Entity.ID
		}
		if !withAttribute {
			h.Attribute = ""
		}
		out = append(out, h)
	}
	var ids []string
	for _, m := range members {
		if !slices.Contains(ids, m.ID) { // a member can be in many changes
			ids = append(ids, m.ID)
		}
	}
	nm, err := memberNames(d.Resolver, ids)
	if err != nil {
		return nil, nil, err
	}
	for _, m := range members {
		if s, ok := nm[m.ID]; ok {
			m.MentionName, m.Name = &s, ""
		}
	}
	return out, seen, nil
}

// memberRefs collects the members that slim puts in the output, so that one
// cache lookup after the walk gives their mention names.
type memberRefs []*historyMember

func (r *memberRefs) slims(vs []any) []any {
	out := make([]any, 0, len(vs))
	for _, v := range vs {
		out = append(out, r.slim(v))
	}
	return out
}

// slim makes a slim entity of the history compact. A member is
// {id, mention_name}, as in story view, plus name when the cache does not
// have the member. An entity with a name (state, epic, team, label, branch) is
// {id, name}. Other entities lose entity_type, uri and app_url. Other values
// stay as they are.
func (r *memberRefs) slim(v any) any {
	switch x := v.(type) {
	case map[string]any:
		if x["entity_type"] == "member:slim" {
			m := &historyMember{}
			m.ID, _ = x["id"].(string)
			m.Name, _ = x["name"].(string)
			*r = append(*r, m)
			return m
		}
		if name, ok := x["name"]; ok {
			return map[string]any{"id": x["id"], "name": name}
		}
		out := map[string]any{}
		for k, e := range x {
			if k != "entity_type" && k != "uri" && k != "app_url" {
				out[k] = r.slim(e)
			}
		}
		return out
	case []any:
		return r.slims(x)
	}
	return v
}
