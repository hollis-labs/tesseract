# Vanta Conduit — MCP Tools (agent reference)

This is the agent-facing catalog for Vanta Conduit's MCP surface. Every tool
here is registered by `contextd mcp` and has an HTTP peer under
`/v1/*` unless the row is marked **MCP-only**.

> Single source of truth for what's reachable on both surfaces lives in
> `tests/parity/parity_test.go::surfaceCatalog`. Adding a tool or route
> there without an entry fails CI.

## Quick facts

- **Transport:** stdio (launched by Claude/Cerberus/custom host). Configure in `~/.claude.json`:
  ```json
  {
    "mcpServers": {
      "vanta": {
        "type": "stdio",
        "command": "/Users/<you>/go/bin/contextd",
        "args": ["mcp", "--token", "<hex-capability-token>"],
        "env": { "CONTEXTD_ROOT": "/Users/<you>/.conduit" }
      }
    }
  }
  ```
- **Tool ID prefix:** `mcp__vanta__` (Claude side). Example: `mcp__vanta__memory_write`.
- **Data root:** `~/.conduit/` — shared with the HTTP server run by Cerberus. Do NOT point MCP at `~/.cortex/` (legacy ghost).
- **Capability token:** required for any tool that requires a `write`/`read`/`promote` scope. Token claims are checked per-tool.
- **No duplicated logic:** every tool and its HTTP peer call the same store/domain function. Responses match 1:1.

## Domains

- **Context** — generic revisioned key-value records. Thirty-ish tools for read/write, typed schemas, views, packet assembly, promotion workflow, embeddings, audit.
- **Memory** — append-only agent memory revisions with recall (activation/chronological/similarity rankings).
- **Knowledge** — pointer-first references to external content (package, doc, note) with structured facets. Backed by the memory revision store with `domain=knowledge`.
- **Unified** — `conduit_lookup` searches memory + knowledge together.

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

| Tool | Scope | HTTP peer | Notes |
|---|---|---|---|
| `memory_write` | `memory:write` | `POST /v1/memory/write` | New revision (optional semantic dedup) |
| `memory_get` | `memory:read` | `GET /v1/memory/current?namespace=&memory_key=` | Current revision for a keyed memory |
| `memory_history` | `memory:read` | `GET /v1/memory/history?namespace=&memory_key=` | Revision history, newest-first |
| `memory_recall` | `memory:read` | `POST /v1/memory/recall` | Multi-knob recall (activation / chronological / similarity) |
| `memory_get_revision` | `memory:read` | `GET /v1/memory/revisions/{id}` | Single revision by id |
| `memory_promote` | `memory:write` | `POST /v1/memory/promote` | Promote session → user / project |
| `memory_deprecate` | `memory:write` | `POST /v1/memory/deprecate` | Deprecate a revision by id |

### Knowledge

| Tool | Scope | HTTP peer | Notes |
|---|---|---|---|
| `knowledge_write` | `memory:write` | `POST /v1/knowledge/write` | Pointer-first write with `kind`/`source`/`pointer` facets |
| `knowledge_get` | `memory:read` | `GET /v1/knowledge/current?namespace=&key=` | Current knowledge revision for (namespace, key) |
| `knowledge_history` | `memory:read` | `GET /v1/knowledge/history?namespace=&key=` | Full history for a knowledge entry |

### Unified

| Tool | Scope | HTTP peer | Notes |
|---|---|---|---|
| `conduit_lookup` | `memory:read` | `POST /v1/conduit/lookup` | Search memory + knowledge together |

## Playbooks

### 1. Write a memory

```json
mcp__vanta__memory_write {
  "namespace": "user/chrispian/memory",
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
mcp__vanta__knowledge_write {
  "namespace": "user/chrispian/knowledge/framework",
  "key": "framework.go-providers",
  "kind": "package",
  "source": "filesystem",
  "pointer_scheme": "file",
  "pointer_locator": "/Users/chrispian/Projects-apps/framework/libs/go-providers",
  "summary": "go-providers: multi-provider AI adapter (Anthropic, OpenAI, Ollama, …) used by Conduit + others.",
  "body": "Exports provider.Embedder and provider.Completer. Replace-directive in consumer go.mod.",
  "author_agent_id": "indexer",
  "session_id": "indexer:2026-04-15"
}
```

Namespace must contain a `knowledge` segment. Pointer `scheme`/`locator` are required. Confidence defaults to 0.9 if omitted.

### 3. Look up anything by topic

```json
mcp__vanta__conduit_lookup {
  "query": "hybrid relevance recall ranking",
  "limit": 20
}
```

Searches memory + knowledge. Returns ranked results with a uniform shape so the agent doesn't need to know which domain a hit came from.

### 4. Pack context at boot

Use `context_broker_fetch` (MCP-only convenience — plan + packet in one call):

```json
mcp__vanta__context_broker_fetch {
  "intent": "boot_project",
  "summary": "Vanta Conduit backend — batch 1 parity work",
  "budget_items": 80,
  "budget_tokens": 8000,
  "payload_mode": "full"
}
```

Or split the phases manually with `context_broker_plan` + `context_packet`.

### 5. Resolve a revision id

```json
mcp__vanta__memory_get_revision { "revision_id": "01HXYZ…" }
```

Useful when a `memory_recall` hit references a revision you want to inspect in full (`conduit_lookup` results surface revision ids too).

## Scopes

Capability tokens carry scope claims; tools check `checkScope(ctx, "<scope>")` before side-effecting operations. Known scopes:

- `read` / `write` — generic context ops.
- `memory:read` / `memory:write` — memory + knowledge domain.
- `promote`, `promote.request`, `promote.approve`, `promote.apply` — promotion workflow stages.
- `namespace.admin` — register / mutate namespace policies.
- `repair` — consistency repair (HTTP-only today; `context_consistency_repair` MCP tool is batch 2).

## Related

- `.agentrc/boot-prompt.md` — session-boot context (links here).
- `README.md` — project top-level (links here).
- `docs/SPECS/MCP.md` / `docs/SPECS/API.md` — protocol specs.
- `tests/parity/parity_test.go` — drift guardrail; every MCP tool + HTTP route must appear in its catalog.
