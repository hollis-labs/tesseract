---
type: plan
title: "Release Sprint — Context Memory Service v0.1.0"
created_at: 2026-02-27
status: todo
sprint: sprint-release-v1
---

# Release Sprint Plan — Context Memory Service v0.1.0

## Context

This repo (`context-memory-service`) is a Volon-managed dev workspace. It will
not be published as-is. The public release is a **clean export** into a new
folder/repo containing only the service code — no Volon scaffolding, no
SPEC-TASKS, no .volon/ system files.

**Execution order is strict.** The name/branding decision (Phase 0) gates
everything else. Do not start GitHub, domain, or website work until the name
is confirmed.

---

## Phases and tasks

### Phase 0 — Name & Branding (GATE)

Must complete before any public-facing work begins.

| Task | Title | Priority |
|---|---|---|
| TASK-20260301-001 | Name + branding decision | A |

**Deliverable:** Confirmed name, confirmed domain availability, rough visual
identity direction (colors, logo style), confirmed GitHub org/repo slug.

---

### Phase 1 — Export Prep (parallel with Phase 0 where independent)

Define and prepare the clean export. Most of this is independent of the name.

| Task | Title | Priority | Blocks |
|---|---|---|---|
| TASK-20260301-002 | Define public export scope | A | Phase 2 |
| TASK-20260301-003 | License decision + LICENSE file | A | Phase 2 |
| TASK-20260301-004 | Go module rename | A | needs name (001) |

**What goes into the public repo:**
- `cmd/contextd/` — binary entrypoint
- `internal/` — all service packages
- `tests/` — integration tests
- `docs/QUICKSTART.md`, `docs/AGENT-SETUP.md`, `docs/CONTEXT-FOR-PROJECTS.md`
- `docs/SPECS/` — API, CLI, MCP, storage specs (user-relevant)
- `examples/` — if any
- `Makefile`, `go.mod`, `go.sum`
- `LICENSE`, `README.md` (new), `.github/`

**What stays in this dev workspace only:**
- `.volon/`, `volon.yaml`, `CLAUDE.md`, `SPEC-TASKS/`, `artifacts/`, `outputs/`
- `plugins/`, `workflows/`, `scripts/`
- `docs/01_*.md` through `docs/15_*.md` (Volon internal docs)
- `user-docs-mini-site/` (becomes the website source; lives separately)
- `VOLON_*.md`, `volon_*.md` root files

---

### Phase 2 — Public Repo Foundation (blocked by Phase 0 + 1)

| Task | Title | Priority | Blocked by |
|---|---|---|---|
| TASK-20260301-005 | Create new public repo + initial export | A | 001, 002, 003, 004 |
| TASK-20260301-006 | Write root README for public repo | A | 005 |
| TASK-20260301-007 | Add CONTRIBUTING.md + CODE_OF_CONDUCT.md | B | 005 |

---

### Phase 3 — Docs Polish (blocked by Phase 2)

| Task | Title | Priority | Blocked by |
|---|---|---|---|
| TASK-20260301-008 | Audit + update all docs for new name | A | 001, 005 |
| TASK-20260301-009 | Write HTTP API reference doc | B | 005 |
| TASK-20260301-010 | Write CHANGELOG + v0.1.0 release notes | B | 005 |

---

### Phase 4 — GitHub Setup (blocked by Phase 2)

| Task | Title | Priority | Blocked by |
|---|---|---|---|
| TASK-20260301-011 | GitHub repo config: description, topics, social preview, branch protection | A | 001, 005 |
| TASK-20260301-012 | GitHub Actions CI: go test + go build on push/PR | A | 005 |
| TASK-20260301-013 | GitHub issue + PR templates | C | 005 |

---

### Phase 5 — Website (blocked by Phase 0)

The `user-docs-mini-site/` in this workspace is an existing starting point.

| Task | Title | Priority | Blocked by |
|---|---|---|---|
| TASK-20260301-014 | Review + adapt mini-site for new name and current feature set | A | 001 |
| TASK-20260301-015 | Domain registration + deploy (GitHub Pages or Netlify) | A | 001, 014 |

---

## Dependency graph

```
001 (name) ──────────────────────────────┐
                                          ▼
002 (scope) ─┐                       004 (module rename)
003 (license)─┤                           │
              └──────────────┬────────────┘
                             ▼
                        005 (new repo + export)
                        /    |    \    \
                       ▼     ▼     ▼    ▼
                      006  007  009  010  (docs + contrib)
                       │
                       ▼
              008 (name audit) ← blocked by 001
              011 (GitHub config) ← blocked by 001
              012 (CI)
              013 (issue templates)

001 (name) ──► 014 (website) ──► 015 (domain + deploy)
```

---

## Execution notes

- **Do not start execution until post-testing weekend is complete.** These tasks
  are created now so they're ready to pick up immediately after.
- The rename affects: module path in `go.mod`, binary name (`contextd` → TBD),
  all doc references, GitHub org/repo slug. Touch it once, in task 004/008.
- The mini-site (`user-docs-mini-site/`) already has 5 pages (index, about,
  setup, use-cases, examples) — review against current feature set before
  adapting.
- CI should run `go test -race ./...` and `go build ./cmd/...` at minimum.
  Consider `golangci-lint` if a linter config is added.
