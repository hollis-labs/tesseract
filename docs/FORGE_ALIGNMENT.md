# Volon Feature Alignment to System Invariants

This document maps existing Volon concepts to the system invariants they uphold.

---

## Project Context Cache (PCC)

**Supports:**
- Invariant 2 — Canonical state is externalized
- Invariant 5 — Context is intentional
- Invariant 6 — Outcomes are observable

**Notes:**
- PCC converts conversational context into infrastructure.
- Git-backed PCC enables replay, diffing, and regression detection.
- PCC amortizes token cost over time.

---

## Tasks

**Supports:**
- Invariant 2 — Externalized state
- Invariant 6 — Observability
- Delegation spectrum boundaries

**Notes:**
- Tasks are the primary durability boundary.
- Tasks allow pause/resume without context loss.
- Tasks prevent conversational drift from becoming state.

---

## Commands

**Supports:**
- Invariant 1 — Harness owns execution
- Invariant 4 — Write authority constraints

**Notes:**
- Commands are the safest place to encode invariants.
- They define entrypoints with explicit intent.
- Commands should be idempotent where possible.

---

## Tools

**Supports:**
- Invariant 3 — Deterministic, bounded execution
- Invariant 6 — Auditable outcomes

**Notes:**
- Tools are the only mechanism that touches reality.
- Volon correctly treats tools as first-class and guarded.

---

## Agents (Orchestrator / Worker)

**Supports:**
- Invariant 1 — Execution control
- Invariant 4 — Write authority constraints
- Delegation spectrum

**Notes:**
- Workers default to read-only.
- Orchestrator is the sole writer.
- Agents isolate perspective, not authority.

---

## Workflows

**Supports (when used correctly):**
- Invariant 1 — Execution control
- Invariant 4 — Write scope
- Invariant 6 — Checkpoints and verification

**Anti-pattern warning:**
- Workflows that only add narrative guidance
  without enforcing artifacts or gates
  violate Volon’s design intent.

**Rule of thumb:**
- A workflow must either:
  - enforce a constraint, or
  - produce a durable checkpoint.

---

## Logging & Run Records

**Supports:**
- Invariant 6 — Observability
- Invariant 2 — Externalized state

**Notes:**
- Logs are not debugging aids; they are system state.
- Silent failure is treated as a bug.

---

## Agent Envelopes

Agent Envelopes are Volon’s execution boundary for agentic behavior.

They:
- declare permissions explicitly
- externalize acceptance criteria
- eliminate prompt-based guardrails
- support pause/resume and replay

### Workflow Relationship

- Workflows define *process*
- Envelopes define *execution*
- Every agent-invoking workflow step must reference one envelope

### Anti-patterns

- Multiple envelopes per step
- Envelopes that only add narrative text
- Agents writing outside envelope scope

---

## Overall Assessment

Volon is internally consistent with its invariants.

Most perceived “complexity” (workflows, PCC, tasks) exists to:
- externalize state
- reduce token entropy
- preserve replayability
- make delegation explicit

This complexity is structural, not incidental.
