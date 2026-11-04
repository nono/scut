package transport_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"testing"

	"github.com/nono/scut/internal/exit"
	"github.com/nono/scut/internal/testapi"
	"github.com/nono/scut/internal/transport"
)

func items(n int) []json.RawMessage {
	out := make([]json.RawMessage, n)
	for i := range out {
		out[i] = json.RawMessage(`{}`)
	}
	return out
}

// stopped checks that err is the exit 1 error of a list cut short.
func stopped(t *testing.T, err error, msg string) {
	t.Helper()
	var xe *exit.Error
	if !errors.As(err, &xe) || xe.Code != exit.Other || xe.Msg != msg {
		t.Errorf("err %v, want exit 1 %q", err, msg)
	}
}

func TestPagerRepeatCursor(t *testing.T) {
	calls := 0
	got, err := transport.Page(context.Background(), func(string) ([]json.RawMessage, string, error) {
		calls++
		return items(1), "string", nil
	}, -1)
	if calls != 2 || len(got) != 2 {
		t.Errorf("calls %d, items %d", calls, len(got))
	}
	stopped(t, err, "stopped after 2 pages: the cursor repeats")
}

func TestPagerCap(t *testing.T) {
	calls := 0
	got, err := transport.Page(context.Background(), func(string) ([]json.RawMessage, string, error) {
		calls++
		return items(1), fmt.Sprint("c", calls), nil
	}, -1)
	if calls != 100 || len(got) != 100 {
		t.Errorf("calls %d, items %d", calls, len(got))
	}
	stopped(t, err, "stopped after 100 pages: page limit")
}

// The limit is reached on the last page: the list is not cut short.
func TestPagerCapLimitReached(t *testing.T) {
	_, err := transport.Page(context.Background(), func(c string) ([]json.RawMessage, string, error) {
		return items(1), c + "x", nil
	}, 100)
	if err != nil {
		t.Errorf("err %v", err)
	}
}

func TestPagerLimit(t *testing.T) {
	calls := 0
	got, _ := transport.Page(context.Background(), func(string) ([]json.RawMessage, string, error) {
		calls++
		return items(25), fmt.Sprint("c", calls), nil
	}, 30)
	if calls != 2 || len(got) != 30 {
		t.Errorf("calls %d, items %d", calls, len(got))
	}
}

func TestPagerStopsOnEmptyCursor(t *testing.T) {
	calls := 0
	got, _ := transport.Page(context.Background(), func(string) ([]json.RawMessage, string, error) {
		calls++
		return items(3), "", nil
	}, -1)
	if calls != 1 || len(got) != 3 {
		t.Errorf("calls %d, items %d", calls, len(got))
	}
}

func TestPageQueryCursorAlone(t *testing.T) {
	base := testapi.Fake(t, map[string]http.HandlerFunc{"GET /api/v4/acme/things": func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		if q.Has("cursor") && len(q) > 1 {
			w.WriteHeader(422)
			_, _ = io.WriteString(w, `{"message":"Other parameters can't be used with cursor."}`)
			return
		}
		next := map[string]string{"": "c2", "c2": "c3", "c3": ""}[q.Get("cursor")]
		body := `{"entities":[{}]`
		if next != "" {
			body += `,"next_page_url":"/api/v4/acme/things?cursor=` + next + `"`
		}
		_, _ = io.WriteString(w, body+"}")
	}})
	d := transport.New(settings, transport.WithBaseURL(base))
	got, err := transport.PageQuery(context.Background(), d, "things", url.Values{"query": {"x"}, "limit": {"1"}}, -1)
	if err != nil || len(got) != 3 {
		t.Errorf("items %d, err %v", len(got), err)
	}
}
