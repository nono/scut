# Run log

Status: approved, 2026-11-06.

## Intent

scut is new. We do not know yet which failures and which usage patterns will occur. A local log keeps the facts of each run, so that later we can answer questions that we cannot predict now.

The log has four purposes:

1. Debug a failure after it occurred.
2. Give statistics about usage.
3. Give clues about features to add, for example many calls that one bulk action can replace.
4. Let Claude Code examine its own use of scut, and improve it.

Only the user and Claude Code read the log, on the machine of the user.

## Goals

- Each run of `scut` adds one JSON line to a local file.
- The line has facts: command, flag names, IDs, each HTTP try, duration, exit code, error.
- The total size of the log has a limit.
- The skill tells Claude Code where the log is, what it has, and when to read it.

## Non-goals

- Sending the log to a server, or collecting the logs of other machines.
- A `scut log` or `scut stats` command. JSON Lines and `jq` are sufficient at first (my call). Add a command only if real use shows a need.
- Debug output on stderr, or a verbosity flag.
- A line for a run that a panic, `SIGKILL` or `SIGTERM` stops. `SIGINT` writes a line, with exit 130. A closed stdout pipe writes a line, with exit 141 and nothing on stderr, as a process that `SIGPIPE` stops.
- Git commands that `scut worktree` runs.

## Anti-goals

The change is a failure, even if it works, if:

- The log has the token, in any field.
- The log has text from a user or from Shortcut, other than the three exceptions below (error fields, the path of `scut api`, the flags in the allowlist): story names, descriptions, comments, search queries, file contents.
- A write error of the log changes stdout, stderr or the exit code. A file system that blocks a write also blocks the run: scut cannot prevent this. A warning on stderr is already an anti-goal of the v1 design.
- The log adds a noticeable delay. herdr runs `scut` for each action and event.

## What a line has

Your decisions: facts only, no text. Raw API error messages are permitted, truncated. The raw path of `scut api` is permitted. The remaining rules below are my calls.

A successful run:

```json
{"t":"2026-11-06T10:15:30.123Z","v":"v0.4.0","session":"00000000-0000-4000-8000-0000000000aa","cwd":"/home/test/project","cmd":"story update","ids":[1234],"args_n":1,"flags":{"state":"Done","name":""},"ms":412,"out_bytes":310,"exit":0,"http":[{"m":"PATCH","p":"/api/v4/example-ws/stories/1234","s":200,"ms":210}]}
```

A failed run, with a retry:

```json
{"t":"2026-11-06T10:16:02.456Z","v":"v0.4.0","cwd":"/home/test/project","cmd":"team list","args_n":0,"ms":20100,"out_bytes":0,"exit":5,"error":"GET api.app.shortcut.com /api/v4/example-ws/teams: context deadline exceeded","http":[{"m":"GET","p":"/api/v4/example-ws/teams","s":503,"ms":40},{"m":"GET","p":"/api/v4/example-ws/teams","s":0,"ms":19500,"err":"context deadline exceeded"}]}
```

| Field | Content | Rule |
|---|---|---|
| `t` | Start time, UTC | |
| `v` | `transport.Version` | |
| `session` | `CLAUDE_CODE_SESSION_ID` | Left out when not set. This groups the calls of one Claude Code session, and tells an agent run from a human or herdr run. |
| `cwd` | `env.Dir` | |
| `cmd` | Command path without `scut`, from the command that cobra returns | An unknown subcommand gives the group, for example `story`. The name that was not known is in `error`. |
| `ids` | Each positional argument of a command that takes IDs, when it is all digits, or `sc-` and digits for a story | At most 100. A number is written as a JSON integer, and a number that does not fit in 63 bits is left out. An epic can have the name `sc-12`, so only digits count for an epic. No such command takes a UUID. Any other argument is left out. `IDArgsAnnotation` marks these commands, and a new command does not have it. So a search query, a title or a URL slug is never logged, also in the form of an ID. |
| `args_n` | Number of positional arguments | Also shows the true count when `ids` is cut. Left out after a flag parse error, because pflag stops at the bad flag. `ids` and `flags` then hold only what came before it. |
| `flags` | Each flag that the user set, name to value | The value is present only for the names in the allowlist, at most 200 bytes. Other flags have `""`. |
| `ms` | Duration of the run | |
| `out_bytes` | Bytes written to stdout | Shows outputs that are too large for an agent. On a closed pipe, it also counts the bytes of the write that failed. |
| `exit` | Exit code | |
| `error` | `exit.Error.Msg`, or `err.Error()` for another error | Left out on success. At most 1 KB. |
| `api` | `exit.Error.API`, as a string | Left out when empty. At most 1 KB. |
| `http` | One entry for each HTTP try, in order | At most 100, and `http_n` gives the true count when cut. |

