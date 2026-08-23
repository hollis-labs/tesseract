# Tesseract Context, Memory, and Knowledge Direction

**Status:** Directional architecture draft

**Date:** 2026-08-22
**Scope:** Desired product boundaries and architecture; not an implementation plan

## Purpose

This document describes Tesseract's intended place in the Hollis Labs
portfolio. It evaluates context persistence, memory, knowledge, retrieval,
promotion, ingestion, synthesis, authentication, storage, observability, and
extension points against the portfolio's shared engineering boundaries.

It deliberately does not define phases, estimates, task breakdowns, migration
steps, or compatibility strategy. Existing ADRs and specifications remain
historical and implementation truth until explicitly superseded. This
document provides a target direction for a later architecture and planning
session.

## Portfolio axiom

> Hollis tools own execution and operational state, but not the business
> definitions or business data they operate on.

Tesseract needs a precise interpretation of this axiom because persistence is
its product. Saying that Tesseract never owns data would make the service
meaningless. Instead, it separates semantic authority from custodial
authority:

- A namespace owner decides what information means, who may write it, and
  whether it is accepted as draft, reviewed, or canonical.
- Tesseract owns the integrity of records deliberately committed to its
  custody: revision identity, immutable history, current heads, policy
  enforcement, indexes, retrieval state, audit facts, and service-local
  access control.
- An external source remains authoritative for pointer-backed knowledge,
  imported source material, and projections of live application state.
- Applications remain authoritative for their native entities. Storing a
  task summary, infrastructure snapshot, conversation memory, or workflow
  outcome in Tesseract does not transfer ownership of the task, resource,
  conversation, or workflow.
- Credential authorities own provider secrets and their lifecycle.

Materialization, caching, indexing, summarization, and embedding do not by
themselves transfer semantic ownership.

## Product definition

Tesseract is a local-first, authority-aware persistence and retrieval
substrate for context, memory, and knowledge. It:

1. Accepts records under an explicit namespace and authority policy.
2. Preserves immutable revisions, provenance, lifecycle facts, and a
   well-defined current head.
3. Enforces the boundary between caller identity, namespace ownership, and
   delegated write authority.
4. Maintains rebuildable indexes and operational retrieval state.
5. Selects and ranks records deterministically where possible and discloses
   any model-dependent behavior where it is not.
6. Supports explicit promotion between scopes without silently changing the
   authority of the source record.
7. Represents externally authoritative knowledge through source-aware
   pointers, snapshots, and provenance.
8. Exposes one service model through its Go library, CLI, HTTP API, MCP tools,
   and administrative interface.

Tesseract is not an agent runtime, workflow engine, task manager, message
broker, crawler, general document-management system, LLM gateway, secret
manager, infrastructure control plane, or universal application database.

Applications may embed Tesseract or run it as a daemon. Those are deployment
modes of the same product, not different semantic models.

## Architecture sketch

```text
Authoritative sources                    Hollis applications and users
files / repositories / services          authored context and memory
          |                                          |
          | pointers, snapshots, explicit writes    |
          +--------------------+---------------------+
                               v
                  +----------------------------+
                  |         Tesseract          |
                  |                            |
                  | identity + authority       |
                  | domain policy              |
                  | revision core              |
                  | promotion protocol         |
                  | audit + provenance         |
                  +-------------+--------------+
                                |
               +----------------+----------------+
               |                                 |
               v                                 v
       authoritative records              derived projections
       revisions + heads                  FTS / embeddings
       policies + audit                   activation / facets
       source observations                freshness / packets
               |                                 |
               +----------------+----------------+
                                v
                  selectors / recall / lookup
                                |
                       optional explicit
                       rerank or synthesis

External credential authority ---- secret references ----^
Tether ----------------------- optional LLM gateway -------^
Hadron ---------------- workflows for ingestion/maintenance^
```

The revision core is authoritative for what Tesseract accepted and stored.
The domain policy determines what that stored information represents. Derived
projections accelerate or improve retrieval but never become record authority.

