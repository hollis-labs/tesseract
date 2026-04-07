# Pivot Open Questions

Status: draft

This document tracks unresolved pivot items and proposed next actions. Canonical references:
- [`docs/ARCHITECTURE.md`](../ARCHITECTURE.md)
- [`docs/SPECS/API.md`](../SPECS/API.md)
- [`docs/SPECS/VIEWS.md`](../SPECS/VIEWS.md)
- [`docs/DECISIONS/ADR-0002-generalized-context-registry.md`](../DECISIONS/ADR-0002-generalized-context-registry.md)

## 1) MCP adapter boundary and rollout
- Question: Should the initial MCP adapter expose the full write/promote surface or start read/view-only?
- Impact: Determines external integration risk, auth requirements, and review scope.
- Proposed owner: Architecture + API maintainers.
- Next step: Draft `docs/SPECS/MCP.md` with phased capability model and threat assumptions.

## 2) Auth posture for read/view endpoints
- Question: Do read/view endpoints remain unauthenticated beyond MVP, or move to optional token-gated mode?
- Impact: Affects local UX, security defaults, and CLI/MCP compatibility.
- Proposed owner: API maintainers.
- Next step: Add explicit post-MVP auth modes and migration notes in API spec.

## 3) Selector extension policy
- Question: How are new selector fields introduced without breaking determinism guarantees?
- Impact: Impacts backward compatibility and contract test maintenance.
- Proposed owner: Views spec owner.
- Next step: Define extension policy (feature flags/versioning) in `docs/SPECS/VIEWS.md`.

## 4) Namespace policy granularity
- Question: Do policy contracts need per-key capabilities in addition to namespace-level ownership?
- Impact: Changes write/promote authorization model and CLI/API payload schemas.
- Proposed owner: Storage + policy maintainers.
- Next step: Prototype policy schema options in ADR follow-up before implementation.

## 5) Audit retention defaults
- Question: What default retention should balance traceability and storage growth for local-first usage?
- Impact: Affects operational cost and compliance posture for teams.
- Proposed owner: Storage/runtime maintainers.
- Next step: Add retention decision proposal linked to compaction behavior in storage docs.
