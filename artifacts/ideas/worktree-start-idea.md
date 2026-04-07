---
id: "feat-2026-02-21-worktree-start"
type: "idea"
status: draft
project: "volon"
tags: ["git", "worktree", "skill"]
priority: B
created_at: "2026-02-21"
updated_at: "2026-02-21"
---

# worktree-start — Idea

## Summary

Volon needs a git worktree skill that isolates feature work into a dedicated
directory and branch without disturbing the main working tree. The `worktree-start`
skill should accept a task ID or slug, derive a branch name from the configured
prefix, create the worktree under the configured root, and report back the paths
so the agent can immediately begin working in isolation.

This skill is the bridge between task creation and code execution. Without it,
developers must manually run `git worktree add`, name branches consistently,
and cross-reference task IDs. With it, the transition from plan to execution is
a single invocation.

## Decisions

- Use `git worktree add` — this is the only standard git mechanism for this.
- Derive slug from task title when a task ID is provided; use argument directly
  when a plain slug is provided.
- Branch prefix comes from `volon.yaml` (`git.branch_prefix`) — not hardcoded.
- Worktree root comes from `volon.yaml` (`git.worktree_root`) — not hardcoded.
- Update task status to `doing` automatically if a task ID is provided.

## Open questions

- Should the skill fail or warn if `git.use_worktrees: false`? (Lean: warn + stop.)
- What happens if the branch already exists remotely? (Lean: fail with clear message.)
- Should the skill `cd` into the worktree, or only report the path?
  (Lean: report path only — let the user switch context.)

## Evidence

- Inspected: `volon.yaml` — `git.use_worktrees`, `git.worktree_root`, `git.branch_prefix`
- Inspected: `plugins/git/skills/worktree-start/SKILL.md` (current scaffold)
- Inspected: `.volon/pcc/04_backlog.md` — worktree-start listed as deferred in iteration 1
- Workflow: `workflow-new-feature "worktree-start"` — iteration 1, step 5
