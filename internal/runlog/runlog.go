// Package runlog writes one JSON line of facts for each run of scut to a local
// log. See docs/design/2026-11-06-log-design.md. A failure of the log has no
// effect on the run.
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
	"sync"
	"time"

	"github.com/nono/scut/internal/exit"
)

const (
	maxError   = 1024 // bytes of error and api
	maxShort   = 200  // bytes of a flag value and of http[].err
	maxEntries = 100  // entries of ids and http
)

var (
	idPattern   = regexp.MustCompile(`^(?:sc-)?([0-9]+)$`)
	uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	// A secret can hold any character. In the encoded line, a quote, a backslash
	// or a space ends it, so the line stays valid JSON.
	tokenPattern = regexp.MustCompile(`sct_[^"\\\s]+`)
)

// loggedFlags are the flags whose values cannot hold free text. A new flag has
// no logged value until someone adds it here.
var loggedFlags = map[string]bool{
	"limit": true, "comments": true, "fields": true, "format": true, "depth": true,
	"method": true, "state": true, "workflow": true, "team": true, "type": true,
}

// Logged tells if the log keeps the value of flag.
func Logged(flag string) bool { return loggedFlags[flag] }

// Result is what a run gives to Finish. Flags has the raw values.
type Result struct {
	Version, Dir, Cmd string
	Args              []string
	IDArgs            bool // the arguments of this command are IDs: keep them in ids
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

	mu    sync.Mutex
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
	ArgsN    int               `json:"args_n"`
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
	if getenv == nil || getenv("SCUT_LOG") == "off" {
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
func (r *Recorder) API(base http.RoundTripper) http.RoundTripper {
	if r == nil {
		return base
	}
	return &recordTransport{r, base, false}
}

// Files returns base with a record of each try. An entry has the host only,
// because a file URL holds the name of the file.
func (r *Recorder) Files(base http.RoundTripper) http.RoundTripper {
	if r == nil {
		return base
	}
	return &recordTransport{r, base, true}
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
	t.r.mu.Lock()
	defer t.r.mu.Unlock()
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
	if b := r.encode(res); b != nil {
		_ = appendLine(r.path, b) // a failure of the log has no effect on the run
	}
}

func (r *Recorder) encode(res Result) []byte {
	l := line{
		T:        r.start.UTC().Format("2006-01-02T15:04:05.000Z07:00"),
		Version:  res.Version,
		Session:  r.session,
		Dir:      res.Dir,
		Cmd:      res.Cmd,
		ArgsN:    len(res.Args),
		Flags:    flags(res.Flags),
		MS:       r.now().Sub(r.start).Milliseconds(),
		OutBytes: res.OutBytes,
		Exit:     res.Exit,
	}
	if res.IDArgs {
		l.IDs = ids(res.Args)
	}
	if res.Err != nil {
		l.Error = cut(res.Err.Error(), maxError)
		var e *exit.Error
		if errors.As(res.Err, &e) {
			l.Error = cut(e.Msg, maxError)
			l.API = cut(string(e.API), maxError)
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	l.HTTP = r.http
	if r.httpN > len(r.http) {
		l.HTTPN = r.httpN
	}
	// No HTML escapes: the line stays easy to search.
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if enc.Encode(l) != nil {
		return nil
	}
	// On the encoded line, so that a new field is covered.
	return tokenPattern.ReplaceAll(b.Bytes(), []byte("***"))
}

// ids keeps each argument that is an ID. An ID cannot hold text.
func ids(args []string) []any {
	var out []any
	for _, a := range args {
		if len(out) == maxEntries {
			break
		}
		if uuidPattern.MatchString(a) {
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
	if len(raw) == 0 {
		return nil
	}
	out := make(map[string]string, len(raw))
	for k, v := range raw {
		if Logged(k) {
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
