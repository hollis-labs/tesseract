# HTTP API Spec

Status: pivot-aligned draft (Task 5)

## Conventions
- Base path: `/v1`
- JSON request/response
- Deterministic ordering for all collection responses
- Error shape:
  - `code` (string)
  - `message` (string)
- `details` (object, optional)

Common error codes:
- `validation_error` (`400`)
- `policy_denied` (`403`)
- `auth_required` (`401`)
- `not_found` (`404`)

## Local auth posture (MVP)
- Mechanism: bearer token in `Authorization` header.
- Scope: mutating endpoints (`POST /v1/namespaces/register`, `POST /v1/context/write`, `POST /v1/context/promote`).
- Behavior:
  - Legacy mode: when static server auth token is configured, mutating requests without a matching bearer token return `401`.
  - Managed mode: token lifecycle is store-backed (issue/rotate/revoke/expiry). Revoked/expired tokens are rejected with `401`.
  - Read endpoints (`head`, `history`, `views/evaluate`) remain side-effect free and do not require auth in MVP.
- Error envelope for auth failures:
  - `code: "auth_required"`
  - `message: "missing or invalid bearer token"`

### Post-MVP read/view auth roadmap

Mode options under evaluation:
1. `mvp-open-read`:
   - `head`, `history`, `views/evaluate` remain unauthenticated.
   - Backward compatible with current local-first defaults.
2. `optional-gated-read`:
   - server flag enables bearer-token checks for read/view endpoints.
   - intended for shared workstations or team-local environments.
3. `strict-auth-all`:
   - all endpoints require auth tokens.
   - requires explicit migration guidance for existing local scripts.

Migration notes:
- Default behavior remains `mvp-open-read` until a mode switch is explicitly configured.
- Future `optional-gated-read`/`strict-auth-all` modes must preserve response ordering and payload schema contracts.
- CLI/MCP adapters should surface mode expectations clearly and fail with deterministic `auth_required` envelopes when gated.

Auth-mode endpoint matrix:

| Endpoint class | `mvp-open-read` | `optional-gated-read` | `strict-auth-all` |
|---|---|---|---|
| Read/view (`GET /v1/context/head`, `GET /v1/context/history`, `POST /v1/views/evaluate`) | No token required | Token required | Token required |
| Mutating (`POST /v1/namespaces/register`, `POST /v1/context/write`, `POST /v1/context/promote`, `POST /v1/context/consistency/repair`) | Token required | Token required | Token required |
| Operational read (`GET /v1/context/audit`, `GET /v1/context/consistency/scan`, `GET /v1/health/readiness`) | No token required | Token required | Token required |

Optional-gated read/view mode examples:
- Gated read request (`head`) with bearer token:
```http
GET /v1/context/head?namespace=user/profile&key=summary
Authorization: Bearer <token>
```
- Gated view request with bearer token:
```http
POST /v1/views/evaluate
Authorization: Bearer <token>
Content-Type: application/json

{"selector":{"namespaces":["user/*"],"order":["namespace","key","revision"]},"limit":50}
```
- Auth failure envelope when token missing/invalid in gated mode:
```json
{
  "code": "auth_required",
  "message": "missing or invalid bearer token",
  "details": null
}
```

Auth failure decision tree (`auth_required`):
1. Identify endpoint class:
   - If mutating endpoint: token is always required.
   - If read/view endpoint: check whether mode is `mvp-open-read` vs gated modes.
2. Confirm active auth mode:
   - `mvp-open-read`: read/view should not require token.
   - `optional-gated-read` or `strict-auth-all`: token required for read/view.
3. Validate token propagation:
   - Ensure `Authorization: Bearer <token>` header is present at call boundary.
   - Confirm token is active (not revoked/expired in managed mode).
4. Re-check caller/tool path:
   - Verify CLI/MCP bridge is not dropping auth headers/tokens.
   - Confirm endpoint target matches expected environment mode.

