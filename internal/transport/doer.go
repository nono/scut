// Package transport sends requests to the Shortcut API.
// It adds auth, retries safe requests and maps failures to exit codes.
package transport

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/nono/scut/internal/config"
	"github.com/nono/scut/internal/exit"
	v4 "github.com/nono/scut/internal/v4"
)

// DefaultBaseURL is the Shortcut API server.
const DefaultBaseURL = "https://api.app.shortcut.com"

// Version is sent in the User-Agent header.
var Version = "dev"

const (
	maxTries     = 4
	retryBudget  = 20 * time.Second
	firstBackoff = 500 * time.Millisecond
	tryTimeout   = 30 * time.Second // limit of each single try
	maxErrBody   = 1 << 20
)

// Doer sends HTTP requests with auth, retries and error mapping.
// It implements the HttpRequestDoer interface of the generated client.
type Doer struct {
	s     config.Settings
	base  string
	hc    *http.Client
	sleep func(time.Duration) // nil: wait on a timer and on ctx
	now   func() time.Time
}

// Option changes a Doer.
type Option func(*Doer)

// WithBaseURL sets the server URL.
func WithBaseURL(u string) Option { return func(d *Doer) { d.base = strings.TrimRight(u, "/") } }

// WithSleep sets the function that waits between tries.
func WithSleep(f func(time.Duration)) Option { return func(d *Doer) { d.sleep = f } }

// WithHTTPClient sets the HTTP client.
func WithHTTPClient(c *http.Client) Option { return func(d *Doer) { d.hc = c } }

// WithClock sets the clock that measures the retry budget.
func WithClock(f func() time.Time) Option { return func(d *Doer) { d.now = f } }

// New returns a Doer for s.
func New(s config.Settings, opts ...Option) *Doer {
	d := &Doer{s: s, base: DefaultBaseURL, hc: &http.Client{Timeout: tryTimeout}, now: time.Now}
	for _, o := range opts {
		o(d)
	}
	return d
}

// Slug returns the workspace slug, to pass to generated methods.
func (d *Doer) Slug() string { return d.s.Workspace }

// Client returns the generated client that sends through d.
func (d *Doer) Client() *v4.ClientWithResponses {
	c, err := v4.NewClientWithResponses(d.base, v4.WithHTTPClient(d))
	if err != nil { // only a bad server URL
		panic(err)
	}
	return c
}

// Do sends req. A non-2xx response comes back as an *exit.Error.
func (d *Doer) Do(req *http.Request) (*http.Response, error) {
	req.Header.Set("Authorization", "Bearer "+d.s.Token)
	req.Header.Set("User-Agent", "scut/"+Version)
	ctx := req.Context()
	retry := req.Method == http.MethodGet
	// The budget bounds the whole call: no try starts after retryBudget from
	// the first try, and no wait ends after it. The elapsed time is the larger
	// of the clock and the sum of the waits, so an injected sleep still counts.
	start := d.now()
	var total time.Duration
	for try := 1; ; try++ {
		resp, err := d.hc.Do(req)
		var wait time.Duration
		var final error
		switch {
		case err != nil:
			if ctx.Err() != nil {
				return nil, fmt.Errorf("%s %s: %w", req.Method, req.URL.Path, ctx.Err())
			}
			final = d.connectError(req, err)
		case resp.StatusCode/100 == 2:
			return resp, nil
		default:
			wait = retryAfter(resp)
			final = d.statusError(req, resp)
		}
		if !retry || try >= maxTries || !Retryable(final) {
			return nil, final
		}
		if wait == 0 {
			wait = jitter(firstBackoff << (try - 1))
		}
		elapsed := max(d.now().Sub(start), total)
		if elapsed+wait > retryBudget {
			return nil, final
		}
		total += wait
		if err := d.wait(ctx, wait); err != nil {
			return nil, fmt.Errorf("%s %s: %w", req.Method, req.URL.Path, err)
		}
	}
}

