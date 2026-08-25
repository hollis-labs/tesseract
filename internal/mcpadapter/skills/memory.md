---
name: memory
description: When to use the memory domain - patterns, required fields, keyed vs. unkeyed.
scope_hint: memory:read
related: [namespaces, revisions, recall-and-ranking, promotion]
---

# Memory domain

The memory domain is for **agent-authored content you'll want to recall later**: observations, preferences, session notes, distilled understanding. Every write is append-only and revisioned; recall is multi-knob.

## When to use memory

- Something the agent noticed and should remember.
- A user preference stated explicitly.
- A session summary worth carrying forward.
- Anything you might want to find later by activation, similarity, or hybrid relevance.

## When NOT to use memory

- **External content you're referencing.** Use knowledge (`knowledge_write`) - the pointer-first model preserves provenance.
- **Generic state records.** Use `context_write` - memory has specific lifecycle semantics (activation, promotion, dedup) you don't need for plain records.
- **Ephemeral session scratch.** Write to session-scoped memory (`user/{id}/session/{sid}/memory/{type}`) when you want promotion later; use app context records (`app/{id}/session/*`) when you just want ephemeral scratch.

## Required fields on memory_write

From the `memory_write` MCP declaration:

- `namespace` (required) - must parse as a typed memory namespace: `user/{id}/memory/{type}`, `user/{id}/project/{pid}/memory/{type}`, or `user/{id}/session/{sid}/memory/{type}`. Allowed types: `decisions`, `feedback`, `followups`, `learnings`, `limitations`, `notes`, `outcomes`, `references`. Use `notes` as the default catch-all when no stronger type fits. See `tesseract_skills namespaces` for the per-type meaning.
- `author_agent_id` (required)
- `trigger` (required) - one of `explicit`, `post_compact`, `per_turn`, `promotion`, `manual`.
- `session_id` (required)
- `origin` (required) - one of `user`, `feedback`, `project`, `reference`, `observation`.
- `confidence` (required) - float in `[0, 1.0]`.
- `payload_summary` (required)

Optional: `memory_key`, `supersedes`, `status` (`draft`|`reviewed`|`canonical`; default `draft`), `author_version`, `tags` (JSON array), `ttl_seconds`, `payload_body`, `dedup` (`none`|`semantic`), `dedup_threshold`.

## Core flow

1. **Write** - `memory_write`.
2. **Recall** - `memory_recall`. Default ranking is `relevance` (RRF) when `query` is set, otherwise `activation`. Other modes: `chronological`, `similarity`. See `tesseract_skills recall-and-ranking`.
3. **Get head** - `memory_get` returns the current (non-deprecated) revision for `(namespace, memory_key)`.
4. **Get revision** - `memory_get_revision` fetches by `revision_id`.
5. **Get history** - `memory_history` returns the full revision chain for a keyed memory, newest first.
6. **Supersede** - pass `supersedes=<revision_id>` on write to mark an explicit ancestor; the old revision is auto-deprecated.
7. **Deprecate** - `memory_deprecate` when a revision is wrong or outdated. Soft; history survives.

**Reinforcement.** Deliberate reads — `memory_get` and `memory_get_revision` — reinforce a memory's `activation` and `access_count`. `memory_recall` does not: search results are guesses, so they don't count as "use." See `tesseract_skills recall-and-ranking`.

## Keyed vs. unkeyed

- **Keyed memory** - a stable `memory_key` that represents an evolving concept (e.g. `user.prefs.style`). Re-writing the key creates a new revision; `memory_get` returns the current head.
- **Unkeyed memory** - no `memory_key`; each write stands alone. Use for observation streams where no stable identity exists. Note: `memory_get` / `memory_history` require a key; unkeyed memories are reachable via `memory_recall` and `memory_get_revision`.

## Promotion

Session-scoped memories can be promoted to user or project scope via `memory_promote` (the shortcut). Source and target must carry the same `{type}` segment — promote is a scope change, not a re-classification. The source is deprecated; the promoted revision lands in the target namespace with `trigger=promotion`. See `tesseract_skills promotion`.
