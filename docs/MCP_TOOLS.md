# Tesseract — MCP Tools (agent reference)

This is the agent-facing catalog for Tesseract's 28-tool MCP surface. Every
tool here is registered by `tesseract mcp` and has an HTTP peer under `/v1/*`
unless the row is marked **MCP-only**.

> MCP registration and its peer mapping are checked against
> `tests/parity/parity_test.go::surfaceCatalog`. The complete HTTP route
> registry is `apiRoutes` in `internal/contextapi/server.go` and is documented
> in [`docs/SPECS/API.md`](SPECS/API.md).

## Quick facts

- **Transport:** stdio (launched by Claude Code or another MCP host). Configure in your MCP client:
  ```json
  {
    "mcpServers": {
      "tesseract": {
        "type": "stdio",
        "command": "tesseract",
        "args": ["mcp", "--token", "<hex-capability-token>"]
      }
    }
  }
  ```
- **Tool ID prefix:** `mcp__tesseract__` (Claude side). Example: `mcp__tesseract__memory_write`.
- **Data root:** resolved through the platform/XDG layout and shared with the
  CLI and HTTP server. Run `tesseract path` in the MCP host's environment to
  print the exact data, config, cache, and state paths before configuring it;
  that command creates nothing.
- **Capability token:** a store-backed token supplied with `mcp --token` is
  required only for tools whose catalog row names a scope. Scope and namespace
  claims are checked per tool. HTTP's `--static-token` value is not an MCP
  capability token.
- **Shared semantics:** tools and HTTP peers use the same store/domain services; deliberate argument or envelope differences are called out below.

## Agent-facing skills (tesseract_skills)

Every agent hitting this surface should start with `tesseract_skills start-here`. The tool is a single progressive-discovery entry point:

- `tesseract_skills` with no args → returns the skill index (name + description + scope hint).
- `tesseract_skills` with `name=<skill-name>` → returns the full markdown body of one skill.

Shipped skills (11):

| Name | Type | Body covers |
|---|---|---|
| `start-here` | orientation | Tesseract's three domains, invariants, how to use this surface. |
| `namespaces` | primitive | Canonical tier patterns, ownership, memory-domain stricter form. |
| `facets-and-kinds` | primitive | Facet vocabulary, the `kind` convention, extension rules. |
| `revisions` | primitive | Append-only model, supersede chains, dedup, revision IDs. |
| `recall-and-ranking` | primitive | Activation / chronological / similarity / relevance (RRF). |
| `promotion` | primitive | App→user workflow: request → approve → apply. |
| `views` | primitive | Selectors-not-processors; namespace globs. |
| `memory` | domain | When to use memory, common patterns. |
| `knowledge` | domain | Content addressed by key, with `kind`/`source`/`pointer` facets. |
| `context-packet` | feature | Boot workflows, plan and fetch, budget tuning. |
| `audit` | feature | Querying the audit log. |

Workflow-specific skills for downstream apps belong in those app repos. Tesseract ships primitives and reference docs only.

## Domains

- **Context** — generic revisioned key-value records. Read/write, typed schemas, views, packet assembly, promotion workflow, embeddings, audit. Several of these tools carry an arm selector (`shape`, `mode`, `stage`, `kind`, `execute`, `full_evaluation`) rather than being split into one tool per fidelity; the catalog below names the selector on each.
- **Memory** — append-only agent memory revisions with recall (activation/chronological/similarity/relevance rankings).
- **Knowledge** — content a later session will go looking for by name (a project canonical, handoff, playbook, doc, package) with structured facets. Backed by the memory revision store with `domain=knowledge`. The boundary against memory, with its limits, is stated once in `tesseract_skills start-here`.
- **Cross-domain** — one `get`, one `history`, one `recall`, and two revision-level ops that span every domain. `domain` is an argument, not a tool-name prefix.

## Tool naming

<!-- BEGIN GENERATED: tool-naming -->
A tool name is `<prefix>_[subject_]<verb>`. The prefix says which domain owns the tool; the verb says what the operation is; anything between them names what is operated on. One verb per operation, so an operation you know in one domain is guessable in another.

This whole section is generated from `internal/mcpadapter/toolvocab.go`. `tests/parity/toolvocab_test.go` asserts every registered name matches it and that this block is still its rendering — edit the Go structure, then run `go test ./tests/parity/ -run TestDocNamingSection -update-docs`.

