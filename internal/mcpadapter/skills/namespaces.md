---
name: namespaces
description: Canonical namespace patterns, ownership, and the actor/client_id matrix.
scope_hint: none
related: [start-here, promotion, memory, knowledge]
---

# Namespaces

Tesseract organizes every record under a **namespace** — a path-like string encoding ownership, tier, and (for memory) user/project/session scope. The namespace is authoritative: it decides who may write, who may read, and whether promotion is required.

## Canonical tier patterns

Five tiers ship today. Each tier has a fixed owner — ownership isn't orthogonal to tier.

| Tier | Pattern | Owner | Purpose |
|---|---|---|---|
| `memory` | `user/memory/*` | user | Curated durable facts; survives sessions |
| `cache` | `user/cache/*` | user | Working context; expirable |
| `pins` | `user/pins/*` | user | Always-include; user-asserted constraints |
| `draft` | `app/<id>/draft/*` | app | App-owned drafts awaiting promotion |
| `session` | `app/<id>/session/<task-id>/*` | app | Ephemeral task-scoped context |

## Memory domain — typed, shallow + faceted

The memory domain (`memory_write`, `memory_recall`, `memory_get`, etc.) uses a **fixed-depth typed namespace**. The `{type}` segment is the ONLY structural level; anything finer is expressed as tags / facets on the revision, not as deeper namespace segments.

```
user/{user_id}/memory/{type}                       # user scope, 4 seg
user/{user_id}/project/{project_id}/memory/{type}  # project scope, 6 seg
user/{user_id}/session/{session_id}/memory/{type}  # session scope, 6 seg
```

**Allowed types** (config-driven allowlist, locked CW-20260519-0029):
- `decisions` — design calls with rationale
- `feedback` — guidance about how to approach work
- `followups` — work deliberately deferred with context
- `learnings` — distilled understanding from a session
- `limitations` — known constraints or preserved tech debt
- `notes` — catch-all default bucket for everything else
- `outcomes` — what happened / what was true after the work
- `references` — pointers to where information lives

Adding a type = one-line code change in `internal/memory/namespaces.go`. Unknown types are rejected at write time with a clear error.

**Recall prefix matching.** Recall accepts both the typed form (exact match against one sub-namespace) AND the legacy / prefix form `user/{id}/memory` — the prefix form matches every typed sub-namespace under that scope, so "give me all my user memory" still works after the split. Project/session scopes have the same prefix behavior (`user/{id}/project/{pid}/memory`, `user/{id}/session/{sid}/memory`). The explicit wildcard `user/{id}/memory/*` is also accepted.

**Promote preserves type.** Promoting a session memory to user/project scope (`memory_promote`) requires source and target to carry the SAME `{type}` segment. Cross-type promotion is rejected — re-classification is a different operation, not a scope change.

Bare `user/memory/*` from the tier spec is rejected by the memory parser — use the full typed form when writing memory revisions.

## Two authority rules

- `user/*` — only `actor=user` may write directly.
- `app/<id>/*` — only `actor=app:<id>` with matching `client_id=<id>` may write.
- **Apps cannot write to `user/*`.** The only bridge is promotion (`tesseract_skills promotion`).

## Actor and client_id matrix

Every write carries an **actor** (logical identity, e.g. `user`, `app:nanite`) and a **client_id** (the API token's identity). Mismatches fail at the write boundary, before anything touches the store.

## Common mistakes

- Writing to `user/*` from an app token — blocked. Route through promotion.
- Omitting the `user_id` when calling `memory_write` — use `user/<who>/memory/<type>`, not bare `user/memory`.
- Omitting the `{type}` segment — `user/<who>/memory` parses for *recall* (prefix form) but is REJECTED on *write*. Writes must include a valid type.
- Assuming cache semantics for `memory/*` — memory is durable. Use `user/cache/*` for disposable data.
- Trying `app/<name>/memory/*` — apps don't own memory-tier records. Use `app/<id>/draft/*` or promote into user memory.
