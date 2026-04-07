---
type: architect-plan
author: architect-session
created_at: 2026-02-27
status: approved-for-execution
objective: Re-focus backlog on CLI/API/MCP MVP; defer GUI to post-MVP
authority: SPEC-TASKS/Context Memory Service Evaluation/ (wins on all conflicts)
---

# Context Memory Service — MVP Re-Plan

## Summary

Evaluation of `SPEC-TASKS/Context Memory Service Evaluation/` against the current codebase
and active task backlog. The evaluation spec is authoritative; all conflicts resolved in its favor.

The app is not live. No breaking-change constraints apply.

**Decision**: GUI is post-MVP. MVP is defined as: CLI + API + MCP adapter usable by agents.

---

## Evaluation Findings vs Codebase (confirmed)

### P0 — Must fix

| Finding | Confirmed in code |
|---------|-------------------|
| Cache vs memory not first-class distinction | Confirmed — no tier semantics, no policy enforcement by tier |
| Promotion lacks gate strength | Confirmed — `POST /v1/context/promote` is direct write, no request/approve/apply |
| Auth tokens not scoped | Confirmed — `authorizeMutatingRequest` validates existence/expiry/revocation only; no `scopes`, no `namespace_globs` |

### P1 — Alignment gaps

| Finding | Confirmed in code |
|---------|-------------------|
| `tags_any` in spec, not in implementation | Confirmed — `VIEWS.md` specifies `tags_any`; `contextstore.Select` has no tag filtering |
| Select is not "context assembly" | Confirmed — no time windows, no budget, no manifest, no include_pins |
| Checksum computed on write | **BUG CONFIRMED**: `store.go:389` inserts `checksum` as `""` despite `sha256` import |

### P2 — Quality gaps

| Finding | Status |
|---------|--------|
| Namespace filtering is post-query | Confirmed — in-memory matchNamespace after row scan |
| No conflict/drift mechanics for memory | Confirmed — no supersedes, no tombstone, no conflict state |

### MCP Adapter

| Finding | Status |
|---------|--------|
| MCP spec exists (docs/SPECS/MCP.md) | Confirmed — `Status: planned` |
| MCP adapter implemented in Go | **NOT IMPLEMENTED** — no Go code for MCP layer exists |

---

## Current API Routes (baseline)

```
POST /v1/namespaces/register   — mutating, auth-gated
GET  /v1/namespaces/get
POST /v1/context/write         — mutating, auth-gated
POST /v1/context/promote       — mutating, auth-gated (direct, no gate)
GET  /v1/context/head
GET  /v1/context/history
GET  /v1/context/audit
POST /v1/views/evaluate
GET  /v1/context/consistency/scan
POST /v1/context/consistency/repair  — mutating, auth-gated
GET  /v1/health/readiness
GET  /v1/metrics
```

Missing for MVP:
- `POST /v1/context/packet`
- `POST /v1/context/promote/request`
- `POST /v1/context/promote/approve`
- `POST /v1/context/promote/apply`
- `POST /v1/context/maintenance`
- `POST /v1/broker/plan`

---

## Task Disposition

### CANCEL — Low-value doc notes, not MVP

| Task | Title | Action |
|------|-------|--------|
| TASK-20260225-236 | Add VIEWS checklist continuous-improvement note | cancelled |
| TASK-20260225-237 | Add MCP rollback evidence trend note | cancelled |
| TASK-20260225-238 | Add outputs stale annotation checkpoint note | cancelled |

### DEFER — Post-MVP GUI sprint

All GUI tasks from `sprint-2026-03-gui-template`. The GUI is a client that consumes a
stable API. It should be built after the API/CLI/MCP surface is finalized.

