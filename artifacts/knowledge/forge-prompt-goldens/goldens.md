---
id: ka-2026-02-22-volon-prompt-goldens
type: knowledge_artifact
intent: knowledge_artifact
status: complete
project: volon
tags: [volon-prompt, testing, fixtures, goldens]
created_at: 2026-02-22
updated_at: 2026-02-22
---

# volon-prompt — Golden Fixtures

Five reference examples showing `/volon-prompt` input → inferred mode → generated output.
Use these to verify the prompt generator produces correct Volon-style prompts.

Each golden is shown exactly as the skill would output it (filled template, no raw placeholders).

---

## Golden 1 — Inception: install volon and run loop for new app

**Command:**
```
/volon-prompt "install volon into nanite and run inception for chat addon"
```

**Inferred mode:** `inception` (intent contains "install volon" and "run inception")

**Generated prompt:**

---PROMPT-START---
You are operating in **nanite** in **Orchestrator Mode**.

**Intent:** install volon into nanite and run inception for chat addon

**Date:** 2026-02-22

Do not rely on prior chat context. Repo artifacts are the only truth:
- `volon.yaml`, `.volon/bootstrap.md`, `.volon/pcc/`, `.volon/tasks/`, `.volon/backlog/`, `.volon/logs/`

---

## Rules

- You are the **single writer**. Only you may modify tasks, logs, PCC, and bootstrap.
- Sub-agents: disabled. This session is the single writer.
- Emit the boot confirmation block before taking any action.
- Emit transition signals at task-start, task-done, and iteration-finalize.
- No destructive git operations (`reset --hard`, force-push) without explicit user confirmation.
- Write output only inside the repo root. Do not write to paths outside the repository.
- Work in small, verifiable steps. Prefer completing 1–3 tasks this run.

---

## Run

### 1) Preflight

Read ground truth from repo artifacts:

1. Run: `cat volon.yaml 2>/dev/null || cat .volon/volon.yaml 2>/dev/null || echo "NO_CONFIG"`
2. Read `.volon/bootstrap.md` — note iteration number, next actions, blockers.
3. Read `.volon/pcc/` (high-level only — `00_project.md` if present).
4. Read `.volon/tasks/TASK-*.md` — collect status counts and top-3 `todo` tasks (A>B>C, oldest first).
5. Run: `git status --short` and `git branch --show-current`

Emit boot confirmation block:
```
Volon Orchestrator confirmed. Here's the current state:

**Iteration <N>** | Branch: `<branch>` | <version/milestone>

**Status:**
- <epic summary or task completion summary>
- <done count> tasks done, <todo count> active todos
- Last run (<iter N-1>): <TASK-ID> — <one-line result>
- <uncommitted changes note, or "clean">

**Backlog (<count> items):**
1. `<BACKLOG-ID>` — <title>

**Ready.** <next action in one sentence>
```

### 2) Select next work unit

Selection order (strict):
1. If bootstrap names a specific next task/step → use that.
2. Else pick highest-priority `todo` task (A > B > C), oldest first for ties.
3. If no `todo` tasks → promote a backlog item from `.volon/backlog/`.
4. If no backlog → ask user for direction.

Emit: `**[TASK-XXXXXX-NNN] starting** — <title>`

### 3) Execute

For the selected task:
1. Set `status: doing` in the task file.
2. Read the task's Description, Acceptance, and Verification sections.
3. Implement the smallest coherent change that satisfies all acceptance criteria.
4. Verify against the task's Verification steps.
5. Append a timestamped `Updates` entry to the task file.

Constraint: complete ≤ 3 tasks per run.

### 4) Close work unit

Mark the task done/blocked/paused. Emit the appropriate signal.

### 5) Log

Write a run log entry at `.volon/logs/run-2026-02-22-iter<N>.md`.

### 6) Finalize

1. Run `/bootstrap-update`.
2. Emit: `**[Iter N] finalizing** — bootstrap update + commit`
3. Commit (iteration mode): `iter <N>: <one-line summary>`

