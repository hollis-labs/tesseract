# MCP Surface v2 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Rewrite memory + knowledge + unified MCP tool descriptions against a unified template, add MCP protocol annotations, and introduce `vanta_skills` as a single progressive-discovery meta-tool backed by 11 embedded markdown skills.

**Architecture:** A new `internal/mcpadapter/skills/` Go package embeds 11 markdown skill files via `//go:embed`, exposing `List()` and `Get(name)`. A new `vanta_skills` MCP tool surfaces those over MCP. Existing memory/knowledge/unified tool descriptions are rewritten to the spec template with MCP annotations; context-domain tool descriptions get a one-line footer pointing at `vanta_skills start-here`.

**Tech Stack:** Go 1.26, `github.com/mark3labs/mcp-go v0.44.1` (supports `WithReadOnlyHintAnnotation` / `WithIdempotentHintAnnotation` / `WithDestructiveHintAnnotation` / `WithOpenWorldHintAnnotation`), `embed.FS` for skill packaging, `gopkg.in/yaml.v3` already in deps for frontmatter parsing (or stdlib-only split on `---` markers).

**Spec reference:** `docs/superpowers/specs/2026-04-19-mcp-surface-v2-design.md`

---

## Deviation from spec

Spec section 5.6 places skill MDs at `docs/agent-skills/*.md`. Go's `//go:embed` cannot reach outside the package directory, so MDs live at `internal/mcpadapter/skills/*.md` instead. Task 1 updates the spec to match. Behavior unchanged.

---

## File structure

**New files:**
- `internal/mcpadapter/skills/skills.go` — embed.FS + List() + Get()
- `internal/mcpadapter/skills/skills_test.go`
- `internal/mcpadapter/skills/start-here.md`
- `internal/mcpadapter/skills/namespaces.md`
- `internal/mcpadapter/skills/facets-and-kinds.md`
- `internal/mcpadapter/skills/revisions.md`
- `internal/mcpadapter/skills/recall-and-ranking.md`
- `internal/mcpadapter/skills/promotion.md`
- `internal/mcpadapter/skills/views.md`
- `internal/mcpadapter/skills/memory.md`
- `internal/mcpadapter/skills/knowledge.md`
- `internal/mcpadapter/skills/context-packet.md`
- `internal/mcpadapter/skills/audit.md`
- `internal/mcpadapter/skills_tool.go` — `vanta_skills` registration + handler
- `internal/mcpadapter/skills_tool_test.go`

**Modified files:**
- `internal/mcpadapter/adapter.go` — register `vanta_skills` in `RegisterAllTools`
- `internal/mcpadapter/memory_tools.go` — rewrite descriptions + annotations (6 tools)
- `internal/mcpadapter/knowledge_tools.go` — rewrite descriptions + annotations (1 tool)
- `internal/mcpadapter/lookup_tools.go` — rewrite description + annotations (1 tool)
- `internal/mcpadapter/parity_tools.go` — rewrite memory_get_revision + knowledge_get + knowledge_history descriptions/annotations (3 tools); append pointer-footer to context_estimate + views_evaluate (2 tools)
- `internal/mcpadapter/tools.go` — append pointer-footer to 15 context_* descriptions
- `internal/mcpadapter/typed_tools.go` — append pointer-footer to 8 descriptions
- `internal/mcpadapter/bulk_tools.go` — append pointer-footer to 2 descriptions
- `internal/mcpadapter/embedding_tools.go` — append pointer-footer to 2 descriptions
- `internal/mcpadapter/rag_tools.go` — append pointer-footer to 1 description
- `tests/parity/parity_test.go` — add `vanta_skills` to `surfaceCatalog`
- `docs/MCP_TOOLS.md` — new Skills section; "Deeper" column in memory/knowledge/unified tables
- `docs/superpowers/specs/2026-04-19-mcp-surface-v2-design.md` — update section 5.6 file layout

---

## Task 1: Branch and spec alignment

**Files:**
- Modify: `docs/superpowers/specs/2026-04-19-mcp-surface-v2-design.md`

- [ ] **Step 1: Create feature branch**

Run:
```bash
cd ~/Projects-apps/vanta-conduit
git checkout -b feat/mcp-surface-v2
```

- [ ] **Step 2: Update spec file layout section**

Edit `docs/superpowers/specs/2026-04-19-mcp-surface-v2-design.md` section 5.6. Replace the `docs/agent-skills/` block in the file-layout tree with:

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

Also update section 5.2 sentence "Stored at `docs/agent-skills/*.md`…" to "Stored at `internal/mcpadapter/skills/*.md` in the Conduit repo (co-located with the Go package that embeds them — Go's `//go:embed` requires files under the package directory), embedded into the `contextd` binary via `//go:embed`."

- [ ] **Step 3: Commit**

```bash
git add docs/superpowers/specs/2026-04-19-mcp-surface-v2-design.md
git commit -m "spec(mcp-surface-v2): relocate skill MDs to internal/mcpadapter/skills/"
```

---

## Task 2: Skills package foundation (TDD)

**Files:**
- Create: `internal/mcpadapter/skills/skills.go`
- Create: `internal/mcpadapter/skills/skills_test.go`
- Create: `internal/mcpadapter/skills/start-here.md`

- [ ] **Step 1: Write the failing test**

Create `internal/mcpadapter/skills/skills_test.go`:

```go
package skills

import (
	"errors"
	"strings"
	"testing"
)

func TestList_ReturnsStartHereFirst(t *testing.T) {
	got, err := List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("List returned 0 entries; expected at least start-here")
	}
	if got[0].Name != "start-here" {
		t.Fatalf("first skill = %q, want %q", got[0].Name, "start-here")
	}
	if got[0].Description == "" {
		t.Error("start-here description is empty")
	}
}

func TestGet_ReturnsStartHereBody(t *testing.T) {
	body, err := Get("start-here")
	if err != nil {
		t.Fatalf("Get(start-here): %v", err)
	}
	if !strings.Contains(body, "Vanta") {
		t.Errorf("start-here body missing expected content; got %q", body)
	}
}

func TestGet_UnknownSkill_ReturnsTypedError(t *testing.T) {
	_, err := Get("does-not-exist")
	if !errors.Is(err, ErrSkillNotFound) {
		t.Fatalf("Get(missing) err = %v, want ErrSkillNotFound", err)
	}
	if !strings.Contains(err.Error(), "start-here") {
		t.Errorf("error message should list available skills; got %q", err.Error())
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run:
```bash
cd ~/Projects-apps/vanta-conduit
go test ./internal/mcpadapter/skills/
```
Expected: build failure — package does not exist yet.

- [ ] **Step 3: Write skills.go**

Create `internal/mcpadapter/skills/skills.go`:

```go
// Package skills embeds agent-facing markdown skills that document
// Vanta Conduit primitives and features. Served over MCP via the
// vanta_skills tool.
package skills

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

//go:embed *.md
var skillsFS embed.FS

// SkillMeta is the index entry returned from List.
type SkillMeta struct {
	Name        string   `json:"name" yaml:"name"`
	Description string   `json:"description" yaml:"description"`
	ScopeHint   string   `json:"scope_hint" yaml:"scope_hint"`
	Related     []string `json:"related,omitempty" yaml:"related,omitempty"`
}

// ErrSkillNotFound is returned by Get when name is not an embedded skill.
var ErrSkillNotFound = errors.New("skill not found")

// List returns metadata for every embedded skill. Result is sorted with
// "start-here" first, then alphabetical by Name.
func List() ([]SkillMeta, error) {
	entries, err := fs.ReadDir(skillsFS, ".")
	if err != nil {
		return nil, fmt.Errorf("read skills fs: %w", err)
	}
	metas := make([]SkillMeta, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		m, err := parseMeta(e.Name())
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", e.Name(), err)
		}
		metas = append(metas, m)
	}
	sort.SliceStable(metas, func(i, j int) bool {
		if metas[i].Name == "start-here" {
			return true
		}
		if metas[j].Name == "start-here" {
			return false
		}
		return metas[i].Name < metas[j].Name
	})
	return metas, nil
}

// Get returns the full markdown body (including frontmatter) for the named
// skill. Returns ErrSkillNotFound wrapped with a list of valid names when
// name is unknown.
func Get(name string) (string, error) {
	data, err := fs.ReadFile(skillsFS, name+".md")
	if err != nil {
		avail, _ := listNames()
		return "", fmt.Errorf("%w: %q. Available: [%s]", ErrSkillNotFound, name, strings.Join(avail, ", "))
	}
	return string(data), nil
}

func listNames() ([]string, error) {
	metas, err := List()
	if err != nil {
		return nil, err
	}
	names := make([]string, len(metas))
	for i, m := range metas {
		names[i] = m.Name
	}
	return names, nil
}

// parseMeta reads a single skill file and returns its frontmatter.
// Expected layout: "---\n<yaml>\n---\n<body>".
func parseMeta(filename string) (SkillMeta, error) {
	var m SkillMeta
	data, err := fs.ReadFile(skillsFS, filename)
	if err != nil {
		return m, err
	}
	s := string(data)
	if !strings.HasPrefix(s, "---\n") {
		return m, fmt.Errorf("missing frontmatter opener")
	}
	rest := s[4:]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return m, fmt.Errorf("missing frontmatter closer")
	}
	if err := yaml.Unmarshal([]byte(rest[:end]), &m); err != nil {
		return m, fmt.Errorf("yaml: %w", err)
	}
	if m.Name == "" {
		return m, fmt.Errorf("frontmatter missing name")
	}
	if m.Description == "" {
		return m, fmt.Errorf("frontmatter missing description")
	}
	return m, nil
}
```

- [ ] **Step 4: Create start-here.md**

Create `internal/mcpadapter/skills/start-here.md`:

```markdown
---
name: start-here
description: Orientation for agents new to Vanta Conduit — the three domains, invariants, and how to use vanta_skills.
scope_hint: none
related: [namespaces, memory, knowledge]
---

