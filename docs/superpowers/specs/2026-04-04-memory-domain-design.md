> **Note:** This document was written when the project was named Cortex. It has since been renamed to Vanta Conduit.

# Cortex Memory Domain — Design Spec

Status: **draft — ready for review**
Track: D (see `docs/RELEASE-ROADMAP.md`)
Started: 2026-04-04
Blocks: Nanite `MemoryService` (see `nanite/docs/phase-c-cortex-investigation.md`)
Blocked by: Track H (shared AI providers), Track I (shared queue) — for
embedding subsystem only; D-core is independent

---

## Goal

Add a memory subsystem to Cortex that supports chat continuity and
agent-authored persistent memory, without compromising the determinism and
auditability of the existing context registry.

## Non-goals (1.0)

- Structured domain schemas (Contact, Organization, Event, etc.) — deferred
  until real usage surfaces shapes worth promoting from freeform.
- Activation-spreading / associative networks — out of scope; only
  simple per-memory activation scores are in.
- Automatic cross-session memory sync — not required at 1.0.
- Replacing PCC or the existing context registry — memory is a sibling, not
  a replacement.

---

## Executive summary

Cortex gains a **memory subsystem** that runs as a sibling to the existing
context registry — same process, same SQLite file, same auth, separate
tables and code paths. Memory is **append-only**: every write is a new
revision, current state is a computed projection over the revision log,
and the log preserves full history. Memories are **agent-authored on behalf
of the user** (not raw extractions), carry first-class provenance, and are
identified by caller-declared **dot-notation keys** (`user.preferences.verbosity`)
with a server-generated fallback for freeform cases.

Memories live under **user-rooted, scope-nested namespaces**:

```
user/{user_id}/memory                         — cross-project user memory
user/{user_id}/project/{project_id}/memory    — project-scoped memory
user/{user_id}/session/{session_id}/memory    — session staging
```

Apps write on behalf of the user; no app has its own memory silo. This
delivers cross-agent continuity — the central product value of Cortex as
an MCP server.

Retrieval is controlled by **two orthogonal knobs**: revision scope
(`current` | `timeline`) and ranking (`activation` | `chronological` |
`similarity`), plus filters. **Activation** is a read-time ranking signal
that reinforces on successful retrieval and decays on a schedule —
strongest memories match first, no storage mutation beyond a small scoped
state table.

The **embedding subsystem** depends on two new shared Go modules (Tracks H
and I) extracted for cross-portfolio reuse: a multi-provider AI module
(from Nanite's existing code) and a Laravel-inspired job queue. D-core —
roughly 60-70% of the memory subsystem — has zero dependencies on H or I
and implements in parallel with their design. Auto-embed, backfill,
semantic dedup, and similarity retrieval layer on top once H and I land.

Cortex is treated as an **API, not a UX**: all errors are structured,
callers handle user communication, degraded states are surfaced via
health endpoints rather than startup warnings.

Consumer integration adds a new **`MemorySource`** to Nanite's
contextbroker (not an extension of `CortexSource`) that calls a new
**`memory_recall`** MCP tool with multi-namespace, multi-knob queries.
Promotion from session staging to user/project scope is **agent-driven
explicit** at 1.0; automatic heuristic promotion is deferred.

---

## Design decisions

### D1. Memory is a sibling subsystem inside Cortex (not a new content type)

Cortex ships as one process and one storage backend, but internally the
context registry and the memory store are two clean domains:

- Same binary, same SQLite file, same auth, same namespace strings.
- Separate tables, separate code paths, separate write APIs.
- Separate retrieval semantics — context registry is deterministic
  key/view lookup; memory is activation-weighted and supports
  similarity-ranked recall.

**Why:** The context registry's append-only + deterministic-reads + explicit
promote model is a trust feature, not an accident. Memory's "current belief
overrides older belief" + "activation-weighted ranking" semantics fight that
model if stuffed into the same type system. Keeping them as siblings lets
each be honest to its own semantics without compromise.

**Not-separate-binary, not-separate-database.** From outside the process
they look like one service. From inside they are two domains.

### D2. Memory storage is append-only; current state is a computed projection

Every memory write creates a new revision. Nothing is overwritten, nothing is
deleted on update. "What does the user currently believe about X" is a
read-time projection: for each logical memory, the live revision is the most
recent non-deprecated one.

**Why:** Append-only gives history and audit trail for free. Corrections and
conflict resolution become "deprecate old revision + write new revision,"
which is reversible, inspectable, and never loses data. The app-name analogy:
you remember every name your app ever had *and* you know the current one —
both fall out of the same log if current-state is computed.

**Implication:** Memory records have two identifiers:
- `memory_id` — stable logical identity across revisions
- `revision_id` — unique per write

### D3. Memories are agent-authored, with first-class provenance

Memories are not raw extractions. An agent reads the conversation, decides
something is worth remembering, and *authors* a memory record — the same way
Claude Code's MEMORY.md system works today. Extraction triggers (PostCompact,
per-turn signals, explicit user requests) are *signals* that invoke an
author call; they are not a separate semantic.