## Authority and custody model

Tesseract should describe every durable item using one of three authority
modes.

### Native or custodial record

A native record is deliberately authored into a Tesseract namespace. The
stored revision is the canonical representation of that record until the
namespace authority supersedes, deprecates, expires, or deletes it according
to policy.

Examples include:

- a user-authored durable preference
- an accepted architectural decision
- a reviewed learning captured from an agent session
- a Tesseract-native context record

Tesseract owns the persistence protocol and custody of these records. The
namespace owner remains the authority for their meaning and acceptance.

### Pointer-backed knowledge

A pointer-backed record describes and locates information whose authoritative
content lives elsewhere. Tesseract owns the pointer record, its provenance,
annotations, source observations, retrieval projections, and any explicitly
stored summary. It does not become authoritative for the referenced source.

Examples include:

- a repository path and commit digest
- a URL and observed content digest
- a document identifier in an external service
- a database entity reference

A recorded observation time is not automatically a verification time.
Tesseract must only claim that a pointer was resolved or verified when the
source was actually accessed and checked.

### Projection, cache, or snapshot

A projection captures information from a live external owner for retrieval or
historical context. Its envelope must identify the source, observation time,
freshness policy, and whether the payload is a complete snapshot, partial
projection, or derived summary.

Examples include:

- selected task state from Torque
- an infrastructure inventory snapshot from Cerberus
- a conversation summary from Nanite
- a workflow result summary from Hadron

The external system remains authoritative for current state. Tesseract may be
authoritative for the historical fact that this snapshot was observed at a
specific time.

## Desired domain model

Context, memory, and knowledge are different policies over a shared revision
substrate. They should not become three unrelated databases or three slightly
different definitions of revision, provenance, promotion, and authority.

### Shared revision core

The desired architecture has one logical revision contract:

```text
LogicalRecord
  identity
  domain
  namespace
  key
  authority mode
  owner reference

Revision
  revision identity
  logical record identity
  immutable payload or content reference
  content digest
  schema and type
  author assertion
  authenticated actor
  provenance
  lifecycle assertion
  supersedes references
  creation time
  expiry or retention policy

Head
  logical record identity
  selected current revision
  selection reason

DerivedState
  activation and access observations
  search indexes
  embedding references
  freshness observations
  computed facets
```

The separation is important:

- Revisions are immutable facts.
- A head is a mutable projection over those facts.
- Activation, access counts, embeddings, freshness, and indexes are derived
  operational state.
- Status changes, deprecation, promotion decisions, and deletion requests are
  new facts, not edits to old facts.
- The authenticated actor is derived from the access boundary. A caller may
  supply an asserted author, but it must not be able to forge the actor.

One logical contract does not require one public endpoint, one namespace
grammar, or necessarily one physical table. Physical convergence is a storage
decision. Semantic convergence is the architectural requirement.

### Context

Context is the general typed-record domain. It supports explicitly scoped,
schema-aware records and deterministic selectors. It is appropriate for
application context that does not need memory activation semantics or a
knowledge source contract.

Context views are selectors. They may filter, order, bound, and project
records. They do not execute processors, call models, mutate source records,
or conceal workflow logic.

### Memory

Memory is the human- and agent-oriented domain for durable recall. It adds:

- a constrained memory type
- scope-aware namespaces
- lifecycle and trust semantics
- activation and access observations
- lexical and semantic recall
- deduplication advice
- promotion across session, project, and user scopes

The shallow, faceted memory shape remains appropriate:

```text
user/{user}/memory/{type}
user/{user}/project/{project}/memory/{type}
user/{user}/session/{session}/memory/{type}
```

Memory types such as decisions, feedback, learnings, limitations, outcomes,
and references are facets over a stable depth. They should not evolve into an
unbounded taxonomy encoded in the namespace path.

Memory lifecycle and trust are related but not identical. `draft`, `reviewed`,
`canonical`, and `deprecated` currently mix acceptance state with lifecycle.
The desired model must make clear:

- who is permitted to assert each state
- whether state is an immutable assertion or a current projection
- how conflicting assertions are resolved
- whether deprecation means superseded, rejected, expired, or merely hidden

An agent may propose or write a draft under a delegated grant. It must not
silently make user-owned information canonical merely because it can address
the user's namespace.

### Knowledge

Knowledge is the source-aware domain. It favors deep, meaningful hierarchy
because source collections and subjects naturally nest:

```text
user/{user}/knowledge/{path...}
app/{app}/knowledge/{path...}
```

Knowledge adds required source semantics over the revision core:

- knowledge kind
- source kind and locator
- source authority
- observation and verification facts
- content digest or immutable source revision where available
- pointer, snapshot, or derived-content classification

Source and kind values should be registered or schema-validated rather than
remaining ungoverned strings. New source adapters can extend the catalog
without changing the revision contract.

Knowledge may be implemented as a thin domain over the shared core. Thin must
mean that it adds policy rather than bypassing it.

## Namespaces and authorization

A namespace is an ownership and scope boundary. It is not an authentication
credential and must not be treated as proof that the caller owns it.

The following identities are orthogonal:

- authenticated principal
- calling application or client
- human or service on whose behalf the call is made
- namespace owner
- asserted content author
- approver

Authorization evaluates the relationship between these identities and an
operation. Namespace registration or discovery must never itself grant write
authority.

### Namespace policy

Namespace policy is authoritative configuration owned by the appropriate
user, application, or administrator. Tesseract validates, stores, and enforces
that policy. A policy may declare:

- owner and authority mode
- permitted principals and clients
- read, append, supersede, promote, approve, and administer grants
- permitted record types and schemas
- allowed lifecycle assertions
- retention and sensitivity classification
- promotion destinations
- whether model-assisted processing is permitted

Automatic materialization of a namespace can be a convenience, but inferred
metadata must not create authority. Unknown namespaces should fail closed when
their ownership cannot be established safely.

### Read authority

Read access is as important as write access. Memory and knowledge may contain
private, security-sensitive, or regulated information even in a local-first
deployment. Open, static-token, and managed-token modes should be deployment
profiles over one authorization model, not different security semantics
scattered across adapters.

Library embedding does not imply bypassing policy. Trusted in-process callers
may receive a broad capability explicitly, but all public surfaces should
ultimately invoke the same authorized domain operations.

## Write and promotion semantics

### Write

A successful write means that Tesseract durably accepted an immutable
revision, applied the domain and namespace policy, advanced any eligible head,
and recorded the required audit fact. If canonical audit is required, a write
must not succeed while silently dropping its audit record.

A write should distinguish:

- a new logical record
- a new revision of a known record
- a duplicate candidate
- a superseding assertion
- an imported snapshot
- a projection refresh

Semantic similarity is evidence, not identity. Deduplication may advise,
reject under an explicit policy, or request confirmation. It must not silently
discard or supersede content solely because an embedding happens to cross a
threshold.

### Promotion

Promotion changes scope and authority. It is not a move operation.

All domain-specific promotion surfaces should implement one semantic
protocol:

```text
Proposal -> Decision -> Apply
```

- A proposal identifies the immutable source revision, target namespace,
  intended lifecycle state, reason, and proposer.
- A decision records the target authority's approval or rejection.
- Apply creates a new target revision with full provenance back to the source.
- The source remains immutable and addressable.
- Any later source deprecation is a separate authorized assertion.

A caller with all three capabilities may use a convenience operation that
performs the protocol atomically. The convenience API must still emit the same
facts and must not bypass target authority.

Session-to-project, session-to-user, and application-to-user promotion are
instances of this same model. Their policy can differ without inventing
different meanings for promotion.

## Retrieval model

Tesseract retrieval has four distinct levels. Keeping them explicit prevents
hidden processing and makes reproducibility understandable.

### Exact retrieval

