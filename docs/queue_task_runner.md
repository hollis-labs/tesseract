---
intent: system_doc
audience: humans
status: concept-draft
---

# Deterministic Queue / Task Runner — Concept Draft

> **Status: CONCEPT DRAFT — NOT IMPLEMENTED**
>
> This document describes a future design for a deterministic, autonomous task runner
> that consumes Task PCC capsules. No code exists for this. It is captured here to
> inform future architecture decisions.

---

## Problem statement

The current Volon Orchestrator operates interactively: a human session starts, the agent
reads bootstrap, executes 1–3 tasks, and the session ends. There is no automated, scheduled,
or queue-driven execution mode.

A **deterministic queue runner** would allow tasks to be executed autonomously without
a live human session, using Task PCC capsules as the resume context.

---

## Proposed design

### Core loop

```
while queue not empty:
    task = queue.pop()           # dequeue next task (priority order: A > B > C, oldest first)
    context = load_context(task) # L0 Global PCC + L2 Task PCC (if exists)
    gate = preflight(task, context) # pre-execution checks
    if gate.fail:
        route(task, "blocked", gate.reason)
        continue
    result = execute(task, context, max_actions=3)  # bounded execution
    verified = verify(result, task.acceptance_criteria)
    update(task, result, verified)  # task file + run log + Task PCC
    if verified.confidence < threshold or verified.blockers:
        route(task, "paused", result.next_actions)
    elif verified.complete:
        route(task, "done")
    else:
        queue.push(task)  # re-enqueue with updated Task PCC
```

### Context loading

```
load_context(task):
    global_pcc = read_all(".agentrc/pcc/global/")   # L0: project-wide invariants
    task_pcc   = read(".agentrc/pcc/tasks/<id>.md") # L2: resume capsule (if exists)
    return merge(global_pcc, task_pcc)
```

The runner loads **only** L0 and L2 — no conversation history, no prior session state.
This is intentional: capsules must be self-sufficient for resume.

### Preflight gates

Before executing any task, the runner checks:

| Gate | Condition | Failure action |
|---|---|---|
| Git cleanliness | `git status --porcelain` is empty or only expected changes | Block: "dirty working tree" |
| Branch match | Current branch matches task's expected branch (if set in Task PCC) | Block: "branch mismatch" |
| Commit drift | Current HEAD == `paused_commit` in Task PCC (if resuming) | Warn: "N commits since pause; review delta" |
| No open blockers | Task has no unresolved `blocker_type: external` | Block: "external dependency unresolved" |
| Baseline tests | (Optional, configurable) test suite passes before starting | Block: "tests failing at baseline" |

### Bounded execution

- The runner executes **at most 3 actions** per task per dequeue cycle.
- Actions are derived from Task PCC `next 1–3 actions` section (if present) or from fresh task analysis.
- After 3 actions (or fewer if verified), the runner writes a new Task PCC capsule and either re-enqueues or finalizes.

### Confidence routing

```
if result.confidence == "low":
    route(task, "paused", reason="low confidence — human review needed")
elif result.confidence == "medium" and blockers_exist:
    route(task, "blocked", blockers)
else:
    continue_or_finalize()
```

---

## Outputs per cycle

For each completed cycle, the runner writes:

1. **Updated task file** — status, Updates entry, new acceptance criteria check state
2. **Run log entry** — in `.agentrc/logs/run-<date>-<iter>.md`
3. **Updated Task PCC capsule** — `.agentrc/pcc/tasks/<task_id>.md` (overwritten)
4. **Bootstrap update** — reflects new task state and `paused_task_id` if paused

---

## Relationship to existing Volon components

| Component | Role in queue runner |
|---|---|
| `.agentrc/pcc/global/` (L0) | Project context loaded at start of every cycle |
| `.agentrc/pcc/tasks/` (L2) | Per-task capsule: loaded at start, written at end |
| `.agentrc/tasks/<id>.md` | Authoritative task state (source of truth) |
| `.agentrc/bootstrap.md` | Queue head pointer; updated after each cycle |
| `pcc-refresh` skill | Keeps L0 fresh; called at workflow start or on git change |
| `pause-task` skill | Human-triggered equivalent of "route to paused" |
| `resume-task` skill | Human-triggered equivalent of "pop from paused + preflight" |

---

## Non-goals (for this draft)

- **Not implemented.** No queue infrastructure, no worker process, no scheduler.
- **Not a multi-agent system.** This runner is single-writer by design (Orchestrator role).
- **Not a CI/CD pipeline.** It does not replace test runners or deployment systems.
- **No L1/L3/L4 PCC.** The runner uses only L0 and L2 context layers.

---

## Open questions (for future design)

1. **Queue storage**: file-based task priority queue vs. nanite-backed queue?
2. **Concurrency**: can two runners hold different tasks safely? (single-writer constraint suggests no without locking)
3. **Trigger mechanism**: cron? git hook? manual invocation only?
4. **Confidence scoring**: how does the runner derive a confidence value from execution results?
5. **Human escalation**: how does the runner notify a human when all tasks are paused/blocked?

---

## Evidence

- Created: 2026-02-22 (iteration 32)
- Status: concept draft only — no implementation exists
- Inspired by: Volon pause/resume pattern, Task PCC capsule spec, L0/L2 layered PCC design
- Related: `docs/pcc_layers.md`, `.agentrc/templates/task-pcc-capsule.md`, `docs/10_pause_resume.md`
