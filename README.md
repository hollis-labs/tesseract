# Volon — Agentic Development System (v0.5)

> This repository (`volon-dev`) is the private development workspace for Volon.
> Clean releases are exported via `scripts/create-volon.sh` and published at
> [`hollis-labs/volon`](https://github.com/hollis-labs/volon). Use this repo for
> contributor work; use the Volon release repo in downstream projects.

A **project-agnostic** set of Claude Code plugins, skills, and workflow contracts for high-throughput, low-drift agentic development.

Volon automates the full development lifecycle:
**Ideation → Requirements → PRD → Spec → Plan → Tasks → Execute (PR/worktree) → QA/Docs → Investigate**

## Context Service Bootstrap Note

This repo is currently being used to scaffold initial planning artifacts for the
Context Memory Service. Bootstrap deliverables are located at:

- `docs/ARCHITECTURE.md`
- `docs/SCOPE.md`
- `docs/DEV.md`
- `docs/DECISIONS/`
- `docs/SPECS/`
- `outputs/`

---

## Agent entry points

**Starting a Volon session (agents):** See [`CLAUDE.md`](CLAUDE.md) — contains the boot sequence and copy-paste Inception Run Prompt.

**Authoritative boot doc:** [`.agentrc/agent-boot.md`](.agentrc/agent-boot.md) — ground truth sources, core rules, reference map.

**Current state:** [`.agentrc/bootstrap.md`](.agentrc/bootstrap.md) — iteration, active tasks, next actions.

---

## What's in this repo

- `agentrc.yaml` — system configuration
- `plugins/` — Claude Code plugins (8 plugins, 22 skills)
- `docs/` — system specs (config, PCC, workflow contracts, task model, orchestrator, inception)
- `.agentrc/` — runtime state (bootstrap, PCC, tasks, logs, boot pack)
- `examples/` — sample configs and workflow traces
- `user-docs-mini-site/` — static user-facing documentation site

## Key capabilities (v0.5 — all epics complete)

- **Project Context Cache (PCC)** — low-token, repo-local agent context
- **Inception workflow** (`/workflow-inception`) — canonical self-building loop
- **Full workflow lifecycle** — new-feature, docs-review, app-investigation, extension authoring
- **Workflow authoring** — create/edit/clone/deprecate workflows from within Volon
- **Task + backlog management** — file-based tasks, priority-ordered execution
- **Git integration** — worktrees, PR mode, commit-per-task or commit-per-iteration
- **Pause/resume** — deterministic state externalization; any session can restart cleanly
- **Quality scans** — correctness, security, dead code, performance smells
- **Bootstrap boundaries** — every iteration ends with a clean, restartable state file

## Quick start (loading plugins)

Run the helper script (keeps this volon-dev repo up to date, then launches Claude with every plugin registered):

```bash
./scripts/volon-cli.sh --repo /path/to/target-repo
```

- Set `FORGE_NO_SYNC=1` to skip the git fetch/pull step.
- Override the Claude binary with `CLAUDE_BIN=/path/to/claude`.
- The `--repo` flag is handled by the script (it `cd`s into that path before launching). Pass additional Claude args after `--`, e.g. `./scripts/volon-cli.sh --repo /path -- --model claude-sonnet-4-6`.

Manual invocation (equivalent, shown for reference):

```bash
claude \
  --plugin-dir /path/to/volon/plugins/core \
  --plugin-dir /path/to/volon/plugins/workflows \
  --plugin-dir /path/to/volon/plugins/git \
  --plugin-dir /path/to/volon/plugins/docsmith \
  --plugin-dir /path/to/volon/plugins/quality \
  --plugin-dir /path/to/volon/plugins/backlog \
  --plugin-dir /path/to/volon/plugins/workflow-author \
  --plugin-dir /path/to/volon/plugins/prompt-volon
```

Then in a target repo with `agentrc.yaml` (copy it from the Volon release repo):

```
/workflow-inception          # run the canonical self-building loop
/workflow-new-feature "..."  # full ideation → tasks pipeline
/workflow-docs-review        # PCC-grounded documentation scan
/quality-run                 # correctness / security / dead-code / perf scan
```

## Docs

- [`docs/13_inception-workflow.md`](docs/13_inception-workflow.md) — the canonical loop
- [`docs/08_orchestrator.md`](docs/08_orchestrator.md) — Orchestrator mode
- [`docs/volon-template.md`](docs/volon-template.md) — clean template/export + release steps
- [`docs/01_config.md`](docs/01_config.md) — configuration reference
- [`docs/03_workflow-contracts.md`](docs/03_workflow-contracts.md) — workflow execution contracts
- [`docs/04_task-model.md`](docs/04_task-model.md) — task model
- [`docs/09_commands.md`](docs/09_commands.md) — all available commands
- [`docs/MCP_TOOLS.md`](docs/MCP_TOOLS.md) — agent-facing MCP tool catalog (per-domain tools, scopes, playbooks)
- [`CHANGELOG.md`](CHANGELOG.md) — release history; consumers (Nanite et al.) watch this for new tools/routes
- [`docs/RELEASE.md`](docs/RELEASE.md) — versioning + release-cut procedure
/notes
- `go build ./cmd/volon` currently emits the CLI binary (prints “Volon” in usage/output).
  The path will move to `cmd/volon` once the module rename (TASK-20260224-010) lands.
- Use Go modules directly: `go install github.com/hollis-labs/volon-dev/cmd/volon@latest`
  (or `@<tag>`) to install the binary globally.
- When preparing a release, run `scripts/create-volon.sh --target ~/Projects-apps/volon`
  and push that clean repo to `hollis-labs/volon` (see `docs/volon-template.md`).
## Repository layout

- `volon-dev/` — private development repo (this project). Remote will move to
  `github.com/hollis-labs/volon-dev` after the rename (see `docs/volon-dev.md`).
- `volon/` — clean release repo (exported via `scripts/create-volon.sh` and pushed to
  `github.com/hollis-labs/volon`).

### Migrating from legacy Forge layout

If you cloned an older revision that still had `forge.yaml` and `.forge/`, run:

```bash
scripts/migrate-to-volon.sh
```

This renames the config/state to `agentrc.yaml` + `.agentrc/`. Then rerun
`scripts/volon-cli.sh /bootstrap-update` to regenerate bootstrap state.