### Read/view gated-mode preflight checklist
Run this preflight before `GET /v1/context/head`, `GET /v1/context/history`, or `POST /v1/views/evaluate` when using `optional-gated-read` or `strict-auth-all`:
1. Mode confirmation:
   - Confirm active mode is gated (`optional-gated-read` or `strict-auth-all`) in the target environment.
   - Re-check endpoint class in the auth-mode endpoint matrix to avoid using mutate assumptions for read/view calls.
2. Token propagation:
   - Ensure `Authorization: Bearer <token>` is present from caller through CLI/MCP/API boundary.
   - Validate token lifecycle status (active, non-revoked, non-expired in managed mode).
3. Endpoint targeting:
   - Verify request is sent to the intended environment/base URL with matching mode configuration.
   - Confirm no intermediary route strips auth headers for read/view paths.
4. Failure expectation:
   - If any step fails in gated modes, expect `401 auth_required` and resolve mode/token propagation before retry.

### Auth mode troubleshooting matrix

Use this when observed auth behavior differs from expectation:

| Endpoint class | Expected token behavior by mode | Common mismatch symptom | First troubleshooting action |
|---|---|---|---|
| Read/view (`head`, `history`, `views/evaluate`) | `mvp-open-read`: token optional; `optional-gated-read`/`strict-auth-all`: token required | Read/view returns `401` in environments assumed to be open-read | Confirm active mode, then verify read/view request path includes bearer token when mode is gated. |
| Mutating (`namespaces/register`, `context/write`, `context/promote`, `consistency/repair`) | Token required in all modes | Mutating call succeeds in one tool path and fails with `401` in another | Compare token propagation across CLI/MCP/API boundaries and validate token lifecycle status. |
| Operational read (`context/audit`, `consistency/scan`, `health/readiness`) | `mvp-open-read`: token optional; `optional-gated-read`/`strict-auth-all`: token required | Operational reads unexpectedly reject unauthenticated calls after rollout | Re-check environment mode and endpoint targeting, then align operator scripts with current mode policy. |

Matrix usage notes:
- Classify the request first using the auth-mode endpoint matrix, then follow the `auth_required` decision tree.
- If endpoint class assumptions are wrong, fix routing/targeting before rotating tokens or changing policy settings.

### Auth troubleshooting quick decision path

Use this first-response path for `auth_required` incidents:
1. Classify endpoint:
   - Determine whether the request is read/view, mutating, or operational read.
2. Confirm mode expectation:
   - Verify active environment mode (`mvp-open-read`, `optional-gated-read`, `strict-auth-all`) and expected token requirement for that endpoint class.
3. Check token propagation:
   - Confirm `Authorization: Bearer <token>` reaches the API boundary from caller/tool path.
   - Validate token lifecycle status if managed auth mode is active.
4. Verify environment targeting:
   - Confirm base URL/cluster/host matches the mode assumption.
   - Check that no proxy/adapter is stripping auth headers.
5. Choose next action:
   - If mode mismatch: update caller assumptions/scripts to current mode.
   - If token path failure: fix propagation before retry.
   - If token invalid: rotate/re-issue token and re-run request.

### Auth incident handoff checklist

Use this when handing unresolved `auth_required` incidents to another operator:
1. Endpoint class + request target:
   - Record endpoint class (read/view, mutating, operational read), endpoint path, and target environment/base URL.
2. Mode assumption snapshot:
   - Record assumed active mode and why (`mvp-open-read`, `optional-gated-read`, `strict-auth-all`).
3. Token-flow findings:
   - Record whether bearer token was present at caller boundary and API boundary.
   - Record token lifecycle status checks (active/revoked/expired) when applicable.
4. Actions completed:
   - List troubleshooting steps already run (matrix checks, decision-path branches, preflight checks).
5. Pending actions + owner:
   - Record next required action, owner, and expected verification signal before retry.

### Auth incident closure checklist

Use this when an `auth_required` incident is resolved:
1. Confirmed root cause:
   - Record the validated root cause (mode mismatch, token propagation gap, token lifecycle issue, or targeting error).
