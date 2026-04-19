---
name: promotion
description: The app->user promotion workflow - request, approve, apply - and the memory_promote shortcut.
scope_hint: none
related: [namespaces, memory]
---

# Promotion

Apps cannot write directly into `user/*` namespaces. The user owns that surface; apps propose, the user (or an agent acting as the user) approves. Promotion is the one-way channel that moves app-authored content across that boundary.

## Three-stage context promotion

For the generic context domain, promotion is a three-stage append-only workflow. Each stage requires its own scope:

1. **Request** - `context_promote_request` (scope: `promote.request`). Opens a pending request. Required: `source_namespace`, `source_key`, `target_namespace`, `target_key`. Optional: `reason`, `actor`. The request is stored in `app/mcp-agent/promotions` (or equivalent app namespace); the head revision carries current status.
2. **Approve** - `context_promote_approve` (scope: `promote.approve`). Moves a `pending` request to `approved`. Default actor is `user`. Writes an approval record to `user/promotions` and appends an updated request revision.
3. **Apply** - `context_promote_apply` (scope: `promote.apply`). Copies the source record's payload into the target namespace as a new revision, then marks the request `applied`. Terminal state; not reversible.

Listing: `context_promote_list` with `status=pending|approved|applied|all` (default `pending`). Read-only, no scope required.

Every stage emits an audit event (`promote.request`, `promote.approve`, `promote`) - reconstruct the chain with `context_audit`.

## memory_promote (the shortcut)

`memory_promote` is a single-call path for the memory domain - not a shortcut through the three-stage `context_promote_*` flow, but a separate tool with its own invariants:

- **Scope:** `memory:write` (not `promote.*`).
- **Direction:** `session` scope -> `user` or `project` scope. Source namespace MUST be session-scoped (e.g. `user/{id}/session/{sid}/memory`); target MUST be user-scoped or project-scoped.
- **Side effect:** the source revision is deprecated after a promoted revision is written to the target namespace. Keyed memories with the same key in the target get an explicit `supersedes` link.
- **Trigger:** the promoted revision is stamped with `trigger=promotion`; status defaults to `reviewed`.

Use it for same-agent, same-session elevations (session scratch -> durable user memory). Cross-ownership moves between apps and user still go through `context_promote_*`.

## Why this exists

- **User sovereignty.** User memory is the user's; apps shouldn't inject or overwrite without a handshake.
- **Audit trail.** Every transition is logged. "When did this enter user memory, and who approved it?" is always answerable.
- **Deferred decisions.** Approve and apply are split so approvals can queue and applies can batch.

## Adjacent tool

`context_status_promote` is unrelated to cross-namespace promotion - it transitions a record's status (`draft` -> `reviewed` -> `canonical`) in place and stays in its original namespace. Reach for it when the lifecycle move is ownership-internal.
