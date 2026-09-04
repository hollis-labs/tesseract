---
name: promotion
description: The app->user promotion workflow - request, approve, apply - and the memory_promote shortcut.
scope_hint: none
related: [namespaces, memory]
---

# Promotion

Apps cannot write directly into `user/*` namespaces. The user owns that surface; apps propose, the user (or an agent acting as the user) approves. Promotion is the one-way channel that moves app-authored content across that boundary.

## Three-stage context promotion

For the generic context domain, promotion is a three-stage append-only workflow on one tool, `context_promote`. The `stage` argument names the stage AND selects the scope checked for it, so holding one stage's scope grants that stage only. There is no default stage: an absent or unrecognized `stage` is a `validation_error` and authorizes nothing.

1. **Request** - `context_promote` with `stage: "request"` (scope: `promote.request`). Opens a pending request. Required: `source_namespace`, `source_key`, `target_namespace`, `target_key`. Optional: `reason`, `actor`. The request is stored in `app/mcp-agent/promotions` (or equivalent app namespace); the head revision carries current status.
2. **Approve** - `context_promote` with `stage: "approve"` (scope: `promote.approve`). Moves a `pending` request to `approved`. Required: `request_id`. Optional: `notes`, `actor` (default `user`). Writes an approval record to `user/promotions` and appends an updated request revision.
3. **Apply** - `context_promote` with `stage: "apply"` (scope: `promote.apply`). Required: `request_id`. Copies the source record's payload into the target namespace as a new revision, then marks the request `applied`. Terminal state; not reversible.

### Worked, on both surfaces

The three MCP calls, in order. Each is a separate call under a separate scope; the `request_id` in stages 2 and 3 comes from stage 1's response.

```json
{
  "stage": "request",
  "source_namespace": "app/my-agent/draft/notes",
  "source_key": "sqlite-findings",
  "target_namespace": "user/chrispian/memory/learnings",
  "target_key": "sqlite.findings",
  "reason": "Measured WAL vs DELETE; the user asked for this to survive the session.",
  "actor": "app:my-agent"
}
```

```json
{"stage": "approve", "request_id": "<from stage 1>", "notes": "confirmed with the user", "actor": "user"}
```

```json
{"stage": "apply", "request_id": "<from stage 1>", "actor": "user"}
```

The HTTP peers are three routes rather than one tool with a `stage` argument. See `tesseract_skills start-here` for `$TESSERACT_URL` / `$TESSERACT_TOKEN`.

```bash
curl -sS -X POST "$TESSERACT_URL/v1/context/promote/request" \
  -H "Authorization: Bearer $TESSERACT_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "client_id": "my-agent",
    "actor": "app:my-agent",
    "source_namespace": "app/my-agent/draft/notes",
    "source_key": "sqlite-findings",
    "target_namespace": "user/chrispian/memory/learnings",
    "target_key": "sqlite.findings",
    "reason": "Measured WAL vs DELETE; the user asked for this to survive the session."
  }'

curl -sS -X POST "$TESSERACT_URL/v1/context/promote/approve" \
  -H "Authorization: Bearer $TESSERACT_TOKEN" -H "Content-Type: application/json" \
  -d '{"actor": "user", "request_id": "<from the request response>", "notes": "confirmed with the user"}'

curl -sS -X POST "$TESSERACT_URL/v1/context/promote/apply" \
  -H "Authorization: Bearer $TESSERACT_TOKEN" -H "Content-Type: application/json" \
  -d '{"actor": "user", "request_id": "<from the request response>"}'
```

Two fields exist only on the HTTP request route and have no MCP argument: `source_revision_id` (pin the promotion to a specific revision instead of the current head) and `proposed_summary`. These routes reject unknown fields, so do not send MCP-only spellings the other way.

Listing: `context_promotion_list` with `status=pending|approved|applied|all` (default `pending`). Read-only, no scope required. Over HTTP, reconstruct the same picture from `GET /v1/context/audit?event_type=promote.request`.

