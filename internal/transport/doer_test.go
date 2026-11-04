package transport_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nono/scut/internal/config"
	"github.com/nono/scut/internal/exit"
	"github.com/nono/scut/internal/testapi"
	"github.com/nono/scut/internal/transport"
)

const token = "sct_rw_acme_s3cret"

var settings = config.Settings{Token: token, Workspace: "acme"}

type waits struct{ d []time.Duration }

func (w *waits) sleep(d time.Duration) { w.d = append(w.d, d) }

func (w *waits) total() (t time.Duration) {
	for _, d := range w.d {
		t += d
	}
	return t
}

// newDoer returns a Doer on a fake server that answers with the given
// statuses in order, then repeats the last one.
func newDoer(t *testing.T, w *waits, method string, h http.HandlerFunc) *transport.Doer {
	t.Helper()
	base := testapi.Fake(t, map[string]http.HandlerFunc{method + " /api/v4/acme/stories/1": h})
	return transport.New(settings, transport.WithBaseURL(base), transport.WithSleep(w.sleep))
}

func statuses(codes ...int) (http.HandlerFunc, *atomic.Int32) {
	var n atomic.Int32
	return func(rw http.ResponseWriter, r *http.Request) {
		i := int(n.Add(1)) - 1
		if i >= len(codes) {
			i = len(codes) - 1
		}
		if codes[i] == 200 {
			rw.Header().Set("Content-Type", "application/json")
			_, _ = rw.Write([]byte(`{"entity":{"id":1}}`))
			return
		}
		rw.WriteHeader(codes[i])
	}, &n
}

func raw(d *transport.Doer, method string) error {
	_, err := d.Raw(context.Background(), method, "/stories/1", nil, nil)
	return err
}

func asExit(t *testing.T, err error) *exit.Error {
	t.Helper()
	var e *exit.Error
	if !errors.As(err, &e) {
		t.Fatalf("not an *exit.Error: %v", err)
	}
	return e
}

func TestDoerAuthHeader(t *testing.T) {
	var auth, ua string
	d := newDoer(t, &waits{}, "GET", func(rw http.ResponseWriter, r *http.Request) {
		auth, ua = r.Header.Get("Authorization"), r.Header.Get("User-Agent")
		_, _ = rw.Write([]byte(`{}`))
	})
	_ = raw(d, "GET")
	if auth != "Bearer "+token {
		t.Errorf("Authorization %q", auth)
	}
	if ua != "scut/dev" {
		t.Errorf("User-Agent %q", ua)
	}
}

func TestDoerRetries(t *testing.T) {
	w := &waits{}
	h, n := statuses(503, 503, 200)
	if err := raw(newDoer(t, w, "GET", h), "GET"); err != nil {
		t.Fatal(err)
	}
	if n.Load() != 3 || len(w.d) != 2 {
		t.Errorf("tries %d, waits %v", n.Load(), w.d)
	}
	if w.d[0] < 400*time.Millisecond || w.d[0] > 600*time.Millisecond || w.d[1] < 800*time.Millisecond || w.d[1] > 1200*time.Millisecond {
		t.Errorf("backoff out of range: %v", w.d)
	}
}

func TestDoerRetryAfter(t *testing.T) {
	w := &waits{}
	var n atomic.Int32
	d := newDoer(t, w, "GET", func(rw http.ResponseWriter, r *http.Request) {
		if n.Add(1) == 1 {
			rw.Header().Set("Retry-After", "2")
			rw.WriteHeader(429)
			return
		}
		_, _ = rw.Write([]byte(`{}`))
	})
	if err := raw(d, "GET"); err != nil {
		t.Fatal(err)
	}
	if len(w.d) != 1 || w.d[0] != 2*time.Second {
		t.Errorf("waits %v", w.d)
	}
}

func TestDoerGivesUp(t *testing.T) {
	w := &waits{}
	h, n := statuses(504)
	err := raw(newDoer(t, w, "GET", h), "GET")
	e := asExit(t, err)
	if e.Code != exit.Unavailable || e.Status != 504 {
		t.Errorf("%+v", e)
	}
	if n.Load() != 4 {
		t.Errorf("tries %d, want 4", n.Load())
	}
}

func TestDoerBudget(t *testing.T) {
	w := &waits{}
	var n atomic.Int32
	d := newDoer(t, w, "GET", func(rw http.ResponseWriter, r *http.Request) {
		n.Add(1)
		rw.Header().Set("Retry-After", "9")
		rw.WriteHeader(503)
	})
	err := raw(d, "GET")
	if e := asExit(t, err); e.Code != exit.Unavailable {
		t.Errorf("%+v", e)
	}
	if w.total() > 20*time.Second {
		t.Errorf("waited %v", w.total())
	}
	if n.Load() != 3 {
		t.Errorf("tries %d, want 3 (9+9 fits, a third wait does not)", n.Load())
	}
}

