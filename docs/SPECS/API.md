# HTTP API

Status: implemented public-preview contract.

The HTTP server exposes JSON routes under `/v1/` and the embedded web UI at
`/`. The authoritative route registry is `apiRoutes` in
`internal/contextapi/server.go`; this document describes all 66 routes in that
registry.

## Starting the server

```bash
tesseract serve
```

The default listener is `127.0.0.1:8089`. A non-loopback bind is rejected
unless `--managed-auth`, `--static-token`, or the explicit (and discouraged)
`--allow-unauthenticated-remote` override is supplied. The server speaks HTTP,
not TLS; see the repository security guidance before making it reachable from
another machine.

Useful server flags:

```text
--addr 127.0.0.1:8089
--managed-auth
--static-token <token>
--metrics
--request-logs
--request-log-mode redacted|full
```

`--managed-auth` and `--static-token` are mutually exclusive.

## Wire conventions

- Requests and responses use JSON. Successful response shapes vary by route.
- Every response has `Content-Type: application/json` and `X-Request-Id`.
  Supply `X-Request-Id` to retain a caller-generated correlation ID; otherwise
  the server generates one.
- Every request body is capped at 10 MiB (10,485,760 bytes). An oversized JSON
  body returns `400 validation_error` and names the limit.
- JSON decoding is strict: an unknown field is rejected with
  `400 validation_error`; it is never silently discarded. This matters because
  MCP write tools use flat scalar arguments while the HTTP memory, knowledge
  and event writes use nested `author`, `payload`, and `pointer` objects.
- An unknown route or an unsupported method returns `404 not_found`.
- Collection operations use deterministic ordering. Selector truncation occurs
  after sorting.

Errors use this envelope:

```json
{
  "code": "validation_error",
  "message": "description of the failure",
  "details": null
}
```

`details` may be an object. Common authorization failures are
`401 auth_required`, `403 insufficient_scope`,
`403 namespace_not_permitted`, and `403 policy_denied`.

## Authentication and authorization

There are three actual runtime postures, not separate read and write modes:

| Server posture | Protected routes |
|---|---|
| No token mode (the loopback default) | All routes are admitted without credentials; downstream scope and namespace checks are disabled because no claims exist. |
| `--static-token <token>` | Every route except readiness and metrics requires the exact bearer token. The static token receives the default scopes and namespace glob `*`. |
| `--managed-auth` | Every route except readiness and metrics requires a non-expired, non-revoked store-backed bearer token. The server refuses to start until at least one active token exists. |

Use the header on protected routes whenever either token mode is enabled:

```http
Authorization: Bearer <token>
```

Only `GET /v1/health/readiness` and `GET /v1/metrics` are public while a token
mode is active. Metrics still returns 404 unless the server was started with
`--metrics`. Reads of records, namespaces, audit events, admin state, and the
token inventory are protected just like writes.

Authentication admits a request to its handler. Some handlers then require a
specific scope or check the token's `namespace_globs`; those checks are shown
in the route catalog below. A dash means there is no additional scope check,
not that the route is anonymous.

The static token carries:

```text
write, promote.request, promote.approve, promote.apply,
packet, repair, namespace.register
```

It deliberately does not carry `admin`. Consequently the four admin settings
and configuration mutations marked `admin` require a managed token created
with that explicit scope. Default managed tokens use the same non-admin scope
set as the static token. Create scoped managed tokens directly against the
store with `tesseract context token create`; the raw token is shown only once.

HTTP and MCP use different scope names for namespace registration:
`namespace.register` on HTTP and `namespace.admin` on MCP.

## Route catalog

### Health and metrics

| Method and path | Additional authorization | Contract |
|---|---|---|
| `GET /v1/health/readiness` | public | Store readiness, paths, schema version, and consistency state. |
| `GET /v1/metrics` | public | Per-route counters and latency aggregates; 404 unless `--metrics` is enabled. |

### Namespaces

