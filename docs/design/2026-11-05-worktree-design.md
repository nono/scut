# `scut worktree` design

Requirements: N1 on the requirements page (private). It extends the v1 design in `2026-11-04-scut-design.md`.

## Intent

To start work on a story, the user copies the branch name from Shortcut and gives it to a zsh function. The function then runs about 15 lines of git to add a worktree. `scut worktree <story>` does the git part from the story number alone. The shell function becomes short, and a herdr mod can call `scut` with no shell.

## Goals

- `scut worktree <story>` finds or makes the worktree of a story and prints it as JSON.
- `--start` also marks the story as started in Shortcut.
- No configuration file: the repository and the directory come from the current directory.

## Non-goals

- The herdr part (select or make a space, type `/story N`). It stays in the shell function or the herdr mod.
- Story metadata in the output (type, priority) for herdr icons. A herdr mod gets it with `scut story view <id> --fields story_type,custom_fields`. Fields can be added to the `worktree` output later without a break.
- N2 shell completion.
- A change to the user's zshrc. This document shows the new function; the user installs it.

## Anti-goals

- A Shortcut call when the worktree or the branch already exists and `--start` is not given. The command must work during a Shortcut outage for work that already exists.
- A second branch for a story that already has one, on this machine or on `origin`.
- A change to a directory that `scut` did not make.

## Command

```
scut worktree [<story>] [--repo <path>] [--dir <path>] [--branch <name>] [--start]
```

`<story>` follows the rule of the other commands: `1234`, `sc-1234`, a story URL or a branch name, else the current branch.

| Flag | Default |
|------|---------|
| `--repo` | The repository of the current directory. Any worktree or subdirectory of it gives the same repository. |
| `--dir` | A sibling of the main worktree, named with the story number: main at `~/ts/main` gives `~/ts/1234`. |
| `--branch` | None. Selects one branch when more than one matches. |
| `--start` | Off. |

## Behavior

### Repository

`git -C <repo> rev-parse --path-format=absolute --git-common-dir` gives the common git directory. The main worktree is the first entry of `git worktree list --porcelain`. Outside a git repository, exit 2 (usage).

### Branch match

A branch matches when its name contains `sc-<id>` with no digit after it: `sc-12` does not match `sc-123`. The match is on the ID, not on the full name, because a story renamed after the branch was made gives a new `formatted_vcs_branch_name`.

The command takes the first source that has a match:

| `from` | Source | Action |
|--------|--------|--------|
| `worktree` | A worktree whose branch matches (`git worktree list --porcelain`). | Return its directory, even if it is not the default `--dir`. `created: false`. |
| `local` | `refs/heads/*`. | `git worktree add <dir> <branch>`. |
| `remote` | `refs/remotes/origin/*`. | `git worktree add --track -b <branch> <dir> origin/<branch>`. |
| `new` | None of the above. | See Fetch. |

A matching worktree that git marks `prunable` (its directory was deleted without `git worktree remove`) is exit 2, with the suggestion `git -C <repo> worktree prune`. `scut` does not prune: that changes git metadata that `scut` did not make.

A local branch and `origin/<same name>` are one branch. Two or more different names in the first source with a match: exit 2, with the names in `valid`. `--branch` selects one of them; a `--branch` that is not in that list is exit 2.

When no source matches, `--branch` gives the name of the new branch, and step 3 of Fetch (the story read) is skipped. Then a new worktree can be made during a Shortcut outage. A `--branch` with no `sc-<id>` in it is exit 2, because the next run would not find it.

### Fetch

`scut worktree` fetches only when no source matches:

1. `git fetch origin`.
2. Check `refs/remotes/origin/*` again. A match there gives `from: remote`. This finds a branch that another person pushed after the last fetch.
3. Else read the story (`GET /stories/{id}`, field `formatted_vcs_branch_name`). Null gives exit 3, as `story branch` does. Strip a trailing `-` or `/`, as `story branch` does. A name with no `sc-<id>` in it is exit 3 with the suggestion to use `--branch`, because the next run would not find the branch.
4. `git worktree add -b <name> <dir> origin/HEAD`, or `origin/main` when `origin/HEAD` is not set. `from: new`.

### Directory

- The worktree of the story is in the directory: no change, `from: worktree`.
- The directory exists for a different reason, empty or not: exit 2, no change.
- Else git makes it.

### `--start`

After the git part:

1. Read the story: `workflow_id`, `workflow_state_id`, `owner_ids`.
2. Read the workflow states. If the current state has type `unstarted`, set the state to the `started` state with the lowest position. A story in a `started` or `done` state does not move. A workflow with no `started` state is exit 3.
3. Add `@me` with `owner_ids_add`. Skip it if `@me` is an owner.
4. One `PATCH` with the changes, or no call when there is none.

If the git part worked and the Shortcut part fails, the worktree stays. The command exits with the Shortcut error code, and the error JSON has the worktree under `worktree`, so a caller can still open it.

### Git errors

`scut` runs the `git` binary. A failed git command gives exit 1, with its stderr in the error message.

### Output

```json
{"story":1234,"branch":"bug/sc-1234/slug","dir":"/home/u/ts/1234","created":true,"from":"new"}
```

With `--start`, the output also has `"started": true` when the state changed, else `false`. `--human` prints the directory and the branch.

## Shell function

The herdr part stays as it is now.

```zsh
# story <N>: worktree of story N, as a herdr space with Claude Code
story() {
  local wt dir n
  wt=$(cd ~/ts/main && scut worktree "$1") || return 1
  dir=$(jq -r .dir <<<$wt) n=$(jq -r .story <<<$wt)
  # herdr part unchanged: select the space for $dir, or make it and type "/story $n "
}
```

## Tests

- Unit tests on temporary git repositories: a bare repository as `origin`, a clone as the main worktree. One test for each `from` value, for `sc-12` against `sc-123`, for more than one match with and without `--branch`, for a directory in use, for a prunable worktree, for a branch found only after the fetch, for no fetch when a branch exists, and for a call from a worktree that is not main.
- `--start` against an `httptest` server, as in `internal/cli/story_update_test.go`: unstarted moves, started does not, `@me` already an owner, the Shortcut failure after the git part.
- `usage`, the skill reference and the README list the new command. `make check-skill` covers the drift.

## Calls made by the designer

- `--repo` and `--dir` are flags with defaults from the current directory, not configuration keys.
- The `git` binary, not a Go git library.
- The started state is the first `started` state of the story's workflow, not a fixed name.
- `origin` is the only remote. The base of a new branch is `origin/HEAD`, else `origin/main`.
- `--branch` names the new branch when nothing matches, and must contain `sc-<id>`.
- A branch name format with no `sc-<id>` is exit 3, not a branch that later runs cannot find.
- An empty existing directory counts as in use.
- A workflow with no `started` state is exit 3.

## Left to the builder

- The Go package layout of the git code.
- The text of the `--human` output.
