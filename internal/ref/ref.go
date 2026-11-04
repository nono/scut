// Package ref parses story references.
package ref

import (
	"net/url"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"github.com/nono/scut/internal/exit"
)

var (
	digits   = regexp.MustCompile(`^\d+$`)
	scID     = regexp.MustCompile(`sc-(\d+)`)
	urlStory = regexp.MustCompile(`(?:^|/)story/(\d+)(?:/|$)`)
)

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
	return Story(m[0])
}
