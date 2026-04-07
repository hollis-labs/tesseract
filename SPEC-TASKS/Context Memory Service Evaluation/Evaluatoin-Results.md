P0 — Must-fix for the product to match your stated intent

1) Cache vs long-term memory is not yet a first-class distinction
Right now, the system is primarily a versioned KV registry with namespaces. That’s good, but it doesn’t yet encode:
	•	“working context cache” semantics (short-lived, task/session scoped, aggressively trimmed)
	•	“durable memory” semantics (curated, versioned facts, conflict handling, slow-changing)

Recommendation (minimal structural change):
Make the distinction explicit via tiered namespaces + retention policies, e.g.:
	•	user/cache/* (working context, expirable)
	•	user/memory/* (durable facts, curated)
	•	user/pins/* (always-include, user asserted)
	•	app/<id>/session/<task-id>/* (ephemeral task scope)
	•	app/<id>/draft/* (drafts pending promotion)

Then bind different maintenance rules to tiers via policy JSON in namespace_policies (you already have the table and load path via Server.reloadPolicies()).

2) Promotion workflow lacks “gate strength”
Promotion currently requires actor=user (CanPromote) and then uses CanWrite on the target namespace, which is fine, but there’s no structured approval gate, no review record, and no conflict detection.

Recommendation:
Add a deterministic “promotion request” record type in app/<id>/promotions/* and require promotion to reference:
	•	source record id + revision
	•	reason
	•	optional diff/summary
	•	target key
	•	policy rule ID (what allowed this)

Then implement:
	•	promote.request (app writes)
	•	promote.approve (user writes)
	•	promote.apply (system action)

This preserves the core principle: apps can propose, user can commit.

3) Managed auth tokens are not scoped
auth_tokens work as global bearer tokens for mutating routes (when enabled). They are not tied to:
	•	a client_id
	•	namespace patterns
	•	permitted operations (write vs promote vs repair)
	•	TTL policy enforcement beyond expires/revoked

Recommendation:
Evolve tokens into “capability tokens”:
	•	bind token → client_id
	•	add allowed namespace globs
	•	add op scopes (write/promote/repair/namespace-register)
This can remain deterministic and simple, and it’s important if multiple local agents/tools will use the service.

⸻

P1 — Important alignment gaps (spec vs implementation, or missing core features)

4) Views spec includes tags_any, but implementation doesn’t
docs/SPECS/VIEWS.md defines tags_any, but contextstore.Select has no metadata/tag indexing. This is an explicit spec/impl mismatch.

Options:
	•	MVP: remove tags_any from spec and treat it as future work
	•	Or implement minimal tags by:
	•	storing a metadata_json per record in SQLite
	•	indexing a normalized tags table for deterministic filtering

5) Select is deterministic but not yet “context assembly”
Selectors currently support:
	•	namespace glob patterns (post-filtered)
	•	explicit key list (SQL IN)
	•	revision_scope head/all
	•	deterministic order
	•	bounded limit

Missing for your stated “assemble needed context” goal:
	•	time windows (created_at bounds)
	•	pagination cursor
	•	“include pins” / “required items”
	•	token budget / output shaping
	•	manifests (why each item was included)

You have a strong core; you now need an assembly contract on top of selectors.

Recommendation:
Introduce a “Context Packet” endpoint that returns:
	•	items (records)
	•	manifest (normalized selector + policy decisions + truncation + filters applied)
	•	optional budget info (bytes/tokens estimate)

6) Checksums are in schema but not computed on write
records.checksum exists and store imports sha256, but AppendRecord inserts checksum as "".

Recommendation:
Compute and persist checksum on write (file payload hash). This enables:
	•	corruption detection
	•	backup verification
	•	consistency scan improvements

⸻

P2 — Quality and operational ergonomics

7) Namespace filtering is post-query, not in SQL
Select() filters namespaces in-memory (matchNamespace) after reading rows. It’s OK for MVP, but can be optimized later by:
	•	adding a prefix filter when pattern is a plain prefix (most are)
	•	splitting patterns into prefixable vs glob

8) Missing first-class “conflict/drift” mechanics for memory
Durable memory needs:
	•	“supersedes” links
	•	conflict state
	•	resolution workflow
	•	“tombstone” for deletions/redactions

You already have revision history per key; you need metadata to represent semantic replacement vs conflicting truths.

⸻

Broker agent integration (how to do it without violating determinism)

You can add a broker agent without compromising the store/retrieve invariants if you enforce:
	1.	Broker produces a structured retrieval plan (selector + packet spec)
	2.	CMS validates the plan deterministically (policy + budgets + scope)
	3.	CMS executes the retrieval deterministically
	4.	Broker may summarize only within constraints and must return a manifest reference

Implementation-wise, this maps cleanly to:
	•	your existing selector validation (DisallowUnknownFields, limit bounds, order keys)
	•	namespace policy engine
	•	audit events

⸻

Concrete “next build” recommendations (smallest steps with highest leverage)

Step 1: Formalize tiers (cache vs memory) using namespace policies
	•	Define canonical namespaces: user/cache, user/memory, user/pins, app/<id>/session
	•	Add policy JSON fields for retention, allowed ops, required schema keys

Step 2: Add a Context Packet endpoint
	•	Input: selector + assembly options (include pins, budget)
	•	Output: items + manifest + truncation info
	•	This becomes the main “agent continuity” product surface

Step 3: Capability-scoped managed tokens
	•	Token binds to client_id and allowed namespace globs
	•	Store enforces on mutating routes before policy engine

Step 4: Close spec/impl mismatches (tags_any, checksum)
	•	Either remove tags from spec or implement minimal tag indexing
	•	Compute checksum on write
