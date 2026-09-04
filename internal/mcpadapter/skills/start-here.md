---
name: start-here
description: Orientation for agents new to Tesseract — the three domains, invariants, and how to use tesseract_skills.
scope_hint: none
related: [namespaces, memory, knowledge]
---

# Tesseract — start here

Tesseract is a local-first, append-only context and memory service. You reach it through the `mcp__tesseract__*` tool family. Everything you write is revisioned, auditable, and namespace-owned.

## The three domains

- **Memory** — agent-authored observations, preferences, session notes. Recall by activation, chronological order, semantic similarity, or hybrid relevance. Start with `tesseract_skills memory`.
- **Knowledge** — pointer-first references to external content (packages, docs, notes). Every knowledge write carries `kind`/`source`/`pointer` facets. Start with `tesseract_skills knowledge`.
- **Context** — generic revisioned records for app-scoped state (session workspaces, typed payloads, packets). Used heavily by framework tooling; agents typically reach for memory or knowledge instead.

Search across memory + knowledge with `tesseract_recall` — the unified query surface.

## Invariants (don't fight these)

- **Append-only.** Every write creates a new revision. Nothing is mutated in place.
- **Namespace-owned.** `user/*` is user-owned (write-protected except via promotion). `app/*` is app-owned. See `tesseract_skills namespaces`.
- **Deterministic.** Identical selectors against identical state return identical results.
- **Audited.** Context writes and promotions are logged today; memory and knowledge write audit is in flight (see `tesseract_skills audit`). Use `tesseract_skills audit` to query.
- **Views are selectors, not processors.** Retrieval does not synthesize, merge, or infer.

## How to use `tesseract_skills`

- `tesseract_skills` with no args — returns this index.
- `tesseract_skills` with `name=<skill-name>` — returns the full body of a single skill.

Skills are progressive: the index is small; bodies only load when requested.

## Common next steps

- Writing an agent memory? → `tesseract_skills memory`
- Recording a reference to external content? → `tesseract_skills knowledge`
- Looking something up? → use `tesseract_recall` directly, then **close the loop**: hydrate chosen hits with `tesseract_get_revision`, and after reasoning pass projected hits that shaped the turn to `tesseract_touch`. Recall itself does not reinforce; deliberate gets reinforce once, while touch reports use that happened without a fetch (or adds an intentional second signal). `tesseract_skills recall-and-ranking` for ranking modes.
- Working across user/app namespace boundaries? → `tesseract_skills promotion`.
- Booting into a project? → `tesseract_skills context-packet`.
