package transport_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
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
		t.Fatalf("tries %d, waits %v", n.Load(), w.d)
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
		rw.Header().Set("Content-Type", "application/json")
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
	check := func(name string, err error) {
		t.Helper()
		e := asExit(t, err)
		if strings.Contains(e.Msg, token) || strings.Contains(e.Suggestion, token) || strings.Contains(string(e.API), token) || strings.Contains(err.Error(), token) {
			t.Errorf("%s: token in error: %+v", name, e)
		}
	}
	d := newDoer(t, &waits{}, "GET", func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("Content-Type", "application/json")
		rw.WriteHeader(422)
		_, _ = rw.Write([]byte(`{"message":"bad ` + token + `","detail":"` + token + `"}`))
	})
	err := raw(d, "GET")
	if e := asExit(t, err); !strings.Contains(e.Msg, "***") || !strings.Contains(string(e.API), "***") {
		t.Errorf("token not scrubbed: %+v", e)
	}
	check("body", err)

	d = transport.New(settings, transport.WithBaseURL("http://127.0.0.1:1"), transport.WithSleep(func(time.Duration) {}))
	err = raw(d, "GET")
	if e := asExit(t, err); e.Code != exit.Unavailable {
		t.Errorf("code %d", e.Code)
	}
	check("connect", err)
}

func TestDoerBudgetWallClock(t *testing.T) {
	now := time.Unix(1000, 0)
	var n atomic.Int32
	base := testapi.Fake(t, map[string]http.HandlerFunc{"GET /api/v4/acme/stories/1": func(rw http.ResponseWriter, r *http.Request) {
		n.Add(1)
		now = now.Add(15 * time.Second) // the try takes 15 s
		rw.WriteHeader(503)
	}})
	w := &waits{}
	d := transport.New(settings, transport.WithBaseURL(base), transport.WithSleep(func(x time.Duration) {
		w.sleep(x)
		now = now.Add(x)
	}), transport.WithClock(func() time.Time { return now }))
	e := asExit(t, raw(d, "GET"))
	if n.Load() != 2 || e.Status != 503 {
		t.Errorf("tries %d, status %d, waits %v", n.Load(), e.Status, w.d)
	}
}

type rtFunc func(*http.Request) (*http.Response, error)

func (f rtFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// Each try has a deadline of the time left in the budget, so a call cannot
// last much more than the budget. The first try has at least 2 s.
func TestDoerTryDeadline(t *testing.T) {
	now := time.Unix(1000, 0)
	var left []time.Duration
	hc := &http.Client{Transport: rtFunc(func(r *http.Request) (*http.Response, error) {
		dl, ok := r.Context().Deadline()
		if !ok {
			t.Fatal("no deadline")
		}
		left = append(left, time.Until(dl))
		now = now.Add(15 * time.Second) // the try takes 15 s
		return &http.Response{StatusCode: 503, Status: "503", Body: http.NoBody, Header: http.Header{}}, nil
	})}
	d := transport.New(settings, transport.WithBaseURL("http://x"), transport.WithHTTPClient(hc),
		transport.WithSleep(func(x time.Duration) { now = now.Add(x) }), transport.WithClock(func() time.Time { return now }))
	_ = raw(d, "GET")
	// Try 1 has the whole budget; try 2 starts after 15 s and a wait of about 0.5 s.
	if len(left) != 2 || left[0] > 20*time.Second || left[0] < 19*time.Second || left[1] > 5*time.Second || left[1] < 4*time.Second {
		t.Errorf("deadlines %v", left)
	}

	// A clock that says the budget is spent still gives the first try 2 s.
	left = nil
	d = transport.New(settings, transport.WithBaseURL("http://x"), transport.WithHTTPClient(hc),
		transport.WithSleep(func(time.Duration) {}), transport.WithClock(func() time.Time { now = now.Add(time.Minute); return now }))
	_ = raw(d, "GET")
	if len(left) != 1 || left[0] > 2*time.Second || left[0] < time.Second {
		t.Errorf("first try deadline %v", left)
	}
}

// A body that is still being read when the deadline ends is cut: exit 5.
func TestDoerDeadlineCutsBody(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	base := testapi.Fake(t, map[string]http.HandlerFunc{"GET /api/v4/acme/stories/1": func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("Content-Type", "application/json")
		_, _ = rw.Write([]byte(`{"entity":`))
		rw.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}})
	// The clock says the budget is spent: try 1 has the floor of 2 s.
	now := time.Unix(1000, 0)
	d := transport.New(settings, transport.WithBaseURL(base), transport.WithClock(func() time.Time {
		now = now.Add(time.Minute)
		return now
	}))
	start := time.Now()
	e := asExit(t, raw(d, "GET"))
	if took := time.Since(start); e.Code != exit.Unavailable || took < time.Second || took > 5*time.Second {
		t.Errorf("%+v after %v", e, took)
	}
}