| Method and path | Additional authorization | Contract |
|---|---|---|
| `POST /v1/namespaces/register` | `namespace.register` | Register or update ownership policy. Body: `namespace`, `owner_type`, `owner_id`, optional `policy`. |
| `GET /v1/namespaces/list` | — | List policies, filtered, sorted and paged. Filters: `prefix` (literal), or `match` with `match_mode` (`prefix` default, `contains`, `glob`), plus `owner_type` and `owner_id`. Order: `sort` (`namespace` default, `owner`, `updated_at`) and `dir` (`asc` default, `desc`). Paging: `limit` (default 200, max 1000) and `cursor`. Answers `{items, count, truncated, next_cursor}` where `count` is the whole matching set, not the page; page until `next_cursor` is absent to read it all. A cursor is bound to the `sort`/`dir` it was issued under. |
| `GET /v1/namespaces/get` | — | Read one policy; requires `namespace`. |

### Context records, views, and packets

| Method and path | Additional authorization | Contract |
|---|---|---|
| `POST /v1/context/write` | `write` + namespace | Append a generic record revision. |
| `POST /v1/context/promote` | — | Retired direct-promotion route; after any configured authentication gate, returns `410 deprecated`. |
| `POST /v1/context/promote/request` | `promote.request` + source namespace | Create a promotion request. |
| `POST /v1/context/promote/approve` | `promote.approve` | Approve a pending request. |
| `POST /v1/context/promote/apply` | `promote.apply` | Apply an approved request to its target. |
| `GET /v1/context/head` | — | Current record for `namespace` + `key`. |
| `GET /v1/context/history` | — | Revision history for `namespace` + `key`; optional non-negative `limit`. |
| `GET /v1/context/audit` | — | Newest-first audit page; filters: `limit`, `cursor`, `namespace`, `event_type`, `actor`, `since`, `until`. |
| `GET /v1/context/consistency/scan` | — | Scan indexed records, payloads, and heads for issues such as `missing_payload`, `head_mismatch`, and `missing_head`. |
| `POST /v1/context/consistency/repair` | `repair` | Rebuild heads, then report remaining issues. |
| `POST /v1/context/typed-write` | `write` + namespace | Append a schema-checked typed record. |
| `POST /v1/context/bulk-ingest` | `write` + each namespace | Ingest up to 100 typed records with per-item results. |
| `POST /v1/context/status/promote` | `write` | Advance or select a typed record status. |
| `POST /v1/context/status/deprecate` | `write` | Mark a typed record deprecated. |
| `POST /v1/context/typed-view` | — | Evaluate a named type-registry view. |
| `GET /v1/context/types` | — | List registered record types. |
| `GET /v1/context/views` | — | List registered typed views. |
| `POST /v1/context/pack` | — | Rank a named view under item/token budgets. |
| `POST /v1/context/plan` | — | Build a context plan for an intent. |
| `POST /v1/broker/plan` | — | Compatibility path for the same handler as `/v1/context/plan`. |
| `POST /v1/context/packet` | — | Assemble selector results and optional pins into an `{items, manifest}` packet. |
| `POST /v1/context/estimate` | — | Estimate record count, bytes, and tokens without returning payloads. |
| `POST /v1/views/evaluate` | — | Evaluate a full selector and return `{items, evaluation_meta}`. |

`POST /v1/context/pack` and `POST /v1/context/packet` are different contracts.
The former starts from a registered `view_id`; the latter starts from a
selector plus an assembly policy. Likewise, the MCP `context_pack` tool has
two shapes; see [the MCP tool catalog](../MCP_TOOLS.md).

### Maintenance

| Method and path | Additional authorization | Contract |
|---|---|---|
| `POST /v1/maintenance/trim` | `repair` | Trim records older than a retention cutoff, optionally as a dry run. |
| `POST /v1/maintenance/compact` | `repair` | Compact excess revisions in a namespace pattern, optionally as a dry run. |
| `POST /v1/maintenance/ttl-cleanup` | — | Delete records whose TTL has expired. |

### Administration

