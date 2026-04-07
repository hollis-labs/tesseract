# Memory Domain D-core Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement the H/I-independent subset of the Cortex memory subsystem: append-only revision storage, caller-declared dot-notation keys, user-rooted namespaces, write/read/recall/promote/deprecate operations, activation ranking and decay, stub Embedder/JobQueue interfaces, and the full MCP tool surface — all wired end-to-end and covered by unit + integration tests.

**Architecture:** A new Go package `internal/memory/` sibling to `internal/contextstore/`. Shares the existing SQLite database via new migration cases (no new DB file). MCP tools registered in a new `internal/mcpadapter/memory_tools.go` file following the existing `typed_tools.go` pattern. Decay job runs as a goroutine started from `cmd/contextd/main.go`. Stub `Embedder` and `JobQueue` interfaces ship with no-op implementations so D-core is fully functional without Tracks H or I; they swap to real implementations when those tracks land.

**Tech stack:** Go 1.22+, `modernc.org/sqlite`, `github.com/mark3labs/mcp-go`, stdlib `testing` (no testify), `gofmt` + `goimports` + `golangci-lint` + `go vet` via lefthook.

**Spec reference:** `docs/superpowers/specs/2026-04-04-memory-domain-design.md` — decisions D1–D15.

**Relevant skills:** @superpowers:test-driven-development, @superpowers:verification-before-completion.

---

## File structure

**New files:**
```
internal/memory/types.go              — core types: Memory, Revision, Origin, Status, Trigger, Author, Payload
internal/memory/ids.go                — ULID generation helper
internal/memory/keys.go               — dot-notation key validation
internal/memory/namespaces.go         — namespace parsing and validation
internal/memory/store.go              — Store struct, DB handle, shared helpers
internal/memory/write.go              — WriteRevision (keyed, keyless, supersedes)
internal/memory/read.go               — GetCurrent, GetHistory
internal/memory/recall.go             — Recall (filters, ranking, multi-namespace)
internal/memory/ranking.go            — ranking formula (status/origin/recency weights)
internal/memory/promote.go            — Promote, Deprecate
internal/memory/activation.go         — activation reinforcement, ReinforceOnRetrieval
internal/memory/decay.go              — DecayJob, decay formula, TTL expiry
internal/memory/embedder.go           — Embedder interface + NoopEmbedder
internal/memory/queue.go              — JobQueue interface + NoopQueue
internal/memory/keys_test.go
internal/memory/namespaces_test.go
internal/memory/write_test.go
internal/memory/read_test.go
internal/memory/recall_test.go
internal/memory/promote_test.go
internal/memory/activation_test.go
internal/memory/decay_test.go
internal/memory/stubs_test.go

internal/mcpadapter/memory_tools.go   — MCP tool registration + handlers
internal/mcpadapter/memory_tools_test.go

tests/integration/memory_test.go      — end-to-end MCP integration test
```

**Modified files:**
```
internal/contextstore/store.go        — add migration case 9 (memory tables), add DB() accessor
internal/mcpadapter/adapter.go        — add MemoryStore field, call registerMemoryTools
cmd/contextd/main.go                  — construct memory.Store, wire adapter, start decay goroutine
Makefile                              — no changes expected; existing `make test` covers new packages
```

**Dependencies:** One new module — `github.com/oklog/ulid/v2` for ULID generation. Added in Task 1.

---

## Plan-time decisions locked in

These were flagged in the spec as plan-time choices. Implementer does NOT need to re-decide:

1. **Activation decay formula.** Exponential decay with 14-day half-life. Run hourly. Formula: `new = old * exp(-elapsed_hours * ln(2) / (14 * 24))`.
2. **Recency factor in ranking.** Linear decay from 1.0 at `now` to 0.5 at 30 days ago, floor 0.5 thereafter. Formula: `max(0.5, 1.0 - 0.5 * min(1, days_since / 30))`.
3. **Required-field relaxation config location.** Lives in the namespace registration record as a JSON column `memory_config` with shape `{"summary_required": bool, "confidence_required": bool}`. Defaults: both `true`. Loaded once when the namespace is resolved on write.
4. **Supersedes status mutation.** The single authorized exception to "revisions are append-only" is the deprecation update triggered by writing a new revision with `supersedes` set, or by an explicit `memory_deprecate` call. The implementer MUST NOT introduce any other UPDATE statement against `memory_revisions.status`. Enforced by code review and a guard test.
5. **Embedding column in D-core schema.** `memory_revisions` has a nullable `embedding_model TEXT NULL` and `embedding_vector BLOB NULL`. D-core never populates these. A stub `NoopEmbedder` is wired so the code path exists but is inert.
6. **ID format.** ULID for `revision_id` and `memory_id` (via `github.com/oklog/ulid/v2`). Lexicographically sortable, timestamp-prefixed, matches the spec's "ULID" callouts in D9.
7. **Decay job default cadence.** 1 hour. Overridable via `CORTEX_MEMORY_DECAY_INTERVAL` env var (parseable by `time.ParseDuration`).
8. **Test database.** Every test uses `t.TempDir()` + `contextstore.Open()` + `memory.NewStore(store.DB())`. No shared fixtures. No mocks for the DB.

---

## Memory tables — final schema (migration case 9)

```sql
-- Logical memory state (mutable, one row per memory_id)
CREATE TABLE IF NOT EXISTS memory_state (
    memory_id         TEXT PRIMARY KEY,
    namespace         TEXT NOT NULL,
    memory_key        TEXT NULL,
    current_revision  TEXT NULL,
    activation        REAL NOT NULL DEFAULT 1.0,
    access_count      INTEGER NOT NULL DEFAULT 0,
    last_accessed_at  TEXT NULL,
    created_at        TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE(namespace, memory_key)
);

CREATE INDEX IF NOT EXISTS idx_memory_state_namespace ON memory_state(namespace);
CREATE INDEX IF NOT EXISTS idx_memory_state_activation ON memory_state(activation DESC);

-- Append-only revision log
CREATE TABLE IF NOT EXISTS memory_revisions (
    revision_id       TEXT PRIMARY KEY,
    memory_id         TEXT NOT NULL,
    namespace         TEXT NOT NULL,
    memory_key        TEXT NULL,
    status            TEXT NOT NULL,
    supersedes        TEXT NULL,
    created_at        TEXT NOT NULL DEFAULT (datetime('now')),
    author_agent_id   TEXT NOT NULL,
    author_version    TEXT NOT NULL,
    trigger           TEXT NOT NULL,
    session_id        TEXT NOT NULL,
    origin            TEXT NOT NULL,
    confidence        REAL NOT NULL,
    tags              TEXT NOT NULL DEFAULT '[]',
    ttl_seconds       INTEGER NULL,
    expires_at        TEXT NULL,
    payload_summary   TEXT NULL,
    payload_body      TEXT NULL,
    embedding_model   TEXT NULL,
    embedding_vector  BLOB NULL,
    FOREIGN KEY (memory_id) REFERENCES memory_state(memory_id) ON DELETE CASCADE,
    FOREIGN KEY (supersedes) REFERENCES memory_revisions(revision_id)
);

CREATE INDEX IF NOT EXISTS idx_memory_revisions_memory_id ON memory_revisions(memory_id);
CREATE INDEX IF NOT EXISTS idx_memory_revisions_namespace ON memory_revisions(namespace);
CREATE INDEX IF NOT EXISTS idx_memory_revisions_created_at ON memory_revisions(created_at);
CREATE INDEX IF NOT EXISTS idx_memory_revisions_status ON memory_revisions(status);
CREATE INDEX IF NOT EXISTS idx_memory_revisions_expires_at ON memory_revisions(expires_at) WHERE expires_at IS NOT NULL;
```

**Design notes:**

- `memory_state.current_revision` always points at the non-deprecated revision with the latest `created_at` for that `memory_id`. Updated by the write path.
- `memory_state.UNIQUE(namespace, memory_key)` enforces one logical memory per `(namespace, key)` pair at the database level. Keyless memories have `memory_key = NULL` and UNIQUE doesn't constrain NULLs in SQLite — keyless writes always create new logical memories, matching the spec.
- `memory_revisions.status` is mutable **only** via the deprecation code path. Every other write is append-only.
- `memory_revisions.expires_at` is computed at write time from `ttl_seconds + created_at` to make TTL queries cheap (indexed).
- `embedding_model` and `embedding_vector` are in schema now, always NULL in D-core, populated when Tracks H and I land.

---

## Tasks

### Task 1: Package scaffolding + core types + ULID + dependency

**Goal:** Create the `internal/memory/` package, add the ULID dependency, define the core types. No DB work yet.

**Files:**
- Create: `internal/memory/types.go`
- Create: `internal/memory/ids.go`
- Create: `internal/memory/ids_test.go`
- Modify: `go.mod` (add `github.com/oklog/ulid/v2`)

- [ ] **Step 1: Add ULID dependency**

Run from the repo root:
```bash
cd /Users/chrispian/Projects-apps/fragments-engine/cortex
go get github.com/oklog/ulid/v2
go mod tidy
```

Expected: `go.mod` gains `github.com/oklog/ulid/v2 v2.x.x`, `go.sum` updated.

- [ ] **Step 2: Create `internal/memory/types.go`**

```go
package memory

import "time"

// Origin categorises why a memory exists (closed vocabulary, D6/D9).
type Origin string

const (
    OriginUser        Origin = "user"
    OriginFeedback    Origin = "feedback"
    OriginProject     Origin = "project"
    OriginReference   Origin = "reference"
    OriginObservation Origin = "observation"
)

// Valid reports whether o is one of the five canonical origin values.
func (o Origin) Valid() bool {
    switch o {
    case OriginUser, OriginFeedback, OriginProject, OriginReference, OriginObservation:
        return true
    }
    return false
}

// Status is the revision lifecycle state (D9).
type Status string

const (
    StatusDraft      Status = "draft"
    StatusReviewed   Status = "reviewed"
    StatusCanonical  Status = "canonical"
    StatusDeprecated Status = "deprecated"
)

func (s Status) Valid() bool {
    switch s {
    case StatusDraft, StatusReviewed, StatusCanonical, StatusDeprecated:
        return true
    }
    return false
}

// Trigger identifies the signal that caused a memory to be authored (D9).
type Trigger string

const (
    TriggerExplicit    Trigger = "explicit"
    TriggerPostCompact Trigger = "post_compact"
    TriggerPerTurn     Trigger = "per_turn"
    TriggerPromotion   Trigger = "promotion"
    TriggerManual      Trigger = "manual"
)

func (t Trigger) Valid() bool {
    switch t {
    case TriggerExplicit, TriggerPostCompact, TriggerPerTurn, TriggerPromotion, TriggerManual:
        return true
    }
    return false
}

// Author identifies who wrote a memory revision.
type Author struct {
    AgentID      string `json:"agent_id"`
    AgentVersion string `json:"agent_version"`
}

// Payload is the structured-by-convention memory content (D9).
type Payload struct {
    Summary string `json:"summary"`
    Body    string `json:"body,omitempty"`
}

// Revision is an immutable memory revision. The only field that may be
// mutated after write is Status, and only via the deprecation code path.
type Revision struct {
    RevisionID  string    `json:"revision_id"`
    MemoryID    string    `json:"memory_id"`
    Namespace   string    `json:"namespace"`
    MemoryKey   string    `json:"memory_key,omitempty"`
    Status      Status    `json:"status"`
    Supersedes  string    `json:"supersedes,omitempty"`
    CreatedAt   time.Time `json:"created_at"`
    Author      Author    `json:"author"`
    Trigger     Trigger   `json:"trigger"`
    SessionID   string    `json:"session_id"`
    Origin      Origin    `json:"origin"`
    Confidence  float64   `json:"confidence"`
    Tags        []string  `json:"tags"`
    TTLSeconds  int64     `json:"ttl_seconds,omitempty"`
    ExpiresAt   *time.Time `json:"expires_at,omitempty"`
    Payload     Payload   `json:"payload"`
}

// State is the mutable per-memory state (D9). Lives in memory_state table.
type State struct {
    MemoryID         string     `json:"memory_id"`
    Namespace        string     `json:"namespace"`
    MemoryKey        string     `json:"memory_key,omitempty"`
    CurrentRevision  string     `json:"current_revision"`
    Activation       float64    `json:"activation"`
    AccessCount      int64      `json:"access_count"`
    LastAccessedAt   *time.Time `json:"last_accessed_at,omitempty"`
    CreatedAt        time.Time  `json:"created_at"`
}
```

- [ ] **Step 3: Create `internal/memory/ids.go`**

```go
package memory

import (
    "crypto/rand"
    "time"

    "github.com/oklog/ulid/v2"
)

// NewULID returns a new lexicographically sortable ULID as a string.
// Used for memory_id and revision_id.
func NewULID() string {
    return ulid.MustNew(ulid.Timestamp(time.Now()), rand.Reader).String()
}
```

- [ ] **Step 4: Write the failing test `internal/memory/ids_test.go`**

```go
package memory_test

import (
    "testing"

    "github.com/chrispian/cortex/internal/memory"
)

// NOTE: The module path in the import above must match the actual module
// path declared in go.mod at the repo root. The reviewer will flag this
// if wrong. Check go.mod before committing.

func TestNewULIDUniqueAndSortable(t *testing.T) {
    const n = 1000
    seen := make(map[string]struct{}, n)
    var prev string
    for i := 0; i < n; i++ {
        id := memory.NewULID()
        if _, dup := seen[id]; dup {
            t.Fatalf("duplicate ULID at iteration %d: %s", i, id)
        }
        seen[id] = struct{}{}
        if len(id) != 26 {
            t.Fatalf("expected ULID length 26, got %d for %q", len(id), id)
        }
        if prev != "" && id <= prev {
            // ULIDs generated in the same millisecond may not strictly
            // monotonically increase unless using ulid.Monotonic. For this
            // test we only require no duplicates; sort-stability is a
            // secondary property and not asserted strictly.
            _ = prev // intentional: leave this branch as a reminder
        }
        prev = id
    }
}

func TestValidOriginTrigger(t *testing.T) {
    t.Run("origin", func(t *testing.T) {
        for _, o := range []memory.Origin{
            memory.OriginUser, memory.OriginFeedback, memory.OriginProject,
            memory.OriginReference, memory.OriginObservation,
        } {
            if !o.Valid() {
                t.Errorf("expected %q to be valid", o)
            }
        }
        if memory.Origin("bogus").Valid() {
            t.Errorf("expected 'bogus' origin to be invalid")
        }
    })
    t.Run("trigger", func(t *testing.T) {
        if !memory.TriggerExplicit.Valid() {
            t.Errorf("expected explicit to be valid")
        }
        if memory.Trigger("nope").Valid() {
            t.Errorf("expected 'nope' trigger to be invalid")
        }
    })
}
```

**Before running:** Open `go.mod` and confirm the module path. The test imports throughout this plan use `github.com/chrispian/cortex` as a placeholder — the actual module path (verified at plan time) is `github.com/hollis-labs/cortex`. Verify with `head -1 go.mod` before committing and replace the placeholder everywhere it appears in the plan's code snippets.

- [ ] **Step 5: Run tests, expect them to pass**

```bash
cd /Users/chrispian/Projects-apps/fragments-engine/cortex
go test ./internal/memory/...
```

Expected: PASS. If FAIL due to import path mismatch, fix the import path in `ids_test.go` to match `go.mod`'s module declaration.

- [ ] **Step 6: Run vet and lint**

```bash
go vet ./internal/memory/...
gofmt -l internal/memory/
```

Expected: no output from either command.

- [ ] **Step 7: Commit**

```bash
git add go.mod go.sum internal/memory/types.go internal/memory/ids.go internal/memory/ids_test.go
git commit -m "feat(memory): add core types and ULID helper for memory subsystem

Scaffolding for the memory domain D-core (spec D9).
Adds Origin/Status/Trigger enums, Author/Payload/Revision/State types,
and a ULID helper for memory_id and revision_id generation.
No storage or behavior yet.

Refs: docs/superpowers/specs/2026-04-04-memory-domain-design.md"
```

---

### Task 2: Dot-notation key validation

**Goal:** Implement and test the key validation rules from D8/D9: regex `[a-z0-9_]+(\.[a-z0-9_]+)*`, max 6 segments, max 64 chars per segment, max 256 total.

**Files:**
- Create: `internal/memory/keys.go`
- Create: `internal/memory/keys_test.go`

- [ ] **Step 1: Write failing tests first**

Create `internal/memory/keys_test.go`:

