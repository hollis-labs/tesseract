# Tesseract — Agent & Contributor Orientation

## What is this and why

Tesseract is a local-first **context + memory service** for humans and agents. It
provides deterministic, auditable persistence and retrieval of context records and
memories — append-only, namespace-scoped, and exposed over MCP, HTTP, and a CLI.
It exists to give agents a trustworthy substrate to query *before* they reach for
the filesystem or the web: retrieval paths never mutate or synthesize data (views
are selectors, not processors), every write keeps full provenance and revision
history, and app-owned context is bridged to user-owned memory only through an
explicit promotion workflow.

Tesseract was formerly **Cortex** (itself a rebrand of the Laravel-era **Seer v1**),
extracted from `fragments-engine/cortex/` into this standalone repo on 2026-04-07.
Go module: `github.com/hollis-labs/tesseract`. The `vanta-conduit` directory name
is a backward-compat symlink — **tesseract is canonical**. Note the MCP server is
still registered under the name `vanta` (tools surface as `mcp__vanta__*`).

## Where to start — entry points

| Path | Purpose |
|---|---|
| `cmd/contextd/main.go` | CLI + HTTP daemon entry point (`contextd`) |
| `conduit.go` | Library facade — embed Tesseract in-process via `conduit.Open()` |
| `internal/memory/` | Memory domain (revisions, decay, recall ranking) |
| `internal/contextstore/` | Append-only record log + SQLite index |
| `internal/knowledge/` | Knowledge domain (pointer-first references) |
| `internal/mcpadapter/` | MCP stdio transport — thin mapping to API/CLI |
| `internal/contextapi/` | HTTP API handlers |
| `internal/contextcli/` | CLI command implementations |
| `.agentrc/boot-prompt.md` | Session boot state for agents |
| `docs/QUICKSTART.md` | Run the service + write records in ~10 min |
| `docs/ARCHITECTURE.md` | Core model + invariants |
| `docs/MCP_TOOLS.md` | Agent-facing MCP tool catalog (scopes, playbooks) |

## Key domain concepts

- **Record** — versioned JSON payload keyed by `(namespace, key)`, carrying actor,
  revision, timestamp, and checksum.
- **Namespace** — hierarchical path (e.g. `user/memory/task-001`) that scopes
  records. Five ownership tiers (memory / cache / pins / draft / session);
  `user/*` is write-protected.
- **Append-only record log** — writes never mutate; each write allocates the next
  revision atomically. A heads table tracks the current head per key.
- **View** — a deterministic selector over indexed records. Views never process,
  merge, or infer — they only select. Identical selector + store state yields
  identical ordering.
- **Promotion** — the request → approve → apply workflow that moves app-owned
  `app/*` context into user-owned `user/*` memory. Apps cannot bypass it.
- **Three info domains** — Context, Memory, and Knowledge. Memory is revisioned
  with activation decay; Knowledge is pointer-first references; Context is the
  underlying record log.
- **Packet** — a budget-bounded bundle of records assembled for agent context.
- **Token** — a capability credential scoping write access to specific namespaces.

## Common operations

Build (compiles the frontend, then the Go binary):

```bash
make build           # -> ./contextd
make install         # go install ./cmd/contextd/
```

Test and contract verification:

```bash
make test            # go test ./...
make contracts       # API + error + metrics contract suites
make validate        # contracts + summary
make smoke           # HTTP smoke test against a running daemon
make e2e-local       # local end-to-end run
```

Run the daemon (paths resolve via go-apppaths / XDG — run `contextd path` to
see the resolved data, state, and config locations; `CONTEXTD_ROOT` is a
deprecated one-release compatibility shim):

```bash
./contextd serve --addr :8089
```

CLI usage (examples):

```bash
contextd context head <namespace> <key>
contextd context contract list --output table
```

Agents reach the running service over MCP using `mcp__vanta__*` tools — see
`docs/MCP_TOOLS.md` for the per-domain scope matrix and playbooks. The parity test
in `tests/parity/parity_test.go` fails if a tool or HTTP route is added without a
matching catalog entry.

## Where to look for more

- `docs/ARCHITECTURE.md` — core model, components, invariants
- `docs/DECISIONS/` — ADRs (`ADR-0001` storage/namespaces, `ADR-0002` registry);
  `ADR-INDEX.md` for the index
- `docs/SPECS/` — `API.md`, `CLI.md`, `MCP.md`, `NAMESPACES.md`, `VIEWS.md`,
  `PROMOTION.md`, `STORAGE.md`, `MVP.md`
- `docs/RELEASE-ROADMAP.md` — Tesseract 1.0 workstream decomposition
- `docs/DEV.md` — local development, build/test, auth notes
- `docs/AGENT-SETUP.md` — agent integration guide
- `CHANGELOG.md` — release history; downstream consumers watch this for new
  tools/routes
- `.agent-ops/project.yaml` — machine-readable project Source-of-Truth

> **Note:** `README.md` is currently stale — it contains leftover Volon project
> content, not Tesseract content. Treat this file (`AGENTS.md`) and the docs above
> as authoritative until the README is regenerated.
