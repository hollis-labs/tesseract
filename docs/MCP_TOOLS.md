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
        "args": ["mcp", "--token", "<hex-capability-token>"],
        "env": { "XDG_DATA_HOME": "/Users/<you>/.tesseract", "XDG_STATE_HOME": "/Users/<you>/.tesseract" }
      }
    }
  }
  ```
- **Tool ID prefix:** `mcp__tesseract__` (Claude side). Example: `mcp__tesseract__memory_write`.
- **Data root:** `~/.tesseract/` by default, shared with the HTTP server unless you override the config paths.
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

- **Context** — generic revisioned key-value records. Thirty-ish tools for read/write, typed schemas, views, packet assembly, promotion workflow, embeddings, audit.
- **Memory** — append-only agent memory revisions with recall (activation/chronological/similarity rankings).
- **Knowledge** — pointer-first references to external content (package, doc, note) with structured facets. Backed by the memory revision store with `domain=knowledge`.
- **Unified** — `tesseract_lookup` searches memory + knowledge together.

## Tool catalog

### Context

| Tool | Scope | HTTP peer | Notes |
|---|---|---|---|
| `context_write` | `write` | `POST /v1/context/write` | Append a record revision |
| `context_head` | — | `GET /v1/context/head` | Latest revision by namespace/key |
| `context_history` | — | `GET /v1/context/history` | All revisions, newest-first |
| `context_view` | — | — (MCP-only; use `views_evaluate` for full-power selector) | Simplified view over namespace globs |
| `views_evaluate` | — | `POST /v1/views/evaluate` | Full selector + evaluation_meta |
| `context_estimate` | — | `POST /v1/context/estimate` | Record count + bytes + token proxy without payload |
| `context_packet` | — | — (MCP-only — divergent shape from HTTP `/context/packet`) | Budget-bounded context packet + manifest |
| `context_pack` | — | `POST /v1/context/pack` | Pack context as ordered list |
| `context_audit` | — | `GET /v1/context/audit` | Structured audit events |
| `context_typed_write` | `write` | `POST /v1/context/typed-write` | Write with schema-validated payload |
| `context_typed_view` | — | `POST /v1/context/typed-view` | Typed view over schema |
| `context_types_list` | — | `GET /v1/context/types` | List registered types |
| `context_views_list` | — | `GET /v1/context/views` | List registered views |
| `context_bulk_ingest` | `write` | `POST /v1/context/bulk-ingest` | Multi-record ingest in one call |
| `context_status_promote` | `promote` | `POST /v1/context/status/promote` | Move a record's status (e.g. draft → canonical) |
| `context_status_deprecate` | `promote` | `POST /v1/context/status/deprecate` | Mark a record deprecated |
| `context_promote_request` | `promote.request` | `POST /v1/context/promote/request` | Open a cross-namespace promotion |
| `context_promote_list` | — | — (MCP-only; HTTP equivalents iterate audit) | List promotion requests |
| `context_promote_approve` | `promote.approve` | `POST /v1/context/promote/approve` | Approve a pending request |
| `context_promote_apply` | `promote.apply` | `POST /v1/context/promote/apply` | Apply an approved promotion |
| `context_broker_plan` | — | `POST /v1/broker/plan` (alias `/v1/context/plan`) | Build a fetch plan from intent |
| `context_broker_fetch` | — | — (MCP-only; plan + packet in one call) | Convenience wrapper |
| `context_namespace_register` | `namespace.admin` | `POST /v1/namespaces/register` | Register a namespace ownership policy |
| `context_namespace_show` | — | `GET /v1/namespaces/get` | Inspect a namespace policy |
| `context_namespaces_list` | — | — (MCP-only list helper) | List registered namespaces |
| `context_embed` | — | — (MCP-only) | Embedding-only op |
| `context_search` | — | — (MCP-only) | Low-level embedding search |
| `context_rag_query` | — | — (MCP-only) | Convenience RAG query |
| `context_session_snapshot` | — | — (MCP-only) | Capture a session snapshot |
| `context_chunked_ingest` | `write` | — (MCP-only) | Streamed multi-chunk ingest |

### Memory

| Tool | Scope | HTTP peer | Deeper | Notes |
|---|---|---|---|---|
| `memory_write` | `memory:write` | `POST /v1/memory/write` | `tesseract_skills memory` | New revision (optional semantic dedup) |
| `memory_get` | `memory:read` | `GET /v1/memory/current?namespace=&memory_key=` | `tesseract_skills memory` | Current revision for a keyed memory |
| `memory_history` | `memory:read` | `GET /v1/memory/history?namespace=&memory_key=` | `tesseract_skills revisions` | Revision history, newest-first |
| `memory_recall` | `memory:read` | `POST /v1/memory/recall` | `tesseract_skills recall-and-ranking` | Multi-knob recall (activation / chronological / similarity / relevance) |
| `memory_get_revision` | `memory:read` | `GET /v1/memory/revisions/{id}` | `tesseract_skills revisions` | Single revision by id |
| `memory_promote` | `memory:write` | `POST /v1/memory/promote` | `tesseract_skills promotion` | Promote session → user / project |
| `memory_deprecate` | `memory:write` | `POST /v1/memory/deprecate` | `tesseract_skills revisions` | Deprecate a revision by id |

### Knowledge

| Tool | Scope | HTTP peer | Deeper | Notes |
|---|---|---|---|---|
| `knowledge_write` | `memory:write` | `POST /v1/knowledge/write` | `tesseract_skills knowledge` | Pointer-first write with `kind`/`source`/`pointer` facets |
| `knowledge_get` | `memory:read` | `GET /v1/knowledge/current?namespace=&memory_key=` | `tesseract_skills knowledge` | Current knowledge revision for (namespace, memory_key) |
| `knowledge_history` | `memory:read` | `GET /v1/knowledge/history?namespace=&memory_key=` | `tesseract_skills revisions` | Full history for a knowledge entry |

### Unified

| Tool | Scope | HTTP peer | Deeper | Notes |
|---|---|---|---|---|
| `tesseract_lookup` | `memory:read` | `POST /v1/tesseract/lookup` | `tesseract_skills recall-and-ranking` | Search memory + knowledge together |
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
mcp__tesseract__tesseract_lookup {
  "query": "hybrid relevance recall ranking",
  "limit": 20
}
```

Searches memory + knowledge. Returns ranked results with a uniform shape so the agent doesn't need to know which domain a hit came from.

### 4. Pack context at boot

Use `context_broker_fetch` (MCP-only convenience — plan + packet in one call):

```json
mcp__tesseract__context_broker_fetch {
  "intent": "boot_project",
  "summary": "Tesseract backend — batch 1 parity work",
  "budget_items": 80,
  "budget_tokens": 8000,
  "payload_mode": "full"
}
```

Or split the phases manually with `context_broker_plan` + `context_packet`.

### 5. Resolve a revision id

```json
mcp__tesseract__memory_get_revision { "revision_id": "01HXYZ…" }
```

Useful when a `memory_recall` hit references a revision you want to inspect in full (`tesseract_lookup` results surface revision ids too).

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
