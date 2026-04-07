---
intent: system_doc
audience: humans
---

# User Slash Commands — v0.1

This document defines **user-invoked slash commands** (human-facing) as **process protocols**.
They are implemented initially via prompts + skills (no code required).

## Command principles
- Commands are **safe defaults**.
- Commands must be **idempotent** when possible.
- Commands must respect **Orchestrator Mode** (single-writer).
- Commands must externalize state before any context reset/restart.

---

## /pause-task [mode] [note]

### Purpose
Safely pause ongoing work by:
- updating task state + adding an update note (optional)
- writing a run log entry (if enabled)
- updating bootstrap to provide a deterministic resume point

Then instruct the user how to restart cleanly (new session) if desired.

### Modes
- `soft` (default): pause and continue in the same session if needed
- `restart`: pause, then instruct to start a fresh session and run `/resume-task`
- `compact`: pause, then instruct to compact context (if supported) and then run `/resume-task`

> Note: Agents cannot reliably force a "clear context" in the same session. `restart` is the deterministic option.

### Inputs
- `mode`: soft|restart|compact
- `note`: optional human instruction / mid-task context

### Outputs
- task file updated (status → paused; see schema)
- run log appended (optional)
- bootstrap updated (what to do next: resume task)
- printed "Resume Instructions"

---

## /resume-task [note]

### Purpose
Resume work from `.agentrc/bootstrap.md` and the task state on disk.

### Behavior
- Re-ground from:
  - `agentrc.yaml`, `.agentrc/bootstrap.md`, `.agentrc/pcc/`, `.agentrc/tasks/`, `.agentrc/logs/`
- Identify the paused item:
  - prefer `paused_task_id` in bootstrap frontmatter if present
  - otherwise select highest priority `doing/paused/blocked` task with most recent update
- Continue execution using the normal loop rules, starting with the paused task.

### Inputs
- `note`: optional user instruction that can override "what to do next" while staying consistent with state.

---

## /commit-task [task-id] [mode=iteration|isolated]

### Purpose
Commit changes for a completed task. Respects `git.commit_mode` from `agentrc.yaml`.

### Modes
- `iteration` (default): batch commit message referencing the iteration and task(s)
- `isolated`: per-task commit message `volon: TASK-ID — title`

### Inputs
- `task-id` (optional): defaults to most recently completed task
- `mode` (optional): overrides `git.commit_mode` for this invocation

### Notes
- Commits only. Use `/pr-open` to push and open a pull request.
- If `git.auto_commit: false`, must be called manually.
- See `docs/11_git-hooks.md` for commit strategy details.

---

## /pause [note]
Alias for `/pause-task soft [note]`.

## /resume [note]
Alias for `/resume-task [note]`.

---

## /agent use <profile>

### Purpose
Select the agent profile to follow **for this session**. The command displays the profile’s
metadata (prompt, PCC defaults, write scope, tool allowlist, workflow/envelope hints) and
reminds you to read the corresponding `.agentrc/boot/<role>.md`.

### Behavior
- Normalizes the slug, verifies `.agentrc/agents/<profile>.yaml` exists, and lists available profiles when a typo is detected.
- Displays the YAML so you can reference it directly, then prompts you to echo a structured summary (boot prompt, PCC includes, write scope, tools, workflows, envelopes).
- Reminds you how to relaunch with the same profile via CLI (`FORGE_AGENT_PROFILE=<profile> scripts/volon-cli.sh --repo ...` or `scripts/volon-cli.sh --agent <profile>`).

### Inputs
- `<profile>` (required): slug such as `architect`, `orchestrator`, `worker`, `reviewer`.

### Outputs
- Terminal summary of the selected profile plus usage reminders.
- No repository files are modified; run the command at the start of any session that needs
  a different persona.

---

## /workflow-new-agent "agent-name"

### Purpose
Guided workflow for defining a new Volon agent role: purpose, scope & constraints,
context requirements, interface spec, plan, and tasks.