| Method and path | Additional authorization | Contract |
|---|---|---|
| `GET /v1/admin/setup` | — | Setup state and configuration paths. |
| `GET /v1/admin/settings` | — | Current editable runtime settings. |
| `POST /v1/admin/settings/preview` | `admin` | Validate a settings patch and report its effect without installing it. |
| `POST /v1/admin/settings/apply` | `admin` | Validate and atomically install a settings patch. |
| `GET /v1/admin/config/backups` | — | List configuration backups. |
| `POST /v1/admin/config/backup` | `admin` | Create a configuration backup. |
| `POST /v1/admin/config/restore` | `admin` | Restore a configuration backup. |
| `GET /v1/admin/queue` | — | Queue state and counts. |
| `GET /v1/admin/queue/failures` | — | Failed queue entries. |
| `POST /v1/admin/queue/retry-failed` | `repair` | Requeue failed entries. |
| `POST /v1/admin/queue/backfill` | `repair` | Queue embedding backfill work. |
| `GET /v1/admin/storage` | — | Database, payload, and queue storage information. |
| `POST /v1/admin/namespaces/preview` | `namespace.register` | Preview a namespace policy update. |
| `POST /v1/admin/namespaces/update` | `namespace.register` | Install a namespace policy update. |
| `GET /v1/admin/namespaces/history` | — | Namespace-policy history. |

### Managed tokens

| Method and path | Additional authorization | Contract |
|---|---|---|
| `POST /v1/auth/tokens/create` | — | Create a managed token from `name`, `client_id`, `scopes`, `namespace_globs`, and either `ttl` or `expires_at`; returns the raw token once. |
| `GET /v1/auth/tokens/list` | — | List token metadata, never raw token values. |
| `POST /v1/auth/tokens/revoke` | — | Revoke by token `id`. |

These routes are protected whenever a token mode is enabled, but they do not
add a second handler-level scope check. Prefer the local CLI for initial token
creation and recovery.

### Memory, knowledge, event, and workspace

| Method and path | Additional authorization | Contract |
|---|---|---|
| `POST /v1/memory/write` | namespace | Append a memory-domain revision. |
| `POST /v1/memory/recall` | each namespace | Typed ranked recall. The default remains memory + knowledge; workspace and event are opt-in through `filters.domains`. |
| `GET /v1/memory/revisions/{id}` | — | Read one exact revision by `revision_id`. |
| `GET /v1/items/{item_id}` | resolved namespace | Read a current revision or workspace item by stable identity; works for keyless items. Workspace returns its current item shape and reinforces use. |
| `GET /v1/items/{item_id}/history` | resolved namespace | Read a revisioned item's immutable chain newest first; accepts history paging and budget query parameters. Workspace returns `400 history_unavailable`. |
| `POST /v1/refs/resolve` | `memory:read` + resolved namespace | Normalize one typed ID, complete current key, or canonical Tesseract URI to identity metadata. Returns resolution outcomes without content or reinforcement. |
| `GET /v1/memory/current` | namespace | Current memory revision for `namespace` + `key`. |
| `GET /v1/memory/history` | namespace | Memory history for `namespace` + `key`. |
| `POST /v1/memory/touch` | resolved namespace for `item_ids` | Reinforce deliberately used `revision_ids` or current `item_ids`; pass exactly one selector. Deleted workspace IDs are reported under `deleted`. |
| `POST /v1/memory/deprecate` | — | Deprecate one revision by ID. |
| `POST /v1/memory/promote` | source + target namespaces | Promote session-scoped memory to user/project scope. |
| `POST /v1/knowledge/write` | namespace | Append a pointer-first knowledge revision. |
| `GET /v1/knowledge/current` | namespace | Current knowledge revision for `namespace` + `key`. |
| `GET /v1/knowledge/history` | namespace | Knowledge history for `namespace` + `key`. |
| `POST /v1/event/write` | namespace | Append one event-log entry. `key` optional; a keyless write appends a new entry. |
| `GET /v1/event/log` | namespace, per entry | Chronological, keyset-paged read of the event log. `namespace` repeats; `direction`, `since`, `until`, `limit`, `cursor`, `payload_mode`. Every namespace named is authorized, not just the first. |
| `POST /v1/workspace/write` | `write` + namespace | Create by `namespace`, or edit by `item_id` + `version_token`. Keyless create requires `idempotency_key`; an exact retry returns the original identity without returning a stale token. |
| `POST /v1/workspace/delete` | `write` + resolved namespace | Conditionally delete current content and retain an identity tombstone. Repeated delete returns the same successful deleted receipt. |
| `GET /v1/workspace/current` | namespace | Read and reinforce one keyed workspace item by `namespace` + `key`. |
| `POST /v1/workspace/promote/request` | `promote.request` + source and target namespaces | Freeze a reviewed source version, destination selector, metadata, and association. |
| `POST /v1/workspace/promote/approve` | `promote.approve` + retained source and target namespaces | Approve a pending request. Actor is audit attribution, not authority. |
| `POST /v1/workspace/promote/apply` | `promote.apply` + retained source and target namespaces | Atomically verify preconditions, append one target revision, and record an idempotent receipt. |

