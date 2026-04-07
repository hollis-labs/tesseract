# SPEC-TASKS Execution Plan (2026-02-25)

## Source Reviewed
- `SPEC-TASKS/task-1-bootstrap-new-forge-repo`
- `SPEC-TASKS/task-2-architect-planning-and-scope`
- `SPEC-TASKS/task-3-pivot-task` (A/B/C)

## Summary
This repository is a fresh install with Volon boot profiles present but runtime state not initialized. The architect-provided specs define a doc-first sequence: bootstrap scaffolding, baseline architecture/spec set, then a pivot update that generalizes scope from personal cache to a context registry + working memory model.

## Sequencing
1. Initialize Volon runtime directories and baseline bootstrap/log artifacts.
2. Complete bootstrap scaffolding docs and output deliverables.
3. Author baseline architecture/spec docs + ADR-0001.
4. Apply pivot updates to scope/architecture/storage + ADR-0002.
5. Update API + CLI specs for multi-client, deterministic view usage.
6. Add dedicated Views spec and examples.

## Task Breakdown
- `TASK-20260225-001` Bootstrap and scaffold repo/docs/output artifacts.
- `TASK-20260225-002` Author baseline planning docs under `docs/SPECS`.
- `TASK-20260225-003` Write ADR-0001 (storage + namespace constraints).
- `TASK-20260225-004` Pivot A: update architecture/scope/storage + ADR-0002.
- `TASK-20260225-005` Pivot B: update API/CLI specs and output diffs/examples.
- `TASK-20260225-006` Pivot C: define deterministic Views model + examples.

## Dependencies
- Task 001 must complete before 002+ so repository scaffolding and outputs conventions are stable.
- Task 003 depends on 002 (ADR references baseline storage + namespace model).
- Task 004 depends on 002+003.
- Task 005 depends on 004.
- Task 006 depends on 004 and informs 005 examples.

## Risks
- `volon` CLI binary is not currently available in PATH in this environment, so task creation was bootstrapped manually following documented task schema.
- Existing repository docs may overlap with new deliverables; resolve by preferring spec targets as canonical and linking legacy docs where needed.

## Definition of Ready
- Each task has concrete acceptance and verification sections.
- Bootstrap points at the next task with explicit counts.
- Required folders for Volon state are present.