2. Fix verification:
   - Record verification request(s) showing expected behavior after fix.
   - Confirm outcome is consistent with endpoint class + active mode expectations.
3. Residual risk + follow-up:
   - Record any remaining risk (for example, fragile proxy/header path) and assigned follow-up action.
4. Documentation/automation updates:
   - Record updates made to scripts/runbooks/tool configs to prevent recurrence.
5. Closure owner + timestamp:
   - Record incident closer and closure time for auditability.

### Auth post-incident review note

After closure, run a short review to reduce recurrence:
1. Recurrence pattern:
   - Identify whether the incident pattern was mode assumption drift, token-path fragility, lifecycle handling gap, or environment targeting confusion.
2. Mitigation update:
   - Record one concrete mitigation update (runbook/script/config/automation guard) applied after closure.
3. Ownership:
   - Assign an owner for validating the mitigation in the next release/maintenance window.

### Auth incident evidence capture note

Capture these artifacts during incident handling:
1. Request context evidence:
   - Endpoint path/class, request timestamp window, and target environment/base URL.
2. Mode snapshot evidence:
   - Active mode at incident time (`mvp-open-read`, `optional-gated-read`, `strict-auth-all`) and source of confirmation.
3. Token-path evidence:
   - Presence/absence of bearer token at caller boundary and API boundary.
   - Token lifecycle status evidence when managed mode applies.
4. Verification artifacts:
   - Before/after request outcomes used to validate diagnosis and fix.

### Auth incident timeline capture note

Record a minimal timeline for each auth incident:
1. Detection timestamp:
   - First observed failure time and detection source.
2. Mitigation timeline:
   - Ordered list of mitigation steps with timestamps (mode check, token-path fixes, config updates).
3. Closure verification timestamp:
   - Time of successful post-fix verification request(s) and verifier identity.

### Auth incident severity tagging note

Tag each auth incident with a severity label to drive follow-up:
1. Severity intent:
   - `sev-1`: service-wide auth breakage or blocking operator workflows.
   - `sev-2`: scoped auth failure with workaround.
   - `sev-3`: isolated/non-blocking auth issue.
2. Ownership impact:
   - Record escalation owner for `sev-1`/`sev-2` and follow-up owner for `sev-3`.
3. Follow-up expectation:
   - Higher severity requires tighter follow-up timing and explicit mitigation tracking in post-incident review notes.

### Auth incident escalation acknowledgement note

Record escalation acknowledgement details once incident severity is tagged:
1. Acknowledgement actor:
   - Record who accepted escalation (name/role) for `sev-1` and `sev-2` incidents.
2. Acknowledgement timestamp + channel:
   - Record when escalation was acknowledged and where (for example: on-call channel, incident room, paging system).
3. Flow alignment:
   - Link acknowledgement details to the timeline, handoff checklist, and closure checklist records.

### Auth incident escalation timeout note

If escalation acknowledgement does not lead to progress in the expected window:
1. Timeout window:
   - Record the escalation timeout window for the incident severity class and current phase.
2. Reassignment owner + status update:
   - Reassign active escalation ownership and post status update in the incident communication channel when timeout is reached.
3. Continuity linkage:
   - Link timeout handling notes to timeline entries and closure checklist updates.

### Auth incident closure evidence retention note

After incident closure, keep closure evidence available with:
1. Retention window:
   - Record minimum retention period for closure artifacts in the active release/support window.
2. Artifact scope:
   - Retain closure checklist entries, verification request outcomes, and post-incident mitigation references together.
3. Owner responsibility:
   - Assign an owner for retention integrity and archival/cleanup decisions after the retention window ends.

### Auth incident evidence archival note

After retention windows complete, archive incident evidence with:
1. Archive trigger:
   - Archive when retention period ends and no active follow-up/escalation remains.
2. Archival scope:
   - Archive closure checklist records, timeline evidence, escalation acknowledgements/timeouts, and mitigation references together.
3. Archive accountability:
   - Record archival owner and archival timestamp in the incident evidence trail.

### Auth incident evidence retrieval note