func TestDoerNoRetryTLS(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) { n.Add(1) }))
	t.Cleanup(srv.Close)
	w := &waits{}
	d := transport.New(settings, transport.WithBaseURL(srv.URL), transport.WithSleep(w.sleep))
	e := asExit(t, raw(d, "GET"))
	if e.Code != exit.Other || !e.NotSent || len(w.d) != 0 || n.Load() != 0 {
		t.Errorf("code %d, not sent %v, waits %v, hits %d, msg %s", e.Code, e.NotSent, w.d, n.Load(), e.Msg)
	}
}

// A dial failure did not send the request; a connection closed after the
// request was read did.
func TestDoerNotSent(t *testing.T) {
	d := transport.New(settings, transport.WithBaseURL("http://127.0.0.1:1"))
	if e := asExit(t, raw(d, "POST")); e.Code != exit.Unavailable || !e.NotSent {
		t.Errorf("dial: %+v", e)
	}
	d = newDoer(t, &waits{}, "POST", func(rw http.ResponseWriter, r *http.Request) {
		conn, _, err := rw.(http.Hijacker).Hijack()
		if err == nil {
			_ = conn.Close()
		}
	})
	if e := asExit(t, raw(d, "POST")); e.Code != exit.Unavailable || e.NotSent {
		t.Errorf("closed: %+v", e)
	}
}

func TestListCursor(t *testing.T) {
	cases := map[string]string{
		`{"entities":[{"id":1}],"next_page_url":"https://api.app.shortcut.com/api/v4/acme/stories?cursor=abc%3D"}`: "abc=",
		`{"entities":[{"id":1}],"current_page":3}`: "",
	}
	for body, want := range cases {
		d := newDoer(t, &waits{}, "GET", func(rw http.ResponseWriter, r *http.Request) {
			rw.Header().Set("Content-Type", "application/json")
			_, _ = rw.Write([]byte(body))
		})
		items, cur, err := d.List(context.Background(), "/stories/1", nil)
		if err != nil || cur != want || len(items) != 1 {
			t.Errorf("%s: items %d, cursor %q, err %v", body, len(items), cur, err)
		}
	}
}

// A 2xx list response must have entities; an empty list is [] in it.
func TestListNeedsEntities(t *testing.T) {
	for body, ok := range map[string]bool{`{"entities":[]}`: true, `{"data":[]}`: false, `{"entities":null}`: false, `[]`: false} {
		d := newDoer(t, &waits{}, "GET", func(rw http.ResponseWriter, r *http.Request) {
			rw.Header().Set("Content-Type", "application/json")
			_, _ = rw.Write([]byte(body))
		})
		_, _, err := d.List(context.Background(), "/stories/1", nil)
		if ok != (err == nil) {
			t.Errorf("%s: err %v", body, err)
		}
		if !ok {
			if e := asExit(t, err); e.Code != exit.Other || e.Msg != "response is not a list" {
				t.Errorf("%s: %+v", body, e)
			}
		}
	}
}

