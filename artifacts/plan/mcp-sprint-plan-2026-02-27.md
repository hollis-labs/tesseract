---
type: plan
title: "MCP Adapter Sprint Plan"
date: 2026-02-27
status: active
tasks: [TASK-20260228-020, TASK-20260228-021, TASK-20260228-022]
supersedes: [TASK-20260227-019, TASK-20260227-020]
---

# MCP Adapter Sprint Plan

## Objective

Implement a full-surface MCP adapter so local agents (Claude Code, etc.) can use
the context memory service as a memory layer immediately. Collapse the original
phase-1/phase-2/phase-3 deferred task split into a single sprint — for local agent
testing we need read + write + packet; read-only is not useful.

## What changes vs. the original deferred tasks

Original TASK-20260227-019/020 split tools across two phases with a gate between them.
That gate was a production rollout risk control. For local-first agent dev use, we ship
all tool surfaces in one sprint with auth enforced by capability tokens (already built in
phase 4). No phased gate needed; the tools are already safe by construction.

## Architecture

### Transport: stdio

MCP stdio is the standard for local-first usage. The adapter runs as a subprocess with
stdin/stdout as the JSON-RPC channel. This is exactly how Claude Code registers MCP
servers in `.mcp.json`.

### Entry point: `contextd --mcp`

A `--mcp` flag on `contextd` starts the MCP adapter instead of the HTTP server. One
binary, two modes. No separate `cmd/contextmcp` binary — less surface area to maintain.

```bash
# Start MCP adapter (stdio)
contextd --mcp --db /path/to/context.db --mcp-token <token>
```

### Store access: direct (no HTTP hop)

The MCP adapter instantiates `contextstore.Store` directly — same code path as the HTTP
handlers. No network call to a running `contextd` instance. This means:
- Zero latency overhead
- No port conflicts
- Token enforcement happens at the store/handler level, same as HTTP

### Auth: token at startup

Write/mutating tools require a capability token. Token is supplied at startup via
`--mcp-token` flag, not per-call. This matches Claude Code's `.mcp.json` `args` pattern:

```json
{
  "mcpServers": {
    "context": {
      "command": "contextd",
      "args": ["--mcp", "--db", "~/.context/context.db", "--mcp-token", "tok_abc123"]
    }
  }
}
```

Read tools (`context_head`, `context_history`, `context_view`) work without a token
(mvp-open-read posture). Write tools fail with `auth_required` if no token is provided.

## Library: mark3labs/mcp-go

Standard Go MCP server library. Handles JSON-RPC protocol, tool registration,
request/response lifecycle. We write tool handlers; the library handles the transport.

## Tool surface

| Tool | Maps to | Auth required | Phase |
|---|---|---|---|
| `context_head` | store.Head() | no | read |
| `context_history` | store.History() | no | read |
| `context_view` | store.Select() | no | read |
| `context_packet` | handlePacket logic | no (read-only result) | packet |
| `context_write` | store.Write() | yes (write scope) | write |
| `context_promote_request` | store.CreatePromoteRequest() | yes (promote.request scope) | promote |

## Task breakdown

### TASK-20260228-020 — MCP adapter foundation
- Add `mark3labs/mcp-go` dependency
- Create `internal/mcpadapter/` package: `adapter.go`, `server.go`
- `Adapter` struct: holds Store + config (token, DB path, enabled tools)
- `Run()` method: starts mcp-go server on stdio, registers all tools, blocks
- Add `--mcp` and `--mcp-token` flags to `cmd/contextd/main.go`
- Wire: when `--mcp` flag is set, create Store + Adapter and call `adapter.Run()`
- Tool stubs: register tool names with placeholder handlers that return `not_implemented`
- Tests: adapter starts, tools list returns expected names

### TASK-20260228-021 — MCP read tools
- Implement `context_head`: namespace + key args → head record JSON
- Implement `context_history`: namespace + key + limit args → records array JSON
- Implement `context_view`: selector JSON arg → evaluated records JSON
- Error handling: not_found → structured error response (not panic)
- Tests: each tool call returns correct response shape; not_found handled cleanly

### TASK-20260228-022 — MCP write + packet + promote tools
- Implement `context_packet`: selector + budget args → items + manifest JSON
- Implement `context_write`: namespace + key + payload + actor args → record_id
  - Token enforcement: requires `write` scope (via adapter config token)
  - Namespace policy enforcement: delegate to existing store/policy checks
- Implement `context_promote_request`: source/target namespace+key + reason args
  - Token enforcement: requires `promote.request` scope
- Tests: auth_required without token; scope rejection; success paths; policy_denied

## Post-sprint backlog

BACKLOG-20260228-001: User/agent documentation
- Written AFTER this sprint because MCP changes the setup story significantly
- Covers: what the service is, install + run, first write, first packet, .mcp.json setup

## Success definition

After this sprint, an agent can:
```json
// .mcp.json
{
  "mcpServers": {
    "context": {
      "command": "contextd",
      "args": ["--mcp", "--db", "~/.context/context.db", "--mcp-token", "tok_abc123"]
    }
  }
}
```
...and then call `context_write` to store observations and `context_packet` to retrieve
budget-bounded context at session start. That's the core memory loop working end-to-end.
