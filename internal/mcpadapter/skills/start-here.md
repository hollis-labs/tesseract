---
name: start-here
description: Orientation for agents new to Vanta Conduit — the three domains, invariants, and how to use vanta_skills.
scope_hint: none
related: [namespaces, memory, knowledge]
---

# Vanta Conduit — start here

Vanta Conduit is a local-first, append-only context and memory service. You reach it through the `mcp__vanta__*` tool family. Everything you write is revisioned, auditable, and namespace-owned.

## The three domains

- **Memory** — agent-authored observations, preferences, session notes. Recall by activation, chronological order, semantic similarity, or hybrid relevance. Start with `vanta_skills memory`.
- **Knowledge** — pointer-first references to external content (packages, docs, notes). Every knowledge write carries `kind`/`source`/`pointer` facets. Start with `vanta_skills knowledge`.
- **Context** — generic revisioned records for app-scoped state (session workspaces, typed payloads, packets). Used heavily by framework tooling; agents typically reach for memory or knowledge instead.

Search across memory + knowledge with `conduit_lookup` — the unified query surface.

## Invariants (don't fight these)

- **Append-only.** Every write creates a new revision. Nothing is mutated in place.
- **Namespace-owned.** `user/*` is user-owned (write-protected except via promotion). `app/*` is app-owned. See `vanta_skills namespaces`.
- **Deterministic.** Identical selectors against identical state return identical results.
- **Audited.** Context writes and promotions are logged today; memory and knowledge write audit is in flight (see `vanta_skills audit`). Use `vanta_skills audit` to query.
- **Views are selectors, not processors.** Retrieval does not synthesize, merge, or infer.

## How to use `vanta_skills`

- `vanta_skills` with no args — returns this index.
- `vanta_skills` with `name=<skill-name>` — returns the full body of a single skill.

Skills are progressive: the index is small; bodies only load when requested.

## Common next steps

- Writing an agent memory? → `vanta_skills memory`
- Recording a reference to external content? → `vanta_skills knowledge`
- Looking something up? → use `conduit_lookup` directly; `vanta_skills recall-and-ranking` for ranking modes.
- Working across user/app namespace boundaries? → `vanta_skills promotion`.
- Booting into a project? → `vanta_skills context-packet`.