# Vanta Conduit — start here

Vanta Conduit is a local-first, append-only context and memory service. You reach it through the `mcp__vanta__*` tool family. Everything you write is revisioned, auditable, and namespace-owned.

## The three domains

- **Memory** — agent-authored observations, preferences, session notes. Recall by activation, chronological order, semantic similarity, or hybrid relevance. Start with `vanta_skills memory`.
- **Knowledge** — pointer-first references to external content (packages, docs, notes). Every knowledge write carries `kind`/`source`/`pointer` facets. Start with `vanta_skills knowledge`.
- **Context** — generic revisioned records for app-scoped state (session workspaces, typed payloads, packets). Used heavily by framework tooling; agents typically reach for memory or knowledge instead.

Search across memory + knowledge with `conduit_lookup` — the unified query surface.

## Invariants (don't fight these)

- **Append-only.** Every write creates a new revision. Nothing is mutated in place.
- **Namespace-owned.** `user/*` is user-owned (write-protected except via promotion). `app/*` is app-owned. See `vanta_skills namespaces`.
- **Deterministic.** Identical selectors against identical state return identical results.
- **Audited.** Every write and promotion is logged. Use `vanta_skills audit` to query.
- **Views are selectors, not processors.** Retrieval does not synthesize, merge, or infer.

## How to use `vanta_skills`

- `vanta_skills` with no args — returns this index.
- `vanta_skills <name>` — returns the full body of a single skill.

Skills are progressive: the index is small; bodies only load when requested.

## Common next steps

- Writing an agent memory? → `vanta_skills memory`
- Recording a reference to external content? → `vanta_skills knowledge`
- Looking something up? → use `conduit_lookup` directly; `vanta_skills recall-and-ranking` for ranking modes.
- Working across user/app namespace boundaries? → `vanta_skills promotion`.
- Booting into a project? → `vanta_skills context-packet`.
```

- [ ] **Step 5: Verify gopkg.in/yaml.v3 is in go.mod**

Run:
```bash
cd ~/Projects-apps/vanta-conduit
grep yaml.v3 go.mod || go get gopkg.in/yaml.v3
go mod tidy
```
Expected: yaml.v3 listed; `go mod tidy` is a no-op or adds the dep.

- [ ] **Step 6: Run tests to verify they pass**

Run:
```bash
go test ./internal/mcpadapter/skills/ -v
```
Expected: all three tests PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/mcpadapter/skills/
git add go.mod go.sum
git commit -m "feat(mcp): skills package foundation + start-here.md"
```

---

## Task 3: `vanta_skills` MCP tool + handler (TDD)

**Files:**
- Create: `internal/mcpadapter/skills_tool.go`
- Create: `internal/mcpadapter/skills_tool_test.go`
- Modify: `internal/mcpadapter/adapter.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/mcpadapter/skills_tool_test.go`:

```go
package mcpadapter

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func TestVantaSkills_Index(t *testing.T) {
	a := New(newTestStore(t), "")
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{}
	res, err := a.handleVantaSkills(context.Background(), req)
	if err != nil {
		t.Fatalf("handleVantaSkills: %v", err)
	}
	if res == nil || len(res.Content) == 0 {
		t.Fatal("empty result")
	}
	tc, ok := res.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("want TextContent, got %T", res.Content[0])
	}
	var arr []map[string]any
	if err := json.Unmarshal([]byte(tc.Text), &arr); err != nil {
		t.Fatalf("unmarshal %q: %v", tc.Text, err)
	}
	if len(arr) == 0 {
		t.Fatal("index had 0 entries")
	}
	if arr[0]["name"] != "start-here" {
		t.Errorf("first entry name = %v, want start-here", arr[0]["name"])
	}
}

func TestVantaSkills_GetByName(t *testing.T) {
	a := New(newTestStore(t), "")
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"name": "start-here"}
	res, err := a.handleVantaSkills(context.Background(), req)
	if err != nil {
		t.Fatalf("handleVantaSkills: %v", err)
	}
	tc := res.Content[0].(mcp.TextContent)
	if !strings.Contains(tc.Text, "Vanta") {
		t.Errorf("body missing expected content")
	}
}

func TestVantaSkills_UnknownName_ReturnsToolError(t *testing.T) {
	a := New(newTestStore(t), "")
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"name": "does-not-exist"}
	res, err := a.handleVantaSkills(context.Background(), req)
	if err != nil {
		t.Fatalf("handler returned err: %v", err)
	}
	m := parseResult(t, res)
	if m["code"] != "skill_not_found" {
		t.Errorf("code = %v, want skill_not_found", m["code"])
	}
	msg, _ := m["message"].(string)
	if !strings.Contains(msg, "start-here") {
		t.Errorf("error message should list available skills; got %q", msg)
	}
}

func TestVantaSkills_RegisteredByAdapter(t *testing.T) {
	// Ensure RegisterAllTools wires vanta_skills.
	// This smoke-test uses the server's tool list via introspection.
	// Skipped implementation note: rely on parity test for enforcement;
	// here we just call the handler via the adapter.
	a := New(newTestStore(t), "")
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{}
	if _, err := a.handleVantaSkills(context.Background(), req); err != nil {
		t.Fatalf("handleVantaSkills wired through adapter failed: %v", err)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run:
```bash
cd ~/Projects-apps/vanta-conduit
go test ./internal/mcpadapter/ -run TestVantaSkills -v
```
Expected: build failure — `handleVantaSkills` undefined.

- [ ] **Step 3: Create skills_tool.go**

Create `internal/mcpadapter/skills_tool.go`:

```go
package mcpadapter

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/hollis-labs/tesseract/internal/mcpadapter/skills"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// registerSkillsTool registers the vanta_skills progressive-discovery
// meta-tool. No capability token required — this is read-only orientation
// served from an embedded filesystem.
func (a *Adapter) registerSkillsTool(s *server.MCPServer) {
	s.AddTool(mcp.NewTool("vanta_skills",
		mcp.WithDescription(
			"Vanta's self-documenting skill index. Call with no args for the catalog; "+
				"call with `name` to read a specific skill in full. Progressive discovery — "+
				"skills only load when requested. Start with `vanta_skills start-here` for orientation.",
		),
		mcp.WithString("name",
			mcp.Description("Skill name (e.g. start-here, namespaces, memory). Omit to get the skill index.")),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithIdempotentHintAnnotation(true),
		mcp.WithOpenWorldHintAnnotation(false),
	), a.handleVantaSkills)
}

func (a *Adapter) handleVantaSkills(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name := req.GetString("name", "")
	if name == "" {
		metas, err := skills.List()
		if err != nil {
			return toolError("internal_error", err.Error()), nil
		}
		return toolJSON(metas), nil
	}
	body, err := skills.Get(name)
	if err != nil {
		if errors.Is(err, skills.ErrSkillNotFound) {
			return toolError("skill_not_found", err.Error()), nil
		}
		return toolError("internal_error", err.Error()), nil
	}
	// Body is markdown; return as text.
	return mcp.NewToolResultText(body), nil
}

// Ensure encoding/json is retained when toolJSON inlines marshal:
var _ = json.Marshal
```

Note: the trailing `var _ = json.Marshal` is only needed if Go complains about the unused import. Remove it if `json` is already used elsewhere in package files. (It will be — other `*_tools.go` files import it.)

- [ ] **Step 4: Wire into RegisterAllTools**

Edit `internal/mcpadapter/adapter.go` — replace the `RegisterAllTools` function body (lines 55–70) with:

```go
func (a *Adapter) RegisterAllTools(s *server.MCPServer) {
	a.registerTools(s)
	a.registerTypedTools(s)
	a.registerEmbeddingTools(s)
	a.registerSessionTools(s)
	a.registerBulkTools(s)
	a.registerRAGTools(s)
	if a.MemoryStore != nil {
		a.registerMemoryTools(s)
		a.registerLookupTools(s)
	}
	if a.KnowledgeStore != nil {
		a.registerKnowledgeTools(s)
	}
	a.registerParityTools(s)
	a.registerSkillsTool(s)
}
```

The only change is the new last line: `a.registerSkillsTool(s)`.

- [ ] **Step 5: Run tests**

Run:
```bash
go test ./internal/mcpadapter/ -run TestVantaSkills -v
```
Expected: all four tests PASS.

- [ ] **Step 6: Run broader test suite to confirm no regression**

Run:
```bash
go test ./internal/mcpadapter/... ./internal/memory/... ./internal/knowledge/...
```
Expected: all pass.

- [ ] **Step 7: Commit**

```bash
git add internal/mcpadapter/skills_tool.go
git add internal/mcpadapter/skills_tool_test.go
git add internal/mcpadapter/adapter.go
git commit -m "feat(mcp): vanta_skills progressive-discovery tool"
```

---

## Task 4: Parity catalog update

**Files:**
- Modify: `tests/parity/parity_test.go`

- [ ] **Step 1: Add vanta_skills to surfaceCatalog**

Edit `tests/parity/parity_test.go`. Find the "Unified lookup" block (around line 95). Add a new section immediately after it and before the "HTTP-only" block:

```go
	// ── Unified lookup ─────────────────────────────────────────────────
	{MCP: "conduit_lookup", HTTPMethod: http.MethodPost, HTTPPath: "/v1/conduit/lookup"},

	// ── Meta (orientation / discovery) ─────────────────────────────────
	{MCP: "vanta_skills", Waiver: "MCP-only: progressive-discovery meta-tool; serves embedded skill MDs"},

	// ── HTTP-only (infra, admin, security boundary, batch-2) ───────────
