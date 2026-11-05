// Package gitwt finds and adds the git worktree of a story. It runs the git
// binary and knows nothing about Shortcut.
package gitwt

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/nono/scut/internal/exit"
)

// Source tells where the branch of a story was found.
type Source string

// The sources. Find looks at Worktree, Local and Remote, in this order; New
// is a branch that the caller makes when Find returns none.
const (
	Worktree Source = "worktree"
	Local    Source = "local"
	Remote   Source = "remote"
	New      Source = "new"
)

// Match is a branch of a story.
type Match struct {
	Branch   string // short name, without refs/heads/ or origin/
	Dir      string // only for Worktree
	Source   Source
	Prunable bool // only for Worktree: git knows the worktree, but its directory is missing
}

// Repo is a git repository.
type Repo struct {
	Main string // absolute path of the main worktree
}

// storyRef is sc-<id> at the start of a name or after a character that is
// not a letter or a digit, so misc-12 is not story 12.
var storyRef = regexp.MustCompile(`(?:^|[^A-Za-z0-9])sc-(\d+)`)

// HasID tells if branch has sc-<id> with no digit after it and no letter or
// digit before it.
func HasID(branch string, id int64) bool {
	want := strconv.FormatInt(id, 10)
	for _, m := range storyRef.FindAllStringSubmatch(branch, -1) {
		if m[1] == want {
			return true
		}
	}
	return false
}

func run(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	// The C locale: Open reads the text of a git error, and git translates it.
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", &exit.Error{Code: exit.Other, Msg: fmt.Sprintf("git %s: %s", strings.Join(args, " "), msg)}
	}
	return out.String(), nil
}

// Open returns the repository of dir. dir can be in any worktree of it.
func Open(ctx context.Context, dir string) (Repo, error) {
	if _, err := run(ctx, dir, "rev-parse", "--git-dir"); err != nil {
		var e *exit.Error
		if errors.As(err, &e) && strings.Contains(e.Msg, "not a git repository") {
			return Repo{}, exit.Usagef("%s is not in a git repository", dir)
		}
		return Repo{}, err
	}
	es, err := list(ctx, dir)
	if err != nil {
		return Repo{}, err
	}
	if len(es) == 0 || es[0].dir == "" {
		return Repo{}, &exit.Error{Code: exit.Other, Msg: "git worktree list: no main worktree"}
	}
	return Repo{Main: es[0].dir}, nil
}

// entry is one worktree of git worktree list.
type entry struct {
	dir, branch        string
	detached, prunable bool
}

// list parses git worktree list --porcelain -z: each field ends with NUL,
// and each entry with one more NUL. A path can hold any character but NUL.
func list(ctx context.Context, dir string) ([]entry, error) {
	out, err := run(ctx, dir, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return nil, err
	}
	var es []entry
	var e entry
	for _, f := range strings.Split(out, "\x00") {
		if f == "" {
			if e.dir != "" {
				es = append(es, e)
			}
			e = entry{}
			continue
		}
		k, v, _ := strings.Cut(f, " ")
		switch k {
		case "worktree":
			e.dir = v
		case "branch":
			e.branch = strings.TrimPrefix(v, "refs/heads/")
		case "detached":
			e.detached = true
		case "prunable":
			e.prunable = true
		}
	}
	return es, nil
}

// detachedBranch returns the branch of a worktree that a rebase or a bisect
// detached, or "".
func detachedBranch(ctx context.Context, dir string) string {
	gd, err := run(ctx, dir, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return ""
	}
	gd = strings.TrimSpace(gd)
	for _, f := range []string{"rebase-merge/head-name", "rebase-apply/head-name", "BISECT_START"} {
		if b, err := os.ReadFile(filepath.Join(gd, f)); err == nil {
			return strings.TrimPrefix(strings.TrimSpace(string(b)), "refs/heads/")
		}
	}
	return ""
}

// DefaultDir is the directory for the worktree of story id: a sibling of the
// main worktree, named with the ID.
func (r Repo) DefaultDir(id int64) string {
	return filepath.Join(filepath.Dir(r.Main), strconv.FormatInt(id, 10))
}

// Find returns the branches of story id from the first source that has one,
// or nil.
func (r Repo) Find(ctx context.Context, id int64) ([]Match, error) {
	wts, err := r.worktrees(ctx, id)
	if err != nil || wts != nil {
		return wts, err
	}
	for _, s := range []struct {
		src    Source
		prefix string
	}{{Local, "refs/heads"}, {Remote, "refs/remotes/origin"}} {
		out, err := run(ctx, r.Main, "for-each-ref", "--format=%(refname)", s.prefix)
		if err != nil {
			return nil, err
		}
		// for-each-ref sorts by ref name. A ref name can hold a Unicode space,
		// but not a newline.
		var ms []Match
		for _, ref := range strings.Split(strings.TrimSuffix(out, "\n"), "\n") {
			name := strings.TrimPrefix(ref, s.prefix+"/")
			if name != "HEAD" && HasID(name, id) {
				ms = append(ms, Match{Branch: name, Source: s.src})
			}
		}
		if ms != nil {
			return ms, nil
		}
	}
	return nil, nil
}

func (r Repo) worktrees(ctx context.Context, id int64) ([]Match, error) {
	es, err := list(ctx, r.Main)
	if err != nil {
		return nil, err
	}
	var ms []Match
	for _, e := range es {
		if e.detached && !e.prunable {
			e.branch = detachedBranch(ctx, e.dir)
		}
		if e.branch != "" && HasID(e.branch, id) {
			ms = append(ms, Match{Branch: e.branch, Dir: e.dir, Source: Worktree, Prunable: e.prunable})
		}
	}
	slices.SortFunc(ms, func(a, b Match) int { return strings.Compare(a.Branch, b.Branch) })
	return ms, nil
}

// Fetch runs git fetch origin.
func (r Repo) Fetch(ctx context.Context) error {
	_, err := run(ctx, r.Main, "fetch", "--quiet", "origin")
	return err
}

// Add adds a worktree in dir for m. A New branch starts at origin/HEAD, else
// at origin/main.
func (r Repo) Add(ctx context.Context, dir string, m Match) error {
	args := []string{"worktree", "add", "--quiet"}
	switch m.Source {
	case Local:
		args = append(args, dir, m.Branch)
	case Remote:
		args = append(args, "--track", "-b", m.Branch, dir, "origin/"+m.Branch)
	case New:
		base := "origin/main"
		if _, err := run(ctx, r.Main, "rev-parse", "--verify", "--quiet", "origin/HEAD"); err == nil {
			base = "origin/HEAD"
		} else if ctx.Err() != nil {
			return ctx.Err()
		}
		// --no-track: with an upstream of main, git pull merges main, and
		// git push fails or pushes to main, by push.default.
		args = append(args, "--no-track", "-b", m.Branch, dir, base)
	default:
		return &exit.Error{Code: exit.Other, Msg: fmt.Sprintf("cannot add a worktree from source %q", m.Source)}
	}
	_, err := run(ctx, r.Main, args...)
	return err
}
