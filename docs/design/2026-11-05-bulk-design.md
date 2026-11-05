# Bulk update design (G12)

It extends the v1 design in `2026-11-04-scut-design.md`.

## Intent

G12 asks for: read several stories by ID in one command, and apply links and sub-tasks from a list. The evidence: 6 sub-task calls in a row (sc-5428), 12 link calls in one session, and a search on `id:2935,5050,…` to read 10 stories.

v1 has two of the three:

- `story view <story>…` reads up to 100 stories in one `GET /stories?ids=…` call.
- `story link A blocks B relates-to C` makes several links in one command.

The missing part is the same change on several stories. Each sub-task call of sc-5428 was one `story update <story> --parent 5428`.

## Goals

- `scut story update <story>… [flags]` applies the same change to each story given.
- Two stories or more use `PATCH /stories/bulk`: one call for up to 100 stories.
- No story is changed when a story or a name does not resolve.

## Non-goals

- Bulk create (`POST /stories/bulk`). No transcript shows several creates in a row of the same shape.
- A JSON batch file (the Linearis lesson). The command line covers the cases seen. It can come later.
- Bulk links (`POST /story-links/bulk`). `story link` keeps one call per link: the bulk call gives no result per item, so one bad pair hides the result of the others, and the 422 of a duplicate link (a success for scut) fails the whole call.
- Bulk delete.

## Command

```
scut story update <story>… [--state] [--workflow] [--epic] [--requester] [--add-owner …] [--remove-owner …]
                  [--field "Name=Value"]… [--archive|--unarchive] [--parent <story>|--no-parent]
```

- One story: no change. One `PATCH /stories/{id}`, and the output is the story object, with the description.
- Two stories or more, after repeats are removed: the bulk path below. The output is an array of the stories, in argument order, without the description, as a list of `story search` is.
- `--name` and `--description-file` need a single story: with more, exit 2. The same name or description on several stories is a mistake more often than not.
- `--parent` names a story in the list: exit 2. A story cannot be its own parent.

## Bulk path

1. Check the flags (before the token, as each command does).
2. Read the stories: `GET /stories?ids=…&fields=id,workflow&filter=include_archived`, in pages of 100. A story that is not there: exit 3, and `valid` is not set; the error names the missing IDs. No write is made.
3. Resolve each name once: epic, members, fields. `--state`: with `--workflow`, one state ID; without it, one state ID for each workflow of the stories, from step 2. A state that is not in a workflow of the stories: exit 3, no write.
4. Write in chunks of 100 stories, in argument order: `PATCH /stories/bulk` with `{"stories": [{"id": …, …}, …]}` and `limit` = the chunk size, so the reply lists each story of the chunk. Each item has the same keys; only `workflow_state_id` can differ between items.
5. Check each reply: it must list each story of the chunk, and `checkParent` applies to each story, as for one story.

## Failures

The API does not say if a bulk write is atomic, and its errors (400, 404, 422) have no result per item. So:

- A failed chunk is reported as not known. The error has the HTTP status and the API body, as other errors do. Its `suggestion` names the stories of that chunk: "these stories may be partly changed: read them with `scut story view <ids>`".
- The stories of the chunks before it are printed on stdout, as an array, before the error. The chunks after it are not sent.
- A write is not retried (as in v1: only `GET` is). A 5xx or a timeout exits 5.
- A reply that leaves out a story of the chunk: exit 1, with the IDs. The write is saved.

## Calls

For N stories: 1 read per 100 stories, the name lookups of the cache, and 1 write per 100 stories. Six sub-tasks: 2 calls, not 6.

## Tests

- Unit tests with the fake API: the bulk body, chunks of 100, argument order, repeats, the state per workflow, a missing story (no write), a failed chunk (stdout has the chunks before it; the suggestion names the chunk), a reply that leaves out a story, the parent check, the flags that need one story.
- A Prism contract test of `PATCH /stories/bulk`.
- Not tested: atomicity. Add a row to "Verified API facts" when a live test shows it.
