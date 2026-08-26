# Tesseract — MCP Tools (agent reference)

This is the agent-facing catalog for Tesseract's MCP surface. Every tool
here is registered by `tesseract mcp` and has an HTTP peer under
`/v1/*` unless the row is marked **MCP-only**.

> Single source of truth for what's reachable on both surfaces lives in
> `tests/parity/parity_test.go::surfaceCatalog`. Adding a tool or route
> there without an entry fails CI.

## Quick facts

- **Transport:** stdio (launched by Claude Code or another MCP host). Configure in your MCP client:
  ```json
  {
    "mcpServers": {
      "tesseract": {
        "type": "stdio",
        "command": "/Users/<you>/go/bin/tesseract",
        "args": ["mcp", "--token", "<hex-capability-token>"]
      }
    }
  }
  ```
- **Tool ID prefix:** `mcp__tesseract__` (Claude side). Example: `mcp__tesseract__memory_write`.
- **Data root:** the XDG layout by default — `~/.local/share/tesseract` for data,
  `~/.local/state/tesseract` for state — shared with the HTTP server. Run
  `tesseract path` to print the resolved layout rather than trusting this line;
  it creates nothing.
- **Capability token:** required for any tool that requires a `write`/`read`/`promote` scope. Token claims are checked per-tool.
- **No duplicated logic:** every tool and its HTTP peer call the same store/domain function. Responses match 1:1.

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
| `knowledge` | domain | Pointer-first model, `kind`/`source`/`pointer` facets. |
| `context-packet` | feature | Boot workflows, broker plan/fetch, budget tuning. |
| `audit` | feature | Querying the audit log. |

Workflow-specific skills for downstream apps belong in those app repos. Tesseract ships primitives and reference docs only.

## Domains

- **Context** — generic revisioned key-value records. Read/write, typed schemas, views, packet assembly, promotion workflow, embeddings, audit. Several of these tools carry an arm selector (`shape`, `mode`, `stage`, `kind`, `execute`, `include_meta`) rather than being split into one tool per fidelity; the catalog below names the selector on each.
- **Memory** — append-only agent memory revisions with recall (activation/chronological/similarity rankings).
- **Knowledge** — pointer-first references to external content (package, doc, note) with structured facets. Backed by the memory revision store with `domain=knowledge`.
- **Cross-domain** — one `get`, one `history`, one `recall`, and two revision-level ops that span every domain. `domain` is an argument, not a tool-name prefix.

## Tool catalog

### Context

