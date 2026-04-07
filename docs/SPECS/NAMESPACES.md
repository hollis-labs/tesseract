# Namespaces Spec — Tiered Namespace Model

Status: active (sprint-2026-05)

## Overview

Namespaces are hierarchical path-like identifiers that group records by ownership, lifecycle, and retention semantics. The tier model formalizes five canonical namespaces tiers with distinct write authority, retention behavior, and promotion rules.

## Canonical Tiers

| Tier | Namespace pattern | Semantics |
|---|---|---|
| `memory` | `user/memory/*` | Curated durable facts; slow-changing; conflict-aware; survives sessions |
| `cache` | `user/cache/*` | Working context; expirable; aggressively trimmed by retention policy |
| `pins` | `user/pins/*` | Always-include; user-asserted hard constraints and persistent preferences |
| `draft` | `app/<id>/draft/*` | App-owned draft content pending promotion to user memory |
| `session` | `app/<id>/session/<task-id>/*` | Ephemeral task-scoped working context; aggressively trimmed |

## Namespace Pattern Rules

- `user/*`: the **user authority** — only `actor=user` may write directly.
- `app/<id>/*`: the **app authority** — only `actor=app:<id>` with matching `client_id=<id>` may write.
- **No app may write directly to `user/*`.** The promotion workflow (`promote.request → promote.approve → promote.apply`) is the only bridge.

## Tier Definitions

### `memory` tier — `user/memory/*`

- **Owner**: `user`
- **Write authority**: `actor=user` only (direct write) or promotion workflow
- **Retention**: indefinite (no expiry); curated by user
- **Revision model**: all revisions retained; head is current canonical value
- **Use cases**: learned user preferences, long-term project facts, curated knowledge
- **Conflict behavior**: new revision always wins; previous revision preserved in history

### `cache` tier — `user/cache/*`

- **Owner**: `user`
- **Write authority**: `actor=user` only
- **Retention**: configurable per-namespace; default 72h; trimmed by maintenance job
- **Revision model**: typically head-only; older revisions trimmed aggressively
- **Use cases**: active working context, recent session state, short-lived retrieved data

### `pins` tier — `user/pins/*`

- **Owner**: `user`
- **Write authority**: `actor=user` only
- **Retention**: indefinite; user-managed
- **Revision model**: head-only; infrequent updates
- **Special behavior**: context packets always prepend pins (`include_pins: true` by default)
- **Use cases**: always-include project constraints, user preferences that must appear in every context window

### `draft` tier — `app/<id>/draft/*`

- **Owner**: `app:<id>`
- **Write authority**: `actor=app:<id>` with `client_id=<id>`
- **Retention**: configurable; default 7 days
- **Revision model**: all revisions retained until promoted or trimmed
- **Use cases**: app-drafted content awaiting user approval for promotion to `user/memory/*`

### `session` tier — `app/<id>/session/<task-id>/*`

- **Owner**: `app:<id>`
- **Write authority**: `actor=app:<id>` with `client_id=<id>`
- **Retention**: short; default 24h; aggressively trimmed
- **Revision model**: head-only; ephemeral working state
- **Use cases**: task-scoped context state, in-progress computation state, handoff data

## Ownership Rules

| Namespace pattern | Required actor | Required client_id | Can promote to user/* |
|---|---|---|---|
| `user/memory/*` | `user` | any | n/a (already user) |
| `user/cache/*` | `user` | any | n/a |
| `user/pins/*` | `user` | any | n/a |
| `app/<id>/draft/*` | `app:<id>` | `<id>` | via promote.request only |
| `app/<id>/session/*` | `app:<id>` | `<id>` | via promote.request only |

**Critical rule**: Apps cannot write to `user/*` directly. A `403 policy_denied` must be returned for any such attempt regardless of token scopes. The promotion workflow is the only bridge.

## Default Policy JSON per Tier

These policies are the defaults for each tier. Individual namespaces can override any field.

### `memory` defaults
```json
{
  "tier": "memory",
  "max_revisions": 0,
  "max_bytes_per_key": 0,
  "allowed_ops": ["write", "promote.request", "promote.apply", "repair", "namespace.register"],
  "required_schema_keys": [],
  "redaction": { "allowed": true, "tombstone_on_delete": true }
}
```

### `cache` defaults
```json
{
  "tier": "cache",
  "retention": "72h",
  "max_revisions": 5,
  "max_bytes_per_key": 65536,
  "allowed_ops": ["write", "repair"],
  "required_schema_keys": [],
  "redaction": { "allowed": false, "tombstone_on_delete": false }
}
```

### `pins` defaults
```json
{
  "tier": "pins",
  "max_revisions": 0,
  "max_bytes_per_key": 16384,
  "allowed_ops": ["write", "repair"],
  "required_schema_keys": [],
  "redaction": { "allowed": true, "tombstone_on_delete": false }
}
```

### `draft` defaults
```json
{
  "tier": "draft",
  "retention": "168h",
  "max_revisions": 10,
  "max_bytes_per_key": 131072,
  "allowed_ops": ["write", "promote.request"],
  "required_schema_keys": [],
  "redaction": { "allowed": false, "tombstone_on_delete": false }
}
```

### `session` defaults
```json
{
  "tier": "session",
  "retention": "24h",
  "max_revisions": 3,
  "max_bytes_per_key": 65536,
  "allowed_ops": ["write"],
  "required_schema_keys": [],
  "redaction": { "allowed": false, "tombstone_on_delete": false }
}
```

## Determining a Namespace's Tier

Given a namespace string, apply the first matching rule:

1. Starts with `user/memory/` → `memory` tier
2. Starts with `user/cache/` → `cache` tier
3. Starts with `user/pins/` → `pins` tier
4. Starts with `app/<id>/draft/` → `draft` tier
5. Starts with `app/<id>/session/` → `session` tier
6. Otherwise → unclassified (no tier-specific defaults applied)

## Lifecycle Summary

```
App writes draft:           app/<id>/draft/<key>
App requests promotion:     promote.request → app/<id>/promotions/<req-id>
User approves:              promote.approve → user/promotions/<appr-id>
User applies:               promote.apply  → user/memory/<key>  (canonical location)

Session context lifecycle:
App writes session:         app/<id>/session/<task-id>/<key>
Session expires or trimmed: maintenance trim removes old records
```

## CLI/API Mapping

- Register a namespace: `context namespace register --namespace <ns> --owner-type user|app --owner-id <id>`
- Show namespace policy: `context namespace show --namespace <ns>`
- API: `POST /v1/namespaces/register`, `GET /v1/namespaces/get`
