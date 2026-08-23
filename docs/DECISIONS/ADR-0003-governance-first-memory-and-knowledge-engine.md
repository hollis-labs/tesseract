# ADR-0003: Governance-First Memory and Knowledge Engine

Date: 2026-08-23
Status: accepted

## Context
Tesseract has evolved from a generalized context registry into the portfolio's memory and knowledge engine. The current system already contains important foundations: append-only context records with heads, typed memory and knowledge revisions, shallow memory namespaces, deeper pointer-style knowledge namespaces, recall ranking, promotion, audit events, embedded Go usage, daemon usage, and MCP access.

The desired end state is not a generic database, vector store, workflow engine, crawler, or agent runtime. Tesseract's durable value is governed semantic memory: authority-aware writes, provenance, lifecycle, promotion, audit, explainable retrieval, and consistent behavior across embedded and hosted deployments.

This decision records the architectural target only. It does not choose an implementation sequence or require immediate physical storage unification.

## Decision
1. Product identity
- Tesseract is the governance-first memory and knowledge engine for the portfolio.
- Embedded Go library use is first-class. Hosted service, HTTP, and MCP access are deployment modes over the same engine semantics.
- Named instances and workspaces must carry identity in-band in externally returned MCP results and equivalent recall/read/synthesis envelopes.

2. Shared revision substrate
- Context, Memory, and Knowledge are domain policies over one shared logical revision and authority contract.
- Physical storage may remain transitional while the logical contract converges.
- Revisions are immutable accepted facts. Heads, current state, activation, freshness, embeddings, facets, recall scores, and context packets are derived projections.

3. Authority model
- Namespace existence never grants authority.
- Tesseract may infer observed namespaces from existing data for discovery, migration, health checks, admin visibility, and onboarding suggestions.
- Runtime read, write, promote, and admin authority require explicit policy or deterministic parent-authority onboarding.
- Inferred or backfilled namespace policy rows are observational only until converted into explicit policy by an authorized actor.
- Authenticated principal, calling app, on-behalf-of user or service, namespace owner, asserted author, and approver are separate concepts.

4. Lifecycle and trust
- Acceptance and lifecycle are separate dimensions.
- Acceptance states are `draft`, `reviewed`, `canonical`, and `rejected`.
- Lifecycle states are `active`, `superseded`, `deprecated`, `expired`, and `deleted`.
- Acceptance and lifecycle changes are recorded as facts. Current status is a projection, not mutable history.

5. Promotion
- Promotion uses one semantic protocol: proposal, decision, apply.
- Convenience APIs may collapse those steps for local workflows only if they emit the same canonical facts.
- Promotion changes scope and authority. It does not mutate or erase the source revision.
- The promoted target is a new authorized revision related to the source by promotion facts.

6. Memory namespaces
- Memory remains shallow and faceted.
- Canonical memory namespace shapes are `user/{user}/memory/{type}`, `user/{user}/project/{project}/memory/{type}`, and `user/{user}/session/{session}/memory/{type}`.
- The `{type}` segment is a bounded memory kind such as `decisions`, `feedback`, `followups`, `learnings`, `limitations`, `notes`, `outcomes`, or `references`.

7. Knowledge namespaces
- Knowledge remains pointer-first and supports deeper hierarchy.
- Subtree selection is first-class for knowledge namespaces.
- Knowledge records distinguish receipt, observation, resolution, verification, freshness, source authority, and derived status.
- Pointer metadata must not imply verification unless Tesseract or an authorized actor actually performed verification.

8. Retrieval and synthesis
- Exact retrieval, selection, recall, and synthesis are separate operations.
- Recall returns disclosure metadata: instance identity, ranking profile, query normalization, score components, tie-breakers, index generation, embedding provider/model, truncation, and omitted counts.
- Search and recall do not reinforce memory activation unless the caller explicitly requests a deliberate reinforcing read.
- Synthesis is model-assisted output over cited retrieval results. Persisted synthesis requires an authorized write.

9. Audit, backup, and derived state
- Required domain facts, including write, lifecycle, promotion, and policy facts, are part of the authoritative write contract.
- Best-effort behavior is acceptable for telemetry, background queues, and rebuildable projections, not for required audit facts.
- Backup and restore cover all authoritative product state: revisions, heads, namespace policy, promotion facts, lifecycle/tombstones, provenance, audit, and service-local auth.
- Derived projections such as FTS, embeddings, activation aggregates, freshness caches, queues, facets, and packets are rebuildable and may be omitted only with an explicit manifest.

10. Plugins and extensions
- Plugin boundaries are capability-oriented, not raw-store-oriented.
- Long-term extension points include source resolver, ingestor, schema validator, embedder, reranker, synthesizer, store adapter, index adapter, redactor, audit sink, and domain policy module.
- Plugins declare contract versions, effects, namespace requirements, secret references, determinism, idempotency, and health behavior.

## Consequences
Positive:
- Establishes one product identity across library, daemon, HTTP, and MCP usage.
- Keeps current working foundations while clarifying the target contract.
- Prevents observed historical data from accidentally becoming authorization policy.
- Makes recall and synthesis more reproducible, inspectable, and debuggable.
- Gives memory and knowledge different namespace shapes without splitting the architecture.

Trade-offs:
- New namespaces need an explicit onboarding path instead of silent authority inference.
- Existing status handling must converge from single-field mutation to acceptance and lifecycle facts.
- Existing promotion paths must align around the same protocol.
- Response envelopes become more explicit because instance and retrieval disclosure become part of the product contract.
- Backup, restore, audit, and plugin contracts must expand beyond the original context-registry scope.

## Related docs
- `docs/knowledge-memory-direction.md`
- `docs/DECISIONS/ADR-0001-storage-and-namespaces.md`
- `docs/DECISIONS/ADR-0002-generalized-context-registry.md`
- `/Users/chrispian/dev/hollis-labs/docs/portfolio-sweep/05-identity-tesseract.md`