Every memory revision records:
- Who authored it (agent identity)
- What signal triggered it (explicit user request, post-compact, per-turn
  heuristic, etc.)
- What session/conversation it came from
- A confidence score the author assigned

### D4. Activation-based ranking as a read-time signal

Each logical memory has an `activation` scalar that:
- Increments when the memory is successfully used in a retrieval result
- Decays on a schedule (time-based)
- Is used as a ranking input at read time, not a storage gate

**Scope discipline:** this is ~20 lines of code (a float field + two update
paths + a decay job). It is *not* activation spreading, not associative
networks, not a cognitive architecture. If we find ourselves designing graph
traversals, we stop and re-scope.

**Why:** Gives the "strongest memories match first" behavior Chrispian wants,
without committing to a research project. Frequently-used memories rise;
stale ones sink; nothing is lost.

### D5. Two-axis retrieval model (not three modes)

Retrieval is specified by two orthogonal knobs, not three named modes:

- **Revision scope:** `current` (live revision per logical memory) or
  `timeline` (all revisions, ordered)
- **Ranking:** `activation` | `chronological` | `similarity`

Plus filters: namespace, tags, origin category, confidence threshold, time
window.

This covers "state" (`current` + `activation`), "history" (`timeline` +
`chronological`), "information" (`current` + `similarity`), and composite
queries like "history filtered by similarity to X" that don't fit a
three-mode taxonomy.

### D6. Origin categories as metadata tags, not types

Memories carry an `origin` tag from a closed vocabulary of five values:
`user`, `feedback`, `project`, `reference`, `observation` (finalized in
D9). This is where the memory came from / why it exists, not what it is
about.

**No domain schemas at 1.0.** No `Contact`, no `Event`, no `Meeting`. Payload
is freeform text with metadata. If real usage shows 200 contact-shaped
memories across users, we promote `Contact` to a structured subtype in a
later release with a migration. Bias toward later — schemas are expensive
to remove.

### D7. TTL / decay supported per-memory via metadata

Per-memory expiry is opt-in metadata, not a global policy. Session-scoped
staging memories can carry a short TTL; user preferences carry none. The
decay job handles both activation decay and TTL expiry in one pass.

### D8. Caller-declared dot-notation key is the primary identity mechanism

Memories are identified by a **caller-declared `memory_key`** using validated
dot-notation (e.g., `user.preferences.verbosity`,
`project.cortex.naming_decision`, `contact.alice.role`). The write API
requires structured input and validates the key shape at the boundary.

- Writes with the same `(namespace, memory_key)` append a new revision to the
  same logical memory.
- Writes without a key get a server-generated `memory_id` and are treated
  as standalone logical memories (freeform fallback, discouraged but
  supported).
