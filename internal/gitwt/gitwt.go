// Package gitwt finds and adds the git worktree of a story. It runs the git
// binary and knows nothing about Shortcut.
package gitwt

import (
	"bytes"
	"context"
	"fmt"
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

// The sources, in the order Find looks at them.
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

// HasID tells if branch has sc-<id> with no digit after it.
func HasID(branch string, id int64) bool {
	return regexp.MustCompile(`sc-` + strconv.FormatInt(id, 10) + `(?:\D|$)`).MatchString(branch)
}

func run(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
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
		return Repo{}, exit.Usagef("%s is not in a git repository", dir)
	}
	out, err := run(ctx, dir, "worktree", "list", "--porcelain")
	if err != nil {
		return Repo{}, err
	}
	first, _, _ := strings.Cut(out, "\n")
	main, ok := strings.CutPrefix(first, "worktree ")
	if !ok {
		return Repo{}, &exit.Error{Code: exit.Other, Msg: "git worktree list: no main worktree"}
	}
	return Repo{Main: main}, nil
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
		var names []string
		for _, ref := range strings.Fields(out) {
			name := strings.TrimPrefix(ref, s.prefix+"/")
			if name != "HEAD" && HasID(name, id) {
				names = append(names, name)
			}
		}
		slices.Sort(names)
		var ms []Match
		for _, n := range slices.Compact(names) {
			ms = append(ms, Match{Branch: n, Source: s.src})
		}
		if ms != nil {
			return ms, nil
		}
	}
	return nil, nil
}

func (r Repo) worktrees(ctx context.Context, id int64) ([]Match, error) {
	out, err := run(ctx, r.Main, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	var ms []Match
	for _, block := range strings.Split(out, "\n\n") {
		var m Match
		for _, line := range strings.Split(block, "\n") {
			k, v, _ := strings.Cut(line, " ")
			switch k {
			case "worktree":
				m.Dir = v
			case "branch":
				m.Branch = strings.TrimPrefix(v, "refs/heads/")
			case "prunable":
				m.Prunable = true
			}
		}
		if m.Branch != "" && HasID(m.Branch, id) {
			m.Source = Worktree
			ms = append(ms, m)
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

// Base is the start point of a new branch: origin/HEAD, else origin/main.
func (r Repo) Base(ctx context.Context) (string, error) {
	if _, err := run(ctx, r.Main, "rev-parse", "--verify", "--quiet", "origin/HEAD"); err == nil {
		return "origin/HEAD", nil
	}
	return "origin/main", nil
}

// Add adds a worktree in dir for m. base is the start point of a New branch.
func (r Repo) Add(ctx context.Context, dir string, m Match, base string) error {
	args := []string{"worktree", "add", "--quiet"}
	switch m.Source {
	case Local:
		args = append(args, dir, m.Branch)
	case Remote:
		args = append(args, "--track", "-b", m.Branch, dir, "origin/"+m.Branch)
	case New:
		// --no-track: with an upstream of main, git pull and git push use main.
		args = append(args, "--no-track", "-b", m.Branch, dir, base)
	default:
		return &exit.Error{Code: exit.Other, Msg: fmt.Sprintf("cannot add a worktree from source %q", m.Source)}
	}
	_, err := run(ctx, r.Main, args...)
	return err
}
