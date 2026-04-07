# Views Spec

Status: draft (Task 6)

## Definition
A View is a deterministic selector over context records. It returns a stable ordered set of matching revisions and does not transform payload semantics.

## Non-goal
Views are not processors. They do not summarize, infer, merge, or mutate records.

## Selector structure
```json
{
  "namespaces": ["user/*", "app/editor/*"],
  "keys": ["goal", "priority", "summary"],
  "revision_scope": "head|all",
  "order": ["namespace", "key", "revision"],
  "limit": 100,
  "tags_any": ["urgent", "pinned"]
}
```

## Fields
- `namespaces`: optional glob-like namespace filters.
- `keys`: optional explicit key filter list.
- `revision_scope`:
  - `head`: return only current head per `(namespace,key)`.
  - `all`: return full history matches.
- `order`: explicit stable sort keys. Required for deterministic behavior. Allowed values: `namespace`, `key`, `revision`, `created_asc`, `created_desc`.
- `limit`: optional bounded result size.
- `tags_any`: optional list of tags — returns records that have at least one matching tag. Tags are written via `metadata.tags` on the write request and indexed in the `record_tags` table.

## Complexity guardrails (MVP)
- Max `namespaces` patterns: `32`
- Max `keys` entries: `128`
- `limit` behavior:
  - default when omitted/zero: `200`
  - maximum accepted: `500`
- `order` keys allowed: `namespace`, `key`, `revision`, `created_asc`, `created_desc` (no duplicates)

## Determinism rules
1. Selector parsing is pure and side-effect free.
2. Missing `order` defaults to `(namespace,key,revision)`.
3. Equivalent selector + identical store state => identical result ordering.
4. Limit truncation is applied after deterministic sort.

Determinism anti-patterns:
- Anti-pattern: relying on implicit result order when selector order keys are omitted in client expectations.
  - Recommendation: always reason against canonical fallback `(namespace,key,revision)` or provide explicit `order`.
- Anti-pattern: sending unbounded broad selectors in automation and assuming stable subset sampling.
  - Recommendation: set explicit `limit` and deterministic `order`, then paginate/iterate deterministically.
- Anti-pattern: mixing unsupported/unknown selector fields and treating failures as transient.
  - Recommendation: negotiate supported fields via documented capability model and treat `validation_error` as schema mismatch.

Normalization example:
```json
{
  "input_selector": {
    "namespaces": ["user/*"],
    "limit": 0
  },
  "normalized_selector": {
    "namespaces": ["user/*"],
    "revision_scope": "head",
    "order": ["namespace", "key", "revision"],
    "limit": 200
  }
}
```

Truncation example (deterministic):
```json
{
  "sorted_items": [
    {"namespace": "app/editor/session", "key": "goal", "revision": 1},
    {"namespace": "app/editor/session", "key": "summary", "revision": 1},
    {"namespace": "user/profile", "key": "goal", "revision": 3}
  ],
  "limit": 2,
  "returned_items": [
    {"namespace": "app/editor/session", "key": "goal", "revision": 1},
    {"namespace": "app/editor/session", "key": "summary", "revision": 1}
  ],
  "truncated": true
}
```

`revision_scope=all` selector example:
```json
{
  "selector": {
    "namespaces": ["app/editor/session"],
    "keys": ["summary"],
    "revision_scope": "all",
    "order": ["namespace", "key", "revision"],
    "limit": 50
  }
}
```

`revision_scope=all` result semantics:
- Includes every matching revision in deterministic revision order for each `(namespace,key)`.
- Does not collapse to heads; use `revision_scope=head` for current-head-only results.

## Example naming/style conventions

- Valid examples:
  - Use labels ending in `selector example` (for example: `` `revision_scope=all` selector example ``).
  - Show only supported fields unless demonstrating fallback behavior explicitly.
  - Include deterministic cues (`order`, `limit`, `revision_scope`) when behavior depends on them.
- Invalid examples:
  - Prefix with `Invalid ...` and include expected failure class (for example: `Invalid selector examples (400 validation_error)`).
  - Keep examples minimal and focused on one failing constraint per block.
