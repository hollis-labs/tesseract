# Tesseract Beta Release Checklist

Status: draft
Audience: maintainers
Goal: prepare `github.com/hollis-labs/tesseract` for a public beta release under MIT.

## Summary

The codebase is closer to beta than the repo presentation suggests:

- `go test ./...` passes locally as of 2026-05-24.
- The core product identity is now Tesseract, but the public repo still exposes a large amount of historical naming and internal workflow history.
- The main beta blockers are packaging, install/setup UX, and public-repo cleanup rather than core test stability.

## Release blockers

### 1. Make public builds work outside the author workspace

Current state:

- `go.mod` carries no `replace` directives — `grep -c replace go.mod` → 0 — and every
  `github.com/hollis-labs/*` dependency resolves to a tagged version. The local-monorepo
  path assumption that blocked external builds is gone. Re-derive before acting on this.

Required work:

- Verify `go install github.com/hollis-labs/tesseract/cmd/tesseract@<tag>` works in a
  clean environment. Not yet done — this is the one step the measurement above cannot
  stand in for, since it exercises module resolution over the network.

Definition of done:

- Fresh-machine install works with standard Go tooling and no private path assumptions.

### 2. Replace the root README with an actual Tesseract README

Current blocker:

- [README.md](/Users/chrispian/dev/hollis-labs/apps/tesseract/README.md) is still a Tesseract private-workspace README.

Impact:

- The first thing OSS users see is the wrong product.
- Install, setup, scope, and supported providers are unclear.

Required work:

- Rewrite the root README around `Tesseract by Hollis Labs`.
- Put the quick-start path in the first screenful:
  - what Tesseract is
  - install
  - config location
  - provider env vars
  - first `serve` and first MCP setup
- Link out to architecture and API docs after the quick start, not before it.

Definition of done:

- A new user can land on the repo and get from clone to working daemon without reading private or historical docs.

### 3. Publish a clean provider setup story

Current blocker:

- Provider setup is spread across code comments and multiple docs.
- The user-facing docs do not clearly distinguish:
  - embedding provider
  - synthesis / chat provider
  - required env vars
  - what is optional

Observed current behavior:

- Embeddings: currently only `openai` is wired in [cmd/tesseract/main.go](/Users/chrispian/dev/hollis-labs/apps/tesseract/cmd/tesseract/main.go).
- Synthesis: currently `openai` and `anthropic` are wired in the same file.
- Config is read from `config.yaml` under the resolved app config dir via [internal/config/layout.go](/Users/chrispian/dev/hollis-labs/apps/tesseract/internal/config/layout.go).

Required work:

- Add a public sample config such as `examples/config.yaml` or `config/config.example.yaml`.
- Add a matching `.env.example` with `OPENAI_API_KEY` and `ANTHROPIC_API_KEY`.
- Document the minimum viable setups:
  - OpenAI-only
  - Anthropic for synthesis + OpenAI for embeddings
  - local/no-provider mode with degraded features called out explicitly
- Decide whether beta should ship with OpenAI-only embeddings or whether a second embedding provider is required before beta.

Definition of done:

- The README and quickstart make provider setup explicit and unambiguous.

### 4. Remove or quarantine non-public agent / product-history material

Current blocker:

- The repo contains substantial private-era operational material that does not belong in a public product repo.

High-noise candidates:

- [CLAUDE.md](/Users/chrispian/dev/hollis-labs/apps/tesseract/CLAUDE.md)
- [AGENTS.md](/Users/chrispian/dev/hollis-labs/apps/tesseract/AGENTS.md)
- `.agent-ops/`
- `.claude/`
- `SPEC-TASKS/`
- `artifacts/`
- `outputs/`
- `tasks/`
- `workflows/`
- `VOLON_AGENT_ENVELOPE_SPEC.md`
- `VOLON_INVARIANTS.md`
- historical execution briefs and private planning docs
- most of `user-docs-mini-site/`

Impact:

- Public users will see internal process artifacts instead of a coherent product repo.
- Some files reveal internal workflow assumptions that are unrelated to Tesseract.
- Some directories may be safe to keep privately but should not be part of release artifacts.

Required work:

- Decide repo policy:
  - remove from main public repo, or
  - move into an internal/archive branch, or
  - keep but exclude from release artifacts if they are truly maintainer-only
- Keep only public-facing docs that directly serve Tesseract users and contributors.

Definition of done:

- A public visitor sees a product repo, not a private workbench.

## High-priority cleanup

### 5. Finish naming cleanup from legacy brands to Tesseract

Examples still present:

- stale release and changelog entries that still describe retired names or compatibility stories
- generated or archived docs that were bulk-renamed but not manually curated
- MCP/server/tool identifiers that need one intentional public name

Required work:

- Remove remaining legacy-name references from active source, docs, tests, and release notes.
- Keep one canonical public identifier for the MCP/server/tool surface and document it once.
- Delete compatibility language that only exists to explain pre-release internal naming churn.

Definition of done:

- Public docs consistently say `Tesseract by Hollis Labs`.
- Historical naming only appears in migration notes and compatibility docs.

### 6. Clean the repo of generated/local artifacts

