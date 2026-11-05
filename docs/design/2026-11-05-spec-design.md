# `scut spec` design

It extends the v1 design in `2026-11-04-scut-design.md`.

## Intent

Agents that work on scut, and agents that use `scut api`, need the shape of a v4 operation: its method, path, parameters, request body and response. The spec (`api/shortcut.openapi.json`) is 1.5 MB. Its body and response schemas have opaque names (`Body1269210`, `Response1269787`), so a lookup with jq follows a chain of `$ref`s by hand. In the first sessions, agents made more than 150 such calls, and some failed. `scut spec <name>` gives the shape in one call.

## Goals

- `scut spec <operationId>` prints the method, the path, the parameters, the request body and the 2xx response of an operation.
- `scut spec <SchemaName>` prints one named schema.
- No token and no API call: the spec is in the binary.

## Non-goals

- Descriptions of fields and parameters. They double the output. They can come later with a flag.
- Error responses (4xx). They have one generic shape.
- A search over operations. `docs/coverage.md` lists the operations that commands call.

## Command

```
scut spec <operationId|SchemaName> [--depth N]
```

- An operationId is tried first, then a schema name. An unknown name exits 3. `valid` is not given: there are 257 operations.
- The path is in the form that `scut api` takes: relative to the workspace (`stories/{story-public-id}`), or absolute when it has no workspace slug (`/api/v4/whoami`). The `workspace-slug` parameter is left out.

Output (JSON):

```json
{
  "operation": "updateStory",
  "method": "PATCH",
  "path": "stories/{story-public-id}",
  "summary": "Update Story",
  "parameters": {"story-public-id": "path integer(int64)", "fields?": "query string"},
  "body": {"archived?": "boolean", "deadline?": "string(date-time)|null"},
  "response": {"entity": "$Story"}
}
```

## Compact schema

- A primitive is its type: `string`, `integer(int64)`, `string(date-time)`. An enum is its values joined by `|`. `|null` marks a nullable value.
- An object is a map of its properties. An optional property has a `?` suffix. An `additionalProperties` schema gives the key `*`.
- An array is a list of one item schema.
- `oneOf` and `anyOf` give `{"oneOf": [...]}`. `allOf` merges the objects.
- A named schema is shown inline up to `--depth` levels (default 2). Below that it is `"$Name"`; run `scut spec Name` to see it. A schema that is already open on the current path is also `"$Name"`, so a cycle ends.
- Generated names (`Body<n>`, `Response<n>`) and the wrappers of requestBodies and responses are always inline. They do not count as a level.

## Embed

The `api` package embeds the spec. The binary grows by 1.5 MB. The parse takes about 20 ms, and only `scut spec` does it.
