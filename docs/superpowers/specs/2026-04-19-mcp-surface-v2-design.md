# MCP Surface v2 — Agent-Facing Discovery, Descriptions, and Skills

**Date:** 2026-04-19
**Status:** Design — approved, pending spec review
**Scope:** memory + knowledge + unified domain MCP tools; new `vanta_skills` meta-tool; minimal pointer-footer on context domain tools.
**Out of scope:** context domain full rewrite; new `kind=` primitives; surface consolidation.

## 1. Problem

Vanta Conduit exposes ~42 MCP tools across three domains (context, memory, knowledge) plus a unified `conduit_lookup`. Today's tool descriptions are one-liners — they convey what the tool does but not when to use it, how it relates to siblings, or where to find deeper guidance. Agents hitting the surface for the first time must either read `docs/MCP_TOOLS.md` externally or pattern-match from tool names.

Two concrete problems follow:

1. **Context bloat vs. discoverability tradeoff.** Putting everything an agent needs into tool descriptions balloons the default context window. Hiding it in external docs means agents don't find it.
2. **No progressive discovery path.** An agent with a question ("what's the difference between memory and knowledge?", "how does promotion work?") has no MCP-native way to ask Vanta itself.

## 2. Goals

- Agents can decide *when* to use each tool from its description alone — not just *what* the tool does.
- Agents can progressively load deeper orientation on demand, without paying for it at boot.
- Every tool description hits the same MCP standards (annotations, typed schemas, concrete parameter examples).
- Documentation lens stays consistent: **Vanta docs describe primitives and features; consumers author workflow skills.**

## 3. Non-goals

- Adding new kinds, domains, or storage semantics. Today is a presentation-layer change to what's already shipped.
- Rewriting the ~30 `context_*` tool descriptions in detail. They get a one-line pointer-footer; full rewrite is a future spec.
- Consolidating overlapping tools (e.g., `context_write` vs. `memory_write`). Out of scope.
- Writing consumer-facing skills (e.g., "how to track issues in Vanta"). Those belong with consumers (agent-ops, Nanite, Engine); Vanta ships primitives + reference docs only.

## 4. Architectural alignment

Vanta's invariants are structural, not semantic:

- **Rigid:** namespace ownership, actor/client_id matrix, append-only revisions, audit trail, determinism, views-as-selectors.
- **Soft:** facet vocabulary, kinds, tags, payload schema — consumer convention.

This spec respects the line. It adds no semantic rigidity. The new `vanta_skills` tool is a meta-tool serving markdown docs *about* the service — not a new content kind stored *inside* the service.

## 5. Design

### 5.1 `vanta_skills` meta-tool

A single MCP tool, registered in the adapter alongside the existing domain tools.

**Tool name:** `vanta_skills`
**Scope:** none (no capability token required — read-only orientation).
**Parameters:**
- `name` (string, optional) — skill name. If omitted, returns the index.

**Behavior:**
- No `name` → returns a JSON array of `{name, description, scope_hint}` for every registered skill.
- `name` provided → returns the full markdown body of that skill (plain text content).
- `name` unknown → returns a structured error: `skill "<name>" not found. Available: [start-here, namespaces, ...]`.

**Description (what the agent sees):**

```
Vanta's self-documenting skill index. Call with no args for the catalog; call with
`name` to read a specific skill in full. Progressive discovery — skills only load when
requested. Start with `vanta_skills start-here` for orientation.
```

**Annotations:** `readOnlyHint: true`, `idempotentHint: true`, `openWorldHint: false`.

### 5.2 Skill MD files

