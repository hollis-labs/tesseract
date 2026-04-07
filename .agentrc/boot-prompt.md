# Cortex — Session Boot Prompt

Updated: 2026-04-05

## What just happened

D-core memory subsystem is **complete and merged** (PR #1, 17 commits, 44
test functions passing). This was a multi-session effort: brainstorm → spec
→ plan → subagent-driven TDD execution across 15 tasks.

## What's in the tree

`internal/memory/` — a sibling subsystem to the context registry:
- Append-only revision storage with caller-declared dot-notation keys
- User-rooted namespace hierarchy (`user/{id}/memory`, `.../project/{pid}/memory`, `.../session/{sid}/memory`)
- Write path with keyed/keyless/supersedes, strict validation
- Read path (GetCurrent, GetHistory, GetRevisionByID)
- Multi-namespace Recall with activation/chronological ranking + filters
- Promote (session → user/project) and Deprecate with idempotent lifecycle
- Activation reinforcement on retrieval, decay job (14-day half-life)
- Stub `Embedder` (returns `ErrEmbedderUnavailable`) and `NoopQueue` (drops jobs)
- 6 MCP tools: `memory_write`, `memory_get`, `memory_history`, `memory_recall`, `memory_promote`, `memory_deprecate`
- Schema v9 in `contextstore/store.go` — `memory_state` + `memory_revisions` tables + 7 indexes
- Wired in `cmd/contextd/main.go` with decay goroutine

## What's next — release roadmap

See `docs/RELEASE-ROADMAP.md` for the full picture. Summary:

**Unblocked now:**
- **E. Embedded-runtime API surface** — library API for Nanite to link in-process. Can design against the stable memory domain.
- **A. Rename + identity** — naming pass for the Cortex rebrand (deferred, low urgency)
- **B. Repository extraction** — move Cortex out of fragments-engine to standalone repo
- **C. Cross-repo TODO audit** — sweep Engine/Nanite/Cortex for related items

**Blocked on H + I:**
- **D-deferred** — auto-embed, backfill, semantic similarity, semantic dedup. Stub interfaces in place; swap is mechanical once real providers/queue exist.
- **H. Shared AI provider Go module** — extract from Nanite's working multi-provider code. Anthropic + OpenAI-compatible + Ollama + CLI adapters.
- **I. Shared queue Go module** — new, Laravel-inspired. In-process workers at 1.0. First consumer: memory auto-embed.

**Blocked on E:**
- **F. Dual-mode packaging** — CLI + desktop GUI (Wails app)
- **G. Release readiness** — versioning, docs, install story

## Key technical knowledge

- **`"trigger"` is a SQLite reserved word** — quoted in DDL with inline comment warning. All DML against `memory_revisions` must quote it.
- **Module path:** `github.com/hollis-labs/cortex`
- **Pre-commit hook:** gofmt + go vet + golangci-lint (misspell enforces US English)
- **Testing conventions:** stdlib `testing`, external test package (`_test`), `errors.Is` on all sentinel checks, `var _ Interface = Impl{}` compile-time assertions on stubs
- **Design spec:** `docs/superpowers/specs/2026-04-04-memory-domain-design.md` — 15 decisions (D1-D15)
- **Implementation plan:** `docs/superpowers/plans/2026-04-04-memory-domain-d-core.md` — 15 tasks, all complete
