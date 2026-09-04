# Release procedure

Tesseract is pre-1.0 and currently distributed as source. A git tag is a source
release; do not imply that prebuilt binaries, installers, packages, or container
images exist unless a release actually publishes and verifies them.

## Versioning

Use semantic versioning with explicit pre-1.0 compatibility notes:

- minor release for additive features and intentional breaking changes
- patch release for compatible fixes
- every breaking change must have a changelog migration note

There is one runtime version identity. `make build` and `make install` stamp it
from `git describe`; `go install module@tag` receives its module version from
the Go toolchain. The binary passes that value to the MCP adapter. There is no
literal MCP version string to update separately.

## Every user-visible pull request

1. Add an entry under `[Unreleased]` in [`CHANGELOG.md`](../CHANGELOG.md).
2. Update the relevant public contract and examples.
3. Name MCP tools, HTTP paths, flags, config keys, and errors exactly.
4. Run the project gates:

   ```bash
   gofmt -w path/to/changed.go
   go vet ./...
   make test
   make validate
   git diff --check
   ```

5. For frontend changes, run frontend lint/test/build and commit the refreshed
   `internal/webui/dist/` bundle.
6. For storage, auth, or restore changes, include focused failure-path and
   cross-surface tests.

## Prepare a release candidate

1. Confirm the release commit is on `main` through the normal reviewed merge
   process.
2. Ensure the worktree is clean.
3. Re-run all required gates on that exact commit.
4. Exercise the source install in a clean environment with no private Go/npm
   configuration or local module replacements.
5. Run an isolated CLI/HTTP/MCP smoke test. Set all four XDG roots, unset
   `TESSERACT_DB_PATH` and `TESSERACT_WORKSPACE`, and confirm `tesseract path`
   resolves beneath the temporary root before any stateful command.
6. Verify a v2 backup and restore against disposable data, including a
   representative context record and memory/knowledge revision.
7. Review the [support matrix](OPERATIONS.md#support-matrix), security boundary,
   provider egress, and known limitations for accuracy.
8. Move the accumulated changelog entries from `[Unreleased]` to the release
   heading and start a fresh empty `[Unreleased]` section.

## Tag the source release

Only after the release commit has merged and the release candidate is accepted:

```bash
git switch main
git pull --ff-only
git tag -a vX.Y.Z -m "tesseract vX.Y.Z"
git push origin vX.Y.Z
```

If a GitHub release page is created, derive its notes from the matching
changelog section. List only artifacts that were actually built, checksummed,
and attached; for a source-only release, say so directly.

## Consumer upgrade note

Release notes must tell operators to:

- read breaking CLI, HTTP, MCP, Go, config, and schema changes
- stop all processes that share a store before a schema-changing upgrade
- create and verify a backup plus an offline layout copy when downgrade
  recovery matters
- rebuild/reinstall the binary and restart MCP hosts so tool schemas refresh
- verify health and representative reads before resuming writers

The complete upgrade, downgrade, backup, and restore runbooks are in
[`OPERATIONS.md`](OPERATIONS.md).