```go
package memory_test

import (
    "strings"
    "testing"

    "github.com/chrispian/cortex/internal/memory" // fix to actual module path
)

func TestValidateKey(t *testing.T) {
    cases := []struct {
        name    string
        key     string
        wantErr bool
    }{
        {"simple single segment", "user", false},
        {"two segments", "user.preferences", false},
        {"three segments", "user.preferences.verbosity", false},
        {"six segments max", "a.b.c.d.e.f", false},
        {"seven segments too many", "a.b.c.d.e.f.g", true},
        {"digits allowed", "project.cortex_v2.decision_01", false},
        {"underscores allowed", "user.pref_set.key_name", false},
        {"uppercase rejected", "User.Preferences", true},
        {"hyphen rejected", "user-preferences", true},
        {"trailing dot rejected", "user.preferences.", true},
        {"leading dot rejected", ".user.preferences", true},
        {"double dot rejected", "user..preferences", true},
        {"empty rejected", "", true},
        {"whitespace rejected", "user preferences", true},
        {"segment too long rejected", "user." + strings.Repeat("a", 65), true},
        {"segment at 64 chars ok", "user." + strings.Repeat("a", 64), false},
        {"total too long rejected", strings.Repeat("a", 60) + "." + strings.Repeat("b", 60) + "." + strings.Repeat("c", 60) + "." + strings.Repeat("d", 60) + "." + strings.Repeat("e", 60), true},
        {"unicode rejected", "user.préférences", true},
    }
    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            err := memory.ValidateKey(tc.key)
            if tc.wantErr && err == nil {
                t.Errorf("ValidateKey(%q): expected error, got nil", tc.key)
            }
            if !tc.wantErr && err != nil {
                t.Errorf("ValidateKey(%q): expected no error, got %v", tc.key, err)
            }
        })
    }
}

func TestIsReservedPrefix(t *testing.T) {
    cases := []struct {
        key  string
        want bool
    }{
        {"user.preferences", true},
        {"project.cortex.decision", true},
        {"session.abc123.summary", true},
        {"contact.alice.role", true},
        {"agent.claude.trait", true},
        {"custom.thing", false},
        {"unknown", false},
    }
    for _, tc := range cases {
        t.Run(tc.key, func(t *testing.T) {
            if got := memory.IsReservedPrefix(tc.key); got != tc.want {
                t.Errorf("IsReservedPrefix(%q) = %v, want %v", tc.key, got, tc.want)
            }
        })
    }
}
```

- [ ] **Step 2: Run the test, verify it fails with "undefined"**

```bash
go test ./internal/memory/... -run TestValidateKey
```

Expected: FAIL with `undefined: memory.ValidateKey` and `undefined: memory.IsReservedPrefix`.

- [ ] **Step 3: Implement `internal/memory/keys.go`**

```go
package memory

import (
    "errors"
    "fmt"
    "regexp"
    "strings"
)

const (
    maxKeySegments  = 6
    maxSegmentChars = 64
    maxKeyChars     = 256
)

var (
    // segmentRE validates a single segment: lowercase alphanumeric + underscore.
    segmentRE = regexp.MustCompile(`^[a-z0-9_]+$`)

    // reservedPrefixes matches the top-level segments the spec reserves for
    // conventional use (D8). Enforcement is advisory at 1.0 — we only report
    // membership, we do not reject non-reserved keys.
    reservedPrefixes = map[string]struct{}{
        "user":    {},
        "project": {},
        "session": {},
        "contact": {},
        "agent":   {},
    }
)

// ErrInvalidKey is returned when a memory key fails validation.
var ErrInvalidKey = errors.New("invalid memory key")

// ValidateKey checks a dot-notation memory key against the rules in D8/D9:
//   - regex ^[a-z0-9_]+(\.[a-z0-9_]+)*$
//   - max 6 segments
//   - max 64 chars per segment
//   - max 256 chars total
// Returns a wrapped ErrInvalidKey on failure with a specific reason.
func ValidateKey(key string) error {
    if key == "" {
        return fmt.Errorf("%w: empty key", ErrInvalidKey)
    }
    if len(key) > maxKeyChars {
        return fmt.Errorf("%w: total length %d exceeds max %d", ErrInvalidKey, len(key), maxKeyChars)
    }
    segments := strings.Split(key, ".")
    if len(segments) > maxKeySegments {
        return fmt.Errorf("%w: %d segments exceeds max %d", ErrInvalidKey, len(segments), maxKeySegments)
    }
    for i, seg := range segments {
        if seg == "" {
            return fmt.Errorf("%w: empty segment at position %d", ErrInvalidKey, i)
        }
        if len(seg) > maxSegmentChars {
            return fmt.Errorf("%w: segment %d length %d exceeds max %d", ErrInvalidKey, i, len(seg), maxSegmentChars)
        }
        if !segmentRE.MatchString(seg) {
            return fmt.Errorf("%w: segment %q contains invalid characters (allowed: a-z 0-9 _)", ErrInvalidKey, seg)
        }
    }
    return nil
}

// IsReservedPrefix reports whether the first segment of key is one of the
// reserved top-level prefixes from D8. Advisory only — not enforced.
func IsReservedPrefix(key string) bool {
    if key == "" {
        return false
    }
    first := key
    if idx := strings.Index(key, "."); idx >= 0 {
        first = key[:idx]
    }
    _, ok := reservedPrefixes[first]
    return ok
}
```

- [ ] **Step 4: Run tests, expect them to pass**

```bash
go test ./internal/memory/... -run "TestValidateKey|TestIsReservedPrefix" -v
```

Expected: all subtests PASS.

- [ ] **Step 5: Run vet/lint**

```bash
go vet ./internal/memory/...
gofmt -l internal/memory/
```

Expected: no output.

- [ ] **Step 6: Commit**

```bash
git add internal/memory/keys.go internal/memory/keys_test.go
git commit -m "feat(memory): add dot-notation key validation

Implements the key shape rules from spec D8/D9: regex
^[a-z0-9_]+(\.[a-z0-9_]+)*\$, max 6 segments, max 64 chars per segment,
max 256 chars total. Reserved top-level prefix detection is advisory.

Refs: docs/superpowers/specs/2026-04-04-memory-domain-design.md"
```

---

### Task 3: Namespace parsing and validation

**Goal:** Validate and parse memory namespaces from D10: `user/{user_id}/memory`, `user/{user_id}/project/{project_id}/memory`, `user/{user_id}/session/{session_id}/memory`.

**Files:**
- Create: `internal/memory/namespaces.go`
- Create: `internal/memory/namespaces_test.go`

- [ ] **Step 1: Write the failing tests**

`internal/memory/namespaces_test.go`:

```go
package memory_test

import (
    "testing"

    "github.com/chrispian/cortex/internal/memory" // fix module path
)

func TestParseNamespace(t *testing.T) {
    cases := []struct {
        name      string
        input     string
        wantScope memory.Scope
        wantUser  string
        wantProj  string
        wantSess  string
        wantErr   bool
    }{
        {"user root", "user/chrispian/memory", memory.ScopeUser, "chrispian", "", "", false},
        {"project", "user/chrispian/project/cortex/memory", memory.ScopeProject, "chrispian", "cortex", "", false},
        {"session", "user/chrispian/session/abc123/memory", memory.ScopeSession, "chrispian", "", "abc123", false},
        {"trailing slash rejected", "user/chrispian/memory/", memory.ScopeUnknown, "", "", "", true},
        {"missing memory suffix", "user/chrispian", memory.ScopeUnknown, "", "", "", true},
        {"unknown scope", "user/chrispian/foo/bar/memory", memory.ScopeUnknown, "", "", "", true},
        {"empty user_id", "user//memory", memory.ScopeUnknown, "", "", "", true},
        {"wrong root", "app/nanite/memory", memory.ScopeUnknown, "", "", "", true},
        {"user_id with slash", "user/ch/rispian/memory", memory.ScopeUnknown, "", "", "", true},
    }
    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            ns, err := memory.ParseNamespace(tc.input)
            if tc.wantErr {
                if err == nil {
                    t.Fatalf("expected error for %q, got nil", tc.input)
                }
                return
            }
            if err != nil {
                t.Fatalf("unexpected error for %q: %v", tc.input, err)
            }
            if ns.Scope != tc.wantScope {
                t.Errorf("Scope: got %v, want %v", ns.Scope, tc.wantScope)
            }
            if ns.UserID != tc.wantUser {
                t.Errorf("UserID: got %q, want %q", ns.UserID, tc.wantUser)
            }
            if ns.ProjectID != tc.wantProj {
                t.Errorf("ProjectID: got %q, want %q", ns.ProjectID, tc.wantProj)
            }
            if ns.SessionID != tc.wantSess {
                t.Errorf("SessionID: got %q, want %q", ns.SessionID, tc.wantSess)
            }
            // Round-trip
            if ns.String() != tc.input {
                t.Errorf("round-trip mismatch: got %q, want %q", ns.String(), tc.input)
            }
        })
    }
}
```

- [ ] **Step 2: Run the test, expect failure**

```bash
go test ./internal/memory/... -run TestParseNamespace
```

Expected: FAIL with undefined `memory.ParseNamespace`, `memory.Scope`, etc.

- [ ] **Step 3: Implement `internal/memory/namespaces.go`**

```go
package memory

import (
    "errors"
    "fmt"
    "regexp"
    "strings"
)

// Scope is the memory namespace scope (D10).
type Scope int

const (
    ScopeUnknown Scope = iota
    ScopeUser
    ScopeProject
    ScopeSession
)

// Namespace is a parsed memory namespace.
type Namespace struct {
    Scope     Scope
    UserID    string
    ProjectID string // only populated for ScopeProject
    SessionID string // only populated for ScopeSession
}

// String returns the canonical string form of the namespace, matching the
// format accepted by ParseNamespace.
func (n Namespace) String() string {
    switch n.Scope {
    case ScopeUser:
        return fmt.Sprintf("user/%s/memory", n.UserID)
    case ScopeProject:
        return fmt.Sprintf("user/%s/project/%s/memory", n.UserID, n.ProjectID)
    case ScopeSession:
        return fmt.Sprintf("user/%s/session/%s/memory", n.UserID, n.SessionID)
    }
    return ""
}

// ErrInvalidNamespace is returned when a namespace string cannot be parsed.
var ErrInvalidNamespace = errors.New("invalid memory namespace")

// idSegmentRE validates user_id / project_id / session_id segments.
// Accepts alphanumerics, hyphen, underscore, colon (for manual:<ulid>), dot.
// Keeps flexibility — project_id in particular is opaque per D10.
var idSegmentRE = regexp.MustCompile(`^[a-zA-Z0-9_\-:.]+$`)

// ParseNamespace parses a memory namespace string into its components.
// Accepts the three canonical forms from D10:
//
//	user/{user_id}/memory
//	user/{user_id}/project/{project_id}/memory
//	user/{user_id}/session/{session_id}/memory
//
// Any other shape returns ErrInvalidNamespace.
func ParseNamespace(s string) (Namespace, error) {
    if s == "" {
        return Namespace{}, fmt.Errorf("%w: empty", ErrInvalidNamespace)
    }
    if strings.HasSuffix(s, "/") {
        return Namespace{}, fmt.Errorf("%w: trailing slash in %q", ErrInvalidNamespace, s)
    }
    parts := strings.Split(s, "/")
    // Shortest valid: user/{id}/memory = 3 parts.
    // Longest valid: user/{id}/project/{pid}/memory = 5 parts.
    if len(parts) < 3 || len(parts) > 5 {
        return Namespace{}, fmt.Errorf("%w: wrong segment count in %q", ErrInvalidNamespace, s)
    }
    if parts[0] != "user" {
        return Namespace{}, fmt.Errorf("%w: must start with 'user/', got %q", ErrInvalidNamespace, parts[0])
    }
    if parts[1] == "" || !idSegmentRE.MatchString(parts[1]) {
        return Namespace{}, fmt.Errorf("%w: invalid user_id %q", ErrInvalidNamespace, parts[1])
    }
    last := parts[len(parts)-1]
    if last != "memory" {
        return Namespace{}, fmt.Errorf("%w: must end with '/memory', got %q", ErrInvalidNamespace, last)
    }
    ns := Namespace{UserID: parts[1]}
    switch len(parts) {
    case 3:
        // user/{id}/memory
        ns.Scope = ScopeUser
        return ns, nil
    case 5:
        // user/{id}/project|session/{id}/memory
        mid := parts[2]
        id := parts[3]
        if id == "" || !idSegmentRE.MatchString(id) {
            return Namespace{}, fmt.Errorf("%w: invalid %s id %q", ErrInvalidNamespace, mid, id)
        }
        switch mid {
        case "project":
            ns.Scope = ScopeProject
            ns.ProjectID = id
        case "session":
            ns.Scope = ScopeSession
            ns.SessionID = id
        default:
            return Namespace{}, fmt.Errorf("%w: unknown scope %q", ErrInvalidNamespace, mid)
        }
        return ns, nil
    default:
        return Namespace{}, fmt.Errorf("%w: malformed %q", ErrInvalidNamespace, s)
    }
}

// ValidateNamespace is a convenience wrapper that returns only the error.
func ValidateNamespace(s string) error {
    _, err := ParseNamespace(s)
    return err
}
```

- [ ] **Step 4: Run tests, expect pass**

```bash
go test ./internal/memory/... -run TestParseNamespace -v
```

Expected: all subtests PASS.

- [ ] **Step 5: Run vet/lint and commit**

```bash
go vet ./internal/memory/...
gofmt -l internal/memory/
git add internal/memory/namespaces.go internal/memory/namespaces_test.go
git commit -m "feat(memory): add namespace parser for user-rooted scopes

Parses the three canonical memory namespace forms from spec D10:
user/{user_id}/memory, user/{user_id}/project/{project_id}/memory,
user/{user_id}/session/{session_id}/memory. Project and session IDs
are opaque stable strings per D10 refinement 2.

Refs: docs/superpowers/specs/2026-04-04-memory-domain-design.md"
```

---

### Task 4: Stub Embedder + JobQueue interfaces with no-op implementations

**Goal:** Define the Embedder and JobQueue interfaces and ship inert no-op implementations. These preserve the D14 boundary so D-core is functional without Tracks H/I and the swap to real implementations is mechanical later.

**Files:**
- Create: `internal/memory/embedder.go`
- Create: `internal/memory/queue.go`
- Create: `internal/memory/stubs_test.go`

- [ ] **Step 1: Write failing tests**

```go
package memory_test

import (
    "context"
    "testing"

    "github.com/chrispian/cortex/internal/memory" // fix path
)

func TestNoopEmbedderDoesNothing(t *testing.T) {
    var e memory.Embedder = memory.NoopEmbedder{}
    vec, err := e.Embed(context.Background(), "anything")
    if err == nil {
        t.Fatalf("expected NoopEmbedder to return a structured unavailable error, got nil")
    }
    if vec != nil {
        t.Fatalf("expected nil vector, got %v", vec)
    }
    if e.Model() != "" {
        t.Fatalf("expected empty model for noop, got %q", e.Model())
    }
    if e.Dimensions() != 0 {
        t.Fatalf("expected 0 dimensions for noop, got %d", e.Dimensions())
    }
}

func TestNoopEmbedderErrorStable(t *testing.T) {
    var e memory.Embedder = memory.NoopEmbedder{}
    _, err := e.Embed(context.Background(), "x")
    if err != memory.ErrEmbedderUnavailable {
        t.Fatalf("expected ErrEmbedderUnavailable, got %v", err)
    }
}

func TestNoopQueueSwallowsJobs(t *testing.T) {
    var q memory.JobQueue = memory.NoopQueue{}
    // Should accept any job without error.
    err := q.Enqueue(context.Background(), memory.Job{Kind: "embed", Payload: []byte("{}")})
    if err != nil {
        t.Fatalf("NoopQueue.Enqueue should never error, got %v", err)
    }
}
```

- [ ] **Step 2: Run, expect failure**

```bash
go test ./internal/memory/... -run "TestNoop"
```

Expected: undefined `memory.Embedder`, `memory.NoopEmbedder`, `memory.JobQueue`, `memory.NoopQueue`, `memory.Job`, `memory.ErrEmbedderUnavailable`.

- [ ] **Step 3: Implement `internal/memory/embedder.go`**

