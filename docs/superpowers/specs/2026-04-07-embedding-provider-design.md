> **Note:** This document was written when the project was named Cortex. It has since been renamed to Vanta Conduit.

# Embedding Provider Integration — Design Spec

Status: approved
Date: 2026-04-07
Scope: Wire real embedding providers into Cortex, consolidate interfaces,
define the library injection API for embedded hosts (Nanite).

## Context

Cortex's D-core memory subsystem shipped with stub embedders (`NoopEmbedder`,
`NoopQueue`) and a local `embedding.Provider` interface. The embedding
infrastructure exists (vector index, cosine similarity, chunking, MCP tools)
but has never been connected to a real provider.

Nanite has extracted a multi-provider AI package to `pkg/provider/` with a
model-parametric `Embedder` interface. This package will eventually live in
its own repo, but Cortex can import it now from
`github.com/hollis-labs/nanite/pkg/provider`.

## Decisions

### D1. Single provider interface — adopt shared package directly

Cortex drops both `internal/embedding.Provider` and `internal/memory.Embedder`.
All embedding calls go through the shared `provider.Embedder` interface:

```go
// from github.com/hollis-labs/nanite/pkg/provider
type Embedder interface {
    Embed(ctx context.Context, text string, model string) (*EmbeddingResult, error)
    EmbeddingDimensions(model string) int
}
```

The shared interface is model-parametric: one provider serves multiple models.
Cortex stores a default model as configuration, not as a property of the
provider. This is the right abstraction — the provider is a capability, the
model choice is config.

Rationale: Cortex is pre-release with zero external consumers. Aligning now
avoids permanent adapter code. The model-parametric design is more flexible
(different content types could use different models without multiple provider
instances).

### D2. Delete Cortex-local provider code

Files deleted:
- `internal/memory/embedder.go` — `Embedder` interface, `NoopEmbedder`,
  `ErrEmbedderUnavailable`
- `internal/embedding/provider.go` — local `Provider` interface
- `internal/embedding/ollama.go` — `OllamaProvider` (handled by shared package)

Files kept:
- `internal/embedding/mock.go` — `MockProvider` updated to satisfy
  `provider.Embedder` (model-parametric signature)
- `internal/embedding/search.go` — cosine similarity, ranking (Cortex domain)
- `internal/embedding/vectorindex.go` — `VectorIndex` interface (Cortex domain)
- `internal/embedding/sqlite_index.go` — `SQLiteVectorIndex` (Cortex domain)
- `internal/embedding/pgvector.go` — `PgVectorIndex` (exists, not actively wired)
- `internal/embedding/chunker.go` — text chunking (Cortex domain)

### D3. Library init via functional options

Cortex exposes a top-level library API for embedded hosts:

```go
package cortex

func Open(ctx context.Context, cfg Config, opts ...Option) (*Cortex, error)

type Config struct {
    RootDir string // data directory (today's CONTEXTD_ROOT)
}
```

Options inject optional capabilities:

```go
cortex.WithEmbedder(e provider.Embedder)     // enables embedding/search
cortex.WithEmbeddingModel(model string)       // default model for embed calls
cortex.WithQueue(q queue.Queue)               // future: auto-embed jobs
cortex.WithLogger(fn func(string, ...any))    // optional logging
```

What `Open()` does internally:
1. Opens/migrates the SQLite store (schema v9+)
2. Creates the memory subsystem, passing the embedder if present
3. Starts the decay goroutine
4. Returns the `*Cortex` handle

Closing: `Cortex.Close()` stops the decay goroutine and closes the store.
It does NOT close the embedder — the host owns that lifecycle.

### D4. Nanite injects its provider

When Nanite embeds Cortex:

```go
c, err := cortex.Open(ctx, cortex.Config{RootDir: dataDir},
    cortex.WithEmbedder(naniteEmbedder),
    cortex.WithEmbeddingModel("text-embedding-3-small"),
)
```

Nanite owns the provider lifecycle, configuration, and UI for selecting
the embedding provider/model. Cortex receives a configured instance and
uses it. If Nanite shuts down the provider, Cortex calls return errors
naturally.

When Cortex runs standalone (future — Track F/G), it imports
`github.com/hollis-labs/nanite/pkg/provider` directly, creates its own
provider instance, and provides its own configuration GUI.

### D5. On-demand embedding, async auto-embed later

**Now:** On-demand only. The `Cortex.Embed()` and `Cortex.Search()` library
methods are the API. MCP tools (`context_embed`, `context_search`) delegate
to these methods.

**Later (Track I — queue module):** `WithQueue()` enables auto-embed.
Writes dispatch an embed job to the queue (fire-and-forget). The job calls
`Cortex.Embed()` — the same idempotent method used for on-demand.

What we build now to support this:
- `Embed()` is idempotent — re-embedding overwrites the vector safely
- `NoopQueue` stub remains in memory package — drops jobs silently
- No queue interface defined in Cortex — the shared queue package will
  define it (same pattern as provider)

What we do NOT build now:
- Backfill command (re-embed all records)
- Semantic dedup
- Auto-embed triggers on write
- Queue workers

### D6. Model-scoped vectors

Vectors are stored keyed by `(record_id, model)`. This is already the
schema. Switching models means old vectors sit harmlessly — search uses
the current configured model, so stale vectors are naturally excluded.
Re-embed explicitly or via future backfill job.

### D7. Library-level embed and search methods

Logic moves from MCP adapter into the `*Cortex` handle:

```go
func (c *Cortex) Embed(ctx context.Context, recordID string) error
func (c *Cortex) Search(ctx context.Context, query string, opts SearchOptions) ([]SearchResult, error)
```

The MCP adapter becomes a thin wrapper: parse MCP params → call library
method → format response. This enables embedded hosts (Nanite) to call
embed/search directly without MCP.

The "unavailable" path: when no embedder is configured, these methods
return a sentinel error. MCP tools translate it to the structured
`embedding_unavailable` response. Clean degradation, no panics.

### D8. Memory subsystem changes

`memory.NewStore()` signature changes: instead of accepting
`memory.Embedder`, it accepts `provider.Embedder` (or nil for
no-embed mode). The memory subsystem uses the same shared type as
everything else. Calls pass the configured default model from the
`*Cortex` handle.

## Dependency graph

```
github.com/hollis-labs/nanite/pkg/provider  (shared, eventually own repo)
    ↑ imports
github.com/hollis-labs/cortex
    ├── cortex.Open() — library entry point
    ├── internal/embedding/ — vector index, search, chunking, mock
    ├── internal/memory/ — memory subsystem (consumer of embedder)
    └── internal/mcpadapter/ — MCP wrapper (delegates to library)
```

## Out of scope

- Standalone Cortex provider configuration (GUI/config — Track F/G)
- Auto-embed on write (needs queue — Track I)
- Backfill, semantic dedup, semantic similarity (D-deferred, needs queue)
- Queue interface or workers (separate package, separate design)
- PgVectorIndex wiring (exists, future scaling option)
- Rename/identity (Track A)

## Cross-agent dependencies

- **Shared provider package** (`github.com/hollis-labs/nanite/pkg/provider`):
  must expose `Embedder` interface with
  `Embed(ctx, text, model) (*EmbeddingResult, error)` and
  `EmbeddingDimensions(model) int`. Currently being finalized by Nanite agent.
- **Shared queue package** (future): Cortex is first consumer. Separate
  design session in progress.