- Formatting:
  - Prefer compact JSON blocks with stable key ordering for readability.
  - Place explanatory semantics immediately below the example when interpretation is non-obvious.

## Response envelope
- `items`: ordered record references (and payloads if requested)
- `evaluation_meta`:
  - `normalized_selector`
  - `sort_keys`
  - `matched_count`
  - `returned_count`
  - `truncated` (boolean)

## Validation rules
- Reject unknown selector fields.
- Reject non-deterministic order directives.
- Reject invalid namespace glob patterns.
- Reject negative limits.
- Reject limits above max bound.

Complexity rejection examples:
- Too many namespaces (max 32):
```json
{
  "selector": {
    "namespaces": ["user/*", "... more than 32 entries ..."],
    "order": ["namespace", "key", "revision"]
  }
}
```
- Too many keys (max 128):
```json
{
  "selector": {
    "keys": ["k1", "k2", "... more than 128 entries ..."],
    "order": ["namespace", "key", "revision"]
  }
}
```

## Selector extension and versioning policy

Current compatibility mode:
- Selector schema is strict by default.
- Unknown selector fields are rejected with `validation_error`.

Extension path for new fields:
1. Add field behind an explicit server capability gate.
2. Document normalization + determinism impact before enabling by default.
3. Keep default behavior stable for clients that only send existing fields.
4. Promote field to baseline only after API/CLI docs and contract tests are updated.

Versioning expectations:
- MVP operates under an implicit `v1` selector contract.
- Future breaking selector changes require an explicit version key or endpoint version bump.
- Additive, non-breaking fields may be introduced only when strict unknown-field behavior can still be preserved for non-opted-in clients.

Deprecation policy:
- Deprecated selector fields remain accepted for at least one documented release window.
- During deprecation, normalization must map deprecated inputs to canonical equivalents without changing result ordering semantics.
- Removal of deprecated fields is treated as a breaking change and requires explicit version transition notes.

Deprecation normalization examples:
- Deprecated field accepted during release window:
```json
{
  "input_selector": {
    "namespaces": ["user/*"],
    "sort": ["namespace", "key", "revision"],
    "revision_scope": "head"
  },
  "normalized_selector": {
    "namespaces": ["user/*"],
    "order": ["namespace", "key", "revision"],
    "revision_scope": "head",
    "limit": 200
  },
  "deprecation_note": "`sort` is deprecated and normalized to canonical `order`"
}
```
- Ordering remains stable after normalization:
```json
{
  "input_ordering_field": "sort",
  "normalized_ordering_field": "order",
  "ordering_guarantee": "result ordering remains `(namespace,key,revision)` for equivalent selector intent"
}
```

## Selector capability discovery model

Discovery approach (current):
- Capability negotiation is documentation-driven in MVP.
- Clients should assume support only for fields listed in this spec and corresponding API/MCP docs.
- Unknown selector fields remain hard errors (`validation_error`) when capability is not explicitly documented/supported.

Integration notes:
- API callers: see `docs/SPECS/API.md` `POST /v1/views/evaluate` validation notes.
- MCP callers: adapter/tool metadata should expose supported selector capabilities for the active phase while preserving strict unknown-field handling.

Future extension option:
- A runtime capability descriptor may be added later, but must be additive and must not relax deterministic validation behavior for unsupported fields.

Compatibility examples:
- Additive rollout (supported capability):
```json
{
  "selector": {
    "namespaces": ["app/editor/*"],
    "order": ["namespace", "key", "revision"],
    "tags_all": ["active", "session"]
  },
  "assumption": "tags_all is explicitly documented as supported by current server capability profile"
}
```
- Unsupported additive field (strict failure):
```json
{
  "selector": {
    "namespaces": ["app/editor/*"],
    "order": ["namespace", "key", "revision"],
    "tags_all": ["active", "session"]
  },
  "expected_error": {
    "code": "validation_error",
    "message": "unknown selector field: tags_all"
  }
}
```

## Quick validation checklist

