# Context Memory Service — Project Integration Guide

This document explains how to wire the Context Memory Service into another project's
Claude Code setup and provides a ready-to-paste CLAUDE.md block that gives agents
workflow guidance.

---

## Setup checklist

1. **Run `tesseract`** (or confirm it's already running)
   ```bash
   tesseract serve &
   ```

2. **Create a token** with the scopes your agent needs
   ```bash
   tesseract context token create \
     --name <project-agent-name> \
     --scopes write,promote.request \
     --namespaces "app/<project-agent-name>/*" \
     --ttl 8760h
   ```

3. **Add `.mcp.json`** to the project root (or `~/.claude/.mcp.json` for global)
   ```json
   {
     "mcpServers": {
       "context": {
         "command": "/usr/local/bin/tesseract",
         "args": ["mcp", "--token", "<paste-token-here>"]
       }
     }
   }
   ```

4. **Paste the CLAUDE.md block below** into your project's `CLAUDE.md`

5. **Restart Claude Code**

---

## CLAUDE.md block (copy and paste)

Paste this into the relevant project's `CLAUDE.md`. Replace `<project-agent-name>`
with the namespace prefix you want your agent to use (e.g. `my-project`, `code-review`).

```markdown
## Context Memory Service

This project has persistent memory via the Context Memory Service MCP adapter.
The `context_*` tools are available in every session.

### On session start

Call `context_broker_fetch` with your intent before doing anything else:

```
context_broker_fetch(intent="resume_task", summary="<brief task description>")
```

This loads prior context from your session namespace and pinned user memory.
Check the manifest — if `pins_included > 0` the pinned records were prepended.

### During the session

Write observations after significant steps:

```
context_write(
  namespace="app/<project-agent-name>/session/<task-id>",
  key="state",
  payload='{"status":"in_progress","step":3,"checkpoint":{...}}',
  actor="app:<project-agent-name>"
)
```

Use structured JSON payloads. Include enough for the next session to resume
without re-reading everything.

### On session end

Write a summary and request promotion for anything worth keeping long-term:

```
context_write(namespace="app/<project-agent-name>/session/<task-id>",
              key="summary", payload='{"outcome":"...", "key_findings":[...]}')

context_promote_request(
  source_namespace="app/<project-agent-name>/session/<task-id>",
  source_key="summary",
  target_namespace="user/memory/<project-agent-name>",
  target_key="<task-id>-summary",
  reason="<why this should be retained>"
)
```

### Namespace conventions

| Namespace | Purpose |
|---|---|
| `app/<project-agent-name>/session/<task-id>` | Per-task working memory |
| `app/<project-agent-name>/observations` | Cross-task findings |
| `user/memory/<project-agent-name>` | Promoted long-term memory (human-approved) |
| `user/pins/*` | Always-loaded pinned context |

### Tool quick-reference

| Tool | Auth | Use when |
|---|---|---|
| `context_broker_fetch` | none | Session boot — load prior context |
| `context_write` | write scope | Save state or observations |
| `context_packet` | none | Targeted context load (when you know the namespaces) |
| `tesseract_get` | none for `domain: "context"` | Read a specific record |
| `context_promote_request` | promote.request scope | Promote findings to long-term memory |
| `context_promote_list` | none | Check pending promotions from prior sessions |
| `context_audit` | none | Investigate what happened in a namespace |
```

---

## How agents discover tools

The MCP protocol's `tools/list` response includes every tool's name and description.
Claude Code reads this on startup and understands what each tool does without any
CLAUDE.md guidance. The CLAUDE.md block above adds *workflow* guidance on top:
when to call which tool, and in what order. You can omit it if you want the agent
to infer usage from the tool descriptions alone.

---

## Multiple agents on the same store

Different agents can share one `tesseract` instance. Give each agent a distinct
namespace prefix and a token scoped to that prefix:

```bash
tesseract context token create --name agent-review \
  --scopes write,promote.request \
  --namespaces "app/agent-review/*"

tesseract context token create --name agent-docs \
  --scopes write,promote.request \
  --namespaces "app/agent-docs/*"
```

Each agent's token restricts writes to its own prefix. All agents can read each
other's namespaces (read tools require no token) unless you add a read-only token
policy via the namespace policy system.
