# Release procedure

Vanta Conduit ships as a Go module + a `contextd` binary. Consumers (Nanite, Cerberus-managed services, custom integrations) pin against the module path `github.com/hollis-labs/vanta-conduit` at a tagged version. The release procedure below keeps the binary version, MCP advertised version, and `CHANGELOG.md` in lockstep.

## Versioning

Pre-1.0 semver:

- **Minor** (`0.x.0` → `0.(x+1).0`) — additive surface: new MCP tools, new HTTP routes, new exported store methods, new config keys. Default for feature work.
- **Patch** (`0.x.y` → `0.x.(y+1)`) — fixes only. No new public surface.
- Breaking changes — allowed in any minor pre-1.0; call them out in `### Changed` with a migration note.

The MCP server advertises its version via `server.NewMCPServer("context-memory-service", "<X.Y.Z>", …)` in `internal/mcpadapter/adapter.go`. Keep this in lockstep with the released git tag.

## Per-PR checklist

Every PR that lands user-visible surface (new tool, route, store method, config key, behavior change) must:

1. Add a `## [Unreleased]` entry in `CHANGELOG.md` describing the change. Group by `Added` / `Changed` / `Fixed` / `Removed` / `Deprecated`. Name MCP tool IDs and HTTP paths exactly as agents will see them.
2. If the PR is the cut-line for a release, also bump:
   - The `## [Unreleased]` heading to `## [X.Y.Z] — YYYY-MM-DD` and start a fresh empty `## [Unreleased]` above it.
   - The `MCPServer` version string in `internal/mcpadapter/adapter.go`.
   - The compare links at the bottom of `CHANGELOG.md`.
3. Pass `go vet ./... && go test ./... && golangci-lint run`.
4. Pass the parity test (`go test ./tests/parity/`) — this fails if a new MCP tool or HTTP route is missing from `surfaceCatalog`.

## Cutting a tagged release

After the release-cut PR merges to `main`:

```bash
git checkout main && git pull
git tag -a v<X.Y.Z> -m "vanta-conduit v<X.Y.Z>"
git push origin v<X.Y.Z>
gh release create v<X.Y.Z> \
  --title "v<X.Y.Z>" \
  --notes-file <(awk '/^## \[<X.Y.Z>\]/{flag=1;next} /^## \[/{flag=0} flag' CHANGELOG.md)
```

The release notes pull straight from `CHANGELOG.md`. No duplication.

## Consumer impact

Nanite (and any other Conduit consumer) should:

- Bump the `github.com/hollis-labs/vanta-conduit` dependency in their `go.mod` to the new tag.
- Re-`go install ./cmd/contextd` if they bundle the standalone binary (Cerberus-managed `conduit-api`, agent stdio MCP server in `~/.claude.json`).
- Re-read `CHANGELOG.md` for new MCP tool IDs and HTTP paths to surface in their own agent prompts / docs.

Hot-reloading without a `contextd` rebuild is not supported — the MCP stdio server and the Cerberus-managed HTTP server share one binary, and any new tool/route is compiled in.
