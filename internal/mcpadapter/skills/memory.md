---
name: memory
description: When to use the memory domain - the recall/use/touch loop, patterns, required fields, keyed vs. unkeyed.
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

## The read loop: recall → use → touch

**This is the default shape of a turn that consults memory, not one option among several.** Three steps, and the third is the one that is easy to skip and expensive to skip.

```
1. tesseract_recall namespaces=["user/chrispian/memory/decisions"] query="sqlite pragma handling" limit=10
     -> {results: [{revision: {revision_id: "01HXA...", ...}, score}, ...], manifest}

2. read the summaries, hydrate the two that look right:
   tesseract_get_revision revision_id=01HXA...
   tesseract_get_revision revision_id=01HXC...
     -> full bodies; do the actual work of the turn

3. now that you know which ones mattered:
   tesseract_touch revision_ids=["01HXA..."]
```

Note what step 3 does **not** contain. Ten revisions came back, two were read, one actually shaped the answer — so one is what gets reported.

**Why the third step exists at all.** `tesseract_recall` returns results **unreinforced**. Being returned by a search is the ranker's guess about what you need; if that guess reinforced itself, popular-because-returned would beat actually-useful within a few cycles. `tesseract_touch` is you telling the ranking, after the reasoning, which guesses were right. That is the only input activation has, and activation is the default ranking whenever you recall without a query.

**Touch only what genuinely shaped the turn. Under-reporting is fine; over-reporting is worse than silence, because it teaches the ranking that noise is signal.** There is little to win by inflating: reinforcement has diminishing returns — each touch closes a fraction of the remaining distance to a ceiling, so the tenth touch moves a memory far less than the first, and no amount of touching passes the ceiling.

**When to call it.** After the work, not after the search. If you touch as soon as results arrive, you are reinforcing the guess at the moment it was made — the thing recall refuses to do, done manually.

**What counts as one touch.** Each distinct memory named is reinforced once: `activation` moves a fixed fraction toward its ceiling, `access_count` increments, `last_accessed_at` is set. Naming a revision twice, or naming two revisions of the same memory, reinforces it once. A recall spanning both domains is reportable in one call — memory and knowledge revision IDs both resolve.

`tesseract_get` under `domain="memory"` and `tesseract_get_revision` reinforce on their own, because resolving a known key or pulling a specific revision by ID is already a deliberate act. So step 2 above reinforces what you hydrated; step 3 is how you say which of those actually mattered, and how you report a memory whose summary alone was enough.

## The other operations

1. **Write** - `memory_write`.
2. **Get head** - `tesseract_get domain="memory"` returns the current (non-deprecated) revision for `(namespace, key)`. Reinforces.
3. **Get revision** - `tesseract_get_revision` fetches by `revision_id`. Reinforces.
4. **Get history** - `tesseract_history domain="memory"` returns the full revision chain for a keyed memory, newest first.
5. **Supersede** - pass `supersedes=<revision_id>` on write to mark an explicit ancestor; the old revision is auto-deprecated.
6. **Deprecate** - `tesseract_deprecate` when a revision is wrong or outdated. Soft; history survives.

Recall's ranking modes — `relevance` (the default when `query` is set), `activation` (the default without one), `chronological`, `similarity` — are covered in `tesseract_skills recall-and-ranking`, along with everything that bounds a read.

## Keyed vs. unkeyed

- **Keyed memory** - a stable `memory_key` that represents an evolving concept (e.g. `user.prefs.style`). Re-writing the key creates a new revision; `tesseract_get` returns the current head.
- **Unkeyed memory** - no `memory_key`; each write stands alone. Use for observation streams where no stable identity exists. Note: `tesseract_get` / `tesseract_history` require a key; unkeyed memories are reachable via `tesseract_recall` and `tesseract_get_revision`.

## Promotion

Session-scoped memories can be promoted to user or project scope via `memory_promote` (the shortcut). Source and target must carry the same `{type}` segment — promote is a scope change, not a re-classification. The source is deprecated; the promoted revision lands in the target namespace with `trigger=promotion`. See `tesseract_skills promotion`.
