# Context Memory Service Scope

Status: pivot-aligned draft (Task 4)

## Product definition
The service is a general context registry and working-memory store for user and app clients.

- Personal cache (`user/*`) is a first-class namespace.
- App/agent context is first-class under `app/<client-id>/*`.
- Retrieval is context-aware through deterministic views/selectors.

## MVP goals
- Local-first append-only context storage.
- Namespace-safe writes with explicit ownership boundaries.
- Deterministic reads for head, history, and view selectors.
- Provenance and revision tracking on every write.

## Non-goals
- Automatic semantic merging or synthesis of records during retrieval.
- Hidden mutation side effects in read operations.
- Distributed consensus or cloud synchronization.

## Policy boundaries
- Apps write only to owned app namespaces.
- `user/*` is protected and requires explicit promotion/approval for updates from app contexts.
- PCC remains canonical for agent/session orchestration state; this service complements PCC with application-facing context storage.
