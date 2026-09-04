# Tesseract Quick Start

This guide gets a fresh install to a working local daemon, a configured provider, and a first write/read flow.

## What you need

- Go installed
- a built or installed `tesseract` binary
- an API key for at least one supported provider if you want embeddings or synthesis

Current provider support:

- embeddings: `openai`
- synthesis: `openai`, `anthropic`

Without provider keys, Tesseract still supports the core context, memory, knowledge, CLI, API, and MCP flows. Embedding-backed recall and synthesis stay disabled.

## 1. Install `tesseract`

Preferred:

```bash
go install github.com/hollis-labs/tesseract/cmd/tesseract@latest
```

From source:

```bash
go build -o tesseract ./cmd/tesseract
```

## 2. Inspect the runtime paths

Tesseract resolves its data, state, and config locations automatically.

```bash
tesseract path
```

Look for these values:

- `config-file`
- `data`
- `state`
- `records`

The config file lives at the reported `config-file` path.

Note:

- Prefer the default XDG layout unless you have a concrete reason to override it.
- To override it: `TESSERACT_DB_PATH` moves the main database, `TESSERACT_WORKSPACE`
  moves the active workspace, and the `$XDG_*_HOME` vars move the roots those nest
  under. There is no single "one base for everything" variable — point all four of
  `XDG_DATA_HOME`, `XDG_STATE_HOME`, `XDG_CACHE_HOME` and `XDG_CONFIG_HOME` at the
  same directory if that is what you want.

## 3. Create a config file

Start from one of the sample configs in [`../examples/`](../examples):

- [`../examples/config.openai.yaml`](../examples/config.openai.yaml)
- [`../examples/config.anthropic-openai.yaml`](../examples/config.anthropic-openai.yaml)

Minimal example:

```yaml
embedding:
  provider: openai
  model: text-embedding-3-large

dedup:
  similarity_threshold: 0.85

synthesis:
  provider: anthropic
  model: claude-sonnet-4-5
  max_tokens: 1024
```

## 4. Export provider credentials

Use [`../env.example`](../env.example) as a starting point.

OpenAI-only:

```bash
export OPENAI_API_KEY=...
```

Anthropic for synthesis plus OpenAI for embeddings:

```bash
export OPENAI_API_KEY=...
export ANTHROPIC_API_KEY=...
```

## 5. Start the daemon

```bash
tesseract serve
```

What this gives you:

- HTTP API at `http://127.0.0.1:8089/v1/`
- embedded web UI at `http://127.0.0.1:8089/`

The default bind address is `127.0.0.1:8089` — loopback only, and
unauthenticated. Tesseract refuses to start on an address other machines can
reach unless you configure a token mode (`--managed-auth` or
`--static-token <token>`), because with no token mode every route, including
`/v1/admin/*`, answers anyone who can reach the port.

The CLI does not require the server to be running, but the HTTP API and browser UI do.

## 6. Write and read your first record

Write:

```bash
tesseract context put \
  --namespace app/demo/session \
  --key goal \
  --actor app:demo \
  --json '{"phase":"start","objective":"ship beta"}'
```

Read:

```bash
tesseract context get --namespace app/demo/session --key goal
tesseract context history --namespace app/demo/session --key goal --limit 10
```

## 7. Create a capability token for MCP or automated writers

Tokens scope mutating operations to specific namespaces and capabilities.

```bash
tesseract context token create \
  --name demo-agent \
  --client-id app:demo-agent \
  --scopes write,promote.request \
  --namespaces "app/demo-agent/*" \
  --ttl 8760h
```

Copy the raw token immediately. It is only shown once.

## 8. Optional: register a namespace policy

Namespaces are created implicitly on first write. Register them explicitly if you want an ownership policy enforced from the start.

```bash
tesseract context namespace register \
  --namespace app/demo-agent/session \
  --owner-type app \
  --owner-id demo-agent
```

## 9. Optional: build a context packet

Packets assemble a budget-bounded bundle of records for agent context loading.

```bash
tesseract context packet \
  --namespace "app/demo/*" \
  --budget-items 20 \
  --budget-tokens 4000
```

## 10. Optional: request a promotion

Apps write to `app/*` and request promotion into protected `user/*` namespaces.

```bash
tesseract context put \
  --namespace app/demo-agent/session \
  --key summary \
  --actor app:demo-agent \
  --json '{"text":"session complete"}'

tesseract context promote request \
  --source-namespace app/demo-agent/session \
  --source-key summary \
  --target-namespace user/memory/demo-agent \
  --target-key summary
```

## 11. Optional: connect an MCP client

Tesseract can run as an MCP stdio server:

```bash
tesseract mcp --token <capability-token>
```

For a ready-to-copy config, see:

- [`../examples/mcp.json`](../examples/mcp.json)
- [AGENT-SETUP.md](AGENT-SETUP.md)

## Common issues

### `embedding_unavailable`

Cause:

- `embedding.provider` is not supported, or
- `OPENAI_API_KEY` is not set

### `synthesis_unavailable`

Cause:

- `synthesis.provider` is not supported, or
- the corresponding API key is not set

### Managed auth startup error

If you start the daemon with `--managed-auth`, it requires at least one active managed token:

```bash
tesseract context token issue --label admin --ttl 24h
```

That is separate from capability tokens created with `context token create`.

## Next docs

- [AGENT-SETUP.md](AGENT-SETUP.md) for MCP / Claude Code setup
- [ARCHITECTURE.md](ARCHITECTURE.md) for the core model
- [SPECS/API.md](SPECS/API.md) for HTTP details
- [SPECS/CLI.md](SPECS/CLI.md) for CLI behavior