func TestDoerNoRetryPOST(t *testing.T) {
	for _, m := range []string{"POST", "PATCH", "DELETE"} {
		w := &waits{}
		h, n := statuses(504)
		e := asExit(t, raw(newDoer(t, w, m, h), m))
		if n.Load() != 1 || len(w.d) != 0 || e.Code != exit.Unavailable {
			t.Errorf("%s: tries %d, waits %v, code %d", m, n.Load(), w.d, e.Code)
		}
	}
}

func TestDoerNonJSON5xx(t *testing.T) {
	d := newDoer(t, &waits{}, "GET", func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("Content-Type", "text/html")
		rw.WriteHeader(503)
		_, _ = rw.Write([]byte("<html>down</html>"))
	})
	e := asExit(t, raw(d, "GET"))
	if e.Code != exit.Unavailable || len(e.API) != 0 {
		t.Errorf("%+v", e)
	}
}

func TestDoerErrorBody(t *testing.T) {
	const body = `{"message":"Cannot create a duplicate Story Link."}`
	d := newDoer(t, &waits{}, "POST", func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("Content-Type", "application/json")
		rw.WriteHeader(422)
		_, _ = rw.Write([]byte(body))
	})
	e := asExit(t, raw(d, "POST"))
	if e.Code != exit.Other || e.Status != 422 || string(e.API) != body {
		t.Errorf("%+v", e)
	}
}

func TestDoerStatusMap(t *testing.T) {
	for status, want := range map[int]exit.Code{401: 4, 403: 4, 404: 3, 400: 2} {
		h, _ := statuses(status)
		e := asExit(t, raw(newDoer(t, &waits{}, "GET", h), "GET"))
		if e.Code != want || e.Status != status {
			t.Errorf("%d: %+v, want code %d", status, e, want)
		}
	}
}

func TestDoerTokenNotInError(t *testing.T) {
	d := transport.New(settings, transport.WithBaseURL("http://127.0.0.1:1"), transport.WithSleep(func(time.Duration) {}))
	err := raw(d, "GET")
	e := asExit(t, err)
	if e.Code != exit.Unavailable {
		t.Errorf("code %d", e.Code)
	}
	if strings.Contains(err.Error(), token) {
		t.Errorf("token in error: %v", err)
	}
}

func TestDoerCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	h, _ := statuses(503)
	base := testapi.Fake(t, map[string]http.HandlerFunc{"GET /api/v4/acme/stories/1": h})
	d := transport.New(settings, transport.WithBaseURL(base), transport.WithSleep(func(time.Duration) { cancel() }))
	_, err := d.Raw(ctx, "GET", "/stories/1", nil, nil)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("got %v", err)
	}
}

func TestRawPaths(t *testing.T) {
	var got []string
	h := func(rw http.ResponseWriter, r *http.Request) {
		got = append(got, r.URL.RequestURI())
		_, _ = rw.Write([]byte(`{}`))
	}
	base := testapi.Fake(t, map[string]http.HandlerFunc{
		"GET /api/v4/acme/stories/5409": h,
		"GET /api/v4/whoami":            h,
	})
	d := transport.New(settings, transport.WithBaseURL(base))
	if d.Slug() != "acme" {
		t.Errorf("slug %q", d.Slug())
	}
	_, _ = d.Raw(context.Background(), "GET", "/stories/5409", map[string][]string{"fields": {"a"}}, nil)
	_, _ = d.Raw(context.Background(), "GET", "/api/v4/whoami", nil, nil)
	want := []string{"/api/v4/acme/stories/5409?fields=a", "/api/v4/whoami"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestRawUnwrap(t *testing.T) {
	for body, want := range map[string]string{
		`{"entity":{"id":1}}`:                         `{"id":1}`,
		`{"entities":[{"id":1},{"id":2}],"next":"c"}`: `[{"id":1},{"id":2}]`,
	} {
		d := newDoer(t, &waits{}, "GET", func(rw http.ResponseWriter, r *http.Request) {
			rw.Header().Set("Content-Type", "application/json")
			_, _ = rw.Write([]byte(body))
		})
		got, err := d.Raw(context.Background(), "GET", "/stories/1", nil, nil)
		if err != nil || string(got) != want {
			t.Errorf("%s: got %s, %v", body, got, err)
		}
	}
}

func TestPrismGetStory(t *testing.T) {
	base := testapi.Prism(t)
	d := transport.New(settings, transport.WithBaseURL(base))
	resp, err := d.Client().GetStoryWithResponse(context.Background(), d.Slug(), 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode() != 200 {
		t.Errorf("status %d: %s", resp.StatusCode(), resp.Body)
	}
	if !json.Valid(resp.Body) {
		t.Errorf("body not JSON")
	}
}
