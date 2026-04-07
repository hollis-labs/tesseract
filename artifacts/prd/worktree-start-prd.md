---
id: "feat-2026-02-21-worktree-start"
type: "prd"
status: draft
project: "volon"
tags: ["git", "worktree", "skill"]
priority: B
created_at: "2026-02-21"
updated_at: "2026-02-21"
---

# worktree-start — PRD

## Summary

`worktree-start` is a Volon git skill that transitions a task from the backlog
into active development by creating an isolated git worktree. It is the
standard entry point for all code execution work in a Volon-managed repo.

## User Flow

```
Agent or user invokes: /worktree-start TASK-20260221-001

  1. Skill reads volon.yaml for git policy
  2. Skill reads .volon/tasks/TASK-20260221-001.md → title "Implement worktree-start"
  3. Slug derived: "implement-worktree-start"
  4. Base branch detected: main
  5. git worktree add .worktrees/implement-worktree-start \
       -b volon/implement-worktree-start main
  6. Task status updated: todo → doing
  7. Output:
       Worktree: .worktrees/implement-worktree-start
       Branch:   volon/implement-worktree-start
```

## Success Criteria

| Criterion | Measurable check |
|---|---|
| Worktree created | Directory `<worktree_root>/<slug>` exists |
| Branch created | `git branch --list <branch_name>` returns the branch |
| Task updated | `.volon/tasks/<id>.md` has `status: doing` |
| Output emitted | Worktree path and branch name printed |
| No side effects | No uncommitted changes in main working tree |

## Failure Flows

| Condition | Behaviour |
|---|---|
| `git.use_worktrees: false` | Print manual branch steps; stop |
| Branch already exists | `ERROR: branch <name> already exists. Use base=<name> to resume.` |
| Worktree path already exists | `ERROR: worktree path <path> already exists.` |
| Not a git repo | `ERROR: not a git repository.` |
| Task ID not found | `WARN: task <id> not found — continuing without task update.` |
| No argument provided | `ERROR: provide a task ID or slug.` |

## Constraints

- Must read all paths from `volon.yaml` — zero hardcoded paths.
- Must not require network access (no push, no fetch).
- Must be implemented as a SKILL.md protocol (no shell scripts).
- Compatible with the file task backend (no Nanite calls).

## Decisions

- Slugification rule: `echo "<title>" | tr '[:upper:]' '[:lower:]' | sed 's/[^a-z0-9]/-/g' | sed 's/-\+/-/g' | sed 's/^-\|-$//g'`
- Task update uses the same rules as `/task-update` SKILL.md.
- Branch existence check: `git rev-parse --verify <branch_name>` — non-zero exit = branch absent.

## Open questions

- None.

## Evidence

- Inspected: `artifacts/requirements/worktree-start-requirements.md`
- Inspected: `volon.yaml`
- Inspected: `plugins/tasks-nanite/skills/task-update/SKILL.md`
- Workflow: `workflow-new-feature "worktree-start"` — iteration 1, step 5