Eleven skills ship on day 1. Stored at `internal/mcpadapter/skills/*.md` in the Conduit repo (co-located with the Go package that embeds them — Go's `//go:embed` requires files under the package directory), embedded into the `contextd` binary via `//go:embed`.

| Skill name | Type | Body covers |
|---|---|---|
| `start-here` | orientation | What Vanta is, the three domains, invariants, how to use `vanta_skills`, cross-references. |
| `namespaces` | primitive | 5-tier model (memory/cache/pins/draft/session), ownership (user/* vs app/*), actor/client_id matrix, registration. |
| `facets-and-kinds` | primitive | What facets are, the `kind` convention (`package`/`doc`/`note` today), extension rules, how consumers add new kinds. |
| `revisions` | primitive | Append-only model, supersede chains, dedup (exact + semantic), revision IDs (monotonic ULIDs), RFC3339Nano timestamps. |
| `recall-and-ranking` | primitive | Four rankings: activation, chronological, similarity, relevance (RRF). When each is appropriate. Activation decay formula is stable. |
| `promotion` | primitive | app→user promotion workflow: request → approve → apply. Scope matrix. Why it exists. |
| `views` | primitive | Selectors-not-processors model. Namespace globs. Revision scope (head vs. all). |
| `memory` | domain | When to use the memory domain. Common patterns. Write → recall flow. How ranking modes interact. |
| `knowledge` | domain | Pointer-first model. `kind`/`source`/`pointer` facets. When to use knowledge vs. memory. |
| `context-packet` | feature | Boot workflows. `context_broker_plan` + `context_packet` vs. one-shot `context_broker_fetch`. Budget tuning. |
| `audit` | feature | Querying the audit log. Event types. Pagination. |

**Issue is explicitly excluded.** Consumer-owned (agent-ops), authored in consumer repo.

**Frontmatter schema** (YAML, each MD file):

```yaml
---
name: start-here
description: One-line hook shown in the skill index.
scope_hint: none | memory:read | memory:write | promote | namespace.admin
related: [other-skill-name, ...]
---
```

Target length per skill: 200–500 words.

### 5.3 Tool description template

Every rewritten tool's description follows this shape:

```
**<Verb phrase — one line>** under `<primary args>`.
• **Kind of content:** <what this tool handles — observations / pointers / records / etc.>
• **Scope:** `<capability scope>`
• **Use this when:** <primary use case>
• **Don't use this for:** <nearest neighbors and why — omit if no near-neighbors>
• **Deeper:** `vanta_skills <topic>` (one or two skills — pattern-level + orthogonal concern like namespaces or promotion; use one link when a second would be contrived).
```

**Example — `memory_write`:**

> **Append an agent memory revision** under `(namespace, memory_key)`.
> • **Kind of content:** agent observations, preferences, session notes — anything you'll want to recall by similarity, activation, or chronological order.
> • **Scope:** `memory:write`.
> • **Use this when:** the content is authored by you or another agent and belongs to the memory domain.
> • **Don't use this for:** pointer-to-external-content (`knowledge_write`) or generic revisioned records (`context_write`).
> • **Deeper:** `vanta_skills memory` for patterns; `vanta_skills namespaces` for namespace rules.

**Per-parameter descriptions** include a concrete example value inline — e.g.:

```
mcp.WithString("namespace",
    mcp.Required(),
    mcp.Description("Target namespace, e.g. user/chrispian/memory")),
```

### 5.4 MCP annotations

Every rewritten tool declares annotations per the MCP protocol:

| Tool category | readOnlyHint | idempotentHint | destructiveHint | openWorldHint |
|---|---|---|---|---|
| `memory_write`, `knowledge_write` | false | false | false | false |
| `memory_get`, `memory_history`, `memory_get_revision`, `memory_recall`, `knowledge_get`, `knowledge_history`, `conduit_lookup` | true | true | false | false |
| `memory_promote`, `memory_deprecate` | false | false | false | false |
| `vanta_skills` | true | true | false | false |

### 5.5 Minimal footer on context tools

Every existing `context_*` tool description gets one line appended without any other changes:

```
See `vanta_skills start-here` for the primitive model.
```

No rewrite, no annotation pass, no schema changes on context tools. This ensures an agent using `context_write` or `context_view` reaches the same orientation surface without us taking on the full `context_*` rewrite today.

### 5.6 File layout

```
vanta-conduit/
  internal/
    mcpadapter/
      skills/                  # NEW package — MDs embedded via //go:embed
        skills.go              # embed.FS, List(), Get(name)
        skills_test.go
        start-here.md
        namespaces.md
        facets-and-kinds.md
        revisions.md
        recall-and-ranking.md
        promotion.md
        views.md
        memory.md
        knowledge.md
        context-packet.md
        audit.md
      skills_tool.go           # NEW - vanta_skills tool + handler
      skills_tool_test.go
      memory_tools.go          # rewritten descriptions + annotations
      knowledge_tools.go       # rewritten descriptions + annotations
      lookup_tools.go          # rewritten descriptions + annotations
      parity_tools.go          # partial rewrite + pointer-footer
      tools.go                 # pointer-footer append on context_* descriptions
      typed_tools.go           # pointer-footer append
      bulk_tools.go            # pointer-footer append
      embedding_tools.go       # pointer-footer append
      rag_tools.go             # pointer-footer append
  docs/
    MCP_TOOLS.md               # refreshed - new Skills section
  tests/
    parity/
      parity_test.go           # add vanta_skills to surfaceCatalog
```

### 5.7 Skills package API

```go
package skills

//go:embed *.md
var skillsFS embed.FS

type SkillMeta struct {
    Name        string `json:"name"`
    Description string `json:"description"`
    ScopeHint   string `json:"scope_hint"`
    Related     []string `json:"related,omitempty"`
}

// List returns metadata for every embedded skill, sorted by a stable order
// (start-here first, then alphabetical).
func List() ([]SkillMeta, error)

// Get returns the full markdown body for a named skill.
// Returns a typed error if the skill does not exist, with the list of valid names.
var ErrSkillNotFound = errors.New("skill not found")

func Get(name string) (string, error)
```

## 6. Data flow

```
[Agent starts] → sees `vanta_skills` in tool list (short description)
     ↓
[Agent calls] vanta_skills (no args)
     ↓
[Response] JSON: [{name:"start-here", description:"...", scope_hint:"none"}, ...]
     ↓
[Agent calls] vanta_skills name="start-here"
     ↓
[Response] full markdown body — explains three domains, invariants, next steps
     ↓
[Agent calls] memory_write with payload
     ↓
[Tool description in error / in retry] points to `vanta_skills memory`
     ↓
[Agent calls] vanta_skills name="memory" only if needed
```

Context cost at boot: tool descriptions (rewritten ~150–200 words each × 12 tools) + `vanta_skills` one-liner. Skill bodies only loaded when requested. Context tools unchanged except a single-line append.

## 7. Error handling

- **`vanta_skills` unknown name:** structured error `skill "<name>" not found. Available: [...]`.
- **Embedded FS failure:** impossible at runtime — `//go:embed` is build-time; startup check in `mcpadapter.New()` fails loudly if the embed is empty.
- **Existing tool errors:** unchanged. Rewrites only touch descriptions + annotations.

## 8. Testing

### Unit

- `skills.List()` returns 11 entries with `start-here` first, then alphabetical.
- `skills.Get("start-here")` returns non-empty markdown.
- `skills.Get("missing")` returns `ErrSkillNotFound` with a list of valid names in the error message.
- Every MD file parses its YAML frontmatter successfully (table-driven test).
- Every MD file declares a non-empty `name` and `description` in frontmatter.

### Integration

- End-to-end MCP call `vanta_skills` (no args) through the adapter returns valid JSON with 11 entries.
- End-to-end MCP call `vanta_skills` with `name="start-here"` returns markdown body.
- End-to-end MCP call `vanta_skills` with unknown name returns a structured error result.

### Drift guards

- Parity test: `vanta_skills` appears in `surfaceCatalog` with HTTP peer marked `none` (MCP-only).
- Annotation-presence test: every tool registered in the memory / knowledge / unified categories has non-default `readOnlyHint` + `idempotentHint` set (catches drift when new tools are added without annotations).
- Skill-count test: `len(skills.List()) == 11` with a comment explaining to update when adding a skill.

## 9. Deliverables checklist

- [ ] `docs/agent-skills/` directory with 11 MDs, each with YAML frontmatter.
- [ ] `internal/mcpadapter/skills/` package with `List()`, `Get()`, embedded MDs.
- [ ] `skills_tool.go` registering `vanta_skills` tool + handler.
- [ ] Rewritten descriptions + annotations for 11 tools (`memory_write`, `memory_get`, `memory_history`, `memory_recall`, `memory_get_revision`, `memory_promote`, `memory_deprecate`, `knowledge_write`, `knowledge_get`, `knowledge_history`, `conduit_lookup`).
- [ ] Pointer-footer appended to every `context_*` tool description in `tools.go` + any other `*_tools.go` file that registers `context_*` tools.
- [ ] `docs/MCP_TOOLS.md` refreshed — new Skills section at top, per-tool rows include "Deeper" column pointing to skills.
- [ ] `tests/parity/parity_test.go::surfaceCatalog` includes `vanta_skills` entry.
- [ ] Tests: unit (skills package), integration (MCP adapter), drift guards (parity + annotation-presence + skill-count).
- [ ] Session artifacts in `agent-workspaces/execution/vanta-conduit/conduit-backend/2026-04-19/`.

## 10. Open questions / risks

- **Skill body length discipline.** 200–500 words per skill is a target, not a hard cap. Implementation plan should include a word-count sanity check per skill at author time.
- **Tool description length discipline.** The six-line template produces ~150–200 word descriptions. Total boot-time cost is ~12 tools × ~180 words = ~2,200 words across memory/knowledge/unified. Acceptable; worth measuring actual byte size post-rewrite.
- **MCP-go annotation support.** `mcp-go` version in `go.mod` must support tool annotations. Implementation plan should verify and bump if needed.

## 11. References

- Oracle: `agent-workspaces/knowledge/projects/vanta-conduit.md`
- Current catalog: `docs/MCP_TOOLS.md`
- Parity guardrail: `tests/parity/parity_test.go`
- MCP specification: tool annotations (`readOnlyHint`, `idempotentHint`, `destructiveHint`, `openWorldHint`)
- Design philosophy: "primitives over playbooks" (Vanta ships primitives + reference docs; consumers ship conventions)
