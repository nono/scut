# `scut epic update` design

It extends the v1 design in `2026-11-04-scut-design.md`.

## Intent

The Shortcut MCP is removed. Agents called its `update_epic` to add an owner and to replace the description of an epic. scut had reads and a comment on an epic, but no change.

## Command

```
scut epic update <epic> [--name] [--state] [--add-owner …] [--remove-owner …] [--description-file f|-]
                 [--archive|--unarchive]
```

- The epic is an ID or an exact name, as in `epic view`. One epic only.
- The flags have the names and the behaviour of the same flags of `story update`. `--state` is a state of the epic workflow, not of a story workflow.
- One `PATCH epics/{id}` (`updateEpic`) with only the given fields: `name`, `description`, `epic_state_id`, `owner_ids_add`, `owner_ids_remove`, `archived`. An owner flag never replaces the set.
- No flag, `--archive` with `--unarchive`, an empty description or a blank epic exits 2 before a call.
- All names resolve before the `PATCH`. A name that does not resolve exits 3 with `valid`, and there is no write.
- The output is the epic, in the shape of `epic view`.

## Name resolution

- Members (`@me`, mention name, email, UUID) use the resolver of `story update`.
- `--state` reads `GET epic-workflow/states` (`listEpicWorkflowStates`), all pages. Epic states are not cached, as epics: `--state` adds 1 call. The match is exact and case-sensitive.

## Tests

Fake API tests for each flag, each usage error and each name miss. A Prism contract test sends all flags.
