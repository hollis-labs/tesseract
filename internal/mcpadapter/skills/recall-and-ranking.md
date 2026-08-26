---
name: recall-and-ranking
description: The four ranking modes — activation, chronological, similarity, relevance (RRF) — plus search_mode, payload_mode, budgets and paging, estimate_only, similarity_min, and the recall/use/touch loop that feeds activation.
scope_hint: memory:read
related: [memory, revisions]
---

# Recall and ranking

`memory_recall` and `tesseract_lookup` share one ranking surface. Pass `ranking=<mode>`; the default is `relevance` when a `query` is provided, otherwise `activation`. Under `relevance`, `search_mode` picks which retrieval arms run.

## Four modes

- **`activation`** — combines recency, reinforcement, and confidence. Best when you have no query and want "what's top-of-mind." Reinforcement comes from `tesseract_touch` and the deliberate-read paths, never from recall itself — see [Access reinforcement](#access-reinforcement--recall--use--touch) below, because this mode is only as good as what callers report back. Activation decay is a stable, empirically-tuned formula — don't tune without data.
- **`chronological`** — newest first, no scoring. Use when you want a timeline or an audit-style scan.
- **`similarity`** — pure cosine similarity between the query embedding and each candidate's stored vector. Requires `query` to be set and target revisions to be embedded. Unembedded revisions are silently skipped.
- **`relevance`** — RRF fusion of BM25 (keyword) and cosine (semantic). Best default for "search for this topic." Surfaces fresh, pre-embedding memories via the BM25 arm that similarity-only would miss.

## `search_mode` — which signal answers the query

`relevance` runs two retrieval arms and fuses them. `search_mode` says how many of them to run. It applies **only** under `ranking=relevance`; passing it with another ranking is a `validation_error`.

| `search_mode` | What runs | Ordered by |
|---|---|---|
| `hybrid` | BM25 + cosine, fused by RRF, then weighted by status, origin, confidence, recency and activation | fused score |
| `lexical` | BM25 alone | `bm25()` — raw match strength, no weighting |
| `semantic` | cosine alone | cosine similarity |

The default is `hybrid`, which is what every caller got before the knob existed.

**Reach for `lexical` when you know the exact string.** A ticket ID (`CW-20260519-0032`), a function or symbol name, a dotted or slashed path, a namespace. Semantic similarity is the wrong tool for an identifier: it returns things that *mean* something like your query, and an identifier means nothing — it only matches. Fusion blurs the one exact hit in among its semantic neighbours, and the weighting can then push it further down, because a low-confidence draft that happens to be the right answer scores below a canonical entry that merely shares its tokens.

**What `lexical` binds is a punctuation-joined run**, as an adjacent phrase: `CW-20260519-0032` finds the entry containing that identifier, not the entries that mention `CW`, `20260519` and `0032` in unrelated places.

**What it does not do is multi-word phrase search.** Space-separated words are each *required to appear*, not required to appear together:

| query | rows | what it is |
|---|---|---|
| `sqlite NOT NULL` under `lexical` | 2 | the three words, anywhere |
| the phrase `"NOT NULL"` | 6 | not expressible here |

Whitespace carries no signal about which of those a caller meant, so neither is inferred. If you need the phrase, `lexical` is not the tool yet — say so rather than reading an empty result as absence.

Two more things `lexical` does differently from `hybrid`: `AND`, `OR` and `NOT` are matched as **literal words**, not operators (under `hybrid` they *are* operators); and a query containing a non-ASCII letter is a `validation_error` rather than an empty page, because lexical tokens are `[A-Za-z0-9_]` only and no lexical query can name the token the index actually holds. A query of only punctuation is refused for the same reason.

`lexical` results carry **no `score`**. Order is the signal — `bm25()` is lower-is-better, and reporting it in a field that is higher-is-better everywhere else would invert its meaning for one mode.

**Reach for `semantic` when you know the corpus will not use your words.** It is a `similarity_unavailable` error when no embedder is configured — it never quietly falls back to keyword matching, because keyword results labeled semantic are worse than being told. `hybrid` does fall back to its BM25 arm in that situation, which is how freshly-written, not-yet-embedded memories stay reachable.

## Result shape and `score`

Both tools answer with an envelope. `memory_recall` returns `{results, manifest}`; `tesseract_lookup` returns `{results, facets, manifest}`. Inside `results`, every entry is `{revision, score}`, best first.

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
| `relevance` + `search_mode=semantic` | cosine similarity only |
| `relevance` + `search_mode=lexical` | **absent** |
| `chronological` | **absent** |

Under `chronological` the field is omitted rather than set to a sort key. Ordering is already carried by array order plus `revision.created_at`, so a score there would only restate the timestamp in units no other mode uses. Read the order, or read `revision.created_at`.

Do not threshold on `score` across modes, and do not persist it — it describes one ranking of one candidate set.

## `manifest` — what you did not get

Both tools carry a `manifest` next to `results`. **Never infer completeness from the array length** — the manifest is the only thing that tells you whether you got everything.

| Field | Meaning |
|---|---|
| `results_total` | Rows that matched, before `limit` and any budget. Under `relevance` this is bounded by the per-arm candidate cap, so read it as a candidate count, not a corpus count. |
| `results_returned` | Rows in this response. |
| `bytes_returned` / `tokens_estimate` | Size of the `results` array — the quantity `budget_bytes` / `budget_tokens` bound. |
| `truncated` | `false` means you got everything. Always present. |
| `truncation_reason` | `budget_bytes`, `budget_tokens`, `limit`, or `payload_mode_limit_cap`. Empty when `truncated` is `false`. |
| `next_cursor` | Pass it back as `cursor` to continue. `null` means nothing is left. |

`truncated`, `truncation_reason` and `next_cursor` are three readings of one fact and never disagree.

## Bounding a read

- **`budget_bytes` / `budget_tokens`** cap the serialized `results` array. Omit for no ceiling (the deployment may set one). At least one result always comes back even if it alone exceeds the budget, so paging can still make progress.
- **`limit`** is the page size: default 30, max 500 under `keys` and `summary`, **max 100 under `full`** — full carries bodies and costs roughly ten times as much per result. Asking for more is clamped, never silently: you get `truncation_reason: "payload_mode_limit_cap"` and a cursor, so the rest is reached by paging rather than by raising `limit`.

## Paging

Pass `manifest.next_cursor` back as `cursor`. Loop until `next_cursor` is `null`.

A cursor is bound to the ordering that issued it. Resuming it after changing `ranking`, `search_mode`, `namespaces`, `revision_scope`, `query`, any filter, or the reranker is a `validation_error` — not a plausible-looking wrong page. Changing `payload_mode` or `limit` mid-page is fine; neither reorders anything.

A cursor is an offset into a re-derived ordering, not a stable key. On a corpus that changes between pages — or under `activation`, whose scores move with wall-clock time — a row can be seen twice or missed. Within one pass over a settled corpus, paging is exact.

Paging is not available together with a reranker: a reranker reorders within a page, so a position in the ranked ordering does not name a position in what was delivered. That combination is refused rather than silently mis-paged.

### The linear log

`ranking=chronological` + `payload_mode=summary` + `cursor` **is** the episodic log. There is no separate log tool because this composition already is one: strictly newest-first across page boundaries, every entry exactly once, terminating on `next_cursor: null`, at roughly 700 bytes per entry.

```
memory_recall namespaces=[...] ranking=chronological payload_mode=summary limit=200
  -> read results, then repeat with cursor=manifest.next_cursor until it is null
```

## When to use each

| Question | Ranking |
|---|---|
| "What do I know about X?" | `relevance` |
| "Where did we record CW-20260519-0032?" | `relevance` + `search_mode=lexical` |
| "Which entries mention `fetchBM25Candidates`?" | `relevance` + `search_mode=lexical` |
| "Find this exact multi-word error message." | not yet — `lexical` requires the words, not the phrase |
| "Something about retries backing off, I forget the wording." | `relevance` + `search_mode=semantic` |
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

## Access reinforcement — recall → use → touch

**Recall results are unreinforced.** Recall does not bump `activation`, and that is deliberate: being returned by a search is the ranker's guess, not a deliberate read, and letting the ranker's guesses self-reinforce turns popular-because-returned into actually-useful within a few cycles.

So the loop is three steps, and this is the default shape of a turn — not an option:

```
1. memory_recall namespaces=[...] query="..."      -> results, each carrying revision_id
2. read / hydrate / do the work of the turn
3. tesseract_touch revision_ids=["01HXA..."]       -> the ones that actually shaped it
```

Step 3 is what tells the ranking your guess was right. It is the **only** input activation has, and `activation` is the default ranking whenever you recall without a query — so a corpus nobody touches ranks by nothing.

**Timing is the whole point.** Call `tesseract_touch` *after* the reasoning, not when the results arrive. Touching on arrival reinforces the guess at the moment it was made, which is the failure recall refuses to commit for you.

**Touch only what genuinely shaped the turn. Under-reporting is fine; over-reporting is worse than silence, because it teaches the ranking that noise is signal.** Little is gained by inflating: reinforcement closes a fixed fraction of the distance to a ceiling rather than adding a fixed amount, so repeated touches approach that ceiling with ever-smaller steps and never pass it. A memory you genuinely use through the day does end up ranking above one merely written today — that headroom is the point — but it gets there by being used, and a report padded with things you did not use spends the ranking's only signal on noise.

Each distinct memory named is reinforced once. Repeating a revision ID, or naming two revisions of the same memory, counts once. Revision IDs from `tesseract_lookup` work here too: memory and knowledge both resolve.

`memory_get` and `memory_get_revision` reinforce on their own — resolving a known key or pulling a specific revision by ID is already deliberate. `tesseract_touch` covers the rest: the hits whose summary alone was enough, and the distinction between what you read and what you used.

Activation **decay** runs on a schedule and is unchanged by any of this. Untouched memories fall toward a floor; touched ones settle wherever reinforcement and decay balance.

## `estimate_only` — size a read before paying for it

Pass `estimate_only=true` to get the envelope without the results. `memory_recall` answers `{manifest, estimate_only: true}`; `tesseract_lookup` answers `{facets, manifest, estimate_only: true}` — its own envelope, minus `results`. There is no `results` key at all, so an absent array means withheld, never empty.

The numbers are **exact, not approximate**: `results_total`, `results_returned`, `bytes_returned`, `tokens_estimate` — and every facet count on lookup — are what the identical call without the flag returns, under the same `payload_mode`. Because `bytes_returned` depends on `payload_mode`, estimate under the mode you intend to read at.

It is worth most where a read would be cut short. Under `budget_bytes` or `budget_tokens` the estimate carries the same `truncated`, `truncation_reason` and `next_cursor` the real read would, and that `next_cursor` is a valid cursor for the real read — the flag changes what is serialized, never which rows match or in what order.

## `similarity_min` — a floor on how close a match must actually be

A floor on cosine similarity between your query and each result. Results below it are dropped **before** `limit` applies, so it narrows the qualifying set rather than thinning a page of it. Range `[-1, 1]`; outside that is a `validation_error`.

Omitting it is not the same as passing `0.0`. The floor is **inclusive** — a result clears it at a score equal to it, as `confidence_min` does — so a floor of `0.0` keeps results at exactly `0` (orthogonal to your query) and drops only those scoring below it (opposed to it), while omitting the floor keeps the negative ones too.

It is only honored where cosine is the score: `ranking=similarity`, or `ranking=relevance` with `search_mode=semantic`. Passing it anywhere else — including the default `hybrid`, whose score is an RRF fusion rather than a similarity — is a `validation_error` rather than a silently ignored knob.

**Not the same as `confidence_min`**, which filters on the confidence the author recorded when writing the memory. A result can match your query closely and still have been written tentatively.
