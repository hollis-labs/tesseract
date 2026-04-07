# Cortex Release Roadmap

Status: tracking doc — captured 2026-04-04
Purpose: decompose the "Cortex 1.0 release" effort into independent workstreams
with clear ordering and hand-off boundaries. Each track gets its own design spec
and implementation plan — this file only tracks what the tracks *are* and why.

## Product framing

Cortex is a local-first **context + memory** service for humans and agents. It
ships in two deployment profiles:

1. **MCP server mode** — standalone daemon any agent (Claude Code, other MCP
   clients) connects to for context + memory operations.
2. **Embedded runtime mode** — in-process library for apps that want memory as
   a native feature (primary target: Nanite).

It ships in two distribution forms:

1. **CLI release** — headless binary, for power users and embedding hosts.
2. **Desktop/GUI app** — user-facing shell for humans managing their own context
   and memory.

## Workstreams

### A. Rename + identity
Current name "Cortex" collides with other AI products. Needs a new name before
1.0 ships publicly. Existing `ideation-naming-001.md` targeted an orchestration
system (wrong semantic); memory/context needs its own naming pass. Low urgency
per user — run as a sidebar once the memory domain has settled, so the name can
reflect what the thing actually does.

### B. Repository extraction
Move Cortex out of `fragments-engine/cortex/` into a standalone repo. Own
`go.mod`, CI, release pipeline, issue tracker. Preserve git history where
reasonable. Update Nanite's contextbroker integration to point at the new repo.

### C. Cross-repo TODO audit
Sweep Fragments Engine (MCP Engine), Nanite, and Cortex's own docs/SPECS for
open items that belong to Cortex, block Cortex, or are obsoleted by the release
plan. Produce one consolidated list before locking scope on later tracks.

### D. Memory domain *(next up)*
The actual product gap. Cortex today is a deterministic context registry with
15 content types — none are memory. `context_search` and `context_rag_query`
return `embedding_unavailable`. Chat continuity is impossible without this.

In scope:
- Memory content type(s) and lifecycle
- Memory-oriented view(s) and retrieval semantics
- Namespace hierarchy for user/project/session-scoped memories
- Embedding provider decision (blocker)
- Extraction triggers and dedup/conflict resolution
- Retrieval access pattern (contextbroker integration)

Blocks: E, and Nanite's `MemoryService` work (see
`nanite/docs/phase-c-cortex-investigation.md`).

### E. Embedded-runtime API surface
Library API that Nanite (and future embedding hosts) link in-process. Distinct
from the MCP tool surface. Must cover the memory domain from (D) plus the
existing context operations. Decisions needed on: public package boundary,
lifecycle/ownership of the store, concurrency model, error surface.

Depends on: D (can't stabilize an API around a moving domain).

### F. Dual-mode packaging
CLI release binary + desktop GUI shell. The `frontend/` directory exists but
`docs/PRODUCT_FEATURE_BRIEF.md` notes the GUI is not yet a full product surface.
Decide: what ships at 1.0, what's staged. Build + release pipeline for both.

Depends on: D, E (packaging stable surfaces, not moving ones).

### G. Release readiness
Versioning policy, license audit, install story, user docs site, migration
notes for existing Cortex integrations (Nanite contextbroker, Hadron, Volon,
Mentat, Sigil — all have `app/<name>` namespaces registered today).

Depends on: D, E, F.

### H. Shared AI provider Go module *(new — prerequisite for D's embedding subsystem)*
Extract Nanite's multi-provider AI code into a standalone Go module shared by
Nanite, Cortex, Hadron, Engine, and future apps. Covers both LLM calls and
embedding calls. Handles capability differences (not every provider embeds).
At launch: Anthropic, OpenAI-compatible, Ollama, and CLI agent adapters
(Claude/Codex/Copilot/Gemini) where applicable.

**Why shared:** Every app touching AI has the same provider problem. Solving
it once in a versioned module eliminates drift and triples the value of each
bug fix. Nanite already has working code — this is mostly an extraction, not
greenfield design.

Blocks: D's embedding subsystem.

### I. Shared queue Go module *(new — prerequisite for D's auto-embed)*
New standalone Go module implementing a Laravel-inspired job queue: jobs with
serializable payloads, workers, retry/backoff, failed-jobs table, dispatchable
interface, pluggable drivers. At 1.0: in-process workers with SQLite-backed
persistence. Post-1.0: additional drivers (DB, Redis, etc.).

**Why shared:** Every app ends up needing a job queue. Cortex's memory
auto-embed and backfill are the first consumers; every future "do this
reliably in the background" need across the portfolio benefits.

**Why Laravel-style:** Well-understood pattern, proven in production, the user
is already fluent in its mental model. No invention needed.

Blocks: D's auto-embed and backfill paths.

## Dependency order

```
   A ──┐
       │
   B ──┼─── independent, run in parallel, cheap
       │
   C ──┘
           │
           ▼
   H ──┬── shared modules, prerequisites for D's embedding subsystem
   I ──┘
           │
           ▼
       D ──── load-bearing; blocks everything downstream
           │
           ▼
       E ──── depends on stable domain
           │
           ▼
       F ──── packaging stable surfaces
           │
           ▼
       G ──── release engineering
```

**Note on D's internal ordering.** D can start without H or I — its core work
(record types, namespace hierarchy, identity, append-only storage, activation
ranking, status/supersedes lifecycle) has zero AI provider dependencies. Only
the embedding-dependent subsystems (auto-embed on write, backfill, semantic
dedup) block on H and I. If H/I run in parallel with D's core, the timeline
collapses cleanly.

## Current status

- [ ] A. Rename + identity — deferred (sidebar)
- [ ] B. Repository extraction — not started
- [ ] C. Cross-repo TODO audit — not started
- [x] **D. Memory domain — D-core complete** (merged via PR #1, 2026-04-05)
  - D-core: 15 tasks, all merged. Types, keys, namespaces, stubs, schema v9,
    write/read/recall/promote/deprecate, activation, decay, 6 MCP tools,
    main.go wiring, integration test. 44 test functions, all passing.
  - D-embedding: provider integration complete (shared provider.Embedder,
    on-demand embed/search via library API). Auto-embed deferred to Track I.
  - D-deferred: backfill, semantic similarity, semantic dedup —
    blocked on I. Stub `NoopQueue` in place; swap is mechanical.
- [ ] E. Embedded-runtime API surface — **partially complete**; `cortex.Open()`
  with functional options provides the library init pattern. Full API surface TBD.
- [ ] F. Dual-mode packaging — blocked on E
- [ ] G. Release readiness — blocked on E, F
- [ ] H. Shared AI provider module — **in progress**; `pkg/provider` extracted in
  Nanite, Cortex consuming via replace directive. Standalone repo extraction pending.
- [ ] I. Shared queue module — not started; blocks D-deferred auto-embed
