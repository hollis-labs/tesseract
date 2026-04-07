---
intent: system_doc
audience: humans
---

# Sprint Workflow — planning + execution contract

## Purpose
Elevate sprints as the macro-planning layer above iteration loops. A sprint groups multiple iteration runs under a common goal, making it easier to reason about burn-down, backlog rotation, and retro notes without overloading `.agentrc/bootstrap.md`.

## Definitions
- **Sprint** — multi-iteration planning block (usually 1–2 weeks). Owns backlog selection, success criteria, and retro inputs.
- **Iteration** — the execution loop you already run per task batch. Iterations happen inside a sprint but keep the same single-writer rules.
- **Sprint log** — markdown artifact tracking goals, selected tasks, and retro data (store in `artifacts/plan/sprint-<slug>.md` or similar).

## Workflow stages
1. **Preflight**
   - `/pcc-refresh scope=all`
   - `volon backlog list --status captured --priority A,B`
   - Draft sprint slug (`sprint-YYYY-MM`).
2. **Selection**
   - Promote backlog items via the CLI with sprint assignments:  
     `volon backlog promote BACKLOG-20260224-007 --sprint sprint-2026-02`
   - For already-active tasks, run `volon task create ... --sprint sprint-2026-02`.
3. **Execution**
   - Normal Orchestrator loop. Use `volon task list --sprint sprint-2026-02 --status todo` to keep the sprint board visible.
   - Update sprint log with progress per iteration (link run logs + bootstrap snapshots).
4. **Review + retro**
   - `volon task list --sprint sprint-2026-02 --status done` to confirm completion.
   - Summarize learnings + carry-over backlog items in the sprint log.
   - Archive/roll forward any unfinished items (promote to next sprint slug before resuming).

## CLI support
- `volon task create ... --sprint sprint-YYYY-MM` sets `sprint_id` in frontmatter.
- `volon task list --sprint sprint-YYYY-MM` filters the queue; output now includes a Sprint column for quick scanning.
- `volon backlog promote ... --sprint sprint-YYYY-MM` tags new tasks while promoting from the backlog.

## Documentation updates
- `docs/05_bootstrap.md` now differentiates sprint vs iteration responsibilities.
- `docs/tasks.md`, `docs/09_backlog-model.md`, and `docs/09_commands.md` highlight the CLI flags so operators consistently tag sprint work.

## Evidence / next steps
- Use sprint slugs consistently in PCC + artifacts (e.g., `.agentrc/pcc/global/04_backlog.md` current sprint pointer).
- Expand with automation later (e.g., `volon sprint create` skill) once the manual workflow proves stable.