// A 2xx body that is not of a JSON media type is an error, even if it parses.
func TestDoerNonJSONContentType(t *testing.T) {
	for _, ct := range []string{"text/html", "text/plain; charset=utf-8", ""} {
		d := newDoer(t, &waits{}, "GET", func(rw http.ResponseWriter, r *http.Request) {
			rw.Header()["Content-Type"] = []string{ct}
			_, _ = rw.Write([]byte(`{"entity":{"id":1}}`))
		})
		_, err := d.Raw(context.Background(), "GET", "/stories/1", nil, nil)
		if e := asExit(t, err); e.Code != exit.Other || !strings.Contains(e.Msg, "not JSON") {
			t.Errorf("%q: %+v", ct, e)
		}
		if _, _, err := d.List(context.Background(), "/stories/1", nil); err == nil {
			t.Errorf("%q: List: no error", ct)
		}
	}
	// An empty body (a 204) needs no media type.
	d := newDoer(t, &waits{}, "DELETE", func(rw http.ResponseWriter, r *http.Request) { rw.WriteHeader(204) })
	if _, err := d.Raw(context.Background(), "DELETE", "/stories/1", nil, nil); err != nil {
		t.Errorf("204: %v", err)
	}
}

func TestPageList(t *testing.T) {
	var calls int
	base := testapi.Fake(t, map[string]http.HandlerFunc{"GET /api/v4/acme/stories": func(rw http.ResponseWriter, r *http.Request) {
		calls++
		next := map[string]string{"": "p2", "p2": "p3"}[r.URL.Query().Get("cursor")]
		env := map[string]any{"entities": []int{calls}}
		if next != "" {
			env["next_page_url"] = "http://x/api/v4/acme/stories?cursor=" + next
		}
		rw.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(rw).Encode(env)
	}})
	d := transport.New(settings, transport.WithBaseURL(base))
	got, err := transport.Page(context.Background(), func(c string) ([]json.RawMessage, string, error) {
		q := url.Values{}
		if c != "" {
			q.Set("cursor", c)
		}
		return d.List(context.Background(), "/stories", q)
	}, -1)
	if err != nil || len(got) != 3 || calls != 3 {
		t.Errorf("items %d, calls %d, err %v", len(got), calls, err)
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
		`{"entity":{"id":1}}`: `{"id":1}`,
		`{"$schema":"x","current_page":1,"total_items":9,"next_page_url":"https://api.app.shortcut.com/api/v4/acme/stories?cursor=c","entities":[{"id":1},{"id":2}]}`: `[{"id":1},{"id":2}]`,
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
	got, err := d.Raw(context.Background(), "GET", "/stories/1", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(got) {
		t.Errorf("body not JSON: %s", got)
	}
}

func TestScrub(t *testing.T) {
	if got := transport.Scrub("a sct_x b sct_x", "sct_x"); got != "a *** b ***" {
		t.Errorf("got %q", got)
	}
	if got := transport.Scrub("abc", ""); got != "abc" {
		t.Errorf("empty token: got %q", got)
	}
}

// A wait that leaves the next try less than 2 s does not start: the 429 comes
// back at once.
func TestDoerNoWaitWithoutTimeForATry(t *testing.T) {
	now := time.Unix(1000, 0)
	var n int
	hc := &http.Client{Transport: rtFunc(func(r *http.Request) (*http.Response, error) {
		n++
		now = now.Add(900 * time.Millisecond) // the try takes 0.9 s
		return &http.Response{StatusCode: 429, Status: "429 Too Many Requests", Request: r,
			Header: http.Header{"Retry-After": {"19"}, "Content-Type": {"application/json"}},
			Body:   io.NopCloser(strings.NewReader(`{"message":"slow down"}`))}, nil
	})}
	w := &waits{}
	d := transport.New(settings, transport.WithBaseURL("http://x"), transport.WithHTTPClient(hc),
		transport.WithSleep(func(x time.Duration) { w.sleep(x); now = now.Add(x) }), transport.WithClock(func() time.Time { return now }))
	e := asExit(t, raw(d, "GET"))
	if n != 1 || len(w.d) != 0 || e.Status != 429 || string(e.API) != `{"message":"slow down"}` {
		t.Errorf("tries %d, waits %v, error %+v", n, w.d, e)
	}
}
