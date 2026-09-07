# Tesseract

Tesseract is a local-first context, memory and knowledge service: one
append-only store behind five surfaces — a Go library, a CLI, an HTTP API, an
embedded web UI and an MCP server. It is not an agent runtime, a task tracker or
an orchestrator, and it does not reason over what it stores; retrieval selects,
it never merges or infers.

## Start Here

- `README.md` and `docs/README.md` are the front door and the doc index.
  `docs/HISTORICAL.md` names the docs that no longer describe current behavior.
- `tesseract.go` is the library facade; `cmd/tesseract/main.go` is the CLI and
  HTTP daemon entry point.
- `internal/contextstore/store.go` owns the append-only log, the `heads` table
  and the schema migrations.
- `internal/memory/` and `internal/knowledge/` are the two upper domains;
  `internal/contextpolicy/policy.go` owns namespace ownership and write
  authorization.
- `internal/mcpadapter/` renders the MCP surface: `toolvocab.go` is the naming
  vocabulary as data, `skills/` is the skill corpus agents read.
- `tests/parity/parity_test.go` holds `surfaceCatalog`, the single source of
  truth for what Tesseract exposes.

## Commands

```bash
make test          # go test ./... under the Makefile's hermetic env
make validate      # contract suites against a throwaway XDG root
go vet ./...
make build         # Go only; compiles the committed UI bundle
```

`make smoke` curls a daemon you already have listening; `make e2e-local` starts
and tears down its own. Run one when a change touches HTTP, CLI or MCP shape.

## Boundaries

Run `make test`, not a bare `go test ./...`. The Makefile's `HERMETIC_ENV`
unsets `TESSERACT_DB_PATH`, `TESS_MEASURE_DB` and the provider keys; a bare run
inherits them, and `TESS_MEASURE_DB` opts otherwise-skipped tests into opening
and mutating the store it names.

`internal/webui/dist/` is committed, so `make build` and `make install` need
only Go. `make frontend`, `build-all` and `install-all` rewrite that tracked
tree — run them only for a frontend change, and commit the diff with its source.

Adding an MCP tool or a `/v1` route without a `surfaceCatalog` row fails
`TestMCPRegistrationMatchesCatalog` and `TestHTTPRoutesMatchCatalog`. Tool names
must match the verb table in `internal/mcpadapter/toolvocab.go`, and
`TestShippedProseNamesOnlyRegisteredTools` scans shipped docs and skills for
tool names no adapter registers. That guard is blind to a rename that changes
both the domain and the operation segment at once, so land the doc updates in
the same commit.

Writes never mutate: `AppendRecord` allocates the next revision and advances
`heads` in one transaction. Cross-namespace movement goes through the
request → approve → apply promotion workflow, which apps cannot bypass.
