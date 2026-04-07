---
intent: system_doc
audience: humans
---

# Workflow Authoring — v0.4

## Purpose

Workflows are **first-class, user-creatable artifacts** in Volon. This document
defines the workflow definition format, lifecycle, and storage layout.

A workflow is a reusable, parameterizable sequence of steps that produces
predictable artifacts. Volon ships core (volon-domain) workflows; users author
their own (user-domain) workflows using the same machinery.

---

## Workflow Domains

```
workflows/
  volon/    # versioned, opinionated — maintain and evolve Volon itself
  user/     # authored, cloned, or adapted — solve arbitrary user problems
```

**Volon workflows** are the core skills in `plugins/workflows/` and `plugins/*/`.
Their definitions are authoritative in the plugin SKILL.md files.
`workflows/volon/` is a reference index — not the implementation location.

**User workflows** live in `workflows/user/` as Markdown definition files.
They follow the same format as volon workflows but are user-managed.

---

## Workflow Definition Format

Every workflow definition is a Markdown file with YAML frontmatter:

```yaml
---
name: workflow-name           # kebab-case, used as invocation slug
version: "0.1"
domain: volon | user
intent: forge_workflow | user_workflow
status: draft | active | deprecated
tags: []
created_at: YYYY-MM-DD
updated_at: YYYY-MM-DD
description: "One sentence describing what this workflow does."
invocation: "/workflow-name [arg1] [arg2=default]"
plugin: null                  # set to plugin-name if promoted to a plugin
replaces: null                # set to old workflow name if this is a revision
---

# Workflow: <Name>

Brief paragraph expanding the description.

## Arguments

| Arg | Type | Default | Description |
|---|---|---|---|
| arg1 | string | — | ... |

## Steps

1. Step one — what it does, what artifact it produces
2. Step two ...

## Artifacts Produced

| Artifact | Class | Location |
|---|---|---|
| ... | system_doc / project_doc / knowledge_artifact | ... |

## Invariants

- Bullet list of non-negotiable behaviors

## Evidence

- Created: YYYY-MM-DD
- Source: (brief, user intent, etc.)
```

---

## Lifecycle

```
draft → active → deprecated
```

| Status | Meaning |
|---|---|
| `draft` | Being authored; not yet validated or invocable by default |
| `active` | Validated and available for use |
| `deprecated` | Superseded or retired; `replaces` field on successor points back |

**Lifecycle rules:**
- A `draft` workflow may be invoked explicitly but should not appear in default `/list-workflows` output
- Deprecation is non-destructive — the file remains; `status: deprecated` is the signal
- Only one `active` workflow per `name` per domain at a time

---

## Creating a Workflow (manual, v0.4)

Until `/workflow-create` is implemented (EPIC-002 Part 2), create workflows manually:

1. Copy the definition template above to `workflows/user/<name>.md`
2. Fill in all frontmatter fields
3. Write the Steps and Invariants sections
4. Set `status: draft`
5. Validate by dry-running the steps manually or in a scratch session
6. Set `status: active` when satisfied

---

## Cloning a Workflow

To adapt an existing workflow:
1. Copy `workflows/user/<source>.md` to `workflows/user/<new-name>.md`
2. Update `name`, `created_at`, `replaces: <source>` in frontmatter
3. Set `status: draft`
4. Modify steps as needed

---

## Deprecating a Workflow

1. Set `status: deprecated` in the frontmatter
2. Set `updated_at` to today
3. In the successor workflow, set `replaces: <deprecated-name>`

---

## Promotion to Plugin (optional)

When a user workflow is stable and widely reused, it may be promoted to a plugin skill:
1. Create `plugins/<plugin>/skills/<name>/SKILL.md` from the workflow definition
2. Add `plugin: <plugin-name>` to the workflow frontmatter
3. Update plugin.json to include the skill
4. The `workflows/user/<name>.md` file becomes a reference/redirect

---

## Storage Layout

```
workflows/
  volon/
    README.md          # index of core volon workflows + their plugin locations
    workflow-docs-review.md  (reference only; impl in plugins/workflows/)
    workflow-new-feature.md  (reference only)
  user/
    README.md          # how to create and manage user workflows
    <your-workflow>.md
```

---

## Dry-Run Testing

Before setting `status: active`, validate the workflow by dry-running it.
A dry-run is a manual trace through every step to confirm correctness before
any real side effects occur.

### What to check per step

For each step in your workflow:
1. **Inputs are reachable** — any file reads, config keys, or git commands return meaningful output
2. **Outputs are well-defined** — the artifact or side effect produced by the step is unambiguous
3. **Idempotency** — re-running the step on already-completed work produces no duplicates or errors
4. **Error paths** — at least one failure case is handled with a clear message

### Dry-run procedure

1. Open a scratch session (no volon plugin loaded — plain Claude chat or terminal)
2. Trace each step manually using the workflow's actual shell commands (`!` prefixed) and logic
3. Note any ambiguities, missing commands, or unverifiable facts → mark as **TBD** in the workflow
4. Fix issues and repeat until all steps trace cleanly
5. Set `status: active` only after a complete clean trace

### Activating after dry-run

```
/workflow-edit <name> status=active
```

Or manually update the frontmatter `status` field.

### Checklist (recommended before activation)

- [ ] Steps section is fully written (no placeholder text)
- [ ] Every step has a verifiable output or side effect
- [ ] Invariants section is complete
- [ ] At least one full dry-run trace completed without errors
- [ ] Evidence section notes the dry-run date and result

---

## Workflow Lifecycle Skills

| Skill | Purpose |
|---|---|
| `/workflow-create <name>` | Scaffold a new draft workflow from template |
| `/workflow-edit <name> [field=value]` | Apply structured frontmatter edits |
| `/workflow-clone <source> <new-name>` | Copy + reset to draft, records provenance |
| `/workflow-deprecate <name> [successor]` | Mark deprecated, non-destructively |

All skills live in `plugins/workflow-author/`.

---

## Evidence

- Created: 2026-02-21 (EPIC-002 Part 1, TASK-085, iteration 24)
- Source: `volon_v0.4_execution_brief.md` — EPIC-002 scope + workflow domains section
- Reviewed against: `docs/07_artifact-classes.md`, `plugins/workflows/skills/*/SKILL.md`
- Next: EPIC-002 Part 2 — `/workflow-create` skill (iteration 25)
