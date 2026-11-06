---
name: scut
description: "Use when a task reads or changes Shortcut stories, epics, comments, links or attachments: a story ID such as sc-1234, an app.shortcut.com URL, a git branch with sc-NNNN in it, a ticket or bug to file, a sub-task, a state change such as \"move it to In Review\", a Shortcut doc, or the branch name of a story."
allowed-tools: "Bash(scut auth status:*), Bash(scut auth usage:*), Bash(scut cache usage:*), Bash(scut doc search:*), Bash(scut doc usage:*), Bash(scut epic search:*), Bash(scut epic stories:*), Bash(scut epic usage:*), Bash(scut epic view:*), Bash(scut field list:*), Bash(scut field usage:*), Bash(scut field values:*), Bash(scut member list:*), Bash(scut member usage:*), Bash(scut spec:*), Bash(scut story branch:*), Bash(scut story comments:*), Bash(scut story search:*), Bash(scut story subtasks:*), Bash(scut story usage:*), Bash(scut story view:*), Bash(scut team list:*), Bash(scut team usage:*), Bash(scut usage:*), Bash(scut whoami:*), Bash(scut workflow list:*), Bash(scut workflow states:*), Bash(scut workflow usage:*), Read(~/.local/state/scut/**)"
---

# scut

`scut` is a command line client for the Shortcut REST API v4. Use it for all Shortcut work.

## Rules

- Run `scut usage` first. Then run `scut <noun> usage` for the noun you need. Do not guess a command or a flag.
- Output is JSON on stdout. An error is one JSON object on stderr: `error`, `status`, `api`, `suggestion`, `valid`.
- Use names, not IDs: teams, states, members (`@me`), epics, fields (`--field "Name=Value"`).
- A story is an ID (`1234`, `sc-1234`), a URL, or a branch name. With no story, `story view`, `story branch`, `story files`, `story subtasks` and `worktree` use the story of the current git branch.
- Put global flags after the command: `scut story view 12 --human`. Global flags: `--profile <name>` (workspace profile), `--token-file <path>`, `--human` (text output).

## When a command fails

