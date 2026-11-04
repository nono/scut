package transport

import (
	"context"
	"encoding/json"
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