Exact retrieval resolves a logical record, revision, head, or history. It is
deterministic for a fixed store state and policy snapshot.

### Selection

A selector filters and orders indexed metadata using a stable ordering and
documented tie-breaker. Context views belong here. Selection has no model call
and no source mutation.

### Recall

Recall ranks candidates using declared signals such as:

- lexical relevance
- vector similarity
- recency
- activation
- lifecycle or trust state
- namespace and type filters

The result should identify the ranking profile, weights, tie-breaker, index
generation, embedding model identity, and query normalization relevant to the
decision. Exact, lexical, chronological, and activation ranking can be
deterministic for a fixed snapshot and configuration. Vector results are
reproducible only relative to their stored embeddings and model identity.

An external reranker is an explicit advisory stage. Its provider, model,
request policy, and non-deterministic nature must be visible in the response
and operational record.

### Synthesis

Synthesis creates a response from retrieved material. It is an explicit
model-assisted operation, not a more powerful form of `get` or `view`.
Synthesis must return citations or record references and disclose the provider
and model configuration used.

Synthesized output does not become memory or knowledge merely because
Tesseract produced it. Persisting that output requires a separate authorized
write or promotion.

### Read side effects

Activation reinforcement is operational mutation. Tesseract should distinguish
a pure read from a reinforcing read in its contract. Deliberate point reads
may reinforce a memory when requested; search, recall, health checks, exports,
and background inspection should not do so accidentally.

## Context packets and materialized views

A context packet is a bounded, ordered materialization of selected records for
a consumer. It should include a manifest describing:

- selector and policy snapshot
- record and revision identities
- ordering and truncation
- token or byte budget
- omitted-result counts
- projection and model versions where relevant
- creation and expiry times

A packet is a reproducible delivery artifact, not a new source of truth. A
consumer may cache it for an invocation, but the records and sources retain
their respective authority.

## Ingestion and source integration

Ingestion converts externally authoritative material into pointer records,
snapshots, or native records under explicit policy. It should be modeled as a
transparent transformation:

```text
source reference
  -> resolve and observe
  -> parse
  -> normalize
  -> optionally chunk
  -> validate policy and schema
  -> write revisions and provenance
  -> build derived projections
```

Every generated record needs enough provenance to explain its source,
transformation, digest, and parent-child relationship. Chunked records are
derived children, not independent claims about source authority.

Tesseract may provide source adapters and ingestion primitives. It should not
grow a second workflow engine, scheduler, crawler fleet, or message broker to
run them. Hadron can orchestrate multi-step ingestion and maintenance;
applications can invoke simple ingestion directly.

## Indexing, embeddings, and model use

Full-text indexes, embeddings, activation scores, facets, and freshness data
are rebuildable projections. Loss of a projection may degrade retrieval but
must not erase the authoritative record history.

An embedding record should identify:

- model and provider identity
- model revision where available
- dimensionality and normalization
- content digest embedded
- creation time
- projection generation

Changing an embedding model creates a new projection generation; it does not
rewrite record history. Raw vectors are internal projection details and need
not appear in normal record payloads or APIs.

Tesseract can remain standalone by using official provider SDKs behind narrow
embedder, reranker, and synthesizer contracts. In a composed deployment it may
use Tether's LLM gateway through the same contracts. Tether is optional, not a
runtime dependency or alternate owner of Tesseract records.

## Storage architecture

The storage model should distinguish authoritative state from rebuildable
state.

### Authoritative state

- immutable record revisions or content-addressed payloads
- logical record and head facts
- namespace and authorization policy
- promotion proposals and decisions
- lifecycle assertions and tombstones
- provenance and canonical audit events
- service-local credential hashes and revocation state

### Derived or operational state

- full-text and vector indexes
- activation and access aggregates
- computed facets
- freshness caches
- queue leases and retry bookkeeping
- materialized context packets that can be recreated

The current file-plus-SQLite context store and inline-SQLite memory store
express two physical models. The target should provide one atomicity,
consistency, backup, and recovery contract even if different record classes
continue to use different physical layouts.

