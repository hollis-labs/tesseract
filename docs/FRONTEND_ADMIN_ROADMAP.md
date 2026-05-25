# Tesseract Frontend Admin Roadmap

Date: 2026-05-25

## Recon Summary

Tesseract's current UI is strongest around user and agent workflows: context
exploration, memory/knowledge browsing, recall, packet building, writes,
promotion, audit, auth tokens, consistency, and maintenance. The admin surface is
split across several pages and still uses older HUD styling in most page bodies.

Tether's sysop frontend is the target direction for admin/setup work:

- `NavRail` shell with a dense operations layout.
- `PageHeader` and `ListPageLayout` for full-height operational pages.
- `SummaryCards`, `TabStrip`, `DataTable`, `Pill`, `CopyableId`, and
  `SettingsPanel`/`SettingsGrid`/`SettingsField` for setup/config/status pages.
- Neutral `sysop-ui` palette and full-width bands instead of nested cards.

Fragments was found locally as `fragments-engine`; its sysop shell follows the
same direction: route-based operations pages, `NavRail`, and `sysop-ui` theme
imports.

## Comparison Notes From Tether And Cerberus

The clearest pattern in both apps is that strong admin/settings UX comes from
purpose-built backend contracts, not from reusing generic operational routes.

Tether contributes these useful patterns:

- A dedicated settings payload (`/api/settings`) that combines runtime, path,
  daemon, catalog, MCP, provider, launch, and session state into one admin
  snapshot.
- Mutating settings APIs for specific domains instead of one giant "save
  everything" blob:
  - `/api/settings/global/save`
  - `/api/settings/providers/save`
  - `/api/settings/providers/delete`
  - `/api/launches/save`
  - `/api/launches/preview`
  - `/api/launches/delete`
- Settings UI that can actually manage runtime inputs such as providers,
  launches, path roots, and daemon behavior.

Cerberus contributes these useful patterns:

- A split between operator overview (`/api/overview`, `/api/system`) and actual
  settings/change-management APIs (`/api/settings`, migration preview, backup
  list, backup restore).
- Explicit preview/apply flows for risky config mutations.
- Backup and restore surfaced as first-class settings operations.
- A session action token used to gate mutating admin actions.

Tesseract today has the start of the overview side (`/v1/admin/setup`,
`/v1/admin/queue`, `/v1/admin/storage`) and a decent operational command center,
but it does not yet have the dedicated mutable settings/config contracts that
make Tether and Cerberus genuinely user-runnable.

## Current Tesseract Admin Capability

Available frontend/API surfaces:

- Readiness: `GET /v1/health/readiness`
- Metrics: `GET /v1/metrics` when enabled
- Namespaces and policies: `GET /v1/namespaces/list`, `GET /v1/namespaces/get`,
  `POST /v1/namespaces/register`
- Auth tokens: create/list/revoke managed tokens
- Audit log: `GET /v1/context/audit`
- Consistency: scan and repair heads
- Maintenance: trim and compact records
- Broker/packet planning

Gaps:

- Setup inventory now has `GET /v1/admin/setup` plus a first editable settings
  flow through `GET /v1/admin/settings`, `POST /v1/admin/settings/preview`, and
  `POST /v1/admin/settings/apply`, but that flow is still limited to
  config.yaml-backed embedding, synthesis, and dedup settings.
- Embedding queue backlog and worker configuration now have a first admin
  surface at `GET /v1/admin/queue`; storage footprint and namespace retention
  coverage have a first surface at `GET /v1/admin/storage`.
- Maintenance actions now live in the `/admin` operations command center with a
  recent audit feed; deeper event drilldowns and action-specific filters are
  still pending.
- The user-facing screens remain visually mixed: shell is `sysop-ui`, page bodies
  mostly use legacy HUD classes.
- There is still no broad mutable settings/config API family yet. The admin page
  can now safely preview/apply a narrow config slice, but it cannot yet edit
  auth, metrics, request logging, queue behavior, or most storage/runtime
  behavior.
- The new config preview/apply flow still stops at "write config + tell the
  operator to restart"; there is no daemon restart/reload workflow yet.
- Config backup/export/restore now exists, but it is still limited to
  filesystem snapshots of `config.yaml`; there is no richer versioning,
  labeling, or rollback guidance yet.
- There is no action-token or equivalent elevated admin mutation flow distinct
  from ordinary managed auth tokens.
- Namespace policy admin now supports preview/update/history, but it still
  lacks presets, diff rendering, and stronger convention helpers.
- Queue admin now supports failure listing, retry, and embedding backfill, but
  there are still no pause/resume/drain controls or throughput/error trends.
- Audit review is present, but there are no admin drilldowns that tie workflow
  actions to detailed result objects or follow-up actions.