| Task | Title | Action |
|------|-------|--------|
| TASK-20260225-239 | Build GUI foundation and app shell | deferred → sprint-post-mvp-gui |
| TASK-20260225-240 | Context Explorer flow | deferred → sprint-post-mvp-gui |
| TASK-20260225-241 | View Builder and selector evaluation | deferred → sprint-post-mvp-gui |
| TASK-20260225-242 | Write/Promote workflow | deferred → sprint-post-mvp-gui |
| TASK-20260227-001 | Policy Manager flow | deferred → sprint-post-mvp-gui |
| TASK-20260227-002 | Audit/Ops flow | deferred → sprint-post-mvp-gui |
| TASK-20260227-003 | Environment/Auth and token management | deferred → sprint-post-mvp-gui |
| TASK-20260227-004 | GUI hardening pass | deferred → sprint-post-mvp-gui |

### KEEP — MVP backbone (eval phase tasks, well-formed)

| Task | Sprint | Phase |
|------|--------|-------|
| TASK-20260227-005 | sprint-2026-04-eval-phase0 | Phase 0: tags_any spec mismatch |
| TASK-20260227-006 | sprint-2026-04-eval-phase0 | Phase 0: checksum on write |
| TASK-20260227-007 | sprint-2026-05-eval-phase1 | Phase 1: tiered namespace conventions |
| TASK-20260227-008 | sprint-2026-05-eval-phase1 | Phase 1: policy schema extension |
| TASK-20260227-009 | sprint-2026-05-eval-phase1 | Phase 1: maintenance jobs |
| TASK-20260227-010 | sprint-2026-06-eval-phase2 | Phase 2: /v1/context/packet endpoint |
| TASK-20260227-011 | sprint-2026-06-eval-phase2 | Phase 2: token budget estimation |
| TASK-20260227-012 | sprint-2026-06-eval-phase2 | Phase 2: tag indexing (conditional on 005) |
| TASK-20260227-013 | sprint-2026-07-eval-phase3 | Phase 3: promotion record types |
| TASK-20260227-014 | sprint-2026-07-eval-phase3 | Phase 3: promotion lifecycle |
| TASK-20260227-015 | sprint-2026-08-eval-phase4 | Phase 4: capability token schema |
| TASK-20260227-016 | sprint-2026-08-eval-phase4 | Phase 4: token enforcement in handlers |
| TASK-20260227-017 | sprint-2026-09-eval-phase5 | Phase 5: broker plan endpoint |
| TASK-20260227-018 | sprint-2026-09-eval-phase5 | Phase 5: broker plan validation |

### NEW — MCP Adapter (sprint-2026-10-mvp-mcp)

MCP is specced but has no Go implementation. Required for "agent usable" MVP.

| Task ID | Title | Priority | Depends on |
|---------|-------|----------|------------|
| TASK-20260227-019 | MCP adapter phase-1: read/view tools | A | Phase 0 complete |
| TASK-20260227-020 | MCP adapter phase-2+3: write/packet/promote tools | A | Phases 2 and 4 complete |

---

## MVP Sprint Map

```
sprint-2026-04-eval-phase0  (Phase 0 — Spec alignment)
  005  Close tags_any spec/impl mismatch                      [A]
  006  Implement records.checksum on write + scan validation   [A]

sprint-2026-05-eval-phase1  (Phase 1 — Tiers)
  007  Define canonical tiered namespace conventions + defaults [A]
  008  Extend namespace policy schema with tier enforcement     [A]
  009  Add store-level maintenance jobs (trim/compact/tombstone)[B]

sprint-2026-06-eval-phase2  (Phase 2 — Context Packets)
  010  POST /v1/context/packet endpoint                        [A]
  011  Deterministic byte/token budget estimation              [B]
  012  Tag indexing in SQLite (conditional on 005 decision)    [B]

sprint-2026-07-eval-phase3  (Phase 3 — Gated Promotion)
  013  Define promote.request + promote.approve record types   [A]
  014  Implement request/approve/apply lifecycle               [A]

sprint-2026-08-eval-phase4  (Phase 4 — Capability Tokens)
  015  Extend auth_tokens with client_id, scopes, namespace_globs [A]
  016  Enforce capability token claims in all mutating handlers [A]

sprint-2026-09-eval-phase5  (Phase 5 — Broker Agent)
  017  POST /v1/broker/plan endpoint                           [B]
  018  Strict broker plan validation                           [B]

sprint-2026-10-mvp-mcp      (MCP Adapter — NEW)
  019  MCP adapter phase-1: read/view tools                    [A]
  020  MCP adapter phase-2+3: write/packet/promote tools       [A]

sprint-post-mvp-gui         (GUI — DEFERRED)
  239, 240, 241, 242, 001, 002, 003, 004   (all GUI tasks)
```

