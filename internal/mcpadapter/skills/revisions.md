---
name: revisions
description: Append-only revision model, supersede chains, dedup, revision IDs, timestamps.
scope_hint: none
related: [memory, knowledge, audit]
---

# Revisions

Every write in Tesseract creates a new revision. The service never mutates existing records in place.

## Item, revision, and version identity

- **Item ID (`item_id`)** — the primary stable identity for a Tesseract-owned item across its revisions and lifecycle. Responses retain `memory_id` and `record_id` as compatible aliases. It stays fixed across edits, supersedes, and key changes.
- **Revision ID (`revision_id`)** — monotonic ULID for one exact immutable version in a revisioned domain (`context`, `memory`, `knowledge`, `event`). Lexicographically sortable and globally unique within the store. It is not an item ID.
- **Version token (`version_token`)** — concurrency token for mutable items in `workspace`. Rotates on every edit; workspace retains no revision history.
- **Timestamp** — RFC3339Nano (nanosecond precision). Tie-breaking falls back to revision ID lex order for same-millisecond writes.

## When an ID is not found

`tesseract_get` and `tesseract_history` by `item_id`, and `tesseract_get_revision`, answer `not_found` for an ID that resolves to nothing. Their HTTP peers (`GET /v1/items/{item_id}`, `.../history`, `GET /v1/memory/revisions/{revision_id}`) do the same. When that ID is a well-formed ULID, the error also carries a `details` object saying what the ID itself says. The message is unchanged.

```json
{
  "code": "not_found",
  "message": "memory not found: item_id 01M2SFA0ZQ3K4N6P7R8T9V0WXY",
  "details": {
    "field": "item_id",
    "id": "01M2SFA0ZQ3K4N6P7R8T9V0WXY",
    "minted_at": "2026-09-18T05:19:56.919Z",
    "age_seconds": 131760
  }
}
```

The first ten characters of a ULID encode the millisecond it was minted. `minted_at` is when an ID with that prefix would have been minted; `age_seconds` is how long before the server's now that was, and is negative when the ID's timestamp is ahead of the server's clock.

That is a fact about the string, not a verdict: Tesseract does not say whether the ID is real. What it lets you see is that an ID which would have been minted a day and a half before an agent claimed to have written it is not that write's result, and that an ID minted seconds ago which still does not resolve is a genuine problem worth chasing.

`details` is absent when there is nothing to decode — an ID that is not a ULID, or a lookup by `(domain, namespace, key)`, which has no ID to read — and the error is then exactly what it was.

## Head vs. history

- `tesseract_get` — returns the current revision for `item_id`, including keyless items. The complete legacy `(domain, namespace, key)` selector remains supported and domain-filtered.
- `tesseract_history` — returns the item's revision chain newest first by `item_id`, or by the complete legacy keyed selector. It is a **bare array** when no paging or budget argument is present.
- `tesseract_recall` with `revision_scope=timeline` — includes superseded revisions in ranking.
- `tesseract_ref_resolve` — normalizes a typed ID, complete current key, or canonical Tesseract URI to identity metadata without content or reinforcement.

## Reference resolution

Use `tesseract_ref_resolve` when another system needs a stable Tesseract
reference rather than the stored content. Pass exactly one complete selector:

```json
{"item_id":"01HITEM..."}
```

```json
{"revision_id":"01HREVISION..."}
```

```json
{"domain":"knowledge","namespace":"project/tesseract/knowledge/contracts","key":"resolver-contract"}
```

```json
{"uri":"tesseract://revision/01HREVISION..."}
```

An item selector returns `ref.kind=tesseract_item`; an exact revision selector
returns `ref.kind=tesseract_revision` and stays pinned to that revision. The
canonical URI forms are `tesseract://item/<item_id>` and
`tesseract://revision/<revision_id>`.

The successful outcome vocabulary is `resolved`, `deleted`, `not_found`,
`ambiguous`, and `unsupported_reference`. Current v1 selectors are unique, so
none emits `ambiguous`; the status remains in the response model for a future
supported legacy form that can honestly have several candidates. An unknown
ID has no invented canonical reference. An unsupported URI or opaque locator
is not guessed or fetched over the network.

