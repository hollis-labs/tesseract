# Roadmap Plan (MVP -> v0.2)

Date: 2026-02-25

## Phase 0 — Bootstrap (complete)
- Repository scaffolding and baseline doc structure.
- Volon orchestration state initialized.

## Phase 1 — MVP Specs (current)
- Define scope, architecture, storage, API, and CLI contracts.
- Define namespace ownership and permissions, including protected `user/*`.
- Record compatibility-sensitive decisions in ADRs.

## Phase 2 — Pivot Alignment
- Generalize from personal cache framing to context registry + working memory.
- Introduce explicit view selector model as first-class retrieval contract.
- Update API/CLI semantics to be multi-client and deterministic.

## Phase 3 — Implementation v0.1
- Implement storage primitives (append + head + history).
- Implement API handlers and CLI parity.
- Add deterministic selector evaluator.
- Add policy enforcement tests for namespaces and protected writes.

## Phase 4 — v0.2 Hardening
- Add import/export and backup primitives.
- Add richer provenance querying.
- Evaluate local auth hardening for API surface.
- Add GUI-facing view presets and moderation workflows.
