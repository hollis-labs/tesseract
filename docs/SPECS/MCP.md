# MCP adapter

Status: implemented public-preview contract.

Tesseract exposes its agent surface as a Model Context Protocol server over
stdio. The adapter is intentionally thin: tools call the same stores and domain
services as the HTTP and CLI surfaces. It does not introduce a second
persistence model.

The authoritative, complete catalog of all 28 registered tools—including
arguments, scopes, HTTP peers, merged-tool selectors, and worked calls—is
[MCP_TOOLS.md](../MCP_TOOLS.md). Keeping that information in one agent-facing
catalog avoids duplicating schemas here.

## Start and configure

Create a managed capability token in the same Tesseract data root the MCP
process will use:

```bash
tesseract context token create \
  --name agent \
  --client-id app:agent \
  --scopes write,memory:read,memory:write,promote.request,promote.approve,promote.apply,namespace.admin \
  --namespaces 'app/agent/*,user/alex/memory/*' \
  --ttl 720h
```

Then configure an MCP host to launch the binary from `PATH`:

```json
{
  "mcpServers": {
    "tesseract": {
      "type": "stdio",
      "command": "tesseract",
      "args": ["mcp", "--token", "<capability-token>"]
    }
  }
}
```

Run `tesseract path` in the same environment to confirm the data root. The
token is a store-backed managed token; HTTP's `--static-token` value is not a
stored capability token and cannot be substituted here.

`--token` may be omitted when the client will use only tools whose catalog
scope is `—`. A scoped tool called without a token returns `auth_required`.

## Discovery

Call `tesseract_skills` with no arguments to list the shipped agent skills, then
call it with `name: "start-here"` for the orientation guide. Tool descriptions
point directly to the relevant skill before state-changing operations.

MCP clients commonly render registered tool names with a host prefix such as
`mcp__tesseract__context_write`. The protocol-level tool name registered by
Tesseract is `context_write`; the host adds the prefix.

## Authorization

- The token supplied to `tesseract mcp --token` is validated against the local
  `auth_tokens` store whenever a scoped tool runs.
- Each scoped tool checks the exact scope shown in
  [MCP_TOOLS.md](../MCP_TOOLS.md). Promotion checks
  `promote.request`, `promote.approve`, or `promote.apply` according to the
  selected `stage`.
- Write paths also enforce the token's namespace globs where the catalog says
  they do. A scope match does not override namespace restrictions.
- MCP namespace registration uses `namespace.admin`. HTTP namespace
  registration uses the distinct `namespace.register` scope.
- The default token scope set does not include `memory:read`, `memory:write`,
  `namespace.admin`, or `admin`; request those explicitly when needed.
- Revoked, expired, or unknown tokens return `auth_required`; missing scopes
  return `insufficient_scope`; disallowed namespaces return
  `namespace_not_permitted`.

Tokens are passed on the local process command line by the MCP host. Protect
host configuration and process visibility accordingly, and give each agent the
narrowest useful scopes and namespace globs.

## Request and response rules

- Tool arguments are defined by the schemas advertised during MCP discovery.
- Tool application errors are returned as JSON text with `code` and `message`,
  for example `{"code":"validation_error","message":"..."}`. They are tool
  results rather than MCP transport failures.
- Successful results are JSON text unless a discovery skill intentionally
  returns Markdown.
- Collection tools apply response budgets and deterministic ordering. A budget
  or cursor is part of a tool's contract; omitted knobs use the defaults in the
  live tool schema and catalog.
- Selectors and arm discriminators are strict. Unrecognized values and
  arguments retired during tool merges are rejected rather than ignored.
- Tool logs pass through the MCP sanitization middleware. Tokens must never be
  placed in tool arguments, record payloads, or log messages.

HTTP peers share behavior, but their wire shape is not assumed to be identical.
In particular, the MCP memory, knowledge and event writes use flat scalar
arguments while HTTP uses nested objects. Follow the examples for the surface being called;
see [the HTTP API contract](API.md) for HTTP bodies.

## Compatibility and determinism

- Read/view tools do not mutate context records. Deliberate memory fetch and
  touch operations may update memory activation state as documented in the
  tool catalog.
- Selector fallback ordering is deterministic, and limits are applied after
  ordering.
- Merged tools use explicit discriminators such as `domain`, `stage`, `shape`,
  `kind`, `execute`, and `full_evaluation`; these select behavior rather than
  merely changing presentation.
- The MCP initialize response reports the version stamped into the binary. An
  unstamped embedding reports `dev` instead of a stale release number.
- The live adapter registry is checked against the tool catalog by the parity
  and vocabulary tests. Update code and the catalog together when adding,
  removing, or renaming a tool.

## Boundaries

MCP is not a network listener, an HTTP authorization bypass, or a separate
backup surface. The MCP host launches a local process, and that process reads
the same XDG-resolved Tesseract store as the CLI and HTTP server. Operational
server controls, managed-token lifecycle endpoints, backups, and maintenance
remain documented in [CLI.md](CLI.md) and [API.md](API.md).
