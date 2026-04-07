---
intent: system_doc
audience: humans
---

# /volon-prompt — Prompt Generator — v0.1

## Overview

`/volon-prompt` generates a single, copy/paste-ready Volon prompt from a natural language
intent description. It eliminates manual prompt construction by:

1. Inferring the correct template from the intent
2. Reading repo context from `agentrc.yaml`
3. Filling all placeholders with resolved values
4. Enforcing non-negotiable guardrails (single writer, no destructive ops, DONE token)

Use it to bootstrap any Volon workflow without remembering the full prompt structure.

**Aliases:** `/prompt-volon`, `/make-prompt` — identical behavior.

---

## Usage

```
/volon-prompt "<intent>" [--mode inception|task|ideation|planning|patch] \
  [--repo <path>] [--constraints "<text>"] [--deliverables "<text>"]
```

### Examples

```
/volon-prompt "install volon into a new repo and run inception for a chat addon"

/volon-prompt "add layered PCC L0/L2 and wire pause/resume" --mode task

/volon-prompt "brainstorm names for the new nav component" \
  --mode ideation --constraints "lowercase, max 3 words, no abbreviations"

/volon-prompt "create a PRD and spec for sprint-based workflow" \
  --mode planning --deliverables "requirements.md, prd.md, spec.md"

/volon-prompt "apply the diff from review-fixes.patch safely"

/make-prompt "run ideation on the onboarding redesign"
```

---

## Arguments

| Argument | Required | Description |
|---|---|---|
| `<intent>` | **Yes** | Natural language description of what the prompt should accomplish |
| `--mode` | No | Override template selection. Options: `inception` \| `task` \| `ideation` \| `planning` \| `patch` |
| `--repo <path>` | No | Target repo path or name. Default: current directory / `agentrc.yaml project.name` |
| `--constraints "<text>"` | No | Additional scope or guardrail text to inject into the generated prompt |
| `--deliverables "<text>"` | No | Explicit expected outputs to inject into the generated prompt |

---

## Template Selection

If `--mode` is not specified, `/volon-prompt` infers the template from keywords in the intent.
Rules apply in order — first match wins:

| Intent contains any of... | → Template |
|---|---|
| "inception", "install volon", "boot loop", "run loop", "iter", "run inception" | `inception` |
| "ideation", "brainstorm", "brainstorming", "names", "naming", "vibe", "explore ideas", "ideas for" | `ideation` |
| "plan", "prd", "spec", "design", "roadmap", "requirements", "backlog planning" | `planning` |
| "patch", "apply zip", "apply patch", "hotfix", "apply diff", "apply fix" | `patch` |
| (no match) | `task` |

Use `--mode` to override when the inference doesn't match your intent.

---

## Templates

Five composable templates are included. Each produces a self-contained, runnable prompt.

### `inception` — Multi-task self-building loop

**Use when:** starting a new iteration, installing Volon, running multiple tasks in sequence.

**Phases:**
1. Preflight (read bootstrap/PCC/tasks, emit boot confirmation)
2. Select next work unit (bootstrap → todo tasks → backlog → new)
3. Execute (set doing → implement → verify → append Updates)
4. Close (done/blocked/paused signal)
5. Log (run log entry)
6. Finalize (bootstrap-update → commit)

**Key outputs:** task updates, run log, bootstrap, commit

---

### `task` — Single focused task execution

**Use when:** executing one specific task, resuming a paused task, fixing a targeted issue.

**Phases:**
1. Preflight (read bootstrap, identify task)
2. Execute (doing → implement → verify → Updates)
3. Close (done/blocked/paused signal)
4. Log (run log entry)
5. Finalize (bootstrap-update → commit)

**Key outputs:** task file updated, run log, bootstrap, commit

---

### `ideation` — Idea generation session

**Use when:** brainstorming names, features, approaches, or designs without writing code.

**Phases:**
1. Preflight (read PCC for context, check existing ideation artifacts)
2. Generate 5–15 ideas (named, described, rationale, risks)
3. Write `artifacts/ideas/<slug>-<date>.md`
4. Finalize (bootstrap-update → commit)

**Key outputs:** ideation artifact, bootstrap, commit

---

### `planning` — PRD/spec/plan/backlog generation

**Use when:** designing a new feature, defining requirements, producing a technical spec.

**Phases (each skipped if artifact exists):**
- Phase A: Requirements (`artifacts/requirements/<slug>.md`)
- Phase B: PRD (`artifacts/prd/<slug>.md`)
- Phase C: Spec (`artifacts/spec/<slug>.md`)
- Phase D: Plan (`artifacts/plan/<slug>.md`)
- Phase E: Backlog tasks (`.agentrc/tasks/TASK-*.md`)

**Key outputs:** planning artifacts, optional task files, bootstrap, commit

---

### `patch` — Safe patch/zip application

**Use when:** applying a code review diff, hotfix patch, or extracted zip to the repo.

**Phases:**
1. Preflight (verify repo is clean; stop if dirty)
2. Integrity check (stat/list contents, confirm scope)
3. Dry run (`git apply --check`; stop on failure)
4. Apply
5. Verify (lint/test, check for unexpected file changes)
6. Log + Commit (stage specific files only)

**Key outputs:** applied patch, run log, bootstrap, commit

---

## Guardrails (non-negotiable)

All generated prompts **always**:
- Declare Orchestrator Mode (single writer)
- Prohibit `git push --force` and `git reset --hard` without explicit `confirm: yes`
- Include a `## Guardrails` section
- Include explicit stop conditions and `DONE` token
- Restrict writes to repo root only
- Default sub-agents to off (or read-only bounded)

All generated prompts **never**:
- Allow force-push without confirmation
- Allow unbounded sub-agent spawning
- Allow writes outside repo root
- Allow mass destructive file operations
- Omit phased approach and verification requirement

---

## Overriding Defaults

### Per-invocation flags

Use `--constraints` and `--deliverables` to customize a single invocation:

```
/volon-prompt "..." \
  --constraints "do not modify any files in src/api/; read-only investigation only" \
  --deliverables "TASK-001 done, run log written, bootstrap updated"
```

### Project-level config (`agentrc.yaml`)

Add a `prompt_generator:` section to `agentrc.yaml` to set defaults for the project:

```yaml
prompt_generator:
  default_role: orchestrator      # always orchestrator (non-negotiable)
  commit_policy: iteration        # iteration | task
  pr_mode: optional               # off | optional | required
  subagents_enabled: false        # false = single writer only; true = read-only, max 2/run
  verbosity: standard             # standard | verbose | minimal
```

All keys are optional. The `default_role` cannot be changed — it is always `orchestrator`.

---

## Backing Skill

`/volon-prompt` is backed by:
- Skill: `plugins/prompt-volon/skills/volon-prompt/SKILL.md`
- Templates: `plugins/prompt-volon/skills/volon-prompt/templates/`
- Plugin: `plugins/prompt-volon/plugin.json`

---

## Reference

- Inception workflow: `docs/13_inception-workflow.md`
- Commands: `docs/09_commands.md`
- Workflow contracts: `docs/03_workflow-contracts.md`
- Config: `docs/01_config.md`