The record store may support local files and SQLite first and later add object
or database adapters if a concrete deployment requires them. Storage adapters
must preserve the revision and authority semantics rather than exposing the
least-common-denominator behavior of every backend.

### Atomicity and recovery

A record payload, its revision metadata, its selected head, and required audit
fact must have a recoverable commit protocol. When a backend cannot provide a
single transaction, Tesseract needs explicit intents, reconciliation, and
idempotent recovery.

Consistency checks should identify:

- orphaned payloads or metadata
- missing heads and invalid head targets
- broken supersedes relationships
- missing canonical audit facts
- policy references to unknown principals or namespaces
- projections built from missing or mismatched content

Repair is an explicit administrative operation whose effects are audited.

### Backup and restore

Backup is a product-wide contract, not a context-store-only feature. A complete
backup must account for every authoritative domain: context, memory, knowledge,
namespace policy, promotion state, audit facts, and service-local auth state.

Rebuildable projections may be omitted if the manifest says so and restore can
recreate them. External secret material must never be included. Backup and
restore must preserve record identities and provenance and must verify their
own manifests and checksums.

### Retention and deletion

Append-only means that accepted history is not silently rewritten. Retention,
legal deletion, and explicit compaction may physically remove data, but they
are privileged lifecycle operations with policy, tombstones, audit evidence,
and a stated effect on reproducibility.

The useful invariant is therefore:

> Revisions are immutable and retained until an explicit, authorized, audited
> retention or deletion operation says otherwise.

Expiry should normally affect eligibility and visibility before it triggers
physical destruction. Derived indexes must respect the same eligibility
decision.

## Authentication, authorization, and secrets

Tesseract may own credentials that exist specifically to authorize access to
Tesseract. Issuing, hashing, scoping, revoking, and auditing its capability
tokens are part of its operational responsibility. This is different from
owning provider credentials used by source systems or model vendors.

For service-local tokens:

- raw bearer values are returned only at issuance
- durable storage contains one-way hashes and lifecycle metadata
- scopes and namespace grants are explicit
- expiry, revocation, rotation, and use attribution are first-class
- raw tokens are delivered through external secret plumbing, not command-line
  arguments, committed configuration, logs, or backups

For external credentials:

- configuration carries a secret reference, not the secret value
- a runtime authority resolves the reference at the narrow adapter boundary
- Tesseract does not provide generic `get secret` or `set secret` APIs
- provider SDKs receive the credential only for the relevant call
- audit records identify the credential reference or grant, never its value

At-rest protection is a storage and deployment policy. Content sensitivity,
backup encryption, key custody, and local-process trust must be explicit rather
than inferred from the word "local."

## Library and daemon modes

The Go library and `contextd` daemon expose the same core semantics.

### Embedded library

Embedding is useful when an application wants an in-process local substrate.
The host owns process lifecycle and supplies explicit identity, policy,
storage, providers, and background-work configuration.

### Daemon

The daemon is appropriate for multiple local clients, independent lifecycle,
HTTP/MCP access, and centralized administration. It owns its listeners and
background workers but remains supervised by the operating environment or
Cerberus. Tesseract should not implement its own OS daemon manager.

### Store authority

Embedded and daemon instances must not become uncoordinated authorities over
the same store. A deployment needs an explicit single-writer or backend
coordination contract, instance identity, lease behavior for background work,
and clear ownership of migrations. Starting a second process against a path is
not a distributed-systems design.

Background decay, embedding jobs, retention, verification, and maintenance
must be idempotent and safe under the declared instance model.

## Interfaces and parity

CLI, HTTP, MCP, the Go library, and the administrative UI are adapters over
domain services. They may optimize interaction style but must not invent
different authority, promotion, status, or deletion semantics.

Parity means:

- the same operation has the same semantic result
- identity and authorization are preserved across transports
- error classes and lifecycle conditions map predictably
- every mutating adapter reaches the same audit boundary
- intentionally transport-specific operations are documented as such

