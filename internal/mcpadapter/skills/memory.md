---
name: memory
description: When to use the memory domain - the recall/use/touch loop, patterns, required fields, keyed vs. unkeyed.
scope_hint: memory:read
related: [namespaces, revisions, recall-and-ranking, promotion]
---

# Memory domain

**Memory is content that comes *to you*** — recall surfaces it while you are working on something nearby, and a later revision supersedes it rather than editing it. Knowledge is the other half: content you go *to*, because you already knew it was there. **The full statement, including where the rule stops applying, is in `tesseract_skills start-here`; it is stated once, there.**

Every write is append-only and revisioned; recall is multi-knob.

## When to use memory

- A design call and the reasoning behind it.
- A constraint or preserved tech debt someone will otherwise rediscover.
- Work deliberately deferred, with enough context to pick it up.
- Guidance about how to approach work; what was true after the work; what a session distilled.

The thread through those: a later session should **meet** it while working nearby, without having known to ask.

## When NOT to use memory

- **Content someone will come back for by name.** A project's canonical, a handoff, a playbook, a doc or package reference — `knowledge_write`, whether or not it points at anything outside Tesseract.
- **Generic state records.** Use `context_write` - memory has specific lifecycle semantics (activation, promotion, dedup) you don't need for plain records.
- **Ephemeral session scratch.** Write to session-scoped memory (`user/{id}/session/{sid}/memory/{type}`) when you want promotion later; use app context records (`app/{id}/session/*`) when you just want ephemeral scratch.

## The fields, and how to choose their values

From the `memory_write` MCP declaration. The vocabularies below are **closed** —
a value outside them is a `validation_error`, not a new category.

| Field | Required | What it is for |
|---|---|---|
| `namespace` | yes | where the revision lives, and therefore who owns it and what it is *about*. Must parse as a typed memory namespace: `user/{id}/memory/{type}`, `user/{id}/project/{pid}/memory/{type}`, or `user/{id}/session/{sid}/memory/{type}`. |
| `payload_summary` | yes | the one line a later session reads in recall results before deciding whether to hydrate. Write it as the claim, not as a title. |
| `derived_from` | yes | where the content came from. **Weights recall** — see below. |
| `trigger` | yes | what caused you to write *now*. Provenance only; nothing reads it for behaviour. |
| `confidence` | yes | float in `[0, 1.0]`. **Weights recall** as a direct multiplier. |
| `author_agent_id` | yes | who wrote it. |
| `session_id` | yes | the session that produced it, for correlating a turn's writes. |
| `memory_key` | no | a stable identity for an evolving concept; re-writing the key supersedes. See *Keyed vs. unkeyed*. |
| `status` | no | `draft` (default) \| `reviewed` \| `canonical`. **Weights recall**: 0.6 / 0.9 / 1.0, and a deprecated revision drops to 0.1. |
| `payload_data` | no | the record's OWN fields as a JSON object, stored verbatim and never interpreted — not indexed, not embedded, not searched. See below. |
| `payload_data_schema_hash` | no | optional hex sha256 recording which schema `payload_data` claims; stored, never validated. |
| `supersedes`, `author_version`, `tags`, `ttl_seconds`, `payload_body`, `consumer_state`, `dedup`, `dedup_threshold` | no | see the mapping table and the `consumer_state` section below. |

**The `{type}` segment** — `decisions`, `feedback`, `followups`, `learnings`,
`limitations`, `notes`, `outcomes`, `todos`. `notes` is the catch-all when no
stronger type fits. (`references` was retired 2026-09-10 — a pointer to where
information lives is content you go to, so it is knowledge.) The per-type
meanings live in `tesseract_skills namespaces`; they are not restated here.

### `derived_from` — where the content came from

> **This field was called `origin` until 2026-09-12.** The values did not
> change. The old name is **refused, not ignored**, on every surface: a write or
> a recall filter carrying `origin` fails with an error naming `derived_from`.
> The rename happened because `origin` read as *who originated this*, so it was
> being filled in as an authorship claim by agents that had merely been talking
> to a person.

**This is the field most often filled in by reflex, and it is not free.** It
is a direct multiplier on the recall score, so it does not merely label a
revision — it moves it up or down the results a later session reads. A record
stamped `user` outranks the same record stamped `observation` by 1.375x with
everything else equal.

| value | weight | when it applies |
|---|---|---|
| `feedback` | 1.3 | a correction, or a standing instruction about how to work |
| `user` | 1.1 | **a person ruled it.** Not "a person was in the conversation" |
| `project` | 1.0 | a property of a codebase or project — true of the thing |
| `reference` | 0.9 | what `knowledge_write` stamps. On the memory surface, see the note |
| `observation` | 0.8 | you noticed it, measured it, or read it out of the system |

