---
id: "feat-2026-02-21-worktree-start"
type: "requirements"
status: draft
project: "volon"
tags: ["git", "worktree", "skill"]
priority: B
created_at: "2026-02-21"
updated_at: "2026-02-21"
---

# worktree-start — Requirements

## Summary

The `worktree-start` skill must allow an agent (or user) to move a task
from planning to active execution by creating an isolated git worktree with
a consistently-named branch, in a single invocation.

## Acceptance Criteria

### AC-1: Config-driven paths
- GIVEN `volon.yaml` contains `git.worktree_root` and `git.branch_prefix`
- WHEN the skill runs
- THEN it uses those values for all path and branch construction
- AND never hardcodes `.worktrees/` or `volon/`

### AC-2: Slug resolution
- GIVEN `$0` is a task ID (`TASK-YYYYMMDD-NNN`)
- WHEN the skill runs
- THEN it reads `.volon/tasks/<id>.md`, extracts `title`, and slugifies it
  (lowercase, spaces → hyphens, special chars removed)
- GIVEN `$0` is a plain slug (not matching the task ID pattern)
- THEN it uses it directly as the branch suffix

### AC-3: Branch naming
- Branch name = `<branch_prefix><slug>`
- Example: prefix `volon/` + slug `worktree-start` → `volon/worktree-start`

### AC-4: Worktree creation
- Worktree path = `<worktree_root>/<slug>`
- Example: root `.worktrees` + slug `worktree-start` → `.worktrees/worktree-start`
- The worktree directory must not already exist
- The branch must not already exist locally

### AC-5: Base branch
- Default (`base=auto`): use the current branch as base
- If current branch detection fails: use `main`; if absent, use `master`
- Explicit `base=<name>` overrides auto-detection

### AC-6: Task status update
- IF `$0` was a valid task ID AND task file exists
- THEN update `status` from `todo` to `doing` in `.volon/tasks/<id>.md`
- AND append a message `[YYYY-MM-DD] worktree-start: branch <branch_name>` to Updates

### AC-7: Output
- On success: print worktree path and branch name
- On failure: print a clear error with suggested remediation

### AC-8: Guard — worktrees disabled
- IF `git.use_worktrees: false` in `volon.yaml`
- THEN output instructions for manual branch creation and stop (do not create worktree)

## Decisions

- 8 acceptance criteria — all derived from ideation open questions.
- `base=auto` defaults to current branch, not hardcoded `main`.
- Task update is conditional (no failure if task ID absent or not found).
- Non-goals are listed explicitly to bound scope.

## Non-goals

- Does not push the branch to remote.
- Does not open a PR.
- Does not switch the agent's working directory.
- Does not install dependencies in the worktree.

## Open questions

- None remaining from ideation — all resolved above.

## Evidence

- Inspected: `artifacts/ideas/worktree-start-idea.md`
- Inspected: `volon.yaml` (`git.*` fields)
- Inspected: `docs/04_task-model.md` (task status values)
- Inspected: `plugins/tasks-nanite/skills/task-update/SKILL.md` (update protocol)
- Workflow: `workflow-new-feature "worktree-start"` — iteration 1, step 5