**Prefix rule.** `tesseract_` when the tool spans domains or serves the surface itself; `<domain>_` when it is domain-specific.

| Prefix | Covers |
|---|---|
| `context_` | the context domain only — generic revisioned records |
| `event_` | the event domain only — the append-only narrative log |
| `knowledge_` | the knowledge domain only — content addressed by key |
| `memory_` | the memory domain only — agent-authored revisions |
| `tesseract_` | spans every domain, or serves the surface itself |

**Verb table.** The verb is the trailing segment(s) of the name; anything between prefix and verb is a subject naming what is operated on.

| Verb | Means | Prefixes |
|---|---|---|
| `deprecate` | Soft-remove one revision; history keeps it. | `tesseract` |
| `embed` | Compute and store an embedding vector for a record. | `context` |
| `estimate` | Size what a selector would return, without returning it. | `context` |
| `get` | Fetch the current entry at one identity. | `tesseract` |
| `get_revision` | Fetch one revision by its revision_id. | `tesseract` |
| `history` | Every revision of one entry, newest first. | `tesseract` |
| `ingest` | Write many records, or one document split into many, in a single call. | `context` |
| `list` | Enumerate the entries of a registry or a log. | `context`, `event` |
| `pack` | Assemble a budget-bounded bundle of records. | `context` |
| `plan` | Produce a fetch plan for an intent, and optionally run it. | `context` |
| `promote` | Move an entry across scope or ownership. | `context`, `memory` |
| `recall` | Ranked multi-result retrieval across domains. | `tesseract` |
| `register` | Add an entry to a registry. | `context` |
| `search` | Rank records of the context store by vector similarity. | `context` |
| `set` | Move a record to a named value of a closed field. | `context` |
| `touch` | Report deliberate use, so it counts toward activation. | `tesseract` |
| `view` | Evaluate a view or selector and return what it matches. | `context` |
| `write` | Append a revision or record. | `context`, `event`, `knowledge`, `memory` |

**Exemptions.** Registered names that do not match the vocabulary, and why.

| Tool | Why |
|---|---|
| `context_rag_query` | `rag_query` is not an operation in the vocabulary, and it is the one name on the surface that does not fit. It remains unchanged for compatibility; the related `search` and `embed` operation names are part of the public vocabulary. |
| `tesseract_skills` | Named for what it serves rather than for a verb, and both its arms — the catalog and one skill body — are covered by the plural noun. Kept because it is the most-referenced identifier on the surface (every tool description ends in a `tesseract_skills <name>` pointer) and no verb form read better than the noun: it neither purely lists nor purely gets. |
<!-- END GENERATED: tool-naming -->

## Tool catalog

### Context

