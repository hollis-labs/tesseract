---
intent: system_doc
audience: humans
---

# Pause/Resume Protocol — v0.2

## Why pause/resume exists
Long-running work benefits from deterministic suspension points. Instead of relying on chat memory, Volon pauses by **writing state to disk** and resumes by **reading state from disk**.

This is also the safest way to deal with context compaction.

## Key rules
- The Orchestrator is the **single writer**.
- Pausing always updates:
  - task status + Updates
  - `.agentrc/pcc/tasks/<task_id>.md` — Task PCC resume capsule (L2 PCC)
  - bootstrap frontmatter (`paused_task_id`, `paused_at`, `resume_hint`)
  - optional run log entry

## Task PCC capsule (L2 PCC)

On every pause, `/pause-task` writes (or overwrites) a **Task PCC capsule** at:

```
.agentrc/pcc/tasks/<task_id>.md
```

This capsule is the deterministic resume context. It captures:
- current goal + acceptance criteria (brief)
- current plan/hypothesis
- state markers (branch, key files, commands run + outcomes)
- git diff stat at pause
- `paused_commit` — HEAD hash at pause time
- next 1–3 actions (explicit, verifiable)
- open questions / blockers
- confidence: high|medium|low

Schema: `.agentrc/templates/task-pcc-capsule.md`

On resume, `/resume-task` reads the capsule, runs a preflight checklist, then uses the capsule's "next actions" to re-enter execution.

See `docs/pcc_layers.md` for the full layered PCC design.

## Task status
- `paused`: work intentionally paused; not a blocker
- `blocked`: work cannot proceed due to an external dependency
- `doing`: active
- `todo` / `done`: unchanged

If you prefer not to add a new status, you may use `blocked` with a `blocker_type: paused`.
However, `paused` is clearer.

## Bootstrap additions
Add these optional frontmatter keys to `.agentrc/bootstrap.md`:

```yaml
paused_task_id: "TASK-YYYYMMDD-###"
paused_at: "ISO-8601"
resume_hint: "one line"
```

## Recommended pause flow (restart mode)
1. Run `/pause-task restart "<note>"`
2. End the session.
3. Start a new session in the repo root.
4. Run `/resume-task "<optional note>"`

## Why "restart" is preferred
Agents cannot reliably clear context in-place. Restarting a session is deterministic, testable, and matches the behavior of scheduled/automated runs.

## Integrations
- Use hooks later:
  - `on_pause`
  - `on_resume`
- Deterministic tools can populate pause notes (git diff, test status, etc.).
