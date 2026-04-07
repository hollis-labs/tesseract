# Cortex → Vanta Conduit Rename

## Summary

Rename the Cortex context memory and RAG service to **Vanta Conduit** to avoid trademark conflicts with Snowflake Cortex, Palo Alto Cortex, and other established products using the name.

- **Full name**: Vanta Conduit (branding, docs, repo name)
- **Short form**: Conduit (CLI binary, casual reference, internal code)
- **Origin**: "Vanta" from vantablack (the blackest black) and the Vanta Guard — a faction from Chrispian's sci-fi setting that operates near a singularity, using exotic matter to receive information from the future. "Conduit" — the channel that stores and brokers context.

## What the App Does

Vanta Conduit is the context memory and RAG service for the Hollis Labs ecosystem. It stores context (embeddings, documents, metadata), brokers context delivery to consumers, and provides retrieval-augmented search. It serves as the knowledge backbone that other apps (Hadron, Nanite, Engine) depend on.

## Scope

### In scope

**Code/Repo:**
- GitHub repo: create `hollis-labs/vanta-conduit` (new repo, clean break)
- Go module path: `github.com/hollis-labs/vanta-conduit`
- Rename all internal package names, imports, and code references
- Binary/CLI name: `conduit`

**Ecosystem references (other repos):**
- Cerberus service definitions
- Hadron blueprints referencing Cortex
- Nanite API calls to Cortex
- agentrc configs and CLAUDE.md files across projects
- Shared libs (go-queue, go-provider) if they reference Cortex
- MCP server/tool names (`mcp__cortex__*` → `mcp__conduit__*`)

**Docs/Config:**
- CLAUDE.md files across all projects
- Memory files (MEMORY.md and linked memories in this project)
- Spec docs and roadmaps
- Any existing design docs that reference Cortex

### Not in scope

- Domain/DNS setup (conduit.hollislabs.com — handled later)
- Other app renames (Fragments Engine, Cerberus, Sigil, etc.)
- Repo org restructuring beyond extracting Vanta Conduit to its own repo

## Migration Strategy: Clean Break

Since nothing is publicly released and no external consumers depend on current module paths, this is a clean break migration:

1. Create new `hollis-labs/vanta-conduit` GitHub repo
2. Move code from `fragments-engine/cortex/` to the new repo
3. Update Go module path to `github.com/hollis-labs/vanta-conduit`
4. Rename all internal references (package names, imports, comments, configs)
5. Update all external references across the ecosystem (Cerberus, Hadron, Nanite, agentrc, CLAUDE.md files, memory files)
6. Update MCP server registration and tool names
7. Archive the old `cortex` directory under fragments-engine (or remove it once confirmed)
8. Verify all builds pass across affected repos

## Naming Convention

| Context | Usage |
|---------|-------|
| GitHub repo | `hollis-labs/vanta-conduit` |
| Go module | `github.com/hollis-labs/vanta-conduit` |
| CLI binary | `conduit` |
| Subdomain | `conduit.hollislabs.com` |
| MCP tools | `mcp__conduit__*` |
| Casual reference | Conduit |
| Official/branding | Vanta Conduit |
| Local directory | `~/Projects-apps/vanta-conduit/` (peer to other apps) |

## Umbrella Brand

All apps ship under **Hollis Labs** (`hollis-labs` GitHub org, `hollislabs.com`). Vanta Conduit is a Hollis Labs project. No separate org or domain needed.
