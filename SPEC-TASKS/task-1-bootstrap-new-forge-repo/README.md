# Task 1: Bootstrap a new Forge repo for the User Context Service

Date: 2026-02-24

## Objective
Create a new repository that will host the **User Context Service** (desktop app + CLI + HTTP API) using the Forge methodology.

Storage decision is fixed:
- **SQLite index + file-backed content in folders with an index**

## Constraints (non-negotiable)
- Use Forge in this repo (Inception pattern).
- Establish canonical directories for:
  - docs/specs
  - decisions (ADRs)
  - tasks
  - artifacts (agent-produced outputs)
- Do not implement the application yet; this task is repo + scaffolding only.

## Required outputs
- Repository created locally (and optionally on GitHub if available).
- Forge installed/bootstrapped in the repo (minimal footprint).
- Initial docs committed:
  - `README.md`
  - `docs/ARCHITECTURE.md` (stub)
  - `docs/DECISIONS/` (stub + index)
  - `docs/SCOPE.md` (MVP vs non-goals)
  - `docs/DEV.md` (local dev instructions stub)
- Initial Forge artifacts committed:
  - `.forge/boot/` boot prompts as needed for agents
  - `.forge/pcc/` skeleton (user + app namespaces)
  - `.forge/tasks/` skeleton

## Suggested repo name
- `hollis-labs/contextd` (or similar)

## Acceptance criteria
- A clean repo exists with a clear directory layout and stubs committed.
- Running the repo locally has documented steps (even if placeholders).
- There is no ambiguity between:
  - repo “userland” artifacts vs internal Forge state
  - docs vs generated artifacts

## Deliverable artifacts
Write these into `outputs/` for review:
- `outputs/repo-layout.md` (final tree and rationale)
- `outputs/bootstrap-steps.md` (exact commands executed)
- `outputs/open-questions.md` (items for Architect planning task)
