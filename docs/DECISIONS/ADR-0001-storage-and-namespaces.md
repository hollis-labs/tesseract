# ADR-0001: Storage and Namespaces

Date: 2026-02-25
Status: accepted

## Context
The Context Memory Service must provide local-first persistence with deterministic retrieval while enforcing ownership boundaries between user-owned context and app-owned context.

Architectural constraints from project scope:
- canonical storage approach is SQLite index + file-backed payloads
- multiple local clients will read/write context
- `user/*` context must be protected from direct app writes

## Decision
1. Storage model
- Persist payloads as append-only file records.
- Maintain metadata and heads in SQLite for deterministic queries.
- Treat file payloads as source of truth and SQLite as the indexed selection layer.

2. Namespace model
- Reserve `user/*` for canonical user-owned context.
- Assign app namespaces as `app/<client-id>/*`.
- Deny cross-client writes across app namespaces by default.

3. Protected write policy
- Direct writes to `user/*` require explicit user-approved promotion flow.
- App writes may propose data for user promotion but cannot mutate `user/*` directly.

4. Retrieval determinism
- History order is defined by monotonic revision per `(namespace,key)`.
- Head retrieval is defined by single authoritative head entry in index.
- View evaluation must return stable ordering under equal inputs.

## Consequences
Positive:
- Clear boundary between canonical user context and app memory.
- Deterministic retrieval enables reliable agent behavior and repeatable tests.
- Append-only records preserve provenance and auditability.

Trade-offs:
- Dual-layer storage requires consistency checks between file payloads and index rows.
- Promotion workflow introduces extra steps for updates into `user/*`.

Follow-up:
- Specify API/CLI promotion flows and error semantics.
- Define selector grammar and ordering rules for views.

## Related docs
- `docs/SPECS/MVP.md`
- `docs/SPECS/STORAGE.md`
- `docs/SPECS/API.md`
- `docs/SPECS/CLI.md`