When archived incident evidence is needed for audit/follow-up:
1. Retrieval pointer:
   - Record archive location/identifier used to retrieve incident evidence.
2. Retrieval scope:
   - Retrieve timeline, closure, escalation, retention, and archival records as one evidence package.
3. Retriever accountability:
   - Record retriever identity and retrieval timestamp in follow-up notes.

### Auth incident evidence reconciliation note

If evidence records conflict across lifecycle stages:
1. Mismatch record:
   - Record conflicting fields between retained, archived, and retrieved evidence artifacts.
2. Reconciliation action:
   - Resolve mismatch by validating canonical incident evidence trail and correcting stale/incorrect references.
3. Reconciler accountability:
   - Record reconciler owner and reconciliation timestamp in incident follow-up records.

### Auth evidence discrepancy escalation note

If reconciliation cannot resolve evidence mismatches:
1. Escalation trigger:
   - Escalate unresolved evidence mismatches after reconciliation attempts in the current review cycle.
2. Escalation record:
   - Record discrepancy summary, impacted evidence artifacts, and assigned escalation owner.
3. Outcome linkage:
   - Link escalation outcome to corrected evidence references before final closure confirmation.

### Auth evidence prevention note

Reduce future evidence discrepancies with proactive checks:
1. Prevention checks:
   - Run periodic checks for missing lifecycle fields across retention, archival, retrieval, and reconciliation records.
2. Drift watch:
   - Flag incidents with repeated evidence corrections for targeted process updates.
3. Precedence guard:
   - Keep canonical-source precedence and incident closure references explicit in all preventive updates.

### Auth evidence continuous-improvement note

Track recurring evidence issues with a lightweight improvement loop:
1. Recurring issue log:
   - Record repeated evidence mismatch patterns by incident area.
2. Linked improvements:
   - Attach one concrete process/runbook/doc improvement action per recurring pattern.
3. Follow-up verification:
   - Re-check affected evidence paths in the next maintenance pass and record outcome.

### Auth evidence trend note

Track evidence quality patterns across release windows:
1. Trend tracking:
   - Record recurring evidence mismatch/correction patterns by incident stream and release window.
2. Trend interpretation:
   - Classify trend direction as improving, stable, or worsening.
3. Action linkage:
   - Link trend signals to targeted preventive or process-improvement actions.

## Identity and namespace context
Each mutating request carries:
- `client_id` (string)
- `actor` (string: `user`, `app:<client-id>`, `system`)

Server enforces namespace ownership rules independent of caller-provided namespace values.
When namespace policy includes schema contract metadata (`required_keys`), write/promote payloads are validated and schema mismatches return `400` (`validation_error`).

### Actor-namespace contract matrix

| Actor | Read/view (`head/history/views`) | Write to `app/<client-id>/*` | Write to `user/*` | Promote to `user/*` |
|---|---|---|---|---|
| `user` | Allowed | Allowed only when policy grants match target app namespace | Direct write not default path; use promote policy model | Allowed (required actor for promote) |
| `app:<client-id>` | Allowed | Allowed for owned app namespace | Denied by default | Denied (must be user actor) |
| `system` | Allowed | Allowed only for explicit system-owned/maintenance policy scopes | Denied by default | Denied by default |

Policy notes:
- Namespace ownership policy is authoritative for write decisions.
- `user/*` remains protected and is updated through explicit user promotion semantics.
- Policy schema evolution options reference: `docs/SPECS/STORAGE.md#policy-schema-evolution-options`.

### Mutate error quick-reference

| Error code | Typical endpoint context | Common trigger | Operator action |
|---|---|---|---|
| `auth_required` (`401`) | Any mutating endpoint (`namespaces/register`, `context/write`, `context/promote`, `consistency/repair`) | Missing/invalid bearer token, revoked/expired managed token | Verify auth mode and token propagation, then re-issue/rotate token if needed. |
| `policy_denied` (`403`) | `context/write`, `context/promote` | Actor/namespace ownership mismatch or protected `user/*` transition without required actor semantics | Re-check actor identity, namespace ownership policy, and promote constraints in actor/namespace matrix. |
| `validation_error` (`400`) | `namespaces/register`, `context/write`, `context/promote` | Invalid payload schema (e.g., `required_keys`) or malformed request fields | Fix request shape/schema mismatch; align payload with namespace policy contract before retry. |

