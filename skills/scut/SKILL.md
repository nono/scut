---
name: scut
description: Use when a task reads or changes Shortcut stories, epics, comments, links or attachments, or needs a Shortcut ID or branch name.
---

# scut

`scut` is a command line client for the Shortcut REST API v4. Use it for all Shortcut work.

## Rules

- Run `scut usage` first. Then run `scut <noun> usage` for the noun you need.
- Output is JSON on stdout. An error is one JSON object on stderr: `error`, `status`, `api`, `suggestion`, `valid`.
- Exit codes: 0 ok, 1 other, 2 usage, 3 not found, 4 auth, 5 unavailable, 130 cancelled.
- Exit 5 means a write may have taken effect. For `story create`, read the `suggestion` before you retry.
- Pass long text with `--body-file` or `--description-file`. Give a file, or `-` for stdin.
- Use names, not IDs: teams, states, members (`@me`), fields (`--field "Name=Value"`). A wrong name exits 3 and `valid` lists the right ones.
- A story is an ID (`1234`, `sc-1234`), a URL, or a branch name.
- `story search` takes the Shortcut query syntax unchanged. It can lag. Use `story view` for current data.
- Lists give 25 items. Use `--limit N` or `--limit all`.
- If a feature is missing, say so. Do not work around it with `scut api`, unless the user asks.

## Not supported

- Unlink a PR, branch or commit from a story (the v4 API has no endpoint).
- Docs.
- Story history.
- Bulk commands.
- Iterations, objectives and labels as commands.
- Sub-task commands after `story create`.

## Commands

Each line: command, summary, flags. Run `scut <noun> usage` for flag help.

- `api` `<path>` Send a raw request to the API. Flags: --field stringArray, --input string, --method string, --yes.
- `auth login` Read a token from stdin and store it in the keyring.
- `auth status` Show the workspace, scopes and source of the token.
- `cache clear` Remove the cache of this workspace.
- `epic comment` `<epic>` Add a comment to an epic. Flags: --body-file string.
- `epic search` `<query>` Search epics. Flags: --limit string.
- `epic stories` `<epic>` List the stories of an epic, in position order. Flags: --fields string, --limit string.
- `epic view` `<epic>` Show an epic, by ID or exact name.
- `field list` List custom fields.
- `field values` `<field>` List the values of a custom field.
- `member list` List members.
- `story branch` `[<story>]` Print the suggested git branch name of a story.
- `story comment` `<story>` Add a comment to a story. Flags: --body-file string.
- `story comments` `<story>` List the comments of a story, oldest first. Flags: --limit string.
- `story create` Create a story. Flags: --description-file string, --epic string, --estimate int, --external-link stringArray, --field stringArray, --label stringArray, --link stringArray, --name string, --owner stringArray, --parent string, --requester string, --state string, --team string, --type string, --workflow string.
- `story files` `[<story>] --out <dir>` Download the files attached to a story. Flags: --out string.
- `story link` `<story> <verb> <story> [<verb> <story>]...` Link stories.
- `story relink` `<story> <old-verb> <story> <new-verb>` Change the verb of a link.
- `story search` `<query>` Search stories. Flags: --fields string, --limit string.
- `story unlink` `<story> <verb> <story>` Remove a link between stories.
- `story update` `<story>` Change a story. Flags: --add-owner stringArray, --archive, --description-file string, --epic string, --field stringArray, --name string, --remove-owner stringArray, --requester string, --state string, --unarchive, --workflow string.
- `story view` `[<story>…]` Show stories; default story from the git branch. Flags: --comments string, --fields string.
- `team list` List teams.
- `whoami` Show the member and workspace of the token.
- `workflow list` List workflows.
- `workflow states` `[<workflow>]` List the states of a workflow, or of all.