The distinction that goes wrong most often is **`user` vs `observation`**: if the
body of your record says something was *measured*, *observed* or *found*, the
the value is `observation` even when a person asked you to go measure it. `user` is
for the ruling itself — "we are keeping WAL" — not for the evidence behind it.

`reference` is the one to be careful with. It has no settled meaning on the
memory surface; its only systematic writer is the knowledge write path, whose
own comment says it picked the closest available bucket. If you are reaching for
it on a memory write, you probably want `project` or `observation` — or the
record belongs in `knowledge_write`.

### `trigger` — what made you write now

`explicit` (asked to), `post_compact` (carrying context across a compaction),
`per_turn` (a routine end-of-turn capture), `promotion` (set for you by
`memory_promote`), `manual` (a human or a script wrote it directly). Unlike
`derived_from`, nothing reads this for behaviour — it is provenance, so pick the one
that is true and move on.

## A complete write, on both surfaces

Copy one of these and edit it. They write the same revision; the shapes differ, and the differences are structural rather than cosmetic — see `tesseract_skills start-here` for the two-surface rules and for what `$TESSERACT_URL` / `$TESSERACT_TOKEN` are.

**This record's `derived_from` is `observation`, and that is the point of the example.** A person asked for the decision, but the summary says DELETE *was measured and rejected* — the content is the measurement, so `observation` is the honest value. `user` would be right for the ruling alone ("we are staying on WAL"), and choosing it here would quietly buy this record a 1.375x ranking advantage it has not earned.

Over MCP, every field is a flat scalar and `tags` is a JSON-encoded **string**:

```json
{
  "namespace": "user/chrispian/memory/decisions",
  "memory_key": "sqlite.pragma.journal_mode",
  "author_agent_id": "claude",
  "author_version": "opus-5",
  "trigger": "explicit",
  "session_id": "2026-04-19:backend",
  "derived_from": "observation",
  "confidence": 0.9,
  "tags": "[\"sqlite\",\"durability\"]",
  "payload_summary": "Journal mode stays WAL; DELETE was measured and rejected.",
  "payload_body": "WAL survives the concurrent-reader case the CLI hits during a serve. DELETE mode serialized readers behind the writer and doubled p99 on the audit list. Revisit only if we ever ship a networked filesystem target, where WAL is unsafe."
}
```

Over HTTP the same fields nest: `author` and `payload` are objects, and `tags` is a real array.

```bash
curl -sS -X POST "$TESSERACT_URL/v1/memory/write" \
  -H "Authorization: Bearer $TESSERACT_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "namespace": "user/chrispian/memory/decisions",
    "memory_key": "sqlite.pragma.journal_mode",
    "author": {"agent_id": "claude", "agent_version": "opus-5"},
    "trigger": "explicit",
    "session_id": "2026-04-19:backend",
    "derived_from": "observation",
    "confidence": 0.9,
    "tags": ["sqlite", "durability"],
    "payload": {
      "summary": "Journal mode stays WAL; DELETE was measured and rejected.",
      "body": "WAL survives the concurrent-reader case the CLI hits during a serve."
    }
  }'
```

The field-by-field mapping, for the fields that do not simply carry across:

| MCP argument | HTTP field |
|---|---|
| `author_agent_id`, `author_version` | `author: {agent_id, agent_version}` |
| `payload_summary`, `payload_body` | `payload: {summary, body}` |
| `tags` (JSON-encoded string) | `tags` (JSON array) |
| `consumer_state` (JSON-encoded string) | `consumer_state` (JSON object) |
| — | `domain` (HTTP only, and optional there: `/v1/memory/write` sets `memory` for you and refuses any other value) |

`facets` exists on the HTTP body but is a knowledge-domain field: a memory write carrying a non-zero facet is rejected. Facets go to `POST /v1/knowledge/write` — see `tesseract_skills knowledge`.

### A third value, because the contrast is the lesson

Same tool, same required fields, a different answer — this one is `feedback`,
the highest-weighted value in the vocabulary:

```json
{
  "namespace": "user/chrispian/memory/feedback",
  "author_agent_id": "claude",
  "trigger": "explicit",
  "session_id": "2026-09-12:review",
  "derived_from": "feedback",
  "confidence": 0.9,
  "tags": "[\"subagents\",\"prompting\"]",
  "payload_summary": "A read-only instruction in a subagent prompt does not hold; give the agent no write tools instead.",
  "payload_body": "Observed across several dispatches: subagents told 'research only' in prose implemented and committed anyway. Prompt text is not a permission boundary."
}
```

