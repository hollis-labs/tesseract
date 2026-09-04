# Promotion Workflow Spec

Status: implemented (sprint-2026-07)

## Overview

The promotion workflow governs how app-owned records move into user-owned namespaces. Apps **propose**, users **approve**, the service **applies**.

No app token can bypass the approval step to write directly to `user/*`. The legacy `POST /v1/context/promote` endpoint is deprecated (returns 410 Gone).

---

## Lifecycle

```
[App] POST /v1/context/promote/request  → status: pending
[User] POST /v1/context/promote/approve → status: approved
[User] POST /v1/context/promote/apply   → status: applied
```

### State transitions

| State | Who can act | Endpoint |
|---|---|---|
| `pending` | Any user actor | `promote/approve` |
| `approved` | Any user actor | `promote/apply` |
| `applied` | — (terminal) | — |
| `rejected` | — (future; not in MVP) | — |

---

## Record Types

### `promote.request`

**Namespace**: `app/<client_id>/promotions`
**Key**: `<request-id>` (e.g., `req-abc123`)

```json
{
  "type": "promote.request",
  "request_id": "req-abc123",
  "source_namespace": "app/myapp/draft",
  "source_key": "pref-v2",
  "source_revision_id": "rec_1234567890",
  "source_checksum": "sha256hex",
  "target_namespace": "user/memory/preferences",
  "target_key": "user-prefs",
  "reason": "Updated user preferences after confirmation",
  "proposed_summary": "Brief description of what changed",
  "status": "pending|approved|applied",
  "requested_at": "2026-02-27T10:00:00Z",
  "requested_by": "app:myapp"
}
```

A new revision is written whenever status changes (append-only mutation). The HEAD revision carries the current status.

### `promote.approve`

**Namespace**: `user/promotions`
**Key**: `<approval-id>` (e.g., `appr-xyz456`)

```json
{
  "type": "promote.approve",
  "approval_id": "appr-xyz456",
  "request_id": "req-abc123",
  "request_namespace": "app/myapp/promotions",
  "approved_at": "2026-02-27T10:05:00Z",
  "approved_by": "user",
  "notes": "optional approval notes"
}
```

---

## Namespace Conventions

- Apps write requests into their own `app/<client_id>/promotions` namespace.
- Users write approvals into `user/promotions`.
- Apps **cannot** write into `user/*` — that invariant is enforced by the policy engine.
- The promotion target (`target_namespace`) must be a `user/*` namespace.

---

## API Endpoints

### `POST /v1/context/promote/request`

Creates a promotion request. Requires `actor` to be an app identifier.

**Request**:
```json
{
  "source_namespace": "app/myapp/draft",
  "source_key": "pref-v2",
  "source_revision_id": "rec_1234567890",
  "target_namespace": "user/memory/preferences",
  "target_key": "user-prefs",
  "reason": "User confirmed preference",
  "proposed_summary": "Stores user's confirmed theme preference"
}
```

**Response 200**:
```json
{ "request_id": "req-abc123", "status": "pending" }
```

---

### `POST /v1/context/promote/approve`

Approves a pending request. Requires `actor=user`.

**Request**:
```json
{ "request_id": "req-abc123", "notes": "Looks correct" }
```

**Response 200**:
```json
{ "approval_id": "appr-xyz456", "request_id": "req-abc123", "status": "approved" }
```

---

### `POST /v1/context/promote/apply`

Applies an approved request. Requires `actor=user`.

**Request**:
```json
{ "request_id": "req-abc123" }
```

**Response 200**:
```json
{ "record_id": "rec_new", "request_id": "req-abc123", "approval_id": "appr-xyz456" }
```

---

## CLI Commands

```bash
context promote request --source-namespace NS --source-key K --target-namespace TNS --target-key TK --reason R
context promote list [--status pending|approved|applied|all]
context promote approve <request-id> [--notes N]
context promote apply <request-id>
context promote accept <request-id> [--notes N]   # approve + apply in sequence
```

---

## Audit Trail

Every step emits an audit event. The event_type is the same whichever door
opened the promotion — HTTP, MCP and CLI all emit these three names:
- `promote.request` — includes request_id, source, target, actor
- `promote.approve` — includes request_id, approval_id, actor
- `promote` — includes request_id, approval_id, new record_id, actor

Do not confuse these with the identically-spelled capability scopes
(`promote.request`, `promote.approve`, `promote.apply`) or with the `type`
discriminator on the stored request and approval payloads. Three separate
namespaces that happen to share strings; see `internal/contextstore/audittypes.go`.

---

## References

- Tier model: `docs/SPECS/NAMESPACES.md`
- Storage model: `docs/SPECS/STORAGE.md`
- API reference: `docs/SPECS/API.md`