### Mutating call preflight checklist
Run this quick preflight before `POST /v1/namespaces/register`, `POST /v1/context/write`, `POST /v1/context/promote`, or `POST /v1/context/consistency/repair`:
1. Auth/token readiness:
   - Confirm active mode and endpoint expectations from the auth-mode matrix.
   - Ensure `Authorization: Bearer <token>` is present and token is not revoked/expired.
   - If this fails, expect `401 auth_required`.
2. Actor/namespace policy readiness:
   - Confirm `actor` and `client_id` match intended ownership scope.
   - Validate target namespace transitions against the actor-namespace contract matrix.
   - If this fails, expect `403 policy_denied`.
3. Payload/schema readiness:
   - Validate required request fields and payload shape before send.
   - For contract-bound namespaces, confirm payload satisfies `required_keys`.
   - If this fails, expect `400 validation_error`.

## Endpoints

### POST `/v1/namespaces/register`
Register a namespace owner policy.

Request:
- `namespace`
- `owner_type` (`user` | `app`)
- `owner_id`
- `policy` (object)

Response:
- `namespace`
- `owner_type`
- `owner_id`
- `policy`

### GET `/v1/namespaces/get`
Retrieve namespace owner + policy metadata.

Query:
- `namespace`

Response:
- `namespace`
- `owner_type`
- `owner_id`
- `policy`

### POST `/v1/context/write`
Append revision for `(namespace,key)`.

Request:
- `client_id`
- `actor`
- `namespace`
- `key`
- `payload`
- `reason` (optional)

Response:
- `record_id`
- `revision`
- `head_revision`
- `timestamp`

Policy-denied example (`403`):
```json
{
  "code": "policy_denied",
  "message": "actor app:editor-ui is not allowed to write namespace user/profile",
  "details": {
    "actor": "app:editor-ui",
    "namespace": "user/profile"
  }
}
```

### POST `/v1/context/promote`
Promote a record into protected `user/*` with explicit approval semantics.

Request:
- `client_id`
- `actor` (`user` required)
- `from_namespace`
- `from_key`
- `to_namespace` (`user/*`)
- `to_key`
- `source_revision` (optional; defaults to current head)

Response:
- `promoted_record_id`
- `target_revision`

Policy-denied example (`403`):
```json
{
  "code": "policy_denied",
  "message": "promote requires actor user for protected namespace transitions",
  "details": {
    "actor": "app:editor-ui",
    "to_namespace": "user/profile"
  }
}
```

### GET `/v1/context/head`
Query:
- `namespace`
- `key`

Response:
- `record`

### GET `/v1/context/history`
Query:
- `namespace`
- `key`
- `limit` (optional)
- `cursor` (optional)

Response:
- `items` (stable oldest->newest window unless cursor semantics specify bounded page)
- `next_cursor`

### POST `/v1/views/evaluate`
Evaluate deterministic selector for context-aware retrieval.

Request:
- `selector` (object)
- `include_payload` (bool, default true)
- `limit` (optional)

Response:
- `items` (deterministically ordered)
- `evaluation_meta` (`sort_keys`, `matched_count`, `truncated`, `normalized_scope`)

Selector limits:
- `namespaces`: max 32 patterns
- `keys`: max 128 entries
- `limit`: defaults to 200 when omitted/zero, max 500
- invalid selector complexity/shape returns `400` (`code: "validation_error"`)
- selector extension/versioning policy reference: `docs/SPECS/VIEWS.md#selector-extension-and-versioning-policy`
- selector capability discovery reference: `docs/SPECS/VIEWS.md#selector-capability-discovery-model`

