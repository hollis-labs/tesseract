# Agent and MCP setup

Tesseract's MCP stdio server gives a trusted local agent deterministic context
reads, memory/knowledge recall, scoped writes, promotion requests, and
budget-bounded session boot.

## Prerequisites

Build Tesseract from source as described in [Quick start](QUICKSTART.md). A
separate HTTP daemon is not required: the MCP host starts `tesseract mcp`, which
opens the local store itself.

Provider keys are optional. Without them, context and lexical memory/knowledge
workflows remain available; semantic retrieval and synthesis are unavailable.

## Create a capability token

Read-only context tools in the local MCP adapter do not require a token.
Memory and knowledge reads require `memory:read`, and mutations require their
operation-specific scopes. Create a token with the smallest scope and namespace
set the agent needs:

```bash
tesseract context token create \
  --name coding-agent \
  --client-id app:coding-agent \
  --scopes write,promote.request,memory:read \
  --namespaces 'app/coding-agent/*' \
  --ttl 8760h
```

The plaintext token is shown once. Add `memory:write` only if the agent should
write the memory/knowledge revision store. Namespace globs and scopes prevent
ungranted mutations, but they do not turn the local MCP process into a fully
read-isolated tenant: several context and audit reads remain available.

Promotion approval and apply should normally use a different operator token
with `promote.approve` and/or `promote.apply`, rather than adding those scopes
to every coding agent.

## Configure the MCP host

Start from [`../examples/mcp.json`](../examples/mcp.json):

```json
{
  "mcpServers": {
    "tesseract": {
      "command": "tesseract",
      "args": ["mcp", "--token", "<capability-token>"]
    }
  }
}
```

Prefer a secret-aware host configuration or a protected user-level config over
committing the raw token to a repository. Provider credentials should come from
the host process environment or its secret manager. If you put a token in a
local config file, restrict the file permissions and keep it out of source
control.

Restart the MCP host after changing its configuration so it refreshes the tool
registry.

## Discover before calling

Call `tesseract_skills` first, then load `start-here` and the domain-specific
skill needed for the task. The shipped schemas and skill text are more reliable
than a copied tool list in a project prompt.

The main groups are:

- `context_*` for context records, views, packets, plans, promotion, and audit
- `memory_*` for memory writes and domain workflows
- `knowledge_*` for pointer-backed knowledge writes
- `tesseract_*` for cross-domain reads, history, recall, revision access, and touch

The full inventory is in [MCP tools](MCP_TOOLS.md).

## Recommended session workflow

1. Use `context_plan` with `execute: true` for an intent-driven boot, or
   `context_pack` with `shape: "packet"` when the namespace set is known.
2. Read exact current values with `tesseract_get`, always naming the `domain`.
3. Write working state only beneath the agent's `app/<id>/*` grant.
4. Request promotion into protected user memory; do not silently write around
   the ownership boundary.
5. Hydrate only the recall results that matter, then touch summary-only results
   only when they actually informed the work.

## Minimal calls

Load a session packet:

```json
{
  "shape": "packet",
  "namespaces": "app/coding-agent/session/*",
  "include_pins": false,
  "max_items": 50,
  "max_tokens_estimate": 8000
}
```

Read an exact context head:

```json
{
  "domain": "context",
  "namespace": "app/coding-agent/session/task-001",
  "key": "state"
}
```

Write state:

```json
{
  "namespace": "app/coding-agent/session/task-001",
  "key": "state",
  "payload": "{\"status\":\"in_progress\",\"step\":3}",
  "actor": "app:coding-agent",
  "record_type": "state"
}
```

Request promotion:

```json
{
  "stage": "request",
  "source_namespace": "app/coding-agent/session/task-001",
  "source_key": "summary",
  "target_namespace": "user/memory/coding-agent",
  "target_key": "task-001-summary",
  "reason": "retain the reviewed session outcome",
  "actor": "app:coding-agent"
}
```

## Data boundary for agents

An MCP client can cause outbound provider calls when it invokes embedding,
semantic recall, RAG, or other provider-backed tools. OpenAI receives record
text or query text for embeddings. The HTTP synthesis surface can send the
question plus selected summaries and bodies to OpenAI or Anthropic. Review
[data egress](OPERATIONS.md#outbound-connections-and-data-egress) before making
provider-backed tools available to an agent.

## Common failures

### `auth_required`

A mutating MCP tool was called without a configured token, or the token is no
longer valid.

### `insufficient_scope`

The token does not contain the capability required by the selected operation or
promotion stage.

### `namespace_not_permitted`

The target does not match the token's namespace globs.

### `embedding_unavailable`

No supported embedder is available. Configure OpenAI and its API key or use a
lexical/deterministic operation.

For a compact project prompt, see [Project integration](CONTEXT-FOR-PROJECTS.md).
