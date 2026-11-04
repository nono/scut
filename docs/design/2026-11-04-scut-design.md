# scut: a Shortcut CLI on the REST API v4

Status: approved, 2026-11-04.
Requirements: the requirements page (private) (version 8). IDs below (U1, G3, N1) refer to that page.

## Intent

`scut` replaces the Shortcut MCP server for Claude Code and gives herdr plugins and scripts the same access. The MCP has these problems:

- Its gateway fails (about 40 sessions had a 503, 504 or failed connect).
- It works only in `~/ts/main`.
- It needs a second call to set the requester and custom fields.
- It cannot download attachments.
- It returns results too large to read.
- It needs UUIDs as input.

A CLI that calls the REST API v4 directly, takes names, and prints compact JSON removes these problems. A short `SKILL.md` replaces the MCP tool schemas in the agent context.

## Goals (v1)

v1 has every MUST item of the requirements page: U1–U10 and G1, G3–G9. G2 is now COULD.

v1 ships:

1. The `scut` binary.
2. Its `SKILL.md`.

## Non-goals (v1)

- Changes to the skills `file-story`, `story` and `grid-video`. They continue to use the MCP until a later change.
- U11 (sub-task commands after create; the parent at create is in v1, G3), U12 (docs), U13 (history), U14 (external links).
- G2: unlink a PR, branch or commit. v4 has no endpoint for it. v1 can list them as part of `story view`.
- G12: bulk commands. v1 accepts several links in one `story link` command, as one call per link (see Links).
- N1 `scut worktree`, N2 shell completion.
- Iterations, objectives, labels as their own commands. The command tree must have room for them.

## Anti-goals

A command that does these things is a failure, even if it works:

- It drops a field with no error, or applies a filter after paging (shortcut-cli #293, PR 398).
- It exits 0 on a failure, or prints a spinner or warnings on stderr.
- It prompts for input when stdin is not a terminal or `--human` is not set.
- It replaces a set (owners, labels) when you add or remove one item.
- It makes a duplicate story when it retries a create.
- It saves an error page as an attachment.
- It puts the token in a URL, a log line, an error message or the agent transcript.

## Constraints

- **API:** REST API v4 only, base `https://api.app.shortcut.com/api/v4/{workspace-slug}/`. v4 is beta and can change.
- **Token:** a v4 token, `sct_{scopes}_{workspace-slug}_{secret}`. Get the workspace slug from the token. Do not call `whoami` only to get the slug.
- **Scopes:** `r`, `w`, `s` (story write), `c` (comment write), `a` (admin). A read-only token (`sct_r_`) must work for all read commands.
- **Rate limit:** 200 requests per minute.
- **Language and install:** Go, one static binary, install with `mise` from tagged releases.
- **Start time:** herdr runs `scut` for each action and event. With a warm cache, each read command makes at most 2 API calls, plus 1 call for each more page of `--limit`. Only `story view` of one story with comments makes 2, plus 1 when the story has tasks.
- **User rules (CLAUDE.md):** no shell script over 100 lines, no special cases for the inputs of tests. A coverage drop is worse than a failing test.

## Verified API facts (2026-11-04, spec version 4.0, 173 paths)

| Topic | Fact | Design effect |
|---|---|---|
| Attachment download | Test on a test story: `Authorization: Bearer` gives 400 from the storage backend ("Only one auth mechanism allowed"). `Shortcut-Token: <token>` header and `?token=` give 200 and the full file. No auth gives 401. | Send `Shortcut-Token`, never `Bearer`, to the file host. Do not use `?token=`. |
| Find by `external_id` | No endpoint or search operator filters on it. `GET /stories` takes `order_by=created_at`, `order_dir`, `fields`, `ids`, `limit`, `cursor`. | Make the G8 check with `GET /stories?order_by=created_at&order_dir=desc&fields=id,external_id,…`. This reads current data, not the search index. |
| Bulk | Atomicity is not stated. Errors are 400, 404 and 422, with no per-item result. | v1 does not use bulk endpoints. |
| Branch name | `formatted_vcs_branch_name` is "built from the Workspace's branch name format. Null if the Workspace has no branch name format." | On null, exit 3 (not found) with a message. Do not make up a name. |
| G8 lookup, live | Test on 2026-11-04: right after each of 2 creates (201 in 0.5–0.7 s), the newest-stories call found the `external_id`. | The G8 check works with no wait. |
| Updates | `PATCH` with `_add` / `_remove` fields (`owner_ids_add`, `label_ids_remove`). | Map `--add-owner` / `--remove-owner` to these. A set flag (`--owners`) replaces the set. |
| Custom fields, live | Test on 2026-11-04: `custom_field_values_add` with a new value of a field that has a value replaces that value. Values of other fields stay. `custom_field_values_remove` removes one value. | `--field "Name=Value"` maps to `custom_field_values_add`. It never replaces the set. |
| Links, live | Test on 2026-11-04: the verbs are `blocks`, `duplicates`, `relates to`. `PATCH /story-links/{id}` changes the verb, and also the subject and object (swap tested). A second create of the same link gives 422 "Cannot create a duplicate Story Link." The response has `subject.id` and `object.id`, not `subject_story_id`. `DELETE` gives 204. | `relink` is one `PATCH`. A duplicate link is a success (see Links). |
| `whoami` | The path is `/api/v4/whoami`, with no workspace slug. It gives the member, the scopes, the default workflow and the default state. | `auth login` and `auth status` use it. |
| Names, live | "To Do" is a state in 2 workflows (Standard, Simpler Kanban). The Technical Area value is "Back end", not "Back End". | The state rule below is needed. The default in the user file is "Back end". |
| Responses | An envelope with `entity` or `entities` and a cursor. | Print the entity or the array, not the envelope. |
| Paging | Cursor pages, `limit` 1–100, default 10. | The CLI default is 25 (my call). `--limit all` pages to the end. |

## Command tree (v1)

The nouns are singular. Each write action is its own subcommand, so a permission rule like `Bash(scut story comment:*)` allows only that action (G10).

```
scut story view [<story>…] [--fields f,…] [--comments N|all] [--human]
scut story search <query> [--limit N|all] [--fields f,…]
scut story create --name … [--type] [--team] [--workflow] [--state] [--epic] [--parent <story>]
                  [--owner …] [--requester …] [--label …] [--estimate N] [--field "Name=Value"]…
                  [--description-file f|-] [--link "<verb> <story>"]… [--external-link URL]…
scut story update <story> [--name] [--state] [--epic] [--requester] [--add-owner …] [--remove-owner …]
                  [--field "Name=Value"]… [--description-file f|-] [--archive|--unarchive]
scut story comment <story> --body-file f|-
scut story comments <story> [--limit N|all]
scut story link <story> <verb> <story> [<verb> <story>]…
scut story unlink <story> <verb> <story>
scut story relink <story> <old-verb> <story> <new-verb>
scut story branch [<story>]
scut story files [<story>] --out <dir>
scut epic view <epic> · scut epic search <query> · scut epic stories <epic> · scut epic comment <epic> --body-file f|-
scut team list · scut member list · scut workflow list · scut workflow states [<workflow>]
scut field list · scut field values <field>
scut whoami
scut api <path> [-X METHOD] [-f key=value]… [--input file|-]
scut usage · scut <noun> usage
scut auth login · scut auth status
scut cache clear
```

`<story>` accepts `sc-NNNN`, `NNNN`, a story URL, or a branch name. If you do not give a story, `scut` takes it from the current git branch with the regex `sc-(\d+)`. Outside a git repo, or with no match, it exits 2 (usage) with "give a story ID".

`story view` with several IDs makes one `GET /stories?ids=…` call. This covers the "read 10 stories" case of G12.

**API calls of `story view` (my call):** In v4, the comments in a story are `CommentSlim`: id, text and `deleted`, with no author and no date. Each nested list (comments, links, branches, PRs, files) has `current_items` and `total_items`, so the API can cut a list short. For these reasons:

- `story view <one story>` makes 2 calls: the story, and `GET /stories/{id}/comments?order_dir=desc&limit=10` for author and date. If `--fields` does not ask for comments, it makes 1 call.
- The tasks nested in a story are `ChecklistItemSlim`, with no `completed`. When the story has tasks, `story view <one story>` also reads `GET /stories/{id}/checklist-items` to show `complete`. A list of stories shows tasks as `{id, description}`, with no `complete`.
- `story view` with several stories makes 1 call and returns the slim comments.
- When the API cuts a nested list short, the output has `"<list>_total": N` beside the list. `scut` never shows a short list as complete.

Writes need the story-write or comment-write scope, or `w`. `story unlink` and `relink` change data. They do not delete a story, so they do not need `--yes`. A delete command needs `--yes` and prints what it deletes. v1 has no delete command, but `scut api -X DELETE` needs `--yes`.

## Behavior

### Output (G7)

- JSON on stdout by default, on a terminal and in a pipe. A list is an array. A view is an object. A write prints the entity that the API returns.
- `--human` or `SCUT_OUTPUT=human` prints text. Only `--human` on a terminal can open `$EDITOR` or prompt.
- Default fields are compact. `story view` returns: id, name, type, state name, owners and requester as mention names, epic id and name, labels, custom fields as `{name: value}`, the description, tasks, links, branches, PRs, and the last 10 comments. `search` and `epic stories` leave out the description and comments. `--fields` takes API field names and passes them to the API `fields` parameter. It also takes the output names (`state`, `type`, `tasks`, …) as aliases. An API field that the compact shape does not model is printed as the API sent it, under its API name. `genskill` writes the Story field names from the committed spec to `internal/cli/storyfields_gen.go`, and `make check-skill` fails when that file drifts.
- Names in output (state, member, field) come from the cache. The design must keep the IDs as well, so a script can use them.
- Deleted comments (`text: null`) show as `{"deleted": true}`.

### Errors and exit codes (G11)

Errors go to stderr as one JSON object. The object has `error`, `status` (HTTP status, if any), `api` (the API error body), `suggestion`, and `valid` (the valid names when a name did not resolve).

| Code | Meaning |
|---|---|
| 0 | Success |
| 1 | Other error |
| 2 | Usage error (bad flag, no query, no story ID). `--help` and `usage` need no token. |
| 3 | Not found (story, name, branch name is null) |
| 4 | Auth (401, 403, wrong scope, no token) |
| 5 | Unavailable after retries (429, 502, 503, 504, connect failure, non-JSON 5xx). A write can have taken effect. |
| 130 | Cancelled |

Before `scut` parses a body, it checks the status and the `Content-Type`.

### Retries (G5, G8)

- Retry 429, 502, 503, 504 and connect failures with exponential backoff and jitter. Use `Retry-After` when the response has one. Default: 4 tries at most, and a call ends 20 s after it starts (my call): each try, with the read of its body, has a deadline of the time left in the 20 s (at most 30 s; the first try has at least 2 s). After the last try, exit 5.
- A failed `GET` can be retried. A `POST` create is retried only through the G8 check.
- **G8 check:** each create sends `external_id` = a new UUIDv4. Before each retry, and on exit 5, read the newest stories (`order_by=created_at desc`, `limit 25`, `fields=id,external_id,name,app_url`) and look for that UUID. If it is there, print that story and exit 0.
- **G8 after each failure (ruling of 2026-11-04, replaces R17):** a 4xx other than 429 makes no story: exit with its code. After a 429, a 503 or a connect failure before the request was sent, the server did not take the request: read, then create again. After a 502, a 504 or a timeout (no answer, or a cut body), the server may still save the story after the read: never create again, and read again with the backoff until the budget ends. After any other failure (a 500, an empty or non-JSON 2xx body), read once. If no read finds the story, exit 5 with the `external_id` in `suggestion`.

### Names to IDs (G4)

- Names are accepted for team, workflow, state, member (`@me`, mention name, or email), epic (ID or exact name), label, custom field and its values.
- The match is exact and case-sensitive (my call: case-sensitive avoids two names that differ only in case). If there is no match, exit 3 and list the valid names in `valid`.
- A state name that is in more than one workflow needs `--workflow`, or the workflow of `--team`. Otherwise exit 2 and list the workflows.
- Story, epic and project IDs are integers. Member, team, state and field IDs are UUIDs. Normalize the input before the lookup.
- **Cache:** members, teams, workflows and states, custom fields and values, labels. Store as JSON in `$XDG_CACHE_HOME/scut/<workspace>/`. TTL is 1 hour (my call). Each entry keeps the time of its fetch. `cache clear` writes the time of the clear to a `cleared-at` marker, and an entry fetched before it is stale, so a fetch that ends after a clear cannot bring back old data. On a name miss, refresh once and try again before the error.

### Configuration (G4, G9)

- User file: `$XDG_CONFIG_HOME/scut/config.toml`. Repo file: `.scut.toml`, found from the current directory up to the git root.
- Precedence, the same for each setting: flag > env (`SCUT_*`) > repo file > user file > built-in default. The env names are `SCUT_TEAM`, `SCUT_STATE`, `SCUT_WORKFLOW`, `SCUT_REQUESTER`, `SCUT_OWNER`, `SCUT_OUTPUT` and `SCUT_FIELDS` (`Name=Value;Name=Value`, which overrides the `[fields]` of the files per key).
- Token: `--token-file` flag > `SCUT_TOKEN` > `token_file` in the user file > OS keyring. The repo file cannot hold a token or a token file. `scut` refuses one there, because a repo file can be committed. Trim whitespace from the token.
- `auth login` reads the token from stdin with no echo, checks it with `whoami`, and stores it in the keyring. `auth status` shows the workspace, the scopes and the source of the token, but never the token.
- Defaults (your values): team Software Dev, state To Do, requester and owner `@me`, field Technical Area = Back end. These go in the user file.
- Each config value is validated. An error names the file and the key.
- Profiles for more than one workspace: `[profile.<name>]` tables and `--profile` / `SCUT_PROFILE`. Each profile has its own token.

### Links (U5, G1)

- Verbs: `blocks`, `blocked-by`, `relates-to`, `duplicates`, `duplicated-by`. `blocked-by` and `duplicated-by` swap subject and object.
- The CLI verbs map to the API verbs `blocks`, `duplicates` and `relates to`.
- `story link A blocks B relates-to C` makes one `POST /story-links` per link. The output is an array with each link and its result. If one link fails, the others continue, and the exit code is the first error code.
- A 422 "Cannot create a duplicate Story Link" is a success with `"existed": true` (my call). This makes a link retry safe after a 5xx.
- `unlink` finds the link ID with `GET /stories/{id}/story-links`, then `DELETE /story-links/{id}`. `relink` is one `PATCH` with the new verb, subject and object.

### Files (U10, G6)

- `story files` lists with `GET /stories/{id}/files` and downloads each `url` with the `Shortcut-Token` header. A download that fails with 429, 502, 503, 504 or a lost connection is tried again, with the backoff and budget of Retries, into a new temp file.
- `scut` writes to a temp file in `--out` and moves it to its name when the download is complete. On a non-2xx status, or when the size is not the size in the entity, it deletes the temp file and exits with an error.
- File names come from the entity `name`. `scut` cleans the name to its base name. If two names are the same, or the name is taken in `--out`, it adds `-<file id>` (then `-<file id>-2`, …). It never replaces a file: it links the temp file to the free name, and a link fails when the name exists.
- Output: an array of `{id, name, path, size, content_type}`.

### Search (U2)

- The query goes to `GET /stories/search?query=…` unchanged. A filter flag is never applied in the client.
- With no query, exit 2.
- Help says that the search index can lag, and that `story view` reads current data.
- Paging stops when the cursor repeats, or after 100 pages.

### Agent skill

- The skill is in this repo at `skills/scut/SKILL.md`, so `npx skills add nono/scut` can install it. `scut` has no command to install the skill.
- The command reference in `SKILL.md` is generated from the command tree by a dev tool (`go run ./internal/cmd/genskill`). CI fails if the committed file differs from the generated one, so the reference cannot drift from `--help`.
- `scut usage` prints about 200 tokens: the nouns and one line each. `scut <noun> usage` prints that noun's commands and flags.
- The skill says: use `scut`; read `scut usage` first; JSON output; exit codes; write long text to a file and pass `--body-file`.
- The skill does not mention the MCP.
- The skill has a "Not supported" list (for example: unlink a PR or branch, docs, history, bulk). It tells the agent to say that the feature is missing, and not to work around it with `scut api` unless the user asks.

## Build and test

- Commit the v4 spec (`api/shortcut.openapi.json`). It is the contract for the Prism tests, the operation check of the `scut/ops` annotations and the coverage table. No Go client is generated from it: commands shape raw JSON (decision of 2026-11-04). CI downloads the live spec and shows a diff when it changed.
- Unit tests for each package. Contract tests run each command against Prism (Stoplight) in mock mode on the committed spec. A write test checks the request body against the schema (shortcut-cli #366).
- Recorded responses (fixtures) for flows that Prism cannot model: G8 retry, paging loop, file download error page, name resolution.
- One live smoke test, run by hand or in CI with a read-only token: `whoami`, `story view`, `story search`, `story files` on a test story (it has a 9,692-byte attachment).
- Generate a coverage table of the v4 endpoints that `scut` calls, and check it in CI.
- Host: `github.com/nono/scut`, public. This is also the Go module path. CI is GitHub Actions (my call). The GitHub repo does not exist yet. Work locally until it does.
- Release from a git tag, as a GitHub release binary for linux amd64 only (your call; other platforms later). `mise use github:nono/scut` installs them with no token, because the repo is public.

## Left to the builder

- The Go libraries: the CLI framework, the OpenAPI generator, TOML, the keyring.
- The package layout.
- The `--human` text format.
- The JSON field names of the compact output, if they keep the API names when the value is the same.
