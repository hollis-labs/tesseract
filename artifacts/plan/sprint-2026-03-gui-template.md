---
intent: system_doc
audience: humans
sprint_id: sprint-2026-03-gui-template
created_at: 2026-02-26
updated_at: 2026-02-26
---

# Sprint Plan — sprint-2026-03-gui-template

## Goal
Implement the first operational GUI for Context Memory Service using `.volon/templates/website-golden-template/` as the baseline UX/system template.

## Outcome targets
- Ship a working web UI shell aligned to template navigation + IA.
- Wire core screens to real API/CLI-backed data flows.
- Keep deterministic semantics, ownership boundaries, and audit visibility explicit in UI.

## Template source
- `.volon/templates/website-golden-template/README.md`
- Top-level pages:
  - `index.html`, `context-explorer.html`, `view-builder.html`, `write-promote.html`, `policy-manager.html`, `audit-ops.html`, `environment-auth.html`, `docs.html`
- Pattern/detail pages:
  - `namespace-detail.html`, `record-detail.html`, `key-history.html`, `compare-revisions.html`, `selector-preset.html`, `selector-evaluate.html`, `promote-request.html`, `policy-detail.html`, `policy-edit.html`, `event-detail.html`, `incident-detail.html`, `token-management.html`

## Backlog selection for this sprint
1. BACKLOG-20260226-001 — GUI foundation + app shell from template
2. BACKLOG-20260226-002 — Context explorer pages + data wiring
3. BACKLOG-20260226-003 — View builder + selector evaluate/preset flow
4. BACKLOG-20260226-004 — Write/promote + promote request flow
5. BACKLOG-20260226-005 — Policy manager + detail/edit flow
6. BACKLOG-20260226-006 — Audit/ops + event/incident detail flow
7. BACKLOG-20260226-007 — Environment/auth + token management flow
8. BACKLOG-20260226-008 — QA, accessibility, responsive hardening, docs handoff

## Suggested promotion order
1. Foundation + navigation shell
2. Read paths (explorer, views)
3. Mutating paths (write/promote, policy)
4. Operations/auth surfaces
5. QA + release readiness

## Non-goals (this sprint)
- Re-designing the visual system away from the supplied template baseline.
- Building speculative features not represented in current API/CLI/MCP docs.
- Introducing hidden side effects into read/view screens.
