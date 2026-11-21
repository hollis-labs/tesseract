---
name: event
description: The event domain - reasoning, journal, and operational status transitions. Namespace grammar, the linear read path, and why recall does not search it by default.
scope_hint: memory:read
related: [memory, knowledge, recall-and-ranking, namespaces]
---

# Event domain

Event is the **append-only narrative log**: an agent's reasoning about what it is doing, Chrispian's personal log and journal, plus the explicit `status` operational-transition stream.

## Narrative and operational streams

Reasoning and journal keep their narrative contract. Status expands the domain to discrete operational transitions; it does not admit metrics, spans, or periodic chatter.

A trace records *that* something happened, how long it took, and how it was labeled. A reasoning event records *why* — what you were trying, what you chose against, what you noticed and did not act on. **The reasoning in prose is exactly what a trace discards**, and recovering it later is the entire reason this domain exists.

So:

- If what you are recording is a duration, a count, a status code or a structured span — this is the wrong tool, and Tesseract is the wrong system. Use a metrics pipeline.
- If a future session reading it would learn something it would otherwise have to re-derive — that is an event.

For reasoning and journal, ask whether the prose preserves something a trace discards. For status, record an operational transition with a concise summary and optional short body.

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

```text
project/{project_id}/event/{type}
session/{session_id}/event/{type}
system/event/{type}
user/{user_id}/event/{type}
```

`{type}` is a **closed vocabulary** naming the *stream*, not a taxonomy of what happened:

| type | for |
|---|---|
| `reasoning` | an agent's log of what it is doing and why |
| `journal` | a personal log |
| `status` | explicit operational transitions, independently read by workstream/day/writer/subject/state |

Pick the scope by what you would read back as a unit:

- **Session scope** is the natural home for an agent's reasoning — `session/session-20260910-85916030/event/reasoning`. One session's thinking is one linear read.
- **Project scope** is reasoning that spans sessions for one codebase, or a session-close summary — `project/tesseract/event/reasoning`.
- **System scope** is agent runtime operations — `system/event/reasoning`.
- **User scope** is the personal journal — `user/chrispian/event/journal`.

**Do not put dates in the path.** `created_at` is indexed and `event_list` filters on it, so date segments turn every time-range read into a multi-namespace query and buy nothing. Time is an attribute, not a partition.

A bare `project/tesseract/event` reads every stream under that scope — the same prefix shorthand memory namespaces have.

## Writing

`event_write` takes flat arguments. Required: `namespace`, `summary`, `author_agent_id`, `session_id`.

Optional `workstream_id` associates the log item with an opaque exact workstream.
Filter the ordered log with the same argument on `event_list`; the filter is
applied before the keyset page limit. A valid Tether provenance envelope is stored
separately on the revision as an unverified `provenance.write_context` receipt.

```json
{
  "namespace": "session/session-20260910-85916030/event/reasoning",
  "workstream_id": "ws-release-notes-42",
  "summary": "Chose a dedicated log read over ranking=chronological",
  "body": "Recall's fetchCandidates issues no ORDER BY and no LIMIT — it loads every matching row into Go before windowing. Fine at 2k revisions, quadratic-feeling at log volumes. Also its cursor is an offset, and a log is appended at the head, so page 2 would repeat rows written between pages. Went with keyset pagination pushed into SQL. Rejected: adding a LIMIT to fetchCandidates, which would have changed recall's Total semantics for every caller.",
  "author_agent_id": "claude-code",
  "author_version": "claude-opus-5",
  "session_id": "session-20260910-85916030"
}
```

For reasoning, `body` preserves the why. For status, a summary and optional short body are sufficient.

### Keyless by default

`key` is optional and you should usually omit it. A memory is a thing you *revise* — it has an identity and a current value. A log entry is a thing that *happened*: it has a timestamp, and superseding it would be rewriting the record of what you were thinking.

Pass a key only when an entry genuinely has a revisable identity — a running journal page for one day, say. Keyed event writes are held to the memory domain's lowercase dot-notation vocabulary.

### Fixed at the write path

