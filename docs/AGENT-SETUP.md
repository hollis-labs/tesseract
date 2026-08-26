# Agent Setup Guide

This guide covers the supported path for using Tesseract as a persistent memory backend for Claude Code or another MCP-compatible client.

## What this gives an agent

Tesseract over MCP gives an agent:

- deterministic read access to context, memory, knowledge, and audit data
- optional scoped write access to `app/*` namespaces
- promotion requests into protected `user/*` namespaces
- budget-bounded context packet loading for session boot

## Install and prerequisites

Install `tesseract` first:

```bash
go install github.com/hollis-labs/tesseract/cmd/tesseract@latest
```

Then make sure you have:

- a Tesseract config file in the normal app config location
- provider env vars if you want embeddings or synthesis
- a capability token if the agent needs mutating tools

See [QUICKSTART.md](QUICKSTART.md) for the base install and provider setup.

## Create a capability token

Read-only tools do not require a token. Mutating tools do.

Typical agent token:

```bash
tesseract context token create \
  --name claude-agent \
  --client-id app:claude \
  --scopes write,promote.request \
  --namespaces "app/claude/*" \
  --ttl 8760h
```

Copy the raw token value immediately.

Human operator token:

```bash
tesseract context token create \
  --name operator \
  --client-id user \
  --scopes promote.approve,promote.apply,namespace.admin \
  --namespaces "*" \
  --ttl 8760h
```

## Add Tesseract to `.mcp.json`

Project-local example:

```json
{
  "mcpServers": {
    "tesseract": {
      "command": "tesseract",
      "args": ["mcp", "--token", "<paste-token-here>"],
      "env": {
        "OPENAI_API_KEY": "<optional-for-embeddings>",
        "ANTHROPIC_API_KEY": "<optional-for-synthesis>"
      }
    }
  }
}
```

You can place this in:

- project root `.mcp.json` for one repository
- global Claude Code MCP config if you want it everywhere

See [`../examples/mcp.json`](../examples/mcp.json) for the sample file in this repo.

## Restart the MCP client

After adding or changing MCP config, restart Claude Code or your MCP client so the tool registry refreshes.

## Tool groups

The most important tool groups for agents are:

- `context_*` for record reads, writes, packets, promotions, namespace policy, and audit
- `memory_*` for memory-domain workflows when the memory store is enabled
- `knowledge_*` for pointer-first knowledge records when the knowledge store is enabled

For the complete catalog, see [MCP_TOOLS.md](MCP_TOOLS.md).

## Recommended agent workflow

For a typical Claude Code session:

1. Boot from `context_pack` with `shape: "packet"`, or from `context_plan` with `execute: true`.
2. Read current app session state from `app/<agent>/*`.
3. Write new session state only inside the namespaces granted by the token.
4. Request promotions instead of writing directly to `user/*`.
5. Use embedding-backed tools only when providers are configured.

## Minimal tool examples

### Read a head record

```json
{
  "namespace": "app/claude/session/task-001",
  "key": "state"
}
```

### Load a session packet

`context_pack` with `shape: "packet"`:

```json
{
  "shape": "packet",
  "namespaces": "app/claude/session/*,user/memory/*",
  "include_pins": true,
  "max_items": 50,
  "max_tokens_estimate": 8000
}
```

Add `"payload_max_bytes": 512` to survey a wide namespace set cheaply. A capped
item carries `payload_head`, `payload_truncated` and `payload_bytes` in place of
`payload`.

### Write app state

```json
{
  "namespace": "app/claude/session/task-001",
  "key": "state",
  "payload": "{\"status\":\"in_progress\",\"step\":3}",
  "actor": "app:claude",
  "record_type": "state"
}
```

### Request a promotion

```json
{
  "source_namespace": "app/claude/session/task-001",
  "source_key": "summary",
  "target_namespace": "user/memory/claude",
  "target_key": "task-001-summary",
  "reason": "session complete, promoting for long-term memory",
  "actor": "app:claude"
}
```

## Scope model

Common capability scopes:

- `write`
- `promote.request`
- `promote.approve`
- `promote.apply`
- `namespace.admin`

The token must also allow the target namespace glob.

## Common failures

### `auth_required`

The MCP server was started without `--token`, or the configured token is invalid or expired.

### `insufficient_scope`

The token exists but does not include the capability required by the tool call.

### `embedding_unavailable`

No supported embedding provider is configured, or the required API key is missing.

## Optional project guidance

MCP tool descriptions are usually enough for discovery, but you can add a short project-specific usage note for agents. If you want a copy-paste snippet, see [CONTEXT-FOR-PROJECTS.md](CONTEXT-FOR-PROJECTS.md).
