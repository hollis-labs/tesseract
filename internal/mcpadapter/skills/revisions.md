---
name: revisions
description: Append-only revision model, supersede chains, dedup, revision IDs, timestamps.
scope_hint: none
related: [memory, knowledge, audit]
---

# Revisions

Every write in Vanta creates a new revision. The service never mutates existing records in place.

## Revision identity

- **Revision ID** — monotonic ULID (`01HX…`). Lexicographically sortable, globally unique within the store.
- **Timestamp** — RFC3339Nano (nanosecond precision). Tie-breaking falls back to revision ID lex order for same-millisecond writes.

## Head vs. history

- `memory_get` / `knowledge_get` — returns the current (latest, non-deprecated) revision for `(namespace, key)`.
- `memory_history` / `knowledge_history` — returns the full revision chain, newest first.
- `memory_recall` with `revision_scope=timeline` — includes superseded revisions in ranking.

## Supersede chains

Pass `supersedes` to `memory_write` or `knowledge_write` to mark an explicit ancestor. The new revision becomes the head; the old revision stays in history. Supersede is how you "edit" memory without losing provenance.

## Dedup

`memory_write` accepts two dedup modes:

- `dedup=none` (default) — never dedup.
- `dedup=semantic` — cosine-similar existing revisions in the same namespace are auto-superseded. Cross-key matches are surfaced as `DedupMatch` without auto-supersede. Threshold defaults to 0.85; override per call with `dedup_threshold`.

## Deprecation

`memory_deprecate` marks a revision as removed from the current head pool. It remains in history (audit emission for memory deprecations is in flight).

## What NOT to expect

- **No in-place edits.** Every change is a new revision.
- **No hard deletes.** Deprecation is soft; history remains.
- **No write-your-own revision IDs.** The store assigns them.
