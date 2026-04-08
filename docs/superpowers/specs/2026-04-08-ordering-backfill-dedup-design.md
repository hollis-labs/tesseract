# ULID Ordering Fix, Embedding Backfill, and Semantic Dedup — Design Spec

**Date:** 2026-04-08
**Status:** Approved

## Goal

Fix the revision ordering bug (ULID monotonicity + timestamp precision), configure OpenAI text-embedding-3-large as the embedding model, add a backfill CLI command, and implement opt-in semantic dedup on the memory write path.

## 1. ULID Monotonic Fix

**Problem:** `internal/memory/ids.go` uses `rand.Reader` for ULID entropy. ULIDs generated in the same millisecond have random suffixes, making lexicographic ordering unreliable. Combined with second-precision `created_at` timestamps, `ORDER BY created_at DESC, revision_id DESC` produces nondeterministic results.

**Fix:** Replace `rand.Reader` with a package-level `ulid.Monotonic(rand.Reader, 0)` entropy source. This guarantees monotonically increasing ULIDs within the same millisecond. The `oklog/ulid/v2` library handles thread safety internally.

```go
var entropy = ulid.Monotonic(rand.Reader, 0)

func NewULID() string {
    return ulid.MustNew(ulid.Timestamp(time.Now()), entropy).String()
}
```

## 2. Timestamp Nano Precision

**Problem:** `created_at` and `last_accessed_at` use `time.DateTime` format (`"2006-01-02 15:04:05"`), which has second-level precision. Two revisions in the same second produce identical timestamps.

**Fix:** Change all memory-package timestamp formatting to `time.RFC3339Nano`. This gives nanosecond precision and eliminates the tiebreaker problem for all practical purposes.

**Blast radius:**
- Write paths: `write.go` (INSERT created_at, UPDATE last_accessed_at), `activation.go` (UPDATE last_accessed_at)
- Read/parse paths: `read.go` (scanRevision, scanState), `recall.go` (Since/Until filters, expiry check), `decay.go` (timestamp parsing)
- Schema defaults: Replace SQLite `DEFAULT (datetime('now'))` with application-side formatting
- Test fixtures: `decay_test.go` (3 locations that insert backdated timestamps)

**Backward compatibility:** Go's `time.Parse(time.RFC3339Nano, ...)` accepts both nano and second-precision strings. Existing data with old-format timestamps will parse correctly. New data gets full nanosecond precision.

## 3. Embedding Model Configuration

**Model:** OpenAI `text-embedding-3-large` (3072 dimensions).

**Configuration file:** `~/.conduit/config.yaml`

```yaml
embedding:
  provider: openai
  model: text-embedding-3-large

dedup:
  similarity_threshold: 0.85
```

**Config loading:**
- `cmd/contextd/main.go` loads `~/.conduit/config.yaml` at startup
- Falls back to environment variables: `OPENAI_API_KEY` for auth
- Config values passed to `conduit.Open()` via existing options (`WithEmbedder`, `WithEmbeddingModel`) plus new option `WithDedupThreshold`
- The library entry point (`conduit.Open`) stays config-agnostic — config file parsing is a server concern

**No migration needed:** All existing revisions are unembedded (NoopQueue was dropping embed jobs). Fresh backfill with the new model.

## 4. Backfill CLI

New subcommand: `contextd backfill-embeddings`

**Behavior:**
- Queries `memory_revisions WHERE embedding_vector IS NULL`
- Optional `--namespace` flag to filter
- Calls `memory.Store.EmbedRevision()` for each revision, sequentially
- Logs progress: `[42/128] embedded revision rev_abc123`
- Respects context cancellation (Ctrl+C graceful shutdown)
- Exits with count summary: `Embedded 128 revisions (0 errors)`

**No queue needed.** Sync execution is appropriate for a CLI tool. Rate limiting is handled by the provider client.

## 5. Semantic Dedup

**Trigger:** Opt-in per write. Caller passes `Dedup: "semantic"` on `WriteInput`. Default is no dedup.

**Flow:**
1. Caller writes a memory revision with `Dedup: "semantic"`
2. Before writing, extract text from the incoming payload (summary + body)
3. Embed the text using the configured embedder and model
4. Call `Recall(RankingSimilarity)` in the target namespace with the embedded query
5. If the top result's score >= threshold (from config, default 0.85):
   - If the matched revision belongs to the **same memory** (same namespace + key): set `Supersedes` on the new revision to point at the matched revision ID. The existing auto-deprecation logic handles the rest — this is an update-in-place.
   - If the matched revision belongs to a **different memory** (different key): set `DedupMatch` on the response but do NOT set `Supersedes`. The write proceeds as a new memory. The caller can decide whether to merge, skip, or link them. Cross-key supersedes would break the memory model.
6. Proceed with normal write
7. Return the new revision with `DedupMatch` set to the matched revision ID (if dedup found a match, regardless of same-key or cross-key)

**New fields on `WriteInput`:**
```go
Dedup          string  // "none" (default), "semantic"
DedupThreshold float64 // optional per-call override; 0 = use config default
```

**New field on `Revision` (response):**
```go
DedupMatch string `json:"dedup_match,omitempty"` // matched revision ID, if dedup triggered
```

**Threshold configuration:**
- Default: 0.85 (from `config.yaml` → `dedup.similarity_threshold`)
- Per-call override via `WriteInput.DedupThreshold`
- Stored on `memory.Store` as a field, set via `conduit.Open()` option `WithDedupThreshold(float64)`

**Namespace scoping:** Dedup searches only within the target namespace. No cross-namespace matching.

**MCP tool changes (`memory_write`):**
- New optional parameter: `dedup` (string: `"none"` or `"semantic"`)
- New optional parameter: `dedup_threshold` (float, optional override)
- Response includes `dedup_match` field when dedup triggered

**Frontend (out of scope for this spec):**
- Config UI for threshold adjustment
- Dedup indicators in memory views

## Architecture Notes

- The dedup check happens inside `WriteRevision()`, before the INSERT. If the embedder is unavailable and `Dedup: "semantic"` is requested, return `ErrEmbedderUnavailable`.
- The dedup threshold is a `float64` field on `memory.Store`, passed via constructor. The `conduit.Open()` option `WithDedupThreshold` sets it.
- Config file parsing uses `gopkg.in/yaml.v3` (already in the Go stack conventions).
- The backfill command reuses `memory.Store.EmbedRevision()` — no new embedding logic needed.
