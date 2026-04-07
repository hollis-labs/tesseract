# API Pivot Diff Notes

Status: draft reference note

This note summarizes pivot-driven API specification updates. Canonical definitions remain in [`docs/SPECS/API.md`](../API.md).

## Scope change summary
- Product framing shifted from personal-cache-first to a generalized context registry + working memory model.
- `user/*` remains a first-class protected namespace under the same registry model.
- Retrieval contracts emphasize deterministic selector/view behavior.

## Endpoint-level highlights
- Added/clarified namespace ownership registration and lookup:
  - `POST /v1/namespaces/register`
  - `GET /v1/namespaces/get`
- Clarified write/promote identity + ownership policy context:
  - `POST /v1/context/write`
  - `POST /v1/context/promote`
- Added deterministic view evaluation contract:
  - `POST /v1/views/evaluate`
  - selector limits, fallback ordering, truncation semantics, and validation examples.
- Read-path guarantees remain side-effect free (`head`, `history`, `views/evaluate`).

## Security and policy posture
- Mutating endpoints remain bearer-token protected in local auth modes.
- Schema contracts (`required_keys`) are validated on write/promote and map violations to `400 validation_error`.
- Promote semantics preserve explicit user-controlled transitions into `user/*`.

## Determinism contract updates
- Collection responses require deterministic ordering.
- View selectors use explicit sort keys; fallback sort is `(namespace,key,revision)`.
- Limit truncation occurs after deterministic sort.

## Canonical references
- API spec: [`docs/SPECS/API.md`](../API.md)
- Views spec: [`docs/SPECS/VIEWS.md`](../VIEWS.md)
- Storage constraints: [`docs/SPECS/STORAGE.md`](../STORAGE.md)
