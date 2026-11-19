# `scut story history` design

It extends the v1 design in `2026-11-04-scut-design.md` (U13).

## Intent

The Shortcut MCP is removed. During a story run, an agent called its `get_story_history` with `fields=workflow_state`, `order_dir=desc`, `limit=10` to find when the story changed state. scut had no command for it.

## Command

```
scut story history [<story>…] [--fields a,…] [--limit N|all] [--desc]
```

- Story forms and the default story from the git branch are as in `story view`.
- One story calls `getStoryHistory` (`GET stories/{id}/history`). Several stories call `getMultipleStoriesHistory` (`GET stories/history?ids=…`) once: one list, in time order. At most 100 stories, as in `story view`.
- `--fields` is the API attribute filter. It also takes the `story view` names `state`, `type`, `tasks`, `links`, `fields`. scut does not check the names: the API gives 400 with the valid list, and it will track more attributes.
- `--limit` defaults to 25, as the other lists. `--desc` sends `order_dir=desc`.

## Output

One JSON array. Each change:

```json
{"timestamp": "2026-11-17T14:34:59.372Z", "story": 12,
 "actor": {"id": "<uuid>", "mention_name": "test-user"}, "action": "update", "attribute": "workflow_state",
 "removes": [{"id": 8, "name": "In Progress"}], "adds": [{"id": 10, "name": "Done"}]}
```

- `story` only with several stories, and `attribute` only when `--fields` does not name one attribute: else they repeat the request.
- `tx_id` is left out.
- An empty `removes` or `adds`, and a null `actor`, are left out.
- The values are slim entities with names. A member becomes `{id, mention_name}` from the members cache, as in `story view`. When the cache does not have the member (the cache does not refresh on a miss), `mention_name` is null and `name` is the name that the API sent, so the member is not only an ID. An entity with a name (state, epic, team, label, branch) becomes `{id, name}`. Other entities (story link, custom field value, commit) lose `entity_type`, `uri` and `app_url`, and their nested entities follow the same rules. Scalars stay as they are. A description change holds the full text: use `--fields` to leave it out.

## Unknown stories

`getStoryHistory` gives 404 for an unknown story: exit 3. `getMultipleStoriesHistory` leaves an unknown ID out, with no error. When a story of several has no change in the output, scut checks the IDs with one `GET /stories?ids=…&fields=id&filter=include_archived` call, prints the changes, then exits 3 with `no story N`, as `story view` does. When each story has a change, there is no extra call.

## API calls

With a warm cache: 1 call, plus 1 for each more page. Later pages send only the cursor: it carries `fields` and `order_dir` (live test). Several stories add the check call above when a story has no change.

`transport.Doer.ListKey` reads the items of a list from a key other than `entities`: the history list is in `changes`.