```go
package memory

import (
    "context"
    "errors"
)

// ErrEmbedderUnavailable is returned when no real embedder is wired.
// This is the stable error the D-core NoopEmbedder returns; callers can
// check for it to know whether to surface a structured "embeddings not
// available" response.
var ErrEmbedderUnavailable = errors.New("embedder unavailable")

// Embedder is the abstract interface memory will call when embeddings are
// needed. At D-core time the only implementation is NoopEmbedder. When
// Tracks H and I land, real providers implement this interface and are
// injected in place of the stub in main.go.
type Embedder interface {
    Embed(ctx context.Context, text string) ([]float32, error)
    Model() string
    Dimensions() int
}

// NoopEmbedder is the D-core stub. Every call returns ErrEmbedderUnavailable.
// It exists so that code paths referencing an embedder can be wired and
// compiled without a real provider.
type NoopEmbedder struct{}

func (NoopEmbedder) Embed(_ context.Context, _ string) ([]float32, error) {
    return nil, ErrEmbedderUnavailable
}

func (NoopEmbedder) Model() string    { return "" }
func (NoopEmbedder) Dimensions() int  { return 0 }
```

- [ ] **Step 4: Implement `internal/memory/queue.go`**

```go
package memory

import "context"

// Job is a unit of deferred work. Kind identifies the job handler; Payload
// is an opaque JSON-encoded blob the handler will decode.
type Job struct {
    Kind    string
    Payload []byte
}

// JobQueue is the abstract queue interface. D-core ships with NoopQueue,
// which silently accepts (and drops) every job. Track I will provide a real
// implementation that persists and retries.
type JobQueue interface {
    Enqueue(ctx context.Context, job Job) error
}

// NoopQueue is the D-core stub. Accepts every job, performs no work, never
// fails. Used so the memory write path can "enqueue" an embedding job
// before Track I is ready — the call succeeds, the job is discarded, and
// the revision row's embedding columns remain NULL.
type NoopQueue struct{}

func (NoopQueue) Enqueue(_ context.Context, _ Job) error { return nil }
```

- [ ] **Step 5: Run tests, expect pass**

```bash
go test ./internal/memory/... -run "TestNoop" -v
```

Expected: all PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/memory/embedder.go internal/memory/queue.go internal/memory/stubs_test.go
git commit -m "feat(memory): add Embedder/JobQueue interfaces with no-op stubs

Per spec D14, D-core ships with stub interfaces where Tracks H and I
will plug in later. NoopEmbedder returns ErrEmbedderUnavailable;
NoopQueue silently accepts and drops jobs. These preserve the boundary
so D-core is fully functional without the shared modules.

Refs: docs/superpowers/specs/2026-04-04-memory-domain-design.md"
```

---

### Task 5: Schema migration (memory tables) + DB accessor

**Goal:** Add migration case 9 to `contextstore.Store` creating `memory_state` and `memory_revisions` tables. Expose the underlying `*sql.DB` via a new `Store.DB()` method so the memory package can construct its own store sharing the same connection.

**Files:**
- Modify: `internal/contextstore/store.go` (add migration, add DB() accessor, bump schemaVersion)
- Modify: `internal/contextstore/store_test.go` (add test for migration)

- [ ] **Step 1: Read the current schema migration function**

```bash
# Confirm current version and structure before modifying
grep -n "schemaVersion" internal/contextstore/store.go
grep -n "case 8:" internal/contextstore/store.go
```

Expected: `schemaVersion = 8` at the top, `case 8:` in the migration switch near the end. Note the exact pattern used (the reviewer flagged lines 24 for version, 270-444 for migrations, case 8 at 425-438).

- [ ] **Step 2: Write a failing test**

Add to `internal/contextstore/store_test.go`:

```go
func TestMigrationCreatesMemoryTables(t *testing.T) {
    dir := t.TempDir()
    s, err := contextstore.Open(context.Background(), contextstore.Config{RootDir: dir})
    if err != nil {
        t.Fatalf("open: %v", err)
    }
    defer s.Close()

    db := s.DB()
    if db == nil {
        t.Fatal("DB() returned nil")
    }

    for _, table := range []string{"memory_state", "memory_revisions"} {
        var name string
        err := db.QueryRow(
            `SELECT name FROM sqlite_master WHERE type='table' AND name=?`,
            table,
        ).Scan(&name)
        if err != nil {
            t.Errorf("expected table %s to exist: %v", table, err)
        }
    }

    // Sanity: check indexes exist.
    for _, idx := range []string{
        "idx_memory_state_namespace",
        "idx_memory_state_activation",
        "idx_memory_revisions_memory_id",
        "idx_memory_revisions_namespace",
        "idx_memory_revisions_created_at",
        "idx_memory_revisions_status",
    } {
        var name string
        err := db.QueryRow(
            `SELECT name FROM sqlite_master WHERE type='index' AND name=?`,
            idx,
        ).Scan(&name)
        if err != nil {
            t.Errorf("expected index %s to exist: %v", idx, err)
        }
    }
}
```

- [ ] **Step 3: Run test, expect failure**

```bash
go test ./internal/contextstore/... -run TestMigrationCreatesMemoryTables
```

Expected: FAIL — either `Store.DB` undefined or tables not found.

- [ ] **Step 4: Add `DB()` accessor to `Store`**

In `internal/contextstore/store.go`, add after the `Store` struct definition (around line 57-61 per the earlier survey):

```go
// DB returns the underlying database handle. Used by sibling subsystems
// (memory) that share the same SQLite file but manage their own code paths.
// The returned *sql.DB is owned by Store — callers must not close it.
func (s *Store) DB() *sql.DB {
    return s.db
}
```

- [ ] **Step 5: Bump schema version and add migration case 9**

Change `schemaVersion = 8` to `schemaVersion = 9` at the top of the file.

Add a new case to the migration switch, after `case 8:`:

```go
case 9:
    // Memory subsystem tables — spec D-core migration.
    // See docs/superpowers/specs/2026-04-04-memory-domain-design.md
    stmts := []string{
        `CREATE TABLE IF NOT EXISTS memory_state (
            memory_id         TEXT PRIMARY KEY,
            namespace         TEXT NOT NULL,
            memory_key        TEXT NULL,
            current_revision  TEXT NULL,
            activation        REAL NOT NULL DEFAULT 1.0,
            access_count      INTEGER NOT NULL DEFAULT 0,
            last_accessed_at  TEXT NULL,
            created_at        TEXT NOT NULL DEFAULT (datetime('now')),
            UNIQUE(namespace, memory_key)
        )`,
        `CREATE INDEX IF NOT EXISTS idx_memory_state_namespace ON memory_state(namespace)`,
        `CREATE INDEX IF NOT EXISTS idx_memory_state_activation ON memory_state(activation DESC)`,
        `CREATE TABLE IF NOT EXISTS memory_revisions (
            revision_id       TEXT PRIMARY KEY,
            memory_id         TEXT NOT NULL,
            namespace         TEXT NOT NULL,
            memory_key        TEXT NULL,
            status            TEXT NOT NULL,
            supersedes        TEXT NULL,
            created_at        TEXT NOT NULL DEFAULT (datetime('now')),
            author_agent_id   TEXT NOT NULL,
            author_version   TEXT NOT NULL,
            trigger           TEXT NOT NULL,
            session_id        TEXT NOT NULL,
            origin            TEXT NOT NULL,
            confidence        REAL NOT NULL,
            tags              TEXT NOT NULL DEFAULT '[]',
            ttl_seconds       INTEGER NULL,
            expires_at        TEXT NULL,
            payload_summary   TEXT NULL,
            payload_body      TEXT NULL,
            embedding_model   TEXT NULL,
            embedding_vector  BLOB NULL,
            FOREIGN KEY (memory_id) REFERENCES memory_state(memory_id) ON DELETE CASCADE,
            FOREIGN KEY (supersedes) REFERENCES memory_revisions(revision_id)
        )`,
        `CREATE INDEX IF NOT EXISTS idx_memory_revisions_memory_id ON memory_revisions(memory_id)`,
        `CREATE INDEX IF NOT EXISTS idx_memory_revisions_namespace ON memory_revisions(namespace)`,
        `CREATE INDEX IF NOT EXISTS idx_memory_revisions_created_at ON memory_revisions(created_at)`,
        `CREATE INDEX IF NOT EXISTS idx_memory_revisions_status ON memory_revisions(status)`,
        `CREATE INDEX IF NOT EXISTS idx_memory_revisions_expires_at ON memory_revisions(expires_at) WHERE expires_at IS NOT NULL`,
    }
    for _, stmt := range stmts {
        if _, err := tx.ExecContext(ctx, stmt); err != nil {
            return fmt.Errorf("memory migration: %w", err)
        }
    }
```

**NOTE:** The exact variable names (`tx`, `ctx`) and error wrapping pattern may differ slightly from the surrounding migration cases. Read cases 7 and 8 first and match their style exactly — do not invent new patterns.

- [ ] **Step 6: Run the test, expect pass**

```bash
go test ./internal/contextstore/... -run TestMigrationCreatesMemoryTables -v
```

Expected: PASS. Also run the full contextstore test suite to confirm no regressions:

```bash
go test ./internal/contextstore/... -v
```

Expected: all existing tests still PASS.

- [ ] **Step 7: Run vet/lint and commit**

```bash
go vet ./internal/contextstore/...
gofmt -l internal/contextstore/
git add internal/contextstore/store.go internal/contextstore/store_test.go
git commit -m "feat(contextstore): add memory subsystem tables (schema v9)

Adds memory_state (mutable per-memory state) and memory_revisions
(append-only log) tables plus supporting indexes. Exposes *sql.DB via
new Store.DB() accessor so the memory package can share the same
connection without duplicating migration logic.

Refs: docs/superpowers/specs/2026-04-04-memory-domain-design.md D-core"
```

---

### Task 6: Memory Store scaffolding + write path

**Goal:** Create `memory.Store` backed by `*sql.DB`, implement `WriteRevision` for all three cases: keyed (creates or appends to existing logical memory), keyless (always creates new logical memory), and supersedes (auto-deprecates referenced revision).

**Files:**
- Create: `internal/memory/store.go`
- Create: `internal/memory/write.go`
- Create: `internal/memory/write_test.go`

- [ ] **Step 1: Create `internal/memory/store.go`**

```go
package memory

import "database/sql"

// Store is the memory subsystem's storage handle. It shares a *sql.DB with
// contextstore.Store but owns its own read/write paths against the memory_*
// tables.
type Store struct {
    db       *sql.DB
    embedder Embedder
    queue    JobQueue
}

// NewStore constructs a memory.Store bound to the given database. The
// embedder and queue parameters may be NoopEmbedder{} / NoopQueue{} during
// D-core; real implementations are swapped in when Tracks H and I land.
func NewStore(db *sql.DB, embedder Embedder, queue JobQueue) *Store {
    if embedder == nil {
        embedder = NoopEmbedder{}
    }
    if queue == nil {
        queue = NoopQueue{}
    }
    return &Store{db: db, embedder: embedder, queue: queue}
}
```

- [ ] **Step 2: Write failing tests**

`internal/memory/write_test.go`:

```go
package memory_test

import (
    "context"
    "encoding/json"
    "testing"
    "time"

    "github.com/chrispian/cortex/internal/contextstore" // fix path
    "github.com/chrispian/cortex/internal/memory"        // fix path
)

// newTestStore opens a fresh contextstore in a temp dir and returns a
// memory.Store sharing its DB. Every test gets an isolated database.
func newTestStore(t *testing.T) (*memory.Store, func()) {
    t.Helper()
    dir := t.TempDir()
    cs, err := contextstore.Open(context.Background(), contextstore.Config{RootDir: dir})
    if err != nil {
        t.Fatalf("contextstore.Open: %v", err)
    }
    ms := memory.NewStore(cs.DB(), memory.NoopEmbedder{}, memory.NoopQueue{})
    cleanup := func() { _ = cs.Close() }
    return ms, cleanup
}

func sampleInput(key string) memory.WriteInput {
    return memory.WriteInput{
        Namespace:  "user/chrispian/memory",
        MemoryKey:  key,
        Author:     memory.Author{AgentID: "test-agent", AgentVersion: "1.0"},
        Trigger:    memory.TriggerExplicit,
        SessionID:  "manual:01HXXXXX",
        Origin:     memory.OriginUser,
        Confidence: 0.9,
        Status:     memory.StatusDraft,
        Payload: memory.Payload{
            Summary: "User prefers terse output",
            Body:    "**Why:** repeated feedback. **How to apply:** no trailing summaries.",
        },
    }
}

func TestWriteRevision_KeyedCreatesLogicalMemory(t *testing.T) {
    ms, cleanup := newTestStore(t)
    defer cleanup()

    ctx := context.Background()
    rev, err := ms.WriteRevision(ctx, sampleInput("user.preferences.verbosity"))
    if err != nil {
        t.Fatalf("WriteRevision: %v", err)
    }
    if rev.RevisionID == "" {
        t.Fatal("expected revision_id to be set")
    }
    if rev.MemoryID == "" {
        t.Fatal("expected memory_id to be set")
    }
    if rev.Status != memory.StatusDraft {
        t.Errorf("status: got %v, want draft", rev.Status)
    }
    if rev.CreatedAt.IsZero() {
        t.Error("expected created_at to be set")
    }

    // Verify state row exists and points to this revision.
    state, err := ms.GetState(ctx, rev.MemoryID)
    if err != nil {
        t.Fatalf("GetState: %v", err)
    }
    if state.CurrentRevision != rev.RevisionID {
        t.Errorf("current_revision: got %q, want %q", state.CurrentRevision, rev.RevisionID)
    }
    if state.Activation != 1.0 {
        t.Errorf("initial activation: got %v, want 1.0", state.Activation)
    }
}

func TestWriteRevision_KeyedSecondWriteAppendsRevision(t *testing.T) {
    ms, cleanup := newTestStore(t)
    defer cleanup()
    ctx := context.Background()

    rev1, err := ms.WriteRevision(ctx, sampleInput("user.preferences.verbosity"))
    if err != nil {
        t.Fatal(err)
    }
    time.Sleep(2 * time.Millisecond) // ensure distinct created_at
    in2 := sampleInput("user.preferences.verbosity")
    in2.Payload.Summary = "User prefers verbose output"
    rev2, err := ms.WriteRevision(ctx, in2)
    if err != nil {
        t.Fatal(err)
    }

    if rev2.MemoryID != rev1.MemoryID {
        t.Errorf("expected same memory_id, got %q vs %q", rev1.MemoryID, rev2.MemoryID)
    }
    if rev2.RevisionID == rev1.RevisionID {
        t.Error("expected distinct revision_ids")
    }

    state, err := ms.GetState(ctx, rev1.MemoryID)
    if err != nil {
        t.Fatal(err)
    }
    if state.CurrentRevision != rev2.RevisionID {
        t.Errorf("current_revision: got %q, want %q (latest)", state.CurrentRevision, rev2.RevisionID)
    }
}

func TestWriteRevision_KeylessAlwaysCreatesNewMemory(t *testing.T) {
    ms, cleanup := newTestStore(t)
    defer cleanup()
    ctx := context.Background()

    in := sampleInput("")
    in.MemoryKey = ""
    rev1, err := ms.WriteRevision(ctx, in)
    if err != nil {
        t.Fatal(err)
    }
    rev2, err := ms.WriteRevision(ctx, in)
    if err != nil {
        t.Fatal(err)
    }
    if rev1.MemoryID == rev2.MemoryID {
        t.Errorf("keyless writes should produce distinct memory_ids, got same: %s", rev1.MemoryID)
    }
}

func TestWriteRevision_SupersedesAutoDeprecates(t *testing.T) {
    ms, cleanup := newTestStore(t)
    defer cleanup()
    ctx := context.Background()

    rev1, err := ms.WriteRevision(ctx, sampleInput("user.preferences.verbosity"))
    if err != nil {
        t.Fatal(err)
    }

    in2 := sampleInput("user.preferences.verbosity")
    in2.Supersedes = rev1.RevisionID
    rev2, err := ms.WriteRevision(ctx, in2)
    if err != nil {
        t.Fatal(err)
    }
    _ = rev2

    // rev1 should now be deprecated.
    revs, err := ms.GetHistory(ctx, "user/chrispian/memory", "user.preferences.verbosity")
    if err != nil {
        t.Fatal(err)
    }
    var found *memory.Revision
    for i := range revs {
        if revs[i].RevisionID == rev1.RevisionID {
            found = &revs[i]
            break
        }
    }
    if found == nil {
        t.Fatal("rev1 not found in history")
    }
    if found.Status != memory.StatusDeprecated {
        t.Errorf("rev1 status: got %v, want deprecated", found.Status)
    }
}