---

## Constraints

(none — use Volon defaults)

---

## Expected Deliverables

Task status updates, run log entry, bootstrap updated, iteration commit.

---

## Guardrails

- **Single writer**: only this session writes tasks/logs/PCC/bootstrap.
- **Sub-agents: disabled. This session is the single writer.**
- No `git push --force` or `git reset --hard` without explicit user confirmation: `confirm: yes`.
- No writes outside repo root.
- Verify before marking `done` — "done" requires evidence.
- If `max_tasks` (3) reached: stop, log, finalize — do not continue unbounded.

---

Stop after 3 tasks or when no `todo` remains. End with `DONE`.
---PROMPT-END---

---

## Golden 2 — Task: implement specific feature work

**Command:**
```
/volon-prompt "add layered PCC L0/L2 and wire pause/resume"
```

**Inferred mode:** `task` (no inception/ideation/planning/patch keywords — default)

**Generated prompt:**

---PROMPT-START---
You are operating in **volon** in **Orchestrator Mode**.

**Intent:** add layered PCC L0/L2 and wire pause/resume

**Date:** 2026-02-22

Do not rely on prior chat context. Repo artifacts are the only truth:
- `volon.yaml`, `.volon/bootstrap.md`, `.volon/pcc/`, `.volon/tasks/`, `.volon/logs/`

---

## Rules

- You are the **single writer**. Only you may modify tasks, logs, PCC, and bootstrap.
- Sub-agents: disabled. This session is the single writer.
- No destructive git operations (`reset --hard`, force-push) without explicit user confirmation.
- Write output only inside the repo root.
- Work in small, verifiable steps.

---

## Run

### 1) Preflight

1. Run: `cat volon.yaml 2>/dev/null || echo "NO_CONFIG"`
2. Read `.volon/bootstrap.md` — identify the task to execute.
3. Read the target task file in `.volon/tasks/`.
4. Run: `git status --short`

Emit: `**[TASK-XXXXXX-NNN] starting** — <title>`

### 2) Execute

1. Set `status: doing` in the task file.
2. Read Description, Acceptance, and Verification sections.
3. Implement smallest coherent change satisfying acceptance criteria.
4. Verify against Verification steps.
5. Append timestamped Updates entry.

### 3) Close

Mark done/blocked/paused. Emit the appropriate signal.

### 4) Log

Write run log at `.volon/logs/run-2026-02-22.md`.

### 5) Finalize

1. Run `/bootstrap-update`.
2. Commit (iteration mode): `iter <N>: <one-line summary>`

---

## Constraints

(none — use Volon defaults)

---

## Expected Deliverables

Task file updated (status + Updates), run log written, bootstrap updated, commit.

---

## Guardrails

- **Single writer**: only this session writes tasks/logs/PCC/bootstrap.
- **Sub-agents: disabled. This session is the single writer.**
- No `git push --force` or `git reset --hard` without `confirm: yes`.
- No writes outside repo root.
- Verify before marking done — evidence required.

---

End with `DONE`.
---PROMPT-END---

---

## Golden 3 — Ideation: brainstorm names with constraints

**Command:**
```
/volon-prompt "run ideation to brainstorm names with vibe constraints" \
  --constraints "lowercase only, max 3 words, no abbreviations, evoke craft/precision"
```

**Inferred mode:** `ideation` (intent contains "ideation", "brainstorm", "names", "vibe")

**Generated prompt:**

---PROMPT-START---
You are operating in **volon** in **Orchestrator Mode**.

**Intent:** run ideation to brainstorm names with vibe constraints

**Date:** 2026-02-22

This is an **ideation session**. Your goal is to produce a structured ideation artifact.
Do NOT write application code or modify non-artifact source files during this session.

Do not rely on prior chat context. Repo artifacts are the only truth:
- `volon.yaml`, `.volon/pcc/`, `.volon/bootstrap.md`, `artifacts/`

