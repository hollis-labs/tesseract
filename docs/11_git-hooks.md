---
intent: system_doc
audience: humans
---

# Git & Hooks — v0.1

## Commit strategy

Volon supports two commit modes. Set via `git.commit_mode` in `agentrc.yaml`.

### Iteration mode (default)
One commit per iteration — all task changes batched together at finalize.

```
# Commit message format
volon: iteration <N> — <task-count> tasks (<task-ids>)

# Example
volon: iteration 28 — 3 tasks (TASK-098, TASK-099, TASK-100)
```

- Triggered by `/bootstrap-update` (Step 5 of that skill, if `git.auto_commit: true`)
- Lower commit noise; better for fast-moving iterations
- Entire iteration is one atomic unit

### Isolated task mode
One commit per task — triggered by `/commit-task` after each task completes.

```
# Commit message format
volon: TASK-YYYYMMDD-NNN — <task title>

# Example
volon: TASK-20260221-098 — Create extension workflows
```

- Finer granularity; better blame, easier revert per task
- Higher commit count but cleaner history per feature
- Required when tasks produce artifacts that must be reviewable independently

### Choosing a mode

| Mode | Use when |
|---|---|
| `iteration` | Fast iteration on Volon system itself; multiple small tasks |
| `isolated` | Feature work; tasks produce independently reviewable artifacts |

---

## Hook lifecycle (v0.1 — defined, not yet implemented)

Hooks are named trigger points in the Volon lifecycle. When implemented, they will
allow external scripts or commands to run at defined points without modifying core skills.

### Trigger points

| Hook | Fires when |
|---|---|
| `on_task_start` | A task transitions from `todo` → `doing` |
| `on_task_complete` | A task transitions to `done` |
| `on_task_pause` | A task transitions to `paused` (via /pause-task) |
| `on_iteration_start` | A loop runner session begins |
| `on_iteration_end` | A loop runner session ends (before /bootstrap-update) |
| `on_commit` | After any git commit by Volon |
| `on_bootstrap_update` | After `.agentrc/bootstrap.md` is written |
| `on_pause` | After /pause-task completes |
| `on_resume` | Before /resume-task begins execution |

### Proposed config (agentrc.yaml, not yet active)

```yaml
hooks:
  on_task_complete:
    - cmd: "scripts/notify.sh"
      args: ["{{task.id}}", "{{task.title}}"]
  on_commit:
    - cmd: "scripts/post-commit-log.sh"
```

### Implementation notes

- Hooks are NOT implemented in v0.1. This doc defines the lifecycle contract.
- Implementation target: a future task/epic.
- Hooks must not block iteration progress — run async or fail-soft.
- Orchestrator is responsible for firing hooks (worker/sub-agents do not fire hooks).

---

## /commit-task skill

See `plugins/git/skills/commit-task/SKILL.md`.

Commits changes associated with a completed task. Usable in both commit modes:
- Iteration mode: called once at finalize (commits all staged changes)
- Isolated mode: called after each individual task completes

---

---

## Investigation hook points

Investigation workflows fire at defined points to allow external tooling (AST scanners,
dependency analyzers, config validators) to augment discovery without modifying core skills.

Hooks are **advisory** — they are integration points, not required for workflow execution.

### on_discovery_start

**Trigger:** Before the discovery phase enumerates files

**Example use cases:**
- Run `git log --oneline -20` to add commit history context
- Find package manifests (package.json, requirements.txt, go.mod, etc.)
- Seed config file discovery (`find . -name "*.config.*"`)
- List environment variable references (.env files, deployment configs)

### on_analysis_complete

**Trigger:** After all target files have been read and component breakdown drafted

**Example use cases:**
- Run AST analysis (complexity metrics, cyclomatic complexity)
- Generate dependency graph (internal call chains, external imports)
- Run static analysis (unused code, dead imports)
- Validate config files against known schemas
- Check for known security anti-patterns

### Notes

- Investigation hooks are NOT implemented in v0.1. This section defines the contract.
- Hooks must be **async-safe** — they should not block iteration progress.
- Hooks must produce output that can be appended to the relevant stage artifact.
- Hooks are called by the **Orchestrator** only; sub-agents do not fire hooks.
- When implemented, configuration follows the same agentrc.yaml pattern as git hooks above.

---

## References

- Commit mode config: `docs/01_config.md` under `git:`
- Loop runner: `docs/06_loop-runner.md` (finalize step)
- Orchestrator: `docs/08_orchestrator.md` (commit per policy, step 6)
- Investigation workflow: `plugins/workflows/skills/workflow-app-investigation/SKILL.md`
