# Vanta Conduit — Session Boot Prompt

Updated: 2026-04-08

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

- **D-deferred. Backfill + semantic dedup** — done.
  - Backfill CLI: `contextd backfill-embeddings [--namespace=...]`
  - Semantic dedup: opt-in via `Dedup: "semantic"` on `WriteInput`.
    Same-key matches auto-supersede; cross-key matches set `DedupMatch`
    without superseding. Threshold from `config.yaml` (default 0.85),
    overridable per-call via `DedupThreshold`.
  - MCP `memory_write` tool has `dedup` and `dedup_threshold` params.
- **Ordering fix** — done. Monotonic ULIDs (`ulid.Monotonic`) + RFC3339Nano
  timestamps eliminate nondeterministic revision ordering.
- **Config file** — done. `~/.conduit/config.yaml` for embedding
  provider/model and dedup threshold. Falls back to env vars for auth
  (`OPENAI_API_KEY`).

### In progress / not started

- **G. Release readiness** — blocked on docs and final stabilization.

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
  `WithLogger()`, `WithQueue()`, `WithDedupThreshold()`.
- **Config file:** `~/.conduit/config.yaml` — `embedding.provider`,
  `embedding.model`, `dedup.similarity_threshold`. Loaded by
  `internal/config.Load()`. Defaults: OpenAI, text-embedding-3-large, 0.85.
- **ULIDs:** monotonic via `ulid.Monotonic(rand.Reader, 0)` in `ids.go`.
  Guarantees lexicographic ordering within the same millisecond.
- **Timestamps:** `time.RFC3339Nano` in all memory tables. `parseMemoryTime()`
  falls back to `time.DateTime` for backward compat.
- **Semantic dedup:** `internal/memory/dedup.go` — `findSemanticMatch()`
  uses `Recall(RankingSimilarity)`. Wired into `WriteRevision()` when
  `Dedup: "semantic"` is set. Same-key → auto-supersede. Cross-key →
  `DedupMatch` only.
- **Backfill CLI:** `contextd backfill-embeddings [--namespace=...]` —
  iterates unembedded revisions, calls `EmbedRevision()` for each.
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

## Session 2026-04-14 — Engine planning handoff

Next session is an Engine (task tool) review/planning pass. Context to
load in before discussing:

### Engine project ID

Renamed `cortex` → `vanta-conduit` in the Engine postgres DB this session.
All tasks (259), sprints (10), backlog items (17), activity events (2)
migrated. Project name stays "Vanta Conduit". Use
`ENGINE_POSTGRES_DSN=postgres://localhost/engine?sslmode=disable` if
direct DB access is needed again.

### New this session

- **EPIC-20260414-19124** — Hybrid relevance recall. Sprint
  `SPR-20260414-hybrid-relevance-s1` with 8 tasks: FTS5 (`TASK-...-002`),
  BM25 fetch (`-003`), `RankingRelevance` RRF fusion (`-004`), reinforce
  on all modes (`-005`), reranker interface (`-006`), eval harness
  (`-007`), repurposed chunking (`TASK-20260316-008`), research
  (`TASK-20260316-024`).
- **EPIC-20260414-84967** — Knowledge domain. Sprint
  `SPR-20260414-knowledge-domain-s1` with 8 tasks: domain discriminator
  + `DomainPolicy` interface (`-008`, foundational — has a planning
  decision about plugin extensibility), facets `kind`/`source`/`pointer`
  (`-009`), `knowledge_write` (`-010`), `conduit_lookup` (`-011`),
  framework+agentrc indexer (`-012`), Obsidian ingester (`-013`), Nil
  ingester (`-014`), agent convention rollout (`-015`).
- **TASK-20260414-001** — Wire memory + embeddings into HTTP serve
  mode. Both epics depend on this for HTTP surfaces.
- **TASK-20260414-016/017/018** — Standalone P2 primitives enabling an
  external inbox workflow for memory ingestion:
  `import_batch_id`, server-side `similarity_threshold`, write
  `dry_run`.
- **BLG-20260414-016** — Backlog: evaluate external vector DB (Qdrant /
  Weaviate / LanceDB / pgvector) when SQLite + in-process cosine hits
  scale pain.

### Archived / superseded this session

- `TASK-20260316-007` (pgvector) — archived; replaced by BLG above.
- `TASK-20260225-236/237/238` — stale doc-notes.
- `TASK-20260314-072` (demo seed), `-108` (drift detection), `TASK-20260313-053` (frontend bug).
- `BLG-20260311-051` (temporal decay) — deleted, already implemented.

### Cortex → Conduit renames (titles)

9 tasks had "Cortex" in their title; renamed to "Conduit":
`TASK-20260311-L2-002`, `-L2-003`, `TASK-20260314-137`, `-147`, `-214`,
`-215`, `-318`, `TASK-20260318-002`, `TASK-20260313-036`.

Some descriptions still say "Cortex" — cosmetic, fix as tasks are picked
up.

### Open workload snapshot

- 33 todo tasks total across vanta-conduit (down from 39).
- 2 active epics (hybrid relevance, knowledge domain), both with
  sprints and tasks populated; both awaiting planning review.
- Two existing sprints with loose associations that may need re-slotting
  during planning: `SPR-20260320-Cortex Pipeline Enhancements` and
  `SPR-20260314-Cortex — Context Pipeline & Maintenance` (tasks `-214`
  and `-215` currently sit in the latter).

### Planning session asks

1. Review EPIC-20260414-19124 (hybrid relevance) — task breakdown,
   priorities, sprint ordering.
2. Review EPIC-20260414-84967 (knowledge domain) — especially the
   polymorphism decision in `TASK-20260414-008` (in-tree `DomainPolicy`
   vs plugin-extensible domains).
3. Decide sprint/epic ordering: hybrid recall first (more self-contained)
   vs knowledge domain first (unblocks agent-first-search use case).
   Neither is strictly blocked on the other — `TASK-20260414-001` is the
   only shared hard dep.
4. Slot `TASK-20260414-001` (HTTP wiring) — precedes GUI work and is a
   hard dep for any HTTP surfaces on the new epics.
5. Re-evaluate GUI tasks (`TASK-20260225-239..242`,
   `TASK-20260228-001..004`) after new domains/recall land.
6. Sprint/sprint-archive pass on `SPR-20260320-Cortex Pipeline
   Enhancements` and `SPR-20260314-Cortex — Context Pipeline &
   Maintenance`.

### Other context notes

- `cmd/smoke` is the embedding smoke test. Documented in `docs/DEV.md`.
  Uses the live `~/.conduit` store.
- `~/.cerberus/config.yaml` now has `conduit-api` / `conduit-frontend`
  (renamed from `cortex-*`), with `env_file: .env` loading
  `OPENAI_API_KEY` into the service process.
- Cortex data migrated to Vanta (137 legacy records in the `records`
  table; schema v9). Legacy records are NOT in the memory domain —
  explicitly deferred; the knowledge domain's `kind=pointer` pattern is
  how we'll surface valuable legacy content going forward.
- User runs an external inbox for memory ingestion review (plan: agent
  proposes drafts → inbox UI → approved entries written to Vanta as
  canonical). Tasks `-016/017/018` support this.
- Nil was previously called Nanite. Nanite is now the agent chat
  harness; Nil is the notes app.
