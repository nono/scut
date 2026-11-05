package cli

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net/url"
	"strconv"
	"strings"

	"github.com/nono/scut/internal/exit"
	"github.com/nono/scut/internal/resolve"
	"github.com/nono/scut/internal/transport"
)

// bulkSize is the most stories of one PATCH /stories/bulk, and of one read.
const bulkSize = maxPageSize

// updateMany makes the change of body, u and specs to each of ids (two or
// more, no repeats), in chunks of bulkSize. It resolves each name before the
// first write. See docs/design/2026-11-05-bulk-design.md.
func updateMany(ctx context.Context, d *deps, ids []int64, u updateFlags, changed func(string) bool, specs []fieldSpec, body map[string]any) error {
	r := d.Resolver
	workflows, err := storyWorkflows(ctx, d, ids)
	if err != nil {
		return err
	}
	states := map[int64]int64{} // story ID to its new state
	if changed("state") {
		if err := resolveStates(r, u, ids, workflows, states); err != nil {
			return err
		}
	}
	if err := resolveCommon(r, u, changed, specs, body); err != nil {
		return err
	}
	done := []StoryOut{}
	for start := 0; start < len(ids); start += bulkSize {
		chunk := ids[start:min(start+bulkSize, len(ids))]
		items := make([]map[string]any, 0, len(chunk))
		for _, id := range chunk {
			item := maps.Clone(body)
			item["id"] = id
			if sid, ok := states[id]; ok {
				item["workflow_state_id"] = sid
			}
			items = append(items, item)
		}
		q := url.Values{"limit": {strconv.Itoa(len(chunk))}}
		raw, err := d.Doer.Raw(ctx, "PATCH", "stories/bulk", q, map[string]any{"stories": items})
		if err != nil {
			return partial(d, done, notKnown(err, chunk))
		}
		outs, err := bulkReply(raw, chunk, body, r)
		if err != nil {
			return partial(d, done, err)
		}
		done = append(done, outs...)
	}
	return d.Out(done)
}

// storyWorkflows reads the workflow of each story. A story that is not there is exit 3.
func storyWorkflows(ctx context.Context, d *deps, ids []int64) (map[int64]int64, error) {
	out := map[int64]int64{}
	for start := 0; start < len(ids); start += bulkSize {
		chunk := ids[start:min(start+bulkSize, len(ids))]
		q := url.Values{"ids": {joinIDs(chunk, ",")}, "filter": {"include_archived"},
			"fields": {"id,workflow"}, "limit": {strconv.Itoa(len(chunk))}}
		items, err := transport.PageQuery(ctx, d.Doer, "stories", q, -1)
		if err != nil {
			return nil, err
		}
		for _, raw := range items {
			var s struct {
				ID       int64 `json:"id"`
				Workflow *struct {
					ID int64 `json:"id"`
				} `json:"workflow"`
			}
			if err := json.Unmarshal(raw, &s); err != nil {
				return nil, &exit.Error{Code: exit.Other, Msg: "bad story data: " + err.Error()}
			}
			if s.Workflow != nil {
				out[s.ID] = s.Workflow.ID
			} else {
				out[s.ID] = 0
			}
		}
	}
	if missing := idsNotIn(ids, out); len(missing) > 0 {
		return nil, exit.NotFoundf("no story %s: no story is changed", joinIDs(missing, ", "))
	}
	return out, nil
}

// resolveStates puts in states the ID of the state u.state for each story: in
// u.workflow if given, else in the workflow of the story.
func resolveStates(r *resolve.R, u updateFlags, ids []int64, workflows, states map[int64]int64) error {
	byWorkflow := map[int64]int64{}
	for _, id := range ids {
		wf := workflows[id]
		if u.workflow != "" {
			wf = -1 // one state for all stories
		}
		sid, ok := byWorkflow[wf]
		if !ok {
			var err error
			if u.workflow != "" {
				var w int64
				if w, err = r.Workflow(u.workflow); err == nil {
					sid, err = r.State(u.state, w, "")
				}
			} else {
				sid, err = r.State(u.state, wf, "")
			}
			if err != nil {
				return err
			}
			byWorkflow[wf] = sid
		}
		states[id] = sid
	}
	return nil
}

// bulkReply checks the reply of one chunk and shapes its stories, in chunk order.
func bulkReply(raw json.RawMessage, chunk []int64, body map[string]any, r *resolve.R) ([]StoryOut, error) {
	var list []json.RawMessage
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, &exit.Error{Code: exit.Other, Msg: "bad reply to the bulk write; the write is saved: " + err.Error()}
	}
	byID := map[int64]json.RawMessage{}
	for _, s := range list {
		var head struct {
			ID int64 `json:"id"`
		}
		if json.Unmarshal(s, &head) == nil {
			byID[head.ID] = s
		}
	}
	if missing := idsNotIn(chunk, byID); len(missing) > 0 {
		return nil, &exit.Error{Code: exit.Other,
			Msg:        "the reply does not list story " + joinIDs(missing, ", ") + ". The write is saved.",
			Suggestion: "read the stories with scut story view " + joinIDs(chunk, " ")}
	}
	outs := make([]StoryOut, 0, len(chunk))
	for _, id := range chunk {
		if err := checkParent(byID[id], body); err != nil {
			var e *exit.Error
			if errors.As(err, &e) {
				e.Msg = "story " + strconv.FormatInt(id, 10) + ": " + e.Msg
			}
			return nil, err
		}
		out, err := shapeStory(byID[id], r, shapeOpts{})
		if err != nil {
			return nil, err
		}
		outs = append(outs, out)
	}
	return outs, nil
}

// notKnown marks the error of a failed bulk write: the API does not say if a
// bulk write is atomic, so each story of the chunk may be changed or not.
func notKnown(err error, chunk []int64) error {
	var e *exit.Error
	if !errors.As(err, &e) {
		e = &exit.Error{Code: exit.Other, Msg: err.Error()}
	}
	e.Suggestion = "these stories may be partly changed, as the API does not say if a bulk write is atomic: " +
		"read them with scut story view " + joinIDs(chunk, " ")
	return e
}

// partial prints the stories of the chunks that are done, then returns err.
func partial(d *deps, done []StoryOut, err error) error {
	if len(done) > 0 {
		if perr := d.Out(done); perr != nil {
			return perr
		}
	}
	return err
}

func joinIDs(ids []int64, sep string) string {
	s := make([]string, 0, len(ids))
	for _, id := range ids {
		s = append(s, strconv.FormatInt(id, 10))
	}
	return strings.Join(s, sep)
}

// idsNotIn returns the ids that are not keys of m, in order.
func idsNotIn[V any](ids []int64, m map[int64]V) []int64 {
	var out []int64
	for _, id := range ids {
		if _, ok := m[id]; !ok {
			out = append(out, id)
		}
	}
	return out
}