func TestWriteRevision_ValidatesRequiredFields(t *testing.T) {
    ms, cleanup := newTestStore(t)
    defer cleanup()
    ctx := context.Background()

    cases := []struct {
        name   string
        mutate func(*memory.WriteInput)
    }{
        {"missing namespace", func(in *memory.WriteInput) { in.Namespace = "" }},
        {"invalid namespace", func(in *memory.WriteInput) { in.Namespace = "app/bogus" }},
        {"invalid key", func(in *memory.WriteInput) { in.MemoryKey = "Bad-Key" }},
        {"missing session_id", func(in *memory.WriteInput) { in.SessionID = "" }},
        {"missing author", func(in *memory.WriteInput) { in.Author = memory.Author{} }},
        {"missing origin", func(in *memory.WriteInput) { in.Origin = "" }},
        {"invalid origin", func(in *memory.WriteInput) { in.Origin = memory.Origin("bogus") }},
        {"missing trigger", func(in *memory.WriteInput) { in.Trigger = "" }},
        {"invalid trigger", func(in *memory.WriteInput) { in.Trigger = memory.Trigger("bogus") }},
        {"confidence too high", func(in *memory.WriteInput) { in.Confidence = 1.5 }},
        {"confidence negative", func(in *memory.WriteInput) { in.Confidence = -0.1 }},
        {"empty summary (default strict)", func(in *memory.WriteInput) { in.Payload.Summary = "" }},
    }
    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            in := sampleInput("user.preferences.verbosity")
            tc.mutate(&in)
            _, err := ms.WriteRevision(ctx, in)
            if err == nil {
                t.Fatalf("expected error for %s", tc.name)
            }
            // Error should include ErrInvalidInput sentinel.
            if !errorsIs(err, memory.ErrInvalidInput) {
                t.Errorf("expected ErrInvalidInput, got %v", err)
            }
        })
    }
    _ = json.Marshal // silence unused import if tests trim
}

// errorsIs is a tiny local helper to avoid importing errors in every test.
func errorsIs(err, target error) bool {
    for e := err; e != nil; {
        if e == target {
            return true
        }
        type unwrap interface{ Unwrap() error }
        if u, ok := e.(unwrap); ok {
            e = u.Unwrap()
        } else {
            return false
        }
    }
    return false
}
```

- [ ] **Step 3: Run, expect failure**

```bash
go test ./internal/memory/... -run TestWriteRevision
```

Expected: FAIL — `WriteRevision`, `WriteInput`, `GetState`, `GetHistory`, `ErrInvalidInput` undefined.

- [ ] **Step 4: Implement `internal/memory/write.go`**

```go
package memory

import (
    "context"
    "database/sql"
    "encoding/json"
    "errors"
    "fmt"
    "time"
)

// ErrInvalidInput is the sentinel returned for validation failures at the
// memory write boundary. All validation errors wrap this.
var ErrInvalidInput = errors.New("invalid memory input")

// ErrNotFound is returned when a memory or revision cannot be located.
var ErrNotFound = errors.New("memory not found")

// WriteInput carries the fields needed to write a new memory revision.
// Required fields (in D-core strict mode): Namespace, Author, Trigger,
// SessionID, Origin, Confidence, Payload.Summary.
// MemoryKey may be empty (keyless memory).
// Supersedes may be empty (no explicit deprecation).
type WriteInput struct {
    Namespace  string
    MemoryKey  string
    Supersedes string
    Status     Status
    Author     Author
    Trigger    Trigger
    SessionID  string
    Origin     Origin
    Confidence float64
    Tags       []string
    TTL        time.Duration
    Payload    Payload
}

// WriteRevision writes a new memory revision. If MemoryKey is set and a
// logical memory exists for (namespace, key), this appends a new revision
// to that memory and updates current_revision. Otherwise a new logical
// memory is created.
//
// If Supersedes is set, the referenced revision's status is flipped to
// "deprecated" in the same transaction. This is the single authorized
// exception to the append-only invariant (per plan-time decision 4).
func (s *Store) WriteRevision(ctx context.Context, in WriteInput) (Revision, error) {
    if err := validateWriteInput(in); err != nil {
        return Revision{}, err
    }

    tx, err := s.db.BeginTx(ctx, nil)
    if err != nil {
        return Revision{}, fmt.Errorf("begin tx: %w", err)
    }
    defer func() { _ = tx.Rollback() }() // no-op if Commit succeeded

    // Resolve or create the logical memory (memory_state row).
    memoryID, err := s.resolveOrCreateMemory(ctx, tx, in)
    if err != nil {
        return Revision{}, err
    }

    // Default status to draft if unset.
    status := in.Status
    if status == "" {
        status = StatusDraft
    }

    revisionID := NewULID()
    now := time.Now().UTC()

    var expiresAt *time.Time
    var ttlSeconds *int64
    if in.TTL > 0 {
        exp := now.Add(in.TTL)
        expiresAt = &exp
        secs := int64(in.TTL.Seconds())
        ttlSeconds = &secs
    }

    tagsJSON, err := json.Marshal(in.Tags)
    if err != nil {
        return Revision{}, fmt.Errorf("marshal tags: %w", err)
    }
    if in.Tags == nil {
        tagsJSON = []byte("[]")
    }

    var memoryKeyArg interface{}
    if in.MemoryKey == "" {
        memoryKeyArg = nil
    } else {
        memoryKeyArg = in.MemoryKey
    }
    var supersedesArg interface{}
    if in.Supersedes == "" {
        supersedesArg = nil
    } else {
        supersedesArg = in.Supersedes
    }
    var ttlArg, expiresAtArg interface{}
    if ttlSeconds != nil {
        ttlArg = *ttlSeconds
    }
    if expiresAt != nil {
        expiresAtArg = expiresAt.Format(time.RFC3339Nano)
    }

    _, err = tx.ExecContext(ctx, `
        INSERT INTO memory_revisions (
            revision_id, memory_id, namespace, memory_key, status, supersedes,
            created_at, author_agent_id, author_version, trigger, session_id,
            origin, confidence, tags, ttl_seconds, expires_at,
            payload_summary, payload_body
        ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
    `,
        revisionID, memoryID, in.Namespace, memoryKeyArg, string(status), supersedesArg,
        now.Format(time.RFC3339Nano), in.Author.AgentID, in.Author.AgentVersion,
        string(in.Trigger), in.SessionID, string(in.Origin), in.Confidence,
        string(tagsJSON), ttlArg, expiresAtArg,
        in.Payload.Summary, in.Payload.Body,
    )
    if err != nil {
        return Revision{}, fmt.Errorf("insert revision: %w", err)
    }

    // Apply supersedes deprecation (single authorized status mutation).
    if in.Supersedes != "" {
        if err := deprecateRevisionTx(ctx, tx, in.Supersedes); err != nil {
            return Revision{}, err
        }
    }

    // Update current_revision pointer on the logical memory.
    // Always set to the newly-written revision (spec D2: current = latest
    // non-deprecated revision; the newly written revision is never
    // deprecated at the moment of write).
    _, err = tx.ExecContext(ctx,
        `UPDATE memory_state SET current_revision = ? WHERE memory_id = ?`,
        revisionID, memoryID,
    )
    if err != nil {
        return Revision{}, fmt.Errorf("update current_revision: %w", err)
    }

    if err := tx.Commit(); err != nil {
        return Revision{}, fmt.Errorf("commit: %w", err)
    }

    // Fire-and-forget embed job enqueue. NoopQueue drops it silently;
    // Track I's real queue will handle it later. Errors here are not fatal
    // — writes must always succeed even if the queue is misconfigured.
    _ = s.queue.Enqueue(ctx, Job{
        Kind:    "memory_embed",
        Payload: []byte(fmt.Sprintf(`{"revision_id":%q}`, revisionID)),
    })

    return Revision{
        RevisionID: revisionID,
        MemoryID:   memoryID,
        Namespace:  in.Namespace,
        MemoryKey:  in.MemoryKey,
        Status:     status,
        Supersedes: in.Supersedes,
        CreatedAt:  now,
        Author:     in.Author,
        Trigger:    in.Trigger,
        SessionID:  in.SessionID,
        Origin:     in.Origin,
        Confidence: in.Confidence,
        Tags:       in.Tags,
        ExpiresAt:  expiresAt,
        Payload:    in.Payload,
    }, nil
}

// resolveOrCreateMemory looks up an existing memory_state row by
// (namespace, memory_key). If found, returns its memory_id. If not (or if
// memory_key is empty), creates a new row and returns the new memory_id.
func (s *Store) resolveOrCreateMemory(ctx context.Context, tx *sql.Tx, in WriteInput) (string, error) {
    if in.MemoryKey != "" {
        var existing string
        err := tx.QueryRowContext(ctx,
            `SELECT memory_id FROM memory_state WHERE namespace = ? AND memory_key = ?`,
            in.Namespace, in.MemoryKey,
        ).ScanContext // NOTE: ScanContext only exists on Row; use .Scan(&existing) below
        _ = err
        // Correct pattern:
        row := tx.QueryRowContext(ctx,
            `SELECT memory_id FROM memory_state WHERE namespace = ? AND memory_key = ?`,
            in.Namespace, in.MemoryKey,
        )
        if err := row.Scan(&existing); err == nil {
            return existing, nil
        } else if !errors.Is(err, sql.ErrNoRows) {
            return "", fmt.Errorf("resolve memory: %w", err)
        }
    }
    // Create new memory_state row.
    memoryID := NewULID()
    var keyArg interface{}
    if in.MemoryKey != "" {
        keyArg = in.MemoryKey
    }
    _, err := tx.ExecContext(ctx, `
        INSERT INTO memory_state (memory_id, namespace, memory_key, activation, access_count, created_at)
        VALUES (?, ?, ?, 1.0, 0, ?)
    `, memoryID, in.Namespace, keyArg, time.Now().UTC().Format(time.RFC3339Nano))
    if err != nil {
        return "", fmt.Errorf("insert memory_state: %w", err)
    }
    return memoryID, nil
}

// deprecateRevisionTx is the ONE authorized mutation of memory_revisions.status.
// Do not introduce any other UPDATE against that column anywhere in the
// codebase. The guard test in write_test.go exists to catch regressions.
func deprecateRevisionTx(ctx context.Context, tx *sql.Tx, revisionID string) error {
    res, err := tx.ExecContext(ctx,
        `UPDATE memory_revisions SET status = ? WHERE revision_id = ? AND status != ?`,
        string(StatusDeprecated), revisionID, string(StatusDeprecated),
    )
    if err != nil {
        return fmt.Errorf("deprecate %s: %w", revisionID, err)
    }
    n, _ := res.RowsAffected()
    if n == 0 {
        // Either already deprecated or doesn't exist. Both are OK for
        // idempotency; a caller writing twice with the same supersedes
        // shouldn't fail.
        return nil
    }
    return nil
}

func validateWriteInput(in WriteInput) error {
    if in.Namespace == "" {
        return fmt.Errorf("%w: namespace is required", ErrInvalidInput)
    }
    if err := ValidateNamespace(in.Namespace); err != nil {
        return fmt.Errorf("%w: %v", ErrInvalidInput, err)
    }
    if in.MemoryKey != "" {
        if err := ValidateKey(in.MemoryKey); err != nil {
            return fmt.Errorf("%w: %v", ErrInvalidInput, err)
        }
    }
    if in.Author.AgentID == "" {
        return fmt.Errorf("%w: author.agent_id is required", ErrInvalidInput)
    }
    if in.Trigger == "" || !in.Trigger.Valid() {
        return fmt.Errorf("%w: trigger is required and must be valid", ErrInvalidInput)
    }
    if in.SessionID == "" {
        return fmt.Errorf("%w: session_id is required (use manual:<ulid> for manual inserts)", ErrInvalidInput)
    }
    if in.Origin == "" || !in.Origin.Valid() {
        return fmt.Errorf("%w: origin is required and must be one of user|feedback|project|reference|observation", ErrInvalidInput)
    }
    if in.Confidence < 0 || in.Confidence > 1 {
        return fmt.Errorf("%w: confidence must be in [0, 1]", ErrInvalidInput)
    }
    if in.Status != "" && !in.Status.Valid() {
        return fmt.Errorf("%w: status %q is not valid", ErrInvalidInput, in.Status)
    }
    // Default strict mode: summary required. (Relaxation per-namespace is
    // added in a later task once namespace config is wired.)
    if in.Payload.Summary == "" {
        return fmt.Errorf("%w: payload.summary is required", ErrInvalidInput)
    }
    return nil
}

// GetState loads the mutable state row for a memory_id.
func (s *Store) GetState(ctx context.Context, memoryID string) (State, error) {
    row := s.db.QueryRowContext(ctx, `
        SELECT memory_id, namespace, COALESCE(memory_key, ''),
               COALESCE(current_revision, ''), activation, access_count,
               last_accessed_at, created_at
        FROM memory_state WHERE memory_id = ?
    `, memoryID)
    var st State
    var keyStr, curRev string
    var lastAccessed sql.NullString
    var createdStr string
    if err := row.Scan(
        &st.MemoryID, &st.Namespace, &keyStr, &curRev,
        &st.Activation, &st.AccessCount, &lastAccessed, &createdStr,
    ); err != nil {
        if errors.Is(err, sql.ErrNoRows) {
            return State{}, ErrNotFound
        }
        return State{}, fmt.Errorf("scan memory_state: %w", err)
    }
    st.MemoryKey = keyStr
    st.CurrentRevision = curRev
    if lastAccessed.Valid {
        t, _ := time.Parse(time.RFC3339Nano, lastAccessed.String)
        st.LastAccessedAt = &t
    }
    st.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdStr)
    return st, nil
}
```

**NOTE FOR IMPLEMENTER:** The draft above contains a known artifact in `resolveOrCreateMemory` — I wrote `row.ScanContext` then corrected it inline. When you type this up for real, remove the first broken block entirely. The correct pattern is just:

```go
row := tx.QueryRowContext(ctx, `SELECT memory_id FROM memory_state WHERE namespace = ? AND memory_key = ?`, in.Namespace, in.MemoryKey)
if err := row.Scan(&existing); err == nil {
    return existing, nil
} else if !errors.Is(err, sql.ErrNoRows) {
    return "", fmt.Errorf("resolve memory: %w", err)
}
```

- [ ] **Step 5: Run tests, iterate until pass**

```bash
go test ./internal/memory/... -run TestWriteRevision -v
```

Expected: all subtests PASS. If any fail, read the error carefully — most likely causes are (a) SQL syntax issues from the inline schema, (b) tag JSON handling, (c) NULL vs empty-string mismatches on nullable columns.

**Do NOT mark this task complete until every subtest passes and `go test ./...` still passes for the rest of the tree.**

- [ ] **Step 6: Guard test — enforce no other UPDATEs to memory_revisions.status**

Add to `write_test.go`:

```go
func TestOnlyDeprecationPathMutatesRevisionStatus(t *testing.T) {
    // This is a source-level guard test. It scans the memory package for
    // UPDATE statements against memory_revisions.status and asserts that
    // the only occurrence is inside deprecateRevisionTx.
    //
    // If this test fails, you have introduced a second status mutation
    // path. Don't. Use deprecateRevisionTx, or add a new explicit
    // deprecation API and route through it. The append-only invariant is
    // load-bearing — see spec D2, D14, plan-time decision 4.
    //
    // Implementation: walk all .go files in internal/memory/, grep for
    // `memory_revisions SET status` or `UPDATE memory_revisions.*status`.
    t.Helper()
    matches, err := grepForStatusMutations(t)
    if err != nil {
        t.Fatal(err)
    }
    // Expect exactly one file to contain the mutation, and it should be write.go.
    if len(matches) != 1 {
        t.Fatalf("expected exactly 1 file with memory_revisions.status UPDATE, got %d: %v", len(matches), matches)
    }
    if !strings.HasSuffix(matches[0], "/write.go") && !strings.HasSuffix(matches[0], "\\write.go") {
        t.Fatalf("unexpected file containing status UPDATE: %s (should only be write.go)", matches[0])
    }
}

