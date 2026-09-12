---
name: event
description: The event domain - the append-only narrative log. Reasoning in prose, not telemetry. Namespace grammar, the linear read path, and why recall does not search it by default.
scope_hint: memory:read
related: [memory, knowledge, recall-and-ranking, namespaces]
---

# Event domain

Event is the **append-only narrative log**: an agent's reasoning about what it is doing, and Chrispian's personal log and journal.

## It is not telemetry

This is the distinction that decides whether you are using the right system at all.

A trace records *that* something happened, how long it took, and how it was labeled. An event records *why* — what you were trying, what you chose against, what you noticed and did not act on. **The reasoning in prose is exactly what a trace discards**, and recovering it later is the entire reason this domain exists.

So:

- If what you are recording is a duration, a count, a status code or a structured span — this is the wrong tool, and Tesseract is the wrong system. Use a metrics pipeline.
- If a future session reading it would learn something it would otherwise have to re-derive — that is an event.

The test in one line: **would a trace already have this?** If yes, do not write it here.

## Event vs. memory vs. knowledge

All three are prose. They differ in what the prose *is*.

| | holds | shape |
|---|---|---|
| **memory** | a settled conclusion worth recalling on its own | keyed, revised, curated |
| **knowledge** | content someone will come back for by name — a canonical, a handoff, a playbook, a doc | addressed by key, facets |
| **event** | what happened and what you were thinking at the time | keyless, appended, read in order |

The practical fork: a decision you would want a future session to *apply* is memory. The reasoning that produced it — including the paths you rejected — is event. Writing the second does not excuse skipping the first; a log nobody distills is a log nobody reads.

## Namespace grammar

An event namespace has memory's shape with `event` in the domain-segment position:

```
user/{user_id}/event/{type}
user/{user_id}/project/{project_id}/event/{type}
user/{user_id}/session/{session_id}/event/{type}
```

`{type}` is a **closed vocabulary** naming the *stream*, not a taxonomy of what happened:

| type | for |
|---|---|
| `reasoning` | an agent's log of what it is doing and why |
| `journal` | a personal log |

Pick the scope by what you would read back as a unit:

- **Session scope** is the natural home for an agent's reasoning — `user/chrispian/session/session-20260910-85916030/event/reasoning`. One session's thinking is one linear read.
- **User scope** is the journal — `user/chrispian/event/journal`.
- **Project scope** is reasoning that spans sessions but belongs to one codebase — `user/chrispian/project/tesseract/event/reasoning`.

**Do not put dates in the path.** `created_at` is indexed and `event_list` filters on it, so date segments turn every time-range read into a multi-namespace query and buy nothing. Time is an attribute, not a partition.

A bare `user/chrispian/event` reads every stream under that scope — the same prefix shorthand memory namespaces have.

## Writing

`event_write` takes flat arguments. Required: `namespace`, `summary`, `author_agent_id`, `session_id`.

```json
{
  "namespace": "user/chrispian/session/session-20260910-85916030/event/reasoning",
  "summary": "Chose a dedicated log read over ranking=chronological",
  "body": "Recall's fetchCandidates issues no ORDER BY and no LIMIT — it loads every matching row into Go before windowing. Fine at 2k revisions, quadratic-feeling at log volumes. Also its cursor is an offset, and a log is appended at the head, so page 2 would repeat rows written between pages. Went with keyset pagination pushed into SQL. Rejected: adding a LIMIT to fetchCandidates, which would have changed recall's Total semantics for every caller.",
  "author_agent_id": "claude-code",
  "author_version": "claude-opus-5",
  "session_id": "session-20260910-85916030"
}
```

**`body` is the field the domain exists for.** A summary on its own is a log line, which is what a trace already gives you.

### Keyless by default

`key` is optional and you should usually omit it. A memory is a thing you *revise* — it has an identity and a current value. A log entry is a thing that *happened*: it has a timestamp, and superseding it would be rewriting the record of what you were thinking.

Pass a key only when an entry genuinely has a revisable identity — a running journal page for one day, say. Keyed event writes are held to the memory domain's lowercase dot-notation vocabulary.

### Fixed at the write path