An `http` entry has `m` (method), `s` (status, 0 when there is no response), `ms` (time to the response headers) and `err` (the transport error, at most 200 bytes, only when `s` is 0). An API call has `p`, the URL path. A file download has `h`, the host, and no path, because a file URL holds the name of the file. No entry has a URL query, because a search query is there.

Because the entries come from each try, retries and redirects are visible. A cut 2xx body that no retry heals shows in `error`, not in `http`. A cut file body that a retry heals shows only as one more try.

**Flag allowlist (approved 2026-11-06):** `limit`, `comments`, `fields`, `format`, `depth`, `method`, `state`, `workflow`, `team`, `type`. These flags are for a number or a name. The log keeps any text that a user types there. A new flag has no logged value until someone adds it. Values of `name`, `field`, `epic`, `label`, `owner`, `add-owner`, `remove-owner`, `requester`, `body-file`, `description-file`, `token-file`, `input`, `out`, `dir`, `branch`, `repo`, `parent`, `link` and `external-link` are not logged. `-f` of `scut api` also has no value in the log.

**Errors of scut itself:** `error` also holds errors that scut makes, not only API errors. Some of them quote an input, for example `-f "x" is not key=value` or a name that did not resolve. I keep them, because these errors are the best evidence of how an agent misuses scut (purposes 2 to 4). So input text can reach the log here, but only on a failed run.

**Path of `scut api` (your decision):** `p` is the raw URL path, also when the user gave it to `scut api`. So `scut api /stories/some-text` logs `some-text`. The path shows which missing command each call could become. Its URL query is not logged, as for all other calls.

**Token:** the logger replaces each match of `sct_(?:[^"\\]|\\.)+` in the encoded line with `***`, so a new field is covered with no new code. A secret can hold any character, so the match goes to the end of the JSON string, and the rest of that string is lost. It takes an escape pair as a whole, so the line stays valid JSON. It uses a pattern, not the value, because a usage error can occur before the token loads. All other token rules of the v1 design stay.

## Where the log is

- `$XDG_STATE_HOME/scut/scut.log`, or `$HOME/.local/state/scut/scut.log`.
- A relative value counts as not set, as the XDG specification says, because a log under the current directory can be committed. With neither variable set, there is no log.
- The directory has mode 0700 and the files have mode 0600, because the log holds IDs, paths and error text. scut also sets these modes on an existing directory, log and `.1`. If it cannot set them on the directory or the log, it writes no line.
- A log path that is not a regular file, such as a FIFO, gets no line. The open does not wait.
- `SCUT_LOG=off` stops the log. There is no other setting. The config files do not control it, because the log must also record a config error.

The log is on by default, and the README says so, with what it records and how to stop it.

## Size limit

- When `scut.log` has 5 MiB or more at the start of a write, scut renames it to `scut.log.1`, which replaces the old `scut.log.1`. The log is thus at most about 10 MiB.
- A typical line is about 300 bytes, so one file holds about 17,000 runs.
- There is no compression, because `jq` and the Read tool must read the files directly.
- If the rename cannot complete, the log grows until it has twice the size that starts a rotation. Then scut writes no more lines, and the run continues.

**Concurrent runs:** herdr can run many `scut` processes at the same time.

- Each process writes its line with one `write` call on a file opened with `O_APPEND`. Lines from concurrent processes do not mix on a local Linux file system.
- Two processes can both see a full file. If both rename, the second rename replaces the full `.1` with a small file, and the history is lost. So the rename needs a non-blocking `flock`. The process that gets the lock checks the size again and renames. A process that does not get the lock writes its line and does not rename.
- A process that opened the file before the rename writes its line to `.1`. This is harmless. A process that holds the file across two rotations writes to a deleted file, and its line is lost. That needs about 17,000 other runs during one write.
- A short write, for example on a full disk, leaves a line with no newline. The next process sees this and starts with a newline, so that its own line stays whole. The recipes skip the broken line.

`syscall.Flock` does not compile on Windows. The release is Linux only, so this is acceptable now. A Windows port needs its own lock.

## Where the code goes

