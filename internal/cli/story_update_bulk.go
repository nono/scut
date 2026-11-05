package cli

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/nono/scut/internal/exit"
	"github.com/nono/scut/internal/resolve"
	"github.com/nono/scut/internal/transport"
)

// bulkSize is the most stories of one PATCH /stories/bulk ("up to 100" in the spec).
const bulkSize = 100

// updateMany makes the change of body and u to each of ids (two or more, no
// repeats), in chunks of bulkSize. It resolves each name before the first
// write. See docs/design/2026-11-05-bulk-design.md.
func updateMany(ctx context.Context, d *deps, ids []int64, u updateFlags, body map[string]any) error {
	r := d.Resolver
	workflows, err := storyWorkflows(ctx, d, ids)
	if err != nil {
		return err
	}
	var states map[int64]int64 // story ID to its new state
	if u.changed("state") {
		if states, err = resolveStates(r, u, ids, workflows); err != nil {
			return err
		}
	}
	if err := resolveCommon(r, u, body); err != nil {
		return err
	}
	done, err := writeChunks(ctx, ids, func(chunk []int64) (storyList, error) {
		items := make([]map[string]any, 0, len(chunk))
		for _, id := range chunk {
			item := maps.Clone(body)
			item["id"] = id
			if sid, ok := states[id]; ok {
				item["workflow_state_id"] = sid
			}
			items = append(items, item)
		}
		// Without include_archived, the reply leaves out archived stories.
		q := url.Values{"limit": {strconv.Itoa(len(chunk))}, "filter": {"include_archived"}}
		raw, err := d.Doer.Raw(ctx, "PATCH", "stories/bulk", q, map[string]any{"stories": items})
		if err != nil {
			return nil, notKnown(ctx, err, chunk)
		}
		outs, err := bulkReply(raw, chunk, body, r)
		if err != nil {
			return nil, saved(ctx, err, chunk)
		}
		return outs, nil
	})
	if len(done) > 0 || err == nil {
		// Print the chunks that are done, also before an error.
		if perr := d.Out(done); perr != nil {
			return perr
		}
	}
	return err
}

// writeChunks calls write for each chunk of bulkSize ids, in order, and
// returns the stories of the chunks that are done. It stops at the first
// error, or at a cancel before a chunk: that chunk and the next ones are not sent.
func writeChunks(ctx context.Context, ids []int64, write func(chunk []int64) (storyList, error)) (storyList, error) {
	done := storyList{}
	start := 0
	for chunk := range slices.Chunk(ids, bulkSize) {
		if ctx.Err() != nil {
			return done, &exit.Error{Code: exit.Cancelled, Msg: "cancelled",
				Suggestion: "no write was sent for stories " + joinIDs(ids[start:], " ") + ": they are not changed"}
		}
		outs, err := write(chunk)
		if err != nil {
			return done, err
		}
		done = append(done, outs...)
		start += len(chunk)
	}
	return done, nil
}

// storyWorkflows reads the workflow of each story. A story that is not there is exit 3.
func storyWorkflows(ctx context.Context, d *deps, ids []int64) (map[int64]int64, error) {
	out := map[int64]int64{}
	for chunk := range slices.Chunk(ids, maxPageSize) {
		q := url.Values{"ids": {joinIDs(chunk, ",")}, "filter": {"include_archived"},
			"fields": {"id,workflow"}, "limit": {strconv.Itoa(len(chunk))}}
		items, err := transport.PageQuery(ctx, d.Doer, "stories", q, -1)
		if err != nil {
			return nil, err
		}
		for _, raw := range items {
			id, wf, err := storyWorkflow(raw)
			if err != nil {
				return nil, err
			}
			out[id] = wf
		}
	}
	if missing := idsNotIn(ids, out); len(missing) > 0 {
		return nil, exit.NotFoundf("no story %s: no story is changed", joinIDs(missing, ", "))
	}
	return out, nil
}

// resolveStates returns, for each story, the ID of the state u.state: in
// --workflow if given, else in the workflow of the story. It resolves each
// workflow once: the resolver reads and decodes its cache file at each call
// (300 stories in 3 workflows: 195 ms with a call per story, 52 ms with this).
func resolveStates(r *resolve.R, u updateFlags, ids []int64, workflows map[int64]int64) (map[int64]int64, error) {
	var given int64
	hasWorkflow := u.changed("workflow")
	if hasWorkflow {
		var err error
		if given, err = r.Workflow(u.workflow); err != nil {
			return nil, err
		}
	}
	states := map[int64]int64{}
	byWorkflow := map[int64]int64{}
	for _, id := range ids {
		wf := workflows[id]
		if hasWorkflow {
			wf = given
		}
		sid, ok := byWorkflow[wf]
		if !ok {
			var err error
			if sid, err = r.State(u.state, wf, ""); err != nil {
				return nil, err
			}
			byWorkflow[wf] = sid
		}
		states[id] = sid
	}
	return states, nil
}

// bulkReply checks the reply of one chunk and shapes its stories, in chunk order.
func bulkReply(raw json.RawMessage, chunk []int64, body map[string]any, r *resolve.R) ([]StoryOut, error) {
	var list []json.RawMessage
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, &exit.Error{Code: exit.Other, Msg: "bad reply to the bulk write: " + err.Error()}
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
			Msg: "the reply does not list story " + joinIDs(missing, ", ")}
	}
	outs := make([]StoryOut, 0, len(chunk))
	for _, id := range chunk {
		if err := checkParent(byID[id], body); err != nil {
			var e *exit.Error
			if !errors.As(err, &e) {
				return nil, err
			}
			return nil, &exit.Error{Code: e.Code, Msg: "story " + strconv.FormatInt(id, 10) + ": " + e.Msg}
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
// bulk write is atomic, so each story of the chunk may be changed or not. A
// request that was not sent changed no story. After a cancel, the error has
// code 130, so that RunContext prints it.
func notKnown(ctx context.Context, err error, chunk []int64) error {
	e := chunkError(ctx, err, "cancelled during the write")
	e.Suggestion = "these stories may be partly changed, as the API does not say if a bulk write is atomic: " +
		"read them with scut story view " + joinIDs(chunk, " ")
	if e.NotSent {
		e.Suggestion = "the write was not sent: stories " + joinIDs(chunk, " ") + " are not changed"
	}
	return e
}

// chunkError returns a copy of err as an *exit.Error, or after a cancel a new
// error with code 130 and cancelMsg, so that RunContext prints it.
func chunkError(ctx context.Context, err error, cancelMsg string) *exit.Error {
	if ctx.Err() != nil {
		return &exit.Error{Code: exit.Cancelled, Msg: cancelMsg}
	}
	var e *exit.Error
	if !errors.As(err, &e) {
		return &exit.Error{Code: exit.Other, Msg: err.Error()}
	}
	c := *e
	return &c
}

// saved marks the error of a reply that fails a check after the chunk is
// saved. After a cancel, the error has code 130, so that RunContext prints it.
func saved(ctx context.Context, err error, chunk []int64) error {
	e := chunkError(ctx, err, "cancelled after the write")
	e.Suggestion = "the write of these stories is saved, but its reply did not pass the checks: " +
		"read them with scut story view " + joinIDs(chunk, " ")
	return e
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
