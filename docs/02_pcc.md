---
intent: system_doc
audience: humans
---

# Project Context Cache (PCC) — v0.2

## Purpose
PCC is an **agent-only** set of short, structured files that reduce repeated repository scanning.

PCC is **not** the user documentation set; it is a compact, truth-checked context pack for agents.
**PCC is a cache, not authoritative state.** Tasks, bootstrap, and git history remain the source of truth.

## Layered PCC model

Volon uses two PCC layers:

| Layer | Name | Location | Purpose |
|---|---|---|---|
| L0 | Global | `.agentrc/pcc/global/` | Project-wide invariants — refreshed on workflow start or git change |
| L2 | Task | `.agentrc/pcc/tasks/<task_id>.md` | Resume capsule — written on `/pause-task`, read on `/resume-task` |

See `docs/pcc_layers.md` for full layer design, caps, and staleness strategy.

## L0 — Required files (in `.agentrc/pcc/global/`)
- `00_project.md` — what this repo is, goals/non-goals, repo type signals
- `01_architecture.md` — components, boundaries, data flow
- `02_conventions.md` — coding standards, branching, naming, test commands
- `03_workflows.md` — run/test/release commands, environments
- `04_backlog.md` — current sprint pointer + top roadmap items
- `05_decisions.md` — lightweight ADR log (append-only)

Optional:
- `06_interfaces.md` — public APIs/CLIs
- `07_security.md` — auth surfaces, secret handling, threat notes
- `08_observability.md` — logging/metrics/tracing notes

## L2 — Task PCC capsule
- Written by `/pause-task` to `.agentrc/pcc/tasks/<task_id>.md`
- Read by `/resume-task` before re-entering the task loop
- Schema: `.agentrc/templates/task-pcc-capsule.md`
- Max 400 words per capsule

## Style rules
- Keep each section short (config `max_section_words`)
- Prefer bullets and links over prose
- Never invent behavior; mark unknowns as **TBD**
- Always include an **Evidence** section (files/commands used)

## Refresh protocol
1. Read existing PCC.
2. Check git signals:
   - `git status --porcelain`
   - `git diff --name-only`
   - recent log window (default `HEAD~20..HEAD`)
3. Update only impacted sections.
4. Write run log + decision log (if enabled).
5. Log the refresh in the current task’s `## Updates` so the hygiene cycle is auditable (see `docs/05_bootstrap.md` for the initial boot + recurring cleanup checklists that mandate a PCC refresh).

## PCC maintenance skill
- `/pcc-refresh [scope]`