## Backend API Backlog

The next admin improvements should be driven by dedicated backend contracts.
Recommended order:

### 1. Config Snapshot + Update Contracts

First slice is now in place; extend it instead of stretching `/v1/admin/setup`.

Recommended endpoints:

- `GET /v1/admin/settings`
  - canonical editable config/runtime snapshot
  - includes resolved paths, runtime flags, provider config, queue config,
    auth mode, request logging mode, metrics, retention defaults, and web UI
    support state
- `POST /v1/admin/settings/preview`
  - validate a proposed settings patch
  - return normalized patch, validation errors, warnings, and restart impact
- `POST /v1/admin/settings/apply`
  - persist config changes
  - return what changed, whether restart is required, and any follow-up actions

Implemented today:

- config snapshot now powers the admin config tab
- preview/apply now handles:
  - `embedding.provider`
  - `embedding.model`
  - `dedup.similarity_threshold`
  - `synthesis.provider`
  - `synthesis.model`
  - `synthesis.max_tokens`
  - `synthesis.temperature`
  - `synthesis.system_prompt`

Still needed in this family:

- auth mode editing
- metrics and request logging editing
- queue/runtime behavior editing
- explicit reload/restart assist
- backup/version history on config writes

Suggested frontend impact:

- setup/config tab becomes a real editor, not just inventory
- users can configure embeddings and synthesis providers directly from admin
- auth, metrics, and request logging become normal admin operations

### 2. Config Backup / Restore

Mirror the Cerberus pattern so operator changes are reversible.

Recommended endpoints:

- `GET /v1/admin/config/backups`
- `POST /v1/admin/config/backup`
- `POST /v1/admin/config/restore`

Suggested semantics:

- backup list returns path, timestamp, size, and source
- restore returns restored target plus pre-restore safety snapshot path
- all mutations are gated behind elevated admin auth

Implemented today:

- operator-visible config backup list/create/restore flow in `/admin`
- pre-restore safety snapshot creation before rollback writes
- config restore updates the active admin view and marks restart required

### 3. Admin Mutation Session / Action Token

We should separate ordinary API tokens from high-trust admin mutation actions.

Recommended endpoints:

- `POST /v1/admin/session`
  - mint a short-lived admin action token for the web UI after a guarded check
- `POST /v1/admin/session/revoke`

This follows the Cerberus direction more closely than using raw managed tokens
for every destructive action from the browser.

### 4. Namespace Policy Editing And Preview

Current namespace registration is a good start, but it is not enough for a real
instance admin surface.

Recommended endpoints:

- `POST /v1/admin/namespaces/preview`
  - validate policy changes, tier conventions, limits, retention
- `POST /v1/admin/namespaces/update`
  - mutate an existing namespace policy
- `GET /v1/admin/namespaces/history`
  - audit-oriented history for policy changes

Suggested frontend impact:

- policy presets
- editable namespace rows
- preview-before-apply behavior

Implemented today:

- preview/update/history endpoints
- editable namespace rows in `/admin`
- policy history panel for register/update audit events

### 5. Queue / Embedding Operations

The read-only queue view should grow into actual embedding/runtime control.

Recommended endpoints:

- `POST /v1/admin/queue/backfill`
  - trigger embedding backfill with scope controls
- `POST /v1/admin/queue/retry-failed`
- `POST /v1/admin/queue/pause`
- `POST /v1/admin/queue/resume`
- `GET /v1/admin/queue/failures`

Suggested frontend impact:

- retry and backfill actions from `/admin`
- failure history and per-job inspection
- worker state controls if we keep queue workers in-process

Implemented today:

- queue failure listing
- retry failed embed jobs
- enqueue embedding backfill by namespace/limit

Still needed in this family:

- pause/resume/drain controls
- worker throughput and age/error trend views
- richer payload inspection for failed jobs

### 6. Storage / Retention Preview APIs

Trim and compact already support dry-run patterns in the UI, but storage
recommendation and retention planning are still missing.

Recommended endpoints:

- `GET /v1/admin/storage/recommendations`
  - suggest retention policy changes based on current usage
- `POST /v1/admin/storage/trim/preview`
- `POST /v1/admin/storage/compact/preview`
- `POST /v1/admin/storage/ttl/preview`

These can wrap existing backend logic while giving the admin UI stable
contracts for display and confirmation.

### 7. Audit Drilldown / Operation Results

The admin audit tab should be able to answer "what happened?" without forcing
the operator into raw event browsing.

Recommended endpoints:

- `GET /v1/admin/audit/operations`
  - higher-level operational history over repair/trim/compact/token/policy actions
- `GET /v1/admin/audit/operations/{id}`
  - detailed result payload, warnings, follow-up recommendations

### 8. Provider And Runtime Capability Introspection

