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
