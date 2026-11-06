package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"slices"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/nono/scut/internal/exit"
	"github.com/nono/scut/internal/gitwt"
	"github.com/nono/scut/internal/output"
	"github.com/nono/scut/internal/ref"
	"github.com/nono/scut/internal/transport"
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
	var start bool
	c := &cobra.Command{
		Use:   "worktree [<story>]",
		Short: "Find or make the git worktree of a story",
		Long: "Find or make the git worktree of a story. The branch is the first that has sc-<id>: " +
			"in a worktree, a local branch, a branch on origin (after a fetch if none is found), " +
			"else a new branch with the name that Shortcut suggests, from origin/HEAD (else origin/main).",
		Args:        cobra.MaximumNArgs(1),
		Annotations: map[string]string{OpsAnnotation: "getStory,listWorkflowStatesWithinAWorkflow,getWhoami,updateStory", IDArgsAnnotation: "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			ids, err := storyIDs(env, args)
			if err != nil {
				return err
			}
			id, ctx := ids[0], cmd.Context()
			// The API client is made only when a Shortcut call is necessary,
			// so an existing branch needs no token.
			var cached *deps
			api := func() (*deps, error) {
				var err error
				if cached == nil {
					cached, err = newDeps(cmd, env)
				}
				return cached, err
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
			if ms == nil {
				if m, ms, err = newBranch(ctx, r, id, branch, api); err != nil {
					return err
				}
			}
			if ms != nil {
				if m, err = pick(ms, branch, id); err != nil {
					return err
				}
			}
			res := worktreeResult{Story: id, Branch: m.Branch, From: string(m.Source)}
			switch {
			case m.Source != gitwt.Worktree:
				if err := r.Add(ctx, dir, m); err != nil {
					return err
				}
				res.Dir, res.Created = dir, true
			case m.Prunable:
				return &exit.Error{Code: exit.Usage,
					Msg:        fmt.Sprintf("the worktree of %s at %s is missing", m.Branch, m.Dir),
					Suggestion: "git -C " + r.Main + " worktree prune"}
			default:
				res.Dir = m.Dir
			}
			if start {
				d, err := api()
				started := false
				if err == nil {
					started, err = startStory(ctx, d, id)
				}
				if err != nil {
					return withWorktree(ctx, err, res)
				}
				res.Started = &started
			}
			// Not d.Out: the output setting of a config file is read only when
			// the API is called, so the format would depend on the path.
			human, _ := cmd.Flags().GetBool("human")
			human = human || env.Getenv("SCUT_OUTPUT") == "human"
			return output.Print(cmd.OutOrStdout(), res, human)
		},
	}
	f := c.Flags()
	f.StringVar(&repo, "repo", "", "a directory in the git repository (default: the current directory)")
	f.StringVar(&dir, "dir", "", "the directory of a new worktree (default: <parent of the main worktree>/<id>)")
	f.BoolVar(&start, "start", false, "also move a backlog or unstarted story to the first started state of its workflow, and add @me as owner")
	f.StringVar(&branch, "branch", "", "the branch to use when more than one matches, or the name of a new branch")
	return c
}

// newBranch gives the new branch of story id when no branch matches: the
// --branch name, else the name that Shortcut suggests. After the story read,
// it looks for the branches of the story again and returns them: another run
// can make one during the read, and that one is used, not a second one.
func newBranch(ctx context.Context, r gitwt.Repo, id int64, branch string, api func() (*deps, error)) (gitwt.Match, []gitwt.Match, error) {
	if branch != "" {
		if !ref.HasID(branch, id) {
			return gitwt.Match{}, nil, exit.Usagef("--branch %q has no sc-%d", branch, id)
		}
		return gitwt.Match{Branch: branch, Source: gitwt.New}, nil, nil
	}
	d, err := api()
	if err != nil {
		return gitwt.Match{}, nil, err
	}
	name, err := branchName(ctx, d, id)
	if err != nil {
		return gitwt.Match{}, nil, err
	}
	if !ref.HasID(name, id) {
		return gitwt.Match{}, nil, &exit.Error{Code: exit.NotFound,
			Msg: fmt.Sprintf("the branch name %q has no sc-%d", name, id), Suggestion: "give --branch"}
	}
	ms, err := r.Find(ctx, id)
	return gitwt.Match{Branch: name, Source: gitwt.New}, ms, err
}

