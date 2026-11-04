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