Compatibility names such as `contextd`, `/v1/conduit/lookup`,
`conduit_lookup`, and the `vanta` MCP prefix may remain supported. Tesseract
should nevertheless have one canonical product vocabulary so compatibility
does not become permanent conceptual ambiguity.

## Plugin and adapter model

The Hollis Labs plugin SDK should provide shared loading, lifecycle,
configuration, capability, diagnostics, and compatibility mechanics.
Tesseract should define narrow domain contracts on top of it.

Useful extension classes include:

- source resolver or ingestor
- schema and record-type provider
- embedder
- reranker
- synthesizer
- record or blob store
- index or projection backend
- redactor or content-policy evaluator
- audit sink
- domain policy extension

Each plugin declares:

- the contract and versions it implements
- configuration schema and defaults
- network and filesystem effects
- namespace read and write requirements
- secret references it may consume
- determinism and idempotency properties
- health, readiness, and diagnostic behavior

Plugins receive the minimum capability required for their contract. They
should not receive an unscoped raw store merely because it is convenient.
Registration is transactional: a failed plugin does not leave partial tools,
routes, hooks, or UI state behind.

A manifest on disk does not make an implementation dynamically loadable.
In-process constructors, external processes, and future Go plugin mechanisms
are different execution models and should be named accurately. Configuration
can enable registered implementations; it cannot instantiate code that is not
present.

## Operations and observability

Tesseract owns evidence about its own behavior:

- accepted and rejected operations
- actor, client, authority, and policy decision
- revision and head changes
- promotion proposal, decision, and application
- source resolution and verification observations
- projection generation and rebuild
- queue, retry, and background-job state
- model/provider calls, latency, usage, and failure classification
- backup, restore, retention, deletion, and repair
- plugin and adapter health

Operational logs are event streams written to stdout and stderr. Canonical
audit facts are structured durable data, not log scraping. Metrics and traces
are projections for operating the service and must avoid record content and
secret values by default.

Health answers whether the process is alive. Readiness answers whether the
configured storage, migrations, authority policy, and required providers can
serve the advertised capabilities. A process can be healthy without being
ready.

## Portfolio composition

Tesseract is useful precisely because applications can compose with it without
surrendering their own domain authority.

### Nanite

Nanite owns agents, sessions, conversation behavior, launch policy, and agent
runtime state. It may embed Tesseract or call the daemon for durable memory and
knowledge. Nanite decides when an observation is worth proposing; Tesseract
enforces where and how it may be stored, promoted, and recalled.

A conversation transcript or session checkpoint remains Nanite data unless it
is explicitly committed into a Tesseract namespace under an appropriate
record contract.

### Tether

Tether owns gateway routing, provider transport, messaging, and its own
operational delivery state. Tesseract may optionally use Tether's LLM gateway
for embeddings, reranking, or synthesis. Tether events may be referenced or
projected into Tesseract, but Tesseract is not Tether's message database.

### Hadron

Hadron owns workflow definitions at execution time, run state, schedules,
gates, and execution provenance. A Hadron workflow may invoke Tesseract for
ingestion, retrieval, promotion, maintenance, or durable outcomes. Tesseract
does not schedule or orchestrate the workflow.

### Torque

Torque remains authoritative for tasks, sprints, dependencies, and delivery
state. Tesseract may hold durable decisions, lessons, pointer-backed project
knowledge, or explicitly labeled task snapshots. Recall results must not be
presented as current Torque state without resolving the authoritative source.

### Cerberus

Cerberus deploys, registers, observes, and may supervise a Tesseract service or
its attached resources. Tesseract owns its internal record semantics;
Cerberus owns the declared deployment and resource lifecycle. Cerberus passes
secret references and endpoints, not provider secret values embedded in
Tesseract configuration.

### Other applications

An application integrates through records, pointers, projections, and explicit
contracts. Direct access to Tesseract's SQLite tables or record files is not a
composition API. Cross-application reads should preserve source authority and
must not turn the union lookup surface into an ungoverned global data lake.