Before adding/updating selector examples:
1. Ordering:
   - Ensure `order` is explicit or the example expectation uses fallback `(namespace,key,revision)`.
2. Limits:
   - Confirm `limit` is non-negative and within max bound (`<=500`), or omitted to use default behavior.
3. Unknown fields:
   - Verify no undocumented selector fields are present unless explicitly described as invalid examples.
4. Revision scope:
   - Use `revision_scope=head` for head-only expectations.
   - Use `revision_scope=all` only when examples intentionally include full history semantics.
5. API alignment:
   - Cross-check `POST /v1/views/evaluate` validation notes in `docs/SPECS/API.md` for matching error/constraint behavior.

## Reviewer checklist (doc PRs)

Use this during VIEWS doc review after the quick validation checklist has passed:
1. Deterministic ordering guarantees:
   - Confirm examples and prose keep fallback ordering `(namespace,key,revision)` intact when `order` is omitted.
   - Verify no new language suggests non-deterministic ordering or sampling behavior.
2. Unknown-field strictness:
   - Ensure unsupported selector fields are still documented as `validation_error` outcomes.
   - Confirm new additive-field examples explicitly state capability support assumptions.
3. Capability discovery consistency:
   - Check that capability negotiation guidance stays documentation-driven for MVP.
   - Verify API/MCP cross-references remain aligned with selector capability and validation behavior.

## Selector examples consistency checklist

Use this when editing multiple selector example sections in one PR:
1. Fallback ordering consistency:
   - Ensure compatibility, deprecation, and validation examples all preserve canonical fallback `(namespace,key,revision)` semantics.
2. Normalization consistency:
   - Confirm normalized selector outputs use canonical fields (`order`, `revision_scope`, `limit`) across all example blocks.
3. Unknown-field outcome consistency:
   - Keep unsupported-field outcomes aligned to `validation_error` and avoid mixed error-class wording for equivalent cases.
4. Cross-section drift check:
   - Reconcile deprecation examples with capability examples so no field is simultaneously shown as both unsupported and baseline without an explicit assumption.

## Section cross-reference map

Use this map when deciding where to update VIEWS guidance:
- Validation constraints and example hygiene:
  - `Quick validation checklist`
- Reviewer-oriented PR checks:
  - `Reviewer checklist (doc PRs)`
- Cross-section example drift checks:
  - `Selector examples consistency checklist`
- Compatibility, additive fields, and unknown-field strictness:
  - `Selector capability discovery model` and `Compatibility examples`
- Deprecation and version transition behavior:
  - `Selector extension and versioning policy` and `Deprecation normalization examples`

## Examples maintenance cadence

Recommended cadence for VIEWS example upkeep:
1. Trigger immediate review when selector behavior docs change (validation, compatibility, or deprecation semantics).
2. Run a periodic checklist pass once per release window using:
   - `Selector examples consistency checklist`
   - `Section cross-reference map`
3. If drift is found, update the smallest affected section first, then reconcile linked sections in the same PR.

## Example update PR checklist

Before opening a VIEWS example update PR:
1. Use `Section cross-reference map` to identify all affected sections.
2. Run `Selector examples consistency checklist` and resolve cross-section drift.
3. Confirm PR scope states whether changes are validation-only, compatibility-only, or deprecation/versioning-related.
4. Keep PR focused on example/documentation alignment without altering underlying selector contract intent.

## Checklist selection note

Use this quick chooser when editing VIEWS docs:
- Use `Quick validation checklist` for selector-shape and constraint correctness checks.
- Use `Reviewer checklist (doc PRs)` for review-phase determinism and capability-alignment checks.
- Use `Selector examples consistency checklist` when touching multiple example sections.
- Use `Example update PR checklist` right before opening a PR for example-focused edits.

## Checklist handoff note

For multi-author VIEWS doc updates:
1. Record which checklist was completed (`Quick validation`, `Reviewer`, `Consistency`, `Example update PR`) and which remain.
2. Assign next-owner for unfinished checklist items in the PR summary.
3. Keep checklist status updates in the same PR thread to avoid split/ambiguous ownership.