- Key naming conventions ship as a documented deliverable alongside the
  code, so agents don't freelance near-duplicates like `user.pref.verbose`
  vs `user.preferences.verbosity`.

**Why:** Keys map to how memories are actually authored (an agent updating
a user preference *knows* what it's updating). Deterministic, works offline,
zero hard embedding dependency, gives conflict resolution for free (two
agents writing to the same key converge via append-only revisions). Schema
validation catches malformed keys at the write boundary rather than in
downstream queries.

**Deferred:** Optional semantic-dedup write mode (the "please dedup this
for me" opt-in from brainstorm option (e)) is on the table for MVP pending
the Q5 embedding provider decision. The deterministic key-based path is
the foundation; semantic dedup is an optional layer on top if embeddings
land in time.

### D9. Record shape: rigid metadata envelope, structured-by-convention payload

**Revision (append-only, immutable):**

| Field | Type | Required? | Notes |
|---|---|---|---|
| `revision_id` | ULID | yes | Unique per write |
| `memory_id` | ULID | yes | Stable across revisions of one logical memory |
| `namespace` | string | yes | Validated against registered namespaces (Q4) |
| `memory_key` | string | no | Dot-notation, validated. Null = freeform no-key memory |
| `status` | enum | yes | `draft` \| `reviewed` \| `canonical` \| `deprecated` |
| `supersedes` | revision_id | no | Explicit pointer to replaced revision; auto-deprecates it |
| `created_at` | timestamp | yes | — |
| `author` | `{agent_id, agent_version}` | yes | Who wrote it |
| `trigger` | enum | yes | `explicit` \| `post_compact` \| `per_turn` \| `promotion` \| `manual` |
| `session_id` | string | **yes** | Manual inserts use `manual:<ulid>` convention |
| `origin` | enum | yes | `user` \| `feedback` \| `project` \| `reference` \| `observation` (closed set) |
| `confidence` | float | **yes** by default | 0.0–1.0; author-assigned; relaxable per namespace |
| `tags` | []string | no | Freeform ad-hoc grouping |
| `ttl` | duration | no | Null = persistent |
| `payload.summary` | string | **yes** by default | One-liner; relaxable per namespace (falls back to first line of body) |
| `payload.body` | string | no | Markdown; structured-by-convention (`**Why:**`, `**How to apply:**`) not by schema |
| `embedding` | vector | no | Backfillable; written when embedding provider available |

**Mutable per-memory state (separate table, keyed by `memory_id`):**

| Field | Type | Notes |
|---|---|---|
| `activation` | float | Incremented on retrieval use, decayed by periodic job |
| `last_accessed_at` | timestamp | — |
| `access_count` | int | — |
| `current_revision` | revision_id | Pointer to the live (non-deprecated) revision |

**Key validation rules:**
- Regex: `[a-z0-9_]+(\.[a-z0-9_]+)*`
- Max 6 segments
- Max 64 chars per segment
- Max 256 chars total
- Rejected at write boundary with structured error; no silent normalization
- Reserved top-level prefixes (conventional, not enforced at 1.0):
  `user.*`, `project.*`, `session.*`, `contact.*`, `agent.*`

**Required-field relaxation policy.** `summary`, `confidence`, and
`session_id` are required **by default** but can be made optional via a
per-namespace (or per-app) configuration setting for callers that
participate willingly at a lower level of discipline:

- `summary` optional → server computes from first line of `body`
- `confidence` optional → defaults to 0.7 ("unknown")
- `session_id` is *always* required; manual inserts use the
  `manual:<ulid>` sentinel convention

The system is opinionated by default. Callers opt into looser modes
explicitly, so the complexity is visible, not hidden.

**Embedding input (when provider available):** concatenate
`memory_key + payload.summary + tags.join + payload.body` and embed as a
single vector per revision. Embedding is a property of content (immutable),
so it lives on the revision, not the mutable state table.

**Explicitly rejected additions:**
- No `importance` field (overlaps confidence + activation + origin ranking)
- No freeform `extra` JSON escape hatch
- No per-origin tables (one memory table, origin as a column)
- No tag hierarchy (hierarchy belongs in the key)

### D10. User-rooted, scope-nested namespace hierarchy

Memory lives under user-owned namespaces, scoped by project and session.
Apps author memories **on behalf of** the user; no app has its own memory
silo.

```
user/{user_id}/memory                              — cross-project user memory
user/{user_id}/project/{project_id}/memory         — project-scoped memory
user/{user_id}/session/{session_id}/memory         — session staging (pre-promotion)
```

**Refinements:**

1. **`user_id` segment included from day 1**, even for single-user local
   installs. Migrations are expensive; one extra path component today is
   free. Default the id to a local config value (`cortex.user_id`) so
   callers don't have to think about it.

2. **`project_id` is opaque.** Cortex accepts any stable string as a
   project identifier. It does not try to canonicalize, normalize, or
   serve as the source of truth for what a project is. Callers pass
   whatever stable string they already use (git remote + path, Nanite
   working dir, Hadron blueprint id, Engine project record id). If two
   callers disagree about the project id for the same codebase, they
   get two memory scopes — that's a caller coordination problem, not
   Cortex's to solve.

3. **Session scope is staging-only.** Session memories are the "draft
   before promotion to user/project scope" case. No cross-agent sharing
   happens at session level — each caller has its own session scope.
   Cross-agent memory sharing kicks in *after* session memories are
   promoted to `user/{id}/memory` or `user/{id}/project/{pid}/memory`.

**Rejected alternatives:**

- **App-rooted (phase-c doc recommendation):** `app/nanite/memory/...`.
  Siloed per app. Defeats the cross-agent continuity use case that
  Cortex exists to enable. Phase-c was optimizing for data isolation;
  we're optimizing for memory reuse across callers.
- **Memory-rooted top-level:** `memory/user/...`. Clean conceptually but
  introduces a new top-level root with ambiguous ownership. Existing
  `user/*` semantics handle this fine.
- **Hybrid with app-private escape hatch:** `user/{id}/memory/*` plus
  `app/{id}/memory/*` for app-private. YAGNI — no concrete app-private
  memory use case exists today. Can be added post-1.0 if one shows up.

**Auth implication (deferred to E).** Apps writing on behalf of a user
need an "acting as" model. At 1.0 with a single local user this is
almost free — the user token is the auth. Multi-user future adds real
auth, but the namespace shape doesn't change.

---

## Embedding infrastructure — ground truth (2026-04-04 survey)

The phase-c doc's "embedding_unavailable" finding is outdated. Current
state:

- **Provider interface exists:** `internal/embedding/provider.go` —
  `Provider` interface (`Embed`, `Model`, `Dimensions`). Fully pluggable
  via struct composition.
- **Working implementations:** `OllamaProvider` (production-ready,
  `nomic-embed-text`, 768 dims) and `MockProvider` (deterministic, tests
  only). No cloud providers yet.
- **Storage:** SQLite `embeddings` table with float32 BLOBs, brute-force
  cosine similarity ranking. PgVector implementation also exists but is
  not wired. Schema supports multiple models per record via
  `(record_id, model)` composite key.
- **Search path:** `context_search` and `context_rag_query` return
  `embedding_unavailable` **only because `EmbeddingProvider` is nil in
  `cmd/contextd/main.go`** — never initialized. Everything downstream
  works.
- **Write path:** No auto-embed on `context_typed_write` today.
  `context_session_snapshot` and bulk ingest tools conditionally embed
  when a provider is wired. No backfill job.
- **Configuration:** No env var / config file / flag to select a
  provider. Must be injected programmatically at startup.

**Gap to "working out of the box":** roughly one afternoon — wire Ollama
provider in `main.go` behind a config default, add the `VectorIndex`
inject, add a startup warning if the provider can't connect. Everything
else is done.

---

### D11. Embedding subsystem depends on two new shared Go modules

The memory domain's embedding and auto-embed paths depend on two new
workstreams that did not exist when this spec started:

- **Track H — Shared AI provider module.** Extracted from Nanite's working
  multi-provider code. Covers LLM + embedding calls across Anthropic,
  OpenAI-compatible, Ollama, and CLI agent providers. Versioned, shared by
  all apps in the portfolio.
- **Track I — Shared queue module.** New, Laravel-inspired. Jobs, workers,
  retry/backoff, failed-jobs, dispatchable interface, pluggable drivers.
  In-process workers with SQLite persistence at 1.0.

Memory domain's core work (types, namespaces, identity, append-only storage,
activation ranking, lifecycle) has **zero dependency** on H or I and can
proceed in parallel. Only the embedding-dependent subsystems block on them.

### D12. Embedding MVP scope

**In for 1.0:**

- **No default provider.** Cortex ships with provider selection required.
  Zero-config install runs in degraded mode — memory writes succeed,
  embedding jobs queue but can't be processed, similarity queries return
  structured `provider_not_configured` errors. The Wails onboarding UI
  (Track F) handles provider selection; memory domain does not ship with
  a hardcoded default.
- **Provider support at launch (via Track H):**
  - Anthropic API (Claude)
  - OpenAI-compatible API (OpenAI, Azure, Together, Groq, compatible
    endpoints)
  - Ollama (supported, not defaulted — for power users / offline dev)
  - CLI agent adapters (Claude / Codex / Copilot / Gemini) where
    applicable; embedding capability varies per provider
- **Async auto-embed via queue (Track I).** Memory writes persist
  synchronously. Embedding jobs are enqueued to the shared queue. Workers
  embed asynchronously and write the embedding to the revision row when
  complete. Writes never block on provider availability or latency.
- **Backfill as a queue job.** Scans for revisions with null embeddings,
  enqueues embed jobs. Runs on startup and periodically. Same worker
  infrastructure as auto-embed.
- **Semantic dedup on write (D8 opt-in) — synchronous only.** When the
  caller requests `dedup=semantic`, the server embeds the incoming memory,
  searches for matches in the same namespace above a threshold (default
  0.85), attaches as a new revision on the closest match or creates a
  new logical memory. Response tells the caller which happened.
  - *Failure mode:* if the provider is unavailable, returns structured
    `dedup_unavailable`. Caller decides whether to retry, fall through
    to a plain write, or surface to the user.
  - *No async dedup.* Race conditions (two writes arriving before either
    is embedded-and-merged) make async semantic dedup a correctness trap.
    Sync-only at 1.0; revisit post-1.0 if demand justifies the complexity.
- **Vector storage: current brute-force SQLite.** `(record_id, model)`
  composite key schema already exists. Adequate for <50k vectors.
  `sqlite-vec` upgrade deferred.

**Out for 1.0:**

- `sqlite-vec` upgrade (premature)
- Auto-embed for the existing context registry (memory-only at 1.0; other
  tracks can add it later without changing memory domain contracts)
- Per-caller embedding model selection (schema supports it; API doesn't
  expose it at 1.0)
- Async semantic dedup (deferred indefinitely; sync is the design)

### D13. Error posture: Cortex is an API, callers handle user comms

Cortex is rarely seen directly by the user. It's used by agents on the
user's behalf. The error model reflects that:

- All error responses are structured envelopes with stable error codes
- No silent fallbacks, no "best effort" defaults that hide failures
- No startup warnings intended for end users (the caller is the audience)
- Degraded states are surfaced via a health/status endpoint callers can
  poll — not via CLI output users might miss
- When the embedding provider is down, writes still succeed (queue absorbs
  them), similarity queries return `provider_not_configured` /
  `provider_unavailable` errors, and the caller is responsible for
  whatever the user sees

---

### D14. Sequencing: finish D's spec, implement D-core in parallel with H/I design

D's spec completes first (through Q6). Implementation of D-core proceeds in
parallel with separate brainstorm sessions for Tracks H and I.

**D-core scope (no H/I dependencies):**
- Append-only storage schema and write path
- Record shape (D9) and key validation (D8)
- Namespace hierarchy (D10) and registration
- Logical identity, supersedes, status lifecycle
- Activation state table, retrieval reinforcement, decay job (time-based,
  no external dependencies)
- Non-embedding retrieval paths (current/timeline × chronological/activation
  × filters)
- MCP tool surface for writes and non-semantic reads
- TTL enforcement
- Tests for all of the above

**D-deferred (requires H or I):**
- Auto-embed worker path
- Backfill job
- Semantic similarity retrieval
- Semantic dedup on write
- Any code path that calls an embedding provider

**Discipline requirement.** D-core ships with clean stub interfaces where H
and I will plug in later: an `Embedder` interface with a no-op/queue-dropping
implementation, a `JobQueue` interface with an in-memory no-op. When H and I
land, we swap stubs for real implementations. Do not prematurely wire real
provider or queue code into D-core — keep the interface boundary honest.

---

### D15. Consumer integration — separate MemorySource, dedicated MCP surface, explicit promotion

#### 15a. Separate `MemorySource` in Nanite's contextbroker

Nanite's contextbroker gains a **new `MemorySource`** alongside the
existing `CortexSource`, `PCCSource`, `SessionSource`, `EngineSource`, and
`HadronBlueprintGate`. `CortexSource` is not extended to serve memory.

**Why separate:**
- `CortexSource` is intent-driven and queries typed context records via
  `context_broker_fetch`. `MemorySource` is scope-driven (user + project)
  and queries by activation or similarity. Same source with two contracts
  is a mess.
- Ranking models differ — `CortexSource` uses type `rank_bias`,
  `MemorySource` uses activation × status × confidence × origin weights.
- Budget allocation differs — memory is high-signal, low-volume; context
  is broader.
- Separation of concerns — `CortexSource` never needs to know memory
  exists; `MemorySource` never needs to know context-registry types exist.

This is a Nanite-side change. The Cortex side exposes the MCP tools
(15b) that `MemorySource` calls.

#### 15b. MCP tool surface for memory

Cortex exposes the following new MCP tools (final names may be refined
during implementation):

| Tool | Purpose |
|---|---|
| `memory_write` | Write a new memory revision. Params: namespace, memory_key (optional), payload, metadata (origin, author, trigger, session_id, confidence, tags, ttl, supersedes). Optional `dedup=semantic` mode (D12). |
| `memory_recall` | Context-assembly retrieval. Params: `namespaces` (list, multi-namespace single-call), `ranking` (`activation` \| `chronological` \| `similarity`), `revision_scope` (`current` \| `timeline`), `query` (for similarity), filters (origin, status, tags, confidence_min, time_window), `limit`, `token_budget`. Returns unified ranked result set with source namespace attribution. |
| `memory_get` | Fetch the current revision of a specific `(namespace, memory_key)`. For agents that know what they're looking for. |
| `memory_history` | Full revision timeline for a `(namespace, memory_key)`. For audit/history UI and conflict inspection. |
| `memory_promote` | Move a session-scoped memory to user or project scope (see 15e). |
| `memory_deprecate` | Mark a revision deprecated without writing a replacement. For explicit removal/correction flows. |

**Single `memory_recall` tool, not per-mode tools.** The two-axis
retrieval model (D5) makes every mode a combination of knobs. One tool
that takes the knobs matches the design. Mode-specific tools would hide
the combinatorics behind two bad abstractions.

**What is NOT exposed at 1.0:**
- No bulk memory ingest tool (writes are one-at-a-time; bulk is a
  power-user feature, can add post-1.0)
- No memory-specific search tool separate from `memory_recall`
  (similarity search is `ranking=similarity`)
- No direct activation manipulation API (agents cannot boost or decay
  memories directly; only internal retrieval reinforcement and the decay
  job modify activation)
- No cross-user memory sharing (each user has their own
  `user/{id}/memory`; teams/orgs are post-1.0)
- No export/import tool for memories (can use existing `context_*` bulk
  tools or a future memory-specific one)
- No diff/compare tool (nice-to-have, not needed at 1.0)

#### 15c. Multi-namespace recall with unified server-side ranking

`memory_recall` accepts a list of namespaces and fetches candidates from
all of them in a single call. The server applies the unified ranking
model across the pooled set and returns top-N within the token budget.
Each result carries its source namespace so the caller can attribute.

**Why single-call multi-namespace, not multiple calls with client-side
merge:** unified ranking requires server-side scoring. A client can't
decide whether a user-scope memory outranks a project-scope memory
without applying the same weighting model the server would — at which
point the client is reimplementing the server. Round-trip latency also
matters for per-turn context assembly.

**Unified ranking formula for `activation` mode:**

```
score = activation
      × status_weight[status]
      × confidence
      × origin_weight[origin]
      × recency_factor(last_accessed_at)
```

**Status weights at 1.0** (tunable with usage data):

| Status | Weight |
|---|---|
| `canonical` | 1.0 |
| `reviewed` | 0.9 |
| `draft` | 0.6 |
| `deprecated` | 0.1 (still visible for conflict detection, heavily demoted) |

**Origin weights at 1.0** (tunable with usage data):

| Origin | Weight |
|---|---|
| `feedback` | 1.3 (highest — user corrections override everything else) |
| `user` | 1.1 |
| `project` | 1.0 (baseline) |
| `reference` | 0.9 |
| `observation` | 0.8 (lowest — passive notes outrank nothing) |

**Similarity mode** swaps `activation` for `cosine_similarity(query, memory)`
in the first factor; other factors remain as re-ranking multipliers.
**Chronological mode** ignores the scoring formula and sorts by timestamp.

#### 15d. Token budget allocation is a Nanite-side concern

Cortex honors the `token_budget` parameter on `memory_recall` and returns
top-N results that fit within it. How the budget is divided across
sources (`MemorySource`, `CortexSource`, `PCCSource`, etc.) is a
contextbroker configuration decision in Nanite, not a Cortex design
decision.

For reference only, a likely Nanite allocation is:
- `MemorySource`: 10-15% of total budget (5-7.5k tokens at 50k total) —
  enough for 30-60 memories, adequate for any single assembly
- `CortexSource`: 25-35% (reduced slightly from current 30-40%)
- Other sources: unchanged

This is illustrative. The actual allocation is tuned in Nanite's config.

#### 15e. Promotion workflow: agent-driven explicit at 1.0

Session-scoped memories (`user/{id}/session/{sid}/memory`) graduate to
user or project scope via explicit agent action, not automatic heuristic.

**Mechanics of `memory_promote`:**

1. **Input:** source `(namespace, memory_id)`, target scope
   (`user` or `project/{pid}`)
2. **Read** the source memory's current revision
3. **Write** a new revision in the target namespace with the same
   payload/metadata, `trigger=promotion`
4. **Deduplicate by key:** if a memory with the same `memory_key` already
   exists in the target namespace, the new revision auto-sets
   `supersedes` to the existing current revision (normal update
   semantics)
5. **Mark** the source revision in the session namespace as `deprecated`
   with a pointer to the promoted revision
6. **Preserve** the source — do not delete, the audit trail stays intact

**Why explicit, not automatic at 1.0:**
- Heuristics for automatic promotion get wrong often enough that either
  over-promotion pollutes user scope or under-promotion leaves useful
  memories rotting in sessions. Both failure modes are hard to audit.
- The agent already knows whether a memory should persist — that's a
  decision it made when writing with `trigger=explicit` vs `per_turn` vs
  `post_compact`. Making promotion a separate explicit step forces the
  agent to reason about scope.
- Explicit is simpler to test and reason about. Automatic heuristic
  promotion is a post-1.0 enhancement, tuned against real usage patterns.

---

## Decision index

| # | Title | Summary |
|---|---|---|
| D1 | Sibling subsystem | Memory is a sibling to the context registry, not a new content type. Same process, separate tables/code. |
| D2 | Append-only + projection | Every write is a new revision; current state is computed at read time. |
| D3 | Agent-authored provenance | Memories are authored (not raw-extracted) with first-class provenance. |
| D4 | Activation ranking | Read-time scalar reinforced on use, decayed on schedule. Small scope — not activation spreading. |
| D5 | Two-axis retrieval | Revision scope × ranking, plus filters. Not three named modes. |
| D6 | Origin tags, not types | Closed five-value origin vocabulary. No domain schemas at 1.0. |
| D7 | Per-memory TTL | Opt-in metadata; decay job handles both activation and TTL. |
| D8 | Dot-notation keys | Caller-declared, validated at write boundary. Server-generated fallback for keyless writes. |
| D9 | Record shape | Rigid metadata envelope, structured-by-convention payload. Required fields relaxable per namespace. |
| D10 | User-rooted namespaces | `user/{id}/memory` + `user/{id}/project/{pid}/memory` + `user/{id}/session/{sid}/memory`. App-rooted rejected. |
| D11 | H/I prerequisites | Embedding subsystem depends on Tracks H and I; D-core is independent. |
| D12 | Embedding MVP scope | No default provider; async auto-embed via queue; sync-only semantic dedup. |
| D13 | API error posture | Structured errors, no silent fallbacks, health endpoint for degraded states. |
| D14 | D-core/deferred split | D-core implements in parallel with H/I design; stub interfaces preserve boundaries. |
| D15 | Consumer integration | New `MemorySource` in Nanite; `memory_recall` as single multi-knob tool; explicit promotion. |

---

## Plan-time decisions (handoff notes for the implementation plan)

These items were deliberately deferred from the spec. They are not gaps —
they are choices best made when writing the implementation plan, where
concrete numbers and config locations can be picked with more context.

- **Decay formula and cadence.** D4 + D7 call for a decay job handling
  both activation decay and TTL expiry. The plan picks the activation
  decay model (e.g., exponential with a half-life of N days) and the run
  cadence (e.g., every M minutes). Default should be gentle enough that
  a memory used once a week retains meaningful activation.
- **Recency factor definition.** D15c's ranking formula references
  `recency_factor(last_accessed_at)` without defining the function. The
  plan picks the shape — likely a gentle decay that plateaus for
  recently-accessed memories and falls off slowly for stale ones.
- **Required-field relaxation config location.** D9 allows per-namespace
  relaxation of `summary` and `confidence` requirements but does not
  specify where that config lives. The natural home is the namespace
  registration record (D10 already validates namespaces against
  registrations) — the plan should confirm and wire it.
- **`supersedes` status mutation note.** D9 states that writing a new
  revision with `supersedes` auto-deprecates the referenced revision. This
  is the one place where writing a new revision mutates an older row's
  `status` column. This is consistent with D2 (the revision's payload and
  metadata remain immutable; only the status enum flips, which is a
  lifecycle marker, not content) — but the plan should explicitly document
  this as the single authorized exception to "revisions are append-only."
- **Embedding column is D-core schema, D-deferred population.** D-core
  adds a nullable `embedding` column to the revision table. Population is
  D-deferred (waits for H and I). The plan should make this split
  explicit in the migration and in the write-path code.

---

## References

- `nanite/docs/phase-c-cortex-investigation.md` — investigation that kicked
  off this work
- `docs/SCOPE.md` — existing Cortex product scope
- `docs/PRODUCT_FEATURE_BRIEF.md` — existing Cortex product framing
- `docs/RELEASE-ROADMAP.md` — overall release decomposition
