---
intent: system_doc
audience: humans
---

# Configuration (agentrc.yaml) — v0.1 (updated with Orchestrator)

## Goals
- Project-agnostic configuration
- Composable plugin enablement
- Storage backend selection (files)
- Observability/verbosity policy
- Git worktree + PR policy
- PCC (Project Context Cache) policy
- Orchestrator mode and bounded sub-agents

## File locations (resolution order)
1. `agentrc.yaml`
2. `.agentrc/agentrc.yaml`

## Schema (proposed)
```yaml
version: 1

agents:
  orchestrator:
    enabled: true
    default_role: pm_orchestrator
    single_writer: true
    read_bootstrap_first: true
  subagents:
    enabled: false
    max_per_task: 2
    max_per_run: 6
    allow_commands: false
    forbid_writes: true
    no_recursive_spawns: true

project:
  id: auto
  name: auto

storage:
  backend: files
  files:
    root: ".agentrc/tasks"  # task file location

observability:
  verbosity: normal # quiet|normal|verbose|trace
  write_run_log: true
  write_decision_log: true
  redact_secrets: true
  log_dir: ".agentrc/logs"

git:
  use_worktrees: true
  worktree_root: ".worktrees"
  branch_prefix: "volon/"
  pr_mode: optional # off|optional|required
  pr:
    base_branch: auto
    title_prefix: "volon:"
    body_template: ".agentrc/templates/pr-body.md"
  commit_mode: iteration   # iteration|isolated — see docs/11_git-hooks.md
  auto_commit: false       # if true, /commit-task runs automatically at finalize

pcc:
  enabled: true
  location: ".agentrc/pcc"
  commit_to_git: true
  refresh:
    on_workflow_start: true
    on_git_change: true
    scheduled: false
  limits:
    max_files: 25
    max_section_words: 400

workflows:
  new_feature:
    enabled: true
    defaults:
      track_tasks: true
      require_spec: true
  new_app:
    enabled: false
  update_docs:
    enabled: true
  quality_loop:
    enabled: true
    cadence_days: 14
  app_investigation:
    enabled: true
    defaults:
      depth: deep                      # surface|deep — default analysis depth
      output_recommendations: false    # if true, findings stage includes recommendations section

quality:
  modes: [dead_code, security, correctness, perf_smells]
  on_issue:
    default_action: create_task # log|create_task|auto_fix_pr

prompt_generator:
  default_role: orchestrator      # always orchestrator; cannot be changed
  commit_policy: iteration        # iteration|task — default commit grouping for generated prompts
  pr_mode: optional               # off|optional|required — whether to include PR step
  subagents_enabled: false        # false = single writer only; true = read-only sub-agents, max 2/run
  verbosity: standard             # standard|verbose|minimal — affects prompt detail level

tasks:
  tasks_dir: ".agentrc/tasks"   # canonical markdown location
  state_dir: ".agentrc/state"   # directory for volon.db (created automatically)
  db_file: "volon.db"         # SQLite cache file, rebuildable

backlog:
  dir: ".agentrc/backlog"       # location for BACKLOG-*.md (list/show/promote commands scan this)

models:
  default: claude-sonnet-4-6
  fallback: claude-haiku-4-5-20251001
  overrides:
    read_scan: claude-haiku-4-5-20251001
    summarize: claude-haiku-4-5-20251001
    generate: claude-sonnet-4-6
    plan: claude-sonnet-4-6
    orchestrate: claude-sonnet-4-6
    complex_reasoning: claude-opus-4-6
  workflows:
    quality_run: claude-sonnet-4-6
    pcc_refresh: claude-haiku-4-5-20251001
  agent_caps:
    worker: claude-haiku-4-5-20251001
    reviewer: claude-sonnet-4-6
  large_context_threshold_tokens: 50000
  large_context_action: warn              # warn|downgrade|block
```

## Notes
- Unrecognized keys should be ignored (forward compatible).
- Orchestrator mode is a **process role**; enforcement is via prompts + workflow contracts until implemented as tooling.
- `tasks_dir` / `state_dir` / `db_file` are optional; omit `tasks:` entirely to keep legacy defaults. The SQLite cache is index-only: deleting it is safe because `volon task reindex` rebuilds it from `.agentrc/tasks/`.
- `volon.db`, `volon.db-shm`, and `volon.db-wal` in `.agentrc/state/` are the Volon SQLite cache. They are safe to delete and rebuild via `volon task reindex`.
