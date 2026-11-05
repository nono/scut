package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/nono/scut/internal/exit"
	"github.com/nono/scut/internal/gitwt"
	"github.com/nono/scut/internal/output"
)

type worktreeResult struct {
	Story   int64  `json:"story"`
	Branch  string `json:"branch"`
	Dir     string `json:"dir"`
	Created bool   `json:"created"`
	From    string `json:"from"`
	Started *bool  `json:"started,omitempty"`
}

func (r worktreeResult) Human() string { return r.Dir + "\n" + r.Branch }

func newWorktree(env Env) *cobra.Command {
	var repo, dir, branch string
	c := &cobra.Command{
		Use:   "worktree [<story>]",
		Short: "Find or make the git worktree of a story",
		Long: "Find or make the git worktree of a story. The branch is the first that has sc-<id>: " +
			"in a worktree, a local branch, a branch on origin (after a fetch if none is found), " +
			"else a new branch with the name that Shortcut suggests, from origin/HEAD.",
		Args:        cobra.MaximumNArgs(1),
		Annotations: map[string]string{OpsAnnotation: "getStory"},
		RunE: func(cmd *cobra.Command, args []string) error {
			ids, err := storyIDs(env, args)
			if err != nil {
				return err
			}
			id := ids[0]
			ctx := cmd.Context()
			// The API client is made only when a Shortcut call is necessary,
			// so an existing branch needs no token.
			var d *deps
			api := func() (*deps, error) {
				if d == nil {
					var err error
					if d, err = newDeps(cmd, env); err != nil {
						d = nil
						return nil, err
					}
				}
				return d, nil
			}
			r, err := gitwt.Open(ctx, abs(env.Dir, repo))
			if err != nil {
				return err
			}
			if dir == "" {
				dir = r.DefaultDir(id)
			} else {
				dir = abs(env.Dir, dir)
			}
			ms, err := r.Find(ctx, id)
			if err == nil && ms == nil {
				if err = r.Fetch(ctx); err == nil {
					ms, err = r.Find(ctx, id)
				}
			}
			if err != nil {
				return err
			}
			var m gitwt.Match
			switch {
			case ms == nil:
				m = gitwt.Match{Branch: branch, Source: gitwt.New}
				if branch == "" {
					d, err := api()
					if err != nil {
						return err
					}
					if m.Branch, err = branchName(ctx, d, id); err != nil {
						return err
					}
					if !gitwt.HasID(m.Branch, id) {
						return &exit.Error{Code: exit.NotFound,
							Msg:        fmt.Sprintf("the branch name %q has no sc-%d", m.Branch, id),
							Suggestion: "give --branch"}
					}
				} else if !gitwt.HasID(branch, id) {
					return exit.Usagef("--branch %q has no sc-%d", branch, id)
				}
			case branch != "":
				found := false
				for _, x := range ms {
					if x.Branch == branch {
						m, found = x, true
					}
				}
				if !found {
					return &exit.Error{Code: exit.Usage, Msg: fmt.Sprintf("--branch %q is not a branch of sc-%d", branch, id), Valid: branchNames(ms)}
				}
			case len(ms) > 1:
				return &exit.Error{Code: exit.Usage, Msg: fmt.Sprintf("more than one branch for sc-%d", id),
					Suggestion: "give --branch", Valid: branchNames(ms)}
			default:
				m = ms[0]
			}
			res := worktreeResult{Story: id, Branch: m.Branch, From: string(m.Source)}
			if m.Source == gitwt.Worktree {
				if m.Prunable {
					return &exit.Error{Code: exit.Usage,
						Msg:        fmt.Sprintf("the worktree of %s at %s is missing", m.Branch, m.Dir),
						Suggestion: "git -C " + r.Main + " worktree prune"}
				}
				res.Dir = m.Dir
			} else {
				if _, err := os.Lstat(dir); err == nil {
					return exit.Usagef("%s is in use", dir)
				} else if !errors.Is(err, fs.ErrNotExist) {
					return &exit.Error{Code: exit.Other, Msg: err.Error()}
				}
				base := ""
				if m.Source == gitwt.New {
					if base, err = r.Base(ctx); err != nil {
						return err
					}
				}
				if err := r.Add(ctx, dir, m, base); err != nil {
					return err
				}
				res.Dir, res.Created = dir, true
			}
			if d != nil {
				return d.Out(res)
			}
			human, _ := cmd.Flags().GetBool("human")
			human = human || env.Getenv("SCUT_OUTPUT") == "human"
			return output.Print(cmd.OutOrStdout(), res, human)
		},
	}
	f := c.Flags()
	f.StringVar(&repo, "repo", "", "a directory in the git repository (default: the current directory)")
	f.StringVar(&dir, "dir", "", "the directory of a new worktree (default: <parent of the main worktree>/<id>)")
	f.StringVar(&branch, "branch", "", "the branch to use when more than one matches, or the name of a new branch")
	return c
}

// abs returns p made absolute against base. An empty p gives base.
func abs(base, p string) string {
	if p == "" {
		return base
	}
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Join(base, p)
}

func branchNames(ms []gitwt.Match) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.Branch
	}
	return out
}