## Twelve-factor and Go operating model

Tesseract follows the portfolio's adapted twelve-factor direction:

- The codebase produces versioned binaries and libraries used in many
  deployments.
- Go module dependencies and external tool requirements are explicit.
- Environment and config files contain non-secret settings and secret
  references; environment-specific values are not hardcoded.
- Record stores, indexes, model gateways, and source systems are attached
  resources addressed through explicit configuration.
- Build creates an immutable binary; release binds it to config and registered
  adapters; run executes that release.
- The process does not rely on local mutable state outside declared attached
  stores.
- The daemon embeds its HTTP/MCP servers and binds configured ports directly.
- Scale and concurrency follow the declared store-authority model rather than
  assuming multiple processes can share local files safely.
- Startup is fast, shutdown is graceful, and background leases and in-flight
  writes have explicit recovery behavior.
- Development, test, and production exercise the same storage and domain
  contracts even when their adapters differ.
- Logs go to stdout and stderr; durable audit is a separate service record.
- Migrations, consistency checks, reindexing, backup, restore, and repair run
  as one-off commands from the same release and configuration model.

## Current strengths to preserve

The current implementation already contains much of the right architectural
shape:

- append-only revision history and explicit heads
- deterministic selectors and bounded query surfaces
- namespace ownership and protected user-scope concepts
- promotion rather than silent cross-scope writes
- pointer-first knowledge records
- separate memory revision and mutable activation state
- recall that does not reinforce every search result
- derived embeddings rather than embedding vectors in normal payloads
- lexical, semantic, and blended lookup
- service-local token hashes, scopes, namespace grants, expiry, and revocation
- backup, consistency, readiness, and administrative surfaces
- interface parity tests and shared core packages
- official provider SDKs and Hollis Labs contract modules
- both embeddable-library and daemon usage
- XDG-aware paths and externally supplied configuration

The direction should consolidate these strengths rather than replace them with
a generic database abstraction.

## Architectural tensions to resolve

These are target-state design questions exposed by the current implementation,
not an implementation backlog.

### Multiple revision models

Generic context records use file payloads plus SQLite records and heads.
Memory and knowledge use a separate inline-SQLite state/revision model. Each
has its own status, provenance, embedding, promotion, and history behavior.
They need one logical revision, authority, audit, backup, and recovery
contract even if their physical storage remains specialized.

### Promotion semantics differ by domain

Generic context describes proposal, approval, and apply for application-to-user
promotion. Memory promotion currently performs a direct session-to-project or
session-to-user copy and deprecation. These should become policy profiles over
one promotion protocol, with an atomic convenience form only for callers that
already possess all required authority.

### Append-only claims have mutable exceptions

Memory status and deprecation can mutate existing state, while compaction and
retention can remove revisions. The desired model should represent lifecycle
changes as new facts and state exactly where physical destruction narrows the
append-only guarantee.

### Authorization is adapter-dependent

Namespace checks and managed-token behavior are strongest at selected HTTP or
MCP boundaries. An embedded caller or another adapter can otherwise acquire
different semantics. Authorization belongs in the domain operation, with an
explicit trusted capability for intentionally privileged hosts.

### Audit can be best effort

Some operations can succeed even when audit emission fails. That conflicts
with an audit trail described as canonical. The architecture must classify
which events are required commit facts and which telemetry may remain best
effort.

### Caller assertions and authenticated identity can blur

Author and actor fields supplied by a caller can be useful provenance, but
they cannot substitute for an authenticated principal. Both need distinct
fields and trust semantics.

### Namespace discovery can blur into authority creation

Inferring and automatically registering a namespace from incoming data is
convenient. It must not create ownership or authorization as a side effect of
a write attempt.

### Semantic deduplication can become hidden policy

Similarity-based deduplication and same-key supersession currently influence
writes. A model score is advisory unless an explicit namespace policy gives it
authority. Concurrency and changing model generations also require identity
to remain independent of similarity.

