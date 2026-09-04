# Contributing to Tesseract

Thanks for helping improve Tesseract. The project is in public preview, so
small, reviewable changes with explicit compatibility notes are especially
valuable.

## Before opening an issue

- Search existing issues and the [documentation index](docs/README.md).
- Use `tesseract --help` and the relevant subcommand help to confirm current
  CLI behavior.
- Do not file vulnerabilities or include sensitive data publicly; follow
  [`SECURITY.md`](SECURITY.md).

Bug reports should include the version or commit, operating system, exact
command or API surface, expected result, actual result, and a minimal
reproduction. Redact tokens, provider keys, paths, record payloads, and query
strings before attaching logs or backups.

## Development setup

Use Go 1.26.6. A normal backend build uses the committed embedded web UI and
does not require Node:

```bash
git clone https://github.com/hollis-labs/tesseract.git
cd tesseract
go mod download
make test
make validate
make build
```

Frontend changes also require Node.js and npm. Install exactly the lockfile
graph, run frontend checks, and regenerate the committed embedded bundle:

```bash
npm_config_userconfig=/dev/null npm --prefix frontend ci --no-audit --no-fund
npm --prefix frontend run lint
npm --prefix frontend test
npm --prefix frontend run build
make frontend
```

The final `make frontend` updates `internal/webui/dist/`; include that generated
diff with frontend source changes.

## Change guidelines

- Keep HTTP, CLI, MCP, and Go behavior aligned where the contracts say they
  are peers. Differences that are intentional should be documented explicitly.
- Preserve append-only history, deterministic ordering, namespace policy, and
  audit linkage.
- Treat backup and restore changes as data-safety changes and add failure-path
  tests.
- Add an `[Unreleased]` changelog entry for user-visible behavior.
- Update examples and public docs in the same change as a contract change.
- Do not add a private module, npm dependency, absolute workstation path, or
  untagged local replacement to the public build graph.

## Checks before a pull request

```bash
gofmt -w path/to/changed.go
go vet ./...
make test
make validate
git diff --check
```

If the change touches frontend source, also run its lint, test, and build
commands and confirm `internal/webui/dist/` is current. If it changes a public
surface, run the focused integration/parity tests that cover it.

## Pull requests

Explain the user-visible outcome, compatibility impact, tests run, and any
follow-up intentionally left out of scope. Keep generated files in the same
commit or series that changes their source. Do not include secrets or a real
Tesseract store in commits or test fixtures.

Maintainers may ask for a smaller patch or additional contract coverage before
review. Release and versioning rules are in [`docs/RELEASE.md`](docs/RELEASE.md).
