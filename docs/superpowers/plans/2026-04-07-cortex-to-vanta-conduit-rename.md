# Cortex → Vanta Conduit Rename Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Rename Cortex to Vanta Conduit — new standalone repo, updated module paths, all ecosystem references updated, all tests passing.

**Architecture:** Clean break migration. Copy code to new `hollis-labs/vanta-conduit` repo, update Go module path from `github.com/hollis-labs/cortex` to `github.com/hollis-labs/tesseract`, rename all internal "cortex/Cortex" references to "conduit/Conduit", update all external repos that reference Cortex.

**Tech Stack:** Go 1.26.1, SQLite, MCP (mcp-go), shared libs (go-providers, go-plugin, mcp-helpers, otel)

**Spec:** `docs/superpowers/specs/2026-04-07-cortex-to-vanta-conduit-rename.md`

---

### Task 1: Bootstrap New Repo with Code Copy

**Files:**
- Create: `~/Projects-apps/vanta-conduit/` (entire project tree)

- [ ] **Step 1: Clone the new empty repo**

```bash
cd ~/Projects-apps
git clone git@github.com:hollis-labs/vanta-conduit.git
```

- [ ] **Step 2: Copy all source code from cortex to vanta-conduit**

Copy everything except `.git/`, `.claude/`, and `.agentrc-legacy/`:

```bash
cd ~/Projects-apps/fragments-engine/cortex
rsync -av --exclude='.git' --exclude='.claude' --exclude='.agentrc-legacy' . ~/Projects-apps/vanta-conduit/
```

- [ ] **Step 3: Verify the copy**

```bash
cd ~/Projects-apps/vanta-conduit
ls -la
# Expected: cmd/ internal/ tests/ docs/ frontend/ go.mod go.sum Makefile CLAUDE.md etc.
```

- [ ] **Step 4: Commit the raw copy**

```bash
cd ~/Projects-apps/vanta-conduit
git add -A
git commit -m "chore: copy cortex source as starting point for vanta-conduit rename"
```

---

### Task 2: Update Go Module Path

**Files:**
- Modify: `go.mod` (module declaration)
- Modify: All `*.go` files containing `github.com/hollis-labs/cortex` imports

- [ ] **Step 1: Update go.mod module declaration**

In `go.mod`, change:
```
module github.com/hollis-labs/cortex
```
to:
```
module github.com/hollis-labs/tesseract
```

- [ ] **Step 2: Find-and-replace the import path in all Go files**

```bash
cd ~/Projects-apps/vanta-conduit
find . -name '*.go' -exec sed -i '' 's|github.com/hollis-labs/cortex|github.com/hollis-labs/tesseract|g' {} +
```

Verify the replacement:
```bash
grep -r 'hollis-labs/cortex' --include='*.go' .
# Expected: no results
```

- [ ] **Step 3: Update go.mod replace directives**

The current `go.mod` has replace directives pointing to `../../framework/libs/...`. These need to be updated to reflect the new repo location (`~/Projects-apps/vanta-conduit` is now a peer of `~/Projects-apps/fragments-engine`).

Update the replace paths:
```
replace github.com/hollis-labs/mcp-helpers => ../fragments-engine/libs/go-mcp
replace github.com/hollis-labs/otel => ../fragments-engine/libs/go-otel
replace github.com/hollis-labs/go-plugin => ../fragments-engine/libs/go-plugin
replace github.com/hollis-labs/go-providers => ../fragments-engine/libs/go-providers
```

Note: If these libs have moved to standalone repos under `~/Projects-apps/libs/`, update the paths accordingly. Check with `ls ~/Projects-apps/libs/` first.

- [ ] **Step 4: Verify the module compiles**

```bash
cd ~/Projects-apps/vanta-conduit
go build ./...
```

Expected: clean build, no errors.

- [ ] **Step 5: Run tests**

```bash
go test ./...
```

Expected: all tests pass.

- [ ] **Step 6: Commit**

```bash
cd ~/Projects-apps/vanta-conduit
git add -A
git commit -m "chore: update go module path from cortex to vanta-conduit"
```

---

### Task 3: Rename Package `cortex` to `conduit`

