---
name: recall-and-ranking
description: The four ranking modes — activation, chronological, similarity, relevance (RRF) — and when to use each.
scope_hint: memory:read
related: [memory, revisions]
---

# Recall and ranking

`memory_recall` and `tesseract_lookup` share one ranking surface. Pass `ranking=<mode>`; the default is `relevance` when a `query` is provided, otherwise `activation`.

## Four modes

- **`activation`** — combines recency, reinforcement, and confidence. Best when you have no query and want "what's top-of-mind." Activation decay is a stable, empirically-tuned formula — don't tune without data.
- **`chronological`** — newest first, no scoring. Use when you want a timeline or an audit-style scan.
- **`similarity`** — pure cosine similarity between the query embedding and each candidate's stored vector. Requires `query` to be set and target revisions to be embedded. Unembedded revisions are silently skipped.
- **`relevance`** — RRF fusion of BM25 (keyword) and cosine (semantic). Best default for "search for this topic." Surfaces fresh, pre-embedding memories via the BM25 arm that similarity-only would miss.

## Result shape and `score`

Every result is `{revision, score}`, best first. `memory_recall` returns the bare array; `tesseract_lookup` wraps it as `{results, facets}`.

How much of each result you get is set by `payload_mode`:

| `payload_mode` | Each result carries |
|---|---|
| `keys` | `revision.{revision_id, memory_id, domain, namespace, memory_key, created_at}` + `score`. The browse/enumerate shape. |
| `summary` | `keys` + `revision.{status, tags, confidence}` + `revision.payload.summary`. The default. |
| `full` | The whole revision including `revision.payload.body`, plus `state`. |

The default comes from server config (`read.payload_mode`); pass `payload_mode` to override it per call. `state` — activation, access counts, `current_revision` — rides **only** on `full`.

Work just-in-time: **recall → choose → hydrate.** Recall at the default to see what exists, pick the few hits that matter, then pass each `revision_id` to `memory_get_revision`. `revision_id` is present in every mode, so hydration is always available. Reaching for `payload_mode=full` to skip the third step is how one recall eats a context window.

Projected results (`keys`, `summary`) carry a `payload_mode` field; `full` results do not. That marker matters when you intend to **edit** what you read: `payload.body` is omitted both when it was withheld by projection and when it is genuinely empty, so the marker is the only thing that tells them apart. If it is present and not `full`, treat the body as unknown — re-request with `payload_mode=full` or hydrate by id. Never write back a body you never received.

`score` is **ranking-relative** — comparable against other results in the same response, and meaningless across responses or across modes. Its meaning per mode:

| Ranking | `score` |
|---|---|
| `activation` | activation strength — recency x reinforcement x confidence |
| `similarity` | cosine similarity between query and revision embeddings; legitimately 0 (orthogonal) or negative (opposite) |
| `relevance` | RRF-fused BM25 + cosine, weighted by status, origin, confidence, recency, and activation |
| `chronological` | **absent** |

Under `chronological` the field is omitted rather than set to a sort key. Ordering is already carried by array order plus `revision.created_at`, so a score there would only restate the timestamp in units no other mode uses. Read the order, or read `revision.created_at`.

Do not threshold on `score` across modes, and do not persist it — it describes one ranking of one candidate set.

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
- `facet_kinds` / `facet_sources` (knowledge-aware, via `tesseract_lookup`)

## Access reinforcement

Recall does **not** reinforce access. Being returned by a search is the system's guess, not a deliberate read — letting recall bump `activation` would let the ranker's own guesses self-reinforce into an echo chamber.

Reinforcement happens only on the deliberate-read paths: `memory_get` (resolve a known key) and `memory_get_revision` (pull a specific revision by ID). Those bump `activation`, `access_count`, and `last_accessed_at`. Activation **decay** is unchanged and still runs on a schedule.
