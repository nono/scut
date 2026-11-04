package cli

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	mrand "math/rand/v2"
	"net/http"
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

// cancelled is the error for a cancel after a create was sent: the story may
// exist, so the external_id must reach the user.
func cancelled(externalID string) error {
	return mayExist(&exit.Error{Code: exit.Cancelled, Msg: "cancelled"}, externalID)
}

// createOutcome tells what a failed create means for the next step.
type createOutcome int

const (
	createRefused createOutcome = iota // a 4xx (not 429): no story
	createResend                       // 429, 503, or no connection: the server did not take it
	createWait                         // 502, 504, no answer, or a cut body: the server may still save it
	createFinal                        // any other failure after the send: the story may exist
)

// classifyCreate returns the outcome of a create that failed with err.
func classifyCreate(err error) createOutcome {
	var e *exit.Error
	if !errors.As(err, &e) {
		return createFinal
	}
	switch {
	case e.Status == http.StatusTooManyRequests, e.Status == http.StatusServiceUnavailable, e.NotSent:
		return createResend
	case e.Status/100 == 4:
		return createRefused
	case e.Status == http.StatusBadGateway, e.Status == http.StatusGatewayTimeout,
		e.Status == 0 && e.Code == exit.Unavailable:
		return createWait
	}
	return createFinal
}

// unavailable returns err with exit code 5: a create may have taken effect.
func unavailable(err error) error {
	var e *exit.Error
	if !errors.As(err, &e) {
		return &exit.Error{Code: exit.Unavailable, Msg: err.Error()}
	}
	c := *e
	c.Code = exit.Unavailable
	return &c
}

// createStoryRetry sends the createStory request. body must hold external_id.
// Each failure after the send makes the result uncertain, except a 4xx. Then
// it looks for the story by external_id, and it never sends a create again
// while it cannot tell if the last one worked:
//
//   - After a 429, a 503 or a connect failure, the server did not take the
//     request: it looks for the story, then sends the create again.
//   - After a 502, a 504, no answer or a cut body, the server may still save
//     the story: it never sends the create again, and looks for the story with
//     the backoff until createBudget ends.
//   - After any other failure (a 500, an empty or bad 2xx body), it looks for
//     the story once.
//
// No create starts after createBudget from the first. When it gives up after
// an uncertain create that no find has checked yet, it looks for the story
// once more, even after the budget. Exit 5 then says that the story may exist.
//
// When the story exists but the full entity cannot be read, it returns the
// list item together with the error.
func createStoryRetry(ctx context.Context, d *deps, body map[string]any) (json.RawMessage, error) {
	extID, _ := body["external_id"].(string)
	now := d.Now
	if now == nil {
		now = time.Now
	}
	start := now()
	var total time.Duration
	elapsed := func() time.Duration { return max(now().Sub(start), total) }
	sent := false      // a create was sent
	uncertain := false // an earlier create may have worked
	checked := false   // a find ran after the last create
	resend := true     // a new create may follow a find
	var last error     // the last retryable failure
	fail := func(err error) error {
		switch {
		case sent && ctx.Err() != nil:
			return cancelled(extID)
		case uncertain:
			return mayExist(err, extID)
		}
		return err
	}
	// giveUp runs the last find when no find checked the last create.
	giveUp := func(err error) (json.RawMessage, error) {
		if uncertain && !checked && ctx.Err() == nil {
			if it, ok, ferr := findCreated(ctx, d.Doer, extID); ferr == nil && ok {
				return readCreated(ctx, d, it, extID)
			}
		}
		return nil, fail(err)
	}
	for try := 1; ; try++ {
		var failure error
		if uncertain {
			it, ok, err := findCreated(ctx, d.Doer, extID)
			switch {
			case err != nil:
				failure = err
			case ok:
				return readCreated(ctx, d, it, extID)
			default:
				checked = true
				if !resend {
					failure = last // look again later
				}
			}
		}
		if failure == nil {
			// The list call has its own budget: check ours again.
			if try > 1 && elapsed() > createBudget {
				return giveUp(last)
			}
			sent = true
			checked = false
			out, err := d.Doer.Raw(ctx, "POST", "stories", nil, body)
			if err == nil && out != nil {
				return out, nil
			}
			if ctx.Err() != nil {
				return nil, fail(err)
			}
			if err == nil {
				err = &exit.Error{Code: exit.Other, Msg: "POST /stories: empty response"}
			}
			switch classifyCreate(err) {
			case createRefused:
				return giveUp(err)
			case createFinal:
				uncertain = true
				return giveUp(unavailable(err))
			case createWait:
				resend = false
			}
			uncertain = true
			failure = err
		}
		last = failure
		if !transport.Retryable(failure) {
			return giveUp(failure)
		}
		wait := time.Duration(float64(createBackoff<<(try-1)) * (0.8 + 0.4*mrand.Float64()))
		var xe *exit.Error
		if errors.As(failure, &xe) && xe.RetryAfter > 0 {
			wait = xe.RetryAfter
		}
		// Finds alone stop at the budget only; a new create also stops at the try limit.
		if (resend && try >= createTries) || elapsed()+wait > createBudget {
			return giveUp(failure)
		}
		total += wait
		if err := sleepCtx(ctx, d.Sleep, wait); err != nil {
			return nil, fail(err)
		}
	}
}

// readCreated reads the full story of a list item that findCreated found.
func readCreated(ctx context.Context, d *deps, it json.RawMessage, extID string) (json.RawMessage, error) {
	var s struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(it, &s); err != nil || s.ID == 0 {
		return nil, mayExist(&exit.Error{Code: exit.Other, Msg: "bad story in list"}, extID)
	}
	full, err := d.Doer.Raw(ctx, "GET", "stories/"+strconv.FormatInt(s.ID, 10), nil, nil)
	if err != nil {
		return it, err
	}
	return full, nil
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