Legacy keys resolve the item that owns the exact key now. A key rename keeps
the item ID but the former key stops resolving. Deletion leaves the item ID as
a tombstone, and recreating the same key creates and resolves a new identity.
Resolution reads metadata only: it returns no content, does not reinforce
activation or access counts, does not rotate workspace version tokens, and
does not create revisions.

To bound a history read, pass `limit`, `cursor`, `budget_bytes`, or `budget_tokens`. Any of them switches the response from the bare array to `{results, manifest}`, with the same manifest and cursor semantics `tesseract_skills recall-and-ranking` documents. Chains are shallow in practice, so this is a ceiling against unbounded growth rather than a routine knob.

```json
{"item_id": "01HITEM...", "limit": 20}
```

Use exactly one selector form. Mixed calls and partial legacy triples are rejected:

```json
{"domain": "memory", "namespace": "project/tesseract/memory/decisions", "key": "sqlite.pragma.journal_mode", "limit": 20}
```

The preferred HTTP peer is `GET /v1/items/{item_id}/history`. Per-domain namespace/key routes remain supported and use the same `key` query name. The old `memory_key` query parameter is refused with migration guidance, including when both names are present. Read responses still carry `memory_key`. See `tesseract_skills start-here` for `$TESSERACT_URL` / `$TESSERACT_TOKEN`.

```bash
curl -sS -G "$TESSERACT_URL/v1/memory/history" \
  -H "Authorization: Bearer $TESSERACT_TOKEN" \
  --data-urlencode "namespace=project/tesseract/memory/decisions" \
  --data-urlencode "key=sqlite.pragma.journal_mode" \
  --data-urlencode "limit=20"
```

`GET /v1/knowledge/history` is the knowledge-domain equivalent. `budget_bytes` and `budget_tokens` are accepted on both routes and must be greater than zero — a zero budget can only produce an empty page, so it is rejected rather than treated as "no ceiling". Omit them for no ceiling.

## Supersede chains

Pass `supersedes` to `memory_write` or `knowledge_write` to mark an explicit ancestor. The new revision becomes the head; the old revision stays in history. Supersede is how you "edit" memory without losing provenance.

An edit is a full write plus one field — there is no partial-update call, so every field the new revision should carry has to be present, not just the ones that changed:

```json
{
  "namespace": "project/tesseract/memory/decisions",
  "memory_key": "sqlite.pragma.journal_mode",
  "supersedes": "01HXA...",
  "author_agent_id": "claude",
  "trigger": "explicit",
  "session_id": "2026-05-02:backend",
  "derived_from": "observation",
  "confidence": 0.95,
  "payload_summary": "Journal mode stays WAL, now confirmed under the networked-filesystem case too."
}
```

```bash
curl -sS -X POST "$TESSERACT_URL/v1/memory/write" \
  -H "Authorization: Bearer $TESSERACT_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "namespace": "project/tesseract/memory/decisions",
    "memory_key": "sqlite.pragma.journal_mode",
    "supersedes": "01HXA...",
    "author": {"agent_id": "claude"},
    "trigger": "explicit",
    "session_id": "2026-05-02:backend",
    "derived_from": "observation",
    "confidence": 0.95,
    "summary": "Journal mode stays WAL, now confirmed under the networked-filesystem case too."
  }'
```

The value of `supersedes` is a `revision_id`, which is what `tesseract_recall`, `tesseract_get` and `tesseract_history` all carry on every result. It is not an `item_id`/`memory_id` — those two response fields carry the same stable item identity, and `memory_promote` retains `source_memory_id` for compatibility. Full field lists for both shapes are in `tesseract_skills memory`.

## Guarded writes: create-only and expected-revision

By default a revisioned write is unconditional, and two things follow that a writer who shares records with others has to know:

- **A key that already exists is appended to, silently.** Writing an existing `(namespace, key)` adds a revision and makes it the head. Nothing tells you the key was taken, and nothing marks the old head superseded unless you passed `supersedes`.
- **`supersedes` is not checked against the head.** It has to exist and belong to the same item; it does not have to be current. Two writers who read the same revision and each supersede it both succeed. The later one wins the head, and the other's revision stays in history with no signal that it was displaced.

