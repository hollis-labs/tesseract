# Tesseract Frontend Admin Roadmap

Date: 2026-05-24

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

- Setup inventory now has a first HTTP surface at `GET /v1/admin/setup`, but it
  is read-only and does not yet support config edits or guided initialization.
- Embedding queue backlog and worker configuration now have a first admin
  surface at `GET /v1/admin/queue`; storage footprint and namespace retention
  coverage have a first surface at `GET /v1/admin/storage`.
- Maintenance actions now live in the `/admin` operations command center with a
  recent audit feed; deeper event drilldowns and action-specific filters are
  still pending.
- The user-facing screens remain visually mixed: shell is `sysop-ui`, page bodies
  mostly use legacy HUD classes.

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
- Report resolved go-apppaths locations: config file, records dir, index DB,
  queue DB, state dir, cache dir, data dir.
- Report config/runtime flags: auth mode, metrics enabled, request logging mode,
  synthesis provider/model, embedding provider/model, dedup threshold.
- Show filesystem presence and parent-writability checks.
- Next: define config edit/apply contracts and validation errors.

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
- Next: add edit history, policy presets, and validation previews for namespace
  conventions.

### Phase 4: System Observability

- Landed first queue health slice: `GET /v1/admin/queue` plus `/admin` runtime
  display for active, available, delayed, reserved, failed, and by-type queue
  counts.
- Landed first storage/retention slice: `GET /v1/admin/storage` plus `/admin`
  display for DB/records/queue/config bytes, record counts, expired TTL records,
  namespace policy coverage, and top namespaces by revisions.
- Landed metrics tab with route request counts, errors, error rate, latency,
  status buckets, and recent request IDs from `GET /v1/metrics`.
- Next: add request logging state/detail and link recent request IDs to logs when
  a log query endpoint exists.
- Add worker error history and embedding throughput timing.
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

- Add policy presets and validation previews for namespace registration.
- Add scoped token presets for common agent/admin access patterns.
- Define config edit/apply backend contracts before making setup/config mutable.
- Add audit drilldowns and filters for recent operations.
- Add request-log lookup only after a log query endpoint exists.
