# Cortex “Context Types” — architecture and implementation plan
Date: 2026-03-04

## Problem statement
Agents and humans need different classes of context at different times:
- **Task execution**: concrete technical details, interfaces, invariants, current constraints.
- **Project strategy**: goals, tradeoffs, priorities, what exists, what is allowed, what is next.
- **Long-lived canon**: decisions, contracts, system maps, principles.
Without an explicit typing/lifecycle model, retrieval tends to overfetch and prompts become noisy.

## Design goals
- Deterministic retrieval (“give me the minimum context for *this* purpose”).
- Strong ownership boundaries (app namespaces; human vs agent writes).
- Lifecycle controls (draft → reviewed → canonical; TTL for volatile).
- Low storage duplication (store pointers; preserve provenance).
- Extensible types without turning into taxonomy sprawl.

## Core model

### 1) ContextItem (stored in Cortex)
A minimal record with typed metadata and content-as-summary.

**Fields**
- `id`: ULID/UUIDv7
- `namespace`: e.g. `app:carrier`, `user:chrispian`, `system:cortex`
- `type`: one of core types or `custom/<ns>/<name>`
- `purpose_tags`: `["task_exec", "strategy", "briefing", "governance"]`
- `title`
- `summary`: short, model-friendly representation (primary payload)
- `pointers`: list of references (repo paths, commit SHAs, URLs, file paths, object ids)
- `owners`: `["user"]`, `["app:carrier"]`, etc.
- `visibility`: `private|shared` (local-only still; used for policy)
- `status`: `draft|reviewed|canonical|deprecated`
- `ttl`: optional expiration for volatile items
- `version`: semantic version or monotonically increasing integer
- `created_at`, `updated_at`
- `provenance`: `{"source": "...", "source_id": "...", "hash": "...", "generated_by": "..."}`

### 2) ContextType registry
Types have policy and retrieval hints.

**ContextType fields**
- `type_id`: e.g. `strategy/goal`, `task/spec`, `contract/api`, `decision/adr`
- `default_ttl`: optional
- `allowed_statuses`
- `required_fields`: e.g. `pointers` required for `task/spec`
- `max_summary_bytes`
- `promotion_rules`: e.g. `draft -> reviewed requires human approval`
- `retrieval_rank_bias`: e.g. decisions > notes for strategy

### 3) Retrieval views (the key)
Instead of “search everything”, use view presets.

**View** = `{purpose, allowed_types, max_items, max_bytes, rank_weights}`

Examples:
- `view:task_exec`:
  - types: `task/spec`, `contract/api`, `contract/data`, `decision/adr`, `runbook`, `system/map`
- `view:strategy`:
  - types: `strategy/goal`, `strategy/constraints`, `strategy/roadmap`, `decision/adr`, `system/map`
- `view:briefing`:
  - types: `brief/summary`, `brief/rationale`, `decision/adr`, `system/map`
- `view:agent_boot`:
  - types: `system/map`, `principles`, `constraints/global`, `contract/*`

## Suggested core types (MVP set)
Keep these small; they cover most needs.

### Strategy
- `strategy/goal`
- `strategy/constraints`
- `strategy/roadmap`
- `system/map` (what exists + how it fits)

### Execution
- `task/spec`
- `runbook`
- `contract/api`
- `contract/data`

### Knowledge artifacts
- `decision/adr`
- `brief/summary`
- `note/volatile` (TTL by default)

### Governance (optional early)
- `principles`
- `constraints/global`

## Promotion + lifecycle
### Status progression
- `draft` (agent or human)
- `reviewed` (human approved or policy-approved)
- `canonical` (default retrieval preference)
- `deprecated` (kept for history but low-rank)

### TTL defaults
- `note/volatile`: 7–30 days
- `task/spec`: until closed + 30 days
- `brief/summary`: 90 days
- `decision/adr`: no TTL

## Storage + indexing
- Persist ContextItems in SQLite (or existing Cortex store).
- Index fields:
  - `(namespace, type, status)`
  - full-text index on `title`, `summary`

## API surface (minimal)
### Write
- `PUT /context-items` (create)
- `PATCH /context-items/{id}` (update)
- `POST /context-items/{id}/promote` (draft->reviewed->canonical)
- `POST /context-items/{id}/deprecate`

### Read
- `GET /context-items/search?q=...&types=...&namespace=...`
- `GET /context-views/{view_id}?q=...&namespace=...` (recommended default)
- `GET /context-items/{id}`

## Agent integration patterns
### Pattern A: “Ask for a view”
Agents request `{view_id, objective, constraints}` and receive a bounded bundle.

### Pattern B: “Propose then promote”
Agents write drafts; humans promote to reviewed/canonical when appropriate.

### Pattern C: “Pointer-first”
Raw docs stay outside Cortex; Cortex stores summaries + pointers + provenance hashes.

## Implementation plan (sprints)
### Sprint 1 — Types + views foundation
- Add `type`, `status`, `ttl`, `version`, and `pointers` to ContextItem storage.
- Create a config-backed ContextTypeRegistry.
- Implement view-based retrieval (`task_exec`, `strategy`) with bounded results.
- Add TTL handling for `note/volatile`.

### Sprint 2 — Promotion workflow + guardrails
- Implement promote/deprecate operations.
- Add policy checks (namespace write rules; promotion permissions).
- Add provenance recording.

### Sprint 3 — Tooling + ergonomics
- CLI helpers for add/view/promote.
- Optional “context pack” export (bounded JSON bundle for agents).

### Sprint 4 — Ranking + evaluation
- Add rank weights per type/status.
- Add evaluation harness (bundle size, relevance, token budget).

## Carrier integration notes
- Carrier can write: `brief/summary`, `note/volatile` automatically.
- Carrier proposes: `decision/adr` drafts for human promotion.
- Carrier retrieves: `view:task_exec` vs `view:strategy` deterministically.
