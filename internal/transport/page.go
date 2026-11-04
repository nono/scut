package transport

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/nono/scut/internal/exit"
)

const maxPages = 100

// Page calls fetch until the cursor is empty. limit < 0 means all items.
// Otherwise it returns at most limit items. When the cursor repeats, or after
// 100 pages, it stops and returns the items read with an *exit.Error (exit 1).
// Any other error comes with no items.
func Page(ctx context.Context, fetch func(cursor string) ([]json.RawMessage, string, error), limit int) ([]json.RawMessage, error) {
	if limit == 0 {
		return nil, nil
	}
	var out []json.RawMessage
	seen := map[string]bool{}
	cursor := ""
	var stop error
	for n := 1; ; n++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		items, next, err := fetch(cursor)
		if err != nil {
			return nil, err
		}
		out = append(out, items...)
		if next == "" || (limit >= 0 && len(out) >= limit) {
			break
		}
		if seen[next] {
			stop = &exit.Error{Code: exit.Other, Msg: fmt.Sprintf("stopped after %d pages: the cursor repeats", n)}
			break
		}
		if n == maxPages {
			stop = &exit.Error{Code: exit.Other, Msg: fmt.Sprintf("stopped after %d pages: page limit", n)}
			break
		}
		seen[next] = true
		cursor = next
	}
	if limit >= 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, stop
}

// PageQuery reads a list endpoint, up to limit items (all with limit < 0).
// Page 1 sends query. Each later page sends only the cursor: the API refuses
// other parameters with a cursor.
func PageQuery(ctx context.Context, d *Doer, path string, query url.Values, limit int) ([]json.RawMessage, error) {
	return Page(ctx, func(cursor string) ([]json.RawMessage, string, error) {
		q := query
		if cursor != "" {
			q = url.Values{"cursor": {cursor}}
		}
		return d.List(ctx, path, q)
	}, limit)
}