---

## Rules

- You are the **single writer** for the output artifact.
- Sub-agents: disabled. This session is the single writer.
- Do not modify application source code during this session.
- Do not create TASK files unless explicitly instructed.
- Keep ideas grounded in the repo's actual context (read PCC before generating).

---

## Run

### 1) Preflight

1. Run: `cat volon.yaml 2>/dev/null || echo "NO_CONFIG"`
2. Read `.volon/pcc/` (scan `global/` for project goals and constraints).
3. Scan `artifacts/ideas/` for existing related ideation (skip duplicates).
4. Run: `git branch --show-current`

### 2) Derive slug

Slug: `names-vibe-constraints`
Artifact path: `artifacts/ideas/names-vibe-constraints-2026-02-22.md`

Idempotency check: if artifact exists, skip to Finalize.

### 3) Generate ideas

Generate names aligned with: **run ideation to brainstorm names with vibe constraints**

For each name, include: title, one-sentence description, rationale, risks/trade-offs.
Aim for 5–15 concrete names before filtering. Group by theme if natural.

Write to `artifacts/ideas/names-vibe-constraints-2026-02-22.md`.

### 4) Finalize

1. Run `/bootstrap-update`.
2. Commit: `iter <N>: ideation — names-vibe-constraints`

---

## Constraints

lowercase only, max 3 words, no abbreviations, evoke craft/precision

---

## Expected Deliverables

`artifacts/ideas/names-vibe-constraints-2026-02-22.md` with 5–15 names, bootstrap updated, commit.

---

## Guardrails

- No application code changes during this session.
- No TASK file creation unless explicitly asked.
- Artifact output scope: `artifacts/ideas/` only.
- **Single writer**: only this session writes artifacts/bootstrap.
- **Sub-agents: disabled. This session is the single writer.**
- Ideas must be grounded in repo context (PCC).

---

End with `DONE`.
---PROMPT-END---

---

## Golden 4 — Planning: PRD + spec for new workflow

**Command:**
```
/volon-prompt "create a PRD and spec for a new sprint-based workflow" \
  --mode planning \
  --deliverables "requirements.md, prd.md, spec.md, backlog tasks"
```

**Inferred mode:** `planning` (explicitly set via `--mode`)

**Generated prompt:**

---PROMPT-START---
You are operating in **volon** in **Orchestrator Mode**.

**Intent:** create a PRD and spec for a new sprint-based workflow

**Date:** 2026-02-22

This is a **planning session**. Your goal is to produce structured planning artifacts.
Do NOT implement features or modify application source code during this session.

Do not rely on prior chat context. Repo artifacts are the only truth:
- `volon.yaml`, `.volon/pcc/`, `.volon/bootstrap.md`, `artifacts/`

---

## Rules

- You are the **single writer** for all artifacts.
- Sub-agents: disabled. This session is the single writer.
- Do not modify application source code during this session.
- Prefer editing existing artifacts over creating new ones (check first).
- All artifact frontmatter must use Volon schema (see `docs/03_workflow-contracts.md`).

---

## Run

### 1) Preflight

1. Run: `cat volon.yaml 2>/dev/null || echo "NO_CONFIG"`
2. Read `.volon/pcc/` (global context — goals, architecture, constraints).
3. Scan `artifacts/` for existing related artifacts — skip any phase whose artifact exists.
4. Run: `git branch --show-current`

Derive slug: `sprint-workflow`

### 2) Produce artifacts (phased)

Execute each phase in order. Skip a phase if its artifact already exists.

- **Phase A — Requirements:** `artifacts/requirements/sprint-workflow-2026-02-22.md`
- **Phase B — PRD:** `artifacts/prd/sprint-workflow-2026-02-22.md` (requires Phase A)
- **Phase C — Spec:** `artifacts/spec/sprint-workflow-2026-02-22.md` (requires Phase B)
- **Phase D — Plan:** `artifacts/plan/sprint-workflow-2026-02-22.md` (requires Phase C)
- **Phase E — Backlog tasks:** `.volon/tasks/TASK-*.md` (requires Phase D, if `track_tasks: true`)