Current hygiene issues:

- Committed generated frontend bundle: `internal/webui/dist/` —
  `git ls-files internal/webui/dist | wc -l` → 3. Re-derive before acting on it.

Decision needed:

- Keep `internal/webui/dist/` committed to support `go install`, or switch to a release/build pipeline that always regenerates it before packaging.

Required work:

- Decide whether `internal/webui/dist/` is source-controlled or generated.
- Audit `.gitignore` for Tesseract-era paths instead of Tesseract-era assumptions.

Definition of done:

- The tracked tree contains source, docs, fixtures, and intentionally versioned assets only.

### 7. Make the install path simple

Current state:

- `make build` depends on frontend build steps.
- `go test ./...` works locally.
- Public install instructions are not coherent.

Required work:

- Choose one primary install path and one fallback:
  - primary: prebuilt GitHub release binaries
  - fallback: `go install .../cmd/tesseract@latest`
- Optionally add Homebrew later; it is not required for beta.
- Document exact prerequisites:
  - Go version
  - Node requirement, if building from source
  - whether frontend rebuild is needed for ordinary users

Definition of done:

- A normal user can install Tesseract without reverse-engineering the maintainer workflow.

## Documentation work

### 8. Replace or archive stale docs

Likely stale or off-scope for OSS beta:

- Large parts of `docs/0*.md`, `docs/1*.md`, and `docs/tesseract-*`
- [docs/12_model-config.md](/Users/chrispian/dev/hollis-labs/apps/tesseract/docs/12_model-config.md)
- [docs/FORGE_ALIGNMENT.md](/Users/chrispian/dev/hollis-labs/apps/tesseract/docs/FORGE_ALIGNMENT.md)
- [docs/NEXT_SESSION_BOOT_ADMIN.md](/Users/chrispian/dev/hollis-labs/apps/tesseract/docs/NEXT_SESSION_BOOT_ADMIN.md)
- `docs/superpowers/`

Required work:

- Define a public doc set for beta:
  - README
  - QUICKSTART
  - INSTALL
  - CONFIG
  - MCP setup
  - API / CLI reference
  - ARCHITECTURE
  - RELEASE notes for maintainers
- Archive or delete historical planning docs that are not useful to users.

Definition of done:

- The docs tree has a clear public information architecture.

### 9. Add provider recipes and use cases

Requested beta-friendly additions:

- Sample configs
- recipes
- use cases

Recommended public docs:

- `examples/config.openai.yaml`
- `examples/config.anthropic-openai.yaml`
- `examples/mcp.json`
- `docs/USE_CASES.md`
- `docs/RECIPES.md`

Useful recipe topics:

- Claude Code persistent memory backend
- local API daemon for one developer
- team-shared conventions for namespaces
- embeddings backfill after enabling a provider

Definition of done:

- Users can copy known-good recipes instead of inventing their own setup.

## Product and compatibility decisions

### 10. Clarify the beta support matrix

Open questions to resolve before release:

- Is the beta CLI/API/MCP only, or is the web UI part of the supported beta surface?
- Is OpenAI-only embeddings acceptable for beta?
- Is synthesis a core feature or an optional advanced feature?
- Is the MCP server name staying `tesseract` through beta, or do we want to change it later?
- Are plugin/task/workflow subsystems actually part of Tesseract, or are they leftover private tooling that should be removed?

Recommendation:

- Keep beta scope narrow:
  - context store
  - memory
  - knowledge
  - HTTP API
  - CLI
  - MCP
  - optional web UI if you are willing to support it

### 11. Add a public compatibility/migration note

Needed because the codebase has renamed more than once.

Recommended short public note:

- Tesseract is the official name.
- The Go module is `github.com/hollis-labs/tesseract`.
- Some internal compatibility names still reference `tesseract` for MCP.
- Earlier internal names should be treated as historical only.

Keep this short and put it in one place.

## Suggested execution order

### Phase 1: unblock public consumption

1. Remove `go.mod` local replaces.
2. Rewrite `README.md`.
3. Add sample config and `.env.example`.
4. Verify clean-machine install.

### Phase 2: clean the repo surface

1. Remove binary, symlink, and non-product artifacts.
2. Archive or delete Tesseract/agent docs and scripts.
3. Finish naming cleanup.

### Phase 3: improve beta onboarding

1. Rewrite `docs/QUICKSTART.md`.
2. Add install/config/provider docs.
3. Add recipes and use-case examples.
4. Decide release packaging path.

## Validation checklist

- `go test ./...`
- clean-machine `go install github.com/hollis-labs/tesseract/cmd/tesseract@<tag>`
- fresh config from sample file
- provider env vars picked up as documented
- `tesseract serve`
- MCP configured from sample `.mcp.json`
- one record write/read flow works
- one embedding flow works
- one synthesis flow works when configured

## Notes from current review

- MIT license already exists in [LICENSE](/Users/chrispian/dev/hollis-labs/apps/tesseract/LICENSE).
- The public product name should be rendered consistently as `Tesseract by Hollis Labs`.
- The current root README is the single most misleading file in the repo.
- The current packaging story is the single biggest functional blocker for OSS beta.