func (d *Doer) wait(ctx context.Context, w time.Duration) error {
	if d.sleep != nil {
		d.sleep(w)
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

func jitter(d time.Duration) time.Duration {
	return time.Duration(float64(d) * (0.8 + 0.4*rand.Float64()))
}

// Retryable tells if err is a failure that a retry can heal: 429, 502, 503,
// 504 or a connect failure.
func Retryable(err error) bool {
	var e *exit.Error
	if !errors.As(err, &e) {
		return false
	}
	switch e.Status {
	case 0: // connect failure
		return e.Code == exit.Unavailable
	case http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	}
	return false
}

// retryAfter reads a Retry-After header in seconds. It returns 0 if absent.
func retryAfter(resp *http.Response) time.Duration {
	n, err := strconv.Atoi(strings.TrimSpace(resp.Header.Get("Retry-After")))
	if err != nil || n <= 0 {
		return 0
	}
	return time.Duration(n) * time.Second
}

func (d *Doer) scrub(s string) string { return Scrub(s, d.s.Token) }

// Scrub replaces each copy of token in s with ***.
func Scrub(s, token string) string {
	if token != "" {
		s = strings.ReplaceAll(s, token, "***")
	}
	return s
}

func (d *Doer) connectError(req *http.Request, err error) error {
	if ue, ok := err.(*url.Error); ok {
		err = ue.Err
	}
	code := exit.Unavailable
	if isTLS(err) { // a bad certificate does not heal: no retry
		code = exit.Other
	}
	return &exit.Error{
		Code: code,
		Msg:  d.scrub(fmt.Sprintf("%s %s %s: %v", req.Method, req.URL.Host, req.URL.Path, err)),
	}
}

func isTLS(err error) bool {
	var cv *tls.CertificateVerificationError
	var ua x509.UnknownAuthorityError
	var he x509.HostnameError
	var ci x509.CertificateInvalidError
	return errors.As(err, &cv) || errors.As(err, &ua) || errors.As(err, &he) || errors.As(err, &ci)
}

func (d *Doer) statusError(req *http.Request, resp *http.Response) error {
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrBody))
	e := &exit.Error{
		Code:       CodeFor(resp.StatusCode),
		Status:     resp.StatusCode,
		RetryAfter: retryAfter(resp),
		Msg:        fmt.Sprintf("%s %s: %s", req.Method, req.URL.Path, resp.Status),
	}
	if isJSON(resp.Header.Get("Content-Type")) && json.Valid(body) {
		e.API = json.RawMessage(d.scrub(string(bytes.TrimSpace(body))))
		var m struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(body, &m) == nil && m.Message != "" {
			e.Msg = d.scrub(m.Message)
		}
	}
	return e
}

// CodeFor maps an HTTP status to an exit code.
func CodeFor(status int) exit.Code {
	switch {
	case status == http.StatusUnauthorized, status == http.StatusForbidden:
		return exit.Auth
	case status == http.StatusNotFound:
		return exit.NotFound
	case status == http.StatusBadRequest:
		return exit.Usage
	case status == http.StatusTooManyRequests, status >= 500:
		return exit.Unavailable
	}
	return exit.Other
}

func isJSON(ct string) bool {
	mt, _, err := mime.ParseMediaType(ct)
	return err == nil && (mt == "application/json" || strings.HasSuffix(mt, "+json"))
}

// Raw sends one request and returns the unwrapped "entity" or "entities".
// A path that does not start with /api/ is relative to the workspace.
// Any other JSON body is returned as is.
func (d *Doer) Raw(ctx context.Context, method, path string, query url.Values, body any) (json.RawMessage, error) {
	b, err := d.send(ctx, method, path, query, body)
	if err != nil {
		return nil, err
	}
	return unwrap(b)
}

// List gets one page of a list endpoint. cursor is the cursor parameter of
// next_page_url, or "" on the last page.
func (d *Doer) List(ctx context.Context, path string, query url.Values) (items []json.RawMessage, cursor string, err error) {
	b, err := d.send(ctx, http.MethodGet, path, query, nil)
	if err != nil {
		return nil, "", err
	}
	var env struct {
		Entities    []json.RawMessage `json:"entities"`
		NextPageURL string            `json:"next_page_url"`
	}
	if err := json.Unmarshal(b, &env); err != nil {
		return nil, "", &exit.Error{Code: exit.Other, Msg: "response is not a list"}
	}
	if env.NextPageURL != "" {
		u, err := url.Parse(env.NextPageURL)
		if err != nil {
			return nil, "", &exit.Error{Code: exit.Other, Msg: "bad next_page_url"}
		}
		cursor = u.Query().Get("cursor")
	}
	return env.Entities, cursor, nil
}

func (d *Doer) send(ctx context.Context, method, path string, query url.Values, body any) ([]byte, error) {
	if !strings.HasPrefix(path, "/api/") {
		path = "/api/v4/" + url.PathEscape(d.s.Workspace) + "/" + strings.TrimLeft(path, "/")
	}
	u := d.base + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("encode request body: %w", err)
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return nil, exit.Usagef("bad request: %v", err)
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := d.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("read response: %w", ctx.Err())
		}
		return nil, &exit.Error{Code: exit.Unavailable, Msg: d.scrub("read response: " + err.Error())}
	}
	return b, nil
}

func unwrap(b []byte) (json.RawMessage, error) {
	if len(bytes.TrimSpace(b)) == 0 {
		return nil, nil
	}
	if !json.Valid(b) {
		return nil, &exit.Error{Code: exit.Other, Msg: "response is not JSON"}
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(b, &m) == nil {
		for _, k := range []string{"entity", "entities"} {
			if v, ok := m[k]; ok {
				return v, nil
			}
		}
	}
	return json.RawMessage(b), nil
}
