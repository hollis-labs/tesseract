---
name: recall-and-ranking
description: The four ranking modes — activation, chronological, similarity, relevance (RRF) — and when to use each.
scope_hint: memory:read
related: [memory, revisions]
---

# Recall and ranking

`memory_recall` and `conduit_lookup` share one ranking surface. Pass `ranking=<mode>`; the default is `relevance` when a `query` is provided, otherwise `activation`.

## Four modes

- **`activation`** — combines recency, reinforcement, and confidence. Best when you have no query and want "what's top-of-mind." Activation decay is a stable, empirically-tuned formula — don't tune without data.
- **`chronological`** — newest first, no scoring. Use when you want a timeline or an audit-style scan.
- **`similarity`** — pure cosine similarity between the query embedding and each candidate's stored vector. Requires `query` to be set and target revisions to be embedded. Unembedded revisions are silently skipped.
- **`relevance`** — RRF fusion of BM25 (keyword) and cosine (semantic). Best default for "search for this topic." Surfaces fresh, pre-embedding memories via the BM25 arm that similarity-only would miss.

## When to use each

| Question | Ranking |
|---|---|
| "What do I know about X?" | `relevance` |
| "What are the most active memories right now?" | `activation` |
| "Show me the last 10 entries in this namespace." | `chronological` |
| "Find semantically similar memories to this text." | `similarity` |

## Filters

All rankings accept the same filter set:

- `namespaces` (JSON array)
- `origins`, `statuses`, `tags` (JSON arrays)
- `confidence_min` (0-1)
- `since` / `until` (RFC3339 bounds)
- `facet_kinds` / `facet_sources` (knowledge-aware, via `conduit_lookup`)

## Access reinforcement

Every recall mode reinforces access (widened from activation-only in v0.4.0). Recall counts as "use" even when ranking is chronological or similarity.
