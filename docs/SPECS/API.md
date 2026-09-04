# HTTP API

Status: implemented public-preview contract.

The HTTP server exposes JSON routes under `/v1/` and the embedded web UI at
`/`. The authoritative route registry is `apiRoutes` in
`internal/contextapi/server.go`; this document describes all 63 routes in that
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
  MCP write tools use flat scalar arguments while HTTP memory and knowledge
  writes use nested `author`, `payload`, and `pointer` objects.
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
| `GET /v1/namespaces/list` | — | List policies; accepts `prefix` and `limit`. |
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

### Memory and knowledge

| Method and path | Additional authorization | Contract |
|---|---|---|
| `POST /v1/memory/write` | namespace | Append a memory-domain revision. |
| `POST /v1/memory/recall` | each namespace | Ranked memory/knowledge recall with cursor and response budgets. |
| `GET /v1/memory/revisions/{id}` | — | Read one revision by ID. |
| `GET /v1/memory/current` | namespace | Current memory revision for `namespace` + `memory_key`. |
| `GET /v1/memory/history` | namespace | Memory history for `namespace` + `memory_key`. |
| `POST /v1/memory/touch` | — | Reinforce deliberately used revision IDs. |
| `POST /v1/memory/deprecate` | — | Deprecate one revision by ID. |
| `POST /v1/memory/promote` | source + target namespaces | Promote session-scoped memory to user/project scope. |
| `POST /v1/knowledge/write` | namespace | Append a pointer-first knowledge revision. |
| `GET /v1/knowledge/current` | namespace | Current knowledge revision for `namespace` + `memory_key`. |
| `GET /v1/knowledge/history` | namespace | Knowledge history for `namespace` + `memory_key`. |

Here, `namespace` means a `namespace_globs` authorization check when managed or
static authentication is active. HTTP memory and knowledge routes currently do
not require the MCP-only `memory:read` or `memory:write` scopes.

### Retrieval and synthesis

| Method and path | Additional authorization | Contract |
|---|---|---|
| `POST /v1/tesseract/lookup` | each namespace | Cross-domain ranked lookup with filters, facets, cursors, and payload budgets. |
| `GET /v1/recall` | namespace | Script-oriented recall. Requires `namespace`; accepts comma-separated `tags`, `limit`, and `format=brief|full`. |
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

### Memory write shape

HTTP uses nested objects:

```json
{
  "namespace": "user/alex/memory/feedback",
  "memory_key": "editor.preferences",
  "author": {"agent_id": "assistant", "agent_version": "1"},
  "trigger": "explicit",
  "session_id": "session-2026-09-04",
  "origin": "user",
  "confidence": 0.9,
  "tags": ["preference"],
  "payload": {"summary": "Prefer concise diffs.", "body": "Keep reviews focused."}
}
```

Memory keys are validated as written, not normalized: at most six dot-separated
segments, each using lowercase letters, digits, and underscore, with 64
characters per segment and 256 total. Hyphens, uppercase letters, and spaces
are rejected.

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
