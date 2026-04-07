---
id: "feat-2026-02-21-nanite-backend-adapter"
type: "requirements"
status: draft
project: volon
tags: ["feature"]
priority: B
created_at: "2026-02-21"
updated_at: "2026-02-21"
---

# Nanite Backend Adapter — Requirements

## Summary

The Nanite backend adapter adds an alternative task storage path to volon so
that task operations are routed to the user's Nanite instance when
`storage.backend: nanite` is set in `volon.yaml`. The file backend must
remain the default and must be unaffected by this change. Success is defined
as all three task skills (`task-create`, `task-list`, `task-update`) operating
correctly against a live Nanite instance without modification to their SKILL.md
protocols beyond a backend-dispatch step.

## Acceptance Criteria

1. `volon.yaml` accepts `storage.backend: nanite` without error when the adapter is installed.
2. `/task-create` with `storage.backend: nanite` creates a new item in the Nanite inbox
   and returns the assigned Nanite item ID as the task ID.
3. `/task-list` with `storage.backend: nanite` returns only volon-tagged Nanite items,
   formatted identically to the file-backend table output.
4. `/task-update <id>` with `storage.backend: nanite` locates the item by ID, updates
   status/priority fields, and appends the message to the item body.
5. If Nanite is unreachable: each skill outputs a clear error
   (`ERROR: Nanite backend unavailable — <reason>`) and stops without corrupting state.
6. Switching `storage.backend` from `nanite` back to `files` does not destroy existing
   file-backend tasks.
7. The file backend is unaffected when `storage.backend: files` (default behaviour
   preserved exactly).

## Decisions

- Nanite connection config lives under `storage.nanite` in `volon.yaml`
  (e.g., `vault`, `api_url`, `tag_prefix`).
- Task IDs in Nanite mode follow Nanite's native ID scheme; the volon canonical
  ID is stored as a tag or in the item body.
- Authentication credential stored as environment variable, not in `volon.yaml`,
  to avoid committing secrets.

## Open questions

- Which Nanite API version should the adapter target?
- Is there a local Nanite MCP server the adapter can call, or does it use HTTP?

## Evidence

- Input: artifacts/ideas/nanite-backend-adapter.md
- Workflow: workflow-new-feature "Nanite backend adapter" — 2026-02-21
