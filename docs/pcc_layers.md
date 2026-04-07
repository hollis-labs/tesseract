---
intent: system_doc
audience: humans
---

# PCC Layers — v0.1

## What PCC is (and is not)

**PCC (Project Context Cache) is a cache**, not authoritative state.

- Tasks, bootstrap, and git history are the source of truth.
- PCC exists to reduce repeated repository scanning by providing pre-digested, bounded summaries.
- PCC may be stale. When in doubt, re-read the source files.
- PCC may be regenerated at any time without data loss.

---

## Layered PCC model

Volon implements two PCC layers. Higher-numbered layers are narrower in scope and shorter-lived.

| Layer | Name | Scope | Location | Lifetime |
|---|---|---|---|---|
| L0 | Global | Project-wide invariants | `.agentrc/pcc/global/` | Persistent; refreshed on workflow start or git change |
| L2 | Task | Single task resume capsule | `.agentrc/pcc/tasks/<task_id>.md` | Per-task; written on pause, consumed on resume, retained for audit |

> **L1 (Iteration) and L3/L4 (Sprint) layers are NOT implemented.** They are reserved for future use if iteration-scoped context becomes necessary.

---

## L0 — Global PCC

**Scope:** Project-wide facts that change rarely and apply to all agents in all tasks.

**Required files** (in `.agentrc/pcc/global/`):
- `00_project.md` — identity, purpose, goals/non-goals, active config
- `01_architecture.md` — components, plugin/skill table, data flow
- `02_conventions.md` — coding standards, branching, naming, test commands
- `03_workflows.md` — run/test/release commands, environments
- `04_backlog.md` — current sprint pointer + top roadmap items
- `05_decisions.md` — lightweight ADR log (append-only)

**Caps:**
- Max 400 words per file (configurable via `pcc.limits.max_section_words` in `agentrc.yaml`)
- Max 25 files total (configurable via `pcc.limits.max_files`)

**Staleness strategy:**
- Refreshed automatically on workflow start and on detected git changes (`agentrc.yaml` → `pcc.refresh`)
- The `pcc-refresh` skill performs targeted updates (minimal diff — only impacted sections change)
- Each file carries a `## Evidence` section with `Last refreshed` date and trigger paths

---

## L2 — Task PCC (Resume Capsule)

**Scope:** Everything needed to resume a single task after a context reset or session restart.

**Location:** `.agentrc/pcc/tasks/<task_id>.md`
*Example:* `.agentrc/pcc/tasks/TASK-20260222-007.md`

**Written by:** `/pause-task` skill (every pause, overwrites if exists)
**Read by:** `/resume-task` skill (on session start, before execution)

**Caps:**
- Max 400 words per capsule
- One file per task; task_id in filename matches task file id

**Staleness strategy:**
- `paused_commit` field captures HEAD hash at pause time
- On resume, `/resume-task` compares current HEAD to `paused_commit` to detect divergence
- If diverged: agent notes delta and adjusts next actions if needed
- Capsule is NOT deleted after resume (retained as audit record)

**Schema:** See `.agentrc/templates/task-pcc-capsule.md`

---

## Caps and enforcement

| Dimension | Limit | Enforcement |
|---|---|---|
| Words per file | 400 (default) | `pcc-refresh` trims body bullets; never removes headings or Evidence |
| Files (global) | 25 max | Enforced by convention; alert if exceeded |
| Task capsules | 1 per task | Overwrite on re-pause; accumulate in `tasks/` dir |
| `05_decisions.md` | No trim | WARN only; manual trim required |

---

## Non-goals

- **PCC is not a transcript store.** It does not replay conversation history.
- **PCC is not the PRD or spec.** Those live in `artifacts/` or `docs/`.
- **Task PCC is not a diff store.** It summarizes; the git diff is the diff.
- **No L1 iteration PCC.** Iteration state lives in `.agentrc/bootstrap.md`.
- **No L3/L4 sprint PCC.** Sprint state is a future backlog item.
- **No queue runner implementation.** See `docs/queue_task_runner.md` for the concept draft.

---

## Evidence

- Created: 2026-02-22 (iteration 32)
- Trigger: Layered PCC implementation — new feature work
- Source docs: `docs/02_pcc.md`, `agentrc.yaml` (`pcc.*` keys), `docs/10_pause_resume.md`
