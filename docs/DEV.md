# Local development

## Prerequisites

- Go 1.26.6
- Git
- Node.js and npm only for frontend changes

The normal Go build consumes the committed bundle in `internal/webui/dist/`.

## Build and test

```bash
go mod download
make test
make validate
go vet ./...
make build
```

Run `git diff --check` before committing. Format changed Go files with `gofmt`.
Public surface changes should also run focused integration/parity suites and
update `[Unreleased]` in the changelog.

## Isolate stateful development

Tesseract uses four XDG roots plus specific database/workspace overrides. There
is no `TESSERACT_HOME`. Before any local test that writes, migrates, restores,
or runs a server, use a disposable layout and verify it:

```bash
export XDG_DATA_HOME="$PWD/.tmp/tesseract/data"
export XDG_STATE_HOME="$PWD/.tmp/tesseract/state"
export XDG_CACHE_HOME="$PWD/.tmp/tesseract/cache"
export XDG_CONFIG_HOME="$PWD/.tmp/tesseract/config"
unset TESSERACT_DB_PATH TESSERACT_WORKSPACE
unset TESS_MEASURE_DB TESS_MEASURE_NS TESS_MEASURE_RANKING
./tesseract path
```

Do not proceed until every reported path is beneath the disposable root. The
`TESS_MEASURE_*` variables are test instrumentation overrides and can redirect
some measurement tests to a supplied database, so clear them for ordinary test
runs.

## Run the daemon

```bash
./tesseract serve
```

The default is unauthenticated loopback at `127.0.0.1:8089`. For an auth test,
create the token inside the isolated layout first:

```bash
./tesseract context token create \
  --name dev-client \
  --client-id app:dev-client \
  --scopes write,promote.request,packet \
  --namespaces 'app/dev-client/*' \
  --ttl 1h

./tesseract serve --managed-auth --addr 127.0.0.1:8089
```

All HTTP routes except readiness and enabled metrics require the bearer token
when a token mode is active. Static auth deliberately has no `admin` scope.
See [Operations](OPERATIONS.md#listener-and-authentication-modes).

Structured request logs are opt-in:

```bash
./tesseract serve --request-logs --request-log-mode redacted
```

Use `full` mode only against disposable data: it records raw query strings.

## Frontend

Install the lockfile graph without relying on private npm configuration:

```bash
npm_config_userconfig=/dev/null npm --prefix frontend ci --no-audit --no-fund
npm --prefix frontend run lint
npm --prefix frontend test
npm --prefix frontend run build
```

After source changes, regenerate the committed embedded bundle:

```bash
make frontend
go test ./internal/webui
```

Commit changes under `internal/webui/dist/` with the frontend source. Backend
contributors who do not change the UI do not need Node.

## Contract and smoke checks

Useful repository gates include:

```bash
make contracts
make contract-lint
make contract-commands
```

The smoke client targets an already-running daemon. Pass its auth mode and
token explicitly:

```bash
make smoke \
  BASE_URL=http://127.0.0.1:8089 \
  AUTH_MODE=managed \
  TOKEN="$TESSERACT_TOKEN"
```

Run smoke only after the disposable-path verification above. Any command that
enables embeddings can transmit record/query text to OpenAI.

## Data-safety changes

Backup, restore, migration, compaction, permissions, authentication, and
namespace-policy changes need failure-path tests as well as happy-path tests.
Restore tests must use a disposable store and should assert that the old store
survives every pre-swap failure. See the [restore runbook](OPERATIONS.md#restore-runbook)
for the operator-visible contract.

## Repository map

- `cmd/tesseract/` — binary and process assembly
- `internal/contextstore/` — context storage, audit, auth, backup, restore
- `internal/memory/` and `internal/knowledge/` — revision domains and recall
- `internal/contextapi/` — HTTP routes
- `internal/mcpadapter/` — MCP tools and shipped discovery skills
- `internal/contextcli/` — context subcommands
- `frontend/` — React operator UI source
- `internal/webui/dist/` — generated bundle embedded in the binary
- `tests/integration/` and `tests/parity/` — public contract coverage

General contribution guidance is in [`CONTRIBUTING.md`](../CONTRIBUTING.md).