| Tool | Scope | HTTP peer | Notes |
|---|---|---|---|
| `context_write` | `write` | `POST /v1/context/write` | Append a record revision |
| `context_view` | — | `POST /v1/views/evaluate` | `include_meta` selects the arm: default is the summary envelope over namespace globs; `true` is the full selector + `evaluation_meta`, the exact peer of the HTTP route |
| `context_estimate` | — | `POST /v1/context/estimate` | Record count + bytes + token proxy without payload |
| `context_pack` | — | `POST /v1/context/pack` | `shape` selects the arm: `list` (default) ranks a named view; `packet` assembles namespace globs + pins into a budget-bounded packet + manifest (MCP-only — divergent shape from HTTP `/context/packet`) |
| `context_audit` | — | `GET /v1/context/audit` | Structured audit events |
| `context_typed_write` | `write` | `POST /v1/context/typed-write` | Write with schema-validated payload |
| `context_typed_view` | — | `POST /v1/context/typed-view` | Typed view over schema |
| `context_registry_list` | — | `GET /v1/context/types`, `GET /v1/context/views`, `GET /v1/namespaces/list`, `GET /v1/namespaces/get` | `kind` selects the registry: `types`, `views`, or `namespaces` (with `name` for one namespace's policy) |
| `context_ingest` | `write` | `POST /v1/context/bulk-ingest` | `mode` selects the arm: `bulk` (default) writes a list of records; `chunked` splits one document into auto-embedded chunks (MCP-only) |
| `context_status_set` | `write` | `POST /v1/context/status/promote`, `POST /v1/context/status/deprecate` | `status` names the target: omit to advance one step, `deprecated` to retire |
| `context_promote` | `promote.request` / `promote.approve` / `promote.apply` | `POST /v1/context/promote/request`, `/approve`, `/apply` | `stage` selects the stage AND the scope checked for it; an absent or unrecognized stage is a validation_error and authorizes nothing |
| `context_promote_list` | — | — (MCP-only; HTTP equivalents iterate audit) | List promotion requests |
| `context_broker` | — | `POST /v1/broker/plan` (alias `/v1/context/plan`) | `execute` selects the arm: `false` (default) returns the plan; `true` runs it and returns the records (MCP-only) |
| `context_namespace_register` | `namespace.admin` | `POST /v1/namespaces/register` | Register a namespace ownership policy |
| `context_embed` | — | — (MCP-only) | Embedding-only op |
| `context_search` | — | — (MCP-only) | Low-level embedding search |
| `context_rag_query` | — | — (MCP-only) | Convenience RAG query |
| `context_session_snapshot` | — | — (MCP-only) | Capture a session snapshot |

### Memory

| Tool | Scope | HTTP peer | Deeper | Notes |
|---|---|---|---|---|
| `memory_write` | `memory:write` | `POST /v1/memory/write` | `tesseract_skills memory` | New revision (optional semantic dedup) |
| `memory_promote` | `memory:write` | `POST /v1/memory/promote` | `tesseract_skills promotion` | Promote session → user / project |

### Knowledge

| Tool | Scope | HTTP peer | Deeper | Notes |
|---|---|---|---|---|
| `knowledge_write` | `memory:write` | `POST /v1/knowledge/write` | `tesseract_skills knowledge` | Pointer-first write with `kind`/`source`/`pointer` facets |

### Cross-domain

`domain` is an argument on the keyed reads: `context`, `memory`, or `knowledge`. It is required — there is no default, because inferring it from the namespace would answer the wrong question silently. A domain with no store wired answers `domain_unavailable`, which is a different fact from `not_found`.

The revision-level ops take no `domain`. Memory and knowledge revisions share one table keyed by `revision_id`, so an id from either resolves without saying which it was.

`domain` is a **filter**, not a hint. A namespace does not identify a domain — `memory_state` has no domain column and both domains share `memory_revisions` — so a keyed read that named `memory` and found a knowledge revision at that key returns `not_found` rather than the other domain's row. Only a matching read reinforces.

Each of these covers several HTTP routes rather than one; the parity catalog carries one row per (tool, route) pair. The routes are unchanged and still wired.

**Argument name:** the MCP tools take `key`; their HTTP peers take `?memory_key=`. The retired per-domain tools took `memory_key` on both doors, so this is a rename on the MCP side only. The revision JSON still carries the field as `memory_key`. `memory_write` still takes `memory_key` too — aligning it is out of scope here.

| Tool | Scope | HTTP equivalents | Deeper | Notes |
|---|---|---|---|---|
| `tesseract_get` | `memory:read` for `memory`/`knowledge`; none for `context` | `GET /v1/context/head`, `GET /v1/memory/current`, `GET /v1/knowledge/current` | `tesseract_skills memory` | Current entry at (domain, namespace, key). `not_found` if the key holds another domain's revision. Reinforces under `memory` only, and only on a match. |
| `tesseract_history` | as above | `GET /v1/context/history`, `GET /v1/memory/history`, `GET /v1/knowledge/history` | `tesseract_skills revisions` | Revision history, newest-first, filtered to the named domain |
| `tesseract_recall` | `memory:read` | `POST /v1/tesseract/lookup`, `POST /v1/memory/recall` | `tesseract_skills recall-and-ranking` | Multi-knob recall over memory + knowledge (activation / chronological / similarity / relevance). Narrow with `domains`. |
| `tesseract_get_revision` | `memory:read` | `GET /v1/memory/revisions/{id}` | `tesseract_skills revisions` | Single revision by id, any domain |
| `tesseract_deprecate` | `memory:write` | `POST /v1/memory/deprecate` | `tesseract_skills revisions` | Deprecate a revision by id, any domain |
| `tesseract_touch` | `memory:read` | `POST /v1/memory/touch` | `tesseract_skills memory` | Report which recalled revisions actually shaped the turn — the only input activation has. Memory and knowledge revision ids both resolve. |

### Meta

| Tool | Scope | HTTP peer | Deeper | Notes |
|---|---|---|---|---|
| `tesseract_skills` | — | — (MCP-only meta-tool) | self-documenting | Call with no args for the index; with `name` for the full skill body |

## Playbooks

### 1. Write a memory

```json
mcp__tesseract__memory_write {
  "namespace": "user/chrispian/memory/feedback",
  "memory_key": "boot-prompt-preference",
  "author_agent_id": "claude-code",
  "trigger": "explicit",
  "session_id": "2026-04-15:backend",
  "origin": "user",
  "confidence": 0.9,
  "payload_summary": "Prefer dense prose over bullets for boot prompts.",
  "payload_body": "User feedback 2026-04-15: pure prose sections scan faster during boot.",
  "tags": "[\"preference\",\"style\"]",
  "dedup": "semantic",
  "dedup_threshold": 0.85
}
```

Returns the created `memory.Revision`. Semantic dedup: same-key matches auto-supersede; cross-key matches surface as `DedupMatch`.

### 2. Write a knowledge entry

```json
mcp__tesseract__knowledge_write {
  "namespace": "user/chrispian/knowledge/framework",
  "key": "framework.go-providers",
  "kind": "package",
  "source": "filesystem",
  "pointer_scheme": "file",
  "pointer_locator": "/Users/chrispian/Projects-apps/framework/libs/go-providers",
  "summary": "go-providers: multi-provider AI adapter (Anthropic, OpenAI, Ollama, …) used by Tesseract + others.",
  "body": "Exports provider.Embedder and provider.Completer.",
  "author_agent_id": "indexer",
  "session_id": "indexer:2026-04-15"
}
```

Namespace must contain a `knowledge` segment. Pointer `scheme`/`locator` are required. Confidence defaults to 0.9 if omitted.

### 3. Look up anything by topic

```json
mcp__tesseract__tesseract_recall {
  "namespaces": ["user/chrispian/memory", "user/chrispian/knowledge"],
  "query": "hybrid relevance recall ranking",
  "limit": 20
}
```

Searches memory + knowledge. Returns ranked results with a uniform shape so the agent doesn't need to know which domain a hit came from.

### 4. Pack context at boot

Use `context_broker` with `execute: true` (MCP-only — plan + packet in one call):

```json
mcp__tesseract__context_broker {
  "execute": true,
  "intent": "boot_project",
  "summary": "Tesseract backend — batch 1 parity work",
  "budget_items": 80,
  "budget_tokens": 8000
}
```

Or split the phases: `context_broker` with `execute` omitted returns the plan,
and `context_pack` with `shape: "packet"` assembles it.

### 5. Resolve a revision id

```json
mcp__tesseract__tesseract_get_revision { "revision_id": "01HXYZ…" }
```

Useful when a `tesseract_recall` hit references a revision you want to inspect in full. Every result carries `revision_id` under every `payload_mode`, so this hydrate step is always available.

### 6. Close the loop after using what you recalled

```json
mcp__tesseract__tesseract_touch { "revision_ids": ["01HXYZ…"] }
```

Recall returns results **unreinforced** — being returned by a search is the ranker's guess, not evidence it was right. Call this after the reasoning, naming only the revisions that actually shaped the turn. It is the only input `ranking=activation` has.

Under-reporting is fine; over-reporting is worse than silence, because it teaches the ranking that noise is signal. See `tesseract_skills memory` for the worked loop.

## Scopes

Capability tokens carry scope claims; tools check `checkScope(ctx, "<scope>")` before side-effecting operations. Known scopes:

- `read` / `write` — generic context ops.
- `memory:read` / `memory:write` — memory + knowledge domain.
- `promote`, `promote.request`, `promote.approve`, `promote.apply` — promotion workflow stages.
- `namespace.admin` — register / mutate namespace policies.
- `repair` — consistency repair (HTTP-only today; `context_consistency_repair` MCP tool is batch 2).

## Related

- `README.md` — project top-level (links here).
- `docs/SPECS/MCP.md` / `docs/SPECS/API.md` — protocol specs.
- `tests/parity/parity_test.go` — drift guardrail; every MCP tool + HTTP route must appear in its catalog.
