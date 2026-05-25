# Tesseract Release Roadmap

Status: historical planning snapshot
Purpose: summarize the major workstreams that shaped the Tesseract 1.0 path.

This document is no longer the active execution board. Use current release docs,
the beta checklist, and the repository surface itself for present-day status.

## Product framing

Tesseract is a local-first **context + memory** service for humans and agents. It
ships in two deployment profiles:

1. **MCP server mode** — standalone daemon any agent (Claude Code, other MCP
   clients) connects to for context + memory operations.
2. **Embedded runtime mode** — in-process library for apps that want memory as
   a native feature.

It ships in two distribution forms:

1. **CLI release** — headless binary, for power users and embedding hosts.
2. **Desktop/GUI app** — user-facing shell for humans managing their own context
   and memory.

## Workstreams

### A. Rename + identity
Unify the product under the Tesseract name.

### B. Repository extraction
Move Tesseract into a standalone repository with its own module, CI, and release process.

### C. Cross-repo TODO audit
Audit related repos and docs for work that blocks or depends on Tesseract.

### D. Memory domain *(next up)*
The actual product gap. Tesseract today is a deterministic context registry with
15 content types — none are memory. `context_search` and `context_rag_query`
return `embedding_unavailable`. Chat continuity is impossible without this.

In scope:
- Memory content type(s) and lifecycle
- Memory-oriented view(s) and retrieval semantics
- Namespace hierarchy for user/project/session-scoped memories
- Embedding provider decision (blocker)
- Extraction triggers and dedup/conflict resolution
- Retrieval access pattern (contextbroker integration)

Blocks: E.

### E. Embedded-runtime API surface
Library API for in-process embedding. Distinct from the MCP tool surface.

Depends on: D (can't stabilize an API around a moving domain).

### F. Dual-mode packaging
CLI release binary + desktop GUI shell. The `frontend/` directory exists but
the GUI/operator surface is now real: dashboard, review queue, memory/context/
knowledge write flows, search/recall tools, and operator/policy screens all
exist. The remaining work is packaging, release hardening, and deciding what
ships as the supported 1.0 surface. Build + release pipeline for both.

Depends on: D, E (packaging stable surfaces, not moving ones).

### G. Release readiness
Versioning policy, install story, public docs, and migration notes.

Depends on: D, E, F.

### H. Shared AI provider Go module
Extract multi-provider AI code into a standalone Go module shared across Hollis Labs apps.

Blocks: D's embedding subsystem.

### I. Shared queue Go module
Adopt a shared background-job module for embedding backfill and other async work.

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

## Snapshot status

- [x] A. Rename + identity
- [x] B. Repository extraction
- [ ] C. Cross-repo TODO audit — not started
- [x] D. Memory domain
- [~] E. Embedded-runtime API surface — partially complete
- [~] F. Dual-mode packaging — partially complete
- [~] G. Release readiness — in progress
- [x] H. Shared AI provider module
- [x] I. Shared queue module
