---
intent: system_doc
audience: humans
---

# Workflow contracts — v0.1 (updated with Orchestrator)

## Design goals
- Composable workflows (interactive, one-shot, looped, scheduled).
- Each step produces predictable artifacts with frontmatter.
- Handoffs pass minimal context.
- Manual runs can safely reset context between iterations via **Bootstrap**.
- Default execution role is **Orchestrator** (single writer).

## Orchestrator mode (normative)
Unless explicitly overridden, the active session is the Orchestrator.
- It is the **only writer** to tasks/logs/PCC/bootstrap.
- It may delegate read-only sub-agents if enabled by config.
- It must always re-ground from bootstrap/PCC.

See: `docs/08_orchestrator.md`

## Delegation contract (sub-agents)
If sub-agents are enabled:
- Sub-agents are read-only and return results only.
- No recursive spawning.
- Orchestrator validates and applies changes.

## Artifact conventions
Markdown with YAML frontmatter.

```yaml
id: feat-YYYY-MM-DD-slug
type: idea|requirements|prd|spec|plan
intent: system_doc|project_doc|knowledge_artifact|pcc|bootstrap
status: draft
project: "{{repo.name}}"
tags: ["feature"]
priority: B
created_at: YYYY-MM-DD
updated_at: YYYY-MM-DD
```

The `intent` field declares artifact class (see `docs/07_artifact-classes.md`).
Use `intent` instead of the generic term "docs" when describing artifact targets.

Include sections:
- Summary
- Decisions
- Open questions
- Evidence (if derived from repo)

## Bootstrap (iteration boundary)
At the end of each iteration, workflows/loops should generate:
- `.agentrc/bootstrap.md` (current)
- `.agentrc/bootstrap/history/bootstrap-iteration-<N>.md` (optional)

See: `docs/05_bootstrap.md`

## Core workflow: new-feature
Artifact intent: **`project_doc`** (default — user project feature).
Override to `system_doc` when adding features to Volon itself.

Steps:
1. Preflight: `/pcc-refresh`
2. Ideation → `artifacts/ideas/<slug>.md` (`project_doc`)
3. Requirements → `artifacts/requirements/<slug>.md` (`project_doc`)
4. PRD → `artifacts/prd/<slug>.md` (`project_doc`)
5. Spec → `artifacts/spec/<slug>.md` (`project_doc`)
6. Plan → `artifacts/plan/<slug>.md` (`project_doc`)
7. Create/triage tasks
8. **Finalize iteration**: `/bootstrap-update`

## Core workflow: docs-review (`/workflow-docs-review`)
Full PCC-grounded scan — always checks all target files (System Docs and Project Docs)
against PCC ground truth. Produces updates to `system_doc` or `project_doc` artifacts.
1. Preflight: `/pcc-refresh`
2. Update target artifacts (minimal diffs against PCC)
3. Sync PCC
4. Optional PR
5. **Finalize iteration**: `/bootstrap-update`

## Supporting skill: docs-sync (`/docs-sync`)
Change-driven — intersects git change set with focus target set; no-op if no overlap.
Produces updates to `system_doc` or `project_doc` artifacts.
1. Read config + git signals
2. Intersect target set with changed paths
3. Update only changed targets (minimal diffs)
4. Sync PCC (scope=backlog)
5. Optional PR

## Extension workflows
Guided workflows for authoring new Volon extensions. Each follows the staged pattern
(preflight → ideation/purpose → requirements/scope → spec → plan → tasks → finalize).

- `/workflow-new-agent "name"` → `artifacts/agents/<slug>/` — define a new agent role
- `/workflow-new-skill "name" [plugin=<dir>]` → `artifacts/skills/<slug>/` — create a new skill
- `/workflow-new-slash-command "name"` → `artifacts/slash-commands/<slug>/` — define a user command
- `/workflow-new-plugin "name"` → `artifacts/plugins/<slug>/` — scaffold a new plugin

All four register in `docs/09_commands.md` and live in `plugins/workflows/skills/`.

## Sprint workflow (planning layer)
- **Intent:** `system_doc` (planning artifact lives under `artifacts/plan/sprint-<slug>.md`)
- **Trigger:** `sprint-YYYY-MM` cadence (weekly/bi-weekly)
- **Stages:**
  1. Preflight (`/pcc-refresh`, `volon backlog list --status captured --priority A,B`)
  2. Selection (`volon backlog promote ... --sprint sprint-YYYY-MM`, `volon task create ... --sprint ...`)
  3. Execution (normal iteration loops; monitor via `volon task list --sprint sprint-YYYY-MM`)
  4. Review/retro (summaries + carry-over backlog updates)
- **Docs:** `docs/15_sprint-workflow.md` — canonical contract
- **Output:** sprint log (targets, selected tasks, retro notes) + sprint-tagged tasks

## Investigation workflow

Investigate applications and codebase components without development intent.
Produces Knowledge Artifacts that guide future decisions.

**Intent:** `investigate`

**Command:** `/workflow-app-investigation "name" [scope=path] [depth=surface|deep]`

**Output path:** `artifacts/knowledge/<slug>/`

**Output files:**
- `scope.md` — investigation boundaries and questions (Stage 2)
- `discovery.md` — file inventory, dependencies, entry points (Stage 3)
- `analysis.md` — component breakdown, data flows, patterns, risks (Stage 4)
- `findings.md` — synthesized insights with confidence levels (Stage 5)
- `report.md` — shareable executive summary (Stage 6, status: `complete`)

**Key distinction:** Unlike new-feature and extension workflows, investigation does NOT produce tasks or development artifacts. It produces Knowledge Artifacts for decision-making.

**Knowledge Artifact frontmatter schema:**
```yaml
id: ka-YYYY-MM-DD-slug
type: knowledge_artifact
intent: knowledge_artifact
status: draft | complete
project: <project.name>
tags: [investigation, <slug>]
depth: surface | deep
created_at: YYYY-MM-DD
updated_at: YYYY-MM-DD
```

**Arguments:**
- `$0` (required): Application or component name
- `scope=<path>` (optional): Restrict investigation to this directory
- `depth=surface|deep` (optional, default: `deep`)
  - `surface`: read entry points and config only
  - `deep`: comprehensive analysis of all discovered files

**Idempotency:** Skips any stage whose artifact already exists. Safe to re-run.

## Looping behavior (tasks-driven)
A loop runner should:
1. Read `.agentrc/bootstrap.md` if present (optional; provides next action).
2. Drive execution from `.agentrc/tasks/`.
3. Update task states and write run logs.
4. End the run with **Finalize iteration**:
   - `/bootstrap-update`
5. Idempotent: if no action, say "No action required" and output DONE.