Here, `namespace` means a `namespace_globs` authorization check when managed or
static authentication is active. The item routes resolve the stored namespace first and
apply the same policy before returning revision content or reinforcing activation. HTTP
memory, knowledge and event content routes currently do not require the MCP-only
`memory:read` or `memory:write` scopes. Reference resolution is the exception shown above:
it requires `memory:read` before resolving metadata.

`item_id` is the stable identity of any Tesseract-owned item. Revisioned domains retain
the same value as `memory_id`; revision and state responses carry both fields for
compatibility. Workspace has no `memory_id` or `revision_id`: its current content carries
`item_id` and `version_token`. The preferred item routes need no domain, namespace, or key,
so they reach keyless items. Existing per-domain namespace/key routes remain supported.
`revision_id` still selects one exact immutable revision and must not be treated as an item
ID.

### Retrieval and synthesis

| Method and path | Additional authorization | Contract |
|---|---|---|
| `POST /v1/tesseract/lookup` | each namespace | Cross-domain ranked lookup with filters, facets, cursors, and payload budgets. |
| `GET /v1/recall` | namespace | Script-oriented typed recall. Requires `namespace`; accepts comma-separated `tags` and `domains`, plus `limit` and `format=brief|full`. Workspace and event remain opt-in. |
| `POST /v1/synthesis/ask` | — | Recall sources and ask the configured LLM; returns answer, numbered sources, and usage/cost metadata. |

Synthesis returns `503 synthesis_unavailable` unless a provider and its API key
are configured. Provider data egress is described in the repository security
guidance.

## Core request examples

### Generic write and read

```bash
curl -sS http://127.0.0.1:8089/v1/context/write \
  -H 'Content-Type: application/json' \
  -H "Authorization: Bearer $TESSERACT_TOKEN" \
  --data '{
    "client_id": "editor",
    "actor": "app:editor",
    "namespace": "app/editor/session",
    "key": "goal",
    "payload": {"text": "ship the preview"},
    "reason": "session update"
  }'

curl -sS \
  -H "Authorization: Bearer $TESSERACT_TOKEN" \
  'http://127.0.0.1:8089/v1/context/head?namespace=app%2Feditor%2Fsession&key=goal'
```

The write response contains `record_id`, `revision`, `head_revision`, and
`timestamp`; the read response contains `record`.

### Three-stage context promotion

Direct `POST /v1/context/promote` is gone. Use the three explicit stages:

```json
{
  "actor": "app:editor",
  "client_id": "editor",
  "source_namespace": "app/editor/session",
  "source_key": "summary",
  "target_namespace": "user/alex/memory/notes",
  "target_key": "summary",
  "reason": "approved session result"
}
```

Send that body to `/v1/context/promote/request`, then pass the returned
`request_id` to `/v1/context/promote/approve`:

```json
{"actor":"user","request_id":"req-...","notes":"reviewed"}
```

Finally send this to `/v1/context/promote/apply`:

```json
{"actor":"user","request_id":"req-..."}
```

The target write requires `actor=user` when its namespace starts with
`user/`. Promotion audit events use `promote.request`, `promote.approve`, and
`promote`.

### Stable item read

Resolve a stored reference without reading its content:

```bash
curl -sS -X POST "$TESSERACT_URL/v1/refs/resolve" \
  -H "Authorization: Bearer $TESSERACT_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"revision_id":"01HREVISION..."}'
```

The body must contain exactly one complete selector: `item_id`, `revision_id`,
`domain` + `namespace` + `key`, or `uri`. The two canonical URI forms are
`tesseract://item/<item_id>` and `tesseract://revision/<revision_id>`.
`resolved`, `deleted`, `not_found`, `ambiguous`, and `unsupported_reference`
are response statuses returned with HTTP 200; malformed selectors,
authorization failures, and unavailable stores remain HTTP errors. Current v1
selectors cannot produce `ambiguous`. A successful result never carries
content, a key, namespace, activation state, or workspace version token.

