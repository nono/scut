# scut

- Check with `make lint test`. Raw `go test` skips the Prism contract tests when `node_modules` is missing, and runs with no race detector.
- After a change to a command, a flag or `internal/cmd/genskill/skill_head.md`, run `make skill`. It writes `skills/scut/SKILL.md`, `docs/coverage.md`, `internal/cli/storyfields_gen.go` and the "Not supported" list of `README.md`. Do not edit these by hand.
- To see the shape of a v4 operation or schema, run `go run ./cmd/scut spec <operationId|SchemaName>`. Do not use jq on `api/shortcut.openapi.json`.
- API facts that the spec does not show are in "Verified API facts" of `docs/design/2026-11-04-scut-design.md`. When a live test or a review finds a new fact, add a row.
- Design: `docs/design/2026-11-04-scut-design.md` is v1. A new feature gets its own dated doc in `docs/design/`, with a pointer from the v1 doc.
- The repository is public. Tests and fixtures use fake values (`example-ws`, `Test User`, `00000000-0000-4000-8000-0000000000aa`). Live tests read real values from `SCUT_LIVE_*`.