## Checklist escalation note

If checklist conflicts remain unresolved:
1. Escalate with a brief summary of conflicting checklist outcomes.
2. Identify current owner and proposed deciding reviewer in the same PR thread.
3. Pause merge until escalation outcome is recorded and affected checklist status is updated.

## Checklist completion note

When all VIEWS checklist gates are satisfied:
1. Record final checklist status (`Quick validation`, `Reviewer`, `Consistency`, `PR checklist`) in the PR summary.
2. Confirm no unresolved escalation items remain.
3. Note completion owner and completion timestamp for traceability.

## Checklist evidence capture note

Before marking checklist completion, capture these minimum references:
1. Checklist evidence links:
   - Link the exact review artifacts for `Quick validation`, `Reviewer`, `Consistency`, and `PR checklist` outcomes.
2. Escalation outcome reference:
   - If escalation occurred, link the final escalation decision entry alongside updated checklist status.
3. Completion alignment:
   - Keep evidence links in the same PR summary entry as completion owner/timestamp to preserve audit traceability.

## Checklist variance note

When an intentional checklist deviation is necessary:
1. Variance disclosure:
   - Record the exact checklist item deviated from and why the standard requirement is not applicable in this change.
2. Approval reference:
   - Link reviewer/owner acknowledgement for the variance in the same PR thread.
3. Completion linkage:
   - Tie the variance entry to checklist evidence and completion records so final status remains auditable.

## Checklist exception expiry note

For temporary checklist exceptions, include explicit expiry handling:
1. Expiry window:
   - Record the date/release window when the exception expires.
2. Revalidation trigger:
   - Define the trigger that requires checklist revalidation at or before expiry.
3. Ownership:
   - Assign owner responsible for closing or renewing the exception with updated evidence.

## Checklist exception closure note

When a temporary checklist exception is resolved:
1. Closure record:
   - Record closure date and the checklist item now revalidated.
2. Evidence update:
   - Replace exception references with current checklist evidence links in the PR summary.
3. Status alignment:
   - Confirm completion records no longer depend on the closed exception.

## Checklist revalidation reminder note

For long-running PRs, revalidate checklist evidence before merge:
1. Reminder trigger:
   - If checklist evidence is older than the current review window, rerun relevant checklist checks.
2. Evidence refresh:
   - Update links/results for stale checklist artifacts in the PR summary.
3. Merge alignment:
   - Confirm final completion status reflects the refreshed evidence set.

## Checklist review-window note

Apply a consistent checklist review window for evidence freshness:
1. Window definition:
   - Treat checklist evidence as in-window only within the active review window for the PR.
2. Window breach handling:
   - If evidence is outside the window, trigger checklist revalidation before merge.
3. Completion alignment:
   - Keep completion status tied to evidence that is still within the review window.

## Checklist evidence-owner note

Assign clear ownership for checklist evidence freshness:
1. Owner assignment:
   - Identify the evidence owner responsible for keeping checklist links/results current during review.
2. Owner action:
   - Evidence owner refreshes stale checklist artifacts when review-window or revalidation triggers fire.
3. Completion handoff:
   - Completion records should reference the latest evidence owner confirmation before merge.

## Checklist ownership handoff note

When checklist ownership changes during review:
1. Handoff record:
   - Record outgoing owner, incoming owner, and handoff timestamp in the PR thread.
2. Evidence continuity:
   - Transfer responsibility for stale evidence refresh and revalidation follow-ups to the incoming owner.
3. Completion readiness:
   - Confirm completion status references the incoming owner confirmation after handoff.

## Checklist prevention note

Reduce revalidation failures with proactive checklist checks:
1. Prevention checks:
   - Validate checklist evidence freshness and link integrity before review-window expiry.
2. Drift watch:
   - Flag PRs with repeated checklist refresh churn for targeted cleanup.
3. Completion guard:
   - Keep completion status tied to prevention-check outcomes and latest owner confirmation.

## CLI/API mapping
- API: `POST /v1/views/evaluate`
- CLI: `context view --selector ...`