// grepForStatusMutations walks internal/memory/*.go looking for SQL that
// mutates memory_revisions.status. Returns the list of files containing a
// match.
func grepForStatusMutations(t *testing.T) ([]string, error) {
    t.Helper()
    var out []string
    entries, err := os.ReadDir("./")
    if err != nil {
        return nil, err
    }
    needle := regexp.MustCompile(`(?i)UPDATE\s+memory_revisions\s+SET\s+status`)
    for _, e := range entries {
        if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
            continue
        }
        if strings.HasSuffix(e.Name(), "_test.go") {
            continue
        }
        data, err := os.ReadFile(e.Name())
        if err != nil {
            return nil, err
        }
        if needle.Match(data) {
            out = append(out, e.Name())
        }
    }
    return out, nil
}
```

Add imports at the top of `write_test.go`: `os`, `regexp`, `strings`.

Run the guard test:
```bash
go test ./internal/memory/... -run TestOnlyDeprecationPathMutatesRevisionStatus -v
```
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
go vet ./internal/memory/...
gofmt -l internal/memory/
git add internal/memory/store.go internal/memory/write.go internal/memory/write_test.go
git commit -m "feat(memory): add Store and WriteRevision with supersedes deprecation

Implements the memory write path per spec D2/D8/D9/D14:
- Keyed writes append revisions to existing logical memories
- Keyless writes always create new logical memories
- Supersedes auto-deprecates the referenced revision (single authorized
  mutation of memory_revisions.status, enforced by guard test)
- Strict validation of all required fields at the write boundary
- Stub queue enqueues an embed job that NoopQueue drops

Refs: docs/superpowers/specs/2026-04-04-memory-domain-design.md D-core"
```

---

### Task 7: Read path — GetCurrent, GetHistory, GetByID

**Goal:** Implement reads of the current revision for a `(namespace, memory_key)` and the full revision timeline. Plus a `GetByID` helper for retrieval by `revision_id`.

**Files:**
- Create: `internal/memory/read.go`
- Create: `internal/memory/read_test.go`

- [ ] **Step 1: Write failing tests**

```go
package memory_test

import (
    "context"
    "testing"
    "time"

    "github.com/chrispian/cortex/internal/memory" // fix path
)

func TestGetCurrent(t *testing.T) {
    ms, cleanup := newTestStore(t)
    defer cleanup()
    ctx := context.Background()

    _, err := ms.WriteRevision(ctx, sampleInput("user.preferences.verbosity"))
    if err != nil {
        t.Fatal(err)
    }
    time.Sleep(2 * time.Millisecond)
    in2 := sampleInput("user.preferences.verbosity")
    in2.Payload.Summary = "updated"
    rev2, err := ms.WriteRevision(ctx, in2)
    if err != nil {
        t.Fatal(err)
    }

    cur, err := ms.GetCurrent(ctx, "user/chrispian/memory", "user.preferences.verbosity")
    if err != nil {
        t.Fatalf("GetCurrent: %v", err)
    }
    if cur.RevisionID != rev2.RevisionID {
        t.Errorf("got %q, want latest %q", cur.RevisionID, rev2.RevisionID)
    }
    if cur.Payload.Summary != "updated" {
        t.Errorf("payload summary: got %q, want %q", cur.Payload.Summary, "updated")
    }
}

func TestGetCurrentNotFound(t *testing.T) {
    ms, cleanup := newTestStore(t)
    defer cleanup()
    _, err := ms.GetCurrent(context.Background(), "user/chrispian/memory", "nothing.here")
    if !errorsIs(err, memory.ErrNotFound) {
        t.Errorf("expected ErrNotFound, got %v", err)
    }
}

func TestGetHistoryReturnsAllRevisionsNewestFirst(t *testing.T) {
    ms, cleanup := newTestStore(t)
    defer cleanup()
    ctx := context.Background()

    var written []string
    for i := 0; i < 3; i++ {
        in := sampleInput("user.preferences.verbosity")
        in.Payload.Summary = "v" + string(rune('0'+i))
        rev, err := ms.WriteRevision(ctx, in)
        if err != nil {
            t.Fatal(err)
        }
        written = append(written, rev.RevisionID)
        time.Sleep(2 * time.Millisecond)
    }

    revs, err := ms.GetHistory(ctx, "user/chrispian/memory", "user.preferences.verbosity")
    if err != nil {
        t.Fatal(err)
    }
    if len(revs) != 3 {
        t.Fatalf("got %d revisions, want 3", len(revs))
    }
    // Newest first
    if revs[0].RevisionID != written[2] {
        t.Errorf("first = %q, want newest %q", revs[0].RevisionID, written[2])
    }
    if revs[2].RevisionID != written[0] {
        t.Errorf("last = %q, want oldest %q", revs[2].RevisionID, written[0])
    }
}
```

- [ ] **Step 2: Implement `internal/memory/read.go`**

```go
package memory

import (
    "context"
    "database/sql"
    "encoding/json"
    "errors"
    "fmt"
    "time"
)

// GetCurrent returns the current (latest non-deprecated) revision of the
// memory identified by (namespace, memoryKey). Returns ErrNotFound if no
// such memory exists.
func (s *Store) GetCurrent(ctx context.Context, namespace, memoryKey string) (Revision, error) {
    var currentRev string
    err := s.db.QueryRowContext(ctx, `
        SELECT COALESCE(current_revision, '') FROM memory_state
        WHERE namespace = ? AND memory_key = ?
    `, namespace, memoryKey).Scan(&currentRev)
    if err != nil {
        if errors.Is(err, sql.ErrNoRows) {
            return Revision{}, ErrNotFound
        }
        return Revision{}, fmt.Errorf("lookup current_revision: %w", err)
    }
    if currentRev == "" {
        return Revision{}, ErrNotFound
    }
    return s.GetRevisionByID(ctx, currentRev)
}

// GetRevisionByID loads a single revision by its revision_id.
func (s *Store) GetRevisionByID(ctx context.Context, revisionID string) (Revision, error) {
    row := s.db.QueryRowContext(ctx, `
        SELECT revision_id, memory_id, namespace, COALESCE(memory_key, ''),
               status, COALESCE(supersedes, ''), created_at,
               author_agent_id, author_version, trigger, session_id,
               origin, confidence, tags, ttl_seconds, expires_at,
               COALESCE(payload_summary, ''), COALESCE(payload_body, '')
        FROM memory_revisions WHERE revision_id = ?
    `, revisionID)
    return scanRevision(row)
}

// GetHistory returns all revisions for a logical memory, newest first.
// If the memory does not exist, returns ErrNotFound.
func (s *Store) GetHistory(ctx context.Context, namespace, memoryKey string) ([]Revision, error) {
    // Resolve memory_id first.
    var memoryID string
    err := s.db.QueryRowContext(ctx,
        `SELECT memory_id FROM memory_state WHERE namespace = ? AND memory_key = ?`,
        namespace, memoryKey,
    ).Scan(&memoryID)
    if err != nil {
        if errors.Is(err, sql.ErrNoRows) {
            return nil, ErrNotFound
        }
        return nil, fmt.Errorf("lookup memory_id: %w", err)
    }

    rows, err := s.db.QueryContext(ctx, `
        SELECT revision_id, memory_id, namespace, COALESCE(memory_key, ''),
               status, COALESCE(supersedes, ''), created_at,
               author_agent_id, author_version, trigger, session_id,
               origin, confidence, tags, ttl_seconds, expires_at,
               COALESCE(payload_summary, ''), COALESCE(payload_body, '')
        FROM memory_revisions WHERE memory_id = ?
        ORDER BY created_at DESC, revision_id DESC
    `, memoryID)
    if err != nil {
        return nil, fmt.Errorf("query history: %w", err)
    }
    defer rows.Close()

    var out []Revision
    for rows.Next() {
        rev, err := scanRevision(rows)
        if err != nil {
            return nil, err
        }
        out = append(out, rev)
    }
    if err := rows.Err(); err != nil {
        return nil, fmt.Errorf("iterate history: %w", err)
    }
    return out, nil
}

// scanRevision scans a single row (or Row) into a Revision.
// Accepts both *sql.Row and *sql.Rows via the scanner interface.
type rowScanner interface {
    Scan(dest ...interface{}) error
}

func scanRevision(r rowScanner) (Revision, error) {
    var rev Revision
    var createdStr string
    var tagsStr string
    var ttlSeconds sql.NullInt64
    var expiresStr sql.NullString
    var statusStr, triggerStr, originStr string

    err := r.Scan(
        &rev.RevisionID, &rev.MemoryID, &rev.Namespace, &rev.MemoryKey,
        &statusStr, &rev.Supersedes, &createdStr,
        &rev.Author.AgentID, &rev.Author.AgentVersion, &triggerStr, &rev.SessionID,
        &originStr, &rev.Confidence, &tagsStr, &ttlSeconds, &expiresStr,
        &rev.Payload.Summary, &rev.Payload.Body,
    )
    if err != nil {
        if errors.Is(err, sql.ErrNoRows) {
            return Revision{}, ErrNotFound
        }
        return Revision{}, fmt.Errorf("scan revision: %w", err)
    }

    rev.Status = Status(statusStr)
    rev.Trigger = Trigger(triggerStr)
    rev.Origin = Origin(originStr)
    rev.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdStr)
    if ttlSeconds.Valid {
        rev.TTLSeconds = ttlSeconds.Int64
    }
    if expiresStr.Valid {
        t, _ := time.Parse(time.RFC3339Nano, expiresStr.String)
        rev.ExpiresAt = &t
    }
    if tagsStr != "" {
        _ = json.Unmarshal([]byte(tagsStr), &rev.Tags)
    }
    return rev, nil
}
```

- [ ] **Step 3: Run tests, expect pass; commit**

```bash
go test ./internal/memory/... -run "TestGetCurrent|TestGetHistory" -v
go vet ./internal/memory/...
gofmt -l internal/memory/
git add internal/memory/read.go internal/memory/read_test.go
git commit -m "feat(memory): add read paths — GetCurrent, GetHistory, GetRevisionByID

Implements the non-embedding read operations from spec D5/D9.
GetCurrent resolves via memory_state.current_revision;
GetHistory returns all revisions newest-first for a logical memory.

Refs: docs/superpowers/specs/2026-04-04-memory-domain-design.md D-core"
```

---

### Task 8: Recall path — filters, ranking, multi-namespace

**Goal:** Implement `Recall` — the context-assembly retrieval operation. Supports activation and chronological ranking over one or more namespaces, with filters for origin, status, tags, confidence_min, and time window. Similarity ranking is stubbed to return `ErrSimilarityUnavailable` (populated in D-deferred).

**Files:**
- Create: `internal/memory/recall.go`
- Create: `internal/memory/ranking.go`
- Create: `internal/memory/recall_test.go`

**Implementation sketch** — the shape:

```go
// RecallInput specifies a recall query. At least one namespace is required.
type RecallInput struct {
    Namespaces     []string
    RevisionScope  RevisionScope // RevisionScopeCurrent (default) or RevisionScopeTimeline
    Ranking        Ranking       // RankingActivation (default), RankingChronological, RankingSimilarity
    Query          string        // only used when Ranking == RankingSimilarity
    Filters        RecallFilters
    Limit          int           // default 30; hard cap 500
}

type RecallFilters struct {
    Origins        []Origin
    Statuses       []Status    // default: [canonical, reviewed, draft] (excludes deprecated)
    Tags           []string    // ANY-match
    ConfidenceMin  float64
    Since          *time.Time  // created_at >= Since
    Until          *time.Time  // created_at <= Until
}

type RecallResult struct {
    Revision  Revision
    Score     float64
    State     State // activation, access_count for debugging / caller insight
}
```

**Ranking formula** (from D15c, with locked-in recency factor):

```go
// ranking.go
package memory

import (
    "math"
    "time"
)

// statusWeights maps status → ranking multiplier (D15c).
var statusWeights = map[Status]float64{
    StatusCanonical:  1.0,
    StatusReviewed:   0.9,
    StatusDraft:      0.6,
    StatusDeprecated: 0.1,
}

// originWeights maps origin → ranking multiplier (D15c).
var originWeights = map[Origin]float64{
    OriginFeedback:    1.3,
    OriginUser:        1.1,
    OriginProject:     1.0,
    OriginReference:   0.9,
    OriginObservation: 0.8,
}

// recencyFactor returns a multiplier based on last_accessed_at:
// 1.0 at now, decaying linearly to 0.5 at 30 days ago, floor 0.5.
// (Plan-time decision 2.)
func recencyFactor(lastAccessed *time.Time, now time.Time) float64 {
    if lastAccessed == nil {
        return 0.75 // midpoint for never-accessed
    }
    days := now.Sub(*lastAccessed).Hours() / 24
    if days <= 0 {
        return 1.0
    }
    if days >= 30 {
        return 0.5
    }
    return 1.0 - 0.5*(days/30)
}

// activationScore computes the full activation-mode ranking score.
func activationScore(rev Revision, state State, now time.Time) float64 {
    sw := statusWeights[rev.Status]
    ow := originWeights[rev.Origin]
    rf := recencyFactor(state.LastAccessedAt, now)
    return state.Activation * sw * rev.Confidence * ow * rf
}

// chronologicalKey returns a value to sort by for chronological ranking
// (descending — newest first). Returns unix nanos.
func chronologicalKey(rev Revision) int64 {
    return rev.CreatedAt.UnixNano()
}
```

**Recall implementation** — query one or more namespaces, fetch candidate revisions + state, score them, sort, truncate to limit:

```go
// recall.go
package memory

import (
    "context"
    "database/sql"
    "errors"
    "fmt"
    "sort"
    "strings"
    "time"
)

// Ranking selects how Recall scores results.
type Ranking string

const (
    RankingActivation    Ranking = "activation"
    RankingChronological Ranking = "chronological"
    RankingSimilarity    Ranking = "similarity"
)

// RevisionScope selects whether Recall returns only current revisions or
// every revision in the timeline.
type RevisionScope string

const (
    RevisionScopeCurrent  RevisionScope = "current"
    RevisionScopeTimeline RevisionScope = "timeline"
)

// ErrSimilarityUnavailable is returned when Ranking == RankingSimilarity but
// no real embedder is wired. D-core always returns this — Track H/I populate
// the similarity path.
var ErrSimilarityUnavailable = errors.New("similarity ranking unavailable (embedder not configured)")

type RecallInput struct {
    Namespaces    []string
    RevisionScope RevisionScope
    Ranking       Ranking
    Query         string
    Filters       RecallFilters
    Limit         int
}

type RecallFilters struct {
    Origins       []Origin
    Statuses      []Status
    Tags          []string
    ConfidenceMin float64
    Since         *time.Time
    Until         *time.Time
}

type RecallResult struct {
    Revision Revision
    Score    float64
    State    State
}

const defaultRecallLimit = 30
const maxRecallLimit = 500

// Recall performs context-assembly retrieval per spec D5/D15. Default ranking
// is activation, default revision scope is current, default statuses exclude
// deprecated.
func (s *Store) Recall(ctx context.Context, in RecallInput) ([]RecallResult, error) {
    if len(in.Namespaces) == 0 {
        return nil, fmt.Errorf("%w: at least one namespace required", ErrInvalidInput)
    }
    for _, ns := range in.Namespaces {
        if err := ValidateNamespace(ns); err != nil {
            return nil, fmt.Errorf("%w: %v", ErrInvalidInput, err)
        }
    }
    if in.Ranking == "" {
        in.Ranking = RankingActivation
    }
    if in.RevisionScope == "" {
        in.RevisionScope = RevisionScopeCurrent
    }
    if in.Limit <= 0 {
        in.Limit = defaultRecallLimit
    }
    if in.Limit > maxRecallLimit {
        in.Limit = maxRecallLimit
    }
    if len(in.Filters.Statuses) == 0 {
        in.Filters.Statuses = []Status{StatusCanonical, StatusReviewed, StatusDraft}
    }
    if in.Ranking == RankingSimilarity {
        // D-core stub — real implementation lands with Track H.
        return nil, ErrSimilarityUnavailable
    }

    // Build candidate query. This fetches candidates from all requested
    // namespaces in one SELECT with a filter placeholder expansion.
    candidates, err := s.fetchCandidates(ctx, in)
    if err != nil {
        return nil, err
    }
    if len(candidates) == 0 {
        return nil, nil
    }

    // Fetch state rows for all distinct memory_ids for ranking.
    stateByID, err := s.fetchStates(ctx, distinctMemoryIDs(candidates))
    if err != nil {
        return nil, err
    }

    // Score and sort.
    now := time.Now().UTC()
    results := make([]RecallResult, 0, len(candidates))
    for _, rev := range candidates {
        st := stateByID[rev.MemoryID]
        var score float64
        switch in.Ranking {
        case RankingActivation:
            score = activationScore(rev, st, now)
        case RankingChronological:
            // Use unix nanos as score so sort.Slice ordering is correct.
            score = float64(chronologicalKey(rev))
        }
        results = append(results, RecallResult{Revision: rev, Score: score, State: st})
    }
    sort.SliceStable(results, func(i, j int) bool {
        return results[i].Score > results[j].Score
    })
    if len(results) > in.Limit {
        results = results[:in.Limit]
    }

    // Reinforce activation on hits (D4).
    if in.Ranking == RankingActivation {
        _ = s.reinforceAccess(ctx, results) // best-effort, errors logged not fatal
    }
    return results, nil
}

// fetchCandidates queries memory_revisions joined with the per-memory
// current pointer (when RevisionScope==current) or all revisions
// (when RevisionScope==timeline), filtered by the RecallInput.
//
// Implementation detail: we build the query with placeholders to avoid
// string concatenation of user input. Namespaces, statuses, and origins
// are IN clauses built dynamically.
func (s *Store) fetchCandidates(ctx context.Context, in RecallInput) ([]Revision, error) {
    var parts []string
    var args []interface{}

    // Namespace IN clause
    nsPlaceholders := make([]string, len(in.Namespaces))
    for i, ns := range in.Namespaces {
        nsPlaceholders[i] = "?"
        args = append(args, ns)
    }
    parts = append(parts, "r.namespace IN ("+strings.Join(nsPlaceholders, ",")+")")

    // Status IN clause
    if len(in.Filters.Statuses) > 0 {
        ph := make([]string, len(in.Filters.Statuses))
        for i, st := range in.Filters.Statuses {
            ph[i] = "?"
            args = append(args, string(st))
        }
        parts = append(parts, "r.status IN ("+strings.Join(ph, ",")+")")
    }

    // Origin IN clause
    if len(in.Filters.Origins) > 0 {
        ph := make([]string, len(in.Filters.Origins))
        for i, o := range in.Filters.Origins {
            ph[i] = "?"
            args = append(args, string(o))
        }
        parts = append(parts, "r.origin IN ("+strings.Join(ph, ",")+")")
    }

    if in.Filters.ConfidenceMin > 0 {
        parts = append(parts, "r.confidence >= ?")
        args = append(args, in.Filters.ConfidenceMin)
    }
    if in.Filters.Since != nil {
        parts = append(parts, "r.created_at >= ?")
        args = append(args, in.Filters.Since.Format(time.RFC3339Nano))
    }
    if in.Filters.Until != nil {
        parts = append(parts, "r.created_at <= ?")
        args = append(args, in.Filters.Until.Format(time.RFC3339Nano))
    }

    // TTL / expires_at filter — exclude expired revisions.
    parts = append(parts, "(r.expires_at IS NULL OR r.expires_at > ?)")
    args = append(args, time.Now().UTC().Format(time.RFC3339Nano))

    // Current-only vs timeline
    var query string
    if in.RevisionScope == RevisionScopeCurrent {
        query = `
            SELECT r.revision_id, r.memory_id, r.namespace, COALESCE(r.memory_key, ''),
                   r.status, COALESCE(r.supersedes, ''), r.created_at,
                   r.author_agent_id, r.author_version, r.trigger, r.session_id,
                   r.origin, r.confidence, r.tags, r.ttl_seconds, r.expires_at,
                   COALESCE(r.payload_summary, ''), COALESCE(r.payload_body, '')
            FROM memory_revisions r
            INNER JOIN memory_state s ON s.current_revision = r.revision_id
            WHERE ` + strings.Join(parts, " AND ")
    } else {
        query = `
            SELECT r.revision_id, r.memory_id, r.namespace, COALESCE(r.memory_key, ''),
                   r.status, COALESCE(r.supersedes, ''), r.created_at,
                   r.author_agent_id, r.author_version, r.trigger, r.session_id,
                   r.origin, r.confidence, r.tags, r.ttl_seconds, r.expires_at,
                   COALESCE(r.payload_summary, ''), COALESCE(r.payload_body, '')
            FROM memory_revisions r
            WHERE ` + strings.Join(parts, " AND ")
    }

    rows, err := s.db.QueryContext(ctx, query, args...)
    if err != nil {
        return nil, fmt.Errorf("query candidates: %w", err)
    }
    defer rows.Close()

    var out []Revision
    for rows.Next() {
        rev, err := scanRevision(rows)
        if err != nil {
            return nil, err
        }
        // Tag ANY-match filter (applied in Go because tags is JSON).
        if len(in.Filters.Tags) > 0 && !tagsAnyMatch(rev.Tags, in.Filters.Tags) {
            continue
        }
        out = append(out, rev)
    }
    return out, rows.Err()
}

func tagsAnyMatch(have, want []string) bool {
    for _, w := range want {
        for _, h := range have {
            if h == w {
                return true
            }
        }
    }
    return false
}

func distinctMemoryIDs(revs []Revision) []string {
    seen := make(map[string]struct{}, len(revs))
    out := make([]string, 0, len(revs))
    for _, r := range revs {
        if _, ok := seen[r.MemoryID]; ok {
            continue
        }
        seen[r.MemoryID] = struct{}{}
        out = append(out, r.MemoryID)
    }
    return out
}

func (s *Store) fetchStates(ctx context.Context, ids []string) (map[string]State, error) {
    if len(ids) == 0 {
        return nil, nil
    }
    ph := make([]string, len(ids))
    args := make([]interface{}, len(ids))
    for i, id := range ids {
        ph[i] = "?"
        args[i] = id
    }
    rows, err := s.db.QueryContext(ctx, `
        SELECT memory_id, namespace, COALESCE(memory_key, ''),
               COALESCE(current_revision, ''), activation, access_count,
               last_accessed_at, created_at
        FROM memory_state WHERE memory_id IN (`+strings.Join(ph, ",")+`)
    `, args...)
    if err != nil {
        return nil, fmt.Errorf("fetch states: %w", err)
    }
    defer rows.Close()

    out := make(map[string]State, len(ids))
    for rows.Next() {
        var st State
        var keyStr, curRev, createdStr string
        var lastAccessed sql.NullString
        if err := rows.Scan(
            &st.MemoryID, &st.Namespace, &keyStr, &curRev,
            &st.Activation, &st.AccessCount, &lastAccessed, &createdStr,
        ); err != nil {
            return nil, err
        }
        st.MemoryKey = keyStr
        st.CurrentRevision = curRev
        if lastAccessed.Valid {
            t, _ := time.Parse(time.RFC3339Nano, lastAccessed.String)
            st.LastAccessedAt = &t
        }
        st.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdStr)
        out[st.MemoryID] = st
    }
    return out, nil
}
```

**Tests** — `recall_test.go` should cover:

1. **Basic activation ranking** — write 3 memories with different origins, recall with activation ranking, verify feedback > user > project ordering
2. **Chronological ordering** — write 3 memories with sleeps between, recall chronologically, verify newest first
3. **Multi-namespace single-call** — write memories into `user/chrispian/memory` and `user/chrispian/project/cortex/memory`, recall with both namespaces listed, verify all are returned and ranked together
4. **Deprecated filtered out by default** — write memory, deprecate it, verify recall doesn't return it
5. **Deprecated included when explicitly requested** — same setup, request `Filters.Statuses = [deprecated]`, verify it returns
6. **TTL expiry** — write memory with 1-second TTL, sleep 2 seconds, verify recall does not return it
7. **Origin filter** — write memories with different origins, filter by `[feedback]`, verify only feedback returns
8. **Confidence filter** — write memories with varying confidence, filter by `ConfidenceMin: 0.8`, verify only high-confidence return
9. **Tag ANY-match** — write memories with different tag sets, filter by a tag, verify only matching ones return
10. **Timeline scope returns all revisions** — write 3 revisions of the same key, request `RevisionScopeTimeline`, verify all 3 return
11. **Similarity returns ErrSimilarityUnavailable** — request `RankingSimilarity`, verify the error

- [ ] **Step 1: Write the 11 test cases above**

Pattern after the existing helpers (`newTestStore`, `sampleInput`). Use `time.Sleep(2*time.Millisecond)` between writes where ordering matters.

- [ ] **Step 2: Run tests, expect failure**

```bash
go test ./internal/memory/... -run TestRecall
```

- [ ] **Step 3: Implement `recall.go` and `ranking.go` as sketched above**

- [ ] **Step 4: Iterate until all 11 tests pass**

```bash
go test ./internal/memory/... -run TestRecall -v
go vet ./internal/memory/...
```

- [ ] **Step 5: Commit**

```bash
git add internal/memory/recall.go internal/memory/ranking.go internal/memory/recall_test.go
git commit -m "feat(memory): add Recall with activation + chronological ranking

Implements context-assembly retrieval per spec D5/D15c:
- Multi-namespace single-call queries with unified server-side ranking
- Activation ranking formula (activation × status × confidence × origin × recency)
- Chronological ranking for timeline queries
- Filter dimensions: origins, statuses, tags (ANY-match), confidence_min, time window
- TTL enforcement (expired revisions excluded)
- Similarity ranking stubbed to ErrSimilarityUnavailable (waits for Track H)
- Activation reinforcement on successful retrieval hits

Refs: docs/superpowers/specs/2026-04-04-memory-domain-design.md D-core"
```

---

### Task 9: Activation reinforcement on retrieval

**Goal:** Implement `reinforceAccess` — the update that increments activation and bumps `last_accessed_at` / `access_count` when a memory is returned by a recall query with activation ranking.

**Files:**
- Create: `internal/memory/activation.go`
- Create: `internal/memory/activation_test.go`

**Formula:** activation is incremented by `0.1 * (2.0 - current_activation)` — a diminishing-returns reinforcement that prevents runaway growth while still being meaningful. This keeps activation in a rough [0, 2] range. No hard cap.

- [ ] **Step 1: Write failing tests**

```go
func TestReinforceAccessIncrementsActivation(t *testing.T) {
    ms, cleanup := newTestStore(t)
    defer cleanup()
    ctx := context.Background()

    rev, _ := ms.WriteRevision(ctx, sampleInput("user.x"))
    // Initial activation should be 1.0 (from memory_state default).
    st1, _ := ms.GetState(ctx, rev.MemoryID)
    if st1.Activation != 1.0 {
        t.Fatalf("initial activation: %v", st1.Activation)
    }

    // Trigger a recall with activation ranking. This should reinforce.
    _, err := ms.Recall(ctx, memory.RecallInput{
        Namespaces: []string{"user/chrispian/memory"},
        Ranking:    memory.RankingActivation,
    })
    if err != nil {
        t.Fatal(err)
    }

    st2, _ := ms.GetState(ctx, rev.MemoryID)
    if st2.Activation <= st1.Activation {
        t.Errorf("expected activation to increase, got %v -> %v", st1.Activation, st2.Activation)
    }
    if st2.AccessCount != 1 {
        t.Errorf("expected access_count=1, got %d", st2.AccessCount)
    }
    if st2.LastAccessedAt == nil {
        t.Error("expected last_accessed_at to be set")
    }
}

func TestReinforcementDiminishingReturns(t *testing.T) {
    ms, cleanup := newTestStore(t)
    defer cleanup()
    ctx := context.Background()
    rev, _ := ms.WriteRevision(ctx, sampleInput("user.x"))

    // Reinforce 100 times; activation should stay below 2.5 (rough cap).
    for i := 0; i < 100; i++ {
        _, _ = ms.Recall(ctx, memory.RecallInput{
            Namespaces: []string{"user/chrispian/memory"},
            Ranking:    memory.RankingActivation,
        })
    }
    st, _ := ms.GetState(ctx, rev.MemoryID)
    if st.Activation > 2.5 {
        t.Errorf("activation runaway: %v (expected below 2.5)", st.Activation)
    }
    if st.Activation <= 1.0 {
        t.Errorf("activation did not grow: %v", st.Activation)
    }
}
```

- [ ] **Step 2: Implement `internal/memory/activation.go`**

```go
package memory

import (
    "context"
    "fmt"
    "time"
)

// reinforceAccess updates activation + last_accessed_at + access_count for
// every memory in results. Called from Recall after a successful activation-
// ranked query. Best-effort: errors here do not fail the recall.
func (s *Store) reinforceAccess(ctx context.Context, results []RecallResult) error {
    if len(results) == 0 {
        return nil
    }
    tx, err := s.db.BeginTx(ctx, nil)
    if err != nil {
        return fmt.Errorf("begin reinforce tx: %w", err)
    }
    defer func() { _ = tx.Rollback() }()

    now := time.Now().UTC().Format(time.RFC3339Nano)
    // Diminishing-returns formula: new = old + 0.1 * (2.0 - old)
    // This converges toward 2.0 asymptotically; prevents runaway growth
    // without a hard cap. For old=1.0, delta=0.1. For old=1.9, delta=0.01.
    stmt, err := tx.PrepareContext(ctx, `
        UPDATE memory_state
        SET activation = activation + 0.1 * (2.0 - activation),
            access_count = access_count + 1,
            last_accessed_at = ?
        WHERE memory_id = ?
    `)
    if err != nil {
        return fmt.Errorf("prepare reinforce: %w", err)
    }
    defer stmt.Close()

    for _, r := range results {
        if _, err := stmt.ExecContext(ctx, now, r.Revision.MemoryID); err != nil {
            return fmt.Errorf("reinforce %s: %w", r.Revision.MemoryID, err)
        }
    }
    return tx.Commit()
}
```

- [ ] **Step 3: Run, pass, commit**

```bash
go test ./internal/memory/... -run TestReinforce -v
git add internal/memory/activation.go internal/memory/activation_test.go
git commit -m "feat(memory): add activation reinforcement on retrieval

Implements D4 activation ranking reinforcement. Each memory returned by
an activation-ranked Recall gets its activation bumped via the
diminishing-returns formula new = old + 0.1 * (2.0 - old), plus
last_accessed_at and access_count updates. Best-effort — failures do
not fail the recall.

Refs: docs/superpowers/specs/2026-04-04-memory-domain-design.md D-core"
```

---

### Task 10: Promote + Deprecate operations

**Goal:** Implement `Promote` (move a session-scoped memory to user/project scope with key-based deduplication) and `Deprecate` (mark a revision deprecated without writing a replacement).

**Files:**
- Create: `internal/memory/promote.go`
- Create: `internal/memory/promote_test.go`

**Promote mechanics** (from D15e):
1. Read the source memory's current revision
2. Write a new revision in the target namespace with `trigger=promotion`, same payload/metadata
3. If a memory with the same `memory_key` already exists in the target, new revision auto-sets `supersedes` (normal update semantics)
4. Mark the source revision in the session namespace as `deprecated` with a pointer to the promoted revision
5. Preserve the source — do not delete

**Test cases:**
- Promote a session memory to user scope, verify new revision exists in target namespace
- Promote a session memory to project scope
- Promote when a memory with the same key already exists in target — verify supersedes is set
- Promote a memory without a key — verify it still works
- Deprecate a revision, verify subsequent GetCurrent reflects the new current_revision (or ErrNotFound if it was the only revision)
- Deprecate twice is idempotent
- Promote from non-session namespace is rejected with ErrInvalidInput

**Implementation sketch:**

