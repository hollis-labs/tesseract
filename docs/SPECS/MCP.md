# MCP Adapter Spec

Status: **implemented** (commit `426691d`, sprint-post-mvp-mcp)

## Getting started

### Prerequisites

- `contextd` binary built (`go build -o contextd ./cmd/contextd`)
- A capability token with required scopes (see below)

### Minimal `.mcp.json`

```json
{
  "mcpServers": {
    "context": {
      "command": "/path/to/contextd",
      "args": ["mcp", "--token", "<your-token>"],
      "env": {
        "CONTEXTD_ROOT": "/Users/<you>/.context"
      }
    }
  }
}
```

The `--token` value must be a token created with:
```bash
contextd context token create \
  --name my-agent \
  --scopes write,promote.request \
  --namespaces "app/my-agent/*" \
  --ttl 8760h
```

Read tools (`tesseract_get`, `tesseract_history`, `context_view`, `context_packet`) work
without a token under `domain: "context"`; the `memory` and `knowledge` domains check `memory:read`. Write tools (`context_write`, `context_promote_request`) require a token
with the matching scope and a namespace glob that covers the target namespace.

### Available tools

| Tool | Auth required | Description |
|---|---|---|
| `tesseract_get` | none under `domain: "context"` | Read latest record revision |
| `tesseract_history` | none under `domain: "context"` | Read revision history (newest first) |
| `context_view` | none | Evaluate a namespace/selector query |
| `context_packet` | none | Assemble a budget-bounded context bundle |
| `context_write` | `write` scope | Append a record revision |
| `context_promote_request` | `promote.request` scope | Request promotion to user/* |

See [`docs/AGENT-SETUP.md`](../AGENT-SETUP.md) for full tool schemas and agent workflow.

---

## Purpose
Define the MCP adapter as a thin transport mapping to existing Context Memory Service contracts.

## Scope
- Provide MCP-accessible operations that map directly to API/CLI behavior.
- Preserve namespace ownership, deterministic ordering, and audit semantics.
- Avoid introducing parallel state models or MCP-specific business rules.

## Non-goals
- No processor semantics in view operations (selectors only).
- No hidden data synthesis/merging beyond existing core contracts.
- No alternate persistence path outside the existing store/index model.

## Contract mapping
- Namespace policy:
  - maps to `POST /v1/namespaces/register` and CLI `context namespace register`
- Write/read:
  - maps to `POST /v1/context/write`, `GET /v1/context/head`, `GET /v1/context/history`
- Promote:
  - maps to `POST /v1/context/promote` with explicit user actor constraints
- View evaluation:
  - maps to `POST /v1/views/evaluate` / CLI `context view`

## Determinism constraints
- Result ordering must match API/CLI determinism guarantees.
- Selector fallback ordering remains `(namespace,key,revision)`.
- Limit truncation occurs after deterministic sort.
- MCP adapter must remain side-effect free for read/view operations.

## Policy and auth notes
- Mutating operations follow server auth policy exactly as API path.
- Namespace ownership policy remains authoritative.
- Protected `user/*` updates continue to require explicit user promotion semantics.

## Threat assumptions and trust boundaries

Deployment assumptions:
- Default target is local-first usage on a trusted developer machine.
- Shared-host or multi-user environments are treated as higher-risk and should enable stricter auth modes.

Trust boundary model:
- MCP client process boundary: requests may originate from tools with varying trust levels.
- Adapter boundary: must enforce core API/CLI policy semantics; it is not an authorization bypass.
- Core service boundary: remains the source of truth for auth, namespace ownership, and deterministic validation.

Risk notes by environment:
- Local-only host:
  - Primary risk is accidental overexposure of mutating tools.
  - Mitigation: keep write/promote tools explicit and auditable.
- Shared host / team workstation:
  - Elevated risk of unauthorized read/write access through exposed MCP surfaces.
  - Mitigation: prefer gated read mode or strict-auth-all mode, restrict mutating capabilities to trusted operators.

Alignment requirements:
- Follow API auth roadmap (`mvp-open-read`, `optional-gated-read`, `strict-auth-all`) from `docs/SPECS/API.md`.
- Preserve actor/namespace policy guarantees for all mutating operations.
- Keep selector operations evaluator-only; no hidden processor/synthesis behavior.

## Phased capability model

| Phase | Allowed operations | Auth/policy expectations | Risk controls |
|---|---|---|---|
| `phase-1-read-view` | `head`, `history`, `views/evaluate` | Follows API read/view mode (`mvp-open-read` by default, optional gated modes later). | Keep adapter side-effect free for read/view and enforce deterministic ordering parity with API/CLI. |
| `phase-2-namespace-admin` | `namespaces/register`, `namespaces/get` | Mutating calls require auth per server mode and must respect ownership schema validation. | Restrict to explicit admin/tool contexts; audit every mutation path. |
| `phase-3-write-promote` | `context/write`, `context/promote` | Write requires namespace ownership grants; promote requires user actor semantics and protected `user/*` policy. | Enforce actor/namespace matrix, deterministic error envelopes, and explicit promote approval flow. |

### Phase-to-tool exposure matrix

| Phase | Tool surface | Default posture | Enablement rule |
|---|---|---|---|
| `phase-1-read-view` | `head`, `history`, `views/evaluate` | Default-enabled | Keep enabled by default while preserving deterministic read/view parity with API/CLI. |
| `phase-2-namespace-admin` | `namespaces/get` | Explicitly-enabled | Enable for operators who need policy inspection in shared/team environments. |
| `phase-2-namespace-admin` | `namespaces/register` | Explicitly-enabled (mutating) | Enable only for trusted admin identities after `phase-1 -> phase-2` gate criteria are met. |
| `phase-3-write-promote` | `context/write` | Explicitly-enabled (mutating) | Enable only after actor/namespace policy checks and mutate token flow verification pass. |
| `phase-3-write-promote` | `context/promote` | Explicitly-enabled (mutating) | Enable only when protected `user/*` promote semantics (`actor=user`) and audit visibility are verified. |

Exposure guidance:
- Keep mutating tool surfaces opt-in even after phase promotion; do not auto-enable in broad/shared operator contexts.
- Validate explicit enablement decisions against phase gate criteria and environment adoption recommendations before widening access.

Phase progression constraints:
- Each phase must be releasable without changing deterministic behavior in prior phases.
- Selector semantics remain evaluator-only in all phases (no processor behavior).
- Capability expansion requires corresponding API/CLI contract documentation updates.
- Selector capability discovery/negotiation follows `docs/SPECS/VIEWS.md#selector-capability-discovery-model`.

---

## Appendix: Operator runbook (phase gating, rollback, evidence)

The sections below are reference material for team-managed or production deployments.
For local developer usage, the getting-started section above is sufficient.

## Phase gate criteria

Minimum criteria before promoting from one MCP phase to the next:

| Gate transition | Auth readiness minimum | Policy check minimum | Observability readiness minimum |
|---|---|---|---|
| `phase-1-read-view` -> `phase-2-namespace-admin` | Mutating token enforcement validated end-to-end for namespace admin calls. | Namespace registration/get flows verified against API/CLI contract behavior. | Namespace mutation events are traceable in audit surfaces with operator attribution. |
| `phase-2-namespace-admin` -> `phase-3-write-promote` | Token propagation validated for `write` and `promote` tool paths in target environment mode. | Actor/namespace matrix checks pass, including protected `user/*` promote semantics (`actor=user`). | Write/promote outcomes and failures (`auth_required`, `policy_denied`, `validation_error`) are observable and actionable in operator runbooks. |
| Any phase promotion in team-managed environments | Auth mode aligned to environment guidance (`strict-auth-all` preferred for team-managed). | Preflight actor/policy checks completed for newly enabled mutate capabilities. | Rollout includes explicit rollback trigger and audit verification before widening access. |

Gate alignment notes:
- Apply these criteria together with the `Preflight checklist (team-managed / production-like)` before enabling new mutate capabilities.
- Phase sequencing should follow `Environment-based phase adoption recommendations` so auth posture and risk controls stay aligned.

## Phase promotion evidence checklist

Capture this evidence before approving a phase transition:
1. Auth validation proof:
   - Record token enforcement results for the target phase tool surfaces in active environment mode.
   - Include at least one successful authenticated flow and one expected `auth_required` failure case.
2. Policy verification proof:
   - Record actor/namespace policy checks for newly enabled mutating tools.
   - Include evidence that protected `user/*` promote semantics (`actor=user`) are enforced where applicable.
3. Observability proof:
   - Capture audit/trace evidence for representative success and failure paths.
   - Ensure `auth_required`, `policy_denied`, and `validation_error` outcomes are visible and actionable in operator workflows.
4. Rollout control proof:
   - Document rollback trigger and rollback owner for the promotion window.
   - Confirm phase-to-tool exposure decisions match explicit enablement posture in this spec.

Evidence handling notes:
- Store promotion evidence with release/change artifacts so approvals are reproducible.
- Treat missing evidence in any category as a no-go for phase promotion.

## Phase rollback-readiness checklist

Complete this before enabling new mutating MCP capabilities:
1. Rollback trigger:
   - Define explicit rollback conditions (for example: repeated `auth_required` failures under validated token flow, policy invariant violations, or audit visibility gaps).
2. Rollback owner:
   - Assign a named operator/team responsible for executing rollback and validating post-rollback state.
3. Verification scope:
   - Identify the minimum verification set to run after rollback (tool exposure state, auth mode expectations, policy enforcement, audit visibility).
4. Communication expectations:
   - Define who receives rollback notices and where status updates are posted during the rollback window.
5. Post-rollback confirmation:
   - Confirm system returns to previously approved phase behavior and update promotion evidence artifacts with rollback outcome notes.

Rollback alignment notes:
- Keep rollback checklist decisions consistent with `Phase gate criteria` and `Phase promotion evidence checklist`.
- Do not advance phase rollout when rollback trigger/owner/scope are undefined.

## Phase promotion sign-off template

Use this template when approving a phase transition:
- `Phase transition:` `<from_phase> -> <to_phase>`
- `Approver:` `<name/team>`
- `Date:` `YYYY-MM-DD`
- `Gate criteria status:` `pass|fail` (reference `Phase gate criteria`)
- `Evidence checklist status:` `pass|fail` (reference `Phase promotion evidence checklist`)
- `Rollback readiness status:` `pass|fail` (reference `Phase rollback-readiness checklist`)
- `Tool exposure decision:` `<enabled surfaces + scope>`
- `Approval note:` `<brief rationale + constraints>`

Sign-off rule:
- Any `fail` status blocks promotion approval until remediated and re-verified.

## Post-promotion monitoring note

Immediately after phase promotion approval, monitor:
1. Auth failure signals:
   - Track `auth_required` rates for newly enabled tool surfaces to detect token flow regressions.
2. Policy denial signals:
   - Watch `policy_denied` outcomes for unexpected actor/namespace mismatches in promoted paths.
3. Observability integrity:
   - Confirm audit/trace visibility remains complete for success and failure paths.
4. Escalation threshold:
   - If auth/policy failures exceed expected baseline, trigger rollback-readiness process and pause further rollout expansion.

Monitoring alignment:
- Use this note with `Phase gate criteria`, `Phase promotion evidence checklist`, and `Phase promotion sign-off template`.

## Promotion freeze-condition note

Freeze further phase rollout when any of these conditions are met:
1. Auth threshold breach:
   - Sustained `auth_required` failures above expected rollout baseline for newly enabled surfaces.
2. Policy threshold breach:
   - Repeated `policy_denied` outcomes indicating unresolved actor/namespace policy mismatches.
3. Observability threshold breach:
   - Missing or incomplete audit/trace visibility for promoted success/failure paths.

Freeze action:
- Pause promotion expansion and route through rollback-readiness + remediation flow before resuming rollout.

Freeze alignment:
- Apply with `Post-promotion monitoring note` and `Phase rollback-readiness checklist`.

## Rollback validation snapshot note

Immediately after rollback execution, capture:
1. Auth-state snapshot:
   - Post-rollback `auth_required` behavior for affected tool surfaces matches pre-promotion expectations.
2. Policy-state snapshot:
   - Post-rollback `policy_denied` behavior confirms actor/namespace protections are restored to expected baseline.
3. Observability snapshot:
   - Audit/trace records show rollback actions and subsequent validation checks with complete visibility.
4. Snapshot record:
   - Store captured results with rollback timestamp and validator owner for reproducibility.

Snapshot alignment:
- Use with `Phase rollback-readiness checklist` and `Promotion freeze-condition note` before considering rollout resume.

## Rollback resume gate note

Resume rollout only when all gates pass:
1. Freeze-condition remediation:
   - Auth/policy/observability threshold breaches that triggered freeze are resolved and verified.
2. Rollback validation snapshot completeness:
   - Snapshot evidence is captured and confirms expected post-rollback baseline behavior.
3. Re-approval gate:
   - Promotion sign-off is re-run with updated evidence before any rollout expansion resumes.

Resume alignment:
- Apply with `Promotion freeze-condition note`, `Rollback validation snapshot note`, and `Phase promotion sign-off template`.

## Rollback communication closure note

After freeze/rollback/resume decisions, publish a closure update including:
1. Final decision state:
   - `frozen`, `rolled back`, or `resume-approved`.
2. Evidence reference:
   - Pointer to rollback snapshot and/or resume-gate verification artifacts.
3. Owner + next checkpoint:
   - Responsible owner and next planned review/checkpoint time.

Closure alignment:
- Keep closure messaging consistent with `Promotion freeze-condition note`, `Rollback resume gate note`, and sign-off records.

## Rollback verification handoff note

Before handing rollback validation to the next responder, record:
1. Snapshot handoff package:
   - Link the rollback validation snapshot, freeze-condition status, and any open verification gaps.
2. Handoff owner + timestamp:
   - Record outgoing owner, incoming owner, and handoff timestamp in the same evidence thread.
3. Resume/closure linkage:
   - Identify whether next action targets resume-gate re-approval or closure communication, with direct links to required artifacts.

Handoff alignment:
- Keep handoff records aligned with `Rollback validation snapshot note`, `Rollback resume gate note`, and `Rollback communication closure note`.

## Rollback evidence retention note

After rollback handling is complete, retain verification artifacts with:
1. Retention window:
   - Minimum retention period aligned to current release/support window for the affected phase transition.
2. Artifact scope:
   - Preserve rollback snapshot evidence, handoff records, and closure communication references together.
3. Ownership:
   - Assign a responsible owner for retention integrity and eventual archival/cleanup decision.

Retention alignment:
- Keep retention expectations aligned with sign-off evidence handling and post-promotion monitoring needs.

## Rollback evidence archival note

After retention windows are satisfied, archive rollback evidence with:
1. Archive trigger:
   - Archive when retention window closes and no active freeze/resume investigations remain.
2. Archive bundle:
   - Archive rollback snapshot, handoff records, closure communication, and retention metadata together.
3. Archive accountability:
   - Record archive owner and archive timestamp in the same evidence trail for audit continuity.

Archival alignment:
- Keep archival handling aligned with retention, handoff, and closure guidance to preserve end-to-end evidence lineage.

## Rollback evidence retrieval note

When archived rollback evidence must be reviewed:
1. Retrieval pointer:
   - Record where archived evidence is stored and the retrieval identifier/path.
2. Retrieval scope:
   - Retrieve snapshot, handoff, closure, and retention/archival metadata as a single evidence package.
3. Retrieval accountability:
   - Record retriever identity and retrieval timestamp for audit follow-up.

Retrieval alignment:
- Keep retrieval guidance aligned with retention and archival sections to maintain evidence continuity.

## Rollback evidence reconciliation note

When retained and retrieved evidence differ, reconcile with:
1. Mismatch record:
   - Record which artifact fields differ between retained and retrieved bundles.
2. Reconciliation action:
   - Resolve mismatch by validating canonical evidence trail and updating incorrect references.
3. Accountability:
   - Record reconciler owner and reconciliation timestamp in the evidence log.

Reconciliation alignment:
- Keep reconciliation outcomes linked to retrieval, archival, and closure evidence references.

## Rollback evidence discrepancy escalation note

If reconciliation cannot resolve evidence discrepancies:
1. Escalation trigger:
   - Escalate unresolved mismatches after reconciliation attempts in the current review cycle.
2. Escalation record:
   - Record discrepancy summary, affected evidence artifacts, and assigned escalation owner.
3. Outcome linkage:
   - Link escalation outcome to updated reconciliation/closure evidence before final sign-off.

Escalation alignment:
- Keep discrepancy escalation aligned with reconciliation, retrieval, and closure evidence guidance.

## Rollback evidence recovery note

After discrepancy escalations are resolved, recover evidence flow with:
1. Recovery sequence:
   - Re-run reconciliation checks and refresh evidence references across retained/retrieved bundles.
2. Recovery record:
   - Record recovered artifacts, unresolved gaps, and recovery owner/timestamp.
3. Closure readiness:
   - Confirm closure/sign-off evidence references use the recovered evidence set.

Recovery alignment:
- Keep recovery outcomes linked to discrepancy escalation, reconciliation, and closure evidence sections.

## Rollback evidence prevention note

Reduce future evidence discrepancy escalations with proactive checks:
1. Prevention checks:
   - Run periodic completeness checks across reconciliation, discrepancy escalation, and recovery records.
2. Drift watch:
   - Flag rollback streams with recurring discrepancies for targeted maintenance updates.
3. Precedence guard:
   - Keep canonical evidence references explicit in preventive updates and sign-off artifacts.

Prevention alignment:
- Keep prevention controls aligned with recovery, reconciliation, and discrepancy escalation guidance.

## Rollback evidence continuous-improvement note

Track recurring rollback evidence issues with a lightweight loop:
1. Recurring issue log:
   - Record repeated discrepancy patterns across rollback evidence streams.
2. Linked improvements:
   - Attach one concrete runbook/process/documentation improvement per recurring pattern.
3. Follow-up verification:
   - Re-check affected evidence paths in the next maintenance pass and record results.

Improvement alignment:
- Keep continuous-improvement outcomes linked to prevention, recovery, and discrepancy escalation evidence flows.

## Operator enablement checklist

Phase 1 (`read-view`) checklist:
1. Confirm intended auth mode in API roadmap (`mvp-open-read` or gated variants).
2. Expose only read/view MCP tools (`head`, `history`, `views/evaluate`).
3. Verify deterministic ordering parity with API/CLI for identical selector inputs.
4. Validate unknown selector fields fail with `validation_error`.

Phase 2 (`namespace-admin`) checklist:
1. Enable mutating namespace tools only for trusted operators.
2. Verify bearer-token enforcement for mutating calls.
3. Validate namespace policy registration/get flows mirror CLI/API behavior.
4. Confirm admin actions are auditable.

Phase 3 (`write-promote`) checklist:
1. Enable `write`/`promote` tools only after policy matrix review.
2. Verify actor constraints (`promote` requires user actor).
3. Validate protected `user/*` behavior and deterministic error envelopes.
4. Run end-to-end register/write/promote/audit verification flow before rollout.

## Troubleshooting quick-reference

| Symptom | Likely cause | Operator action |
|---|---|---|
| Read/view MCP calls fail with `auth_required` unexpectedly | Server running in `optional-gated-read` or `strict-auth-all` mode without token injection | Provide bearer token in MCP call path and verify active auth mode in API roadmap section. |
| Selector evaluation returns `validation_error` for unknown field | Client sent selector field not in supported capability set | Remove unsupported field, align with `docs/SPECS/VIEWS.md`, and confirm capability discovery guidance. |
| Write or promote returns `policy_denied` | Actor/namespace combination violates ownership matrix | Re-check actor identity and namespace policy; confirm promote uses `actor=user` for protected `user/*` targets. |
| Promote succeeds in CLI but fails in MCP | MCP tool mapping omitted required promote fields or actor propagation | Compare MCP payload mapping to API/CLI contract fields and ensure full parity for `from_*`/`to_*`/actor parameters. |

## Error-to-action matrix

| Error class | Typical MCP phase context | First response action |
|---|---|---|
| `auth_required` | Any phase when endpoint/mode requires token | Verify auth mode expectations, then confirm bearer token propagation and token lifecycle status. |
| `policy_denied` | `phase-2`/`phase-3` mutate paths | Re-check actor-to-namespace mapping and policy grants, especially protected `user/*` promote constraints. |
| `validation_error` | Selector evaluation or malformed mutate payload | Normalize payload/selector shape to documented schema and remove unsupported fields. |

## Environment-based phase adoption recommendations

| Environment | Recommended phase adoption | Suggested auth mode | Risk posture guidance |
|---|---|---|---|
| Single-user local workstation | `phase-1` -> `phase-2` -> `phase-3` as needed | `mvp-open-read` acceptable; mutate paths still token-gated | Lower risk; prioritize operator ergonomics while keeping mutate actions explicit and audited. |
| Shared workstation | Start with `phase-1`; adopt `phase-2/3` only for trusted operators | `optional-gated-read` minimum | Medium risk; reduce accidental data exposure and require explicit token flow for shared contexts. |
| Team-managed environment | Start with constrained `phase-1`, then staged rollout to `phase-2/3` with approvals | `strict-auth-all` preferred | Higher risk; enforce strong auth posture, ownership policy checks, and auditability before enabling mutating tools. |

## Preflight checklist (team-managed / production-like)

Before enabling `phase-2` or `phase-3` MCP capabilities:
1. Auth mode confirmation:
   - Verify active mode is consistent with team policy (`strict-auth-all` recommended).
2. Token flow validation:
   - Confirm bearer token propagation from MCP client through adapter boundary to API calls.
   - Validate token lifecycle status (active/non-revoked) for operational identities.
3. Actor/policy checks:
   - Validate actor mapping for mutate tools.
   - Confirm promote paths enforce `actor=user` for protected `user/*` transitions.
4. Audit visibility:
   - Verify mutate actions appear in audit query surfaces and can be traced by operator.
5. Rollout guard:
   - Enable capabilities phase-by-phase with rollback path if auth/policy invariants fail.

## References
- Agent setup guide: [`docs/AGENT-SETUP.md`](../AGENT-SETUP.md)
- Quick start: [`docs/QUICKSTART.md`](../QUICKSTART.md)
- API spec: [`docs/SPECS/API.md`](./API.md)
- CLI spec: [`docs/SPECS/CLI.md`](./CLI.md)
- Views spec: [`docs/SPECS/VIEWS.md`](./VIEWS.md)
- Architecture: [`docs/ARCHITECTURE.md`](../ARCHITECTURE.md)
- ADR-0002 pivot: [`docs/DECISIONS/ADR-0002-generalized-context-registry.md`](../DECISIONS/ADR-0002-generalized-context-registry.md)
