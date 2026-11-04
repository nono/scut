package cli

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nono/scut/internal/config"
	"github.com/nono/scut/internal/exit"
	"github.com/nono/scut/internal/transport"
)

// createRT answers the create calls of createStoryRetry. The first POST fails
// with err; each other POST gets 201, and each list an empty list.
type createRT struct {
	mu           sync.Mutex
	err          error
	posts, lists int
}

func (rt *createRT) RoundTrip(r *http.Request) (*http.Response, error) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	body := `{"entities":[]}`
	status := http.StatusOK
	switch r.Method {
	case http.MethodPost:
		rt.posts++
		if rt.posts == 1 {
			return nil, rt.err
		}
		body, status = `{"entity":{"id":5818}}`, http.StatusCreated
	case http.MethodGet:
		rt.lists++
	}
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    r,
	}, nil
}

func createDeps(c *http.Client, base string) *deps {
	s := config.Settings{Workspace: "acme", Token: "tok"}
	d := transport.New(s, transport.WithBaseURL(base), transport.WithHTTPClient(c), transport.WithSleep(func(time.Duration) {}))
	return &deps{Settings: s, Doer: d, Clock: transport.Clock{Sleep: func(time.Duration) {}}}
}

// A dial failure did not send the create: it looks for the story, then sends
// the create again.
func TestCreateResendAfterDialError(t *testing.T) {
	rt := &createRT{err: &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("refused")}}
	d := createDeps(&http.Client{Transport: rt}, "https://api.example")
	out, err := createStoryRetry(context.Background(), d, map[string]any{"name": "x", "external_id": "e1"})
	if err != nil || out == nil || rt.posts != 2 || rt.lists != 1 {
		t.Errorf("out %s, err %v, creates %d, lists %d, want 2, 1", out, err, rt.posts, rt.lists)
	}
}

// A bad certificate ends the create at once: the handshake did not end, so no
// story was made.
func TestCreateTLSError(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	t.Cleanup(srv.Close)
	var lists atomic.Int32
	rt := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodGet {
			lists.Add(1)
		}
		return http.DefaultTransport.RoundTrip(r) // it does not trust the server
	})
	d := createDeps(&http.Client{Transport: rt}, srv.URL)
	_, err := createStoryRetry(context.Background(), d, map[string]any{"name": "x", "external_id": "e1"})
	var e *exit.Error
	if !errors.As(err, &e) || e.Code != exit.Other || strings.Contains(e.Suggestion, "may exist") || lists.Load() != 0 {
		t.Errorf("err %v, lists %d, want exit 1, 0 lists; got %+v", err, lists.Load(), e)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