```bash
curl -sS "$TESSERACT_URL/v1/items/01HITEM..." \
  -H "Authorization: Bearer $TESSERACT_TOKEN"

curl -sS "$TESSERACT_URL/v1/items/01HITEM.../history?limit=20" \
  -H "Authorization: Bearer $TESSERACT_TOKEN"
```

The current read reinforces memory, knowledge, and workspace activation after
authorization. Event items do not participate in activation, and history does not
reinforce any domain. A deleted workspace item returns `410 deleted`; an unknown
`item_id` returns `404 not_found`.

### Workspace create, retry, edit, and delete

Create uses `namespace` as its selector. A keyless create requires an opaque
`idempotency_key`; keyed creates may use one too:

```json
{
  "namespace": "project/example/workspace/scratch",
  "idempotency_key": "run-42-draft-1",
  "summary": "Draft release notes",
  "body": "Current working copy",
  "data": {"section": 1},
  "tags": ["release"],
  "consumer_state": {"phase": "draft"},
  "author": {"agent_id": "assistant", "agent_version": "1"},
  "session_id": "session-42"
}
```

The first response is `{status:"created", item_id, version_token}`. An exact retry
returns `{status:"replayed", item_id, availability:"live"}` and omits
`version_token`, because the original token may no longer be current. Reusing the retry
key with different create arguments returns `409 idempotency_conflict`.

Edit through the same route with `item_id` and the current `version_token`; supplied
content replaces current values, while `clear_fields` removes optional fields:

```json
{
  "item_id": "01HITEM...",
  "version_token": "01HTOKEN...",
  "summary": "Reviewed release notes",
  "clear_fields": ["body"],
  "author": {"agent_id": "assistant"},
  "session_id": "session-42"
}
```

