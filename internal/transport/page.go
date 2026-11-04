package transport

import (
	"context"
	"encoding/json"
	"net/url"
)

const maxPages = 100

// Page calls fetch until the cursor is empty or repeats, or after 100 pages.
// limit < 0 means all items. Otherwise it returns at most limit items.
func Page(ctx context.Context, fetch func(cursor string) ([]json.RawMessage, string, error), limit int) ([]json.RawMessage, error) {
	var out []json.RawMessage
	seen := map[string]bool{}
	cursor := ""
	for range maxPages {
		if limit >= 0 && len(out) >= limit {
			break
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		items, next, err := fetch(cursor)
		if err != nil {
			return nil, err
		}
		out = append(out, items...)
		if next == "" || seen[next] {
			break
		}
		seen[next] = true
		cursor = next
	}
	if limit >= 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
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
