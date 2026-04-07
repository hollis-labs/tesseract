---
id: "feat-2026-02-21-nanite-backend-adapter"
type: "prd"
status: draft
project: volon
tags: ["feature"]
priority: B
created_at: "2026-02-21"
updated_at: "2026-02-21"
---

# Nanite Backend Adapter — PRD

## Summary

The Nanite backend adapter extends volon's storage layer with a second backend
so that developers who use Nanite as their personal task system can route
volon-generated tasks directly into Nanite. The adapter is activated by a
single `volon.yaml` flag and is transparent to all volon skill protocols.

## Agent / Developer Flow

| Step | Action | Expected output |
|---|---|---|
| 1 | Set `storage.backend: nanite` in `volon.yaml`; set `NANITE_TOKEN` env var | — |
| 2 | Run `/task-create "Fix auth bug" priority=A` | `Created: NANITE-<id>` + item appears in Nanite inbox |
| 3 | Run `/task-list status=todo` | Markdown table of volon-tagged Nanite items with volon field mapping |
| 4 | Run `/task-update NANITE-<id> status=done message="Fixed in PR #42"` | `Updated: NANITE-<id>` + item body updated in Nanite |
| 5 | Revert `storage.backend: files` | All task operations return to file backend; Nanite items unaffected |

## Success Criteria

| Criterion | Measurement |
|---|---|
| Backend dispatch | `task-create` skill reads `storage.backend` from config in Step 1 |
| Round-trip create | Created Nanite item contains all volon frontmatter fields |
| Round-trip list | `/task-list` output identical structure for both backends |
| Round-trip update | Updated item in Nanite shows new status + message appended |
| Error handling | Unreachable Nanite → clean error; no partial writes |
| Isolation | `storage.backend: files` unchanged behaviour (regression-free) |

## Failure Flows

| Condition | Behaviour |
|---|---|
| `NANITE_TOKEN` not set | `ERROR: NANITE_TOKEN env var not set — Nanite backend requires authentication.` |
| Nanite API returns 4xx | `ERROR: Nanite API error <code> — <message>.` |
| Nanite API unreachable (timeout) | `ERROR: Nanite backend unavailable — connection timed out.` |
| Item not found for `/task-update` | `ERROR: task <id> not found in Nanite.` + hint to run `/task-list` |
| `storage.backend` value unrecognised | `ERROR: unknown storage backend '<value>' — expected files or nanite.` |

## Decisions

- Adapter is a new dispatch block in each task skill's Step 1 (read config); no new files required.
- Nanite item body stores the full volon frontmatter block as a fenced YAML block for round-trip fidelity.
- `tag_prefix` (default: `volon/`) distinguishes volon-owned items from other Nanite items.

## Open questions

- Does Nanite support partial-update (PATCH) or only full-replace (PUT)?

## Evidence

- Input: artifacts/requirements/nanite-backend-adapter.md
- Workflow: workflow-new-feature "Nanite backend adapter" — 2026-02-21
