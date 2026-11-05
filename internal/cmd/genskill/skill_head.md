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
- A doc is a doc URL, a doc ID or an exact title. A doc can be large: `doc view --out <file>` writes the content to a file, so read the file in parts.
- `story search` takes the Shortcut query syntax unchanged. It can lag. Use `story view` for current data.
- Searches and comment lists give 25 items. Use `--limit N` or `--limit all`. Lookup lists give all items.
- Global flags: `--profile <name>` picks a workspace profile, `--token-file <path>` reads the token from a file, `--human` prints text.
- If a feature is missing, say so. Do not work around it with `scut api`, unless the user asks.

## Not supported

- Unlink a PR, branch or commit from a story (the v4 API has no endpoint).
- Create or update a doc.
- Story history.
- Bulk commands.
- Iterations, objectives and labels as commands.

## Commands

Each line: command, summary, flags. Run `scut <noun> usage` for flag help.