### Pointer metadata can overstate verification

Recording `resolved_at` as the write time without actually resolving the
source turns an observation default into a false verification claim. Source
receipt, observation, resolution, and verification must be distinct facts.

### Backup does not yet express the whole product

The existing backup shape is centered on generic context records, audit, and
auth tokens. The desired contract must cover authoritative memory, knowledge,
namespace, promotion, and policy state or explicitly declare external backup
responsibility for them.

### Model behavior needs one disclosure contract

Some documentation and code describe different default embedding providers,
while reranking and synthesis add further provider behavior. Model-dependent
operations need registered capabilities and durable model identities rather
than implied defaults.

### Plugin loading and capability boundaries are incomplete

The current host relies on constructors already compiled into the process,
exposes broad store access, and has partial service and UI registration
semantics. The target needs accurate execution-model naming, transactional
registration, narrow capabilities, and Tesseract-specific contracts.

### Background work assumes an instance model

Decay and queue workers are safe only when ownership and leasing are explicit.
Embedded and daemon processes sharing a store need coordination or a clear
single-authority rule.

### Product naming carries compatibility history

Tesseract is the canonical product name, while Vanta- and Conduit-derived
binary, route, MCP, and Cerberus identifiers remain for compatibility. The
public model should distinguish intentional compatibility aliases from current
vocabulary so new integrations do not deepen the naming split.

## Boundary guidance

When deciding whether a capability belongs in Tesseract, use these tests.

It belongs in Tesseract when it primarily:

- preserves a context, memory, or knowledge record and its history
- enforces namespace ownership, grants, lifecycle, or promotion
- maintains a rebuildable retrieval projection
- retrieves, ranks, packages, or explicitly synthesizes stored material
- verifies storage integrity, provenance, or source observations
- operates Tesseract's own service, credentials, and background work

It probably belongs elsewhere when it primarily:

- authors application business data or domain definitions
- manages conversations, agents, tasks, workflows, messages, or infrastructure
- crawls and schedules broad multi-step source synchronization
- acts as a general provider or LLM gateway
- stores or brokers provider secrets
- treats Tesseract as a replacement database for another app's live state

If the answer is mixed, keep authority in the owning application and integrate
through a pointer, snapshot, explicit write, narrow adapter, or workflow.

## Questions for the next architecture session

1. Is the shared revision core a required physical storage model, or is a
   rigorously shared logical contract over specialized stores sufficient?
2. What is the exact distinction between record lifecycle and trust or
   acceptance state?
3. Which principals may write drafts, mark reviewed content, establish a head,
   or declare content canonical in each namespace class?
4. Should every promotion expose proposal, decision, and apply records even
   when one authorized caller performs them atomically?
5. Which audit facts are part of the write transaction, and which operational
   telemetry is allowed to be best effort?
6. What are the default read policies for local, embedded, daemon, and remote
   deployments?
7. Should a point read reinforce activation by default, require an explicit
   flag, or be represented as a separate operation?
8. Which ranking profiles must be deterministic, and what disclosure is
   required for embeddings, external rerankers, and synthesis?
9. What complete set of authoritative state must backup and restore cover, and
   which projections are always rebuildable?
10. Is service-local capability-token issuance part of every Tesseract
    deployment, or can external identity providers supply the principal and
    grants while Tesseract retains only policy?
11. Which knowledge source observations constitute receipt, resolution,
    digest verification, freshness verification, and source-authoritative
    truth?
12. What store-authority rule governs embedded and daemon instances, and is
    multi-writer operation a supported product requirement?
13. Which extension classes are stable enough for the plugin SDK, and which
    should remain internal until their semantics settle?
14. Which Vanta/Conduit compatibility names are permanent public contracts and
    which are transitional aliases around Tesseract?

These questions refine the target architecture without changing the core
direction: Tesseract is the portfolio's authority-aware persistence and
retrieval substrate, not the owner of every source it can remember.