Because Tesseract is provider-driven, the admin page should expose what the
instance can and cannot do right now.

Recommended endpoints:

- `GET /v1/admin/providers`
  - configured embedding/synthesis providers, models, env readiness, capability flags
- `GET /v1/admin/runtime/capabilities`
  - whether embeddings, synthesis, queue workers, metrics, request logging,
    token auth, and web UI are operational

This is especially useful for first-run setup and supportability.

## Roadmap

### Phase 1: Admin Hub Foundation

- Add `/admin` route backed by `sysop-ui` primitives.
- Show live readiness, namespace count, token count, metrics state, consistency
  issue count, and known management endpoints.
- Use roadmap rows to make missing backend capabilities explicit.
- Keep mutating actions linked to existing pages until we add guarded admin
  workflows.

### Phase 2: Setup And Config Inventory

- Landed first read-only slice: `GET /v1/admin/setup`.
- Landed editable config slice:
  `GET /v1/admin/settings`, `POST /v1/admin/settings/preview`,
  `POST /v1/admin/settings/apply`.
- Landed config backup slice:
  `GET /v1/admin/config/backups`, `POST /v1/admin/config/backup`,
  `POST /v1/admin/config/restore`.
- Report resolved go-apppaths locations: config file, records dir, index DB,
  queue DB, state dir, cache dir, data dir.
- Report config/runtime flags: auth mode, metrics enabled, request logging mode,
  synthesis provider/model, embedding provider/model, dedup threshold.
- Show filesystem presence and parent-writability checks.
- Next: extend settings beyond provider/dedup/synthesis and add restart/reload
  assist.

### Phase 3: Management Workflows

- Landed first workflow slice in `/admin`: consistency scan/repair, trim,
  compact, and TTL cleanup.
- Trim and compact require matching dry-run previews before apply is enabled.
- TTL cleanup has an explicit warning/confirmation because the backend has no
  dry-run variant yet.
- Landed recent audit review in `/admin`, including refresh/load-more controls
  and event rows for operational history.
- Next: add audit-row drilldowns, filters, and maintenance result links.

### Phase 3A: Access Operations

- Landed first access slice in `/admin`: managed token create/list/revoke.
- Token creation preserves one-time token visibility and copy action.
- Next: add scoped presets for common agent/admin tokens and clarify auth-mode
  behavior when managed auth is disabled.

### Phase 3B: Namespace Policy Operations

- Landed namespace policy table in `/admin` with owner, tier, retention, revision
  cap, byte cap, and update timestamp columns.
- Landed namespace registration form backed by `POST /v1/namespaces/register`.
- Landed `POST /v1/admin/namespaces/preview`,
  `POST /v1/admin/namespaces/update`, and
  `GET /v1/admin/namespaces/history`.
- Next: add presets, better diff rendering, and stronger convention validation.

### Phase 4: System Observability

- Landed first queue health slice: `GET /v1/admin/queue` plus `/admin` runtime
  display for active, available, delayed, reserved, failed, and by-type queue
  counts.
- Landed first queue operations slice:
  `GET /v1/admin/queue/failures`,
  `POST /v1/admin/queue/retry-failed`, and
  `POST /v1/admin/queue/backfill`.
- Landed first storage/retention slice: `GET /v1/admin/storage` plus `/admin`
  display for DB/records/queue/config bytes, record counts, expired TTL records,
  namespace policy coverage, and top namespaces by revisions.
- Landed metrics tab with route request counts, errors, error rate, latency,
  status buckets, and recent request IDs from `GET /v1/metrics`.
- Next: add request logging state/detail and link recent request IDs to logs when
  a log query endpoint exists.
- Add pause/resume/drain controls, worker error trends, and embedding throughput timing.
- Add historical storage growth and namespace retention recommendations.

### Phase 5: Style Migration

- Migrate admin pages first to `sysop-ui` page primitives.
- Then migrate user-facing pages in groups: read/browse pages, write/promote
  pages, recall/search pages, maintenance/access pages.
- Retire legacy HUD layout patterns after page bodies no longer depend on them.

## First Slice

Create the `/admin` route with a live overview and roadmap tabs. It should not
invent backend data; it should use existing endpoints and clearly identify
backend work required for setup/config management.

## Resume Point

Next practical slices:

- Extend `admin/settings` beyond provider/dedup/synthesis fields.
- Add daemon reload/restart guidance or automation hooks.
- Add policy presets and richer diff rendering for namespace registration/update.
- Add queue pause/resume/drain controls and richer failed-job inspection.
- Add audit drilldowns and operation-detail APIs across config, namespace, queue,
  and maintenance actions.
- Add scoped token presets for common agent/admin access patterns.
- Add request-log lookup only after a log query endpoint exists.
