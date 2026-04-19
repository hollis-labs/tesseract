---
name: namespaces
description: Canonical namespace patterns, ownership, and the actor/client_id matrix.
scope_hint: none
related: [start-here, promotion, memory, knowledge]
---

# Namespaces

Vanta organizes every record under a **namespace** — a path-like string encoding ownership, tier, and (for memory) user/project/session scope. The namespace is authoritative: it decides who may write, who may read, and whether promotion is required.

## Canonical tier patterns

Five tiers ship today. Each tier has a fixed owner — ownership isn't orthogonal to tier.

| Tier | Pattern | Owner | Purpose |
|---|---|---|---|
| `memory` | `user/memory/*` | user | Curated durable facts; survives sessions |
| `cache` | `user/cache/*` | user | Working context; expirable |
| `pins` | `user/pins/*` | user | Always-include; user-asserted constraints |
| `draft` | `app/<id>/draft/*` | app | App-owned drafts awaiting promotion |
| `session` | `app/<id>/session/<task-id>/*` | app | Ephemeral task-scoped context |

## Memory domain — stricter form

The memory domain (`memory_write`, `memory_recall`, `memory_get`, etc.) validates against a narrower pattern. It requires an explicit user_id segment and ends in `/memory`:

```
user/{user_id}/memory
user/{user_id}/project/{project_id}/memory
user/{user_id}/session/{session_id}/memory
```

Bare `user/memory/*` from the tier spec is rejected by the memory parser — use the full form when writing memory revisions.

## Two authority rules

- `user/*` — only `actor=user` may write directly.
- `app/<id>/*` — only `actor=app:<id>` with matching `client_id=<id>` may write.
- **Apps cannot write to `user/*`.** The only bridge is promotion (`vanta_skills promotion`).

## Actor and client_id matrix

Every write carries an **actor** (logical identity, e.g. `user`, `app:nanite`) and a **client_id** (the API token's identity). Mismatches fail at the write boundary, before anything touches the store.

## Common mistakes

- Writing to `user/*` from an app token — blocked. Route through promotion.
- Omitting the `user_id` when calling `memory_write` — use `user/<who>/memory`, not bare `user/memory`.
- Assuming cache semantics for `memory/*` — memory is durable. Use `user/cache/*` for disposable data.
- Trying `app/<name>/memory/*` — apps don't own memory-tier records. Use `app/<id>/draft/*` or promote into user memory.
