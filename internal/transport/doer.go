// Package transport sends requests to the Shortcut API.
// It adds auth, retries safe requests and maps failures to exit codes.
package transport

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/nono/scut/internal/config"
	"github.com/nono/scut/internal/exit"
)

// DefaultBaseURL is the Shortcut API server.
const DefaultBaseURL = "https://api.app.shortcut.com"

// Version is sent in the User-Agent header.
var Version = "dev"

const (
	maxTries     = 4                // tries of a safe request
	retryBudget  = 20 * time.Second // a call ends this long after it starts
	firstBackoff = 500 * time.Millisecond
	tryTimeout   = 30 * time.Second // limit of each single try
	firstTryMin  = 2 * time.Second  // the first try has at least this, so a normal call is not cut
	maxErrBody   = 1 << 20
)

// Doer sends HTTP requests with auth, retries and error mapping.
type Doer struct {
	s     config.Settings
	base  string
	hc    *http.Client
	clock Clock
}

// Option changes a Doer.
type Option func(*Doer)

// WithBaseURL sets the server URL.
func WithBaseURL(u string) Option { return func(d *Doer) { d.base = strings.TrimRight(u, "/") } }

// WithSleep sets the function that waits between tries.
func WithSleep(f func(time.Duration)) Option { return func(d *Doer) { d.clock.Sleep = f } }

// WithHTTPClient sets the HTTP client.
func WithHTTPClient(c *http.Client) Option { return func(d *Doer) { d.hc = c } }

// WithRoundTripper sets the transport of the HTTP client. The client keeps its
// timeout. A nil rt is the default transport.
func WithRoundTripper(rt http.RoundTripper) Option { return func(d *Doer) { d.hc.Transport = rt } }

// WithClock sets the clock that measures the retry budget.
func WithClock(f func() time.Time) Option { return func(d *Doer) { d.clock.Now = f } }

// New returns a Doer for s.
func New(s config.Settings, opts ...Option) *Doer {
	d := &Doer{s: s, base: DefaultBaseURL, hc: &http.Client{Timeout: tryTimeout}}
	for _, o := range opts {
		o(d)
	}
	return d
}

// Slug returns the workspace slug.
func (d *Doer) Slug() string { return d.s.Workspace }

// TokenID returns the first 16 hex digits of the SHA-256 of the token. It
// tells tokens apart, for a cache key, and does not show the token.
func (d *Doer) TokenID() string {
	sum := sha256.Sum256([]byte(d.s.Token))
	return hex.EncodeToString(sum[:])[:16]
}

// Do sends req. A non-2xx response comes back as an *exit.Error.
func (d *Doer) Do(req *http.Request) (*http.Response, error) {
	req.Header.Set("Authorization", "Bearer "+d.s.Token)
	req.Header.Set("User-Agent", "scut/"+Version)
	ctx := req.Context()
	// Each try, with the read of its body, has a deadline of the time left in
	// the budget (at most tryTimeout; the first try has at least firstTryMin).
	// So a call ends retryBudget after it starts.
	var resp *http.Response
	try := func(n int, left time.Duration) error {
		limit := min(tryTimeout, left)
		if n == 1 {
			limit = max(limit, firstTryMin)
		}
		tctx, cancel := context.WithTimeout(ctx, limit)
		r, err := d.hc.Do(req.WithContext(tctx)) //nolint:bodyclose // the caller closes a 2xx body; statusError closes the others
		switch {
		case err != nil:
			cancel()
			if ctx.Err() != nil {
				return fmt.Errorf("%s %s: %w", req.Method, req.URL.Path, ctx.Err())
			}
			return d.connectError(req, err)
		case r.StatusCode/100 == 2:
			r.Body = &cancelBody{r.Body, cancel} // the deadline also bounds the read
			resp = r
			return nil
		}
		defer cancel()
		return d.statusError(req, r)
	}
	var err error
	if req.Method == http.MethodGet {
		err = Retry(ctx, d.clock, req.Method+" "+req.URL.Path, try)
	} else {
		err = try(1, retryBudget)
	}
	if err != nil {
		return nil, err
	}
	return resp, nil
}

// cancelBody ends the context of a try when the body is closed.
type cancelBody struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (b *cancelBody) Close() error {
	err := b.ReadCloser.Close()
	b.cancel()
	return err
}