A stale token returns `409 version_conflict`. Delete uses
`POST /v1/workspace/delete` with `{item_id, version_token}`. It erases content, retains
the item's namespace identity in a tombstone, and returns `{status:"deleted", item_id,
deleted_at}`. Repeating delete is successful even though the old version token is no
longer usable for content mutation.

### Workspace promotion

Promotion copies the current workspace `summary`, `body`, and `data` into a
revisioned domain without deleting or redirecting the workspace item. Request
names the exact reviewed source version and nests destination fields:

```json
{
  "source_item_id": "01HWORKSPACE...",
  "source_version_token": "01HVERSION...",
  "actor": "agent:reviewer",
  "reason": "reviewed",
  "target": {
    "domain": "memory",
    "namespace": "user/alex/memory/notes",
    "key": "reviewed.release_notes",
    "author": {"agent_id": "assistant", "agent_version": "1"},
    "session_id": "session-42",
    "trigger": "promotion",
    "derived_from": "project"
  }
}
```

The response supplies `request_id`. Approve with `{request_id, actor, notes}`
at `/approve`, then apply with `{request_id, actor}` at `/apply`. An existing
target uses `target.item_id` and `target.expected_revision_id` and omits target
domain, namespace, and key. Apply rejects a changed or deleted source, an
advanced existing target, or a newly occupied target key before writing. Its
target revision, current-state and link-index changes commit in the same
transaction as the applied receipt; retrying a committed apply returns that
receipt without another revision.

The applied receipt contains `request_id`, `status`, `source_item_id`,
`source_version_token`, `target_item_id`, and `target_revision_id`, plus the
frozen target domain, namespace and optional key. The same typed receipt is
returned by the Go library, HTTP, and MCP.

Destination metadata is reviewed independently of workspace metadata. Omitted
association copies the source workstream for a new target and preserves the
current association for an existing target; an explicit empty string clears
it. Knowledge uses its canonical/reference/manual defaults. Event is canonical
and defaults to observation/manual. Memory requires `trigger` and
`derived_from` and defaults status to draft.

### Memory write shape

HTTP mirrors the flat library content input; `author` remains an object:

```json
{
  "namespace": "user/alex/memory/feedback",
  "memory_key": "editor.preferences",
  "author": {"agent_id": "assistant", "agent_version": "1"},
  "trigger": "explicit",
  "session_id": "session-2026-09-04",
  "derived_from": "user",
  "confidence": 0.9,
  "tags": ["preference"],
  "summary": "Prefer concise diffs.",
  "body": "Keep reviews focused."
}
```

`data` is an optional JSON object carrying the record's own fields, stored verbatim and never
interpreted, with optional `data_schema_hash` recording an unvalidated schema claim.
All three HTTP write routes take top-level `summary`, `body`, `data` and `data_schema_hash`,
matching their library inputs. Author and pointer objects stay structured. Responses still
carry content under `payload`, including `payload.summary`, `payload.body` and `payload.data`.

The old memory write `payload` object is refused with a `400 validation_error` explaining
which fields to move to the top level. Sending both shapes is also refused. MCP's three write
tools now use `data` and `data_schema_hash`; retired `payload_data` and
`payload_data_schema_hash` are refused with migration guidance, including when both names
are present. MCP continues to accept JSON-encoded data strings to preserve exact bytes through
its sanitized and checked arguments path.

Memory write keys still use `MemoryKey` in the library and `memory_key` in HTTP, and memory MCP still uses
`memory_key`, `payload_summary` and `payload_body`. These retained names are outside this
slice's normalization. Keyed reads use `key` on HTTP and MCP. On HTTP get/history routes,
`memory_key` is refused even when empty or accompanied by `key`; response `memory_key` is unchanged.

Memory keys are validated as written, not normalized: at most six dot-separated
segments, each using lowercase letters, digits, and underscore, with 64
characters per segment and 256 total. Hyphens, uppercase letters, and spaces
are rejected.

Every ordinary memory, knowledge, event, and workspace write accepts an optional
top-level `workstream_id`. The value is opaque, exact, and limited to 256 bytes;
leading/trailing whitespace and whitespace-only values are rejected. Revisioned
writes preserve the current association when omitted and clear it with an explicit
empty string. Workspace edits preserve on omission and clear through
`clear_fields: ["workstream_id"]`; supplying and clearing it together is invalid.
HTTP `null` is invalid rather than an alias for omission.

Read results expose the association as top-level `workstream_id`. A receiver
receipt, when present, is separate under `provenance.write_context` and makes no
authentication claim (`verification` is `unverified`). `workstream_id` is an exact
filter on recall/lookup and event-log reads and is applied before limits. Existing
rows are not backfilled.

### Knowledge write shape

```json
{
  "namespace": "user/alex/knowledge/libraries",
  "key": "example-library",
  "kind": "package",
  "source": "filesystem",
  "pointer": {"scheme": "file", "locator": "/workspace/example-library"},
  "summary": "Example library reference.",
  "body": "Public API and integration notes.",
  "author": {"agent_id": "indexer", "agent_version": "1"},
  "session_id": "index-2026-09-04",
  "confidence": 0.9
}
```

Knowledge keys are free-form and are not subject to the memory key grammar.

### Selector evaluation

```json
{
  "selector": {
    "namespaces": ["user/*", "app/editor/*"],
    "keys": ["goal", "summary"],
    "revision_scope": "head",
    "order": ["namespace", "key", "revision"]
  },
  "include_payload": false,
  "limit": 50
}
```

`POST /v1/views/evaluate` responds with `items` and the closed
`evaluation_meta` set: `sort_keys`, `matched_count`, `truncated`, and
`normalized_scope`. There is no separate `returned_count`; use `len(items)`.
See [the views contract](VIEWS.md) for selector fields, bounds, and ordering.

## Determinism and compatibility

- The default selector order is `(namespace, key, revision)`.
- Audit results are newest first by audit ID and use `next_cursor` for paging.
- Context history is ordered by ascending revision and currently returns
  `next_cursor: null`.
- `/v1/broker/plan` is a compatibility alias for `/v1/context/plan`.
- `/v1/context/promote` is retained only as a 410 response after authentication
  so old clients fail with an actionable migration message; it must not be used
  in new examples.
  Its former `source_revision` request field and `target_revision` response
  field are not part of the three-stage contract.
- HTTP and MCP share store/domain logic but do not always share request shapes.
  Use [MCP_TOOLS.md](../MCP_TOOLS.md) for MCP arguments.
