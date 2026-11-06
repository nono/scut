// Package runlog writes one JSON line of facts for each run of scut to a local
// log. See docs/design/2026-11-06-log-design.md. A write error of the log has
// no effect on the run.
package runlog

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/nono/scut/internal/exit"
	"github.com/nono/scut/internal/ref"
)

const (
	maxError   = 1024 // bytes of error and api
	maxShort   = 200  // bytes of a flag value and of http[].err
	maxEntries = 100  // entries of ids and http
)

var (
	idPattern = regexp.MustCompile(`^(?:sc-)?([0-9]+)$`)
	// A secret can hold any character. In the encoded line, a quote, a backslash
	// or a space ends it, so the line stays valid JSON.
	tokenPattern = regexp.MustCompile(`sct_[^"\\\s]+`)
)

// loggedFlags are the flags for a number or a name. The log keeps any text that
// a user types there. A new flag has no logged value until someone adds it here.
var loggedFlags = map[string]bool{
	"limit": true, "comments": true, "fields": true, "format": true, "depth": true,
	"method": true, "state": true, "workflow": true, "team": true, "type": true,
}

// Result is what a run gives to Finish. Flags has the raw values.
type Result struct {
	Version, Dir, Cmd string
	Args              []string
	IDArgs            bool // the arguments of this command are IDs: keep them in ids
	ArgsCut           bool // a flag parse error stopped the parse: the count of Args is not known
	Flags             map[string]string
	OutBytes          int64
	Exit              int
	Err               error
}

// Recorder collects the facts of one run. A nil Recorder records nothing.
type Recorder struct {
	now     func() time.Time
	start   time.Time
	path    string
	session string

	http  []entry
	httpN int
}

type entry struct {
	Method string `json:"m"`
	Path   string `json:"p,omitempty"`
	Host   string `json:"h,omitempty"`
	Status int    `json:"s"`
	MS     int64  `json:"ms"`
	Err    string `json:"err,omitempty"`
}

type line struct {
	T        string            `json:"t"`
	Version  string            `json:"v"`
	Session  string            `json:"session,omitempty"`
	Dir      string            `json:"cwd"`
	Cmd      string            `json:"cmd"`
	IDs      []any             `json:"ids,omitempty"`
	ArgsN    *int              `json:"args_n,omitempty"`
	Flags    map[string]string `json:"flags,omitempty"`
	MS       int64             `json:"ms"`
	OutBytes int64             `json:"out_bytes"`
	Exit     int               `json:"exit"`
	Error    string            `json:"error,omitempty"`
	API      string            `json:"api,omitempty"`
	HTTP     []entry           `json:"http,omitempty"`
	HTTPN    int               `json:"http_n,omitempty"`
}

// Start begins the record of a run. It returns nil when SCUT_LOG is off or when
// the environment gives no directory for the log.
func Start(getenv func(string) string, now func() time.Time) *Recorder {
	if getenv("SCUT_LOG") == "off" {
		return nil
	}
	// The XDG specification says to ignore a relative value.
	state := getenv("XDG_STATE_HOME")
	if !filepath.IsAbs(state) {
		home := getenv("HOME")
		if !filepath.IsAbs(home) {
			return nil
		}
		state = filepath.Join(home, ".local", "state")
	}
	return &Recorder{
		now:     now,
		start:   now(),
		path:    filepath.Join(state, "scut", "scut.log"),
		session: getenv("CLAUDE_CODE_SESSION_ID"),
	}
}

// API returns base with a record of each try. An entry has the URL path.
func (r *Recorder) API(base http.RoundTripper) http.RoundTripper { return r.wrap(base, false) }

// Files returns base with a record of each try. An entry has the host only,
// because a file URL holds the name of the file.
func (r *Recorder) Files(base http.RoundTripper) http.RoundTripper { return r.wrap(base, true) }

func (r *Recorder) wrap(base http.RoundTripper, hostOnly bool) http.RoundTripper {
	if r == nil {
		return base
	}
	return &recordTransport{r, base, hostOnly}
}

type recordTransport struct {
	r        *Recorder
	base     http.RoundTripper
	hostOnly bool
}

func (t *recordTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	start := t.r.now()
	resp, err := base.RoundTrip(req)
	e := entry{Method: req.Method, MS: t.r.now().Sub(start).Milliseconds()}
	if t.hostOnly {
		e.Host = req.URL.Hostname()
	} else {
		e.Path = req.URL.Path
	}
	if err != nil {
		e.Err = cut(err.Error(), maxShort)
	} else {
		e.Status = resp.StatusCode
	}
	t.r.httpN++
	if len(t.r.http) < maxEntries {
		t.r.http = append(t.r.http, e)
	}
	return resp, err
}

// Finish writes the line of the run.
func (r *Recorder) Finish(res Result) {
	if r == nil {
		return
	}
	_ = appendLine(r.path, r.encode(res)) // a write error of the log has no effect on the run
}

func (r *Recorder) encode(res Result) []byte {
	l := line{
		T:        r.start.UTC().Format("2006-01-02T15:04:05.000Z07:00"),
		Version:  res.Version,
		Session:  r.session,
		Dir:      res.Dir,
		Cmd:      res.Cmd,
		Flags:    flags(res.Flags),
		MS:       r.now().Sub(r.start).Milliseconds(),
		OutBytes: res.OutBytes,
		Exit:     res.Exit,
	}
	if res.IDArgs {
		l.IDs = ids(res.Args)
	}
	if !res.ArgsCut {
		n := len(res.Args)
		l.ArgsN = &n
	}
	if res.Err != nil {
		l.Error = cut(res.Err.Error(), maxError)
		var e *exit.Error
		if errors.As(res.Err, &e) {
			l.Error = cut(e.Msg, maxError)
			l.API = cut(string(e.API), maxError)
		}
	}
	l.HTTP = r.http
	if r.httpN > len(r.http) {
		l.HTTPN = r.httpN
	}
	// No HTML escapes: the line stays easy to search.
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(l) // the fields are strings, numbers and lists of them
	// On the encoded line, so that a new field is covered.
	return tokenPattern.ReplaceAll(b.Bytes(), []byte("***"))
}

// ids keeps each argument in the form of an ID. The caller sets IDArgs only for
// a command that takes IDs, because a query in that form is not an ID.
func ids(args []string) []any {
	var out []any
	for _, a := range args {
		if len(out) == maxEntries {
			break
		}
		if ref.IsUUID(a) {
			out = append(out, a)
		} else if m := idPattern.FindStringSubmatch(a); m != nil {
			if n, err := strconv.ParseInt(m[1], 10, 64); err == nil {
				out = append(out, n)
			}
		}
	}
	return out
}

func flags(raw map[string]string) map[string]string {
	out := make(map[string]string, len(raw))
	for k, v := range raw {
		if loggedFlags[k] {
			out[k] = cut(v, maxShort)
		} else {
			out[k] = ""
		}
	}
	return out
}

// cut returns s as valid UTF-8, in at most n bytes.
func cut(s string, n int) string {
	return strings.ToValidUTF8(s[:min(len(s), n)], "")
}