Deterministic ordering behavior:
- If `selector.order` is omitted, server applies fallback sort `(namespace,key,revision)`.
- `limit` truncation is applied after deterministic sort and reflected via `evaluation_meta.truncated`.

Valid request example:
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

Additional selector examples (`revision_scope=all`) are documented in `docs/SPECS/VIEWS.md`.

Valid response example:
```json
{
  "items": [
    {
      "namespace": "app/editor/session",
      "key": "goal",
      "revision": 3
    },
    {
      "namespace": "user/profile",
      "key": "summary",
      "revision": 8
    }
  ],
  "evaluation_meta": {
    "sort_keys": ["namespace", "key", "revision"],
    "matched_count": 2,
    "truncated": false,
    "normalized_scope": "head"
  }
}
```

Invalid selector examples (`400 validation_error`):
- Unknown selector field:
```json
{
  "selector": {
    "namespaces": ["user/*"],
    "order": ["namespace", "key", "revision"],
    "unknown_field": true
  }
}
```
- Limit above allowed maximum:
```json
{
  "selector": {
    "namespaces": ["user/*"],
    "order": ["namespace", "key", "revision"]
  },
  "limit": 1000
}
```

Selector validation error map (`POST /v1/views/evaluate`):

| Failure category | Example cue | Expected error class | First remediation step |
|---|---|---|---|
| Unknown selector field | Request includes undocumented selector key | `400 validation_error` | Remove/rename field to documented schema and verify capability support. |
| Limit bounds violation | Negative `limit` or `limit` above max bound | `400 validation_error` | Set `limit` within accepted range (default/`<=500`). |
| Structural/shape mismatch | Wrong value type or malformed selector payload | `400 validation_error` | Normalize request to expected JSON object shape for selector fields. |

### GET `/v1/context/audit`
Query:
- `limit` (optional; default `50`, max `200`)
- `cursor` (optional; positive integer from prior `next_cursor`)
- `namespace` (optional exact filter)
- `event_type` (optional exact filter)

Response:
- `items` (newest-first deterministic ordering by `id DESC`)
- `count`
- `next_cursor` (nullable integer)

Example pagination:
1. `GET /v1/context/audit?limit=2`
2. Read `next_cursor` from response.
3. `GET /v1/context/audit?limit=2&cursor=<next_cursor>`

### GET `/v1/context/consistency/scan`
Run a deterministic consistency scan across index rows and payload files.

Response:
- `count` (integer)
- `issues` (array of typed findings such as `missing_payload`, `head_mismatch`, `missing_head`)

### POST `/v1/context/consistency/repair`
Rebuild `heads` from latest indexed revisions for each `(namespace,key)` pair.

Auth:
- Treated as mutating; requires bearer token when auth token is configured.

Response:
- `rebuilt_heads` (integer)
- `remaining_issues` (integer)
- `issues` (post-repair scan findings)

### GET `/v1/health/readiness`
Return deterministic operational readiness status.

Response:
- `healthy` (boolean)
- `status` (`healthy` | `degraded` | `failing`)
- `db_path` (string)
- `records_dir` (string)
- `records_dir_exists` (boolean)
- `schema_version` (integer)
- `consistency_issues` (integer)
- `generated_at` (RFC3339 UTC)

### GET `/v1/metrics` (optional)
Expose lightweight runtime request counters and latency aggregates.

Behavior:
- Endpoint is enabled only when service starts with metrics flag (`tesseract serve --metrics`).
- When disabled, endpoint returns `404`.

Response:
- `enabled` (boolean)
- `routes` (array of route metrics sorted by `(method,path)`)
  - `method`
  - `path`
  - `requests`
  - `errors`
  - `latency_ns_total`
  - `latency_ns_avg`
  - `status_counts` (object keyed by HTTP status code)
  - `recent_request_ids` (array of most recent request IDs for route correlation)
- `totals`
  - `requests`
  - `errors`

## Determinism requirements
- Identical store state + identical request must produce identical item ordering.
- Selector evaluation must use explicit sort keys; fallback sort is `(namespace,key,revision)`.
- APIs never mutate state during read/view calls.