### Outputs
- `artifacts/agents/<slug>/` — purpose, scope, context, interface, plan artifacts
- Tasks in `.agentrc/tasks/` for implementation

---

## /workflow-new-skill "skill-name" [plugin=<dir>] [standalone=true|false]

### Purpose
Guided workflow for creating a new Volon skill: ideation, requirements, SKILL.md spec,
plan, and tasks. Updates `plugin.json` and optionally `docs/09_commands.md`.

### Inputs
- `"skill-name"` (required)
- `plugin=<dir>` (optional): target plugin directory
- `standalone=true|false` (optional, default false)

### Outputs
- `artifacts/skills/<slug>/` — ideation, requirements, spec, plan artifacts
- Tasks for SKILL.md creation and plugin.json update

---

## /workflow-new-slash-command "command-name"

### Purpose
Guided workflow for defining a new user slash command: ideation, requirements, spec
(including backing skill mapping), plan, and tasks. Adds entry to `docs/09_commands.md`.

### Outputs
- `artifacts/slash-commands/<slug>/` — ideation, requirements, spec, plan artifacts
- Tasks for skill creation and docs/09_commands.md update

---

## /workflow-new-plugin "plugin-name"

### Purpose
Guided workflow for scaffolding a new Volon plugin: ideation, requirements, `plugin.json`
spec, directory structure plan, and scaffold tasks. Use `/workflow-new-skill` afterward
for full skill implementation.

### Outputs
- `artifacts/plugins/<slug>/` — ideation, requirements, spec, plan artifacts
- Tasks for plugin scaffold creation

---

## /workflow-app-investigation "app-name" [scope=path] [depth=surface|deep]

### Purpose
Guided investigation workflow for analyzing applications and codebase components.
Produces Knowledge Artifacts (scope, discovery, analysis, findings, report) suitable
for architecture reviews, dependency audits, risk assessment, or decision-making
without development intent.

### Inputs
- `"app-name"` (required): Name of app or component to investigate
- `scope=<path>` (optional): Restrict investigation to this path (default: repo root)
- `depth=surface|deep` (optional): Analysis depth
  - `surface`: entry points + config only
  - `deep`: comprehensive file-by-file analysis (default)

### Outputs
- `artifacts/knowledge/<slug>/scope.md` — investigation boundaries and questions
- `artifacts/knowledge/<slug>/discovery.md` — file inventory, dependencies, entry points
- `artifacts/knowledge/<slug>/analysis.md` — components, data flows, patterns, risks
- `artifacts/knowledge/<slug>/findings.md` — synthesized findings with confidence levels
- `artifacts/knowledge/<slug>/report.md` — shareable executive summary (status: complete)

### Notes
- Does NOT create tasks or development artifacts
- Idempotent: safe to re-run; skips existing stages
- Depth setting affects analysis breadth, not output structure
- Use for architecture reviews, audits, due diligence, or pre-feature investigation

---

## /volon-prompt "\<intent\>" [--mode ...] [--constraints ...] [--deliverables ...]

### Purpose
Generate a copy/paste-ready Volon prompt from a natural language intent. Infers the
correct template (inception/task/ideation/planning/patch), reads repo context, fills
all placeholders, and enforces guardrails automatically. Backed by
`plugins/prompt-volon/skills/volon-prompt/SKILL.md`.

### Aliases
- `/prompt-volon "<intent>"` — identical behavior
- `/make-prompt "<intent>"` — identical behavior

### Inputs
- `"<intent>"` (required): natural language description of what the prompt should do
- `--mode inception|task|ideation|planning|patch` (optional): override template selection
- `--repo <path>` (optional): target repo path (default: current directory)
- `--constraints "<text>"` (optional): additional scope/guardrail text to inject
- `--deliverables "<text>"` (optional): explicit expected outputs to inject

### Outputs
- A single formatted Markdown prompt block, ready to copy/paste into Claude Code or any agent session
- Includes: Orchestrator Mode declaration, phased approach, guardrails, DONE token

