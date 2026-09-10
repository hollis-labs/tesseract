# Project integration guidance

This page provides a minimal MCP setup and a prompt block for a project that
wants Tesseract-backed working context. It is optional: the MCP tool schemas and
`tesseract_skills` remain the authoritative discovery surface.

## Setup

1. Build or install Tesseract from the current source checkout.
2. Confirm the intended store with `tesseract path`.
3. Create a project-specific capability token:

   ```bash
   tesseract context token create \
     --name project-agent \
     --client-id app:project-agent \
     --scopes write,promote.request,memory:read \
     --namespaces 'app/project-agent/*' \
     --ttl 8760h
   ```

4. Add an MCP server entry to the client configuration:

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

5. Keep the real token in a protected local/secret-aware configuration, never
   in the repository, and restart the MCP host.

`tesseract mcp` opens the store directly. Do not start `tesseract serve` solely
for an MCP client.

## Prompt block

Replace `<agent-id>` with the identifier used in the token grant.

````markdown
## Tesseract context

This project has a local Tesseract MCP server. Begin discovery with
`tesseract_skills`, then load the `start-here` skill and any domain skill needed
for the current operation.

### Session start

Use `context_plan` with `execute=true` and an honest intent/summary, or use
`context_pack` with `shape="packet"` and explicit namespace globs when the
required context set is already known. Inspect the returned manifest before
assuming the packet is complete.

### During work

Write resumable working state only beneath:

`app/<agent-id>/session/<task-id>`

Use `context_write` with a stable key and a JSON-string payload. Exact reads use
`tesseract_get` and must name `domain`, `namespace`, and `key`.

### Session end

Write a concise outcome under the session namespace. If it belongs in protected
user memory, call `context_promote` with `stage="request"`; do not bypass the
human approval/apply stages.

### Recall discipline

Recall at the default projection, hydrate only the revision IDs worth reading,
then call `tesseract_touch` after the reasoning for the hits that actually shaped
it. Recall deliberately does not reinforce a result for merely returning it — a
search result is the ranker's guess, and a guess that reinforces itself turns
popular-because-returned into actually-useful within a few cycles. Under-reporting
is fine; over-reporting teaches the ranking that noise is signal.

A recalled record is evidence of reasoning that held at the time, not a ruling on
the current instruction. If it surfaces a consequence that is live now, raise the
consequence; a record disagreeing with a current direction is not by itself
grounds to push back.

Tesseract holds durable context and reasoning; the project's task system stays
authoritative for work state. Keeping work state out of here is what stops recall
from being dominated by rows that were only ever true for an afternoon.

### Security and privacy

The capability token restricts mutations and grants memory/knowledge reads via
`memory:read`, but it is not a complete read-isolation boundary. Several context
and audit reads remain available without a token. Treat the client as able to
read the context store. Provider-backed embedding/semantic tools can send
record or query text to OpenAI.
````

## Multi-agent stores

Give each writer a distinct app prefix and token. This prevents one agent from
mutating another agent's namespace:

```bash
tesseract context token create \
  --name review-agent \
  --client-id app:review-agent \
  --scopes write,promote.request,memory:read \
  --namespaces 'app/review-agent/*'

tesseract context token create \
  --name docs-agent \
  --client-id app:docs-agent \
  --scopes write,promote.request,memory:read \
  --namespaces 'app/docs-agent/*'
```

This is mutation separation plus an explicit grant for memory/knowledge reads,
not complete read tenancy. If agents must not see each other's content, use
separate Tesseract layouts and verify each with `tesseract path`.

See [Agent setup](AGENT-SETUP.md) for tool examples and
[Operations](OPERATIONS.md) for isolation and data egress.