**Files:**
- Rename: `cortex.go` → `conduit.go`
- Rename: `cortex_test.go` → `conduit_test.go`
- Modify: `conduit.go` — change `package cortex` to `package conduit`
- Modify: `conduit_test.go` — change `package cortex_test` to `package conduit_test`
- Modify: `embed.go` — change `package cortex` to `package conduit`
- Modify: `embed_test.go` — change `package cortex_test` to `package conduit_test`
- Modify: `cmd/contextd/main.go` — update import from `cortex` to `conduit`, update any `cortex.Open()` calls to `conduit.Open()`
- Modify: `cmd/contextd/main_test.go` — same

- [ ] **Step 1: Rename root package files**

```bash
cd ~/Projects-apps/vanta-conduit
mv cortex.go conduit.go
mv cortex_test.go conduit_test.go
```

- [ ] **Step 2: Update package declarations in root package files**

In `conduit.go`, `conduit_test.go`, `embed.go`, `embed_test.go`, replace:
- `package cortex` → `package conduit`
- `package cortex_test` → `package conduit_test`

```bash
sed -i '' 's/^package cortex$/package conduit/' conduit.go embed.go
sed -i '' 's/^package cortex_test$/package conduit_test/' conduit_test.go embed_test.go
```

- [ ] **Step 3: Update all Go files that reference the `cortex` package name**

Search for any remaining references to the `cortex` package name (not the import path, which was already handled in Task 2):

```bash
grep -rn '"cortex"' --include='*.go' .
grep -rn 'cortex\.' --include='*.go' . | grep -v 'context\.' | grep -v '/cortex/'
```

In `cmd/contextd/main.go`, the import alias and usage will need updating. The import was `github.com/hollis-labs/cortex` (now `github.com/hollis-labs/tesseract`) and code calls `cortex.Open()`, `cortex.Config{}`, etc. These become `conduit.Open()`, `conduit.Config{}`.

```bash
find . -name '*.go' -exec sed -i '' 's/cortex\.Open/conduit.Open/g; s/cortex\.Config/conduit.Config/g; s/cortex\.Option/conduit.Option/g; s/cortex\.With/conduit.With/g; s/cortex\.DB/conduit.DB/g; s/cortex\.Close/conduit.Close/g' {} +
```

- [ ] **Step 4: Update any string literals referencing "cortex"**

Search for string literals like `"cortex"` used as identifiers, server names, etc.:

```bash
grep -rn '"cortex"' --include='*.go' .
```