### 3) Finalize

1. Run `/bootstrap-update`.
2. Commit: `iter <N>: planning — sprint-workflow`

---

## Constraints

(none — use Volon defaults)

---

## Expected Deliverables

requirements.md, prd.md, spec.md, backlog tasks

---

## Guardrails

- No application code changes during this session.
- All artifacts must use Volon frontmatter schema.
- Do not create tasks for phases not yet completed.
- **Single writer**: only this session writes artifacts/tasks/bootstrap.
- **Sub-agents: disabled. This session is the single writer.**
- Phase skipping is idempotent — never overwrite an existing artifact.

---

End with `DONE`.
---PROMPT-END---

---

## Golden 5 — Patch: apply a diff safely

**Command:**
```
/volon-prompt "apply the patch from review-fixes.patch and verify no data loss"
```

**Inferred mode:** `patch` (intent contains "apply" and "patch")

**Generated prompt:**

---PROMPT-START---
You are operating in **volon** in **Orchestrator Mode**.

**Intent:** apply the patch from review-fixes.patch and verify no data loss

**Date:** 2026-02-22

This is a **patch application session**. Safely apply a patch to the repository,
verifying integrity at every step. If any verification step fails, stop and report.

Do not rely on prior chat context. Repo artifacts are the only truth:
- `volon.yaml`, `.volon/bootstrap.md`, `git status`

---

## Rules

- You are the **single writer**. Only you apply changes.
- Sub-agents: disabled. This session is the single writer.
- **No destructive operations without explicit user confirmation (`confirm: yes`):**
  - `git reset --hard`
  - `rm -rf`
  - Overwriting files outside the declared patch scope
- Stage specific files only — never `git add -A` without reviewing the full diff.
- Do NOT auto-commit on verification failure. Stop and report.
- If the repo is not clean before applying: **stop and report**.

---

## Run

### 1) Preflight

1. Run: `cat volon.yaml 2>/dev/null || echo "NO_CONFIG"`
2. Run: `git status --short` — if dirty: STOP and report.
3. Run: `git branch --show-current`
4. Patch file: `review-fixes.patch`

Emit: `[patch] preflight OK — branch: <branch>, repo clean`

### 2) Verify patch integrity

Run: `git apply --stat review-fixes.patch`
Confirm files are within expected scope.

Emit: `[patch] integrity check — <N> files in scope`

### 3) Dry run

Run: `git apply --check review-fixes.patch`
If fails: output error and STOP.

Emit: `[patch] dry run OK`

### 4) Apply

Run: `git apply review-fixes.patch`
Run: `git diff --stat` to confirm changes.

### 5) Verify

1. Run lint/test if available (check `volon.yaml` quality config).
2. Confirm no unexpected files in `git diff --name-only`.
3. If verification passes: emit `[patch] verification OK`
   If fails: emit `[patch] FAILED — <reason>` and STOP.

### 6) Log + Commit

1. Write run log at `.volon/logs/run-2026-02-22.md`.
2. Stage specific files: `git add <specific files>`
3. Commit: `patch: apply the patch from review-fixes.patch and verify no data loss`
4. Run `/bootstrap-update`.

---

## Constraints

(none — use Volon defaults)

---

## Expected Deliverables

Patch applied and verified, run log written, commit, bootstrap updated.

---

## Guardrails

- Repo must be clean before patch application. Stop if dirty.
- Dry run before apply. Stop on dry-run failure.
- Patch scope must be confirmed if unexpected files appear.
- No auto-commit on failure.
- No destructive operations without `confirm: yes`.
- **Single writer**: only this session applies changes.
- **Sub-agents: disabled. This session is the single writer.**

---

End with `DONE`.
---PROMPT-END---
