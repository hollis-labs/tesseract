# Inception Workflow — v0.5

## Purpose
**Inception** is the “system builds itself” workflow: a repeatable loop where Volon uses its own artifacts (tasks, PCC, bootstrap, skills, workflows) to evolve Volon (or any repo) with high throughput and low drift.

Optimized for:
- many small, verifiable changes
- frequent context resets/restarts
- single-writer discipline
- optional bounded parallelism (read-only sub-agents)
- deterministic checkpoints (bootstrap + logs + commits)

## Core principle
**Chat is not the state. Artifacts are the state.**

Authoritative state lives in:
- `agentrc.yaml` (or `.agentrc/agentrc.yaml`)
- `.agentrc/bootstrap.md`
- `.agentrc/pcc/`
- `.agentrc/tasks/` and `.agentrc/backlog/`
- `.agentrc/logs/`

Any session can restart and resume by reading these artifacts.

## Roles

### Orchestrator (default session)
Responsibilities:
- interpret ground truth from repo artifacts
- select next unit of work (task / workflow step)
- delegate bounded analysis to sub-agents (optional)
- apply changes as the **single writer**
- verify, log, update bootstrap
- commit per policy (iteration/task)

Constraints:
- only writer to tasks/backlog/logs/PCC/bootstrap
- no recursive sub-agent spawning

### Sub-agents (optional)
Read-only “pure compute.”
- single objective
- explicit inputs
- strict output format
- no file writes, no task/log/PCC/bootstrap updates, no spawning

## Inputs
- repository root
- Volon artifacts (if present)
- `agentrc.yaml` (or `.agentrc/agentrc.yaml`)

Optional:
- sub-agent enablement/limits (config)
- PR mode policy (config)

## Outputs per run
At minimum:
- task status updates + `Updates` appended
- run log entry (`.agentrc/logs/…`)
- bootstrap updated (`.agentrc/bootstrap.md`)
- optional commit (policy-driven)

Over time:
- new/updated skills and workflows
- refined PCC summaries
- clarified docs/contracts
- backlog capture/promotions

## Canonical loop

### Phase 0 — Preflight (every session start)
Orchestrator:
- reads `agentrc.yaml`
- reads `.agentrc/bootstrap.md` (if present)
- reads PCC index (high-level only)
- reads task list + statuses
- reads latest run log pointer (if referenced)
- runs `git status` / `git diff --stat` if allowed

Exit criteria: Orchestrator can state “What’s next” in one sentence based on artifacts.

### Phase 1 — Select next work unit
Selection order:
1. If bootstrap names a next task/step, do that.
2. Else pick highest priority `todo` task (A > B > C), oldest first.
3. If no tasks: promote a backlog item to a task.
4. If nothing exists: run a workflow to create structured backlog/tasks.

### Phase 2 — Execute (small, verifiable)
For the selected task:
1. set `status: doing`
2. implement the smallest coherent change
3. verify (tests/lint/sanity checks)
4. append task `Updates` (timestamped, factual)

Optional: delegate read-only sub-agents for bounded analysis.

Rule: sub-agents never write; Orchestrator integrates outputs.

### Phase 3 — Close work unit
Mark the task:
- `done` if verified
- `blocked` if cannot proceed (external dependency)
- `paused` if intentionally suspended (not a blocker)

Write a run log entry with:
- tasks completed
- deliverables (files changed/added)
- notable decisions
- constraints / risks
- next actions

### Phase 4 — Finalize iteration
- run `/bootstrap-update`
- commit per policy:
  - iteration mode: one commit per iteration
  - isolated task: commit per task (common sense)

Bootstrap should include:
- iteration number
- counts (todo/blocked/paused/done)
- top next tasks
- blockers
- latest run log pointer

## Pause/Resume
Use:
- `/pause-task restart "<note>"` to externalize state and restart cleanly.
- start a new session and run `/resume-task "<optional note>"`.

## Exploration without polluting orchestration
Preferred patterns:

### Pattern A: bounded delegation
Orchestrator delegates scoped reading/summaries/scans to read-only sub-agents.

### Pattern B: separate workflow session (deep exploration)
1. Orchestrator pauses and writes a handoff artifact (Knowledge Artifact or backlog item).
2. Run an investigation workflow in a separate session.
3. Produce a Knowledge Artifact.
4. Resume orchestration via bootstrap and integrate.

## Guardrails (non-negotiable)
- Single writer: only Orchestrator writes tasks/logs/PCC/bootstrap.
- Bounded delegation: max sub-agents per task/run; no recursive spawns.
- Artifact-first: if it isn’t in an artifact, it isn’t reliable.
- Small steps: no large refactors without explicit planning artifacts.
- Verification required: “done” requires evidence.
- Restartability: any session can end at any point; bootstrap ensures continuity.

## Recommended Inception Run Prompt (copy/paste)
```markdown
You are operating in a Volon-managed repository in Orchestrator Mode.

Do not rely on prior chat context. Repo artifacts are the only truth:
- agentrc.yaml, .agentrc/bootstrap.md, .agentrc/pcc/, .agentrc/tasks/, .agentrc/backlog/, .agentrc/logs/

Rules:
- You are the single writer. Sub-agents (if enabled) are read-only.
- Work in small, verifiable steps. Prefer completing 1–3 tasks this run.

Run:
1) Preflight: read bootstrap/PCC/tasks; summarize “what’s next” in one sentence.
2) Execute: select next task (A>B>C, oldest first). doing → verify → done/blocked/paused.
3) Log: write run log entry.
4) Finalize: run /bootstrap-update.
5) Commit per policy (iteration if in iteration mode; task if isolated).

Stop after <K> tasks or when no todo remains. End with DONE.
```
