---
name: namespaces
description: 5-tier namespace model, ownership rules, and the actor/client_id matrix.
scope_hint: none
related: [start-here, promotion, memory, knowledge]
---

# Namespaces

Vanta organizes every record under a **namespace** — a path-like string that encodes ownership and tier. Namespaces are authoritative: they decide who can write, read, and promote.

## Five tiers

- `memory/*` — durable agent/user memory. Append-only; recall-friendly.
- `cache/*` — ephemeral, disposable; subject to eviction.
- `pins/*` — high-priority records kept at the top of context packets.
- `draft/*` — in-progress content awaiting promotion.
- `session/*` — per-session scratch.

## Two ownership domains

- `user/*` — user-owned. Apps cannot write here directly; must go through **promotion** (`vanta_skills promotion`).
- `app/*` — app-owned. Freely writable by the owning app.

Common combinations:
- `user/<who>/memory` — durable user memory.
- `app/<name>/session/<id>` — session scratch for an app.
- `user/<who>/knowledge/<topic>` — user knowledge domain entries.

## Actor and client_id matrix

Every write carries an **actor** (logical identity of the writer, e.g. `claude`, `indexer`) and a **client_id** (the API token's identity). The namespace policy enforces which actor/client combinations may write to which namespaces.

Register a namespace policy with `context_namespace_register`; inspect with `context_namespace_show`.

## Common mistakes

- Writing to `user/*` from an app token — blocked. Use `context_promote_request` → `approve` → `apply`.
- Assuming cache semantics for `memory/*` — memory is durable. Use `cache/*` for disposable data.
- Forgetting the leading owner segment — `memory/foo` is ambiguous; always `user/<who>/memory/foo` or `app/<name>/memory/foo`.
