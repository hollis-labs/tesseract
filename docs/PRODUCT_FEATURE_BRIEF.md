# Context Memory Service — Product Feature Brief

Status: design handoff draft

## What this app is
Context Memory Service is a local-first context registry and working-memory system for humans and agents.

It provides:
- deterministic context storage/retrieval
- strict namespace ownership boundaries
- auditable write/promote lifecycle
- shared contracts across API, CLI, and MCP

## Who it is for
- Human operators:
  - engineers, maintainers, and power users managing canonical and app context
- Agent/app clients:
  - local tools/agents that write to app namespaces and retrieve deterministic views

## What problem it solves
- Keeps user context and app/agent context in one system with clear ownership boundaries.
- Prevents hidden mutation/synthesis in retrieval paths.
- Makes context retrieval deterministic and reproducible.
- Preserves provenance and revision history for every write.

## Core feature list
1. Namespaced context model
- `user/*` for canonical user-owned context.
- `app/<client-id>/*` for app/agent-owned context.
- explicit policy boundaries between actors and namespaces.

2. Append-only revisioned records
- every write creates a new revision.
- no in-place payload mutation.
- deterministic head per `(namespace,key)`.

3. Deterministic retrieval
- `head`: current revision.
- `history`: revision timeline.
- `views/evaluate`: selector-driven deterministic result sets.

4. Promote workflow for protected context
- app writes remain in app namespaces.
- user-owned context updates use explicit promote semantics.

5. Policy and schema enforcement
- actor/namespace ownership checks.
- optional schema contract checks (for example `required_keys`).

6. Auth model (current + roadmap)
- bearer-token auth for mutating endpoints.
- read/view auth modes documented (`mvp-open-read`, `optional-gated-read`, `strict-auth-all` roadmap).

7. Audit and operations posture
- audit-friendly metadata and deterministic error envelopes.
- operational guidance for incident handling, evidence lifecycle, and escalation.

8. Multi-interface parity
- HTTP API and CLI are implemented against shared contracts.
- MCP adapter follows same contract semantics (phased model).

## How users will use it
1. Human operator workflow
- register or inspect namespace policy
- write/update app context
- evaluate deterministic views for active work context
- promote approved app context into `user/*` when needed
- inspect history/audit trails for verification

2. Agent workflow
- write task/session outputs into `app/<client-id>/*`
- query `head/history/view` deterministically
- avoid direct writes to `user/*`
- submit promote flow when user-owned context needs updates

3. Team/shared-environment workflow
- enforce stronger auth mode
- use policy checks and deterministic envelopes for runbook automation
- trace outcomes via audit/revision evidence

## Suggested GUI feature modules (for design mockups)
1. Context Explorer
- namespace tree
- key list + revision timeline
- head vs history compare

2. View Builder
- selector editor (`namespaces`, `keys`, `revision_scope`, `order`, `limit`)
- deterministic results table
- saved selector presets

3. Write/Promote Console
- write form (actor, client_id, namespace, key, payload)
- promote flow with explicit source/target and actor validation
- policy feedback and validation errors

4. Policy Manager
- namespace policy registration/inspection
- ownership matrix visibility
- schema contract preview (`required_keys`)

5. Audit & Operations
- operation events feed (write/promote)
- incident evidence lifecycle panel (retain/archive/retrieve/reconcile/escalate)
- auth/policy error diagnostics

6. Environment & Auth
- active auth mode indicator
- token status hints for mutating paths
- environment targeting/base URL visibility

## Primary UX constraints for design
- deterministic by default:
  - UI should always show ordering and scope controls explicitly.
- explicit ownership:
  - UI should make actor + namespace authorization state visible before mutation.
- no hidden side effects:
  - read/view screens must be clearly read-only.
- provenance-first:
  - timestamps, actor identity, revision IDs should be first-class in detail views.

## Current implementation reality (important for mockups)
- Backend/API/CLI contracts are active and tested.
- GUI is not yet implemented as a full product surface.
- `user-docs-mini-site/` is a static documentation site, not the operational app UI.
