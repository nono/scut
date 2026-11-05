// Package ref parses story and doc references.
package ref

import (
	"encoding/base64"
	"net/url"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"github.com/nono/scut/internal/exit"
)

var (
	digits = regexp.MustCompile(`^\d+$`)
	// sc-<id> at the start of a name or after a character that is not a
	// letter or a digit, so misc-12 is not story 12.
	scID     = regexp.MustCompile(`(?:^|[^A-Za-z0-9])sc-(\d+)`)
	urlStory = regexp.MustCompile(`(?:^|/)story/(\d+)(?:/|$)`)
	uuid     = regexp.MustCompile(`^` + uuidPattern + `$`)
	// docKey is the decoded last part of a doc URL of the web app.
	docKey = regexp.MustCompile(`^"Doc":#uuid "(` + uuidPattern + `)"$`)
)

const uuidPattern = `[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`

// IsUUID reports whether s is a UUID.
func IsUUID(s string) bool { return uuid.MatchString(s) }

// Doc parses a doc argument. It returns the doc ID of a UUID, a doc URL of
// the web app (…/write/<base64>) or an API doc URL (…/documents/<id>).
// Anything else that is not a URL is a title: Doc returns it trimmed, with no
// ID. A blank argument or any other URL is a usage error.
func Doc(s string) (id, title string, err error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", "", exit.Usagef("give a doc URL, ID or title")
	}
	if uuid.MatchString(s) {
		return strings.ToLower(s), "", nil
	}
	if l := strings.ToLower(s); !strings.HasPrefix(l, "http://") && !strings.HasPrefix(l, "https://") {
		return "", s, nil
	}
	bad := exit.Usagef("bad doc URL %q", s)
	u, perr := url.Parse(s)
	if perr != nil {
		return "", "", bad
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 2 {
		return "", "", bad
	}
	last := parts[len(parts)-1]
	switch parts[len(parts)-2] {
	case "documents":
		if uuid.MatchString(last) {
			return strings.ToLower(last), "", nil
		}
	case "write":
		// The web app puts base64 of `"Doc":#uuid "<id>"` in the URL.
		b64 := strings.NewReplacer("-", "+", "_", "/").Replace(strings.TrimRight(last, "="))
		dec, derr := base64.RawStdEncoding.DecodeString(b64)
		if derr != nil {
			return "", "", bad
		}
		if m := docKey.FindSubmatch(dec); m != nil {
			return strings.ToLower(string(m[1])), "", nil
		}
	}
	return "", "", bad
}

// Story returns the story ID in s: sc-5409, 5409, a story URL or a branch name.
func Story(s string) (int64, error) {
	s = strings.TrimSpace(s)
	var m []string
	switch {
	case digits.MatchString(s):
		m = []string{s, s}
	case strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://"):
		if u, err := url.Parse(s); err == nil {
			m = urlStory.FindStringSubmatch(u.Path)
		}
	default:
		m = scID.FindStringSubmatch(s)
	}
	if m == nil {
		return 0, exit.Usagef("bad story reference %q", s)
	}
	id, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil || id <= 0 {
		return 0, exit.Usagef("bad story reference %q", s)
	}
	return id, nil
}

// FromGit returns the story ID in the branch name of the repository in dir.
func FromGit(dir string) (int64, error) {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		return 0, exit.Usagef("give a story ID")
	}
	m := scID.FindStringSubmatch(strings.TrimSpace(string(out)))
	if m == nil {
		return 0, exit.Usagef("give a story ID")
	}
	return Story("sc-" + m[1])
}

// HasID tells if the branch name has sc-<id>, by the rule of Story.
func HasID(branch string, id int64) bool {
	want := strconv.FormatInt(id, 10)
	for _, m := range scID.FindAllStringSubmatch(branch, -1) {
		if m[1] == want {
			return true
		}
	}
	return false
}
