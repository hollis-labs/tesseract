# Vanta Conduit — Session Boot Prompt

Updated: 2026-04-07

## Current state

Vanta Conduit is a standalone repo (`github.com/hollis-labs/vanta-conduit`)
extracted from `fragments-engine/cortex/`. The old cortex repo is archived
at `~/.archived/cortex/`.

### Completed tracks

- **A. Rename + identity** — done. All references updated to Vanta Conduit.
- **B. Repository extraction** — done. Standalone repo with own `go.mod`.
- **D-core. Memory domain** — done (PR #1). 15 tasks, 44 test functions.
  Append-only revisions, namespace hierarchy, write/read/recall/promote/
  deprecate, activation decay, 6 MCP tools, schema v9.
- **D-embedding. Provider integration** — done. `Conduit.Embed()` and
  `Conduit.Search()` library methods, MCP tools (`context_embed`,
  `context_search`, `context_rag_query`, `context_bulk_ingest`,
  `context_chunked_ingest`, `context_session_snapshot`). Uses shared
  `go-providers` package.
- **H. Shared AI provider module** — done. `go-providers` at
  `framework/libs/go-providers/`. Multi-provider: Anthropic, OpenAI,
  Ollama, Gemini, Mistral, Azure, OpenRouter, CLI adapters. Conduit
  imports via replace directive.
- **I. Shared queue module** — done. `go-queue` at
  `framework/libs/go-queue/`. Queue interface, Worker, SQLite/memory/noop
  drivers. **Wired into Conduit** via PR #1: `QueueAdapter`, `WithQueue()`
  option, SQLite-backed queue DB, worker with embed handler.

- **D-deferred. Auto-embed on write** — done. `memory.Store.EmbedRevision()`
  loads revision, extracts text from payload summary+body, calls embedder,
  writes vector inline to `memory_revisions.embedding_model/embedding_vector`.
  Queue embed handler wired to call `EmbedRevision` on every write.
- **D-deferred. Memory similarity recall** — done. `Recall()` with
  `RankingSimilarity` embeds the query, scores candidates by cosine
  similarity against stored vectors. Unembedded revisions filtered out.
  Both `context_search`/`context_rag_query` MCP tools and `memory_recall`
  now support similarity.
- **C. Cross-repo TODO audit** — done. All stale "cortex" references
  replaced with "conduit" (plugin CLI usage strings + env var).
- **E. Embedded-runtime API surface** — done. `conduit.Open()` with
  functional options + facade methods: `WriteMemory`, `RecallMemory`,
  `GetCurrentRevision`, `GetRevisionHistory`, `EmbedRevision`, `Embed`,
  `Search`, plus `Store()`/`MemoryStore()` accessors for advanced use.
- **F. Dual-mode packaging** — done. `cmd/contextd` standalone server and
  `conduit.Open()` library mode both work with clean separation.

### In progress / not started

- **D-deferred. Backfill + semantic dedup** — not started.
- **G. Release readiness** — blocked on API stabilization and docs.

## Key technical knowledge

- **Module path:** `github.com/hollis-labs/vanta-conduit`
- **Shared modules:** `go-providers` and `go-queue` at `framework/libs/`,
  imported via replace directives in `go.mod`
- **`"trigger"` is a SQLite reserved word** — quoted in DDL. All DML
  against `memory_revisions` must quote it.
- **Pre-commit hook:** gofmt + go vet + golangci-lint (misspell enforces
  US English)
- **Testing conventions:** stdlib `testing`, external test package (`_test`),
  `errors.Is` on sentinels, `var _ Interface = Impl{}` compile-time checks
- **Library entry point:** `conduit.Open(ctx, cfg, opts...)` returns
  `*Conduit`. Options: `WithEmbedder()`, `WithEmbeddingModel()`,
  `WithLogger()`, `WithQueue()`.
- **Queue wiring:** `WithQueue()` creates `QueueAdapter` (bridges
  `memory.JobQueue` → `queue.Queue`), starts worker with `"embed"` handler.
  Separate `queue.db` for write safety. `cmd/contextd/main.go` uses
  SQLite-backed queue; `conduit.Open()` accepts any `queue.Queue` impl.
- **Embed handler:** `embed_handler.go` — calls
  `memory.Store.EmbedRevision()`. Decodes `{"revision_id":"..."}` from
  job payload, embeds the revision inline.
- **Memory embedding:** `internal/memory/embed.go` — `EmbedRevision()`
  loads revision, extracts text from `Payload.Summary`+`Body`, calls
  embedder, writes vector to `embedding_model`/`embedding_vector` columns.
- **Similarity recall:** `internal/memory/recall.go` — `RankingSimilarity`
  embeds the query via `Store.embedder`, scores candidates by cosine
  similarity using `embedding.CosineSimilarity()`.
- **Conduit facade:** `WriteMemory`, `RecallMemory`, `GetCurrentRevision`,
  `GetRevisionHistory`, `EmbedRevision` on `*Conduit`. Library consumers
  no longer need to reach through `.Store()`/`.MemoryStore()`.
