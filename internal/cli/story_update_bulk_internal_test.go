package cli

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/nono/scut/internal/exit"
)

func TestNotKnown(t *testing.T) {
	chunk := []int64{12, 13}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, c := range []struct {
		name string
		ctx  context.Context
		err  error
		code exit.Code
		want string
	}{
		{"status", context.Background(), &exit.Error{Code: exit.Other, Status: 422, Msg: "m"}, exit.Other, "may be partly changed"},
		{"plain", context.Background(), errors.New("m"), exit.Other, "may be partly changed"},
		{"not sent", context.Background(), &exit.Error{Code: exit.Unavailable, NotSent: true, Msg: "dial"}, exit.Unavailable, "not sent: stories 12 13 are not changed"},
		{"cancel", cancelled, errors.New("context canceled"), exit.Cancelled, "may be partly changed"},
	} {
		var e *exit.Error
		if !errors.As(notKnown(c.ctx, c.err, chunk), &e) || e.Code != c.code || !strings.Contains(e.Suggestion, c.want) || !strings.Contains(e.Suggestion, "12 13") {
			t.Errorf("%s: %+v", c.name, e)
		}
	}
}

// A cancel between two chunks sends no more writes and names the stories that are not sent.
func TestWriteChunksCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ids := make([]int64, 0, 250)
	for i := range int64(250) {
		ids = append(ids, 6000+i)
	}
	calls := 0
	done, err := writeChunks(ctx, ids, func(chunk []int64) (storyList, error) {
		calls++
		cancel()
		return storyList{{ID: chunk[0]}}, nil
	})
	var e *exit.Error
	if calls != 1 || len(done) != 1 || !errors.As(err, &e) || e.Code != exit.Cancelled {
		t.Fatalf("calls %d, done %d, err %v", calls, len(done), err)
	}
	if !strings.Contains(e.Suggestion, "6100 6101") || !strings.Contains(e.Suggestion, "6249") || strings.Contains(e.Suggestion, "6099") || !strings.Contains(e.Suggestion, "not changed") {
		t.Errorf("suggestion %q", e.Suggestion)
	}
}