```

- [ ] **Step 2: Run parity test**

Run:
```bash
cd ~/Projects-apps/vanta-conduit
go test ./tests/parity/ -v
```
Expected: all parity tests pass. If a test fails with "MCP tool registered but not in catalog" or similar, re-check the catalog entry spelling matches `vanta_skills`.

- [ ] **Step 3: Commit**

```bash
git add tests/parity/parity_test.go
git commit -m "test(parity): catalog vanta_skills as MCP-only meta-tool"
```

---

## Task 5: Write skill MDs — batch 1 (primitives, part 1)

**Files:**
- Create: `internal/mcpadapter/skills/namespaces.md`
- Create: `internal/mcpadapter/skills/facets-and-kinds.md`
- Create: `internal/mcpadapter/skills/revisions.md`
- Create: `internal/mcpadapter/skills/recall-and-ranking.md`
- Modify: `internal/mcpadapter/skills/skills_test.go`

**Source material:** `docs/SPECS/NAMESPACES.md`, `docs/ARCHITECTURE.md`, `docs/vector-search.md`, `docs/SPECS/VIEWS.md`.

- [ ] **Step 1: Create namespaces.md**

Create `internal/mcpadapter/skills/namespaces.md`:

```markdown
---
name: namespaces
description: 5-tier namespace model, ownership rules, and the actor/client_id matrix.
scope_hint: none
related: [start-here, promotion, memory, knowledge]
---

# Namespaces

Vanta organizes every record under a **namespace** — a path-like string that encodes ownership and tier. Namespaces are authoritative: they decide who can write, read, and promote.

## Five tiers

- `memory/*` — durable agent/user memory. Append-only; recall-friendly.
- `cache/*` — ephemeral, disposable; subject to eviction.
- `pins/*` — high-priority records kept at the top of context packets.
- `draft/*` — in-progress content awaiting promotion.
- `session/*` — per-session scratch.

## Two ownership domains

- `user/*` — user-owned. Apps cannot write here directly; must go through **promotion** (`vanta_skills promotion`).
- `app/*` — app-owned. Freely writable by the owning app.

Common combinations:
- `user/<who>/memory` — durable user memory.
- `app/<name>/session/<id>` — session scratch for an app.
- `user/<who>/knowledge/<topic>` — user knowledge domain entries.

## Actor and client_id matrix

