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
	uuid     = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	// docKey is the decoded last part of a doc URL of the web app.
	docKey = regexp.MustCompile(`^"Doc":#uuid "([0-9a-fA-F-]{36})"$`)
)

// Doc returns the doc ID in s: a UUID, a doc URL of the web app or an API doc
// URL. ok is false when s is not a URL and not a UUID: s is then a title.
// A URL with no doc ID is a usage error.
func Doc(s string) (id string, ok bool, err error) {
	s = strings.TrimSpace(s)
	if uuid.MatchString(s) {
		return strings.ToLower(s), true, nil
	}
	if !strings.HasPrefix(s, "http://") && !strings.HasPrefix(s, "https://") {
		return "", false, nil
	}
	bad := exit.Usagef("bad doc URL %q", s)
	u, perr := url.Parse(s)
	if perr != nil {
		return "", false, bad
	}
	path := strings.TrimRight(u.Path, "/")
	last := path[strings.LastIndex(path, "/")+1:]
	if uuid.MatchString(last) {
		return strings.ToLower(last), true, nil
	}
	// The web app puts base64 of `"Doc":#uuid "<id>"` in the URL.
	b64 := strings.NewReplacer("-", "+", "_", "/").Replace(strings.TrimRight(last, "="))
	dec, derr := base64.RawStdEncoding.DecodeString(b64)
	if derr != nil {
		return "", false, bad
	}
	m := docKey.FindSubmatch(dec)
	if m == nil || !uuid.Match(m[1]) {
		return "", false, bad
	}
	return strings.ToLower(string(m[1])), true, nil
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