| Exit | Meaning | Do this |
|---|---|---|
| 2 | Usage error, or the API refused a request (HTTP 400). | Pick from `valid`, or do the `suggestion`. A noun with no command is an error. A command that sends several writes (`story link` with several pairs, `story update` on several stories) can have done some: read stdout, and the `suggestion` names the stories that may be changed. |
| 3 | Not found. | Check the ID. For a name, pick from `valid`. |
| 4 | No token, a bad token, or a missing scope. | Do not retry. A read-only token (see `scut auth status`) cannot write: stop. If `SCUT_TOKEN`, `--token-file` or `token_file` in the config is set, ask the user to fix it: `auth login` writes only the keyring. Else ask the user to run `scut auth login` in a terminal (token from https://app.shortcut.com/settings/account/api-tokens). |
| 5 | Shortcut is unavailable. | Retry later. A write may have taken effect: read the `suggestion` first. For `story create`, it says if the story exists. |
| 1 | Other error. | Read `error` and `api`. |
| 130 | Cancelled. | Read the `suggestion`, if any. |

- If the shell says `scut: command not found`, tell the user to install it with `mise use github:nono/scut`. Do not install it yourself.

## Common tasks

```sh
scut story view
scut story view 12 13 14 --fields id,name,state  # several stories in one call, few fields
scut story search '!is:done owner:me' --fields id,name,state
scut story create --type bug --name "Login fails" --state "To Do" --description-file d.md
scut story update 12 --state "In Review" --add-owner @me
scut story update 13 14 15 --parent 12  # make sub-tasks of 12
scut story comment 12 --body-file note.md
scut story link 12 blocks 13 relates-to 14
scut story files 12 --out ./files  # download the attachments
scut story branch 12  # the git branch name
scut worktree 12 --start  # find or make the worktree, and start the story
```

## Output size

- `story view`, `story search`, `story subtasks` and `epic stories` take `--fields`.
- Searches and comment lists give 25 items. Use `--limit N` or `--limit all`. Lookup lists give all items.
- `story search` takes the Shortcut query syntax unchanged. It can lag: use `story view` for current data.
- A doc is a doc URL, a doc ID or an exact title. A doc can be large: `doc view --out <file>` writes the content to a file, so read the file in parts.

## Long text

Write the text to a file with your file tool. Then pass `--description-file <file>` or `--body-file <file>`, or `-` for stdin. Do not put markdown in a shell argument: the shell can break its newlines and quotes.

## Writes

- Archive, unlink, `--no-parent`, or a change of parent: only when the user asks.
- Never add `--yes` yourself. `scut api -X DELETE` needs it.

## Missing features

- If a feature is missing, say so. Do not work around it with `scut api`, unless the user asks.
- When the user asks for `scut api`, first run `scut spec <operationId>` (for example `scut spec getStory`) to get the method, the path and the body. A path is relative to the workspace: `scut api stories/12/story-links`.

## Log

Each run adds one JSON line to `~/.local/state/scut/scut.log` (older lines are in `scut.log.1`). `$XDG_STATE_HOME` changes the directory. Fields: `t`, `v`, `session`, `cwd`, `cmd`, `ids`, `args_n`, `flags`, `ms`, `out_bytes`, `exit`, `error`, `api`, `http` (each try: `m`, `p` or `h`, `s`, `ms`, `err`).

Read it when the user asks about scut usage or a past failure, or when an error on stderr does not explain itself. Do not read it on each failure. `fromjson?` skips a line that a full disk broke.

```sh
jq -cR 'fromjson?|select(.exit!=0)|{t,cmd,exit,error}' ~/.local/state/scut/scut.log
jq -R 'fromjson?' ~/.local/state/scut/scut.log | jq -s -c 'map(select(.session))|group_by([.session,.cmd])|map({session:.[0].session,cmd:.[0].cmd,n:length})|map(select(.n>=3))|.[]'  # bulk candidates
```

## Not supported

- Unlink a PR, branch or commit from a story (the v4 API has no endpoint).
- Create or update a doc.
- Story history.
- Bulk create, and a batch file of changes.
- Iterations, objectives and labels as commands.

## Commands

Each line: command, summary, flags. A flag that ends in `…` is repeatable. Run `scut <noun> usage` for flag help.

- `api` `<path>` Send a raw request to the API. Flags: --field …, --input, --method, --yes.
- `auth login` Read a token from stdin and store it in the keyring.
- `auth status` Show the workspace, scopes and source of the token.
- `cache clear` Remove the cache of this workspace.
- `doc search` `<text>` Search docs by title. Flags: --limit.
- `doc view` `<doc>` Show a doc and its content, by URL, ID or exact title. Flags: --format, --out.
- `epic comment` `<epic>` Add a comment to an epic. Flags: --body-file.
- `epic search` `<query>` Search epics. Flags: --limit.
- `epic stories` `<epic>` List the stories of an epic, in position order. Flags: --fields, --limit.
- `epic view` `<epic>` Show an epic, by ID or exact name.
- `field list` List custom fields.
- `field values` `<field>` List the values of a custom field.
- `member list` List members.
- `spec` `<operationId|SchemaName>` Show the shape of a v4 operation or schema. Flags: --depth.
- `story branch` `[<story>]` Print the suggested git branch name of a story.
- `story comment` `<story>` Add a comment to a story. Flags: --body-file.
- `story comments` `<story>` List the comments of a story, oldest first. Flags: --limit.
- `story create` Create a story. Flags: --description-file, --epic, --estimate, --external-link …, --field …, --label …, --link …, --name, --owner …, --parent, --requester, --state, --team, --type, --workflow.
- `story files` `[<story>] --out <dir>` Download the files attached to a story. Flags: --out.
- `story link` `<story> <verb> <story> [<verb> <story>]...` Link stories.
- `story relink` `<story> <old-verb> <story> <new-verb>` Change the verb of a link.
- `story search` `<query>` Search stories. Flags: --fields, --limit.
- `story subtasks` `[<story>]` List the sub-tasks of a story, in position order. Flags: --fields, --limit.
- `story unlink` `<story> <verb> <story>` Remove a link between stories.
- `story update` `<story>…` Change one story, or make the same change to several. Flags: --add-owner …, --archive, --description-file, --epic, --field …, --name, --no-parent, --parent, --remove-owner …, --requester, --state, --unarchive, --workflow.
- `story view` `[<story>…]` Show stories; default story from the git branch. Flags: --comments, --fields.
- `team list` List teams.
- `whoami` Show the member and workspace of the token.
- `workflow list` List workflows.
- `workflow states` `[<workflow>]` List the states of a workflow, or of all.
- `worktree` `[<story>]` Find or make the git worktree of a story. Flags: --branch, --dir, --repo, --start.
