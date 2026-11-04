package cli

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	mrand "math/rand/v2"
	"net/url"
	"strconv"
	"time"

	"github.com/nono/scut/internal/exit"
	"github.com/nono/scut/internal/transport"
)

// The create retry follows the budget of the transport: 4 tries, and no new
// try after 20 s from the first. The transport does not retry a POST, because
// a retry after a timeout can make a second story.
const (
	createTries   = 4
	createBudget  = 20 * time.Second
	createBackoff = 500 * time.Millisecond
)

// newExternalID returns a random UUID (version 4). The story carries it, so a
// retry can tell if an earlier try made the story.
func newExternalID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err) // the system random source failed
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// findCreated looks in the newest stories for one with this external ID. It
// includes archived stories.
func findCreated(ctx context.Context, d *transport.Doer, externalID string) (json.RawMessage, bool, error) {
	items, _, err := d.List(ctx, "stories", url.Values{
		"order_by":  {"created_at"},
		"order_dir": {"desc"},
		"limit":     {"25"},
		"fields":    {"id,external_id,name,app_url"},
		"filter":    {"include_archived"},
	})
	if err != nil {
		return nil, false, err
	}
	for _, it := range items {
		var s struct {
			ExternalID string `json:"external_id"`
		}
		if json.Unmarshal(it, &s) == nil && s.ExternalID == externalID {
			return it, true, nil
		}
	}
	return nil, false, nil
}

func unavailable(err error) bool {
	var e *exit.Error
	return errors.As(err, &e) && e.Code == exit.Unavailable
}

// mayExist adds to err the advice to look for the story before a new create.
func mayExist(err error, externalID string) error {
	var e *exit.Error
	if !errors.As(err, &e) {
		return err
	}
	c := *e
	c.Suggestion = "the story may exist: search for external_id " + externalID + " before you create it again"
	return &c
}

// createStoryRetry sends the createStory request. body must hold external_id.
// After a failure with exit code 5 it looks for the story by external_id
// before each new try, and it never sends a create again while it cannot tell
// if the last one worked. A 4xx is never retried.
func createStoryRetry(ctx context.Context, d *deps, body map[string]any) (json.RawMessage, error) {
	extID, _ := body["external_id"].(string)
	now := d.Now
	if now == nil {
		now = time.Now
	}
	start := now()
	var total time.Duration
	uncertain := false // an earlier create may have worked
	for try := 1; ; try++ {
		var failure error
		found := false
		var raw json.RawMessage
		if uncertain {
			it, ok, err := findCreated(ctx, d.Doer, extID)
			switch {
			case err != nil:
				failure = err
			case ok:
				found, raw = true, it
			}
		}
		if found {
			var s struct {
				ID int64 `json:"id"`
			}
			if err := json.Unmarshal(raw, &s); err != nil || s.ID == 0 {
				return nil, mayExist(&exit.Error{Code: exit.Other, Msg: "bad story in list"}, extID)
			}
			full, err := d.Doer.Raw(ctx, "GET", "stories/"+strconv.FormatInt(s.ID, 10), nil, nil)
			if err != nil {
				return nil, mayExist(err, extID)
			}
			return full, nil
		}
		if failure == nil {
			out, err := d.Doer.Raw(ctx, "POST", "stories", nil, body)
			if err == nil {
				return out, nil
			}
			failure = err
			if unavailable(err) {
				uncertain = true
			}
		}
		if !unavailable(failure) {
			if uncertain {
				return nil, mayExist(failure, extID)
			}
			return nil, failure
		}
		wait := time.Duration(float64(createBackoff<<(try-1)) * (0.8 + 0.4*mrand.Float64()))
		if try >= createTries || max(now().Sub(start), total)+wait > createBudget {
			return nil, mayExist(failure, extID)
		}
		total += wait
		if err := sleepCtx(ctx, d.Sleep, wait); err != nil {
			return nil, fmt.Errorf("create story: %w", err)
		}
	}
}

// sleepCtx waits for w. With sleep set, it calls that instead of a timer.
func sleepCtx(ctx context.Context, sleep func(time.Duration), w time.Duration) error {
	if sleep != nil {
		sleep(w)
		return ctx.Err()
	}
	t := time.NewTimer(w)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