---

## Implementation Notes for Each Phase

### Phase 0 execution notes
- Task 005 (tags_any): **strongly recommend Option A** (remove from spec, not implement).
  Implementation adds SQLite schema complexity and is not needed for agent context assembly.
  Defer tag indexing to Phase 2 task 012 as an optional follow-on.
- Task 006 (checksum): One-line fix at `store.go:389`. Change `""` to `hex.EncodeToString(sha256sum[:])`.
  Also update consistency scan to validate non-empty checksums.

### Phase 1 execution notes
- Policy schema extension (task 008) must match the tier spec from task 007.
  Write task 007 spec first, validate it, then implement 008 against it.
- Maintenance jobs (task 009) are manually triggered for MVP.
  No scheduler needed. Expose via API endpoint `POST /v1/context/maintenance`
  and a CLI `context maintenance` command.

### Phase 2 execution notes
- The `/v1/context/packet` endpoint (task 010) becomes the primary agent-facing surface.
  It must return: `items`, `manifest`, and optional `budget_info`.
- Token budget estimation (task 011) must be a pure heuristic — no model call.
  Use `bytes ÷ 4` as a conservative tokens estimate. Never call an LLM.
- Also add CLI command `context packet` wrapping this endpoint.

### Phase 3 execution notes
- Old `POST /v1/context/promote` can be deprecated (kept for one sprint, then removed).
- Three new endpoints: `POST /v1/context/promote/request`, `POST /v1/context/promote/approve`,
  `POST /v1/context/promote/apply`.
- Also add CLI commands: `context promote request`, `context promote approve`.

### Phase 4 execution notes
- Schema migration: `auth_tokens` table gains `client_id`, `scopes` (JSON array),
  `namespace_globs` (JSON array), `max_budget_json`.
- Existing tokens (no scopes) default to full access for backwards compat during migration.
  Since app is not live, this transition can be handled cleanly.
- `authorizeMutatingRequest` becomes `authorizeRequest(op, namespace)` with richer logic.
- Also add CLI commands: `context token create`, `context token revoke`, `context token list`.

### Phase 5 execution notes
- Broker produces a `PacketRequest` struct; service validates against caps before executing.
- Broker is sandboxed: cannot expand scope, cannot target forbidden namespaces.
- This phase is B-priority — deliver Phases 0-4 first.

### MCP Adapter notes (sprint-2026-10)
- Phase-1 adapter maps: `head`, `history`, `views/evaluate` → MCP tool calls.
  This is mostly wiring — thin transport layer over existing HTTP handlers.
  Use stdio transport (standard for local MCP).
- Phase-2+3 adapter adds: `context/packet`, `promote/request`, `promote/approve`.
  Must respect capability token scopes (Phase 4 must be complete first).
- The adapter lives in `internal/mcpadapter/` (new package).
- Expose via `cmd/contextd` with `--mcp` flag or as a separate `cmd/contextmcp`.

---

## CLI Command Coverage (MVP target)

After all phases, the CLI should support:

```
context namespace register <ns> [--actor <a>]
context namespace show <ns>
context put <ns> <key> [--actor <a>] [--file <f>]
context get <ns> <key> [--revision <r>]
context history <ns> <key>
context view [--spec <json>]
context packet [--selector <json>] [--pins] [--budget <n>]     # new Phase 2
context promote request <ns> <key> --target <ns2>/<key2>       # new Phase 3
context promote approve <request-id>                            # new Phase 3
context maintenance trim [--namespace <ns>]                     # new Phase 1
context maintenance scan                                        # existing, improved
context token create --name <n> --scopes <s> [--namespaces <g>] # new Phase 4
context token list                                              # new Phase 4
context token revoke <id>                                       # new Phase 4
context contract [run|list]
```

---

## Orchestrator Execution Instructions

The following concrete changes must be made by the Orchestrator:

### 1. Cancel tasks 236, 237, 238
In each task file, change `status: todo` → `status: cancelled` and add note.

### 2. Defer GUI tasks 239, 240, 241, 242, 001, 002, 003, 004
In each task file:
- Change `sprint_id: sprint-2026-03-gui-template` → `sprint_id: sprint-post-mvp-gui`
- Change `priority: A` → `priority: C`
- Add field: `deferred_reason: "GUI is post-MVP; build after CLI/API/MCP are agent-usable"`

### 3. Create new MCP task files
Create `TASK-20260227-019.md` and `TASK-20260227-020.md` (see task specs below).

### 4. Update bootstrap.md
- Change "What to do next" to point to `TASK-20260227-005`
- Update todo count and sprint summary

---

## New Task Specs

### TASK-20260227-019: MCP adapter phase-1 read/view

```yaml
id: TASK-20260227-019
title: "Implement MCP adapter phase-1: read and view tools"
status: todo
priority: A
project: context-memory-service
tags: [mcp,adapter,read,views,sprint-2026-10-mvp-mcp]
sprint_id: sprint-2026-10-mvp-mcp
depends_on: [sprint-2026-04-eval-phase0]
created_at: 2026-02-27
updated_at: 2026-02-27
```

**Description**: Implement the MCP adapter for phase-1-read-view capability set. Creates a thin
stdio transport layer in `internal/mcpadapter/` that maps MCP tool calls to existing API/CLI
contracts. Phase-1 covers: `head`, `history`, `views/evaluate`.

**Acceptance**:
- MCP adapter package created in `internal/mcpadapter/`
- Phase-1 tools: `context_head`, `context_history`, `views_evaluate` implemented
- Result ordering matches API/CLI determinism guarantees
- Adapter is side-effect free for read/view operations
- MCP adapter exposed via `cmd/contextd --mcp` flag or `cmd/contextmcp`
- Tests validate tool call → HTTP handler → response round-trip

### TASK-20260227-020: MCP adapter phase-2+3 write/packet/promote tools

```yaml
id: TASK-20260227-020
title: "Implement MCP adapter phase-2+3: write, packet, and promote tools"
status: todo
priority: A
project: context-memory-service
tags: [mcp,adapter,write,packet,promote,sprint-2026-10-mvp-mcp]
sprint_id: sprint-2026-10-mvp-mcp
depends_on: [TASK-20260227-019, sprint-2026-06-eval-phase2, sprint-2026-08-eval-phase4]
created_at: 2026-02-27
updated_at: 2026-02-27
```

**Description**: Extend the MCP adapter to include write and promote tool surfaces.
Phase-2 tools: `context_packet`. Phase-3 tools: `context_write`, `promote_request`,
`promote_approve`. All mutating operations require explicit enablement and capability token scopes.

**Acceptance**:
- Phase-2+3 tools are explicitly-enabled (not default-on)
- Capability token scopes enforced before any mutating call
- Namespace ownership policy respected
- Audit events emitted for all mutations via MCP surface
- Tests cover: auth-required rejection, scope rejection, policy-denied rejection, success path

---

## Architecture Note

The core architectural principle this plan enforces:

> The GUI is a client. Clients are optional. The product is the deterministic
> context memory service with its API, CLI, and MCP surfaces. Ship those first.
> A GUI built against a stable, well-understood API will be better than one
> built while the API is still changing.

This is captured in `.volon/pcc/05_decisions.md` as ADR-MVP-001.
