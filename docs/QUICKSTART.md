# Context Memory Service — Quick Start

Get the service running and writing records in under 10 minutes.

## What is this?

A local-first persistent memory store for AI agents and developer tools. Agents
write structured records into typed namespaces, read them back with deterministic
view selectors, and request promotions between namespaces. The store is a SQLite
index on top of local JSON record files.

Key concepts:
- **Namespace** — hierarchical path (`user/memory/task-001`) that scopes records
- **Record** — versioned JSON payload with actor, revision, and checksum
- **Packet** — budget-bounded bundle of records assembled for agent context
- **Token** — capability credential scoping write access to specific namespaces

---

## 1. Build

```bash
go build -o contextd ./cmd/contextd
```

Place `contextd` on your `$PATH` or use the full path in commands below.

---

## 2. Configure root directory

All data lives under `CONTEXTD_ROOT`. Set it in your shell or in `.env`:

```bash
export CONTEXTD_ROOT="$HOME/.context"
mkdir -p "$CONTEXTD_ROOT"
```

The store auto-initializes the first time you write a record.

---

## 3. Start the API server (optional — CLI works without it)

```bash
contextd serve --addr :8080
```

The CLI (`context ...` commands) talks directly to the store — no server required.
Start the server only if you need the HTTP API or the MCP adapter.

---

## 4. Create a capability token

Tokens scope write access to specific namespace globs and operations.

```bash
contextd context token create \
  --name my-agent \
  --scopes write,packet,promote.request \
  --namespaces "app/my-agent/*" \
  --ttl 8760h
```

**Copy the token value immediately** — it is only shown once.

Output:
```
Token created. Copy this value now — it will not be shown again.

  Token:       tok_abc123...
  ID:          tid_...
  Name:        my-agent
  Client:
  Scopes:      write, packet, promote.request
  Namespaces:  app/my-agent/*
  Expires:     2027-02-27T...
```

---

## 5. Register a namespace (optional)

Namespaces are created implicitly on first write. Explicit registration sets
ownership policy, which the policy engine enforces on write.

```bash
contextd context namespace register \
  --namespace app/my-agent/session \
  --owner-type app \
  --owner-id my-agent
```

---

## 6. Write a record

```bash
contextd context put \
  --namespace app/my-agent/session \
  --key goal \
  --actor app:my-agent \
  --json '{"phase":"start","objective":"ship quick start"}'
```

Response:
```json
{"record_id":"rec_...","namespace":"app/my-agent/session","key":"goal","revision":1,"actor":"app:my-agent","created_at":"...","checksum":"sha256:..."}
```

---

## 7. Read it back

```bash
# Head (latest revision)
contextd context get --namespace app/my-agent/session --key goal

# Full revision history
contextd context history --namespace app/my-agent/session --key goal --limit 10
```

---

## 8. Get a context packet

A packet is a budget-bounded bundle of records for agent context loading:

```bash
contextd context packet \
  --namespace "app/my-agent/*" \
  --budget-items 20 \
  --budget-tokens 4000
```

With a human-readable manifest:
```bash
contextd context packet \
  --namespace "app/my-agent/*" \
  --namespace "user/memory/*" \
  --budget-tokens 8000 \
  --output json
```

---

## 9. Request a promotion

Agents write to `app/*` namespaces and request promotion to `user/*` for
human review:

```bash
# Agent writes a candidate summary
contextd context put \
  --namespace app/my-agent/session \
  --key summary \
  --actor app:my-agent \
  --json '{"text":"session complete, ready for review"}'

# Agent requests promotion to user/memory
contextd context promote request \
  --source-namespace app/my-agent/session \
  --source-key summary \
  --target-namespace user/memory/my-agent \
  --target-key summary
```

---

## 10. Configure MCP for Claude Code

### Add `.mcp.json` to your project

```json
{
  "mcpServers": {
    "context": {
      "command": "/usr/local/bin/contextd",
      "args": ["mcp", "--token", "<your-token-here>"],
      "env": {
        "CONTEXTD_ROOT": "/Users/<you>/.context"
      }
    }
  }
}
```

Place this in your project root (applies to that project only) or in
`~/.claude/.mcp.json` (applies globally to all Claude Code sessions).

Restart Claude Code after adding or changing `.mcp.json`.

### Available tools

Claude Code discovers tools automatically via MCP — no CLAUDE.md setup required.
There are 14 tools in three groups:

**No token needed (read-only)**

| Tool | What it does |
|---|---|
| `context_head` | Read the latest revision of a record |
| `context_history` | Read revision history for a record |
| `context_view` | Query records across multiple namespace globs |
| `context_packet` | Load a budget-bounded bundle of records (primary boot tool) |
| `context_promote_list` | List promotion requests by status |
| `context_broker_plan` | Generate a namespace fetch plan for a given intent |
| `context_broker_fetch` | Plan + fetch in one call (recommended for session boot) |
| `context_namespace_show` | Show the ownership policy for a namespace |
| `context_audit` | Query the audit event log |

**Write token required**

| Tool | Required scope |
|---|---|
| `context_write` | `write` |
| `context_promote_request` | `promote.request` |
| `context_promote_approve` | `promote.approve` |
| `context_promote_apply` | `promote.apply` |
| `context_namespace_register` | `namespace.admin` |

### Token scopes by role

```bash
# Typical agent (read + write session memory + request promotions)
contextd context token create --name my-agent \
  --scopes write,promote.request \
  --namespaces "app/my-agent/*"

# Human operator (approve and apply promotions, manage namespaces)
contextd context token create --name operator \
  --scopes promote.approve,promote.apply,namespace.admin \
  --namespaces "*"
```

### Explicit guidance for agents in other projects

MCP exposes tool descriptions automatically, but you can add a CLAUDE.md snippet
to any project to give agents workflow guidance. See
[CONTEXT-FOR-PROJECTS.md](./CONTEXT-FOR-PROJECTS.md) for a copy-paste block.

See [AGENT-SETUP.md](./AGENT-SETUP.md) for the full agent workflow and tool reference.

---

## CLI reference summary

| Command | Description |
|---|---|
| `context namespace register` | Register namespace with ownership policy |
| `context put` | Append a record revision |
| `context get` | Read head revision |
| `context history` | Read revision history |
| `context view` | Evaluate a selector query |
| `context packet` | Assemble a budget-bounded context packet |
| `context promote request` | Request record promotion to user/* |
| `context promote list` | List pending promotion requests |
| `context promote approve` | Approve a pending request |
| `context promote apply` | Apply an approved request |
| `context token create` | Create a capability token |
| `context token list` | List active tokens |
| `context token revoke` | Revoke a token |
| `context broker plan` | Generate a context fetch plan |
| `context broker fetch` | Fetch context for an intent |
| `context maintenance trim` | Trim old revisions |
