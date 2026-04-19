---
name: audit
description: The audit log — emitting events from service-internal code and querying them via context_audit.
scope_hint: none
related: [revisions, promotion]
---

# Audit

Every write and every promotion stage records a structured audit event. The audit log is append-only: no tool mutates it. Use `context_audit` to query it — no scope required.

## Emitting (for service-internal code)

All audit writes go through helper methods on `*contextstore.Store`. Handlers
and CLI commands do NOT construct `AuditEvent` values directly, and the raw
emit path (`recordAuditEvent`) is unexported. The helpers live in
`internal/contextstore/audit_emit.go` and the canonical event-type names live
in `internal/contextstore/audittypes.go`.

Helpers (one per semantic class):

- `EmitWrite`, `EmitTypedWrite`, `EmitStatusPromote`, `EmitStatusDeprecate`
- `EmitSessionSnapshot`, `EmitPacket`
- `EmitBulkIngest`, `EmitChunkedIngest`
- `EmitPromote(ctx, eventType, ...)` — accepts any of the promote-stage constants
- `EmitMaintenance(ctx, eventType, ...)` — accepts `EventMaintenanceTrim` or `EventMaintenanceCompact`

Helpers structured-log on error via `log/slog`; callers may continue to
discard the error (`_ = store.EmitWrite(...)`), which is the project
convention for audit emits.

## Querying

`context_audit` returns events newest-first, with filters:

- `namespace` — exact namespace match (not a glob).
- `event_type` — exact event type match (see below).
- `limit` — max events per call (default 10, max 25).
- `cursor` — integer event ID from the previous response's `next_cursor`; the server returns events with `id < cursor`.

The response envelope carries `next_cursor` when more results are available; omit it (or pass `0`) on the first call.

## Event types

Event types actually emitted by the MCP surface:

- `write` — a `context_write` succeeded.
- `promote.request` — a `context_promote_request` was opened.
- `promote.approve` — a pending request moved to `approved`.
- `promote` — an approved request was applied (terminal). Also emitted by direct `memory_promote` and typed status-promote flows on the HTTP side.
- `typed_write` — a typed-view write (`context_typed_write`).
- `status_promote` / `status_deprecate` — `context_status_promote` / `context_status_deprecate`.
- `session_snapshot` — `context_session_snapshot`.
- `bulk_ingest` / `chunked_ingest` — bulk/chunked write tools.

Not all write paths emit audit events today. Notably, `memory_write`,
`memory_deprecate`, and `knowledge_write` do not record audit events —
reconstruct memory/knowledge history via `memory_history` / `knowledge_history`
instead. (Follow-up: `CW-20260419-0040` will land these emits using the
helpers above.)

## Envelope shape

Each returned event carries:

- `id` — numeric event ID (monotonic; use it as the next `cursor`).
- `event_type` — one of the types above.
- `actor` — logical identity (`user`, `app:<id>`, `mcp-agent`, …).
- `namespace`, `key` — record identity.
- `revision` — numeric revision number for the affected record.
- `record_id` — ULID of the record (when known).
- `created_at` — RFC3339 timestamp.

Metadata is stored on the row but not projected into the MCP response today.

## Common queries

- "What wrote into `user/chrispian/memory` recently?" — set `namespace: user/chrispian/memory`, page with `cursor`. Note: `memory_write` does not audit, so this surfaces context writes only.
- "What promotions are pending?" — prefer `context_promote_list` (domain-aware); `context_audit` gives the raw event stream.
- "When was revision X applied?" — filter `event_type: promote` and walk until `record_id` or `revision` matches; there is no direct revision index.
