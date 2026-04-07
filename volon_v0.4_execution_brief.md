# Volon v0.4 — Execution Brief for Local Agent

> Purpose: Provide a concise, actionable brief to continue Volon development using the Volon process itself.
> This file incorporates the reviewed summary and translates it into concrete execution guidance.

---

## Context (Read First)

You are operating inside a Volon-managed repository.

Ground truth sources:
- `agentrc.yaml`
- `.agentrc/pcc/`
- `.agentrc/tasks/`
- `.agentrc/bootstrap.md`
- `.agentrc/logs/`

Do **not** rely on prior chat context. Treat repository artifacts as authoritative.

Volon v0.3 has proven viable. v0.4 focuses on **clarifying intent boundaries**, **formalizing workflow authoring**, and **expanding beyond dev-only use cases** (investigation, knowledge synthesis).

---

## Current State Summary (v0.3)

Volon is now:

- **Workflow- and artifact-driven**
- **Restartable** via Bootstrap
- **Context-resilient** (PCC + externalized state)
- **Safe for bounded parallelism** (sub-agent policy)
- **Human-governed**, not autonomous

What works well:
- Iteration loops
- Task/backlog separation
- Bootstrap as an execution boundary
- Markdown + frontmatter as the universal artifact format

What feels fuzzy (and is now scoped):
- “Docs” are overloaded
- Volon workflows vs user workflows are not explicitly separated
- Investigation / knowledge-generation workflows are not first-class (yet)

These are **semantic and organizational issues**, not architectural failures.

---

## Clarified Concepts (Authoritative for v0.4)

### Artifact Classes (Intent-Based)

| Class | Purpose | Audience |
|------|--------|----------|
| System Docs | Explain Volon itself | Humans |
| Project Docs | Explain a user’s project | Humans |
| Knowledge Artifacts | Investigation, summaries, maps | Humans + Agents |
| PCC | Compressed agent context | Agents only |
| Bootstrap | Execution handoff | Humans + Agents |

> “Docs” should no longer be used as a generic term in workflows.

---

### Workflow Domains

Workflows fall into two domains:

```
workflows/
  forge/    # opinionated, versioned, core
  user/     # authored, cloned, deprecated
```

- **Volon workflows** maintain and evolve Volon.
- **User workflows** solve arbitrary problems (apps, investigations, writing, planning).

Same machinery, different intent.

---

## Key Use Cases to Support (Validation Targets)

1. **App Investigation / Understanding**
   - Deterministic analysis + AI synthesis
   - Produces Knowledge Artifacts
   - May not create tasks
   - Does not assume development intent

2. **GUI for Local Agent Management**
   - One chat per task
   - One orchestration chat
   - Volon acts as protocol + state backend
   - UI is out of scope; ensure protocol compatibility

---

## Volon v0.4 Epics & Backlog

### EPIC-001: Artifact Intent Formalization
Goal: Remove ambiguity around “docs”.

- Define artifact classes formally
- Update workflow contracts to reference artifact intent
- Add `intent` frontmatter field to artifacts

---

### EPIC-002: Workflow Authoring Workflow (Meta)
Goal: Make workflows first-class, user-creatable artifacts.

- Guided workflow creation
- Edit / review / clone / deprecate workflows
- Dry-run testing of workflows
- Optional promotion to plugin

Artifacts produced:
- Workflow definition (Markdown + frontmatter)
- Supporting docs
- Optional skill/plugin scaffolding

---

### EPIC-003: Investigation Workflows
Goal: Expand Volon beyond dev workflows.

- Introduce `investigate` workflow intent
- Create an `app-investigation` workflow
- Define Knowledge Artifact storage conventions
- Add deterministic analysis hook points (AST, deps, config)

---

### EPIC-004: Backlog Capture & Promotion
Goal: Make idea capture frictionless and structured.

- Add `/backlog-task` skill
- Introduce `.agentrc/backlog/` directory
- Document backlog → task promotion
- Add `sprint_id` and `iteration_id` to task schema

Rules:
- Sprint can invoke a loop
- Loop cannot invoke a sprint

---

### EPIC-005: Git & Hooks Formalization
Goal: Reduce noise while preserving traceability.

- Document commit strategy:
  - Iteration mode → commit per iteration
  - Isolated task → commit per task
- Define hook lifecycle (no full implementation yet)
- Add `/commit-task` skill scaffold
- Reference commit hooks in loop runner docs

---

### EPIC-006: Bootstrap & Restart UX
Goal: Make restartability a first-class UX.

- Add `/bootstrap-up <instruction>` skill
- Allow user intent override on restart
- Add CLI + future GUI usage examples

---

## Execution Guidance (How to Proceed)

Use the Volon process itself:

1. Create backlog entries for each Epic and initial Tasks.
2. Select a small slice (1–2 epics) for the next iteration.
3. Run iteration loops (tasks-driven).
4. Finalize each iteration with:
   - task updates
   - run log
   - bootstrap update
   - appropriate git commit

Avoid large refactors. Prefer incremental clarification.

---

## Verification Checklist (v0.4 Exit Criteria)

- [ ] Artifact intent is explicit everywhere
- [ ] Workflow authoring is possible without hand-editing
- [ ] Investigation workflows produce Knowledge Artifacts cleanly
- [ ] Backlog → task promotion is seamless
- [ ] Restarting from Bootstrap works with and without user overrides

---

## Notes

- External tools (AST, graphs, CLI helpers) should be added via hooks, not embedded logic.
- Favor protocol definitions over implementations.
- Keep Volon usable without any GUI.

---

Generated on: 2026-02-22T01:29:40.989064