// pick selects one of the branches of story id: the --branch one, else the
// only one.
func pick(ms []gitwt.Match, branch string, id int64) (gitwt.Match, error) {
	if branch != "" {
		i := slices.IndexFunc(ms, func(x gitwt.Match) bool { return x.Branch == branch })
		if i < 0 {
			return gitwt.Match{}, &exit.Error{Code: exit.Usage,
				Msg: fmt.Sprintf("--branch %q is not a branch of sc-%d", branch, id), Valid: branchNames(ms)}
		}
		return ms[i], nil
	}
	if len(ms) > 1 {
		return gitwt.Match{}, &exit.Error{Code: exit.Usage, Msg: fmt.Sprintf("more than one branch for sc-%d", id),
			Suggestion: "give --branch", Valid: branchNames(ms)}
	}
	return ms[0], nil
}

// withWorktree adds the worktree to the error of the --start part, so a
// caller can still open it. After a cancel, the code is 130.
func withWorktree(ctx context.Context, err error, res worktreeResult) error {
	var e *exit.Error
	if !errors.As(err, &e) {
		e = &exit.Error{Code: exit.Other, Msg: err.Error()}
	}
	c := *e
	c.Worktree = res
	if ctx.Err() != nil {
		c.Code = exit.Cancelled
	}
	return &c
}

// startStory moves a backlog or unstarted story to the started state with the
// lowest position in its workflow, and adds @me as owner. It makes one PATCH,
// or none when there is nothing to change. started tells if the state changed.
func startStory(ctx context.Context, d *deps, id int64) (started bool, err error) {
	path := "stories/" + strconv.FormatInt(id, 10)
	raw, err := d.Doer.Raw(ctx, "GET", path, url.Values{"fields": {"workflow,workflow_state,owners"}}, nil)
	if err != nil {
		return false, err
	}
	type member struct {
		ID string `json:"id"`
	}
	var s struct {
		Workflow struct {
			ID int64 `json:"id"`
		} `json:"workflow"`
		State struct {
			ID int64 `json:"id"`
		} `json:"workflow_state"`
		Owners struct {
			Entities []member `json:"entities"`
		} `json:"owners"`
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		return false, &exit.Error{Code: exit.Other, Msg: "bad story response"}
	}
	items, err := transport.PageQuery(ctx, d.Doer, "workflows/"+strconv.FormatInt(s.Workflow.ID, 10)+"/states",
		url.Values{"fields": {"id,type,position"}, "limit": {"100"}}, -1)
	if err != nil {
		return false, err
	}
	type state struct {
		ID       int64  `json:"id"`
		Type     string `json:"type"`
		Position int64  `json:"position"`
	}
	states, err := decodeAll[state](items, "workflow state")
	if err != nil {
		return false, err
	}
	var cur string
	var first *state
	for i, st := range states {
		if st.ID == s.State.ID {
			cur = st.Type
		}
		if st.Type == "started" && (first == nil || st.Position < first.Position) {
			first = &states[i]
		}
	}
	var target int64 // the state to move to; 0 for no move
	if cur == "backlog" || cur == "unstarted" {
		if first == nil {
			return false, exit.NotFoundf("no started state in workflow %d", s.Workflow.ID)
		}
		target = first.ID
	}
	me, err := d.Resolver.Me()
	if err != nil {
		return false, err
	}
	// A nested list can be cut short: then me can be an owner that is not in
	// it, and owner_ids_add of an owner changes nothing.
	addMe := !slices.Contains(s.Owners.Entities, member{me})
	if target != 0 {
		// Read the state again just before the PATCH, so a story that another
		// writer moved meanwhile is not moved back. The API has no
		// conditional update, so this makes the window small, not zero.
		raw, err := d.Doer.Raw(ctx, "GET", path, url.Values{"fields": {"workflow_state"}}, nil)
		if err != nil {
			return false, err
		}
		var now struct {
			State struct {
				ID int64 `json:"id"`
			} `json:"workflow_state"`
		}
		if err := json.Unmarshal(raw, &now); err != nil {
			return false, &exit.Error{Code: exit.Other, Msg: "bad story response"}
		}
		if now.State.ID != s.State.ID {
			target = 0
		}
	}
	body := map[string]any{}
	if target != 0 {
		body["workflow_state_id"] = target
	}
	if addMe {
		body["owner_ids_add"] = []string{me}
	}
	if len(body) == 0 {
		return false, nil
	}
	if _, err := d.Doer.Raw(ctx, "PATCH", path, nil, body); err != nil {
		return false, err
	}
	return target != 0, nil
}

// abs returns p made absolute against base. An empty p gives base.
func abs(base, p string) string {
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