| Tool | Scope | HTTP peer | Notes |
|---|---|---|---|
| `context_write` | `write` | `POST /v1/context/write` | Append a record revision |
| `context_view` | — | `POST /v1/views/evaluate` | `full_evaluation` selects the arm: default is the summary envelope over namespace globs; `true` is the full selector + `evaluation_meta`, the exact peer of the HTTP route |
| `context_estimate` | — | `POST /v1/context/estimate` | Record count + bytes + token proxy without payload |
| `context_pack` | — | `POST /v1/context/pack` | `shape` selects the arm: `list` (default) ranks a named view; `packet` assembles namespace globs + pins into a budget-bounded packet + manifest (MCP-only — divergent shape from HTTP `/context/packet`) |
| `context_audit_list` | — | `GET /v1/context/audit` | Structured audit events |
| `context_typed_write` | `write` | `POST /v1/context/typed-write` | Write with schema-validated payload |
| `context_typed_view` | — | `POST /v1/context/typed-view` | Typed view over schema |
| `context_registry_list` | — | `GET /v1/context/types`, `GET /v1/context/views`, `GET /v1/namespaces/list`, `GET /v1/namespaces/get` | `kind` selects the registry: `types`, `views`, or `namespaces` (with `name` for one namespace's policy). Under `kind=namespaces` the list is filtered by `prefix`, or `match` with `match_mode` (`prefix`/`contains`/`glob`), plus `owner_type`/`owner_id`; ordered by `sort` (`namespace`/`owner`/`updated_at`) and `dir`; paged by `limit` and `cursor`. `total` counts the whole match and `next_cursor` carries the rest. Every list knob is a validation_error under `types`, `views`, or alongside `name`. |
| `context_ingest` | `write` | `POST /v1/context/bulk-ingest` | `mode` selects the arm: `bulk` (default) writes a list of records; `chunked` splits one document into auto-embedded chunks (MCP-only) |
| `context_status_set` | `write` | `POST /v1/context/status/promote`, `POST /v1/context/status/deprecate` | `status` names the target: omit to advance one step, `deprecated` to retire |
| `context_promote` | `promote.request` / `promote.approve` / `promote.apply` | `POST /v1/context/promote/request`, `/approve`, `/apply` | `stage` selects the stage AND the scope checked for it; an absent or unrecognized stage is a validation_error and authorizes nothing |
| `context_promotion_list` | — | — (MCP-only; HTTP equivalents iterate audit) | List promotion requests |
| `context_plan` | — | `POST /v1/context/plan`, `POST /v1/broker/plan` | `execute` selects the arm: `false` (default) returns the plan; `true` runs it and returns the records (MCP-only). Both routes are the same handler; both are peers of the default arm. |
| `context_namespace_register` | `namespace.admin` | `POST /v1/namespaces/register` | Register a namespace ownership policy; the HTTP peer checks the distinct `namespace.register` scope |
| `context_embed` | — | — (MCP-only) | Embedding-only op; uses the configured shared provider/model and returns `embedding_unavailable` when disabled |
| `context_search` | — | — (MCP-only) | Low-level embedding search; provider failures return `embedding_error` rather than an empty success |
| `context_rag_query` | — | — (MCP-only) | Convenience RAG query; requires the configured embedding provider |
| `context_session_write` | `write` | — (MCP-only) | Write a session snapshot record with an enforced schema, and embed it |

### Memory

| Tool | Scope | HTTP peer | Deeper | Notes |
|---|---|---|---|---|
| `memory_write` | `memory:write` | `POST /v1/memory/write` | `tesseract_skills memory` | New revision (optional semantic dedup); memory revisions cannot carry knowledge facets; optional `consumer_state` JSON bag |
| `memory_promote` | `memory:write` | `POST /v1/memory/promote` | `tesseract_skills promotion` | Promote session → user / project |

### Knowledge

| Tool | Scope | HTTP peer | Deeper | Notes |
|---|---|---|---|---|
| `knowledge_write` | `memory:write` | `POST /v1/knowledge/write` | `tesseract_skills knowledge` | Write with required canonical `kind`, non-empty `source`, and complete `pointer` facets (scheme `nil` when there is no external source) |

### Event

The append-only narrative log — reasoning in prose, not telemetry. Two properties set it apart from every other domain and both are visible on this surface: `tesseract_recall` does **not** search it unless you pass `domains: ["event"]`, and `ranking=activation` over it is an error rather than an answer. See `tesseract_skills event`.

| Tool | Scope | HTTP peer | Deeper | Notes |
|---|---|---|---|---|
| `event_write` | `memory:write` | `POST /v1/event/write` | `tesseract_skills event` | Append one log entry. `key` is optional and usually omitted — a log entry records that something happened, not a current value. Stamped `status=canonical`. |
| `event_list` | `memory:read` | `GET /v1/event/log` | `tesseract_skills event` | The linear read: chronological, keyset-paged, `direction` + `since`/`until`. No total, by design. Deprecated entries excluded. |

### Cross-domain

`domain` is an argument on the keyed reads: `context`, `memory`, `knowledge`, or `event`. It is required — there is no default, because inferring it from the namespace would answer the wrong question silently. A domain with no store wired answers `domain_unavailable`, which is a different fact from `not_found`.

The revision-level ops take no `domain`. Revisions of every domain share one table keyed by `revision_id`, so an id from any of them resolves without saying which it was.

`domain` is a **filter**, not a hint. A namespace does not identify a domain: `memory_state` *does* carry a `domain` column, but it is stamped once at creation and the head pointer it holds addresses `memory_revisions`, which memory and knowledge share — so resolving `(namespace, key)` returns whatever was written at that key, whichever domain wrote it. The `not_found` is therefore an explicit check on the **resolved revision's** domain (`GetCurrentInDomain`), not a property of the schema. Only a matching read reinforces, and the check runs *before* the reinforcement write — bumping a row that is then withheld would teach the ranking that a memory mattered on the strength of a read that never returned it.

Each of these covers several HTTP routes rather than one; the parity catalog carries one row per (tool, route) pair. The routes are unchanged and still wired.

**Argument name:** the MCP tools take `key`; their HTTP peers take `?memory_key=`. The retired per-domain tools took `memory_key` on both doors, so this is a rename on the MCP side only. The revision JSON still carries the field as `memory_key`. `memory_write` still takes `memory_key` too — aligning it is out of scope here.

| Tool | Scope | HTTP equivalents | Deeper | Notes |
|---|---|---|---|---|
| `tesseract_get` | `memory:read` for `memory`/`knowledge`/`event`; none for `context` | `GET /v1/context/head`, `GET /v1/memory/current`, `GET /v1/knowledge/current` | `tesseract_skills memory` | Current entry at (domain, namespace, key). `not_found` if the key holds another domain's revision. Reinforces under `memory` and `knowledge`, and only on a match; `context` has no activation state and `event` opts out of activation. Most events are keyless — read them with `event_list`. |
| `tesseract_history` | as above | `GET /v1/context/history`, `GET /v1/memory/history`, `GET /v1/knowledge/history` | `tesseract_skills revisions` | Revision history, newest-first, filtered to the named domain. Under `event` this is where a retracted entry stays findable. |
| `tesseract_recall` | `memory:read` | `POST /v1/tesseract/lookup`, `POST /v1/memory/recall` | `tesseract_skills recall-and-ranking` | Multi-knob recall over the curated corpus — memory + knowledge — (activation / chronological / similarity / relevance). Narrow with `domains`, which is also how you opt `event` in. |
| `tesseract_get_revision` | `memory:read` | `GET /v1/memory/revisions/{id}` | `tesseract_skills revisions` | Single revision by id, any domain. Reinforces the parent entry. |
| `tesseract_deprecate` | `memory:write` | `POST /v1/memory/deprecate` | `tesseract_skills revisions` | Deprecate a revision by id, any domain |
| `tesseract_touch` | `memory:read` | `POST /v1/memory/touch` | `tesseract_skills memory` | Report which recalled revisions actually shaped the turn, especially projected hits not deliberately fetched. Any domain's revision id resolves; an event id comes back under `not_reinforced`, since that domain opts out of activation. |

Under the default `revision_scope=current`, omitted `statuses` continue to hide
deprecated revisions. When `statuses` explicitly includes `deprecated`, current
scope additionally returns terminal deprecated revisions (deprecated revisions
with no incoming `supersedes` edge) and excludes ordinary superseded history.
Use `revision_scope=timeline` when the complete deprecated revision history is
the intended result.

### Meta

| Tool | Scope | HTTP peer | Deeper | Notes |
|---|---|---|---|---|
| `tesseract_skills` | — | — (MCP-only meta-tool) | self-documenting | Call with no args for the index; with `name` for the full skill body |

## Playbooks

### 1. Write a memory

```json
mcp__tesseract__memory_write {
  "namespace": "user/alex/memory/feedback",
  "memory_key": "boot_prompt_preference",
  "author_agent_id": "claude-code",
  "trigger": "explicit",
  "session_id": "2026-04-15:backend",
  "derived_from": "user",
  "confidence": 0.9,
  "payload_summary": "Prefer dense prose over bullets for boot prompts.",
  "payload_body": "User feedback 2026-04-15: pure prose sections scan faster during boot.",
  "tags": "[\"preference\",\"style\"]",
  "dedup": "semantic",
  "dedup_threshold": 0.85
}
```

Returns the created `memory.Revision`. Semantic dedup: same-key matches auto-supersede; cross-key matches surface as `DedupMatch`.

**`payload_data`** is an optional JSON object on any write door, holding the record's own fields in the caller's shape — an ADR's alternatives, a contact's phone (CW-20260912-0036). Tesseract validates that it parses, that it is an object, and that it is under a global 1 MiB ceiling — and nothing about its CONTENT: no typing, no schema enforcement, no required keys. The ceiling is a sanity limit, not a policy knob, and it is global rather than per-type. It is **not indexed, not embedded and not searched**, so anything that must be findable belongs in the prose as well. **The HTTP shape differs by domain** — `/v1/memory/write` nests it as `payload.data`, while `/v1/knowledge/write` and `/v1/event/write` take a top-level `data`, matching how those routes already take `summary` and `body` flat. Those routes decode strictly, so a wrong shape there is a `400` rather than a dropped field; the MCP tools do NOT — an undeclared argument name is silently ignored, so a wrong name over MCP is a successful write with the object missing. On read every domain returns it at `payload.data`. The optional `payload_data_schema_hash` records which schema the data claims; Tesseract stores that claim and never validates against it. See `tesseract_skills memory`.

**`consumer_state`** is an optional JSON object on any write door, holding the caller's own lifecycle data for that revision (CW-20260909-0036). Tesseract validates well-formed JSON, object-ness and the type's declared `required_fields`, and never reads a value out of it — no vocabulary, no transition checking. It is **not** the `state` block on a full-mode recall result, which is Tesseract's activation bookkeeping and is not writable. Filter on it with `state_filters` on `tesseract_recall` (HTTP: `POST /v1/tesseract/lookup`). `user/{id}/memory/todos` is the first type built on it; see `tesseract_skills memory`.

### 2. Write a knowledge entry

```json
mcp__tesseract__knowledge_write {
  "namespace": "user/alex/knowledge/framework",
  "key": "framework.go-providers",
  "kind": "package",
  "source": "filesystem",
  "pointer_scheme": "file",
  "pointer_locator": "/workspace/example-library",
  "summary": "Example library: a multi-provider adapter used by this project.",
  "body": "Exports embedding and completion interfaces.",
  "author_agent_id": "indexer",
  "session_id": "indexer:2026-04-15"
}
```

Namespace must contain a `knowledge` segment. Pointer `scheme`/`locator` are required. Confidence defaults to 0.9 if omitted.

### 3. Look up anything by topic

```json
mcp__tesseract__tesseract_recall {
  "namespaces": ["user/alex/memory", "user/alex/knowledge"],
  "query": "hybrid relevance recall ranking",
  "limit": 20
}
```

Searches memory + knowledge. Returns ranked results with a uniform shape so the agent doesn't need to know which domain a hit came from.

### 4. Pack context at boot

Use `context_plan` with `execute: true` (MCP-only — plan + packet in one call):

```json
mcp__tesseract__context_plan {
  "execute": true,
  "intent": "boot_project",
  "summary": "Tesseract backend — batch 1 parity work",
  "max_items": 80,
  "max_tokens_estimate": 8000
}
```

Or split the phases: `context_plan` with `execute` omitted returns the plan,
and `context_pack` with `shape: "packet"` assembles it.

### 5. Resolve a revision id

```json
mcp__tesseract__tesseract_get_revision { "revision_id": "01HXYZ…" }
```

Useful when a `tesseract_recall` hit references a revision you want to inspect in full. Every result carries `revision_id` under every `payload_mode`, so this hydrate step is always available. The deliberate fetch reinforces the parent entry once.

### 6. Close the loop after using what you recalled

```json
mcp__tesseract__tesseract_touch { "revision_ids": ["01HXYZ…"] }
```

Recall itself does not reinforce results — being returned by a search is the ranker's guess, not evidence it was right. Call this after the reasoning for projected hits that shaped the turn without a deliberate fetch. `tesseract_get` under `domain=memory` or `domain=knowledge`, and `tesseract_get_revision`, already reinforce once; touching the same hit adds a second reinforcement and should be intentional.

Under-reporting is fine; over-reporting is worse than silence, because it teaches the ranking that noise is signal. See `tesseract_skills memory` for the worked loop.

## Scopes

Capability tokens carry scope claims; tools check `checkScope(ctx, "<scope>")` before side-effecting operations. Known scopes:

- `write` — generic context mutations. `read` remains token metadata but no current MCP tool checks it. The planned `context_consistency_repair` tool is not registered; consistency repair remains HTTP/CLI-only.
- `memory:read` / `memory:write` — memory + knowledge domain.
- `promote.request`, `promote.approve`, `promote.apply` — context promotion workflow stages.
- `namespace.admin` — register / mutate namespace policies.
- `repair` — HTTP consistency, queue, trim, and compact mutations; no MCP tool currently checks it.

The default store-backed token scopes are `write`, `promote.request`,
`promote.approve`, `promote.apply`, `packet`, `repair`, and
`namespace.register`. MCP-only `memory:read`, `memory:write`, and
`namespace.admin` must be requested explicitly.

## Related

- `README.md` — project top-level (links here).
- `docs/SPECS/MCP.md` / `docs/SPECS/API.md` — protocol specs.
- `tests/parity/parity_test.go` — drift guardrail for the MCP registry and its documented HTTP peers.
