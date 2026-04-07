---
intent: system_doc
audience: humans
---

# Artifact Classes — v0.4

## Purpose

"Docs" is overloaded. This document defines the **5 artifact classes** used
in Volon, each with a distinct intent, audience, and storage location.
Workflows should reference these classes by name rather than the generic term "docs."

---

## Artifact Classes

| Class | Intent | Audience | Location |
|---|---|---|---|
| **System Docs** | Explain Volon itself — what it is, how skills work, configuration schema | Humans | `docs/` |
| **Project Docs** | Explain the user's specific project — features, architecture, decisions | Humans | `docs/` or project root |
| **Knowledge Artifacts** | Investigation outputs, analysis summaries, entity maps, research notes | Humans + Agents | `artifacts/knowledge/` |
| **PCC** | Compressed, truth-checked agent context — reduces repeated repo scanning | Agents only | `.agentrc/pcc/` |
| **Bootstrap** | Execution handoff — iteration boundary, what to do next | Humans + Agents | `.agentrc/bootstrap.md` |

---

## Key Rules

- **PCC is not docs.** PCC is agent-only context. Updating PCC is not "updating documentation."
- **Bootstrap is not PCC.** Bootstrap is an execution boundary. PCC is persistent project memory.
- **Knowledge Artifacts are not Project Docs.** Knowledge Artifacts are produced by investigation
  workflows and may be ephemeral or exploratory. Project Docs are curated human-readable explanations.
- When in doubt, use `intent:` frontmatter to declare class explicitly (see Conventions).

---

## The `intent` Frontmatter Field

All workflow-produced artifacts should declare their class:

```yaml
intent: system_doc | project_doc | knowledge_artifact | pcc | bootstrap
```

This field is **optional** for existing artifacts but **required** for new artifacts
produced by v0.4+ workflows.

---

## Workflow Domain Separation

Workflows also have an intent domain (from execution brief):

```
workflows/
  volon/    # opinionated, versioned — maintain and evolve Volon itself
  user/     # authored or cloned — solve arbitrary user problems
```

Same machinery, different intent. Artifact class maps to workflow domain:
- Volon workflows → `system_doc`, PCC updates, Bootstrap
- User workflows → `project_doc`, `knowledge_artifact`, task artifacts

### Workflow → Artifact Intent mapping (normative)

| Workflow | Artifact intent | Rationale |
|---|---|---|
| `workflow-new-feature` | `project_doc` | Describes a feature of the user's project |
| `workflow-new-agent` | `system_doc` | Defines a new Volon agent role |
| `workflow-new-skill` | `system_doc` | Creates a new Volon skill |
| `workflow-new-slash-command` | `system_doc` | Defines a new Volon command |
| `workflow-new-plugin` | `system_doc` | Scaffolds a new Volon plugin |
| `workflow-app-investigation` | `knowledge_artifact` | Investigation output; may be ephemeral |
| `workflow-docs-review` / `docs-sync` | `system_doc` OR `project_doc` | Depends on target |
| PCC refresh | `pcc` | Agent-only context |
| `bootstrap-update` | `bootstrap` | Execution handoff |

**Override rule:** When Volon developers use `workflow-new-feature` to build Volon features, set `intent: system_doc` to override the default.

## System Docs vs Project Docs in `docs/`

All files in the `docs/` directory are **System Docs** — they explain how Volon works.
All files in `docs/` should carry `intent: system_doc` frontmatter.

Project Docs live in the user's own project directories, not in `docs/`. If a user
adds documentation about their project inside a Volon-managed repo, those files should
declare `intent: project_doc`.

---

## Evidence

- Created: 2026-02-21 (EPIC-001, TASK-076, iteration 23)
- Source: `volon_v0.4_execution_brief.md` — Artifact Classes table + Workflow Domains section
- Reviewed against: `docs/02_pcc.md`, `docs/03_workflow-contracts.md`, `docs/05_bootstrap.md`
