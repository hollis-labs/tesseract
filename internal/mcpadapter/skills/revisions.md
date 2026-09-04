---
name: revisions
description: Append-only revision model, supersede chains, dedup, revision IDs, timestamps.
scope_hint: none
related: [memory, knowledge, audit]
---

# Revisions

Every write in Tesseract creates a new revision. The service never mutates existing records in place.

## Revision identity

- **Revision ID** — monotonic ULID (`01HX…`). Lexicographically sortable, globally unique within the store.
- **Timestamp** — RFC3339Nano (nanosecond precision). Tie-breaking falls back to revision ID lex order for same-millisecond writes.

## Head vs. history

- `tesseract_get` — returns the current (latest, non-deprecated) revision for `(domain, namespace, key)`. `domain` filters: a key holding another domain's revision answers `not_found`, not that revision.
- `tesseract_history` — returns the revision chain, newest first, as a **bare array** under `domain="memory"` and `domain="knowledge"`.
- `tesseract_recall` with `revision_scope=timeline` — includes superseded revisions in ranking.

To bound a history read, pass `limit`, `cursor`, `budget_bytes`, or `budget_tokens`. Any of them switches the response from the bare array to `{results, manifest}`, with the same manifest and cursor semantics `tesseract_skills recall-and-ranking` documents. Chains are shallow in practice, so this is a ceiling against unbounded growth rather than a routine knob.

```json
{"domain": "memory", "namespace": "user/chrispian/memory/decisions", "key": "sqlite.pragma.journal_mode", "limit": 20}
```

The HTTP peer is per-domain rather than one route with a `domain` argument, and **it spells the key differently**: `GET /v1/memory/history` takes `memory_key`, not `key`. See `tesseract_skills start-here` for `$TESSERACT_URL` / `$TESSERACT_TOKEN`.

```bash
curl -sS -G "$TESSERACT_URL/v1/memory/history" \
  -H "Authorization: Bearer $TESSERACT_TOKEN" \
  --data-urlencode "namespace=user/chrispian/memory/decisions" \
  --data-urlencode "memory_key=sqlite.pragma.journal_mode" \
  --data-urlencode "limit=20"
```

`GET /v1/knowledge/history` is the knowledge-domain equivalent. `budget_bytes` and `budget_tokens` are accepted on both routes and must be greater than zero — a zero budget can only produce an empty page, so it is rejected rather than treated as "no ceiling". Omit them for no ceiling.

## Supersede chains

Pass `supersedes` to `memory_write` or `knowledge_write` to mark an explicit ancestor. The new revision becomes the head; the old revision stays in history. Supersede is how you "edit" memory without losing provenance.

An edit is a full write plus one field — there is no partial-update call, so every field the new revision should carry has to be present, not just the ones that changed:

```json
{
  "namespace": "user/chrispian/memory/decisions",
  "memory_key": "sqlite.pragma.journal_mode",
  "supersedes": "01HXA...",
  "author_agent_id": "claude",
  "trigger": "explicit",
  "session_id": "2026-05-02:backend",
  "origin": "observation",
  "confidence": 0.95,
  "payload_summary": "Journal mode stays WAL, now confirmed under the networked-filesystem case too."
}
```

```bash
curl -sS -X POST "$TESSERACT_URL/v1/memory/write" \
  -H "Authorization: Bearer $TESSERACT_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "namespace": "user/chrispian/memory/decisions",
    "memory_key": "sqlite.pragma.journal_mode",
    "supersedes": "01HXA...",
    "author": {"agent_id": "claude"},
    "trigger": "explicit",
    "session_id": "2026-05-02:backend",
    "origin": "observation",
    "confidence": 0.95,
    "payload": {"summary": "Journal mode stays WAL, now confirmed under the networked-filesystem case too."}
  }'
```

The value of `supersedes` is a `revision_id`, which is what `tesseract_recall`, `tesseract_get` and `tesseract_history` all carry on every result. It is not a `memory_id` — that is the stable id of the memory the revision belongs to, and it is what `memory_promote` takes. Full field lists for both shapes are in `tesseract_skills memory`.

## Dedup

`memory_write` accepts two dedup modes:

- `dedup=none` (default) — never dedup.
- `dedup=semantic` — cosine-similar existing revisions in the same namespace are auto-superseded. Cross-key matches are surfaced as `DedupMatch` without auto-supersede. Threshold defaults to 0.85; override per call with `dedup_threshold`.

## Deprecation

`tesseract_deprecate` marks a revision as removed from the current head pool. It remains in history (audit emission for memory deprecations is in flight).

```json
{"revision_id": "01HXA..."}
```

```bash
curl -sS -X POST "$TESSERACT_URL/v1/memory/deprecate" \
  -H "Authorization: Bearer $TESSERACT_TOKEN" -H "Content-Type: application/json" \
  -d '{"revision_id": "01HXA..."}'
```

One route serves both domains here, the way one tool does: memory and knowledge revisions share a table keyed by `revision_id`, so an ID from either resolves.

## What NOT to expect

- **No in-place edits.** Every change is a new revision.
- **No hard deletes.** Deprecation is soft; history remains.
- **No write-your-own revision IDs.** The store assigns them.