// Clock measures the retry budget and waits between tries.
type Clock struct {
	Now   func() time.Time    // nil: time.Now
	Sleep func(time.Duration) // nil: wait on a timer and on ctx
}

// Retry calls try with the try number (from 1) and the time left in the
// budget, until try returns nil, a failure that is not Retryable, or the
// failure of try number maxTries. Between tries it waits Backoff, with the
// Retry-After of the failure. It does not wait when the next try would then
// start less than firstTryMin before retryBudget from the start: then it
// returns the last failure. The elapsed time is the larger of the
// clock and the sum of the waits, so an injected sleep still counts. If the
// wait fails, it returns that error after name.
func Retry(ctx context.Context, c Clock, name string, try func(n int, left time.Duration) error) error {
	now := c.Now
	if now == nil {
		now = time.Now
	}
	start := now()
	var total time.Duration
	elapsed := func() time.Duration { return max(now().Sub(start), total) }
	for n := 1; ; n++ {
		err := try(n, retryBudget-elapsed())
		if err == nil || n >= maxTries || !Retryable(err) {
			return err
		}
		var after time.Duration
		var e *exit.Error
		if errors.As(err, &e) {
			after = e.RetryAfter
		}
		wait := Backoff(n, after)
		if retryBudget-(elapsed()+wait) < firstTryMin { // the next try would have too little time
			return err
		}
		total += wait
		if serr := Sleep(ctx, c.Sleep, wait); serr != nil {
			return fmt.Errorf("%s: %w", name, serr)
		}
	}
}

// Sleep waits for w, or until ctx ends. With sleep set, it calls that instead
// of a timer, then returns ctx.Err().
func Sleep(ctx context.Context, sleep func(time.Duration), w time.Duration) error {
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

// Backoff returns the wait after failed try number try (from 1): after, when
// the server gave a Retry-After, else 0.5 s doubled for each try, ±20 %.
func Backoff(try int, after time.Duration) time.Duration {
	if after > 0 {
		return after
	}
	return time.Duration(float64(firstBackoff<<(try-1)) * (0.8 + 0.4*rand.Float64()))
}

// Retryable tells if err is a failure that a retry can heal: 429, 502, 503,
// 504, a connect failure or a body cut short (status 0 with exit code 5).
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

// RetryAfter reads a Retry-After header in seconds. It returns 0 if absent.
func RetryAfter(resp *http.Response) time.Duration {
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
	if ue := (*url.Error)(nil); errors.As(err, &ue) {
		err = ue.Err
	}
	code := exit.Unavailable
	tlsErr := IsTLS(err)
	if tlsErr { // a bad certificate does not heal: no retry
		code = exit.Other
	}
	var op *net.OpError
	return &exit.Error{
		Code: code,
		Msg:  d.scrub(fmt.Sprintf("%s %s %s: %v", req.Method, req.URL.Host, req.URL.Path, err)),
		// A bad certificate stops the handshake, before the request is sent.
		NotSent: tlsErr || errors.As(err, &op) && op.Op == "dial",
	}
}

// IsTLS tells if err is a failure to verify the certificate of the server.
func IsTLS(err error) bool {
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
		RetryAfter: RetryAfter(resp),
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
		Entities    *[]json.RawMessage `json:"entities"`
		NextPageURL string             `json:"next_page_url"`
	}
	if err := json.Unmarshal(b, &env); err != nil || env.Entities == nil {
		return nil, "", &exit.Error{Code: exit.Other, Msg: "response is not a list"}
	}
	if env.NextPageURL != "" {
		u, err := url.Parse(env.NextPageURL)
		if err != nil {
			return nil, "", &exit.Error{Code: exit.Other, Msg: "bad next_page_url"}
		}
		cursor = u.Query().Get("cursor")
	}
	return *env.Entities, cursor, nil
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
	if ct := resp.Header.Get("Content-Type"); len(bytes.TrimSpace(b)) > 0 && !isJSON(ct) {
		return nil, &exit.Error{Code: exit.Other, Status: resp.StatusCode,
			Msg: fmt.Sprintf("%s %s: response is not JSON (Content-Type %q)", method, path, ct)}
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