Note that the body reports an observation and the value is still `feedback`.
The two are not in tension: `derived_from` asks where the CONTENT came from, and the
content here is the standing instruction the observation produced. Had the
record stopped at "subagents ignored the instruction three times," it would be
`observation` — a measurement with no rule attached.

**Three records, three values, one field.** The decision above is
`observation` because its content is a measurement; the todo below is `user`
because a person said so; this one is `feedback` because it tells a later
session how to work. If you cannot say which of those three sentences describes
your record, the tie-break is downward — `observation` costs a record ranking
weight it might deserve, and `user` borrows weight it might not.

## `payload_data` — the record's own fields, in your shape

**`payload_summary` and `payload_body` are prose. `payload_data` is everything
else**: an ADR's decision and alternatives, a contact's email and phone, a bug
report's steps and severity. You send an object shaped for you, and you get it
back. Tesseract checks two things — that it parses, and that it is an object —
and nothing else. No typing, no schema enforcement, no required keys.

**It is not indexed, not embedded and not searched.** No value inside it changes
anything Tesseract does: not ranking, not activation, not retention, not
recall's candidate set. That is the point of the field, not a gap in it.

**So put anything you want to be findable in the prose as well.** Recall reaches
a record through its summary and body. A bug report whose severity lives only in
`payload_data` is not findable by severity, and the fix is a sentence in the
summary, not an index here.

**`payload_data` is not `consumer_state`.** They are siblings and the difference
is worth holding: this is **what the record IS**, that is **how it is being
worked** — and `consumer_state` is filterable via `state_filters` while this is
not.

| bag | holds | filterable |
|---|---|---|
| `payload_data` | the record's own fields | no |
| `consumer_state` | lifecycle — done, section, due | yes, `state_filters` |
| `payload_summary` / `payload_body` | prose, and the only thing search reads | via `query` |
| `tags` | cross-cutting labels | yes, `tags` |

### The shape differs by door, and a wrong shape is a `400`

Every write route runs strict decoding, so an unknown field is **rejected, not
ignored**. Sending the wrong shape fails the whole write rather than dropping
the field quietly.

| door | where `payload_data` goes |
|---|---|
| `memory_write`, `knowledge_write`, `event_write` (MCP) | `payload_data` — flat, same on all three |
| `POST /v1/memory/write` | **nested**: `payload.data` |
| `POST /v1/knowledge/write`, `POST /v1/event/write` | **flat**: top-level `data` |

That asymmetry is not new to this field — it is exactly how `summary` and `body`
already differ between those routes, because memory nests them under `payload`
and knowledge and event take them flat.

**On read it is uniform**: every domain returns it at `payload.data`.

```json
{
  "namespace": "user/chrispian/memory/decisions",
  "author_agent_id": "claude",
  "trigger": "explicit",
  "session_id": "2026-09-12:adr",
  "derived_from": "user",
  "confidence": 0.9,
  "payload_summary": "Journal mode stays WAL; DELETE was measured and rejected.",
  "payload_data": "{\"decision\":\"WAL\",\"alternatives\":[\"DELETE\"],\"revisit_if\":\"networked filesystem target\"}"
}
```

### How exact "you get it back" is

Exact in the store: the column holds the bytes you sent, and a Go caller reading
the revision gets those bytes.

**Not byte-exact over the wire, and the difference is only visible if you hash
it.** `encoding/json` compacts and escapes on the way out, so
`{"a": 1, "h":"x<y"}` comes back as `{"a":1,"h":"x\u003cy"}` — the whitespace
gone and `<`, `>`, `&` escaped. Same JSON value, parses to the same object,
different bytes. If you sign or checksum
what you receive, checksum the form you received.

**Over MCP, send a JSON-encoded string when the bytes matter.** A native JSON
object has already been decoded by the transport before the tool sees it, so an
integer beyond 2^53 has been rounded through a float and cannot be recovered.
The string form is stored exactly as you sent it.

### `payload_data_schema_hash` — an optional claim, never checked

If your data follows a schema, you may record which one: a hex sha256 matching a
type's `schema_ref.schema_hash`. **Tesseract never opens the schema and never
validates against it.** It stores what you said, so a record written under an
older schema stays distinguishable from one that drifted.

Omit it if you are making no claim — it is never filled in for you, and a claim
with no `payload_data` to describe is refused.

## `consumer_state` — the bag that is yours, not Tesseract's

A revision can carry `consumer_state`: a JSON **object** holding your own operational state for that entry. Tesseract checks that it is well-formed JSON and an object, plus any `required_fields` the type declares, and **never reads a value out of it**. No vocabulary, no transition checking, ever. Nothing decays, ranks or expires differently because of what is in it.