### Template inference (when `--mode` not specified)
| Intent keywords | Template |
|---|---|
| "inception", "install volon", "run loop", "iter" | inception |
| "ideation", "brainstorm", "names", "vibe" | ideation |
| "plan", "prd", "spec", "design", "roadmap" | planning |
| "patch", "apply zip", "apply patch", "hotfix" | patch |
| (default) | task |

### Notes
- Backed by `plugins/prompt-volon/skills/volon-prompt/SKILL.md`
- Configure defaults in `agentrc.yaml` under `prompt_generator:` (see `docs/14_volon-prompt.md`)
- Full documentation: `docs/14_volon-prompt.md`

---

## `volon task` (CLI)

Although not a slash command, the Go-based `volon task` CLI is part of the standard toolkit. It automates task lifecycle operations against `.agentrc/tasks/TASK-*.md` while keeping markdown canonical.

### Usage

```sh
volon task create "<title>" [--type ...] [--priority A|B|C] [--tags a,b] [--parent ID]
volon task start <id>
volon task done <id>
volon task show <id>
volon task list [--status ...] [--type ...] [--tag ...] [--priority ...] [--limit N]
volon task reindex
```

Run `go run ./cmd/volon task --help` (or build/install `cmd/volon`) for the latest flags. All commands operate within the repo containing `agentrc.yaml` (use `--repo` to target another path).

### Required usage pattern
1. **Queue review:** `volon task list --status todo --priority A` — start of every session/loop.
2. **Creation:** `volon task create "<title>" [--type ... --priority ...]` — never copy/paste skeletons.
3. **Transitions:** `volon task start <id>` when work begins, `volon task done <id>` once acceptance is verified. Manual edits to `status:` are forbidden outside repair scenarios.
4. **Inspection:** `volon task show <id>` — source of truth for acceptance/context; cite lines in run logs.
5. **Index health:** `volon task reindex` — run before finalize, after manual edits, or when the CLI emits a drift warning.

Log these invocations (especially reindex + transitions) in the active task’s `## Updates` section so reviewers can trace ownership changes.

### Notes
- Markdown files remain the single source of truth. The CLI only edits frontmatter and appends to `## Updates`.
- SQLite cache lives under `.agentrc/state/volon.db` (index-only; safe to delete/rebuild).
- Pause/resume flows stay within `/pause-task` and `/resume-task`; the CLI deliberately omits those transitions.
- Full reference: `docs/tasks.md`

---

## /backlog-task ["Title" \| list \| promote]

### Purpose
Capture product/engineering ideas into `.agentrc/backlog/`, review/filter them, and promote the right ones into active tasks. This keeps ideation visible outside the live chat window and preserves single-writer discipline.

### Modes
- `capture` (default): `/backlog-task "Add sprint workflow" priority=B tags=workflow,v0.5`
- `list`: `/backlog-task list [status=captured|promoting|promoted|dropped]`
- `promote`: `/backlog-task promote BACKLOG-20260224-004`

### Behavior
1. Capture writes a new `BACKLOG-YYYYMMDD-###.md` with the supplied metadata.
2. List enumerates `.agentrc/backlog/BACKLOG-*.md` using the requested filters.
3. Promote validates the entry, creates a new task (via `/task-create`), and updates the backlog file (`status: promoted`, `promoted_to: TASK-...`).

### CLI counterpart
All three flows are mirrored in the Go CLI:

```
volon backlog list [--status ...] [--priority ...] [--tag ...] [--limit N]
volon backlog show BACKLOG-20260224-004
volon backlog promote BACKLOG-20260224-004 --priority A --tags cli,backlog --sprint sprint-2026-02
```

Use the CLI when you need deterministic output (e.g., during backlog grooming sessions or scripted automation); use the slash command when you want the `backlog-task` skill to drive a conversational capture. Both mutate the same markdown files, so never run them concurrently in parallel sessions.
