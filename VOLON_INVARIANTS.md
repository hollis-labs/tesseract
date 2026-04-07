# Volon System Invariants

## Purpose

Volon is a **harness for AI-assisted work**.

It exists to enable **agentic behavior inside bounded, deterministic systems** while preserving:
- correctness
- replayability
- controlled delegation
- long-term maintainability

Volon is intentionally *not* a fully autonomous agent runtime.

---

## Hard Invariants (Enforced by Volon)

These rules are **non-negotiable**.  
Features that violate them are misaligned with Volon.

---

### 1. Execution Control Belongs to the Harness

- Volon owns the orchestration loop.
- Models do not decide what happens next.
- Tool execution is always mediated by Volon.

**Rule:**  
Models may propose actions; Volon disposes.

---

### 2. Canonical State Is Externalized

- Authoritative state lives outside the model:
  - files
  - task records
  - PCC
  - logs
- Conversational memory is never canonical.

**Rule:**  
Restarting a session must not destroy system knowledge.

---

### 3. Tools Are Deterministic and Bounded

- Tools are explicit, named capabilities.
- Inputs and outputs are structured.
- Execution semantics are stable and auditable.

**Rule:**  
A tool call must be reproducible without model context.

---

### 4. Write Authority Is Constrained

- Not all agents may write.
- Write scope is explicit (paths, artifacts).
- Read-only agents are the default.

**Rule:**  
Accidental mutation is a system failure, not an agent mistake.

---

### 5. Context Is Intentional

- Context is selected, versioned, and scoped.
- PCC is authoritative for long-lived context.
- Prompts are not relied on to carry state.

**Rule:**  
Token usage is a design concern, not an accident.

---

### 6. Outcomes Must Be Observable

- Decisions, actions, and failures are logged.
- Silent behavior is a bug.
- Debugging must not depend on “what the model thought.”

**Rule:**  
Volon favors explainability over cleverness.

---

## Soft Concepts (Named, Not Enforced)

These abstractions exist to aid reasoning and composition.  
They are **not guarantees**.

---

### Prompt
- Atomic instruction text.
- No enforcement.
- May be rewritten, composed, or ignored.

---

### Skill
- Reusable cognitive pattern.
- Convention only.
- No execution authority.

---

### Command
- Named invocation entrypoint.
- Enforces shape and intent.
- Does not guarantee reasoning correctness.

---

### Agent
- Role + context boundary.
- Permissioned tool access.
- Does not own system logic.

---

### Workflow
- A **process contract**, not a tutorial.
- Defines:
  - required artifacts or structured outputs
  - sequencing expectations
  - role/write constraints
  - verification gates

**Rule:**  
If a workflow does not enforce an invariant or produce a checkpoint, it should not exist.

---

### Process
- Emergent pattern formed by workflows, commands, skills, and agents.
- Not enforced.
- May vary by project or team.

---

## Delegation Spectrum (Explicit)

Volon supports multiple delegation modes **by choice**:

- **Deterministic** — model advises only
- **Structured advisory** — model outputs plans/diffs
- **Bounded agentic** — model may call tools inside an envelope
- **Exploratory** — heuristic, non-canonical output

**Rule:**  
Delegation level must be explicit at the task or workflow boundary.

---

## Design Guardrails

When extending Volon:

- Do not move invariants into prompts.
- Do not let models accumulate implicit state.
- Do not add workflows that only add narrative text.
- Prefer fewer strong abstractions over many weak ones.
- If behavior cannot be replayed, label it exploratory.

---

## Summary

> Volon is not an agent.  
> Volon is the system that agents operate inside.
