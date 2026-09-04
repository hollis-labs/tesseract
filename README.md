# Tesseract

Tesseract is a local-first context, memory, and knowledge service for people,
tools, and AI agents. It keeps append-only revision history, applies namespace
and capability policy, and exposes the same store through a Go library, CLI,
HTTP API, embedded web UI, and MCP server.

Tesseract is currently a public preview. Its contracts are documented and
tested, but pre-1.0 releases may contain breaking changes. Read
[`CHANGELOG.md`](CHANGELOG.md) before upgrading.

## What it provides

- revisioned context records with deterministic heads and history
- separate memory and pointer-backed knowledge domains
- namespace ownership and scoped capability tokens
- explicit request, approval, and apply stages for cross-namespace promotion
- lexical recall plus optional OpenAI-backed embeddings and synthesis
- a local operator UI embedded in the Go binary
- integrity-checked, failure-atomic store backup and restore

## Install from source

The current preview is distributed as source. There are no supported prebuilt
binaries, package-manager formulae, container images, or desktop installers.

Requirements:

- Go 1.26.6
- Git
- Node.js 20.19–20.x or 22.12+ with npm only when changing and rebuilding
  `frontend/`

```bash
git clone https://github.com/hollis-labs/tesseract.git
cd tesseract
make build
./tesseract --version
```

`make build` compiles the committed web UI bundle and needs only Go. Use
`make install` to install the binary into your Go bin directory. Frontend
contributors use `make build-all` or regenerate the embedded bundle explicitly.

## Quick start

Inspect the paths Tesseract will use before creating data:

```bash
./tesseract path
```

Start the daemon:

```bash
./tesseract serve
```

The default listener is `http://127.0.0.1:8089`: loopback-only and
unauthenticated. The API is under `/v1/`, and the web UI is at `/`.

In another terminal, write and read a context record:

```bash
./tesseract context put \
  --client-id demo \
  --namespace app/demo/session \
  --key goal \
  --actor app:demo \
  --json '{"objective":"evaluate tesseract"}'

./tesseract context get \
  --namespace app/demo/session \
  --key goal
```

Provider credentials are optional. Core context, memory, knowledge, lexical
recall, CLI, HTTP, UI, and MCP workflows work without them. To enable
embeddings or synthesis, copy one of the sample configs to the `config-file`
reported by `tesseract path` and set the matching environment variables:

- [`examples/config.openai.yaml`](examples/config.openai.yaml)
- [`examples/config.anthropic-openai.yaml`](examples/config.anthropic-openai.yaml)
- [`env.example`](env.example)

Provider-backed features transmit selected content to the configured provider.
Review the [data egress table](docs/OPERATIONS.md#outbound-connections-and-data-egress)
before enabling them.

The complete first-run flow is in [`docs/QUICKSTART.md`](docs/QUICKSTART.md).

## Network and authentication boundary

Tesseract refuses an unauthenticated non-loopback bind unless the operator uses
the explicit `--allow-unauthenticated-remote` override. For remote access,
choose managed authentication or a static token:

```bash
# Create a managed token before starting managed-auth mode.
./tesseract context token create \
  --name remote-client \
  --client-id app:remote-client \
  --scopes write,promote.request,packet \
  --namespaces 'app/remote-client/*' \
  --ttl 24h

./tesseract serve --managed-auth --addr 0.0.0.0:8089
```

```bash
export TESSERACT_TOKEN='replace-with-a-long-random-token'
./tesseract serve --static-token "$TESSERACT_TOKEN" --addr 0.0.0.0:8089
```

When either token mode is active, every HTTP route requires
`Authorization: Bearer <token>` except readiness and the optional metrics
endpoint. A static token deliberately has no `admin` scope. Mutating the
runtime settings or its config backups therefore requires managed auth and a
managed token created with the explicit `admin` scope.

Authentication is not transport encryption. Tesseract has no built-in TLS;
put any remotely reachable listener behind TLS, a VPN, an SSH tunnel, or a
trusted TLS-terminating reverse proxy. See [`SECURITY.md`](SECURITY.md).

## MCP

Tesseract can run as an MCP stdio server; a separate HTTP daemon is not
required:

```bash
./tesseract mcp --token '<capability-token>'
```

Use [`examples/mcp.json`](examples/mcp.json) as a client configuration starting
point. The token controls mutations, memory/knowledge read scopes, and some
namespace filtering, but it is not a complete read-isolation boundary for the
local stdio server. See
[`docs/AGENT-SETUP.md`](docs/AGENT-SETUP.md) and
[`docs/MCP_TOOLS.md`](docs/MCP_TOOLS.md).

## Backup before real use

A current backup is a directory containing a whole-database snapshot, record
payloads, and a checksummed manifest. Configuration is included only when
`--config` is supplied.

```bash
./tesseract context backup export \
  --out "$PWD/tesseract-backup" \
  --config /path/reported/by/tesseract-path/config.yaml

./tesseract context backup verify --in "$PWD/tesseract-backup"
```

Stop every daemon and MCP process that uses the store before restoring. Restore
replaces the destination; it does not merge. Detailed recovery and upgrade
guidance is in [`docs/OPERATIONS.md`](docs/OPERATIONS.md).

## Documentation

- [`docs/README.md`](docs/README.md) — documentation index
- [`docs/QUICKSTART.md`](docs/QUICKSTART.md) — installation and first run
- [`docs/AGENT-SETUP.md`](docs/AGENT-SETUP.md) — MCP client setup
- [`docs/OPERATIONS.md`](docs/OPERATIONS.md) — support, auth, egress, backup, and upgrades
- [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) — storage and service design
- [`docs/SPECS/API.md`](docs/SPECS/API.md) — HTTP contract
- [`docs/SPECS/CLI.md`](docs/SPECS/CLI.md) — CLI contract
- [`docs/MCP_TOOLS.md`](docs/MCP_TOOLS.md) — MCP tool catalog
- [`SECURITY.md`](SECURITY.md) — security policy and deployment boundary
- [`CONTRIBUTING.md`](CONTRIBUTING.md) — contribution workflow

## Develop

```bash
make test
make validate
go vet ./...
```

See [`CONTRIBUTING.md`](CONTRIBUTING.md) for the complete contributor checks.

## License

Tesseract is released under the [MIT License](LICENSE).