That is the right default for one session appending to its own notes. It is the wrong one when other writers can reach the same records, so `memory_write` and `knowledge_write` take two opt-in guards. Both are off unless you pass them, so no existing call changes:

| Argument | The write is refused unless… | Error |
|---|---|---|
| `create_only: true` | the key has no item yet | `key_conflict` |
| `expected_revision_id` | that revision is the key's current head | `revision_conflict` |

Both are checked inside the write's own transaction, so a guard is a real compare-and-set: of several writers who name the same head, exactly one succeeds. A refused write leaves nothing behind. The two cannot be combined — one demands the key be new and the other that it exist — and `expected_revision_id` needs a key, because a keyless write always creates a new item and there is no head to compare against. A key that has no item at all fails `expected_revision_id` too.

The edit idiom is read, change, then write against what you read. Only `expected_revision_id` and `supersedes` are new here; the rest is the ordinary full write:

```json
{
  "namespace": "project/tesseract/knowledge/framework",
  "key": "framework.go-providers",
  "kind": "package",
  "source": "manual",
  "pointer_scheme": "nil",
  "pointer_locator": "framework/go-providers",
  "summary": "go-providers: multi-provider AI adapter",
  "author_agent_id": "claude",
  "session_id": "2026-09-19:atlas",
  "expected_revision_id": "01HXA...",
  "supersedes": "01HXA..."
}
```

`expected_revision_id` says what the head must be; `supersedes` says what to deprecate. They are independent: pass the same revision to both to replace it, or only the guard to append without deprecating anything.

A refused write names the head it lost to — in the message on MCP, and on HTTP in `details.current_revision_id`, where the refusal is a `409` (`details` also carries `item_id` and echoes `expected_revision_id`). Re-read that revision, apply your change to it, and retry with its id. Nothing retries for you.

A write's response now says what it did. `write_outcome` is `created` when the write minted the item and `appended` when it added a revision to one that already existed; `previous_revision_id` names the head it followed, and is absent when the item was created. That is not `supersedes`, which is what you asked to deprecate rather than what was there. Both fields appear only on the response to a write, never on a read.

The guards add no partial update — a revision still carries every field you send, so an edit resupplies the ones that did not change — and they do not let a supersede cross entries.

## Dedup

`memory_write` accepts two dedup modes:

- `dedup=none` (default) — never dedup.
- `dedup=semantic` — cosine-similar existing revisions in the same namespace are auto-superseded. Cross-key matches are surfaced as `DedupMatch` without auto-supersede. Threshold defaults to 0.85; override per call with `dedup_threshold`.

## Deprecation

`tesseract_deprecate` marks a revision as removed from the current head pool. It remains in history, and the deprecation emits an audit event — under `memory.deprecate` whatever the revision's own domain is, which `tesseract_skills audit` explains and which matters if you are reconstructing a knowledge or event retraction from the log.

```json
{"revision_id": "01HXA..."}
```

```bash
curl -sS -X POST "$TESSERACT_URL/v1/memory/deprecate" \
  -H "Authorization: Bearer $TESSERACT_TOKEN" -H "Content-Type: application/json" \
  -d '{"revision_id": "01HXA..."}'
```

One route serves every revision domain here, the way one tool does: memory, knowledge and event revisions share a table keyed by `revision_id`, so an ID from any of them resolves. Under `event` this is how a log entry is retracted — see `tesseract_skills event`.

## What NOT to expect

- **No in-place edits.** Every change is a new revision.
- **No hard deletes.** Deprecation is soft; history remains.
- **No write-your-own revision IDs.** The store assigns them.
- **No domain changes.** `domain` is stamped when a memory is created and holds for its whole lineage; writing an existing `(namespace, key)` under a different domain is a `validation_error`, not a migration. There is no supported move, and promotion is not one — it crosses namespaces, not domains. Rewriting the content under a new identity works and costs the supersede chain, the created_at history and the lineage edges, which is a real loss on a store whose claim is that history is canonical. Worth knowing before the first write, not worth unpicking after it: a record in a defensible domain is better left where it is.