**`data`** carries the entry's own fields as a JSON object — stored verbatim, never interpreted, not indexed and not searched. The contract and limits of "verbatim" are stated in `tesseract_skills memory` and are identical here. All three HTTP write routes take top-level `summary`, `body`, `data` and `data_schema_hash`; author remains structured. MCP uses `data` and `data_schema_hash` on all three write tools. The retired MCP names `payload_data` and `payload_data_schema_hash` are refused with migration guidance, including when the new names coexist. HTTP memory writes also refuse the old `payload` object. All read responses still use `payload.data`.

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
  "namespaces": ["session/session-20260910-85916030/event/reasoning"],
  "workstream_id": "ws-release-notes-42",
  "direction": "oldest_first",
  "limit": 50
}
```

It answers `{entries: [...], manifest: {returned, limit, direction, has_more, next_cursor, payload_mode}}`.

- **`direction`** — `newest_first` (default: what just happened) or `oldest_first` (replay, in the order it was reasoned).
- **`since` / `until`** — RFC3339 bounds on `created_at`, inclusive. This is why namespaces carry no date segments.
- **`payload_mode`** — `keys` | `summary` | `full`, as on recall. Under `keys` and `summary` an absent `payload.body` means **withheld, never empty**; the manifest says which mode you got.
- **`workstream_id`** — exact Registry association filter, applied before the page limit.
- **`author_agent_id`** — exact claimed writer filter, applied in SQL before LIMIT.
- **`state_filters`** — consumer-state filters, AND across fields, OR across values; e.g. `[{"field":"state","values":["blocked"]}]`. Deprecated and expired revisions are always excluded.

### Paging is by position, not offset

`next_cursor` names the entry the page stopped at. Entries appended while you are paging do not shift what you have already seen — which an offset cursor cannot promise on a log that grows at the head.

A cursor is bound to the namespaces, stream, direction, time window, workstream_id, author_agent_id and state_filters it was issued for. Change any of them and you get an error, not a plausible wrong page. `next_cursor: null` means there is nothing left.

### There is no total, deliberately

Counting a log means scanning it, which is the cost this read exists to avoid, and on an append-only log the number would be stale before you read it. `has_more` answers the question a total is usually standing in for.

## Recall does not search events by default

`tesseract_recall` covers **memory and knowledge** when you do not name `domains`. Event is opt-in:

```json
{
  "namespaces": ["project/tesseract/event/reasoning"],
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

## Item and keyed reads

Every event has an `item_id`, including the keyless majority. `tesseract_get item_id=<item_id>` returns its current revision and `tesseract_history item_id=<item_id>` returns its history without changing activation. The legacy `domain="event"` + namespace + key selector still works on the keyed minority. Use `event_list` when you want the chronological log rather than one known item.

The HTTP equivalents are `GET /v1/items/{item_id}` and `/v1/items/{item_id}/history`. There are no `/v1/event/current` or `/v1/event/history` keyed routes.

## Status entry contract

Write to `project/{slug}/event/status`, with a Registry `workstream_id`, a
summary, and an optional short body. `author_agent_id` is the claimed writer;
`consumer_state.subject_urn` names the agent the transition is about.

```json
{
  "namespace": "project/example/event/status",
  "workstream_id": "<Registry workstream id>",
  "summary": "Validation complete",
  "author_agent_id": "dispatcher",
  "session_id": "session-42",
  "idempotency_key": "transition-42",
  "consumer_state": {"state":"done","subject_urn":"msg://agent/example/worker","task":"T-42","run":"run-42"},
  "data": {"session":"session-42","ref":"commit:abc123","token_jti":"receipt-id-only"}
}
```

`state` and `subject_urn` are required consumer-state keys; `task` and `run`
are optional. Data contains only `session`, optional `ref`, and optional
`token_jti` (an identifier, never a token). Neither `verified` nor `urn` is a
payload field. Never put token material anywhere in an entry.

`writer_principal` is an immutable server-set receipt of the authenticated
credential principal. It is absent for unauthenticated/library writes without
an authentication context and for older entries. HTTP and MCP refuse callers
supplying it. An author naming ops or dispatcher remains a claim: a consumer
may call a transition verified only when `writer_principal` matches its trusted
authenticated ops-server or dispatcher service principal. Do not trust token
labels, author strings, consumer state, data, or unverified write_context as
identity evidence. Tesseract records provenance; it does not choose that trust
mapping or emit a `verified` flag.

For keyless writes, an optional `idempotency_key` (at most 256 bytes) returns
the original revision on an identical retry. Keys are scoped to namespace and
authenticated principal (anonymous writes share the empty principal scope).
Changed request content with the same key is refused. The receipt and revision
commit atomically; retries enqueue and audit nothing. Distinct keys or omitted
keys append distinct entries. Keyed event writes refuse this option.

Tail: `event_list` with namespace and `limit`. By day: add RFC3339 `since` and
`until`. By workstream: add `workstream_id`. By writer: add `author_agent_id`.
HTTP uses `GET /v1/event/log`, repeatable `namespace`, and JSON-encoded
`state_filters`; MCP uses the same filter names. The library accepts
`EventLogInput.AuthorAgentID` and `StateFilters`. There is no event log CLI
surface: the CLI explicitly directs event reads/writes to HTTP or MCP.

Switching orchestrator/dispatcher writers and retiring the Torque STATUS mirror
remain integration follow-ups, after the boot digest reads this stream.

## Reading a stream across projects

Use a scope prefix plus the trailing event type, rather than a mid-path
wildcard. `project/*/event/status` is not a supported namespace selector.

```json
{
  "namespaces": ["project/"],
  "stream": "status",
  "workstream_id": "status-writing",
  "state_filters": [{"field":"task_id","values":["T-42"]},{"field":"run_id","values":["run-42"]}],
  "limit": 50
}
```

`project/` and `project/*` select the same descendants. `stream` accepts the
closed event-type vocabulary (`status`, `reasoning`, `journal`); omit it to
retain the existing all-streams read. It matches the final `/event/{type}`
suffix exactly, and combines with namespace, workstream, consumer-state,
writer, and inclusive `since`/`until` filters before LIMIT. State filter names
refer to literal consumer-state keys: use `task`/`run` when writers follow the
status contract above, or `task_id`/`run_id` when those keys were stored.

HTTP: `GET /v1/event/log?namespace=project/&stream=status&workstream_id=status-writing`.
Pass JSON-encoded `state_filters`, RFC3339 time bounds, and the returned cursor
as query parameters. Library: `memory.EventLogInput{Namespaces: []string{"project/"}, Stream: "status", WorkstreamID: "status-writing"}`.

Follow `manifest.next_cursor` with the same selection, including `stream`;
changing it rejects the cursor. Every row retains `namespace` and, when
recorded, `writer_principal` in keys, summary, and full payload modes. Use the
receipt with your trusted-principal mapping; stream selection does not verify
a writer. Credentials must authorize the entire requested namespace scope;
a single-project grant cannot read the cross-project prefix.

### Consumer workstream lists and row aliases

On **event_list only**, `workstream_id` accepts a scalar ID or a native array
of Registry IDs (a JSON-encoded array string is also accepted). HTTP accepts
repeated `workstream_id=ID` query parameters or one JSON-array value. The IDs
OR together; that set ANDs with the other filters before paging. Empty lists,
empty/invalid elements, and lists longer than 500 entries are refused. IDs
remain opaque, exact, and case-sensitive; Registry ULIDs are passed unchanged.
List order and duplicates do not affect cursor selection; a singleton list
and its scalar spelling are equivalent. Other tools retain scalar filters.

```json
{
  "namespaces": ["project/"],
  "stream": "status",
  "workstream_id": ["01M7WQC9BNP2BY914K3H69WHR9", "01M7WQC9BNP2BY914K3H69WHRA"],
  "limit": 50,
  "payload_mode": "summary"
}
```

`since` accepts either an inclusive RFC3339 timestamp or the opaque
`manifest.next_cursor`. For cursor-as-since, keep the other selection fields
unchanged and omit `cursor`. New cursors carry their original `since` bound,
so the alias also resumes a time-bounded first page. Existing cursors remain
valid with the existing `cursor` parameter and original time bounds. `until`
remains an inclusive RFC3339 bound; `limit` may change between pages.

The envelope remains `entries` plus `manifest`. Every row retains its original
revision fields and adds `id` (= `revision_id`) and `at` (= `created_at`,
RFC3339). Namespace, workstream_id, and recorded writer_principal stay in every
payload mode. In summary (default) and full modes, rows additionally expose:

| Field | Stored source |
|---|---|
| `summary` | `payload.summary` |
| `state` | string `consumer_state.state` |
| `task_id` | string `consumer_state.task_id`; fall back to `task` only if task_id is absent |
| `run_id` | string `consumer_state.run_id`; fall back to `run` only if run_id is absent |
| `team_id` | string `consumer_state.team_id`, when present |
| `workstream_key` | string `consumer_state.workstream_key`, when present |

Only full mode adds optional `detail` (= `payload.body`) and `link` (= string
`payload.data.ref`), preserving the payload-withholding contract. Keys mode
adds only identity aliases. Missing, empty, or non-string optional sources are
omitted; no Registry lookup, trust decision, or missing value is inferred.
Workstream-key filtering is available through existing `state_filters` only
when writers stored that consumer-state key; there is no Registry key lookup.
