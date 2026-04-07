# MVP Spec — Context Memory Service

Status: draft (Task 2)

## Goal
Provide a local-first context service that gives agents/apps deterministic read/write access to namespaced context while protecting canonical user-owned context.

## In scope
- Local storage model: SQLite index + file-backed records.
- Context records with provenance and revision history.
- Namespace model:
  - `user/*` reserved for canonical user context.
  - `app/<client-id>/*` writable by that client.
- Interfaces:
  - CLI for local operations and admin tasks.
  - HTTP API for programmatic access.
  - GUI hooks (schema-level contracts only in MVP).
- Deterministic views/selectors for retrieval.

## Out of scope
- Cloud sync and multi-device conflict resolution.
- LLM-based inference or auto-merge in read paths.
- Cross-host tenancy or remote auth providers.

## Functional requirements
1. Create, append, and read context records in a namespace.
2. Resolve a namespace key to a deterministic head revision.
3. List history for a key with provenance metadata.
4. Evaluate deterministic view selectors over indexed metadata.
5. Enforce namespace write policy and `user/*` protection.

## Non-functional requirements
- Local operations should remain available offline.
- Record retrieval order must be deterministic for identical inputs.
- Every write must include actor and timestamp provenance.

## Success criteria
- A developer can build CLI/API handlers without inventing schema fields.
- Namespace permissions are explicit and testable.
- View semantics are selector-based and deterministic.
