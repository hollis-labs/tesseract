---
intent: system_doc
audience: humans
---

# Iteration Bootstrap — v0.1

## Purpose
Provide a **single, small, action-oriented** file that enables:
- restarting in a fresh prompt/session without relying on chat context
- clear "what to do next"
- a stable boundary between iterations

Bootstrap is **not** PCC and **not** user docs. It is an execution handoff.

## Files
- `.agentrc/bootstrap.md` (current; overwritten each finalize)
- `.agentrc/bootstrap/history/bootstrap-iteration-<N>.md` (optional history copy)

## Bootstrap format
```markdown
---
version: 1
type: bootstrap
iteration: <N>
updated_at: <ISO-8601>
config:
  path: agentrc.yaml
  storage_backend: files
  pcc_path: .agentrc/pcc
  tasks_path: .agentrc/tasks
  logs_path: .agentrc/logs
execution:
  driver: tasks
  max_tasks_per_run: 6
  completion_token: DONE
---

# Volon Bootstrap

## What to do next
- <one-line next action>

## Current state summary
- agentrc.yaml: present
- PCC: present
- Tasks: X todo, Y blocked, Z done
- Last run log: <path>

## Active work
### Highest priority todos
- TASK-... — <title> (A)
- ...

### Blockers
- TASK-... — <one-line blocker>

## Guardrails
- Treat repository files as ground truth. Do not rely on chat memory.
- Minimal diffs; small verifiable steps.
- Update tasks + write a run log per loop invocation.

## Evidence pointers
- PCC index: .agentrc/pcc/00_project.md
- Workflow contract: docs/03_workflow-contracts.md
- Task model: docs/04_task-model.md
```

## When to generate bootstrap
At the end of each iteration ("Finalize iteration"):
1. Ensure tasks are updated (todo/doing/blocked/done).
2. Ensure a run log exists.
3. Refresh PCC if needed.
4. Generate `.agentrc/bootstrap.md`.
5. Copy to history.

## Why this matters
Bootstrap makes **manual runs** behave like **automated runs** by forcing clean re-grounding in repo state.

## Initial boot checklist (new clone/export)
Run this once for any freshly cloned repo or release export before attempting real work:
1. `scripts/volon-cli.sh --repo <path> /bootstrap-update` — proves CLAUDE wiring works and produces the first bootstrap stub.
2. `volon task reindex` — ensures `.agentrc/state/volon.db` mirrors `.agentrc/tasks/*.md` (creates the DB if missing).
3. `volon task list --status todo --priority A` — confirm active queue count; investigate mismatches immediately.
4. `/pcc-refresh scope=all` — hydrate `.agentrc/pcc/global/*.md` based on current repo contents.
5. Verify directories: `.agentrc/{tasks,logs,pcc,state}` exist (create empty dirs with `.gitkeep` if automation didn’t).
6. Create `.agentrc/logs/README.md` or first run log if observability is enabled.

Document the run in `artifacts/plan/<slug>.md` or the initial run log so future sessions know boot is complete.

## Recurring cleanup cycle (run weekly or every iteration)
1. `volon task list --status todo --limit 50` — detect stale statuses; use `volon task start/done <id>` or `/pause-task` to fix drift.
2. `volon task reindex` — rebuild SQLite index before/after bulk task edits.
3. `/pcc-refresh scope=all` — synchronize PCC with the latest docs/code changes; append Evidence blocks.
4. `/bootstrap-update` — rewrite `.agentrc/bootstrap.md` and archive the prior iteration.
5. Archive/trim logs: move oldest files from `.agentrc/logs/` to `archive/` when >10 active logs remain.
6. `git status` — ensure no surprise drift before committing or pausing.

Note these hygiene runs in the latest run log so collaborators know the repo is clean.

## Sprints vs iterations

- **Sprints** define the planning horizon (multiple iterations). Capture backlog candidates, assign them a `sprint_id`, and track burndown for that slice of work. Use `volon backlog list --status captured --priority B` to queue options, then `volon backlog promote ... --sprint sprint-YYYY-MM` when you commit them to an active sprint.
- **Iterations** are execution loops (what the Orchestrator does today). Iterations consume sprint-selected tasks but stay bounded by `.agentrc/bootstrap.md`.
- When bootstrapping a new session, scan both the active iteration queue (`volon task list --status todo --priority A`) and the sprint queue (`volon task list --sprint sprint-YYYY-MM`) so the operator understands the broader commitments.
- The sprint workflow (see `docs/15_sprint-workflow.md`) describes how to plan, run, and retro at the sprint boundary while still finalizing every iteration via `/bootstrap-update`.
