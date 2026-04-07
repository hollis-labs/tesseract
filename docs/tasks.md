---
intent: system_doc
audience: humans
---

# Volon Tasks CLI

`volon task` is a deterministic Go-based CLI for working with `.agentrc/tasks/TASK-*.md`. Markdown remains the source of truth; the CLI simply automates common lifecycle transitions and maintains an optional SQLite index at `.agentrc/state/volon.db` for fast queries.

## Commands

| Command | Description | Example |
|---|---|---|
| `volon task create "<title>" [--type ...] [--priority A|B|C] [--tags a,b] [--parent ID] [--sprint sprint-YYYY-MM]` | Generates the next `TASK-YYYYMMDD-###`, writes a new markdown file with the standard sections, and indexes it. | `volon task create "Add sprint workflow" --type feature --priority A --tags workflow,v0.5 --sprint sprint-2026-02` |
| `volon task start <id>` | Validates the task is `todo`, sets `status: doing`, and appends a timestamped `## Updates` entry. | `volon task start TASK-20260224-001` |
| `volon task done <id>` | Validates the task is `doing`, sets `status: done`, and appends a completion update. | `volon task done TASK-20260224-001` |
| `volon task show <id>` | Prints the entire markdown file to stdout. | `volon task show TASK-20260221-001` |
| `volon task list [--status ...] [--type ...] [--tag ...] [--priority ...] [--sprint ...] [--limit N]` | Lists tasks from the SQLite index (fallback to file scan if the DB is unavailable). Includes a dedicated Sprint column. | `volon task list --status doing --sprint sprint-2026-02 --limit 10` |
| `volon task reindex` | Rebuilds `.agentrc/state/volon.db` from every `.agentrc/tasks/TASK-*.md`. Run this after manual edits or if the DB schema changes. | `volon task reindex` |

## Backlog commands

`volon backlog` mirrors the `/backlog-task` skill so capture/list/promote flows can run entirely in the CLI:

| Command | Description | Example |
|---|---|---|
| `volon backlog list [--status ...] [--priority ...] [--tag ...] [--limit N]` | Scans `.agentrc/backlog/BACKLOG-*.md`, filters by status/priority/tag substring, and prints a tabular queue. | `volon backlog list --status captured --priority B --limit 5` |
| `volon backlog show <id>` | Dumps the full markdown for a backlog entry (frontmatter + body). | `volon backlog show BACKLOG-20260224-004` |
| `volon backlog promote <id> [--title ...] [--priority ...] [--tags a,b] [--type ...] [--sprint slug]` | Creates a new task (same schema as `volon task create`), sets `promoted_from`, and updates the backlog file to `status: promoted`/`promoted_to: TASK-...`. | `volon backlog promote BACKLOG-20260224-004 --priority A --tags cli,backlog` |

Promotion uses the same ID allocator as `volon task create` and writes both files atomically (rolls back the task if the backlog update fails). If you pass `--title/--priority/--tags`, they override the backlog frontmatter; otherwise the CLI reuses the captured values. Use `--sprint <slug>` to populate `sprint_id` in the new task.

Flags are CSV-friendly: `--tags` accepts `a,b,c` and `--tag` can be repeated to require multiple substrings.

> Flag order matters for `volon task create`: place flags before the quoted title (e.g., `volon task create --sprint sprint-2026-02 "Add workflow"`). The Go flag parser stops parsing options once it sees the first positional argument.

## State machine

| Transition | Owner | Notes |
|---|---|---|
| `create → todo` | `volon task create` | Creates file + index row. |
| `todo → doing` | `volon task start` | Validates current status; rejects other states. |
| `doing → done` | `volon task done` | Validates current status; rejects other states. |
| `doing → paused` | `/pause-task` skill | CLI intentionally does **not** implement pause/resume; skills continue to update PCC/bootstrap. |
| `paused → doing` | `/resume-task` skill | Use `/resume-task` after `/pause-task restart` to regain orchestrator context. |

Pause/resume and PCC capsule generation are still owned by the existing skills. `volon task` never touches `.agentrc/bootstrap.md`, `.agentrc/pcc/`, or `.agentrc/logs/`.

## File layout

- `.agentrc/tasks/` — Canonical markdown files (`TASK-YYYYMMDD-###.md`). Create/start/done only modify frontmatter and append to `## Updates`.
- `.agentrc/state/volon.db` — SQLite index (metadata only). Safe to delete; any CLI command will recreate it, and `volon task reindex` fully rebuilds it.

## Reindexing

Run `volon task reindex` when:
- You edited task files manually (outside the CLI).
- The SQLite schema version changes and the CLI prompts you to reindex.
- `.agentrc/state/volon.db` was deleted or corrupted.

The command scans every `.agentrc/tasks/TASK-*.md`, parses frontmatter, and repopulates the `tasks` table. Any files that fail to parse are skipped with an error message so you can fix them manually.

## Examples

```sh
# Create a new task and start it
volon task create "Implement Automation Framework" --type feature --priority A --tags automation,v0.5
volon task start TASK-20260224-004

# List active todos
volon task list --status todo --priority A --limit 5

# Finish work and inspect the final file
volon task done TASK-20260224-004
volon task show TASK-20260224-004 | less

# Rebuild the cache after a manual edit
rm -f .agentrc/state/volon.db
volon task reindex
```

When in doubt, run any command with `--help` to see the available flags and usage hints.

## Operational expectations
- All task lifecycle changes (create/start/done/status inspection) **must** go through `volon task …` unless you are repairing a corrupt file.
- Start every session with `volon task list --status todo --priority A` and cite those counts in the boot confirmation block (`.agentrc/boot/orchestrator.md`).
- Run `volon task reindex` before `/bootstrap-update` and after any manual edits or merges that touch `.agentrc/tasks/`.
- Append a short `## Updates` entry referencing the CLI command (`Started via volon task start`, `Completed via volon task done`) to keep a human-readable audit trail.

## Embedding the CLI into workflows
- **Boot/cleanup:** `docs/05_bootstrap.md` now mandates `volon task reindex` + queue review in both the initial boot and recurring cleanup checklists.
- **Orchestrator loop:** `.agentrc/boot/orchestrator.md` requires the queue to be selected via `volon task list` and transitions to run via CLI commands.
- **Skills:** `plugins/core/skills/forge-task` (to be renamed) wraps CLI usage; use it when a workflow needs to mutate `.agentrc/tasks/` deterministically.

## Feature gaps & proposed skills
1. **`task-audit` skill (new):** Read `.agentrc/bootstrap.md`, run `volon task list`, and emit discrepancies (e.g., bootstrap claims 0 todos but CLI lists >0). Acceptance: fails if drift persists. Would codify the hygiene checklist the user requested.
2. **`task-hygiene` skill (new):** Run `volon task reindex`, `/pcc-refresh scope=all`, and `/bootstrap-update` in one guided loop, capturing a run log entry + evidence for each step. Useful as a scheduled cleanup workflow.
3. **`task-archive` workflow (extension of BACKLOG-20260224-001):** After CLI MVP, move completed tasks into `.agentrc/tasks/archive/YYYY/` while keeping `volon task show` / `--include-archived` semantics. Requires CLI + skill support.

Capture these proposals in `.agentrc/backlog/` once we are ready to staff them; until then this section is the canonical reference.
