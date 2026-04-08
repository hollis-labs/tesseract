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
- **I. Shared queue module** — done (the module). `go-queue` at
  `framework/libs/go-queue/`. Queue interface, Worker, SQLite/memory/noop
  drivers. **Not yet wired into Conduit** — `NoopQueue` stub still in
  `internal/memory/queue.go`.

### In progress / not started

- **D-deferred. Auto-embed on write** — unblocked by go-queue, not wired.
  `NoopQueue` in place, fire-and-forget enqueue in `write.go:163`. Need to:
  add go-queue dependency, write adapter, register embed handler, wire
  worker in `main.go` and `conduit.go`.
- **D-deferred. Memory similarity recall** — `recall.go` returns
  `ErrSimilarityUnavailable`. Embedder exists but isn't plumbed into the
  recall path.
- **D-deferred. Backfill + semantic dedup** — not started.
- **C. Cross-repo TODO audit** — not started.
- **E. Embedded-runtime API surface** — partial. `conduit.Open()` with
  functional options exists. Full API surface TBD.
- **F. Dual-mode packaging** — blocked on E.
- **G. Release readiness** — blocked on E, F.

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
  `WithLogger()`. Needs `WithQueue()` for go-queue integration.
- **Memory write path:** `write.go:163` does fire-and-forget
  `s.queue.Enqueue(ctx, Job{Kind:"embed", ...})`. Currently hits NoopQueue.
- **go-queue interface:** `queue.Queue` with `Push(ctx, jobType, payload,
  ...PushOption)`. Adapter needed: `memory.JobQueue.Enqueue` → `queue.Queue.Push`.