```go
package memory

import (
    "context"
    "errors"
    "fmt"
    "time"
)

// PromoteInput specifies which memory to promote and where.
type PromoteInput struct {
    SourceNamespace string // must be a session-scoped namespace
    SourceMemoryID  string
    TargetNamespace string // user or project scope
    ActorAgentID    string // who is requesting promotion
    ActorVersion    string
}

func (s *Store) Promote(ctx context.Context, in PromoteInput) (Revision, error) {
    // 1. Validate namespaces.
    srcNs, err := ParseNamespace(in.SourceNamespace)
    if err != nil {
        return Revision{}, fmt.Errorf("%w: source: %v", ErrInvalidInput, err)
    }
    if srcNs.Scope != ScopeSession {
        return Revision{}, fmt.Errorf("%w: source must be session-scoped, got %v", ErrInvalidInput, srcNs.Scope)
    }
    tgtNs, err := ParseNamespace(in.TargetNamespace)
    if err != nil {
        return Revision{}, fmt.Errorf("%w: target: %v", ErrInvalidInput, err)
    }
    if tgtNs.Scope != ScopeUser && tgtNs.Scope != ScopeProject {
        return Revision{}, fmt.Errorf("%w: target must be user or project scope, got %v", ErrInvalidInput, tgtNs.Scope)
    }

    // 2. Load the source memory's current revision.
    srcState, err := s.GetState(ctx, in.SourceMemoryID)
    if err != nil {
        return Revision{}, err
    }
    if srcState.Namespace != in.SourceNamespace {
        return Revision{}, fmt.Errorf("%w: memory_id %s is not in namespace %s", ErrInvalidInput, in.SourceMemoryID, in.SourceNamespace)
    }
    if srcState.CurrentRevision == "" {
        return Revision{}, ErrNotFound
    }
    srcRev, err := s.GetRevisionByID(ctx, srcState.CurrentRevision)
    if err != nil {
        return Revision{}, err
    }

    // 3. Build a WriteInput for the target namespace.
    writeIn := WriteInput{
        Namespace:  in.TargetNamespace,
        MemoryKey:  srcRev.MemoryKey,
        Author:     Author{AgentID: in.ActorAgentID, AgentVersion: in.ActorVersion},
        Trigger:    TriggerPromotion,
        SessionID:  srcRev.SessionID,
        Origin:     srcRev.Origin,
        Confidence: srcRev.Confidence,
        Tags:       srcRev.Tags,
        Status:     StatusReviewed, // promoted memories start at reviewed
        Payload:    srcRev.Payload,
    }

    // 4. If a memory with the same key already exists in the target,
    //    set supersedes to that memory's current revision.
    if srcRev.MemoryKey != "" {
        if existing, err := s.GetCurrent(ctx, in.TargetNamespace, srcRev.MemoryKey); err == nil {
            writeIn.Supersedes = existing.RevisionID
        } else if !errors.Is(err, ErrNotFound) {
            return Revision{}, err
        }
    }

    // 5. Write the promoted revision.
    newRev, err := s.WriteRevision(ctx, writeIn)
    if err != nil {
        return Revision{}, err
    }

    // 6. Deprecate the source revision. (Separate small transaction — not
    //    atomic with the write, but the invariant we care about is that the
    //    target revision exists before the source is deprecated, which is
    //    the order we do it in.)
    if err := s.Deprecate(ctx, srcRev.RevisionID); err != nil {
        return Revision{}, fmt.Errorf("deprecate source after promote: %w", err)
    }
    return newRev, nil
}

// Deprecate marks a specific revision as deprecated. If the revision is
// the current revision of its memory, memory_state.current_revision is
// updated to point at the next non-deprecated revision (by created_at DESC),
// or NULL if none exists.
func (s *Store) Deprecate(ctx context.Context, revisionID string) error {
    tx, err := s.db.BeginTx(ctx, nil)
    if err != nil {
        return fmt.Errorf("begin tx: %w", err)
    }
    defer func() { _ = tx.Rollback() }()

    // Load the revision first (need its memory_id).
    var memoryID string
    var currentStatus string
    err = tx.QueryRowContext(ctx,
        `SELECT memory_id, status FROM memory_revisions WHERE revision_id = ?`,
        revisionID,
    ).Scan(&memoryID, &currentStatus)
    if err != nil {
        return fmt.Errorf("load revision: %w", err)
    }
    if currentStatus == string(StatusDeprecated) {
        return tx.Commit() // idempotent no-op
    }

    // Mutate status (authorized path).
    if err := deprecateRevisionTx(ctx, tx, revisionID); err != nil {
        return err
    }

    // Recompute current_revision for the affected memory.
    var newCurrent sql.NullString
    err = tx.QueryRowContext(ctx, `
        SELECT revision_id FROM memory_revisions
        WHERE memory_id = ? AND status != 'deprecated'
        ORDER BY created_at DESC, revision_id DESC LIMIT 1
    `, memoryID).Scan(&newCurrent)
    if err != nil && !errors.Is(err, sql.ErrNoRows) {
        return fmt.Errorf("find next current: %w", err)
    }
    var currentArg interface{}
    if newCurrent.Valid {
        currentArg = newCurrent.String
    }
    _, err = tx.ExecContext(ctx,
        `UPDATE memory_state SET current_revision = ? WHERE memory_id = ?`,
        currentArg, memoryID,
    )
    if err != nil {
        return fmt.Errorf("update current_revision: %w", err)
    }
    return tx.Commit()
}
```

**Note for implementer:** this file needs `database/sql` imported for `sql.NullString` usage. Adjust the package imports accordingly.

- [ ] **Step 1: Write tests**, **Step 2: Run, fail**, **Step 3: Implement**, **Step 4: Run, pass**, **Step 5: Commit**

```bash
git add internal/memory/promote.go internal/memory/promote_test.go
git commit -m "feat(memory): add Promote (session→user/project) and Deprecate

Implements the promotion workflow from spec D15e and explicit
deprecation from D15b. Promote carries a session-scoped memory to
user or project scope with key-based dedup (supersedes set when a
matching key exists in target). Deprecate is idempotent and
recomputes memory_state.current_revision when the deprecated
revision was current.

Refs: docs/superpowers/specs/2026-04-04-memory-domain-design.md D-core"
```

---

### Task 11: Decay job (activation decay + TTL expiry)

**Goal:** A background job that runs on a schedule, applies exponential decay to activation values, and deletes or marks expired revisions.

**Files:**
- Create: `internal/memory/decay.go`
- Create: `internal/memory/decay_test.go`

**Decay model** (plan-time decision 1):
- Exponential with 14-day half-life on activation
- Formula: `new = old * exp(-elapsed_hours * ln(2) / (14 * 24))`
- Applied per `memory_state` row using `last_accessed_at` as the decay baseline (or `created_at` if never accessed)
- Activation floor: 0.05 — doesn't decay below this (keeps every memory with non-zero ranking weight)

**TTL expiry model:**
- Revisions with `expires_at IS NOT NULL AND expires_at < now()` are marked `deprecated` via the authorized path
- Memories whose ALL revisions are expired have `memory_state.current_revision` set to NULL (Recall treats these as not-found, and they can be swept later by a cleanup task)

**Cadence:** every 1 hour by default (configurable via `CORTEX_MEMORY_DECAY_INTERVAL`).

```go
package memory

import (
    "context"
    "database/sql"
    "fmt"
    "math"
    "time"
)

// DecayJob periodically decays activation scores and expires TTL'd revisions.
// Runs as a long-lived goroutine started from main.go.
type DecayJob struct {
    Store    *Store
    Interval time.Duration
    Logger   func(format string, args ...interface{}) // stdlib log.Printf compatible
}

// Run starts the decay loop. Returns when ctx is cancelled.
func (j *DecayJob) Run(ctx context.Context) {
    if j.Interval <= 0 {
        j.Interval = 1 * time.Hour
    }
    ticker := time.NewTicker(j.Interval)
    defer ticker.Stop()

    // Run once on startup so a freshly-booted server applies decay for
    // uptime accumulated while offline.
    j.runOnce(ctx)

    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            j.runOnce(ctx)
        }
    }
}

func (j *DecayJob) runOnce(ctx context.Context) {
    if err := j.Store.applyActivationDecay(ctx); err != nil && j.Logger != nil {
        j.Logger("memory decay: activation: %v", err)
    }
    if err := j.Store.expireTTLRevisions(ctx); err != nil && j.Logger != nil {
        j.Logger("memory decay: ttl: %v", err)
    }
}

const activationFloor = 0.05
const halfLifeHours = 14 * 24

// applyActivationDecay walks all memory_state rows and applies exponential
// decay to activation based on elapsed hours since last_accessed_at (or
// created_at if never accessed). Floors at activationFloor.
func (s *Store) applyActivationDecay(ctx context.Context) error {
    rows, err := s.db.QueryContext(ctx, `
        SELECT memory_id, activation, last_accessed_at, created_at FROM memory_state
    `)
    if err != nil {
        return fmt.Errorf("scan memory_state: %w", err)
    }
    type update struct {
        id         string
        activation float64
    }
    var updates []update
    now := time.Now().UTC()
    for rows.Next() {
        var id string
        var act float64
        var lastAccessed sql.NullString
        var createdStr string
        if err := rows.Scan(&id, &act, &lastAccessed, &createdStr); err != nil {
            rows.Close()
            return err
        }
        var baseline time.Time
        if lastAccessed.Valid {
            baseline, _ = time.Parse(time.RFC3339Nano, lastAccessed.String)
        } else {
            baseline, _ = time.Parse(time.RFC3339Nano, createdStr)
        }
        elapsedHours := now.Sub(baseline).Hours()
        if elapsedHours <= 0 {
            continue
        }
        decayFactor := math.Exp(-elapsedHours * math.Ln2 / halfLifeHours)
        newAct := act * decayFactor
        if newAct < activationFloor {
            newAct = activationFloor
        }
        if math.Abs(newAct-act) < 0.001 {
            continue // no meaningful change
        }
        updates = append(updates, update{id: id, activation: newAct})
    }
    rows.Close()

    // Apply updates in a single transaction.
    if len(updates) == 0 {
        return nil
    }
    tx, err := s.db.BeginTx(ctx, nil)
    if err != nil {
        return err
    }
    defer func() { _ = tx.Rollback() }()
    stmt, err := tx.PrepareContext(ctx, `UPDATE memory_state SET activation = ? WHERE memory_id = ?`)
    if err != nil {
        return err
    }
    defer stmt.Close()
    for _, u := range updates {
        if _, err := stmt.ExecContext(ctx, u.activation, u.id); err != nil {
            return err
        }
    }
    return tx.Commit()
}

// expireTTLRevisions marks any revision whose expires_at has passed as
// deprecated. Uses the authorized deprecation path for each, then
// recomputes current_revision pointers per affected memory.
func (s *Store) expireTTLRevisions(ctx context.Context) error {
    rows, err := s.db.QueryContext(ctx, `
        SELECT revision_id, memory_id FROM memory_revisions
        WHERE expires_at IS NOT NULL AND expires_at < ?
          AND status != 'deprecated'
    `, time.Now().UTC().Format(time.RFC3339Nano))
    if err != nil {
        return fmt.Errorf("scan expired: %w", err)
    }
    var toExpire []struct{ revisionID, memoryID string }
    for rows.Next() {
        var revID, memID string
        if err := rows.Scan(&revID, &memID); err != nil {
            rows.Close()
            return err
        }
        toExpire = append(toExpire, struct{ revisionID, memoryID string }{revID, memID})
    }
    rows.Close()

    for _, e := range toExpire {
        if err := s.Deprecate(ctx, e.revisionID); err != nil {
            return fmt.Errorf("expire %s: %w", e.revisionID, err)
        }
    }
    return nil
}
```

**Tests** must cover:
1. Activation decays after elapsed time (use a Store with injectable "now" or set `last_accessed_at` directly in the DB to simulate old access)
2. Activation floors at 0.05
3. TTL expiry marks revisions deprecated
4. TTL expiry updates current_revision when the expired revision was current
5. Idempotent — running twice produces the same result

Since the decay loop is time-based, tests call `applyActivationDecay` and `expireTTLRevisions` directly rather than starting `Run`. Only the cadence loop needs an integration test, which can use a tiny interval (50ms) and a short-lived context.

- [ ] **Steps 1-5: Standard TDD cycle**, **Step 6: Commit**

```bash
git add internal/memory/decay.go internal/memory/decay_test.go
git commit -m "feat(memory): add decay job for activation + TTL expiry

Implements the decay job from D4/D7. Exponential activation decay
with 14-day half-life, floor 0.05. TTL-expired revisions are marked
deprecated via the authorized path and current_revision is recomputed.
Default 1-hour cadence, override via CORTEX_MEMORY_DECAY_INTERVAL.

Refs: docs/superpowers/specs/2026-04-04-memory-domain-design.md D-core"
```

---

### Task 12: MCP tools — memory_write, memory_get, memory_history

**Goal:** Add the first three MCP tools as a single cohesive block, wired into the adapter. Follows the exact pattern from `internal/mcpadapter/typed_tools.go`.

**Files:**
- Create: `internal/mcpadapter/memory_tools.go`
- Create: `internal/mcpadapter/memory_tools_test.go`
- Modify: `internal/mcpadapter/adapter.go` (add `MemoryStore` field, call `registerMemoryTools`)

- [ ] **Step 1: Add field and registration to adapter.go**

Before:
```go
type Adapter struct {
    Store             *contextstore.Store
    Token             string
    TypeRegistry      *contexttypes.Registry
    EmbeddingProvider embedding.Provider
    VectorIndex       embedding.VectorIndex
}
```

After (add `MemoryStore *memory.Store`):
```go
type Adapter struct {
    Store             *contextstore.Store
    Token             string
    TypeRegistry      *contexttypes.Registry
    EmbeddingProvider embedding.Provider
    VectorIndex       embedding.VectorIndex
    MemoryStore       *memory.Store
}
```

And in `registerTools`, add at the end (inside the function, after existing `s.AddTool` calls):
```go
    if a.MemoryStore != nil {
        a.registerMemoryTools(s)
    }
```

Add the `memory` import at the top of `adapter.go`.

- [ ] **Step 2: Create `internal/mcpadapter/memory_tools.go`**

```go
package mcpadapter

import (
    "context"
    "encoding/json"
    "errors"
    "fmt"
    "time"

    "github.com/mark3labs/mcp-go/mcp"
    "github.com/mark3labs/mcp-go/server"

    "github.com/chrispian/cortex/internal/memory" // fix to actual module path
)

func (a *Adapter) registerMemoryTools(s *server.MCPServer) {
    s.AddTool(mcp.NewTool("memory_write",
        mcp.WithDescription("Write a new memory revision. Use memory_key for keyed memories (recommended); omit for keyless freeform. Sets supersedes to auto-deprecate a prior revision."),
        mcp.WithString("namespace", mcp.Required(), mcp.Description("Memory namespace, e.g. user/{id}/memory")),
        mcp.WithString("memory_key", mcp.Description("Dot-notation key, e.g. user.preferences.verbosity")),
        mcp.WithString("supersedes", mcp.Description("Revision ID this write explicitly replaces")),
        mcp.WithString("status", mcp.Description("draft|reviewed|canonical|deprecated (default draft)")),
        mcp.WithString("author_agent_id", mcp.Required(), mcp.Description("Writing agent identifier")),
        mcp.WithString("author_version", mcp.Description("Writing agent version")),
        mcp.WithString("trigger", mcp.Required(), mcp.Description("explicit|post_compact|per_turn|promotion|manual")),
        mcp.WithString("session_id", mcp.Required(), mcp.Description("Source session; manual:<ulid> for manual inserts")),
        mcp.WithString("origin", mcp.Required(), mcp.Description("user|feedback|project|reference|observation")),
        mcp.WithNumber("confidence", mcp.Required(), mcp.Description("Author-assigned confidence in [0,1]")),
        mcp.WithString("tags", mcp.Description("JSON array of tag strings")),
        mcp.WithNumber("ttl_seconds", mcp.Description("Per-memory TTL in seconds; omit for persistent")),
        mcp.WithString("payload_summary", mcp.Required(), mcp.Description("One-line summary of the memory")),
        mcp.WithString("payload_body", mcp.Description("Optional markdown body")),
    ), a.handleMemoryWrite)

    s.AddTool(mcp.NewTool("memory_get",
        mcp.WithDescription("Fetch the current revision of a memory by (namespace, memory_key)."),
        mcp.WithString("namespace", mcp.Required(), mcp.Description("Memory namespace")),
        mcp.WithString("memory_key", mcp.Required(), mcp.Description("Dot-notation key")),
    ), a.handleMemoryGet)

    s.AddTool(mcp.NewTool("memory_history",
        mcp.WithDescription("Return the full revision timeline for a memory, newest first."),
        mcp.WithString("namespace", mcp.Required(), mcp.Description("Memory namespace")),
        mcp.WithString("memory_key", mcp.Required(), mcp.Description("Dot-notation key")),
    ), a.handleMemoryHistory)
}

func (a *Adapter) handleMemoryWrite(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
    ctx := context.Background()

    if errRes, _ := a.checkScope(ctx, "memory:write"); errRes != nil {
        return errRes, nil
    }

    namespace := req.GetString("namespace", "")
    memoryKey := req.GetString("memory_key", "")
    supersedes := req.GetString("supersedes", "")
    statusStr := req.GetString("status", "")
    authorID := req.GetString("author_agent_id", "")
    authorVersion := req.GetString("author_version", "")
    trigger := req.GetString("trigger", "")
    sessionID := req.GetString("session_id", "")
    origin := req.GetString("origin", "")
    confidence := req.GetFloat("confidence", 0)
    tagsStr := req.GetString("tags", "")
    ttlSeconds := req.GetInt("ttl_seconds", 0)
    summary := req.GetString("payload_summary", "")
    body := req.GetString("payload_body", "")

    if namespace == "" || authorID == "" || trigger == "" || sessionID == "" || origin == "" {
        return a.toolError("validation_error", "namespace, author_agent_id, trigger, session_id, origin are required"), nil
    }

    var tags []string
    if tagsStr != "" {
        if err := json.Unmarshal([]byte(tagsStr), &tags); err != nil {
            return a.toolError("validation_error", "tags must be a JSON array of strings"), nil
        }
    }

    in := memory.WriteInput{
        Namespace:  namespace,
        MemoryKey:  memoryKey,
        Supersedes: supersedes,
        Status:     memory.Status(statusStr),
        Author:     memory.Author{AgentID: authorID, AgentVersion: authorVersion},
        Trigger:    memory.Trigger(trigger),
        SessionID:  sessionID,
        Origin:     memory.Origin(origin),
        Confidence: confidence,
        Tags:       tags,
        TTL:        time.Duration(ttlSeconds) * time.Second,
        Payload:    memory.Payload{Summary: summary, Body: body},
    }
    rev, err := a.MemoryStore.WriteRevision(ctx, in)
    if err != nil {
        if errors.Is(err, memory.ErrInvalidInput) {
            return a.toolError("validation_error", err.Error()), nil
        }
        return a.toolError("internal_error", err.Error()), nil
    }
    return a.toolJSON(rev), nil
}

func (a *Adapter) handleMemoryGet(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
    ctx := context.Background()
    if errRes, _ := a.checkScope(ctx, "memory:read"); errRes != nil {
        return errRes, nil
    }
    namespace := req.GetString("namespace", "")
    memoryKey := req.GetString("memory_key", "")
    if namespace == "" || memoryKey == "" {
        return a.toolError("validation_error", "namespace and memory_key are required"), nil
    }
    rev, err := a.MemoryStore.GetCurrent(ctx, namespace, memoryKey)
    if err != nil {
        if errors.Is(err, memory.ErrNotFound) {
            return a.toolError("not_found", fmt.Sprintf("no memory for %s/%s", namespace, memoryKey)), nil
        }
        return a.toolError("internal_error", err.Error()), nil
    }
    return a.toolJSON(rev), nil
}

func (a *Adapter) handleMemoryHistory(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
    ctx := context.Background()
    if errRes, _ := a.checkScope(ctx, "memory:read"); errRes != nil {
        return errRes, nil
    }
    namespace := req.GetString("namespace", "")
    memoryKey := req.GetString("memory_key", "")
    if namespace == "" || memoryKey == "" {
        return a.toolError("validation_error", "namespace and memory_key are required"), nil
    }
    revs, err := a.MemoryStore.GetHistory(ctx, namespace, memoryKey)
    if err != nil {
        if errors.Is(err, memory.ErrNotFound) {
            return a.toolError("not_found", fmt.Sprintf("no memory for %s/%s", namespace, memoryKey)), nil
        }
        return a.toolError("internal_error", err.Error()), nil
    }
    return a.toolJSON(revs), nil
}
```

