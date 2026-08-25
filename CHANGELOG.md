# Changelog

All notable changes to Tesseract are recorded here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project uses [Semantic Versioning](https://semver.org/spec/v2.0.0.html). Pre-1.0: minor bumps for additive surface, patch bumps for fixes — breaking changes can land in any minor.

Consumers should watch this file for new MCP tools, HTTP routes, store-method additions, and configuration changes. Each release notes the user-visible MCP tool IDs and `/v1/*` routes that landed.

## [Unreleased]

## [0.8.0] — 2026-08-24

Typed memory namespaces, an admin operations surface, and the XDG on-disk
cutover. Consumers pinning `v0.7.x` should read the breaking-changes section
before bumping: namespace strings, two MCP tool IDs, one HTTP route, and the
default on-disk layout all changed.

### Breaking changes

- **Memory namespaces are now typed and fixed-depth.** `ParseNamespace` accepts
  exactly three shapes — `user/{id}/memory/{type}`,
  `user/{id}/project/{project_id}/memory/{type}`, and
  `user/{id}/session/{session_id}/memory/{type}`. The segment-count rule went
  from `len(parts) < 3 || len(parts) > 5` to `len(parts) != 4 && len(parts) != 6`,
  so the legacy flat `user/{id}/memory` is rejected on write with
  `wrong segment count (want 4 or 6, got N)`. `{type}` is validated against an
  allowlist (`decisions`, `feedback`, `followups`, `learnings`, `limitations`,
  `notes`, `outcomes`, `references`). **Recall is more permissive than write:**
  the flat form is still accepted there as a *prefix*, spanning every typed
  sub-namespace under that scope. Callers that construct namespace strings must
  add a type segment; callers that only recall may not need to change.
- **MCP tool IDs renamed:** `conduit_lookup` → `tesseract_lookup`,
  `vanta_skills` → `tesseract_skills`. The other 40 tool IDs are unchanged.
- **HTTP route renamed:** `/v1/conduit/lookup` → `/v1/tesseract/lookup`.
- **On-disk layout moved to XDG roots** via `go-apppaths`. Data, state, cache and
  config now resolve independently instead of nesting under one base directory.
  `CONTEXTD_ROOT` is retired and kept alive by a **one-release deprecation shim**
  that maps it onto all four `$XDG_*_HOME` vars; it is scheduled for removal in
  the release after this one. Migrate to `$XDG_*_HOME`, or to `TESSERACT_DB_PATH`
  / `TESSERACT_WORKSPACE` for the common cases. Run `contextd path` to see where
  a given environment actually resolves.

### Added

- **Admin operations surface** — 15 new routes: `/v1/admin/setup`,
  `/v1/admin/settings`, `/v1/admin/settings/preview`, `/v1/admin/settings/apply`,
  `/v1/admin/storage`, `/v1/admin/queue`, `/v1/admin/queue/failures`,
  `/v1/admin/queue/retry-failed`, `/v1/admin/queue/backfill`,
  `/v1/admin/namespaces/preview`, `/v1/admin/namespaces/update`,
  `/v1/admin/namespaces/history`, `/v1/admin/config/backup`,
  `/v1/admin/config/backups`, `/v1/admin/config/restore` — with a matching
  frontend admin dashboard.
- **`contextd migrate-namespaces`** — one-shot cutover for the typed-namespace
  grammar. Dry-run by default; `--apply` commits. Flags: `--db`, `--apply`,
  `--json`, `--project-threshold`. Reports collisions and derives project tags
  from the corpus rather than a hardcoded list.
- **`contextd path`** — prints the resolved on-disk layout (go-apppaths roots,
  active workspace, main DB, `config.yaml`, `records/`, `queue.db`). Honors every
  override the daemon sees and never creates directories.
- **Environment overrides** `TESSERACT_DB_PATH`, `TESSERACT_WORKSPACE`,
  `TESSERACT_PLUGINS_DIR`, `TESSERACT_MEMORY_DECAY_INTERVAL`.
- Type-aware promotion and namespace prefix-matching in recall, backing the
  typed grammar above.

### Changed

- **Activation reinforcement moved from search to deliberate reads.** `memory_recall` (every ranking mode, including relevance) no longer bumps `activation`, `access_count`, or `last_accessed_at` — being returned by a search is the ranker's guess, not a signal of importance, and reinforcing on recall created a self-reinforcing echo chamber. Reinforcement now fires only on the deliberate-read paths: `memory_get` / `GET /v1/memory/current` and `memory_get_revision` / `GET /v1/memory/revisions/{id}`. Activation **decay** is unchanged. New store methods `Store.GetCurrentReinforced` and `Store.GetRevisionByIDReinforced` back the reinforcing reads; the plain `GetCurrent` / `GetRevisionByID` stay non-reinforcing for internal callers (promotion, embedding, knowledge lookups). Consumers that depended on "recall reinforces activation" (notably Nanite) should flag this — hot-memory activation trails will now reflect deliberate reads only.

- Frontend app shell adopted the `@hollis-labs/sysop-ui` kit (bumped to v0.6.2).
- Repository prepared for the OSS beta: README rewritten, documentation
  restructured under `docs/`, and stale internal task trees removed.

### Fixed

- **SQLite DSN pragmas were written in the wrong driver's dialect and never took
  effect.** Connection strings used the mattn/go-sqlite3 spelling
  (`_busy_timeout=`, `_fk=`) while the driver is `modernc.org/sqlite`, which
  configures pragmas via `_pragma=name(value)` and silently ignores parameters it
  does not recognize. Measured, the mattn form was equivalent to passing no
  parameters at all: `busy_timeout` was 0 and `foreign_keys` was OFF on every
  connection. So the declared `REFERENCES` clauses on `heads`, `embeddings` and
  `memory_revisions` were inert, and concurrent writers failed immediately with
  `SQLITE_BUSY` instead of waiting. Both pragmas are now applied on every
  connection, and DSN construction is centralized in `internal/sqlitedsn` so the
  dialect is stated once. Enabling foreign keys was checked against a copy of a
  live store first: zero `foreign_key_check` violations.
- `memory_write` and related MCP tools now accept a native JSON array for the
  `tags` argument, not only a JSON-encoded string.

## [0.7.0] — 2026-05-15

### Changed

- Finalized the public Tesseract naming pass. The Go module path is `github.com/hollis-labs/tesseract`, and the default local state root is `~/.tesseract`.
- Pinned `go.mod` dependency references to published versions so the module is consumable from GitHub: `go-otel v0.1.0`, `go-queue v0.1.0`, `go-embed-contracts v0.1.1`, `go-modelsdev v0.2.0`, and the renamed `go-mcp v0.1.0` (previously the dead `mcp-helpers` module path). These were placeholder `v0.0.0` requires resolvable only via local `replace` directives.

## [0.6.0] — 2026-05-09

Path B clean-break migration off `github.com/hollis-labs/go-providers`. All
LLM contract types relocate to dedicated repos; Tesseract ships its own
SDK-backed wrappers for the embedding and synthesis paths.

### Breaking changes

- **Dropped `github.com/hollis-labs/go-providers` dependency entirely.**
  All `provider.*` references migrated to the canonical contract repos:
  - `provider.Embedder` / `provider.EmbeddingResult` → `embedcontracts.{Embedder,EmbeddingResult}`
    (`github.com/hollis-labs/go-embed-contracts`)
  - `provider.ChatRequest` / `provider.ChatMessage` → `llmtypes.{ChatRequest,ChatMessage}`
    (`github.com/hollis-labs/go-llm-types`)
  - `provider.Provider` → `llmcontracts.Provider`
    (`github.com/hollis-labs/go-llm-contracts`)
- **`tesseract.WithEmbedder` parameter type** changed from `provider.Embedder`
  to `embedcontracts.Embedder`. Callers must update imports + types.
- **`Server.SynthesisProvider` type** changed from `provider.Provider` to
  `llmcontracts.Provider`. Callers wiring a custom synthesis provider must
  switch to the new contract.
- **Synthesis providers narrowed to `openai` + `anthropic`.** The legacy
  `gemini` and `mistral` synthesis branches in `cmd/contextd/main.go` were
  removed — they were configured but never used in production. Callers
  needing other vendors should inject a custom `llmcontracts.Provider`
  implementation by mutating `srv.SynthesisProvider` before serve.

### Added

- **`internal/llm/openai`** — openai-go (v1.12.0) backed wrapper that
  implements both `embedcontracts.Embedder` and `llmcontracts.Provider`.
  Powers `createEmbedder` (used by `setupMemorySubsystem` + `backfill`) and
  the `synthesis.provider=openai` branch.
- **`internal/llm/anthropic`** — anthropic-sdk-go (v1.41.0) backed wrapper
  that implements `llmcontracts.Provider`. Powers the
  `synthesis.provider=anthropic` branch.

### Notes

- `Complete` is fully implemented in both wrappers (the synthesis route
  is non-streaming). `StreamChat` returns a "not implemented" error —
  Tesseract's only chat consumer (`/v1/synthesis/ask`) is non-streaming.
  If a future caller needs streaming, fill in `StreamChat` per provider or
  import an SDK-backed wrapper from a sibling module.
- `Capabilities()` returns conservative fixed defaults; per-model tuning
  is left to callers that need it.

### Background

go-providers v0.10.0 narrowed scope to CLI/PTY/subprocess-only; the
embedding contract relocated to `go-embed-contracts`, the LLM contracts to
`go-llm-contracts`, and the data types to `go-llm-types`. Per Path B
(clean break, no transitional aliases), this release migrates all
references in one pass.

Sprint: SP-20260508-0001
Tesseract decision: `decisions.nanite.architecture.go_llm_contracts_split`

## [0.5.3] — 2026-04-26

LLM-backed answer synthesis for the Search & Research surface, plus
operator forms for memory write / promote / deprecate, audit timeline
filters (actor + since/until), and an API naming-convention pass that
consolidates `invalid_request` → `validation_error`.

### Added

- **HTTP route: `POST /v1/synthesis/ask`** — fans curated memory + knowledge
  recall into an LLM completion via go-providers, returns the synthesised
  answer + numbered cited sources + per-call telemetry (provider, model,
  latency, tokens when available, cost via go-modelsdev catalog lookup).
  Returns 503 `synthesis_unavailable` when no provider is configured.
- **Config: `synthesis.*`** in `~/.tesseract/config.yaml` —
  `provider`, `model`, `system_prompt`, `max_tokens`, `temperature`.
  Provider names: `openai`, `anthropic`, `gemini`, `mistral`. API key
  is read from the conventional env var (e.g. `ANTHROPIC_API_KEY`).
- **Dependency:** `go-modelsdev` (replace → `../go-modelsdev`) — used for
  model pricing lookups in the synthesis cost report.
- **Memory write / promote / deprecate UI** (`MemoryWritePage`) — a single
  operator surface with three tabs over the existing `/v1/memory/*` routes.
  Symmetric to the context-domain WriteRecord + Promote pages.
- **Search & Research: Synthesis tab** — third tab alongside Answer / Sources.
  Click "Synthesize" to call the new LLM endpoint; result cached on the
  thread entry so revisiting the question doesn't re-spend tokens. Telemetry
  footer shows provider/model/latency/tokens/cost.

### Changed

- **Audit filter: `actor` + `since` + `until`** added to `/v1/context/audit`.
  Backed by SQL substring (`actor LIKE %?%`) and lexical comparison on the
  TEXT `created_at` column. Frontend AuditPage now uses server-side actor
  filter (was client-side workaround) and gains datetime-local pickers for
  `since` / `until`.
- **Error code consolidation:** all `invalid_request` HTTP error codes
  (33 callsites) renamed to `validation_error` to match the dominant
  pattern (105 callsites). No tests depended on the old literal.

## [0.5.2] — 2026-04-26

Frontend operator surfaces: Memory & Knowledge browser, Search & Research
(v1 curated), Memory / Knowledge detail page, Recall improvements
(facet drill-down, URL state, clickable tags, recent namespaces),
Audit Timeline overhaul (memory/knowledge event types, click-through to
detail page, cursor-paginated load-more, day grouping, client-side actor
filter). Plus the foundation: `GET /v1/namespaces/list` backend endpoint,
knowledge `key` → `memory_key` parameter normalisation, and a tsc baseline
fix that re-enables `make frontend` / `npm run build`.

### Changed

- **Breaking (local-only):** Knowledge HTTP + MCP read endpoints renamed
  their key parameter from `key` to `memory_key`, matching the existing
  memory endpoints so both stores expose a single normalized identifier.
  Affected:
  - `GET /v1/knowledge/current` — query param `key` → `memory_key`
  - `GET /v1/knowledge/history` — query param `key` → `memory_key`
  - MCP tool `knowledge_get` — required arg `key` → `memory_key`
  - MCP tool `knowledge_history` — required arg `key` → `memory_key`
  Error messages updated. No other callers in the portfolio depend on
  the old name; safe to break pre-public-release.
- **Audit & Ops page** — restructured into a timeline. Event-type filter
  now exposes the v0.5.1 memory / knowledge events grouped by domain
  (memory, knowledge, context, promote-MCP, promote-HTTP, maintenance).
  Rows for memory/knowledge events click through to the appropriate
  detail page; context events go to the existing record detail. Cursor
  pagination ("Load older events") + day grouping + client-side actor
  filter.
- **Recall page** — facet domain chips are now clickable to filter the
  result list. Form state mirrored into `#recall?…` URL hash so views
  are bookmarkable / shareable.
- **Search & Research page** — thread, named presets, and recent
  namespaces persisted to localStorage. Tag chips on result cards in
  the Answer tab are clickable (add to filter).
- **Memory & Knowledge browser** — new "Load counts" bulk action
  (concurrent recall for every visible namespace) and inline
  Register-namespace form for fast bootstrap.
- **`npm run build` works again** — fixed pre-existing
  `exactOptionalPropertyTypes` strict-mode errors across PromotePage,
  RecordDetailPage, ViewBuilderPage, WriteRecordPage, BrokerPage,
  AuthTokensPage, PolicyManagerPage, PacketBuilderPage,
  CompareRevisionsPage, KeyHistoryPage, NamespaceDetailPage. The
  canonical `make frontend` target builds cleanly again.

### Added

- **HTTP route: `GET /v1/namespaces/list`** — list all registered
  namespaces with optional `?prefix=` (string-prefix match) and
  `?limit=` (default 200, max 1000). Returns `{items, count, truncated}`.
  Backs the new Memory & Knowledge Browser frontend tree view; mirrors
  the existing MCP `context_namespaces_list` tool.
- **Frontend page: Memory & Knowledge Browser** — tree view of registered
  namespaces grouped by tier prefix (`user/`, `app/`); domain tabs filter
  memory vs knowledge; lazy-load keys per namespace via `/v1/recall`;
  drill-through to the new memory/knowledge detail page.
- **Frontend page: Search & Research (v1 curated)** — question textarea
  over `POST /v1/tesseract/lookup` with status / domain / tag / confidence
  filters. Tabbed result view: **Answer** (cards grouped by domain) and
  **Sources** (raw revisions for citation). Session-local thread of past
  Q&As. v2 LLM-backed synthesis is comment-tracked in source — will use
  the portfolio go-modelsdev library for cost / token / latency
  telemetry.
- **Frontend page: Memory / Knowledge Revision Detail** — single
  component handles both `domain="memory"` and `"knowledge"` via prop;
  identity bar (status, confidence, author, tags, timestamps); four
  tabs (Summary / Payload / History / Raw). Wired as click-through
  destination for Recall, Memory & Knowledge Browser, Search & Research,
  and the Audit Timeline.
- **Frontend page: Recall (improved)** — operator surface over
  `GET /v1/recall` (shipped in 0.5.1). Tag chips clickable (add to
  filter); recent-namespaces dropdown via localStorage; click-through
  routed by `item.domain` to the memory/knowledge detail page (fixed
  the "head not found" regression that hit when the click previously
  went to the context-domain record-detail handler).

## [0.5.1] — 2026-04-26

Audit-pipeline catch-up + recall ergonomics. Memory/knowledge writes now appear in `context_audit`, the audit-emit code path is consolidated through canonical `Store.Emit*` helpers, the daemon degrades gracefully when no embedding key is present, and a new `GET /v1/recall` endpoint gives scripted / agent consumers a query-param surface that mirrors `POST /v1/tesseract/lookup`. Includes the audit fixes from `CW-20260419-0040` (PR #11) plus the audit-helper consolidation from `CW-20260419-0060`.

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
  for scripted / agent consumption. Mirrors `POST /v1/tesseract/lookup` but
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

MCP Surface v2 (PR #9 `feat/mcp-surface-v2`). Rewrites memory/knowledge/unified/meta MCP tool descriptions against a consistent v2 template, adds MCP protocol annotations per spec §5.4, and ships `tesseract_skills` as a progressive-discovery meta-tool backed by 11 embedded markdown skills. Additive — no Go library API changes; existing consumers are unaffected.

### Added

- **MCP tool: `tesseract_skills`** — progressive-discovery meta-tool. No-args returns an 11-entry index; `name=<skill>` returns the full markdown body. Skills embedded via `//go:embed` at `internal/mcpadapter/skills/*.md`: `start-here`, `namespaces`, `facets-and-kinds`, `revisions`, `recall-and-ranking`, `promotion`, `views`, `memory`, `knowledge`, `context-packet`, `audit`. `start-here` is indexed first; rest alphabetical.
- **MCP protocol annotations** on all 12 memory / knowledge / unified / meta tools — `ReadOnlyHint`, `IdempotentHint`, `DestructiveHint`, `OpenWorldHint` per spec §5.4. `memory_deprecate` correctly carries `IdempotentHint=true` (second call is a no-op).
- **Drift guardrail:** `internal/mcpadapter/annotations_test.go` fails CI if any of the 12 v2-template tools drops its required annotations.

### Changed

- **MCPServer version string** bumped `0.4.0 → 0.5.0`.
- **12 MCP tool descriptions** rewritten to v2 template — bold action phrase + `Kind of content` / `Scope` / `Use this when` / `Don't use this for` / `Deeper:` bullets plus concrete parameter-value examples. Covers all memory, knowledge, unified-lookup, and meta surfaces.
- **30 context-domain tool descriptions** get an appended `See tesseract_skills start-here for the primitive model.` footer. Minimum-touch; full context-domain rewrite is deferred.
- **Parity catalog** — `tesseract_skills` registered as MCP-only meta-tool in `surfaceCatalog`.
- **`docs/MCP_TOOLS.md`** refreshed — new Skills section, `Deeper` column on memory/knowledge/unified tables, new Meta section for `tesseract_skills`.

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
- **MCP tool descriptions** updated on `memory_recall` and `tesseract_lookup`: `activation, chronological, similarity, or relevance (default: relevance when query is set, else activation)`.
- **Access reinforcement widens to all recall modes** (was activation-only). Dense-only or chronological queries now bump `last_accessed_at` and `access_count` too so hot memories keep their activation trail when agents switch ranking modes.

### Fixed

- **BLG-037** — `memory_recall` and `tesseract_lookup` were shipping the full `EmbeddingVector` (~39KB/record for text-embedding-3-large) in every `Revision` JSON response, blowing agent context budgets on recalls of 5+. `json:"-"` on `Revision.EmbeddingVector` drops the field from every JSON response universally. Struct field is retained — similarity ranking still reads it directly via `similarityScore`/`CosineSimilarity`; SQL storage is untouched. Smoke-test on a live store: `memory_recall limit=10` went from ~390KB to 26KB; `tesseract_lookup limit=20` went from ~660KB to 40KB.

### Operator notes

- First boot after upgrade runs migration case 12, which creates the FTS5 index and backfills every existing `memory_revisions` row. On a fresh / small corpus this is instant; on larger stores, expect a one-shot startup cost proportional to revision count × payload length.
- `modernc.org/sqlite` ships FTS5 by default — no build tags or external sqlite binary needed.
- Out of scope for this release (tracked separately): external vector DB evaluation (`BLG-20260414-016`), markdown/code-aware chunking (moved to `SPR-20260416-ingest-s1` under `EPIC-20260415-64937`).

## [0.3.0] — 2026-04-15

MCP↔HTTP parity batch 1: agent-access fixes plus a durable drift guardrail. Closes the gaps surfaced after the knowledge-domain S1 merge so an agent booted against `mcp__tesseract__*` has functional parity with the HTTP `/v1/*` surface for context, memory, and knowledge reads.

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

- `README.md` links to `docs/MCP_TOOLS.md`.

### Operator notes

The MCP client config should point at the `contextd mcp` binary and the same Tesseract data root used by the HTTP server.

## [0.2.0] — 2026-04-15

Knowledge Domain S1 (`SPR-20260414-knowledge-domain-s1`, `EPIC-20260414-84967`). Adds a second info domain (`knowledge`) alongside `memory`, wires the memory subsystem into HTTP serve mode, and introduces `tesseract_lookup` for cross-domain search. Merged as PR #3.

### Added

- **Domain discriminator** — `domains.Domain` type + in-tree `DomainPolicy` interface (`MemoryDomain`, `KnowledgeDomain`).
- **Knowledge facets** — `kind`, `source`, `pointer{scheme, locator, resolved_at}` on `memory.Revision`. Required on knowledge writes.
- **MCP tool: `knowledge_write`** + HTTP `POST /v1/knowledge/write` — pointer-first writes with required facets.
- **MCP tool: `tesseract_lookup`** + HTTP `POST /v1/tesseract/lookup` — unified search across memory + knowledge with facet histogram.
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

- **`go-queue` integration** — SQLite-backed `go-queue` instance for async embed jobs. `QueueAdapter` bridges `memory.JobQueue` → `queue.Queue`. `WithQueue()` functional option on `tesseract.Open()`. Worker lifecycle with retry (3 max tries / 30s delay). Separate `queue.db` for write safety.
- **Auto-embed on write** — `memory.Store.EmbedRevision()` loads revision, extracts text, calls embedder, writes vector inline to `embedding_model`/`embedding_vector` columns. Queue embed handler wired to call it on every write.
- **Similarity recall** — `Recall(RankingSimilarity)` embeds the query and ranks candidates by cosine similarity. Exposed on both MCP `memory_recall` and library API. Unembedded revisions filtered out.
- **Tesseract facade** — `WriteMemory`, `RecallMemory`, `GetCurrentRevision`, `GetRevisionHistory`, `EmbedRevision` on `*Tesseract`. Library consumers no longer need to reach through `.Store()` / `.MemoryStore()`.
- **Backfill CLI** — `contextd backfill-embeddings [--namespace=...]` iterates unembedded revisions and embeds them.
- **Semantic dedup** — opt-in via `Dedup: "semantic"` on `WriteInput`. Same-key matches auto-supersede; cross-key matches set `DedupMatch` without superseding. MCP `memory_write` exposes `dedup` + `dedup_threshold` params.
- **Config file** — `~/.tesseract/config.yaml` for embedding provider/model and dedup threshold. Loaded by `internal/config.Load()`. Defaults: OpenAI `text-embedding-3-large`, threshold 0.85. Falls back to env vars for auth.
- **SQLite WAL mode** enabled for better concurrent read/write performance.

### Changed

- **Monotonic ULIDs** (`ulid.Monotonic`) + **RFC3339Nano timestamps** eliminate the nondeterministic revision ordering bug. `parseMemoryTime()` falls back to `time.DateTime` for backward compat.
- All stale `tesseract` references replaced with `tesseract` (plugin CLI usage strings, env vars).

### Fixed

- Stale `mcp-helpers` and `otel` replace directives (`fragments-engine/libs/` → `framework/libs/`).
- Empty-text guard in `EmbedRevision` (would previously call the embedder with empty input).

## [0.0.1] — 2026-04-08

Initial standalone-repo baseline tag at commit `3b92f5c`. Captures the post-rename state of the codebase extracted from `fragments-engine/tesseract/` to its own repo at `github.com/hollis-labs/tesseract`. No formal release notes — this tag exists primarily to anchor `git describe` output.

[Unreleased]: https://github.com/hollis-labs/tesseract/compare/v0.8.0...HEAD
[0.8.0]: https://github.com/hollis-labs/tesseract/compare/v0.7.0...v0.8.0
[0.7.0]: https://github.com/hollis-labs/tesseract/compare/v0.6.1...v0.7.0
[0.6.0]: https://github.com/hollis-labs/tesseract/compare/v0.5.3...v0.6.0
[0.5.3]: https://github.com/hollis-labs/tesseract/compare/v0.5.2...v0.5.3
[0.5.2]: https://github.com/hollis-labs/tesseract/compare/v0.5.1...v0.5.2
[0.5.1]: https://github.com/hollis-labs/tesseract/compare/v0.5.0...v0.5.1
[0.5.0]: https://github.com/hollis-labs/tesseract/compare/v0.4.0...v0.5.0
[0.4.0]: https://github.com/hollis-labs/tesseract/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/hollis-labs/tesseract/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/hollis-labs/tesseract/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/hollis-labs/tesseract/compare/v0.0.1...v0.1.0
[0.0.1]: https://github.com/hollis-labs/tesseract/releases/tag/v0.0.1