Every stage emits an audit event (`promote.request`, `promote.approve`, `promote`) - reconstruct the chain with `context_audit_list`. These three names do not vary by door: a promotion opened over HTTP, over MCP or from the CLI lands under the same `event_type`, so one filter per stage catches all of them. (Before v0.9 the HTTP handlers wrote `promote.request.created` / `promote.request.approved` and the CLI wrote nothing at request and approve; both are fixed, and no audit row was ever persisted under the retired spellings.)

## memory_promote (the shortcut)

`memory_promote` is a single-call path for the memory domain - not a shortcut through the three-stage `context_promote` flow, but a separate tool with its own invariants:

- **Scope:** `memory:write` (not `promote.*`).
- **Direction:** `session` scope -> `user` or `project` scope. Source namespace MUST be session-scoped (e.g. `user/{id}/session/{sid}/memory`); target MUST be user-scoped or project-scoped.
- **Side effect:** the source revision is deprecated after a promoted revision is written to the target namespace. Keyed memories with the same key in the target get an explicit `supersedes` link.
- **Trigger:** the promoted revision is stamped with `trigger=promotion`; status defaults to `reviewed`.

Use it for same-agent, same-session elevations (session scratch -> durable user memory). Cross-ownership moves between apps and user still go through `context_promote`.

Worked, on both surfaces:

```json
{
  "source_namespace": "user/chrispian/session/2026-04-19:backend/memory/learnings",
  "source_memory_id": "01HXA...",
  "target_namespace": "user/chrispian/memory/learnings",
  "actor_agent_id": "claude",
  "actor_version": "opus-5"
}
```

```bash
curl -sS -X POST "$TESSERACT_URL/v1/memory/promote" \
  -H "Authorization: Bearer $TESSERACT_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "source_namespace": "user/chrispian/session/2026-04-19:backend/memory/learnings",
    "source_memory_id": "01HXA...",
    "target_namespace": "user/chrispian/memory/learnings",
    "actor_agent_id": "claude",
    "actor_version": "opus-5"
  }'
```

The two shapes are identical here — this call has no nested fields to flatten. Note the `{type}` segment (`learnings`) repeating on both sides: that is enforced, not conventional. `source_memory_id` is a `memory_id`, which every recall result carries under `revision.memory_id` — it is not a `revision_id`.

## Why this exists

- **User sovereignty.** User memory is the user's; apps shouldn't inject or overwrite without a handshake.
- **Audit trail.** Every transition is logged. "When did this enter user memory, and who approved it?" is always answerable.
- **Deferred decisions.** Approve and apply are split so approvals can queue and applies can batch.

## Adjacent tool

`context_status_set` is unrelated to cross-namespace promotion - it transitions a record's status (`draft` -> `reviewed` -> `canonical`, or straight to `deprecated`) in place and stays in its original namespace. Reach for it when the lifecycle move is ownership-internal.

```json
{"namespace": "app/my-agent/specs", "key": "task-001", "status": "reviewed", "actor": "user"}
```

Omit `status` entirely to advance one step along `draft -> reviewed -> canonical`.

**The two surfaces disagree on the name of that argument, and this is the one place in the write surface where they do.** MCP calls it `status`; `POST /v1/context/status/promote` calls it `to_status`:

```bash
curl -sS -X POST "$TESSERACT_URL/v1/context/status/promote" \
  -H "Authorization: Bearer $TESSERACT_TOKEN" -H "Content-Type: application/json" \
  -d '{"actor": "user", "namespace": "app/my-agent/specs", "key": "task-001", "to_status": "reviewed"}'
```

Sending `to_status` to `context_status_set` is a `validation_error` naming `status`, deliberately: an ignored `to_status` would leave `status` empty, which means "advance one step", so a record at `draft` would quietly land on `reviewed` when you asked for `canonical` — and report success.

Deprecation is asymmetric between the doors too. `POST /v1/context/status/deprecate` emits a `status_deprecate` audit event; `context_status_set` with `status: "deprecated"` emits none. See `tesseract_skills audit`.