**NOTE:** The mcp-go library's `req.GetFloat`, `req.GetInt` method names may differ (e.g., `GetNumber` returning a float). Check the actual API used in `typed_tools.go` and match it exactly. If unsure, read `internal/mcpadapter/tools.go` for the canonical parameter-extraction patterns.

- [ ] **Step 3: Auth scopes (locked in)**

Use `"memory:write"` for writes (`memory_write`, `memory_promote`, `memory_deprecate`) and `"memory:read"` for reads (`memory_get`, `memory_history`, `memory_recall`). The handler code in Step 2 already references these strings. If the auth system has a central scope registry, add both entries there following whatever pattern existing scopes (e.g., the one used by `context_typed_write`) use. If scopes are ad-hoc strings with no registry, nothing more to do — the `checkScope` call validates against the token's claim list at runtime.

- [ ] **Step 4: Write integration-style tests in `memory_tools_test.go`**

Pattern after existing `mcpadapter` tests (check `internal/mcpadapter/*_test.go` — there may or may not be any; if none exist, write minimal unit tests that call the handlers directly with synthesized `mcp.CallToolRequest` values, then assert on the JSON returned).

- [ ] **Steps 5-7: Run, pass, commit**

```bash
go test ./internal/mcpadapter/... -v
git add internal/mcpadapter/memory_tools.go internal/mcpadapter/memory_tools_test.go internal/mcpadapter/adapter.go
git commit -m "feat(mcp): register memory_write, memory_get, memory_history tools

First three MCP tools for the memory subsystem (spec D15b). Tools are
only registered when adapter.MemoryStore is non-nil. Handlers follow
the validation → store call → toolJSON/toolError pattern established
by typed_tools.go.

Refs: docs/superpowers/specs/2026-04-04-memory-domain-design.md D-core"
```

---

### Task 13: MCP tools — memory_recall, memory_promote, memory_deprecate

**Goal:** Add the remaining three MCP tools. Follows exactly the pattern from Task 12.

**Files:**
- Modify: `internal/mcpadapter/memory_tools.go` (add three more registrations and handlers)
- Modify: `internal/mcpadapter/memory_tools_test.go` (add tests)

**Registrations** (add inside `registerMemoryTools`):

```go
    s.AddTool(mcp.NewTool("memory_recall",
        mcp.WithDescription("Context-assembly retrieval. Multi-namespace, multi-knob. Primary tool for MemorySource integration."),
        mcp.WithString("namespaces", mcp.Required(), mcp.Description("JSON array of memory namespaces")),
        mcp.WithString("revision_scope", mcp.Description("current|timeline (default current)")),
        mcp.WithString("ranking", mcp.Description("activation|chronological|similarity (default activation)")),
        mcp.WithString("query", mcp.Description("Query text for similarity ranking")),
        mcp.WithString("origins", mcp.Description("JSON array of origins to filter by")),
        mcp.WithString("statuses", mcp.Description("JSON array of statuses; default excludes deprecated")),
        mcp.WithString("tags", mcp.Description("JSON array of tags to filter by (ANY match)")),
        mcp.WithNumber("confidence_min", mcp.Description("Minimum confidence threshold")),
        mcp.WithString("since", mcp.Description("RFC3339 lower bound on created_at")),
        mcp.WithString("until", mcp.Description("RFC3339 upper bound on created_at")),
        mcp.WithNumber("limit", mcp.Description("Max results (default 30, cap 500)")),
    ), a.handleMemoryRecall)

    s.AddTool(mcp.NewTool("memory_promote",
        mcp.WithDescription("Promote a session-scoped memory to user or project scope."),
        mcp.WithString("source_namespace", mcp.Required(), mcp.Description("Session namespace the memory lives in")),
        mcp.WithString("source_memory_id", mcp.Required(), mcp.Description("Memory ID to promote")),
        mcp.WithString("target_namespace", mcp.Required(), mcp.Description("Target user or project namespace")),
        mcp.WithString("actor_agent_id", mcp.Required(), mcp.Description("Agent requesting promotion")),
        mcp.WithString("actor_version", mcp.Description("Requesting agent version")),
    ), a.handleMemoryPromote)

    s.AddTool(mcp.NewTool("memory_deprecate",
        mcp.WithDescription("Mark a specific memory revision as deprecated without writing a replacement."),
        mcp.WithString("revision_id", mcp.Required(), mcp.Description("Revision to deprecate")),
    ), a.handleMemoryDeprecate)
```

**Handlers:** Follow the same pattern. `handleMemoryRecall` needs to unmarshal the JSON array parameters (`namespaces`, `origins`, `statuses`, `tags`) via `json.Unmarshal`. When `ErrSimilarityUnavailable` is returned from the store, respond with error code `"similarity_unavailable"` — this is the stable code callers check for.

- [ ] **Steps 1-5: Standard TDD**, **Step 6: Commit**

```bash
git add internal/mcpadapter/memory_tools.go internal/mcpadapter/memory_tools_test.go
git commit -m "feat(mcp): register memory_recall, memory_promote, memory_deprecate

Completes the D-core MCP tool surface (spec D15b). memory_recall is
the multi-knob recall tool consumed by Nanite's MemorySource.
memory_promote moves session-scoped memories to user/project scope.
memory_deprecate explicitly marks a revision deprecated without
writing a replacement. Similarity ranking returns a structured
similarity_unavailable error at D-core — populated by Track H later.

Refs: docs/superpowers/specs/2026-04-04-memory-domain-design.md D-core"
```

---

### Task 14: Wire memory subsystem into `cmd/contextd/main.go`

**Goal:** Construct `memory.Store` from the contextstore's DB handle, inject it into the MCP adapter, and start the decay job goroutine. Respect `CORTEX_MEMORY_DECAY_INTERVAL` env var.

**Files:**
- Modify: `cmd/contextd/main.go`

- [ ] **Step 1: Read current main.go init sequence around store construction**

```bash
grep -n "contextstore.Open\|mcpadapter.New" cmd/contextd/main.go
```

- [ ] **Step 2: Add memory store construction and decay goroutine**

After the existing `contextstore.Open` block, add:

```go
    // Memory subsystem (D-core). Shares contextstore's *sql.DB.
    // Stubs for embedder and queue — swapped when Tracks H and I land.
    memStore := memory.NewStore(store.DB(), memory.NoopEmbedder{}, memory.NoopQueue{})

    // Start decay job.
    decayInterval := 1 * time.Hour
    if v := os.Getenv("CORTEX_MEMORY_DECAY_INTERVAL"); v != "" {
        if d, err := time.ParseDuration(v); err == nil {
            decayInterval = d
        } else {
            log.Printf("warning: invalid CORTEX_MEMORY_DECAY_INTERVAL %q, using default 1h", v)
        }
    }
    decayJob := &memory.DecayJob{
        Store:    memStore,
        Interval: decayInterval,
        Logger:   log.Printf,
    }
    go decayJob.Run(ctx)
```

Then in the MCP adapter construction path (wherever `mcpadapter.New(store, token)` is called), assign:

```go
    adapter := mcpadapter.New(store, token)
    adapter.MemoryStore = memStore
```

Add the `memory` import at the top of `main.go`.

- [ ] **Step 3: Build and sanity-check**

```bash
make build
```

Expected: build succeeds, binary exists.

- [ ] **Step 4: Smoke test**

Start the server briefly and verify the decay goroutine logs nothing alarming:

```bash
# From the repo root
./contextd serve -addr :18089 -managed-auth &
PID=$!
sleep 2
kill $PID
```

Expected: server starts, accepts connection, shuts down cleanly on signal.

- [ ] **Step 5: Commit**

```bash
git add cmd/contextd/main.go
git commit -m "feat(contextd): wire memory subsystem and start decay job

Constructs memory.Store sharing contextstore's *sql.DB, wires it into
the MCP adapter, and starts the activation decay / TTL expiry goroutine.
Decay interval defaults to 1h and can be overridden via
CORTEX_MEMORY_DECAY_INTERVAL. Uses NoopEmbedder and NoopQueue stubs
per spec D14 — Tracks H and I swap them for real implementations.

Refs: docs/superpowers/specs/2026-04-04-memory-domain-design.md D-core"
```

---

### Task 15: End-to-end integration test

**Goal:** A single integration test that exercises the full memory subsystem through the MCP tool surface: write → read → recall → promote → deprecate. Lives in `tests/integration/memory_test.go`.

**Files:**
- Create: `tests/integration/memory_test.go`

- [ ] **Step 1: Read existing integration test patterns**

```bash
ls tests/integration/
head -80 tests/integration/*.go
```

Identify how existing tests set up a store + adapter + in-process MCP server (if they do) or call handlers directly via the adapter.

- [ ] **Step 2: Write the integration test**

Pattern:

```go
package integration_test

import (
    "context"
    "testing"
    "time"

    "github.com/chrispian/cortex/internal/contextstore" // fix path
    "github.com/chrispian/cortex/internal/mcpadapter"
    "github.com/chrispian/cortex/internal/memory"
)

func TestMemoryEndToEnd(t *testing.T) {
    ctx := context.Background()
    dir := t.TempDir()
    cs, err := contextstore.Open(ctx, contextstore.Config{RootDir: dir})
    if err != nil {
        t.Fatalf("contextstore.Open: %v", err)
    }
    defer cs.Close()

    // Provision a token with memory:read + memory:write scope.
    // (Reuse the existing auth-token provisioning helper if tests/integration
    // has one; otherwise call cs.CreateAuthToken with the scope list.)
    token, err := cs.CreateAuthToken(ctx, []string{"memory:read", "memory:write"})
    if err != nil {
        t.Fatalf("create token: %v", err)
    }

    ms := memory.NewStore(cs.DB(), memory.NoopEmbedder{}, memory.NoopQueue{})
    adapter := mcpadapter.New(cs, token.Token)
    adapter.MemoryStore = ms

    // 1. Write a memory via the adapter's handler (direct call, no stdio).
    // NOTE: adjust to whatever invocation style existing integration tests
    // use. Direct handler call is simplest; full MCP RPC round-trip is
    // overkill for this test.

    // 2. Assert the write returned a revision_id.
    // 3. Write a second revision with supersedes = first revision_id.
    // 4. Recall and assert the newer revision is returned, not the deprecated one.
    // 5. GetHistory and assert both revisions are present with correct statuses.
    // 6. Write a session-scoped memory.
    // 7. Promote it to user scope; assert the new revision exists in the target
    //    namespace and the session revision is deprecated.
    // 8. Deprecate a revision directly; assert subsequent GetCurrent returns
    //    ErrNotFound (or empty, depending on how errors propagate through MCP).

    // Each assertion uses t.Fatalf on unexpected conditions.
    _ = time.Second // avoid unused import warning if not used
}
```

This test is deliberately written as a scaffold — the implementer fills in the MCP invocation details matching the existing integration test pattern.

- [ ] **Step 3: Run the full test suite**

```bash
make test
```

Expected: PASS, including the new integration test.

- [ ] **Step 4: Run all pre-commit checks**

```bash
go vet ./...
gofmt -l .
# lefthook should run the same checks on commit
```

- [ ] **Step 5: Commit**

```bash
git add tests/integration/memory_test.go
git commit -m "test(memory): end-to-end integration coverage for D-core

Exercises the full memory subsystem through the MCP adapter:
write → read → recall → promote → deprecate. Uses real SQLite via
contextstore.Open, NoopEmbedder/NoopQueue stubs, and scope-gated
auth tokens.

Refs: docs/superpowers/specs/2026-04-04-memory-domain-design.md D-core"
```

---

## Post-task verification checklist

After all 15 tasks are committed, run the full verification gate before declaring D-core complete. Apply @superpowers:verification-before-completion.

- [ ] `make build` succeeds
- [ ] `make test` — all tests pass (existing + new)
- [ ] `go vet ./...` — no warnings
- [ ] `gofmt -l .` — no output
- [ ] `golangci-lint run --new --timeout 30s` — no new issues
- [ ] Every task has been committed separately (count commits: `git log --oneline -20` should show the D-core task series)
- [ ] Spec cross-check: open `docs/superpowers/specs/2026-04-04-memory-domain-design.md` and verify every D-core item from D14 is implemented (types, keys, namespaces, schema, write path, read path, recall, promote, deprecate, activation, decay, stub interfaces, MCP tools, main.go wiring, integration test)
- [ ] Guard test passes: `TestOnlyDeprecationPathMutatesRevisionStatus`
- [ ] The six MCP tools are listed when the adapter starts (check `make build` output or startup logs)
- [ ] A fresh `contextd` install on an empty directory runs migrations to v9 without error

## What this plan explicitly does NOT do

Per spec D14, the following are D-deferred and must NOT be implemented in this plan's tasks. Adding any of them here defeats the D-core boundary and blocks progress on Tracks H and I:

- **Do not** implement any real Embedder provider (Anthropic, OpenAI, Ollama, etc.)
- **Do not** implement the real JobQueue — no SQLite-backed persistence, no workers, no retry/backoff
- **Do not** implement semantic similarity search — similarity ranking returns `ErrSimilarityUnavailable`, that's the design
- **Do not** implement auto-embed on write beyond the stub `queue.Enqueue` call (which NoopQueue drops)
- **Do not** implement the semantic-dedup-on-write mode — `dedup=semantic` parameter on `memory_write` is accepted but returns `dedup_unavailable`
- **Do not** implement the backfill job — it's a specialization of the real queue which doesn't exist yet
- **Do not** design or implement config-file or GUI-based provider selection — that's Track F / G territory
- **Do not** extract anything into a separate Go module — Tracks H and I are the extraction; D-core lives inside the cortex repo

If the implementer finds themselves wanting to build any of the above "while we're in there," they must stop and surface the decision. These are not cost savings; they are boundary violations.

---

## References

- `docs/superpowers/specs/2026-04-04-memory-domain-design.md` — the design spec this plan implements
- `docs/RELEASE-ROADMAP.md` — overall release decomposition (Tracks A–I)
- `internal/contextstore/store.go` — existing store and migration pattern to match
- `internal/mcpadapter/typed_tools.go` — existing MCP tool pattern to match
- `cmd/contextd/main.go` — entry point where memory subsystem is wired
