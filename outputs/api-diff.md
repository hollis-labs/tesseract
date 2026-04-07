# API Diff Summary (Task 5)

## Added/changed
- Added namespace registration endpoint: `POST /v1/namespaces/register`.
- Added explicit promotion endpoint: `POST /v1/context/promote`.
- Renamed view endpoint to `POST /v1/views/evaluate` for selector-centric semantics.
- Added `client_id` + `actor` to mutating operations.
- Added explicit determinism requirements section.

## Behavioral clarifications
- Namespace ownership is server-enforced.
- Read and view endpoints are explicitly side-effect free.
- Selector sorting rules are explicit with fallback ordering.
