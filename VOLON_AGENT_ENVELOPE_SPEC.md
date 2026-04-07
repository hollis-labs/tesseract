# Volon Agent Envelope Specification (v0.1)

## Purpose

An **Agent Envelope** is Volon’s unit of bounded delegation.

It defines:
- what an agent is allowed to do
- what it must produce
- how Volon verifies and accepts results

Envelopes enable **partial autonomy without surrendering system control**.

---

## Core Principles

- Envelopes are enforced by the harness, not prompts
- Agents never own canonical state
- Determinism exists at envelope boundaries
- Autonomy is explicit and revocable

---

## Roles

- **Orchestrator**
  - Owns execution loop
  - Applies writes
  - Accepts or rejects outputs

- **Worker**
  - Produces analysis, plans, diffs, reports
  - Read-only by default
  - Never mutates canonical state directly

---

## Envelope Lifecycle

1. Volon resolves envelope + inputs
2. Volon constructs scoped context
3. Agent executes within constraints
4. Agent produces outputs
5. Volon runs verification gates
6. Volon accepts, revises, or escalates

---

## Envelope Format

```yaml
apiVersion: forge.agent-envelope/v0.1
kind: AgentEnvelope
metadata:
  name: example-envelope
spec:
  role: worker
  objective: "Single, testable goal"
  inputs: {}
  outputs: {}
  permissions: {}
  verification: {}