Every event write is stamped `domain=event` and `status=canonical`. Canonical, not the `draft` every other write path defaults to: the `draft → reviewed → canonical` ladder tracks how settled a *claim* is, and a log entry makes no claim to settle. It was as true when written as it will ever be.

`derived_from` defaults to `observation` and `trigger` to `manual`; both are overridable. `confidence` defaults to `0.9`.

### Over HTTP

`POST /v1/event/write` writes the same revision, with `author` nested and `tags` a real array — the same MCP-flat / HTTP-nested split knowledge has.

```bash
curl -sS -X POST "$TESSERACT_URL/v1/event/write" \
  -H "Authorization: Bearer $TESSERACT_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "namespace": "user/chrispian/event/journal",
    "summary": "Settled the event namespace grammar",
    "body": "Went with memory'"'"'s shape rather than knowledge'"'"'s deep hierarchy.",
    "author": {"agent_id": "chrispian", "agent_version": ""},
    "session_id": "manual:2026-09-10",
    "tags": ["tesseract", "design"]
  }'
```

## Reading the log

`event_list` is the **linear read path** — the way a log is meant to be read.

```json
{
  "namespaces": ["user/chrispian/session/session-20260910-85916030/event/reasoning"],
  "direction": "oldest_first",
  "limit": 50
}
```

It answers `{entries: [...], manifest: {returned, limit, direction, has_more, next_cursor, payload_mode}}`.

- **`direction`** — `newest_first` (default: what just happened) or `oldest_first` (replay, in the order it was reasoned).
- **`since` / `until`** — RFC3339 bounds on `created_at`, inclusive. This is why namespaces carry no date segments.
- **`payload_mode`** — `keys` | `summary` | `full`, as on recall. Under `keys` and `summary` an absent `payload.body` means **withheld, never empty**; the manifest says which mode you got.

### Paging is by position, not offset

`next_cursor` names the entry the page stopped at. Entries appended while you are paging do not shift what you have already seen — which an offset cursor cannot promise on a log that grows at the head.

A cursor is bound to the namespaces, direction and time window it was issued for. Change any of them and you get an error, not a plausible wrong page. `next_cursor: null` means there is nothing left.

### There is no total, deliberately

Counting a log means scanning it, which is the cost this read exists to avoid, and on an append-only log the number would be stale before you read it. `has_more` answers the question a total is usually standing in for.

## Recall does not search events by default

`tesseract_recall` covers **memory and knowledge** when you do not name `domains`. Event is opt-in:

```json
{
  "namespaces": ["user/chrispian/event/reasoning"],
  "domains": ["event"],
  "query": "why did we reject offset paging"
}
```

This is not a limitation to route around — it is the isolation the domain was designed with. A reasoning log runs an order of magnitude or two larger than a corpus of deliberate captures, so a default that included it would make every unqualified recall a log search, and the curated records recall exists to surface would be a rounding error in the candidate set.

**Events do carry embeddings.** Recovering the *why* of a past decision is a semantic question — it has no keyword — so `ranking=relevance` with a query is exactly the right way to find an entry by topic. Use `event_list` when you want order; use recall when you want relevance.

### `ranking=activation` is refused over events

Event opts out of activation entirely: a journal that faded because nobody touched it would be a broken journal. Nothing decays an event row and nothing reinforces one, so its stored activation is the insert default forever — **an absence of a score, not a low one**.

Asking for `ranking=activation` over `domains: ["event"]` is therefore an error rather than an answer, because the answer would be an ordering by a constant that looks like it works. Use `chronological` for order, or `relevance` with a query.

For the same reason, `tesseract_touch` on an event revision reports it under `not_reinforced` rather than counting it in `touched`. That is not a failure — it is the call telling you the revision exists and the activation system does not move it.

## Retracting an entry

`tesseract_deprecate` is how a log entry is retracted. `event_list` excludes deprecated revisions, so the linear read stops showing it; `tesseract_history` still returns it, because the store is append-only and nothing is destroyed.

## Keyed reads

`tesseract_get domain="event"` and `tesseract_history domain="event"` work on the keyed minority. Most entries are keyless and have no `(namespace, key)` to fetch — `event_list` is how you read those, which is to say how you read the log.

There are no `/v1/event/current` or `/v1/event/history` HTTP routes for the same reason: they would mostly answer `not_found`.
