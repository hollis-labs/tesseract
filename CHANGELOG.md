# Changelog

All notable changes to Vanta Conduit are recorded here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project uses [Semantic Versioning](https://semver.org/spec/v2.0.0.html). Pre-1.0: minor bumps for additive surface, patch bumps for fixes — breaking changes can land in any minor.

Consumers (Nanite, Cerberus, custom Conduit clients) should watch this file for new MCP tools, HTTP routes, store-method additions, and configuration changes. Each release notes the user-visible MCP tool IDs and `/v1/*` routes that landed.

## [Unreleased]

## [0.5.1] — 2026-04-26

Audit-pipeline catch-up + recall ergonomics. Memory/knowledge writes now appear in `context_audit`, the audit-emit code path is consolidated through canonical `Store.Emit*` helpers, the daemon degrades gracefully when no embedding key is present, and a new `GET /v1/recall` endpoint gives scripted / agent consumers a query-param surface that mirrors `POST /v1/conduit/lookup`. Includes the audit fixes from `CW-20260419-0040` (PR #11) plus the audit-helper consolidation from `CW-20260419-0060`.

### Changed

- `contextstore` now exposes canonical `Store.Emit*` helpers for every audit
  event type. Handlers and CLI commands migrated off direct `AuditEvent`
  construction; `RecordAuditEvent` is now unexported. Event-type identifiers
  centralized as constants in `internal/contextstore/audittypes.go`.
  (`CW-20260419-0060`)
- `context_audit` now captures `maintenance.trim`, `maintenance.compact`, and
  `packet` events that were previously failing the store's validation
  silently. This is a visibility improvement — no event-type names changed.
  (`CW-20260419-0060`)

### Fixed

- `memory_write` / `memory_deprecate` / `memory_promote` / `knowledge_write`
  now record audit events. Previously these write paths bypassed the audit
  log entirely, making `context_audit` queries for memory/knowledge
  namespaces silently empty. Emits at the domain layer so all surfaces
  (MCP, HTTP, library-facade) are covered. (`CW-20260419-0040`)
- `context_audit` MCP response now includes the `metadata` field per row
  when non-empty. HTTP `/v1/context/audit` already projected metadata via
  struct serialization. (`CW-20260419-0040`)
- `contextd` no longer instantiates the OpenAI embedder when
  `OPENAI_API_KEY` is unset. Previously the embedder was constructed
  unconditionally and failed at every invocation; now the daemon logs a
  warning at startup and falls back to BM25-only recall, keeping the
  service usable in offline / no-key environments.

### Added

- Six new audit event types: `memory.write`, `memory.supersede`,
  `memory.deprecate`, `memory.promote`, `knowledge.write`,
  `knowledge.supersede`. Existing event-type strings unchanged.
  (`CW-20260419-0040`)
- `memory.AuditSink` interface + `memory.Store.SetAuditSink(sink)` setter —
  new dependency-injection seam for domain-layer audit. `contextstore.Store`
  satisfies the interface structurally. (`CW-20260419-0040`)
- **HTTP route: `GET /v1/recall`** — query-param-driven recall optimised
  for scripted / agent consumption. Mirrors `POST /v1/conduit/lookup` but
  takes `?namespace=…&tags=…&limit=…&format=brief|full`. Default `brief`
  format returns condensed `{revision_id, memory_id, domain, namespace,
  memory_key, tags, confidence, summary, created_at}` items; `full`
  returns the complete `RecallResult`. Single-namespace only; respects
  bearer-token namespace ACLs. Surfaces `similarity_unavailable` (503) and
  `validation_error` (400) consistent with existing recall handlers.

### Not in scope (flagged)

- `memory.EmbedRevision` async queue audit — infrastructure-only
  observability; not an audit concern. Intentionally scoped out of
  `CW-20260419-0040`.
- `memory.Deprecate` audit events record `actor="system"` because the
  method signature has no actor parameter. Extending the signature is a
  follow-up ticket candidate. (`CW-20260419-0040`)

## [0.5.0] — 2026-04-19

MCP Surface v2 (PR #9 `feat/mcp-surface-v2`). Rewrites memory/knowledge/unified/meta MCP tool descriptions against a consistent v2 template, adds MCP protocol annotations per spec §5.4, and ships `vanta_skills` as a progressive-discovery meta-tool backed by 11 embedded markdown skills. Additive — no Go library API changes; existing consumers are unaffected.

### Added

- **MCP tool: `vanta_skills`** — progressive-discovery meta-tool. No-args returns an 11-entry index; `name=<skill>` returns the full markdown body. Skills embedded via `//go:embed` at `internal/mcpadapter/skills/*.md`: `start-here`, `namespaces`, `facets-and-kinds`, `revisions`, `recall-and-ranking`, `promotion`, `views`, `memory`, `knowledge`, `context-packet`, `audit`. `start-here` is indexed first; rest alphabetical.
- **MCP protocol annotations** on all 12 memory / knowledge / unified / meta tools — `ReadOnlyHint`, `IdempotentHint`, `DestructiveHint`, `OpenWorldHint` per spec §5.4. `memory_deprecate` correctly carries `IdempotentHint=true` (second call is a no-op).
- **Drift guardrail:** `internal/mcpadapter/annotations_test.go` fails CI if any of the 12 v2-template tools drops its required annotations.

### Changed

- **MCPServer version string** bumped `0.4.0 → 0.5.0`.
- **12 MCP tool descriptions** rewritten to v2 template — bold action phrase + `Kind of content` / `Scope` / `Use this when` / `Don't use this for` / `Deeper:` bullets plus concrete parameter-value examples. Covers all memory, knowledge, unified-lookup, and meta surfaces.
- **30 context-domain tool descriptions** get an appended `See vanta_skills start-here for the primitive model.` footer. Minimum-touch; full context-domain rewrite is deferred.
- **Parity catalog** — `vanta_skills` registered as MCP-only meta-tool in `surfaceCatalog`.
- **`docs/MCP_TOOLS.md`** refreshed — new Skills section, `Deeper` column on memory/knowledge/unified tables, new Meta section for `vanta_skills`.

### Follow-ups filed (not in this release)

- `CW-20260419-0040` — `memory_write` / `memory_deprecate` / `knowledge_write` don't call `RecordAuditEvent`; `context_audit` silently omits those writes. `AuditEvent.Metadata` persisted but not projected in the MCP response.
- `CW-20260419-0041` — `context_packet` uses `max_items` / `max_tokens_estimate` while `context_broker_*` uses `budget_items` / `budget_tokens` (same knob, two names, divergent defaults 8000 vs 4000).
- `CW-20260419-0042` — `docs/SPECS/VIEWS.md` documents `evaluation_meta` fields (`normalized_selector`, `returned_count`) absent from the actual `EvaluateResult` struct / `views_evaluate` handler.

## [0.4.0] — 2026-04-16

Hybrid Relevance S1 (`SPR-20260414-hybrid-relevance-s1`, `EPIC-20260414-19124`). Adds a fourth ranking mode `relevance` that fuses BM25 (FTS5) and cosine via Reciprocal Rank Fusion and multiplies by the existing activation-style modifiers. Becomes the smart default for query-backed agent recall so freshly-written memories surface immediately via the BM25 arm (previously: invisible to semantic search until the async embedder caught up). Also ships BLG-037 (context-budget fix for MCP recall responses).

Minor-bump candidate — default ranking changes when a query is provided, and access reinforcement widens from activation-only to every recall mode.

### Added

- **Ranking mode: `relevance`** — `RRF(bm25_rank, cosine_rank, k=60) * statusW * originW * confidence * recency * activation`. Cosine arm is optional; BM25-only fires when no embedder is configured.
- **Schema v12** — FTS5 external-content virtual table `memory_revisions_fts` over `payload_summary`, `payload_body`, `tags`, with AFTER INSERT / AFTER DELETE sync triggers and one-shot backfill for existing rows. Content-only index; status filtering happens at query time via JOIN to keep the BM25 arm deterministic.
- **Reranker interface** (`memory.Reranker`, `RerankerFunc`) + **Cohere/Voyage-compatible HTTP adapter** (`memory.HTTPReranker`). Per-call opt-in via `RecallInput.Reranker` naming a reranker registered with `Store.RegisterReranker`. Self-hosted `bge-reranker` gateways that mimic the same JSON shape also work.
- **Recall regression gate** (`internal/memory/recall_eval_test.go`) — seeds a deterministic corpus, runs three fixture query classes (exact acronym, multi-token semantic, mixed), computes nDCG@10 and hit-rate@10 per mode, and enforces: (1) relevance's aggregate nDCG does not regress below similarity's, (2) relevance strictly outperforms similarity on ≥1 fixture.

### Changed

- **Ranking default is now smart**: empty `Ranking` with a non-empty `Query` resolves to `relevance`; empty `Ranking` with no query stays on `activation`. Explicit callers are unaffected.
- **MCP tool descriptions** updated on `memory_recall` and `conduit_lookup`: `activation, chronological, similarity, or relevance (default: relevance when query is set, else activation)`.
- **Access reinforcement widens to all recall modes** (was activation-only). Dense-only or chronological queries now bump `last_accessed_at` and `access_count` too so hot memories keep their activation trail when agents switch ranking modes. Consumers that depended on "only activation-mode recall reinforces" should flag this — relevant for the Nanite consumer.

### Fixed

- **BLG-037** — `memory_recall` and `conduit_lookup` were shipping the full `EmbeddingVector` (~39KB/record for text-embedding-3-large) in every `Revision` JSON response, blowing agent context budgets on recalls of 5+. `json:"-"` on `Revision.EmbeddingVector` drops the field from every JSON response universally. Struct field is retained — similarity ranking still reads it directly via `similarityScore`/`CosineSimilarity`; SQL storage is untouched. Smoke-test on a live store: `memory_recall limit=10` went from ~390KB to 26KB; `conduit_lookup limit=20` went from ~660KB to 40KB.

### Operator notes

- First boot after upgrade runs migration case 12, which creates the FTS5 index and backfills every existing `memory_revisions` row. On a fresh / small corpus this is instant; on larger stores, expect a one-shot startup cost proportional to revision count × payload length.
- `modernc.org/sqlite` ships FTS5 by default — no build tags or external sqlite binary needed.
- Out of scope for this release (tracked separately): external vector DB evaluation (`BLG-20260414-016`), markdown/code-aware chunking (moved to `SPR-20260416-ingest-s1` under `EPIC-20260415-64937`).

## [0.3.0] — 2026-04-15

MCP↔HTTP parity batch 1: agent-access fixes plus a durable drift guardrail. Closes the gaps surfaced after the knowledge-domain S1 merge so an agent booted against `mcp__vanta__*` has functional parity with the HTTP `/v1/*` surface for context, memory, and knowledge reads.

### Added

- **MCP tool: `context_estimate`** — record count + payload bytes + token proxy for a selector. Peer of `POST /v1/context/estimate`.
- **MCP tool: `views_evaluate`** — full-power selector evaluation with `evaluation_meta`. Peer of `POST /v1/views/evaluate`.
- **MCP tool: `memory_get_revision`** — fetch a memory revision by id. Scope `memory:read`. Peer of `GET /v1/memory/revisions/{id}`.
- **MCP tool: `knowledge_get`** — current knowledge revision for `(namespace, key)`. Scope `memory:read`. Peer of new `GET /v1/knowledge/current`.
- **MCP tool: `knowledge_history`** — full revision history for a knowledge entry, newest-first. Scope `memory:read`. Peer of new `GET /v1/knowledge/history`.
- **HTTP routes:** `GET /v1/knowledge/current?namespace=&key=` and `GET /v1/knowledge/history?namespace=&key=`.
- **Store helpers:** `contextstore.Store.Estimate`, `contextstore.Store.Evaluate`, `contextstore.NormalizedScope`. Used by both MCP and HTTP — no duplicated logic.
- **Knowledge store reads:** `knowledge.Store.GetCurrent` / `knowledge.Store.GetHistory`. Domain-filtered wrappers; non-knowledge revisions return `memory.ErrNotFound`.
- **Drift guardrail:** `tests/parity/parity_test.go` — fails CI if a tool or HTTP route is added without a matching `surfaceCatalog` entry. Each one-sided op carries an explicit waiver string with a reason.
- **Catalog doc:** `docs/MCP_TOOLS.md` — agent-facing per-domain tool tables (scopes, HTTP peers), transport config for `~/.claude.json`, five playbooks (write memory, write knowledge, unified lookup, boot-time packet fetch, resolve revision id).
- **Adapter introspection:** `mcpadapter.Adapter.RegisterAllTools` exported so the parity test (and other tooling) can list registered tools without a stdio server.

### Changed

- `MCPServer` version string bumped to `0.3.0` (was `0.1.0`).
- `contextapi.handleEstimate` now delegates to `contextstore.Store.Estimate`. Response shape unchanged.
- `contextapi.handleView` now delegates to `contextstore.Store.Evaluate`. Response shape unchanged.
- Duplicate `normalizedScope` helper consolidated from `contextstore` + `contextapi` into one exported `contextstore.NormalizedScope`.

### Documentation

- `README.md` and `.agentrc/boot-prompt.md` link to `docs/MCP_TOOLS.md`.
- Portfolio rename docs (`docs/superpowers/plans|specs/2026-04-07-cortex-to-vanta-conduit-rename.md`) updated to `mcp__vanta__*` tool IDs.

### Operator notes

The `cortex` → `vanta` MCP server rename in `~/.claude.json` is required for agents to reach this binary; old `cortex` entries point at the legacy `~/.cortex/` data root and miss every tool added since the knowledge-domain merge. See `docs/MCP_TOOLS.md` for the new `~/.claude.json` block.

After a `go install ./cmd/contextd` the Cerberus-managed `conduit-api` service must be restarted so the HTTP and MCP surfaces stay binary-locked.

## [0.2.0] — 2026-04-15

Knowledge Domain S1 (`SPR-20260414-knowledge-domain-s1`, `EPIC-20260414-84967`). Adds a second info domain (`knowledge`) alongside `memory`, wires the memory subsystem into HTTP serve mode, and introduces `conduit_lookup` for cross-domain search. Merged as PR #3.

### Added

- **Domain discriminator** — `domains.Domain` type + in-tree `DomainPolicy` interface (`MemoryDomain`, `KnowledgeDomain`).
- **Knowledge facets** — `kind`, `source`, `pointer{scheme, locator, resolved_at}` on `memory.Revision`. Required on knowledge writes.
- **MCP tool: `knowledge_write`** + HTTP `POST /v1/knowledge/write` — pointer-first writes with required facets.
- **MCP tool: `conduit_lookup`** + HTTP `POST /v1/conduit/lookup` — unified search across memory + knowledge with facet histogram.
- **HTTP `/v1/memory/*` surface** — `write`, `current`, `history`, `revisions/{id}`, `recall`, `promote`, `deprecate` routes (`TASK-20260414-001`).
- **`memory.RecallFilters`** — new filter fields `Domains`, `FacetKinds`, `FacetSources`.
- **Schema migrations** — v10 adds `domain` (default `'memory'`) to `memory_state` + `memory_revisions` with indexes; v11 adds nullable flat facet columns with partial indexes on `facet_kind` and `facet_source`.

### Changed

- `memory.Recall` namespace validation relaxed to non-empty; per-domain shape enforced on write via `DomainPolicy`.

### Deferred (post-MVP)

- React GUI parity — `BLG-20260415-003`
- Ingester adapter + plugin ecosystem — `EPIC-20260415-64937`
- Decay policy per `kind` — `BLG-20260415-001`
- Additional "search first" surfaces — `BLG-20260415-002`

## [0.1.0] — 2026-04-08

Foundational embedding + memory release. Bundles PR #1 (go-queue integration) and PR #2 (D-deferred tracks: auto-embed, similarity recall, facade, ordering, config, dedup).

### Added

- **`go-queue` integration** — SQLite-backed `go-queue` instance for async embed jobs. `QueueAdapter` bridges `memory.JobQueue` → `queue.Queue`. `WithQueue()` functional option on `conduit.Open()`. Worker lifecycle with retry (3 max tries / 30s delay). Separate `queue.db` for write safety.
- **Auto-embed on write** — `memory.Store.EmbedRevision()` loads revision, extracts text, calls embedder, writes vector inline to `embedding_model`/`embedding_vector` columns. Queue embed handler wired to call it on every write.
- **Similarity recall** — `Recall(RankingSimilarity)` embeds the query and ranks candidates by cosine similarity. Exposed on both MCP `memory_recall` and library API. Unembedded revisions filtered out.
- **Conduit facade** — `WriteMemory`, `RecallMemory`, `GetCurrentRevision`, `GetRevisionHistory`, `EmbedRevision` on `*Conduit`. Library consumers no longer need to reach through `.Store()` / `.MemoryStore()`.
- **Backfill CLI** — `contextd backfill-embeddings [--namespace=...]` iterates unembedded revisions and embeds them.
- **Semantic dedup** — opt-in via `Dedup: "semantic"` on `WriteInput`. Same-key matches auto-supersede; cross-key matches set `DedupMatch` without superseding. MCP `memory_write` exposes `dedup` + `dedup_threshold` params.
- **Config file** — `~/.conduit/config.yaml` for embedding provider/model and dedup threshold. Loaded by `internal/config.Load()`. Defaults: OpenAI `text-embedding-3-large`, threshold 0.85. Falls back to env vars for auth.
- **SQLite WAL mode** enabled for better concurrent read/write performance.

### Changed

- **Monotonic ULIDs** (`ulid.Monotonic`) + **RFC3339Nano timestamps** eliminate the nondeterministic revision ordering bug. `parseMemoryTime()` falls back to `time.DateTime` for backward compat.
- All stale `cortex` references replaced with `conduit` (plugin CLI usage strings, env vars).

### Fixed

- Stale `mcp-helpers` and `otel` replace directives (`fragments-engine/libs/` → `framework/libs/`).
- Empty-text guard in `EmbedRevision` (would previously call the embedder with empty input).

## [0.0.1] — 2026-04-08

Initial standalone-repo baseline tag at commit `3b92f5c`. Captures the post-rename state of the codebase extracted from `fragments-engine/cortex/` to its own repo at `github.com/hollis-labs/vanta-conduit`. No formal release notes — this tag exists primarily to anchor `git describe` output.

[Unreleased]: https://github.com/hollis-labs/vanta-conduit/compare/v0.5.1...HEAD
[0.5.1]: https://github.com/hollis-labs/vanta-conduit/compare/v0.5.0...v0.5.1
[0.5.0]: https://github.com/hollis-labs/vanta-conduit/compare/v0.4.0...v0.5.0
[0.4.0]: https://github.com/hollis-labs/vanta-conduit/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/hollis-labs/vanta-conduit/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/hollis-labs/vanta-conduit/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/hollis-labs/vanta-conduit/compare/v0.0.1...v0.1.0
[0.0.1]: https://github.com/hollis-labs/vanta-conduit/releases/tag/v0.0.1