- New package `internal/runlog`: the line type, the rules for cutting and scrubbing, the path, the rotation and the write.
- `cli.Execute` starts a record before it runs the command, and writes it after. It uses the command that `ExecuteContextC` returns. `RunContext` keeps its signature, and tests that call it directly write no log.
- `Env` gets a field for the record. With nil, nothing is recorded.
- An `http.RoundTripper` that wraps the transport adds the `http` entries. It goes on the clients of the two `transport.New` calls (`internal/cli/deps.go`, `internal/cli/auth.go`, with `transport.WithHTTPClient`; the per-try context of `Doer.Do` gives the time limit) and on `fileClient` (`internal/cli/story_files.go`).
- A counting writer around stdout gives `out_bytes`.
- `internal/live/live_test.go` runs the real binary. It must set `SCUT_LOG=off`, so that tests do not fill the real log.

The log write ignores all errors: no directory, no permission, a full disk.

## Skill

`internal/cmd/genskill/skill_head.md` gets a short section, then `make skill`:

- The path and the fields.
- Read it when the user asks about scut usage or a past failure, or when a failure on stderr does not explain itself.
- Do not read it on each failure.
- The recipes below, or the two most useful ones.

Claude Code needs a permission to read a file outside the project, or to run `jq` on it. A permission rule for the log path in `allowed-tools` is the first thing to try. If `genskill` cannot express the rule, the agent gets a permission prompt, and that is acceptable.

## Recipes

I ran these on a log from the real binary that has one broken line. `fromjson?` skips a broken line.

```sh
L=~/.local/state/scut/scut.log

# Usage and failures for each command
jq -R 'fromjson?' "$L" | jq -s -c 'group_by(.cmd)|map({cmd:.[0].cmd,n:length,failed:(map(select(.exit!=0))|length)})|sort_by(-.n)|.[]'

# Failures
jq -cR 'fromjson?|select(.exit!=0)|{t,cmd,exit,error}' "$L"

# Bulk candidates: the same command 3 or more times in one session
jq -R 'fromjson?' "$L" | jq -s -c 'map(select(.session))|group_by([.session,.cmd])|map({session:.[0].session,cmd:.[0].cmd,n:length})|map(select(.n>=3))|.[]'

# Runs with a rate limit or a server failure
jq -cR 'fromjson?|select(.http)|select(any(.http[];.s==429 or .s>=500))|{t,cmd,exit,codes:[.http[].s]}' "$L"

# What scut api calls: each result is a possible missing command
jq -cR 'fromjson?|select(.cmd=="api")|.http[]?|{m,p}' "$L"
```

## Tests

`make lint test`, plus:

- **No text, for each command:** walk the command tree. With a valid token and a fake API, make one run with valid arguments, one run with a marker in each place of a positional argument, and one run with a marker in each non-bool flag that is not in the allowlist. A command that calls the API must reach HTTP in the run with valid arguments, and a write must send its write method. A flag run can stop at validation. Assert that the marker is not in `flags`, `ids`, `cmd` or `http`. A new command or flag is thus covered with no new test. The test has its own copy of the allowlist and of the ID commands, so a change to either must also change the test. The check does not cover `error` and `api`, by the rule above. It also does not cover `p` for the positional argument of `scut api`, but the check of the other fields and of the flags of `scut api` stays.
- **No query:** a search run has its query in no field of the line, on a 200, a 400 and a 503.
- **No token:** plant a fake `sct_` token in an API error, in a usage error and in a transport error, and a secret with characters outside letters and digits. Assert that it is not in the line.
- **File download:** the entry has the host, and no path and no query.
- **Retries:** a run with 503 then 200 gives two `http` entries.
- **Truncation:** long values and long lists are cut at the limits, the result is valid JSON, and a cut does not split a UTF-8 character.
- **Rotation:** a file of 5 MiB rotates. An old `.1` is replaced. Concurrent goroutines keep all lines, and at most one rename occurs for a full file. Run with the race detector. Other tests hold the lock and assert no wait and no rename, call the rename step on a small file and assert no rename, and stop at the hard limit when the rename fails.
- **Broken line:** the next line starts after a newline.
- **Modes and file type:** an existing 0755 directory and 0644 log get 0700 and 0600. A FIFO at the log path does not block.
- **Failure of the log:** with a state directory that cannot be written, stdout, stderr and the exit code are the same as with a working log.
- **`SCUT_LOG=off`:** no file is made.
- **No HOME:** no file is made, and no error occurs.
- **SIGINT:** a cancelled run writes a line with exit 130.

Measure the time that the log adds to a run, and give the figure in the PR body.

## Left to the builder

- The names of Go types and functions.
- The lock target: a lock file, or the directory.
- The Go encoding details of the line, if the field names above stay.
