---
intent: system_doc
audience: humans
---

# Orchestrator Mode — v0.1

## Purpose
Define a **thin primary session** that runs Volon by:
- reading bootstrap/PCC/tasks/config
- selecting the next unit of work (task/workflow step)
- delegating bounded work to sub-agents (optional)
- applying changes as the **single writer**
- verifying, committing (per policy), logging, and updating bootstrap

This is a **process role**, not a new architecture.

## Roles

### Orchestrator (default)
Responsibilities:
- Interpret ground truth from repo artifacts:
  - `agentrc.yaml`, `.agentrc/bootstrap.md`, `.agentrc/pcc/`, `.agentrc/tasks/`, `.agentrc/logs/`
- Decide what to do next (workflow step/task selection)
- Enforce policies (single-writer, limits, verification, commits)
- Delegate bounded work to sub-agents (optional)
- Integrate results
- Write changes (tasks/logs/PCC/bootstrap) as the only writer

Must not:
- Offload state writes to sub-agents
- Allow recursive sub-agent spawning

### Worker (bounded)
Responsibilities:
- Execute a **single scoped objective**
- Return results in the requested format

Must not:
- Edit files
- Update tasks/logs/PCC/bootstrap
- Spawn more agents

### Reviewer / Investigator (bounded)
Same constraints as Worker. Intended for:
- scans (security/dead-code)
- summaries (knowledge artifacts)
- options/alternatives

## Single-writer rule (normative)
Only the Orchestrator may write/update:
- `.agentrc/tasks/**`
- `.agentrc/backlog/**`
- `.agentrc/logs/**`
- `.agentrc/pcc/**`
- `.agentrc/bootstrap.md` and `.agentrc/bootstrap/history/**`

All sub-agents are **read-only**.

## When to delegate
Delegate when parallel work reduces latency without introducing ambiguity:
- reading multiple filesystem areas
- summarizing code sections
- scanning diffs
- generating option lists

Do not delegate:
- tasks involving file edits, renames, refactors
- anything that requires coherent multi-step local reasoning unless you keep it in the orchestrator

## Model selection
The Orchestrator resolves the model for each dispatch using the hierarchy in `docs/12_model-config.md`:
- Its own session uses `models.overrides.orchestrate` (default: `claude-sonnet-4-6`)
- Sub-agent dispatch uses `models.agent_caps.<role>` as a cap
- Per-skill `model-tier` in SKILL.md frontmatter is looked up in `models.overrides`
- Task frontmatter `model:` overrides the tier lookup

## Orchestrator loop (canonical)
1. Read `.agentrc/bootstrap.md` (if present)
2. Determine next action:
   - pick next `todo` task via `volon task list --status todo --priority A` OR the next workflow step
3. Optional: delegate bounded analysis to sub-agents
4. Apply changes locally (single writer)
5. Use the Volon CLI for lifecycle transitions:
   - `volon task create "<title>" [...]` to add work
   - `volon task start/done <id>` for status changes (never edit YAML directly)
   - `volon task reindex` before `/bootstrap-update` or whenever `.agentrc/tasks/` changed manually
6. Verify (acceptance criteria)
7. Commit per policy (task vs iteration)
8. Update task status (via CLI) + append Updates
9. Write run log
10. Finalize iteration:
    - `/bootstrap-update`

## Pausing and resuming
"Pause" is implemented by **externalizing state**, not suspending memory:
- Update tasks/log/bootstrap with the current state
- Start a fresh session and resume by reading bootstrap

Use:
- `/pause-task` to externalize state and establish a deterministic resume point.
- `/resume-task` to restart from bootstrap and continue.

See: `docs/10_pause_resume.md`, `docs/09_commands.md`, and `docs/05_bootstrap.md`

## Inception workflow

The **inception workflow** (`/workflow-inception`) is the canonical implementation of this Orchestrator loop as an executable skill. It codifies the full 4-phase cycle (Preflight → Select → Execute → Finalize) with argument-driven limits and commit policy control.

See: `docs/13_inception-workflow.md` for the full specification and recommended run prompt.