**It is not the `state` block on a recall result.** That one is Tesseract's own activation bookkeeping — `activation`, `access_count`, `current_revision` — scoped to the entry and not writable from any surface. `consumer_state` is scoped to one revision, is written by you, and is immutable with the revision that carries it: changing state means writing a new revision, which is what makes an item's history readable.

Three bags, three jobs, and collapsing them is the mistake to avoid:

| Bag | Holds | Example |
|---|---|---|
| `consumer_state` | lifecycle | `{"completed": false, "section": "now"}` |
| `payload` | content | the title and the notes |
| `tags` | cross-cutting labels | `["errand", "q3"]` |

Filter on it with `state_filters` — see `tesseract_skills recall-and-ranking`.

### `todos`, the first structured type

`user/{id}/memory/todos` holds flat list items with light state. **A todo is not a task**: a Torque task is FSM-governed tracked work with dispatch, budgets and dependencies, and it stays in Torque. A todo is a note with a checkbox, often ephemeral.

The shape, from NIL's working model — title in `payload_summary`, notes in `payload_body`, and the rest in the bag. **This one keeps `derived_from: "user"` and is the counterexample**: nobody measured or inferred that the registration needs renewing — a person said so, and the record is that instruction. That is what `user` is for.

```json
{
  "namespace": "user/chrispian/memory/todos",
  "author_agent_id": "claude",
  "trigger": "explicit",
  "session_id": "2026-09-10:inbox",
  "derived_from": "user",
  "confidence": 0.9,
  "payload_summary": "renew the domain registration",
  "consumer_state": "{\"kind\":\"todo\",\"section\":\"now\",\"completed\":false,\"pinned\":true,\"priority\":\"high\",\"due_at\":\"2026-09-30\",\"external_ref\":\"fe-doc-991\"}"
}
```

Fields NIL uses: `kind` (todo|note|scratch), `section` (now|soon|anytime), `pinned`, `completed`, `archived`, `priority`, `due_at`, `threshold_at`, `recurrence_rule`, `inbox`, `external_ref`. None of them means anything to Tesseract — they are listed so consumers agree with each other, not because the store checks them. `external_ref` is the one worth carrying deliberately: a writer-supplied idempotency key for correlating to an external system.

Four of those carry an index (`completed`, `external_ref`, `kind`, `section`); the rest are filterable and scan.

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

**Why the third step exists at all.** `tesseract_recall` does not reinforce a result merely for returning it. Being returned by a search is the ranker's guess about what you need; if that guess reinforced itself, popular-because-returned would beat actually-useful within a few cycles. A deliberate `tesseract_get` or `tesseract_get_revision` reinforces once. `tesseract_touch` reports use of a projected hit that did not need a fetch, or adds a second reinforcement only when that extra signal is intentional.

**Touch only what genuinely shaped the turn. Under-reporting is fine; over-reporting is worse than silence, because it teaches the ranking that noise is signal.** There is little to win by inflating: reinforcement has diminishing returns — each touch closes a fraction of the remaining distance to a ceiling, so the tenth touch moves a memory far less than the first, and no amount of touching passes the ceiling.

**When to call it.** After the work, not after the search. If you touch as soon as results arrive, you are reinforcing the guess at the moment it was made — the thing recall refuses to do, done manually.

**What counts as one touch.** Each distinct memory named is reinforced once: `activation` moves a fixed fraction toward its ceiling, `access_count` increments, `last_accessed_at` is set. Naming a revision twice, or naming two revisions of the same memory, reinforces it once. A recall spanning several domains is reportable in one call — any domain's revision ID resolves. What comes back distinguishes three outcomes: `touched` counts the memories the store actually moved, `not_reinforced` lists revisions that exist in a domain outside activation (event, today), and `not_found` lists IDs that name nothing.

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
  - **The key format is enforced, not normalized.** Dot-separated segments, each matching `^[a-z0-9_]+$`; at most 6 segments, 64 characters per segment, 256 characters total. A hyphen, an uppercase letter or a space anywhere in the key is a `validation_error` — `user.prefs-style` and `User.Prefs.Style` are both rejected rather than lowercased or rewritten, so pick the key in that shape up front.
  - The rule is memory-domain only. `knowledge_write` keys are free-form slugs, because they usually come from an external source that does not obey it.
- **Unkeyed memory** - no `memory_key`; each write stands alone. Use for observation streams where no stable identity exists. Note: `tesseract_get` / `tesseract_history` require a key; unkeyed memories are reachable via `tesseract_recall` and `tesseract_get_revision`.

## Promotion

Session-scoped memories can be promoted to user or project scope via `memory_promote` (the shortcut). Source and target must carry the same `{type}` segment — promote is a scope change, not a re-classification. The source is deprecated; the promoted revision lands in the target namespace with `trigger=promotion`. See `tesseract_skills promotion`.
