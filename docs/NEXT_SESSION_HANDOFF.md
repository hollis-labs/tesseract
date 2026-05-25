# Next Session Handoff

Date: 2026-05-25

## Current State

- Admin config editing is live:
  - `GET /v1/admin/settings`
  - `POST /v1/admin/settings/preview`
  - `POST /v1/admin/settings/apply`
- Admin config backup/restore is live:
  - `GET /v1/admin/config/backups`
  - `POST /v1/admin/config/backup`
  - `POST /v1/admin/config/restore`
- Admin namespace policy management is live:
  - `POST /v1/admin/namespaces/preview`
  - `POST /v1/admin/namespaces/update`
  - `GET /v1/admin/namespaces/history`
- Admin queue operations are live:
  - `GET /v1/admin/queue/failures`
  - `POST /v1/admin/queue/retry-failed`
  - `POST /v1/admin/queue/backfill`

## Verification

- `go test ./...` passes
- `npm run build` in `frontend/` passes

## Runtime Rule

Every session that changes backend or frontend behavior must update the live
Cerberus runtime, not just the repo.

Current service:

- Cerberus resource: `conduit-api-service`
- Launchd service: `com.fragments-engine.cerberus.tesseract.conduit-api-service`
- Bound command after latest deploy:
  - `conduit-api-service serve --addr :8089`

Minimum post-change deploy flow:

1. `cerberus resource deploy conduit-api-service`
2. `cerberus resource status conduit-api-service`
3. verify `GET http://127.0.0.1:8089/v1/health/readiness`
4. verify `GET http://127.0.0.1:8089/` returns the embedded frontend

## Next Recommended Slice

Primary next step:

- add admin audit drilldowns and operation-detail APIs so config changes,
  namespace updates, queue retries/backfills, and maintenance actions can be
  inspected as higher-level operator workflows instead of raw audit rows

After that:

- extend `admin/settings` beyond provider/dedup/synthesis fields
- add daemon restart/reload assist
- add queue pause/resume/drain controls
- add namespace presets and richer policy diff rendering

## Key Files

- [docs/FRONTEND_ADMIN_ROADMAP.md](/Users/chrispian/dev/hollis-labs/apps/tesseract/docs/FRONTEND_ADMIN_ROADMAP.md)
- [frontend/src/pages/AdminPage.tsx](/Users/chrispian/dev/hollis-labs/apps/tesseract/frontend/src/pages/AdminPage.tsx)
- [frontend/src/api/client.ts](/Users/chrispian/dev/hollis-labs/apps/tesseract/frontend/src/api/client.ts)
- [frontend/src/api/types.ts](/Users/chrispian/dev/hollis-labs/apps/tesseract/frontend/src/api/types.ts)
- [internal/contextapi/server.go](/Users/chrispian/dev/hollis-labs/apps/tesseract/internal/contextapi/server.go)
- [internal/contextapi/server_test.go](/Users/chrispian/dev/hollis-labs/apps/tesseract/internal/contextapi/server_test.go)
