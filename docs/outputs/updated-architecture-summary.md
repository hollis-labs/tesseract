# Updated Architecture Summary

Status: pivot-aligned summary

This summary captures the architecture shift to a generalized context registry + working-memory store. Canonical definitions remain in:
- [`docs/SCOPE.md`](../SCOPE.md)
- [`docs/ARCHITECTURE.md`](../ARCHITECTURE.md)
- [`docs/SPECS/STORAGE.md`](../SPECS/STORAGE.md)
- [`docs/DECISIONS/ADR-0002-generalized-context-registry.md`](../DECISIONS/ADR-0002-generalized-context-registry.md)

## Pivot intent
- Support multiple local clients and tools under one context model.
- Keep `user/*` as a protected first-class namespace.
- Maintain deterministic retrieval behavior through selector-based views.

## Core architecture elements
- Append-only records for revision history.
- Heads index for current `(namespace,key)` resolution.
- Namespace ownership policies for actor enforcement and protected promotion flow.
- Deterministic view evaluation for context-aware retrieval.
- Audit log for write/promote traceability.

## Responsibility boundaries
- API + CLI layers expose equivalent contracts for write/read/promote/view operations.
- Storage layer enforces deterministic ordering, revision integrity, and policy persistence.
- View model acts as selector/evaluator only; it does not summarize or mutate payload semantics.

## Determinism and policy highlights
- Read/view paths are side-effect free.
- Selector fallback sort remains `(namespace,key,revision)` when order is omitted.
- Promotion into `user/*` is explicit and user-scoped.
- Policy/schema guardrails map violations to deterministic validation errors.

## Integration path note
- MCP adapter remains a thin translation layer to the same core contracts (no separate state model).