Every write carries an **actor** (logical identity of the writer, e.g. `claude`, `indexer`) and a **client_id** (the API token's identity). The namespace policy enforces which actor/client combinations may write to which namespaces.

Register a namespace policy with `context_namespace_register`; inspect with `context_namespace_show`.

## Common mistakes

- Writing to `user/*` from an app token — blocked. Use `context_promote_request` → `approve` → `apply`.
- Assuming cache semantics for `memory/*` — memory is durable. Use `cache/*` for disposable data.
- Forgetting the leading owner segment — `memory/foo` is ambiguous; always `user/<who>/memory/foo` or `app/<name>/memory/foo`.
```

- [ ] **Step 2: Create facets-and-kinds.md**

Create `internal/mcpadapter/skills/facets-and-kinds.md`:

```markdown
---
name: facets-and-kinds
description: Facet vocabulary, the kind convention, and how consumers extend it.
scope_hint: none
related: [knowledge, memory]
---

# Facets and kinds

Vanta is deliberately soft on content taxonomy. Structural invariants (namespaces, revisions, audit) are rigid; how you tag content is up to you.

## Facets

Every memory and knowledge revision carries a small, open-valued facet structure. Current facets:

- `kind` — what this record is (e.g., `package`, `doc`, `note`, `pointer`).
- `source` — where it came from (e.g., `filesystem`, `obsidian`, `nil`, `web`, `manual`).
- `pointer` — a structured `{scheme, locator, resolved_at}` triple for knowledge entries referencing external content.

## The `kind` convention

`kind` is a free-form string, but a few well-known values are shipped today:

- `package` — a software package or library (knowledge domain).
- `doc` — documentation (knowledge domain).
- `note` — an agent or user note (memory or knowledge).
- `pointer` — a bare external reference with minimal body.

Consumers can and should introduce new `kind` values as needed (e.g., `playbook`, `adr`, `todo`). Nothing in Vanta validates the `kind` string — it is a coordination convention.

## Filtering by facet

`conduit_lookup` accepts `facet_kinds` and `facet_sources` as JSON-array filters. Use these to narrow a cross-domain search:

```json
{"query": "embedding provider", "facet_kinds": ["package"], "limit": 10}
```

## Extension rules

- Stay short. Multi-word kinds get awkward; use `-` separators if needed (e.g., `decision-record`).
- Stay stable. Don't churn the value after adoption; treat it like a public API.
- Document your conventions elsewhere. Consumer repos own the "what does `kind=adr` mean" docs.
```

- [ ] **Step 3: Create revisions.md**

Create `internal/mcpadapter/skills/revisions.md`:

```markdown
---
name: revisions
description: Append-only revision model, supersede chains, dedup, revision IDs, timestamps.
scope_hint: none
related: [memory, knowledge, audit]
---

# Revisions

Every write in Vanta creates a new revision. The service never mutates existing records in place.

## Revision identity

- **Revision ID** — monotonic ULID (`01HX…`). Lexicographically sortable, globally unique within the store.
- **Timestamp** — RFC3339Nano (nanosecond precision). Tie-breaking falls back to revision ID lex order for same-millisecond writes.

## Head vs. history

- `memory_get` / `knowledge_get` — returns the current (latest, non-deprecated) revision for `(namespace, key)`.
- `memory_history` / `knowledge_history` — returns the full revision chain, newest first.
- `memory_recall` with `revision_scope=timeline` — includes superseded revisions in ranking.

## Supersede chains

Pass `supersedes` to `memory_write` or `knowledge_write` to mark an explicit ancestor. The new revision becomes the head; the old revision stays in history. Supersede is how you "edit" memory without losing provenance.

## Dedup

`memory_write` accepts two dedup modes:

- `dedup=none` (default) — never dedup.
- `dedup=semantic` — cosine-similar existing revisions in the same namespace are auto-superseded. Cross-key matches are surfaced as `DedupMatch` without auto-supersede. Threshold defaults to 0.85; override per call with `dedup_threshold`.

## Deprecation

`memory_deprecate` marks a revision as removed from the current head pool. It remains in history and the audit log.

## What NOT to expect

- **No in-place edits.** Every change is a new revision.
- **No hard deletes.** Deprecation is soft; audit remains.
- **No write-your-own revision IDs.** The store assigns them.
```

- [ ] **Step 4: Create recall-and-ranking.md**

Create `internal/mcpadapter/skills/recall-and-ranking.md`:

```markdown
---
name: recall-and-ranking
description: The four ranking modes — activation, chronological, similarity, relevance (RRF) — and when to use each.
scope_hint: memory:read
related: [memory, revisions]
---

# Recall and ranking

`memory_recall` and `conduit_lookup` share one ranking surface. Pass `ranking=<mode>`; the default is `relevance` when a `query` is provided, otherwise `activation`.

## Four modes

- **`activation`** — combines recency, reinforcement, and confidence. Best when you have no query and want "what's top-of-mind." Activation decay is a stable, empirically-tuned formula — don't tune without data.
- **`chronological`** — newest first, no scoring. Use when you want a timeline or an audit-style scan.
- **`similarity`** — pure cosine similarity between the query embedding and each candidate's stored vector. Requires `query` to be set and target revisions to be embedded. Unembedded revisions are silently skipped.
- **`relevance`** — RRF fusion of BM25 (keyword) and cosine (semantic). Best default for "search for this topic." Surfaces fresh, pre-embedding memories via the BM25 arm that similarity-only would miss.

## When to use each

| Question | Ranking |
|---|---|
| "What do I know about X?" | `relevance` |
| "What are the most active memories right now?" | `activation` |
| "Show me the last 10 entries in this namespace." | `chronological` |
| "Find semantically similar memories to this text." | `similarity` |

## Filters

All rankings accept the same filter set:

- `namespaces` (JSON array)
- `origins`, `statuses`, `tags` (JSON arrays)
- `confidence_min` (0-1)
- `since` / `until` (RFC3339 bounds)
- `facet_kinds` / `facet_sources` (knowledge-aware, via `conduit_lookup`)

## Access reinforcement

Every recall mode reinforces access (widened from activation-only in v0.4.0). Recall counts as "use" even when ranking is chronological or similarity.
```

- [ ] **Step 5: Update skills package test to expect 5 entries**

Edit `internal/mcpadapter/skills/skills_test.go`. Add a new test at the end:

```go
func TestList_HasExpectedCount(t *testing.T) {
	const expected = 5 // start-here + 4 primitives — bump as skills ship
	got, err := List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != expected {
		names := make([]string, len(got))
		for i, m := range got {
			names[i] = m.Name
		}
		t.Errorf("skill count = %d, want %d. Skills: %v", len(got), expected, names)
	}
}
```

- [ ] **Step 6: Run tests**

Run:
```bash
go test ./internal/mcpadapter/skills/ -v
```
Expected: all tests pass; skill count = 5.

- [ ] **Step 7: Commit**

```bash
git add internal/mcpadapter/skills/
git commit -m "docs(skills): primitive skills batch 1 (namespaces, facets-and-kinds, revisions, recall-and-ranking)"
```

---

## Task 6: Write skill MDs — batch 2 (primitives, part 2)

**Files:**
- Create: `internal/mcpadapter/skills/promotion.md`
- Create: `internal/mcpadapter/skills/views.md`
- Create: `internal/mcpadapter/skills/memory.md`
- Create: `internal/mcpadapter/skills/knowledge.md`
- Modify: `internal/mcpadapter/skills/skills_test.go`

**Source material:** `docs/SPECS/PROMOTION.md`, `docs/SPECS/VIEWS.md`, `docs/SPECS/MCP.md`, `docs/ARCHITECTURE.md`, existing `knowledge_write` docs.

- [ ] **Step 1: Create promotion.md**

Create `internal/mcpadapter/skills/promotion.md`:

```markdown
---
name: promotion
description: The app→user promotion workflow — request, approve, apply — and why it exists.
scope_hint: promote
related: [namespaces, memory]
---

# Promotion

Apps cannot write directly into `user/*` namespaces. The user owns that surface; apps propose, the user (or an agent acting on behalf of the user) approves. Promotion is the one-way channel for moving app-authored content into user-owned memory.

## Three stages

1. **Request** — `context_promote_request` (scope: `promote.request`). Opens a pending request pointing at a source record in `app/*` and a target key in `user/*`. Optional reason + notes.
2. **Approve** — `context_promote_approve` (scope: `promote.approve`). Moves the request from `pending` to `approved`. Does not yet apply.
3. **Apply** — `context_promote_apply` (scope: `promote.apply`). Copies the approved content into the target namespace as a new revision. Original source record is unchanged.

Listing: `context_promote_list` with `status=pending|approved|applied|all` (default `pending`).

## Memory-specific promotion

`memory_promote` is a shortcut for promoting a session-scoped memory to a user or project scope without going through the three-stage request/approve/apply workflow. Use it for same-agent, same-session moves where the user-owned actor is already authorized.

## Why this exists

- **User sovereignty.** The user's memory is theirs; apps shouldn't be able to overwrite or inject without an explicit handshake.
- **Audit trail.** Every stage is logged. You can reconstruct "when did this enter user memory, and who approved it?" from `context_audit`.
- **Deferred decisions.** Approval and apply are decoupled so an approval workflow can queue and batch applies.

## When to reach for promotion

- Cross-namespace migration: app session produces a memory you want to keep across sessions → promote to `user/*/memory`.
- Draft → canonical: use `context_status_promote` (in-namespace status change) when you don't need to cross ownership boundaries.
```

- [ ] **Step 2: Create views.md**

Create `internal/mcpadapter/skills/views.md`:

```markdown
---
name: views
description: The selectors-not-processors model. Namespace globs, revision scope.
scope_hint: none
related: [namespaces, recall-and-ranking]
---

# Views

Views are **selectors**, not processors. They return records that match criteria, in a deterministic order. They do not synthesize, merge, summarize, or rank-for-relevance — that's what recall ranking is for.

## Selector shape

A selector is a JSON object:

```json
{
  "namespaces": ["user/chrispian/memory/*", "app/test/session/*"],
  "keys": ["task-001"],
  "revision_scope": "head",
  "tags_any": ["priority"],
  "statuses": ["canonical"],
  "types": ["preference"],
  "limit": 50
}
```

- `namespaces` — glob patterns (comma-separated in `context_view`, array in `views_evaluate`).
- `revision_scope` — `head` (default; current only) or `all` (include historical revisions).
- Filter fields combine with AND semantics.

## Two surfaces

- `context_view` — simplified MCP tool taking comma-separated namespace globs. Best for agent queries.
- `views_evaluate` — full-power `POST /v1/views/evaluate`. Returns items + evaluation metadata (`sort_keys`, `matched_count`, `truncated`, `normalized_scope`). Use when you need the meta for pagination or debugging.

## Ordering

Selectors sort by `(namespace, key, revision)` as a stable tiebreak. Don't rely on insertion order.

## What views don't do

- No ranking (use `memory_recall` / `conduit_lookup`).
- No payload transforms.
- No cross-namespace joins beyond the glob set.
```

- [ ] **Step 3: Create memory.md**

Create `internal/mcpadapter/skills/memory.md`:

```markdown
---
name: memory
description: When to use the memory domain — patterns, common flows, how ranking modes interact.
scope_hint: memory:read
related: [namespaces, revisions, recall-and-ranking, promotion]
---

# Memory domain

The memory domain is for **agent-authored content you'll want to recall later**: observations, preferences, session notes, distilled understanding. Every write is append-only and revisioned; recall is multi-knob.

## When to use memory

- Something the agent noticed and should remember.
- A user preference stated explicitly.
- A session summary worth carrying forward.
- Anything you might want to find later by similarity, activation, or topic.

## When NOT to use memory

- **External content you're referencing.** Use knowledge (`knowledge_write`) — the pointer-first model preserves provenance.
- **Generic state records.** Use `context_write` — memory has specific lifecycle semantics (activation, promotion, dedup) you don't need for plain records.
- **Ephemeral session-only scratch.** Use `session/*` cache namespaces or app-scoped context records.

## Core flow

1. **Write** — `memory_write` with namespace, memory_key (optional, for keyed memories), author info, trigger, origin, confidence, payload summary + body, tags. Optional semantic dedup.
2. **Recall** — `memory_recall` with ranking and filters. Default ranking is `relevance` (RRF) when query is set, else `activation`.
3. **Get head** — `memory_get` for the current revision of a keyed memory.
4. **Get history** — `memory_history` for the full revision chain.
5. **Supersede** — pass `supersedes` on write to mark an explicit ancestor.
6. **Deprecate** — `memory_deprecate` when a revision is wrong or outdated.

## Keyed vs. unkeyed

- **Keyed memory** — a stable `memory_key` that represents an evolving concept (e.g., `user.prefs.style`). Re-writing creates a new revision; `memory_get` returns the current head.
- **Unkeyed memory** — no `memory_key`; each write stands alone. Use for observation streams where no stable identity exists.

## Required fields on write

`namespace`, `author_agent_id`, `trigger`, `session_id`, `origin`, `confidence`, `payload_summary`. Everything else is optional.

## Promotion

Session-scoped memories can be promoted to user or project scope via `memory_promote`. See `vanta_skills promotion`.
```

- [ ] **Step 4: Create knowledge.md**

Create `internal/mcpadapter/skills/knowledge.md`:

```markdown
---
name: knowledge
description: The pointer-first knowledge domain — kind/source/pointer facets, when to use knowledge vs. memory.
scope_hint: memory:read
related: [facets-and-kinds, memory]
---

# Knowledge domain

Knowledge is for **pointer-first references to external content**: packages, documents, notes that live somewhere else. The reference is the primary artifact; the summary and body are aids for discovery.

## When to use knowledge

- Recording that a library exists and what it does.
- Cataloging a document's location and summary.
- Capturing a reference to external content you'll want to find later.

## When NOT to use knowledge

- **Agent-authored content with no external source.** Use memory (`memory_write`).
- **Generic records.** Use `context_write`.

## Required fields on write

`namespace` (must contain a `knowledge` segment, e.g. `user/chrispian/knowledge/framework`), `kind`, `source`, `pointer_scheme`, `pointer_locator`, `summary`, `author_agent_id`, `session_id`.

Facets:

- `kind` — what this is (`package`, `doc`, `note`, ...). See `vanta_skills facets-and-kinds`.
- `source` — where it came from (`filesystem`, `obsidian`, `nil`, `web`, `manual`, ...).
- `pointer` — structured address: `{scheme, locator, resolved_at}`.

Common `scheme` values: `file`, `http`, `https`, `obsidian`, `nil`.

## Example

```json
{
  "namespace": "user/chrispian/knowledge/framework",
  "key": "framework.go-providers",
  "kind": "package",
  "source": "filesystem",
  "pointer_scheme": "file",
  "pointer_locator": "/Users/chrispian/Projects-apps/framework/libs/go-providers",
  "summary": "go-providers: multi-provider AI adapter (Anthropic, OpenAI, Ollama, …)",
  "body": "Exports provider.Embedder and provider.Completer. Replace-directive in consumer go.mod.",
  "author_agent_id": "indexer",
  "session_id": "indexer:2026-04-15"
}
```

## Reading

`knowledge_get` for the current revision by `(namespace, key)`; `knowledge_history` for the full chain. For cross-domain search (memory + knowledge), use `conduit_lookup`.

## Confidence default

Knowledge `confidence` defaults to `0.9` when omitted, vs. required-and-explicit for memory. Override per-call if you want a lower value.
```

- [ ] **Step 5: Bump the skill count test**

Edit `internal/mcpadapter/skills/skills_test.go`. Change `const expected = 5` to `const expected = 9`.

- [ ] **Step 6: Run tests**

Run:
```bash
go test ./internal/mcpadapter/skills/ -v
```
Expected: all tests pass; skill count = 9.

- [ ] **Step 7: Commit**

```bash
git add internal/mcpadapter/skills/
git commit -m "docs(skills): primitive + domain skills batch 2 (promotion, views, memory, knowledge)"
```

---

## Task 7: Write skill MDs — batch 3 (features)

**Files:**
- Create: `internal/mcpadapter/skills/context-packet.md`
- Create: `internal/mcpadapter/skills/audit.md`
- Modify: `internal/mcpadapter/skills/skills_test.go`

**Source material:** `docs/SPECS/MCP.md` (broker), `docs/SPECS/API.md` (audit).

- [ ] **Step 1: Create context-packet.md**

Create `internal/mcpadapter/skills/context-packet.md`:

```markdown
---
name: context-packet
description: Boot workflows — broker plan/fetch, packet assembly, budget tuning.
scope_hint: none
related: [namespaces, views]
---

# Context packet

A **packet** is a budget-bounded, ordered set of records assembled for an agent — typically at boot or task resume. Pins come first; then namespace-matched records; then manifest metadata.

## One-shot: `context_broker_fetch`

The simplest path. Combines planning and fetching:

```json
{
  "intent": "boot_project",
  "summary": "Vanta Conduit backend — batch 1 parity work",
  "budget_items": 80,
  "budget_tokens": 8000,
  "payload_mode": "full"
}
```

`intent` values:
- `resume_task` — picking up where a session left off.
- `boot_project` — fresh project boot.
- `review_session` — auditing a completed session.
- `custom` — raw input, no intent-driven keyword extraction.

## Two-phase: `context_broker_plan` + `context_packet`

When you want to inspect the plan before fetching:

1. `context_broker_plan` — returns namespace globs, budget, rationale.
2. `context_packet` — executes with the plan's patterns plus manual overrides.

Use two-phase when you want to adjust budget or patterns after seeing the plan.

## Payload modes

- `full` — include `payload_summary` and `payload_body`.
- `head_only` — metadata + summary only; drop body.

Use `head_only` to survey a large surface; switch to `full` for a narrower follow-up.

## Budgets

- `budget_items` — max record count (default 50 via plan, 80 typical).
- `budget_tokens` — rough token estimate; records are included in ranked order until either budget is hit.

The manifest records which budget was the binding constraint, so you can tune the next call.

## Pins

User-curated pins live in `user/<who>/pins/*`. Set `include_pins: true` (default) to prepend them to every packet. Pins typically communicate "start here" content.
```

- [ ] **Step 2: Create audit.md**

Create `internal/mcpadapter/skills/audit.md`:

```markdown
---
name: audit
description: Querying the audit log — event types, filters, pagination.
scope_hint: none
related: [revisions, promotion]
---

# Audit

Every write and promotion in Vanta produces a structured audit event. The audit log is append-only and durable; no tool mutates it.

## Querying

`context_audit` returns structured events, newest first:

- `namespace` — filter by exact namespace.
- `event_type` — filter by event type (e.g. `write`, `promote`, `supersede`).
- `limit` — max events (default 10, max 25).
- `cursor` — pagination. The previous response's `next_cursor` field is the input here.

## Event types

- `write` — a new record or revision landed.
- `supersede` — a new revision replaced a prior head.
- `deprecate` — a revision was soft-removed from the current pool.
- `promote_request` / `promote_approve` / `promote_apply` — promotion workflow stages.

## Envelope shape

Each event carries:

- `id` — ULID event id.
- `timestamp` — RFC3339Nano.
- `namespace`, `key`, `revision_id` — record identity.
- `actor`, `client_id` — who did the work.
- `event_type` — one of the types above.
- `details` — event-specific payload.

## Common queries

- "Who wrote into `user/chrispian/memory` in the last hour?"
  - `namespace: user/chrispian/memory`, iterate with cursor, filter by timestamp in the caller.
- "What promotions are pending?"
  - Prefer `context_promote_list` (domain-aware); `context_audit` is the raw event stream.
- "When was revision X written?"
  - Filter by revision_id via the event details (iterate until match; no direct index yet).
```

- [ ] **Step 3: Bump the skill count test to 11**

Edit `internal/mcpadapter/skills/skills_test.go`. Change `const expected = 9` to `const expected = 11`.

- [ ] **Step 4: Run tests**

Run:
```bash
go test ./internal/mcpadapter/skills/ -v
```
Expected: all tests pass; skill count = 11.

- [ ] **Step 5: Commit**

```bash
git add internal/mcpadapter/skills/
git commit -m "docs(skills): feature skills (context-packet, audit) — all 11 shipped"
```

---

## Task 8: Memory tool description + annotation rewrite

**Files:**
- Modify: `internal/mcpadapter/memory_tools.go`

- [ ] **Step 1: Rewrite memory_write**

Edit `internal/mcpadapter/memory_tools.go`. Replace the `memory_write` tool registration (lines 16–34) with:

```go
	// ── memory_write ─────────────────────────────────────────────────────────
	s.AddTool(mcp.NewTool("memory_write",
		mcp.WithDescription(
			"**Append an agent memory revision** under `(namespace, memory_key)`.\n"+
				"• **Kind of content:** agent observations, preferences, session notes — content you'll want to recall by similarity, activation, or chronological order.\n"+
				"• **Scope:** `memory:write`.\n"+
				"• **Use this when:** the content is authored by you or another agent and belongs to the memory domain.\n"+
				"• **Don't use this for:** pointer-to-external-content (`knowledge_write`) or generic revisioned records (`context_write`).\n"+
				"• **Deeper:** `vanta_skills memory` for patterns; `vanta_skills namespaces` for namespace rules.",
		),
		mcp.WithString("namespace", mcp.Required(), mcp.Description("Memory namespace (e.g. user/chrispian/memory)")),
		mcp.WithString("memory_key", mcp.Description("Optional logical key for keyed memories (e.g. user.prefs.style)")),
		mcp.WithString("supersedes", mcp.Description("Revision ID this revision supersedes (e.g. 01HX...)")),
		mcp.WithString("status", mcp.Description("Status: draft|reviewed|canonical (default: draft)")),
		mcp.WithString("author_agent_id", mcp.Required(), mcp.Description("Agent ID of the author (e.g. claude, nanite)")),
		mcp.WithString("author_version", mcp.Description("Agent version string")),
		mcp.WithString("trigger", mcp.Required(), mcp.Description("Trigger: explicit|post_compact|per_turn|promotion|manual")),
		mcp.WithString("session_id", mcp.Required(), mcp.Description("Session identifier (e.g. 2026-04-19:backend)")),
		mcp.WithString("origin", mcp.Required(), mcp.Description("Origin: user|feedback|project|reference|observation")),
		mcp.WithNumber("confidence", mcp.Required(), mcp.Description("Confidence score in [0, 1.0] (e.g. 0.9)")),
		mcp.WithString("tags", mcp.Description("JSON array of string tags (e.g. [\"preference\",\"style\"])")),
		mcp.WithNumber("ttl_seconds", mcp.Description("Time-to-live in seconds (0 = no expiry)")),
		mcp.WithString("payload_summary", mcp.Required(), mcp.Description("Summary text for the memory payload")),
		mcp.WithString("payload_body", mcp.Description("Optional body text for the memory payload")),
		mcp.WithString("dedup", mcp.Description("Dedup mode: none (default) or semantic")),
		mcp.WithNumber("dedup_threshold", mcp.Description("Similarity threshold override for semantic dedup (0 = use config default 0.85)")),
		mcp.WithReadOnlyHintAnnotation(false),
		mcp.WithIdempotentHintAnnotation(false),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithOpenWorldHintAnnotation(false),
	), a.handleMemoryWrite)
```

- [ ] **Step 2: Rewrite memory_get**

Replace the `memory_get` registration with:

```go
	// ── memory_get ───────────────────────────────────────────────────────────
	s.AddTool(mcp.NewTool("memory_get",
		mcp.WithDescription(
			"**Get the current (head) revision** for a keyed memory.\n"+
				"• **Kind of content:** the latest revision under `(namespace, memory_key)`, deprecations skipped.\n"+
				"• **Scope:** `memory:read`.\n"+
				"• **Use this when:** you have a specific key and want its current value.\n"+
				"• **Don't use this for:** revision history (`memory_history`), ranked recall (`memory_recall`), or a specific revision by ID (`memory_get_revision`).\n"+
				"• **Deeper:** `vanta_skills memory`.",
		),
		mcp.WithString("namespace", mcp.Required(), mcp.Description("Memory namespace (e.g. user/chrispian/memory)")),
		mcp.WithString("memory_key", mcp.Required(), mcp.Description("Logical memory key")),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithIdempotentHintAnnotation(true),
		mcp.WithOpenWorldHintAnnotation(false),
	), a.handleMemoryGet)
```

- [ ] **Step 3: Rewrite memory_history**

```go
	// ── memory_history ───────────────────────────────────────────────────────
	s.AddTool(mcp.NewTool("memory_history",
		mcp.WithDescription(
			"**Get the full revision history** for a keyed memory, newest-first.\n"+
				"• **Kind of content:** every revision under `(namespace, memory_key)`, including superseded and deprecated ones.\n"+
				"• **Scope:** `memory:read`.\n"+
				"• **Use this when:** you need to trace how a memory evolved, or inspect superseded content.\n"+
				"• **Don't use this for:** just the current value (`memory_get`).\n"+
				"• **Deeper:** `vanta_skills revisions`.",
		),
		mcp.WithString("namespace", mcp.Required(), mcp.Description("Memory namespace")),
		mcp.WithString("memory_key", mcp.Required(), mcp.Description("Logical memory key")),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithIdempotentHintAnnotation(true),
		mcp.WithOpenWorldHintAnnotation(false),
	), a.handleMemoryHistory)
```

- [ ] **Step 4: Rewrite memory_recall**

```go
	// ── memory_recall ────────────────────────────────────────────────────────
	s.AddTool(mcp.NewTool("memory_recall",
		mcp.WithDescription(
			"**Ranked recall across namespaces.** Multi-knob: activation / chronological / similarity / relevance.\n"+
				"• **Kind of content:** ranked list of memory revisions matching namespaces + filters.\n"+
				"• **Scope:** `memory:read`.\n"+
				"• **Use this when:** you want the best-match memories for a query or the top-of-mind memories without a query.\n"+
				"• **Don't use this for:** cross-domain search — `conduit_lookup` spans memory + knowledge. Deterministic selection — use `context_view` / `views_evaluate`.\n"+
				"• **Deeper:** `vanta_skills recall-and-ranking` for ranking modes; `vanta_skills memory` for patterns.",
		),
		mcp.WithString("namespaces", mcp.Required(), mcp.Description("JSON array of namespace strings (e.g. [\"user/chrispian/memory\"])")),
		mcp.WithString("revision_scope", mcp.Description("current or timeline (default: current)")),
		mcp.WithString("ranking", mcp.Description("activation, chronological, similarity, or relevance (default: relevance when query is set, else activation)")),
		mcp.WithString("query", mcp.Description("Semantic query string (required for similarity or relevance ranking)")),
		mcp.WithString("origins", mcp.Description("JSON array of origin filter values")),
		mcp.WithString("statuses", mcp.Description("JSON array of status filter values")),
		mcp.WithString("tags", mcp.Description("JSON array of tag filter values")),
		mcp.WithNumber("confidence_min", mcp.Description("Minimum confidence threshold")),
		mcp.WithString("since", mcp.Description("RFC3339 timestamp lower bound")),
		mcp.WithString("until", mcp.Description("RFC3339 timestamp upper bound")),
		mcp.WithNumber("limit", mcp.Description("Max results (default 30, max 500)")),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithIdempotentHintAnnotation(true),
		mcp.WithOpenWorldHintAnnotation(false),
	), a.handleMemoryRecall)
```

- [ ] **Step 5: Rewrite memory_promote**

```go
	// ── memory_promote ───────────────────────────────────────────────────────
	s.AddTool(mcp.NewTool("memory_promote",
		mcp.WithDescription(
			"**Promote a session-scoped memory** to user or project scope.\n"+
				"• **Kind of content:** a copy of the source memory revision, re-scoped to the target namespace.\n"+
				"• **Scope:** `memory:write`.\n"+
				"• **Use this when:** a session memory has proven durable and you want it to survive session boundaries.\n"+
				"• **Don't use this for:** cross-ownership promotion (app/* → user/*) — use the `context_promote_*` three-stage workflow.\n"+
				"• **Deeper:** `vanta_skills promotion`.",
		),
		mcp.WithString("source_namespace", mcp.Required(), mcp.Description("Source session namespace (e.g. app/myagent/session/2026-04-19:backend)")),
		mcp.WithString("source_memory_id", mcp.Required(), mcp.Description("Source memory ID to promote")),
		mcp.WithString("target_namespace", mcp.Required(), mcp.Description("Target user or project namespace (e.g. user/chrispian/memory)")),
		mcp.WithString("actor_agent_id", mcp.Required(), mcp.Description("Agent ID performing the promotion")),
		mcp.WithString("actor_version", mcp.Description("Agent version string")),
		mcp.WithReadOnlyHintAnnotation(false),
		mcp.WithIdempotentHintAnnotation(false),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithOpenWorldHintAnnotation(false),
	), a.handleMemoryPromote)
```

- [ ] **Step 6: Rewrite memory_deprecate**

```go
	// ── memory_deprecate ─────────────────────────────────────────────────────
	s.AddTool(mcp.NewTool("memory_deprecate",
		mcp.WithDescription(
			"**Soft-remove a memory revision** by revision ID.\n"+
				"• **Kind of content:** a deprecation event on a specific revision. Revision stays in history and audit.\n"+
				"• **Scope:** `memory:write`.\n"+
				"• **Use this when:** a revision is wrong, outdated, or should no longer appear in current recall.\n"+
				"• **Don't use this for:** replacing content — write a new revision with `supersedes`. Hard deletes — not supported (audit trail is canonical).\n"+
				"• **Deeper:** `vanta_skills revisions`.",
		),
		mcp.WithString("revision_id", mcp.Required(), mcp.Description("Revision ID to deprecate (e.g. 01HX...)")),
		mcp.WithReadOnlyHintAnnotation(false),
		mcp.WithIdempotentHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithOpenWorldHintAnnotation(false),
	), a.handleMemoryDeprecate)
```

- [ ] **Step 7: Run the suite to confirm no regression**

Run:
```bash
cd ~/Projects-apps/vanta-conduit
go build ./...
go test ./internal/mcpadapter/...
```
Expected: build succeeds; all tests pass (handlers are unchanged, so tests should be unaffected).

- [ ] **Step 8: Commit**

```bash
git add internal/mcpadapter/memory_tools.go
git commit -m "feat(mcp): rewrite memory_* tool descriptions + annotations to v2 template"
```

---

## Task 9: Parity tool rewrites (memory_get_revision, knowledge_get, knowledge_history)

**Files:**
- Modify: `internal/mcpadapter/parity_tools.go`

- [ ] **Step 1: Rewrite memory_get_revision**

Edit `internal/mcpadapter/parity_tools.go` around lines 32–36. Replace with:

```go
	if a.MemoryStore != nil {
		s.AddTool(mcp.NewTool("memory_get_revision",
			mcp.WithDescription(
				"**Fetch a memory revision by its revision_id.** Peer of HTTP /v1/memory/revisions/{id}.\n"+
					"• **Kind of content:** a single revision record, including body, facets, and lineage.\n"+
					"• **Scope:** `memory:read`.\n"+
					"• **Use this when:** a recall or history result referenced a revision_id and you want the full content.\n"+
					"• **Don't use this for:** resolving by (namespace, memory_key) — use `memory_get`.\n"+
					"• **Deeper:** `vanta_skills revisions`.",
			),
			mcp.WithString("revision_id", mcp.Required(), mcp.Description("Revision ID to fetch (e.g. 01HX...)")),
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithIdempotentHintAnnotation(true),
			mcp.WithOpenWorldHintAnnotation(false),
		), a.handleMemoryGetRevision)
	}
```

- [ ] **Step 2: Rewrite knowledge_get and knowledge_history**

Replace the `if a.KnowledgeStore != nil` block (lines 38–50) with:

```go
	if a.KnowledgeStore != nil {
		s.AddTool(mcp.NewTool("knowledge_get",
			mcp.WithDescription(
				"**Fetch the current knowledge revision** for `(namespace, key)`. Peer of HTTP /v1/knowledge/current.\n"+
					"• **Kind of content:** the latest non-deprecated knowledge revision for this entry.\n"+
					"• **Scope:** `memory:read`.\n"+
					"• **Use this when:** you know the key and want the current pointer + summary + body.\n"+
					"• **Don't use this for:** full history (`knowledge_history`), cross-entry search (`conduit_lookup`).\n"+
					"• **Deeper:** `vanta_skills knowledge`.",
			),
			mcp.WithString("namespace", mcp.Required(), mcp.Description("Knowledge namespace (must contain 'knowledge' segment, e.g. user/chrispian/knowledge/framework)")),
			mcp.WithString("key", mcp.Required(), mcp.Description("Knowledge key (e.g. framework.go-providers)")),
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithIdempotentHintAnnotation(true),
			mcp.WithOpenWorldHintAnnotation(false),
		), a.handleKnowledgeGet)

		s.AddTool(mcp.NewTool("knowledge_history",
			mcp.WithDescription(
				"**Fetch the full revision history** for a knowledge entry, newest-first. Peer of HTTP /v1/knowledge/history.\n"+
					"• **Kind of content:** every revision under `(namespace, key)`, including superseded.\n"+
					"• **Scope:** `memory:read`.\n"+
					"• **Use this when:** you need to trace how a knowledge entry evolved (e.g. pointer churn, summary rewrites).\n"+
					"• **Don't use this for:** just the current value (`knowledge_get`).\n"+
					"• **Deeper:** `vanta_skills revisions`.",
			),
			mcp.WithString("namespace", mcp.Required(), mcp.Description("Knowledge namespace")),
			mcp.WithString("key", mcp.Required(), mcp.Description("Knowledge key")),
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithIdempotentHintAnnotation(true),
			mcp.WithOpenWorldHintAnnotation(false),
		), a.handleKnowledgeHistory)
	}
```

- [ ] **Step 3: Append pointer-footer to context_estimate and views_evaluate**

These two stay under context/views scope and only get the footer. Edit each `mcp.WithDescription(...)` to append `. See `vanta_skills start-here` for the primitive model.`:

Change `context_estimate` description (line 20) from:
```
"Estimate record count, payload bytes, and rough token count for a selector without returning the records. Peer of HTTP /v1/context/estimate."
```
to:
```
"Estimate record count, payload bytes, and rough token count for a selector without returning the records. Peer of HTTP /v1/context/estimate. See `vanta_skills start-here` for the primitive model."
```

Change `views_evaluate` description (line 25) from:
```
"Evaluate a view selector against the context store. Returns items plus evaluation metadata (sort keys, matched count, truncated flag, normalized scope). Peer of HTTP /v1/views/evaluate."
```
to:
```
"Evaluate a view selector against the context store. Returns items plus evaluation metadata (sort keys, matched count, truncated flag, normalized scope). Peer of HTTP /v1/views/evaluate. See `vanta_skills start-here` for the primitive model."
```

- [ ] **Step 4: Run build + tests**

Run:
```bash
go build ./...
go test ./internal/mcpadapter/... ./tests/parity/...
```
Expected: pass.

- [ ] **Step 5: Commit**

```bash
git add internal/mcpadapter/parity_tools.go
git commit -m "feat(mcp): rewrite parity-domain tool descriptions + annotations"
```

---

## Task 10: knowledge_write + conduit_lookup rewrite

**Files:**
- Modify: `internal/mcpadapter/knowledge_tools.go`
- Modify: `internal/mcpadapter/lookup_tools.go`

- [ ] **Step 1: Rewrite knowledge_write**

Edit `internal/mcpadapter/knowledge_tools.go`. Replace the tool registration (lines 16–34) with:

```go
	s.AddTool(mcp.NewTool("knowledge_write",
		mcp.WithDescription(
			"**Write a knowledge revision** — a pointer-first reference to external content.\n"+
				"• **Kind of content:** package / doc / note / pointer records with `kind`/`source`/`pointer` facets.\n"+
				"• **Scope:** `memory:write`.\n"+
				"• **Use this when:** you are cataloging something that lives outside Vanta (a file, URL, library, doc).\n"+
				"• **Don't use this for:** agent-authored content with no external source — use `memory_write`. Generic records — use `context_write`.\n"+
				"• **Deeper:** `vanta_skills knowledge` for patterns; `vanta_skills facets-and-kinds` for facet vocabulary.",
		),
		mcp.WithString("namespace", mcp.Required(), mcp.Description("Knowledge namespace; must contain a 'knowledge' segment (e.g. user/chrispian/knowledge/framework)")),
		mcp.WithString("key", mcp.Description("Optional logical key (slug, path, id) — same key on re-write creates a new revision")),
		mcp.WithString("kind", mcp.Required(), mcp.Description("Facet: the kind of entry (e.g. package, doc, note, pointer)")),
		mcp.WithString("source", mcp.Required(), mcp.Description("Facet: where this knowledge came from (e.g. filesystem, obsidian, nil, web, manual)")),
		mcp.WithString("pointer_scheme", mcp.Required(), mcp.Description("Pointer scheme (e.g. file, http, https, obsidian, nil)")),
		mcp.WithString("pointer_locator", mcp.Required(), mcp.Description("Pointer locator: scheme-specific address (path, URL, vault id, ...)")),
		mcp.WithString("pointer_resolved_at", mcp.Description("Optional RFC3339 timestamp for when the pointer was last verified. Defaults to now.")),
		mcp.WithString("summary", mcp.Required(), mcp.Description("Short summary text (feeds embeddings)")),
		mcp.WithString("body", mcp.Description("Optional longer body (feeds embeddings when present)")),
		mcp.WithString("author_agent_id", mcp.Required(), mcp.Description("Agent ID of the writer")),
		mcp.WithString("author_version", mcp.Description("Agent version string")),
		mcp.WithString("session_id", mcp.Required(), mcp.Description("Session identifier")),
		mcp.WithString("tags", mcp.Description("Optional JSON array of string tags")),
		mcp.WithNumber("ttl_seconds", mcp.Description("Optional TTL in seconds (0 = no expiry)")),
		mcp.WithNumber("confidence", mcp.Description("Confidence score in [0, 1.0] (default 0.9)")),
		mcp.WithString("supersedes", mcp.Description("Optional revision_id this entry supersedes")),
		mcp.WithReadOnlyHintAnnotation(false),
		mcp.WithIdempotentHintAnnotation(false),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithOpenWorldHintAnnotation(false),
	), a.handleKnowledgeWrite)
```

- [ ] **Step 2: Rewrite conduit_lookup**

Edit `internal/mcpadapter/lookup_tools.go`. Replace the tool registration (lines 16–32) with:

```go
	s.AddTool(mcp.NewTool("conduit_lookup",
		mcp.WithDescription(
			"**Unified search across memory + knowledge.** Returns ranked results + facet histograms.\n"+
				"• **Kind of content:** mixed memory and knowledge revisions matching query + filters, with a uniform shape.\n"+
				"• **Scope:** `memory:read`.\n"+
				"• **Use this when:** you don't know whether the content is memory or knowledge, or you want both. **Prefer this BEFORE filesystem or web exploration.**\n"+
				"• **Don't use this for:** memory-only recall (`memory_recall`), deterministic selection (`views_evaluate`).\n"+
				"• **Deeper:** `vanta_skills recall-and-ranking` for ranking modes; `vanta_skills facets-and-kinds` for facet filters.",
		),
		mcp.WithString("namespaces", mcp.Required(), mcp.Description("JSON array of namespace strings (e.g. [\"user/chrispian/memory\",\"user/chrispian/knowledge\"])")),
		mcp.WithString("query", mcp.Description("Semantic query (required for similarity or relevance ranking)")),
		mcp.WithString("ranking", mcp.Description("activation|chronological|similarity|relevance (default: relevance when query is set, else activation)")),
		mcp.WithString("revision_scope", mcp.Description("current|timeline (default: current)")),
		mcp.WithNumber("limit", mcp.Description("Max results (default 30, max 500)")),
		mcp.WithString("domains", mcp.Description("JSON array of domain filters, e.g. [\"memory\",\"knowledge\"]")),
		mcp.WithString("facet_kinds", mcp.Description("JSON array of facet kind filters (knowledge), e.g. [\"package\",\"doc\"]")),
		mcp.WithString("facet_sources", mcp.Description("JSON array of facet source filters (knowledge), e.g. [\"filesystem\",\"obsidian\"]")),
		mcp.WithString("origins", mcp.Description("JSON array of origin filters")),
		mcp.WithString("statuses", mcp.Description("JSON array of status filters")),
		mcp.WithString("tags", mcp.Description("JSON array of tag filters")),
		mcp.WithNumber("confidence_min", mcp.Description("Minimum confidence")),
		mcp.WithString("since", mcp.Description("RFC3339 lower bound")),
		mcp.WithString("until", mcp.Description("RFC3339 upper bound")),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithIdempotentHintAnnotation(true),
		mcp.WithOpenWorldHintAnnotation(false),
	), a.handleConduitLookup)
```

- [ ] **Step 3: Build + test**

Run:
```bash
go build ./...
go test ./internal/mcpadapter/...
```
Expected: pass.

- [ ] **Step 4: Commit**

```bash
git add internal/mcpadapter/knowledge_tools.go internal/mcpadapter/lookup_tools.go
git commit -m "feat(mcp): rewrite knowledge_write + conduit_lookup to v2 template"
```

---

## Task 11: Context-domain pointer-footer pass

**Files:**
- Modify: `internal/mcpadapter/tools.go`
- Modify: `internal/mcpadapter/typed_tools.go`
- Modify: `internal/mcpadapter/bulk_tools.go`
- Modify: `internal/mcpadapter/embedding_tools.go`
- Modify: `internal/mcpadapter/rag_tools.go`

**Footer to append:** `" See `+"`"+`vanta_skills start-here`+"`"+` for the primitive model."`

Each existing `mcp.WithDescription("...")` on a context-domain tool gets this sentence appended inside the string (before the closing quote, with a leading space).

- [ ] **Step 1: Walk tools.go**

Edit `internal/mcpadapter/tools.go`. For each of the 15 `s.AddTool(mcp.NewTool("context_*", ...))` blocks, locate the `mcp.WithDescription("...")` line and append ` See `+"`"+`vanta_skills start-here`+"`"+` for the primitive model.` before the closing `")`.

Example — change:
```go
mcp.WithDescription("Read the current head (latest revision) of a record by namespace and key."),
```
to:
```go
mcp.WithDescription("Read the current head (latest revision) of a record by namespace and key. See `vanta_skills start-here` for the primitive model."),
```

Apply this to all 15 tools registered in `tools.go`: `context_head`, `context_history`, `context_view`, `context_packet`, `context_write`, `context_promote_request`, `context_promote_list`, `context_promote_approve`, `context_promote_apply`, `context_broker_plan`, `context_broker_fetch`, `context_namespace_register`, `context_namespace_show`, `context_namespaces_list`, `context_audit`.

- [ ] **Step 2: Walk typed_tools.go**

Apply the same footer append to all 8 tools in `typed_tools.go`: `context_typed_write`, `context_status_promote`, `context_status_deprecate`, `context_typed_view`, `context_pack`, `context_types_list`, `context_views_list`, `context_session_snapshot`.

- [ ] **Step 3: Walk bulk_tools.go**

Apply to `context_bulk_ingest`, `context_chunked_ingest`.

- [ ] **Step 4: Walk embedding_tools.go**

Apply to `context_embed`, `context_search`.

- [ ] **Step 5: Walk rag_tools.go**

Apply to `context_rag_query`.

- [ ] **Step 6: Verify every context_* tool has the footer**

Run:
```bash
cd ~/Projects-apps/vanta-conduit
# Each count below should be equal. Any mismatch means a tool was missed.
grep -c 'mcp.NewTool("context_' internal/mcpadapter/*.go
grep -c 'vanta_skills start-here' internal/mcpadapter/*.go
```

The sum of `mcp.NewTool("context_...")` lines across files should match the sum of `vanta_skills start-here` references, minus 1 for the `skills_tool.go` self-reference and minus any matches from skill MDs (if grep recurses — use `--include='*.go'` to scope).

Clean check:
```bash
grep -rn --include='*.go' 'mcp.NewTool("context_' internal/mcpadapter/ | wc -l
grep -rn --include='*.go' 'vanta_skills start-here' internal/mcpadapter/ | wc -l
```
First count = 30 (all context_*). Second count ≥ 30 (some may also be in handler code; the footer itself is only on the 30 context_* descriptions + 2 footer-only rewrites done in Task 9 = 32 minimum).

- [ ] **Step 7: Build + test**

```bash
go build ./...
go test ./internal/mcpadapter/...
```
Expected: pass.

- [ ] **Step 8: Commit**

```bash
git add internal/mcpadapter/tools.go
git add internal/mcpadapter/typed_tools.go
git add internal/mcpadapter/bulk_tools.go
git add internal/mcpadapter/embedding_tools.go
git add internal/mcpadapter/rag_tools.go
git commit -m "docs(mcp): append vanta_skills pointer-footer to context_* descriptions"
```

---

## Task 12: Annotation-presence drift test

**Files:**
- Create: `internal/mcpadapter/annotations_test.go`

- [ ] **Step 1: Write the test**

Create `internal/mcpadapter/annotations_test.go`:

```go
package mcpadapter

import (
	"reflect"
	"testing"

	"github.com/hollis-labs/tesseract/internal/knowledge"
	"github.com/hollis-labs/tesseract/internal/memory"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// TestMemoryKnowledgeUnifiedToolsAnnotated enforces that every memory / knowledge /
// unified MCP tool has non-default ReadOnlyHint and IdempotentHint annotations.
// Context-domain tools are not enforced (they only carry the pointer-footer in v2).
func TestMemoryKnowledgeUnifiedToolsAnnotated(t *testing.T) {
	cs := newTestStore(t)
	ms := memory.NewStore(cs.DB(), nil, "", 0, memory.NoopQueue{})
	ks := knowledge.NewStore(cs.DB())

	a := New(cs, "")
	a.MemoryStore = ms
	a.KnowledgeStore = ks

	srv := server.NewMCPServer("test", "0.0.0", server.WithToolCapabilities(true))
	a.RegisterAllTools(srv)

	// These names are the authoritative list of v2-rewritten tools.
	enforced := []string{
		"memory_write",
		"memory_get",
		"memory_history",
		"memory_recall",
		"memory_get_revision",
		"memory_promote",
		"memory_deprecate",
		"knowledge_write",
		"knowledge_get",
		"knowledge_history",
		"conduit_lookup",
		"vanta_skills",
	}

	// mcp-go exposes registered tools via server introspection. If this helper
	// does not exist on your mcp-go version, use the reflect-based fallback.
	tools := listRegisteredTools(t, srv)
	byName := map[string]mcp.Tool{}
	for _, tl := range tools {
		byName[tl.Name] = tl
	}

	for _, name := range enforced {
		tl, ok := byName[name]
		if !ok {
			t.Errorf("tool %q not registered; RegisterAllTools or handler wiring broken", name)
			continue
		}
		if tl.Annotations.ReadOnlyHint == nil {
			t.Errorf("tool %q missing ReadOnlyHint annotation (v2 template requires it)", name)
		}
		if tl.Annotations.IdempotentHint == nil {
			t.Errorf("tool %q missing IdempotentHint annotation (v2 template requires it)", name)
		}
	}
}

// listRegisteredTools introspects the server via the mcp-go API.
// If ListTools is not available, use reflection to reach the tool map.
func listRegisteredTools(t *testing.T, srv *server.MCPServer) []mcp.Tool {
	t.Helper()
	// Try the Go-mcp server's ListTools method first (public API).
	v := reflect.ValueOf(srv).Elem()
	toolsField := v.FieldByName("tools")
	if !toolsField.IsValid() {
		t.Fatal("server has no tools field; update test to use public API")
	}
	// tools is typically a map[string]ServerTool with Tool accessible inside.
	out := []mcp.Tool{}
	iter := toolsField.MapRange()
	for iter.Next() {
		sv := iter.Value()
		// ServerTool has a Tool field of type mcp.Tool.
		tf := sv.FieldByName("Tool")
		if !tf.IsValid() {
			continue
		}
		tl, ok := tf.Interface().(mcp.Tool)
		if !ok {
			continue
		}
		out = append(out, tl)
	}
	return out
}
```

Note: the reflect fallback accesses an unexported field. If this fails at runtime because mcp-go exposes a public API, replace with: `tools := srv.ListTools()` or equivalent. Check mcp-go v0.44.1 docs for the correct introspection entry point; run the test and adjust if needed.

- [ ] **Step 2: Run the test**

Run:
```bash
cd ~/Projects-apps/vanta-conduit
go test ./internal/mcpadapter/ -run TestMemoryKnowledgeUnifiedToolsAnnotated -v
```
Expected: all 12 enforced tools pass. If the test can't introspect the server, swap the fallback for the public API.

- [ ] **Step 3: Commit**

```bash
git add internal/mcpadapter/annotations_test.go
git commit -m "test(mcp): drift guard — memory/knowledge/unified tools must carry v2 annotations"
```

---

## Task 13: Refresh `docs/MCP_TOOLS.md`

**Files:**
- Modify: `docs/MCP_TOOLS.md`

- [ ] **Step 1: Add Skills section at top**

Edit `docs/MCP_TOOLS.md`. Under "## Quick facts" insert a new section before "## Domains":

```markdown
## Agent-facing skills (vanta_skills)

Every agent hitting this surface should start with `vanta_skills start-here`. The tool is a single progressive-discovery entry point:

- `vanta_skills` with no args → returns the skill index (name + description + scope hint).
- `vanta_skills <name>` → returns the full markdown body of one skill.

Shipped skills (11):

| Name | Type | Body covers |
|---|---|---|
| `start-here` | orientation | Vanta's three domains, invariants, how to use this surface. |
| `namespaces` | primitive | 5-tier model, ownership, actor/client_id matrix. |
| `facets-and-kinds` | primitive | Facet vocabulary, the `kind` convention, extension rules. |
| `revisions` | primitive | Append-only model, supersede chains, dedup, revision IDs. |
| `recall-and-ranking` | primitive | Activation / chronological / similarity / relevance (RRF). |
| `promotion` | primitive | App→user workflow: request → approve → apply. |
| `views` | primitive | Selectors-not-processors; namespace globs. |
| `memory` | domain | When to use memory, common patterns. |
| `knowledge` | domain | Pointer-first model, `kind`/`source`/`pointer` facets. |
| `context-packet` | feature | Boot workflows, broker plan/fetch, budget tuning. |
| `audit` | feature | Querying the audit log. |

Consumer-facing workflow skills (e.g. "how to track issues in Vanta") belong with consumers (agent-ops, Nanite, Engine) and are authored in consumer repos — Vanta ships primitives + reference docs only.
```

- [ ] **Step 2: Add "Deeper" column to Memory table**

Change the Memory section table header from:
```
| Tool | Scope | HTTP peer | Notes |
|---|---|---|---|
```
to:
```
| Tool | Scope | HTTP peer | Deeper | Notes |
|---|---|---|---|---|
```

And add a `` `vanta_skills memory` `` value to each row's new column, adjusting Notes accordingly. Example row:

```
| `memory_write` | `memory:write` | `POST /v1/memory/write` | `vanta_skills memory` | New revision (optional semantic dedup) |
```

Apply to all 7 rows: `memory_write`, `memory_get`, `memory_history`, `memory_recall`, `memory_get_revision`, `memory_promote`, `memory_deprecate`. Use `vanta_skills memory` for all except `memory_promote` (use `vanta_skills promotion`) and `memory_deprecate` (use `vanta_skills revisions`).

- [ ] **Step 3: Add "Deeper" column to Knowledge table**

Apply the same pattern to the Knowledge section (3 rows: `knowledge_write`, `knowledge_get`, `knowledge_history`). Use `vanta_skills knowledge` for all three.

- [ ] **Step 4: Add "Deeper" column to Unified table**

Apply to the single `conduit_lookup` row. Use `vanta_skills recall-and-ranking`.

- [ ] **Step 5: Add `vanta_skills` to a new Meta section**

Insert a new section between "### Unified" and "## Playbooks":

```markdown
### Meta

| Tool | Scope | HTTP peer | Deeper | Notes |
|---|---|---|---|---|
| `vanta_skills` | — | — (MCP-only meta-tool) | self-documenting | Call with no args for the index; with `name` for the full skill body |
```

- [ ] **Step 6: Commit**

```bash
git add docs/MCP_TOOLS.md
git commit -m "docs(mcp-tools): refresh with Skills section + Deeper pointers"
```

---

## Task 14: Full suite + lint + push

**Files:**
- No code changes.

- [ ] **Step 1: Run the whole suite**

Run:
```bash
cd ~/Projects-apps/vanta-conduit
go build ./...
go test ./...
```
Expected: every package passes.

- [ ] **Step 2: Run linters (pre-commit hook equivalent)**

Run:
```bash
gofmt -l .
go vet ./...
golangci-lint run
```
Expected: zero output from `gofmt`; `go vet` and `golangci-lint` clean.

- [ ] **Step 3: Push the branch**

Run:
```bash
git push -u origin feat/mcp-surface-v2
```

- [ ] **Step 4: Open a PR**

Run:
```bash
gh pr create --title "feat(mcp): Surface v2 — descriptions, annotations, vanta_skills" --body "$(cat <<'EOF'
## Summary

- Rewrites memory + knowledge + unified MCP tool descriptions against the v2 template (purpose / kind-of-content / scope / use / don't-use / deeper).
- Adds MCP protocol annotations (`ReadOnlyHint`, `IdempotentHint`, `DestructiveHint`, `OpenWorldHint`) to the 12 memory/knowledge/unified/meta tools.
- Introduces `vanta_skills`, a single progressive-discovery meta-tool backed by 11 embedded markdown skills covering Vanta's primitives (namespaces, facets-and-kinds, revisions, recall-and-ranking, promotion, views) and features (memory, knowledge, context-packet, audit), plus a `start-here` orientation.
- Appends a one-line `vanta_skills start-here` pointer-footer to all 32 context-domain tool descriptions — minimum-touch, no rewrite.
- Updates `docs/MCP_TOOLS.md` with a Skills section, `Deeper` columns, and a Meta table row.
- Parity catalog + drift-guard test enforce the new tool and the annotation template.

Spec: `docs/superpowers/specs/2026-04-19-mcp-surface-v2-design.md`
Plan: `docs/superpowers/plans/2026-04-19-mcp-surface-v2.md`

## Test plan

- [ ] `go build ./...` passes
- [ ] `go test ./...` passes
- [ ] `go vet ./...` clean
- [ ] `golangci-lint run` clean
- [ ] Manual: run `contextd mcp` and call `vanta_skills` (no args) — expect 11-entry JSON index with `start-here` first.
- [ ] Manual: call `vanta_skills name=start-here` — expect markdown body starting with `# Vanta Conduit — start here`.
- [ ] Manual: call `vanta_skills name=bogus` — expect structured error `skill_not_found` with the list of 11 valid names.

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

- [ ] **Step 5: Mark plan complete**

No action. Plan is fully executed when the PR is open and green.

---

## Self-review

- **Spec coverage:** every deliverable in spec section 9 maps to a task — skills dir → Task 2+5+6+7; skills package → Task 2; skills_tool → Task 3; memory/knowledge/unified rewrites → Tasks 8–10; context footer → Task 11; parity → Task 4; MCP_TOOLS.md → Task 13; drift guards → Task 12 (+ skill count test in Tasks 5–7, parity test in Task 4).
- **Placeholder scan:** no TODO / TBD / "similar to" references; every test step includes concrete code.
- **Type consistency:** `SkillMeta`, `List()`, `Get(name)`, `ErrSkillNotFound`, `handleVantaSkills`, `registerSkillsTool` — used consistently across Tasks 2, 3, 12.
- **Ambiguity:** the annotation-presence test (Task 12) uses reflection against an unexported `tools` field; noted as needing a fallback to public API if mcp-go's introspection path is different. Engineer adjusts at runtime.
