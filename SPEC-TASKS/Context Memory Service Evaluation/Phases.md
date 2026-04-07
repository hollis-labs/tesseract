Phase 0 — Align specs with code (low effort, removes drift)

0.1 Close spec/impl mismatch: tags_any
	•	Current: docs/SPECS/VIEWS.md specifies tags_any, but internal/contextstore.Select does not support tag filtering/indexing.
	•	Decision:
	•	Option A (recommended now): Remove tags_any from the View spec; track as planned feature.
	•	Option B: Implement tag indexing (see Phase 2).

Acceptance
	•	Specs match current capabilities, or tags are implemented with tests.

0.2 Records checksum
	•	Current: records.checksum exists; append path imports sha256 but stores "".
	•	Implement: compute SHA-256 of payload bytes at write-time and store it in records.checksum.
	•	Update consistency scan to validate checksum when file exists.

Acceptance
	•	New records always have checksum.
	•	Scan can detect file corruption.

⸻

Phase 1 — First-class “tiers”: cache vs memory vs pins (policy-enforced)

Right now this is a deterministic, versioned KV registry with namespaces. To match your intent, add tier semantics.

1.1 Namespace conventions
Introduce canonical tiered namespaces (convention + policy):
	•	user/memory/* — curated durable memory
	•	user/cache/* — working set; expirable/trimmed
	•	user/pins/* — always-include “must have” items
	•	app/<id>/draft/* — app-owned drafts
	•	app/<id>/session/<task-id>/* — task-scoped working context

1.2 Namespace policies
You already have namespace_policies and Server.reloadPolicies(). Extend policy schema to include deterministic enforcement knobs:
	•	tier: memory|cache|pins|draft|session
	•	retention: duration (for cache/session)
	•	max_revisions: per (namespace,key)
	•	max_bytes_per_key
	•	allowed_ops: e.g. write, promote.request, promote.apply
	•	schema: required fields (for memory records)
	•	redaction: allowed + tombstone rules

1.3 Maintenance actions
Add store-level maintenance jobs (manual for MVP, scheduled later):
	•	trim for tiers with retention
	•	compact (optional) for old revisions
	•	tombstone handling for deletions/redactions

Acceptance
	•	A cache namespace cannot become long-term memory by accident:
	•	retention trimming works
	•	policies block invalid writes/promotions
	•	Tests:
	•	writing to user/* is still actor=user only
	•	app can write only inside its own namespace patterns

⸻

Phase 2 — “Context Packets” (deterministic assembly + manifest + budgets)

Selectors (/v1/views/evaluate) are a good primitive, but agents need a higher-level “packet” contract.

2.1 Add endpoint
POST /v1/context/packet
Input:
	•	selector (existing view selector)
	•	assembly options:
	•	include_pins: bool (default true)
	•	time_window: optional { since, until }
	•	budget: { max_items, max_bytes, max_tokens_estimate }
	•	shape: { include_payload: true|false, payload_mode: full|head_only, fields_whitelist }
	•	manifest_level: summary|full

Output:
	•	items: ordered records (with or without payload)
	•	manifest:
	•	normalized selector + derived constraints
	•	policy decisions (what was allowed/blocked)
	•	trimming decisions (why items were dropped)
	•	sources count by namespace/key
	•	request_id for audit correlation

2.2 Add “token estimation” (deterministic)
Do not require a model call. Use a stable heuristic:
	•	bytes-based estimate, or a simple token approximation.
This is enough for budgeting and manifests.

2.3 Add optional tag indexing (if you want tags now)
Minimal model:
	•	record metadata includes tags: []string
	•	SQLite table record_tags(record_id, tag) indexed on tag
	•	Select can join/filter when tags_any supplied

Acceptance
	•	Packet retrieval always returns a manifest.
	•	Packet retrieval can be restricted to a namespace + time window + budget.
	•	Pinned items can be always included deterministically.

⸻

Phase 3 — Promotion becomes a gated workflow (request/approve/apply)

Current /v1/context/promote is useful but too “direct” to be your long-term guardrail.

3.1 Define promotion objects
	•	promote.request record in app/<id>/promotions/*
	•	references: (source_namespace, source_key, source_revision_id)
	•	target: (target_namespace, target_key)
	•	reason, proposed summary, checksum
	•	promote.approve record in user/* (or user/memory/promotions/*)
	•	promote.apply is performed by the service after approval

3.2 Enforce policy
	•	Apps: can only create requests in their namespace
	•	Users: can approve
	•	Service: applies, emits audit event with full linkage (request_id, approval_id, resulting record id)

Acceptance
	•	No direct app-to-user memory writes.
	•	Promotion is always traceable with provenance.

⸻

Phase 4 — Capability-scoped auth tokens

Current managed tokens validate existence/expiry/revocation but do not scope capabilities.

4.1 Token bindings
Extend auth_tokens to include:
	•	client_id
	•	scopes: write|view|packet|promote.request|promote.approve|maintenance|namespace.register
	•	namespace_globs: allowed patterns
	•	optional max_budget defaults (server-side caps)

4.2 Request authorization path
Update authorizeMutatingRequest to:
	•	validate token
	•	attach token claims to request context
	•	enforce claims in handlers (before policy engine)

Acceptance
	•	A token cannot mutate outside permitted namespaces.
	•	A token cannot call privileged endpoints without the scope.

⸻

Phase 5 — “Broker agent” integration (subordinate to deterministic gates)

Do not let a broker be “magic.” Make it a planner.

5.1 Add broker contract (no model required)
	•	POST /v1/broker/plan (optional)
	•	Accepts: task summary + optional user intent
	•	Outputs: a structured plan (selector + assembly options)
	•	The service validates + executes the plan via /v1/context/packet.

5.2 Add strict plan validation
	•	broker cannot expand scope beyond default caps
	•	broker cannot target forbidden namespaces
	•	broker cannot request payloads for restricted keys

Acceptance
	•	Broker can help compose selection specs, but cannot bypass the guardrails.

⸻

Deliverable B — Publishable architecture/governance doc (for technical content)

Working title

Deterministic Context Memory for Agents: Separating Cache from Memory with Policy-Enforced Assembly

Core thesis

Agents don’t need “more memory.” They need:
	•	owned namespaces
	•	deterministic retrieval
	•	explicit promotion workflows
	•	budgeted context packets
	•	auditable manifests

This enables continuity without turning your system into an opaque, self-mutating black box.

⸻

Recommended outline

1) The failure mode: “chat history as memory”
	•	Too broad, too noisy
	•	Scope drift and silent mutation
	•	No provenance
	•	No user ownership boundaries

2) The model: cache vs memory vs pins
Define the tiers:
	•	Cache (working set): ephemeral, task/session scoped, trimmed
	•	Memory (durable): curated, versioned, conflict-aware
	•	Pins (always include): explicit user assertions (constraints, preferences, non-negotiables)

3) Namespaces as the ownership boundary
Explain:
	•	user/* is protected
	•	app/<id>/* is app-owned
	•	promotion is the bridge, not “write anywhere”

4) Deterministic retrieval: views and packets
	•	Views = selectors (no inference)
	•	Packets = assembled outputs with budgets
	•	Every output includes a manifest: what was included/excluded and why

5) Promotion lifecycle (draft → request → approve → apply)
	•	Agents propose
	•	Users commit
	•	Service enforces

6) Broker agents are planners, not authorities
	•	Broker proposes a retrieval plan
	•	Deterministic engine validates + executes
	•	Broker never mutates memory directly

7) Practical examples
	•	“Resume a task after 48 hours”
	•	“New agent joins project: boot context packet”
	•	“App drafts learned preference → user approves into memory”

8) Where embeddings fit (and where they do not)
	•	Useful for ranking inside an already-scoped candidate set
	•	Not acceptable as an unrestricted global memory search

9) Tooling ecosystem comparisons
Discuss (sincerely) adjacent approaches:
	•	prompt files + manual context packs
	•	RAG over notes
	•	agent frameworks with memory plugins
	•	why deterministic assembly + ownership is the differentiator

10) Operational posture
	•	local-first + export/import
	•	optional sync later
	•	security + capability tokens