Replace with `"conduit"` where they refer to the service/server name (not Go's `context` package).

- [ ] **Step 5: Verify build and tests**

```bash
go build ./...
go test ./...
```

Expected: all pass.

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "refactor: rename root package from cortex to conduit"
```

---

### Task 4: Update CLAUDE.md and Documentation

**Files:**
- Modify: `CLAUDE.md`
- Modify: `docs/RELEASE-ROADMAP.md`
- Modify: `docs/vector-search.md`
- Modify: All files under `docs/superpowers/specs/` and `docs/superpowers/plans/` that reference Cortex
- Modify: `.agentrc/config.yaml`
- Modify: `.agentrc/boot-prompt.md`

- [ ] **Step 1: Update CLAUDE.md**

Change the header from:
```markdown
# Cortex — Context Memory and RAG
```
to:
```markdown
# Vanta Conduit — Context Memory and RAG
```

- [ ] **Step 2: Bulk-replace "Cortex" with "Vanta Conduit" and "cortex" with "conduit" in docs**

```bash
cd ~/Projects-apps/vanta-conduit
find docs -name '*.md' -exec sed -i '' 's/Cortex/Vanta Conduit/g; s/cortex/conduit/g' {} +
```

Review the changes manually:
- Some instances of "cortex" in file paths or Go import paths inside docs may need specific handling (e.g., `github.com/hollis-labs/cortex` should already be `github.com/hollis-labs/tesseract` in code blocks).
- **Important**: The sed `s/cortex/conduit/g` will NOT match Go's `context` package (different string), but verify no false positives crept in.
- The rename spec doc itself (`cortex-to-vanta-conduit-rename.md`) intentionally references "Cortex" historically — don't blindly replace those.

- [ ] **Step 3: Update .agentrc/config.yaml**

Replace references to "cortex" with "conduit" or "vanta-conduit" as appropriate.

```bash
sed -i '' 's/cortex/conduit/g; s/Cortex/Vanta Conduit/g' .agentrc/config.yaml .agentrc/boot-prompt.md
```

Review the result to ensure paths and names are correct.

- [ ] **Step 4: Update Makefile references**

Check the Makefile for any "cortex" or "Cortex" strings:

```bash
grep -n -i cortex Makefile
```

The Makefile has `CONTEXTD_ROOT ?= .volon/tmp/contextd` — the `.volon` reference is legacy and should be updated too if desired, but that's a separate concern. Only update "cortex" references.

- [ ] **Step 5: Update frontend/package.json**

```bash
grep -n cortex frontend/package.json
```

Update the package name if it references cortex.

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "docs: rename all Cortex references to Vanta Conduit"
```

---

### Task 5: Update MCP Tool Names

**Files:**
- Modify: `internal/mcpadapter/adapter.go` — server name registration
- Modify: `internal/mcpadapter/tools.go` — tool name prefixes
- Modify: All files in `internal/mcpadapter/` that define tool names with "context_" prefix

- [ ] **Step 1: Find the MCP server name registration**

```bash
grep -rn 'cortex\|"context_' internal/mcpadapter/ --include='*.go'
```

The MCP server name (what appears as `mcp__cortex__` / `mcp__vanta__` in tool names) is registered in `adapter.go`. The rename target landed as `vanta` (not `conduit`); change the server registration name from `"cortex"` to `"vanta"`.

- [ ] **Step 2: Update the server name**

In `internal/mcpadapter/adapter.go`, find where the server name is set (likely in `NewServer()` or similar) and change `"cortex"` to `"vanta"`.

- [ ] **Step 3: Verify tool names**

The individual tool names (e.g., `context_write`, `context_search`, `context_view`) use a `context_` prefix which is descriptive of the operation, not the app name. These should stay as-is — they describe what the tool does, and the MCP server name `vanta` already provides the namespace (`mcp__vanta__context_write`).

Confirm:
```bash
grep -rn 'Name:.*"context_' internal/mcpadapter/ --include='*.go'
```

These should NOT be renamed.

- [ ] **Step 4: Run tests**

```bash
go test ./internal/mcpadapter/...
```

Expected: all pass.

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "refactor: update MCP server name from cortex to conduit"
```

---

### Task 6: Update Memory Files in This Repo

**Files:**
- Modify: `.claude/projects/-Users-chrispian-Projects-apps-fragments-engine-cortex/memory/MEMORY.md`

Note: The Claude memory directory path contains "cortex" in the filesystem path. This path is auto-generated by Claude Code based on the working directory. When you start working from `~/Projects-apps/vanta-conduit/`, Claude Code will create a new memory directory automatically. The old memory files don't need to be migrated — they'll be stale references to the old repo.

- [ ] **Step 1: No action needed in the new repo**

Memory files are per-working-directory and will be created fresh when Claude Code is used from the new `vanta-conduit` directory. Skip this task.

- [ ] **Step 2: Commit any remaining uncommitted changes**

```bash
cd ~/Projects-apps/vanta-conduit
git status
# If anything is uncommitted:
git add -A
git commit -m "chore: final cleanup of cortex references"
```

---

### Task 7: Full Build and Test Verification

- [ ] **Step 1: Clean build**

```bash
cd ~/Projects-apps/vanta-conduit
go clean -cache
go build ./...
```

Expected: clean build.

- [ ] **Step 2: Run all unit tests**

```bash
go test ./...
```

Expected: all pass.

- [ ] **Step 3: Run integration tests if available**

```bash
go test ./tests/integration/... -count=1
```

Note: Integration tests may require a running server. If they fail due to missing infrastructure, that's expected and not a blocker for the rename.

- [ ] **Step 4: Grep for any remaining "cortex" references (case-insensitive)**

```bash
grep -ri 'cortex' --include='*.go' --include='*.md' --include='*.yaml' --include='*.yml' --include='*.json' --include='*.toml' . | grep -v '.git/' | grep -v 'context' | grep -v 'vanta-conduit-rename'
```

Expected: no results (excluding the rename spec/plan docs which naturally mention "Cortex" historically, and Go's `context` package).

- [ ] **Step 5: Commit and push**

```bash
git add -A
git commit -m "chore: verify clean build and tests after rename"
git push -u origin main
```

---

### Task 8: Update External Repos — Nanite

**Files:**
- Modify: `~/Projects-apps/nanite/internal/contextbroker/source_cortex.go` — rename to `source_conduit.go`
- Modify: All Nanite files referencing "cortex" or "Cortex"

Nanite has deep Cortex integration: `CortexSource`, `mapToCortexIntent()`, MCP server name `"cortex"`, etc.

- [ ] **Step 1: Find all Cortex references in Nanite**

```bash
cd ~/Projects-apps/nanite
grep -ri 'cortex' --include='*.go' --include='*.md' --include='*.yaml' -l . | grep -v '.git/' | grep -v 'context'
```

- [ ] **Step 2: Rename source_cortex.go**

```bash
mv internal/contextbroker/source_cortex.go internal/contextbroker/source_conduit.go
```

- [ ] **Step 3: Update all Go references**

In `source_conduit.go`:
- `CortexSource` → `ConduitSource`
- `NewCortexSource` → `NewConduitSource`
- `"cortex"` (server name) → `"conduit"`
- `mapToCortexIntent` → `mapToConduitIntent`
- `cortexIntent` → `conduitIntent`
- All comments referencing "Cortex" → "Conduit" or "Vanta Conduit"

```bash
find . -name '*.go' -exec sed -i '' \
  's/CortexSource/ConduitSource/g; s/NewCortexSource/NewConduitSource/g; s/mapToCortexIntent/mapToConduitIntent/g; s/cortexIntent/conduitIntent/g' {} +
```

Then update string literals:
```bash
grep -rn '"cortex"' --include='*.go' .
```
Change MCP server name references from `"cortex"` to `"conduit"`.

- [ ] **Step 4: Update doc and config references**

```bash
find . -name '*.md' -name '*.yaml' -exec sed -i '' 's/Cortex/Vanta Conduit/g; s/cortex/conduit/g' {} +
```

Review changes — be careful not to change Go's `context` package references in markdown code blocks.

- [ ] **Step 5: Build and test Nanite**

```bash
go build ./...
go test ./...
```

Expected: all pass.

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "refactor: rename Cortex references to Vanta Conduit (Conduit)"
```

---

### Task 9: Update External Repos — Cerberus

**Files:**
- Modify: `~/Projects-apps/cerberus/internal/connector/local/mapper_test.go`
- Modify: `~/Projects-apps/cerberus/internal/service/portcheck_test.go`
- Modify: `~/Projects-apps/cerberus/README.md`

- [ ] **Step 1: Find all Cortex references**

```bash
cd ~/Projects-apps/cerberus
grep -ri 'cortex' --include='*.go' --include='*.md' --include='*.yaml' -n . | grep -v '.git/' | grep -v 'context'
```

- [ ] **Step 2: Update references**

These are likely service name references in test fixtures and README. Update:
- `"cortex"` → `"conduit"` (service names)
- `Cortex` → `Vanta Conduit` (docs)

- [ ] **Step 3: Build and test**

```bash
go build ./...
go test ./...
```

- [ ] **Step 4: Commit**

```bash
git add -A
git commit -m "refactor: rename Cortex service references to Conduit"
```

---

### Task 10: Update External Repos — Hadron

**Files:**
- Modify: Files in `~/Projects-apps/hadron/` that reference Cortex (mostly demos, docs, legacy tasks)

- [ ] **Step 1: Find all Cortex references**

```bash
cd ~/Projects-apps/hadron
grep -ri 'cortex' --include='*.go' --include='*.md' --include='*.yaml' -l . | grep -v '.git/' | grep -v 'context' | grep -v '.agentrc-legacy/'
```

Focus on active code and docs, skip `.agentrc-legacy/` (archived task files).

- [ ] **Step 2: Update demo blueprints**

In files like `demos/03-cross-system-mcp.yaml` and the security swarm demos, update MCP server references from `cortex` to `conduit`.

- [ ] **Step 3: Update README and active docs**

```bash
sed -i '' 's/Cortex/Vanta Conduit/g; s/"cortex"/"conduit"/g' README.md
```

- [ ] **Step 4: Build and test**

```bash
go build ./...
go test ./...
```

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "refactor: rename Cortex references to Vanta Conduit in demos and docs"
```

---

### Task 11: Update External Repos — Engine

**Files:**
- Modify: Files in `~/Projects-apps/fragments-engine/engine/` referencing Cortex

- [ ] **Step 1: Find references**

```bash
cd ~/Projects-apps/fragments-engine/engine
grep -ri 'cortex' --include='*.go' --include='*.md' --include='*.yaml' -l . | grep -v '.git/' | grep -v 'context' | grep -v '.agentrc-legacy/'
```

- [ ] **Step 2: Update active code references**

The main code reference is in `internal/guiserver/httpserver/server.go` and `internal/mcpadapter/tools.go`. Update service names and MCP references.

- [ ] **Step 3: Build and test**

```bash
go build ./...
go test ./...
```

- [ ] **Step 4: Commit**

```bash
git add -A
git commit -m "refactor: rename Cortex references to Vanta Conduit"
```

---

### Task 12: Update External Repos — agentrc

**Files:**
- Modify: Files in `~/Projects-apps/agentrc/` referencing Cortex (config, skills, roles, commands)

- [ ] **Step 1: Find references**

```bash
cd ~/Projects-apps/agentrc
grep -ri 'cortex' --include='*.md' --include='*.yaml' -l . | grep -v '.git/'
```

- [ ] **Step 2: Update config.yaml**

This is the central agent config. Update project paths, agent definitions, and skill references that mention cortex.

- [ ] **Step 3: Update skills and commands**

Skills like `doc-search.md`, `doc-note.md`, `qstatus.md`, `qhealth.md`, etc. likely reference Cortex as an MCP server name. Update to `conduit`.

- [ ] **Step 4: Update roles**

Roles that reference Cortex context operations should be updated.

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "refactor: rename Cortex references to Vanta Conduit across agentrc"
```

---

### Task 13: Update Claude Code Memory

**Files:**
- Modify: `~/.claude/projects/-Users-chrispian-Projects-apps-fragments-engine-cortex/memory/MEMORY.md`
- Modify: linked memory files in that directory

- [ ] **Step 1: Update existing memory entries**

The Cortex-specific memory directory will become stale once work moves to `~/Projects-apps/vanta-conduit/`. Update the MEMORY.md entries to note the rename:

In `project_embedding_provider.md` and `project_dcore_complete.md`, add a note that Cortex has been renamed to Vanta Conduit and the new repo is `hollis-labs/vanta-conduit`.

- [ ] **Step 2: No migration needed**

Claude Code will create a new memory directory based on the `vanta-conduit` working directory path. Relevant memories will be recreated as you work in the new repo.

---

### Task 14: Final Cross-Repo Verification

- [ ] **Step 1: Grep across all projects for stale references**

```bash
cd ~/Projects-apps
grep -ri 'hollis-labs/cortex' --include='*.go' --include='*.mod' -r . | grep -v '.git/' | grep -v 'vanta-conduit'
```

Expected: no results (except possibly archived projects in `.archived/`).

- [ ] **Step 2: Grep for "cortex" in active MCP configs**

```bash
grep -ri '"cortex"' --include='*.go' --include='*.yaml' --include='*.json' -r nanite cerberus hadron fragments-engine/engine agentrc | grep -v '.git/' | grep -v 'context'
```

Expected: no results.

- [ ] **Step 3: Verify vanta-conduit builds clean**

```bash
cd ~/Projects-apps/vanta-conduit
go clean -cache && go build ./... && go test ./...
```

- [ ] **Step 4: Push all repos**

Push each updated repo (vanta-conduit, nanite, cerberus, hadron, engine, agentrc) after confirming all builds pass.
