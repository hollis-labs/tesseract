# Tesseract by Hollis Labs

Tesseract is a local-first context and memory service for humans, tools, and AI agents. It stores append-only, revisioned records in deterministic namespaces and exposes them over a Go library, CLI, HTTP API, and MCP.

It is designed for workflows where agents need a trustworthy persistence layer before reaching for the filesystem or the web:

- append-only record history with stable heads
- namespace-scoped ownership and token-gated writes
- deterministic views and context packets
- app-to-user promotion workflow instead of silent memory mutation
- optional semantic recall via embeddings

## Install

Install the `tesseract` binary with Go:

```bash
go install github.com/hollis-labs/tesseract/cmd/tesseract@latest
```

If you are building from source:

```bash
git clone https://github.com/hollis-labs/tesseract.git
cd tesseract
go build -o tesseract ./cmd/tesseract
```

Use `make build` only if you want to rebuild the embedded web UI as part of the binary. A normal `go install` or `go build` path does not require Node.

## Quick Start

1. Inspect the paths Tesseract will use on your machine:

```bash
tesseract path
```

2. Create a config file at the reported `config-file` path. Start from one of:

- [`examples/config.openai.yaml`](examples/config.openai.yaml)
- [`examples/config.anthropic-openai.yaml`](examples/config.anthropic-openai.yaml)

3. Export the provider keys you need:

```bash
export OPENAI_API_KEY=...
export ANTHROPIC_API_KEY=...
```

Use [`env.example`](env.example) as a template.

4. Start the daemon:

```bash
tesseract serve --addr :8089
```

5. Write and read your first record:

```bash
tesseract context put \
  --namespace app/demo/session \
  --key goal \
  --actor app:demo \
  --json '{"objective":"ship beta docs"}'

tesseract context get --namespace app/demo/session --key goal
```

## Provider Setup

Tesseract currently supports these runtime provider paths:

- Embeddings: `openai`
- Synthesis: `openai`, `anthropic`

Recommended beta setups:

- OpenAI only:
  - embeddings enabled
  - synthesis enabled
- Anthropic + OpenAI:
  - Anthropic for synthesis
  - OpenAI for embeddings
- No provider keys:
  - core context, memory, knowledge, CLI, API, and MCP still work
  - embedding-backed recall and synthesis are unavailable

Example config:

```yaml
embedding:
  provider: openai
  model: text-embedding-3-large

synthesis:
  provider: anthropic
  model: claude-sonnet-4-5
```

## MCP Setup

Tesseract can run as an MCP stdio server:

```bash
tesseract mcp --token <capability-token>
```

Sample Claude Code configuration is in [`examples/mcp.json`](examples/mcp.json).

Typical setup flow:

1. Create a token with the scopes you need.
2. Add a Tesseract entry to your MCP client config.
3. Restart the MCP client.
4. Use `context_*`, `memory_*`, and related tools from the client.

## Common Commands

```bash
tesseract serve --addr :8089
tesseract path
tesseract context put --namespace app/demo/session --key state --actor app:demo --json '{"status":"ok"}'
tesseract context get --namespace app/demo/session --key state
tesseract context history --namespace app/demo/session --key state
tesseract context token create --name demo --scopes write,promote.request --namespaces "app/demo/*"
tesseract context packet --namespace "app/demo/*" --budget-items 20 --budget-tokens 4000
tesseract backfill-embeddings
```

## Documentation

- [docs/README.md](docs/README.md) for the public docs entrypoint
- [docs/QUICKSTART.md](docs/QUICKSTART.md) for the end-to-end first-run flow
- [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) for the core model and invariants
- [docs/AGENT-SETUP.md](docs/AGENT-SETUP.md) for MCP and Claude Code setup
- [docs/MCP_TOOLS.md](docs/MCP_TOOLS.md) for the MCP tool catalog
- [docs/SPECS/API.md](docs/SPECS/API.md) for the HTTP surface
- [docs/SPECS/CLI.md](docs/SPECS/CLI.md) for CLI behavior
- [CHANGELOG.md](CHANGELOG.md) for user-visible changes

## Build And Test

```bash
go test ./...
make build
make smoke
```

## License

Tesseract is released under the [MIT License](LICENSE).
