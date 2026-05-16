# Ordering Fix, Backfill, and Semantic Dedup Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix ULID/timestamp ordering bugs, add config file + OpenAI embedding model, add backfill CLI command, and implement opt-in semantic dedup on the memory write path.

**Architecture:** Monotonic ULIDs and RFC3339Nano timestamps fix the ordering race. A `~/.conduit/config.yaml` file configures the embedding provider/model and dedup threshold. The backfill command iterates unembedded revisions and calls `EmbedRevision()`. Semantic dedup hooks into `WriteRevision()` — when requested, it embeds the incoming payload, searches for similar revisions via `Recall(RankingSimilarity)`, and auto-supersedes matches above threshold.

**Tech Stack:** Go 1.22+, SQLite (modernc.org/sqlite), oklog/ulid/v2, gopkg.in/yaml.v3, go-providers (OpenAI embedder), go-queue

---

## File Map

| File | Action | Responsibility |
|------|--------|---------------|
| `internal/memory/ids.go` | Modify | Switch to monotonic ULID entropy |
| `internal/memory/ids_test.go` | Create | Test ULID monotonicity |
| `internal/memory/write.go` | Modify | RFC3339Nano timestamps, dedup logic in WriteRevision |
| `internal/memory/activation.go` | Modify | RFC3339Nano for last_accessed_at |
| `internal/memory/read.go` | Modify | RFC3339Nano parsing in scanRevision/scanState |
| `internal/memory/recall.go` | Modify | RFC3339Nano for Since/Until filters |
| `internal/memory/decay.go` | Modify | RFC3339Nano for expiry checks, parse both formats |
| `internal/memory/decay_test.go` | Modify | Use RFC3339Nano in test fixtures |
| `internal/memory/types.go` | Modify | Add Dedup/DedupThreshold to WriteInput, DedupMatch to Revision |
| `internal/memory/store.go` | Modify | Add dedupThreshold field |
| `internal/memory/dedup.go` | Create | Semantic dedup logic (findSemanticMatch) |
| `internal/memory/dedup_test.go` | Create | Tests for semantic dedup |
| `internal/config/config.go` | Create | Config file loading (yaml) |
| `internal/config/config_test.go` | Create | Config loading tests |
| `cmd/contextd/main.go` | Modify | Load config, pass to conduit.Open, add backfill subcommand |
| `cmd/contextd/backfill.go` | Create | Backfill CLI command |
| `conduit.go` | Modify | Add WithDedupThreshold option, pass to memory.Store |
| `internal/mcpadapter/memory_tools.go` | Modify | Add dedup/dedup_threshold params to memory_write |

---

### Task 1: Monotonic ULIDs

**Files:**
- Modify: `internal/memory/ids.go`
- Create: `internal/memory/ids_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/memory/ids_test.go`:

```go
package memory_test

import (
	"testing"

	"github.com/hollis-labs/tesseract/internal/memory"
)

func TestNewULID_Monotonic(t *testing.T) {
	// Generate 1000 ULIDs in rapid succession.
	// They must be strictly lexicographically increasing.
	ids := make([]string, 1000)
	for i := range ids {
		ids[i] = memory.NewULID()
	}
	for i := 1; i < len(ids); i++ {
		if ids[i] <= ids[i-1] {
			t.Fatalf("ULIDs not monotonic at index %d: %s <= %s", i, ids[i], ids[i-1])
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/chrispian/Projects-apps/vanta-conduit && go test ./internal/memory/ -run TestNewULID_Monotonic -v -count=5`
Expected: FAIL on at least some runs (non-monotonic random entropy)

- [ ] **Step 3: Switch to monotonic entropy**

Replace `internal/memory/ids.go`:

```go
package memory

import (
	"crypto/rand"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"
)

var (
	entropy     = ulid.Monotonic(rand.Reader, 0)
	entropyLock sync.Mutex
)

// NewULID returns a new lexicographically sortable ULID as a string.
// Uses a monotonic entropy source to guarantee ordering within the
// same millisecond.
func NewULID() string {
	entropyLock.Lock()
	defer entropyLock.Unlock()
	return ulid.MustNew(ulid.Timestamp(time.Now()), entropy).String()
}
```

Note: `ulid.Monotonic` provides its own internal locking for reads, but we need the mutex to protect the entire `MustNew` call (timestamp + entropy read must be atomic).

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /Users/chrispian/Projects-apps/vanta-conduit && go test ./internal/memory/ -run TestNewULID_Monotonic -v -count=10`
Expected: PASS on all 10 runs

- [ ] **Step 5: Run full memory tests**

Run: `cd /Users/chrispian/Projects-apps/vanta-conduit && go test ./internal/memory/ -v -count=1`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/memory/ids.go internal/memory/ids_test.go
git commit -m "fix(memory): use monotonic ULID entropy for deterministic ordering"
```

---

### Task 2: RFC3339Nano Timestamp Precision

**Files:**
- Modify: `internal/memory/write.go`
- Modify: `internal/memory/activation.go`
- Modify: `internal/memory/read.go`
- Modify: `internal/memory/recall.go`
- Modify: `internal/memory/decay.go`
- Modify: `internal/memory/decay_test.go`

This task changes all `time.DateTime` formatting/parsing in the memory package to `time.RFC3339Nano`. Both formats must be parseable on read (backward compat with existing data).

- [ ] **Step 1: Create a timestamp format helper**

Add to `internal/memory/read.go` (near the top, after imports):

```go
// memoryTimeFormat is the canonical timestamp format for memory tables.
const memoryTimeFormat = time.RFC3339Nano

// parseMemoryTime parses a timestamp stored in memory tables. It tries
// RFC3339Nano first, then falls back to time.DateTime for backward
// compatibility with data written before the precision upgrade.
func parseMemoryTime(s string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err == nil {
		return t, nil
	}
	return time.Parse(time.DateTime, s)
}
```

- [ ] **Step 2: Update write.go — format timestamps with RFC3339Nano**

In `internal/memory/write.go`:

Line 88 — change `nullTime` helper:
```go
	nullTime := func(t *time.Time) sql.NullString {
		if t == nil {
			return sql.NullString{}
		}
		return sql.NullString{String: t.UTC().Format(memoryTimeFormat), Valid: true}
	}
```

Line 109 — change `created_at` formatting:
```go
		now.Format(memoryTimeFormat),
```

- [ ] **Step 3: Update activation.go — format last_accessed_at**

In `internal/memory/activation.go` line 22:
```go
	now := time.Now().UTC().Format(memoryTimeFormat)
```

- [ ] **Step 4: Update read.go — parse timestamps with parseMemoryTime**

In `internal/memory/read.go`, update `scanRevision` (around lines 40-44):
Replace all `time.Parse(time.DateTime, ...)` calls with `parseMemoryTime(...)`.

Also update the state-scanning code in `recall.go`'s `fetchStates` (around line 286):
Replace `time.Parse(time.DateTime, ...)` with `parseMemoryTime(...)`.

- [ ] **Step 5: Update recall.go — format Since/Until filters**

In `internal/memory/recall.go`, update the time formatting for filter parameters (around lines 225-233):

```go
	if in.Filters.Since != nil {
		where = append(where, "r.created_at >= ?")
		args = append(args, in.Filters.Since.UTC().Format(memoryTimeFormat))
	}
	if in.Filters.Until != nil {
		where = append(where, "r.created_at <= ?")
		args = append(args, in.Filters.Until.UTC().Format(memoryTimeFormat))
	}

	// Always exclude expired revisions.
	now := time.Now().UTC().Format(memoryTimeFormat)
```

- [ ] **Step 6: Update decay.go — format/parse timestamps**

In `internal/memory/decay.go`:

Line 126 — change `collectDecayUpdates` parse call:
```go
		baseTime, parseErr := parseMemoryTime(baseline)
```

Line 173 — change `expireTTLRevisions` format:
```go
	now := time.Now().UTC().Format(memoryTimeFormat)
```

- [ ] **Step 7: Update decay_test.go — use RFC3339Nano in fixtures**

In `internal/memory/decay_test.go`, update lines 25, 61, and ~179 where test fixtures format backdated timestamps:

```go
	oldTime := time.Now().UTC().Add(-14 * 24 * time.Hour).Format(memoryTimeFormat)
```

Note: `memoryTimeFormat` is in the `memory` package (unexported). Since tests are in `memory_test` package (external), they can't access it directly. Instead, use `time.RFC3339Nano` directly in test code:

```go
	oldTime := time.Now().UTC().Add(-14 * 24 * time.Hour).Format(time.RFC3339Nano)
```

- [ ] **Step 8: Run full memory test suite**

Run: `cd /Users/chrispian/Projects-apps/vanta-conduit && go test ./internal/memory/ -v -count=1`
Expected: PASS

- [ ] **Step 9: Run the flaky integration test multiple times**

Run: `cd /Users/chrispian/Projects-apps/vanta-conduit && go test ./tests/integration/ -run TestMemoryEndToEnd -count=20 -v 2>&1 | grep -E "PASS|FAIL"`
Expected: PASS on all 20 runs (the ordering bug is now fixed by both monotonic ULIDs and nano timestamps)

- [ ] **Step 10: Run full test suite**

Run: `cd /Users/chrispian/Projects-apps/vanta-conduit && go test ./... -count=1 2>&1 | tail -20`
Expected: PASS

- [ ] **Step 11: Commit**

```bash
git add internal/memory/write.go internal/memory/activation.go internal/memory/read.go internal/memory/recall.go internal/memory/decay.go internal/memory/decay_test.go
git commit -m "fix(memory): upgrade timestamps to RFC3339Nano for sub-second precision"
```

---

### Task 3: Config File Loading

**Files:**
- Create: `internal/config/config.go`
- Create: `internal/config/config_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/config/config_test.go`:

```go
package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/tesseract/internal/config"
)

func TestLoad_FullConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	os.WriteFile(path, []byte(`
embedding:
  provider: openai
  model: text-embedding-3-large

dedup:
  similarity_threshold: 0.90
`), 0644)

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Embedding.Provider != "openai" {
		t.Errorf("provider: got %q, want openai", cfg.Embedding.Provider)
	}
	if cfg.Embedding.Model != "text-embedding-3-large" {
		t.Errorf("model: got %q, want text-embedding-3-large", cfg.Embedding.Model)
	}
	if cfg.Dedup.SimilarityThreshold != 0.90 {
		t.Errorf("threshold: got %f, want 0.90", cfg.Dedup.SimilarityThreshold)
	}
}

func TestLoad_Defaults(t *testing.T) {
	cfg, err := config.Load("/nonexistent/path/config.yaml")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Embedding.Model != "text-embedding-3-large" {
		t.Errorf("default model: got %q, want text-embedding-3-large", cfg.Embedding.Model)
	}
	if cfg.Dedup.SimilarityThreshold != 0.85 {
		t.Errorf("default threshold: got %f, want 0.85", cfg.Dedup.SimilarityThreshold)
	}
}

func TestLoad_PartialOverride(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	os.WriteFile(path, []byte(`
dedup:
  similarity_threshold: 0.70
`), 0644)

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// Model should still be default.
	if cfg.Embedding.Model != "text-embedding-3-large" {
		t.Errorf("model: got %q, want default", cfg.Embedding.Model)
	}
	if cfg.Dedup.SimilarityThreshold != 0.70 {
		t.Errorf("threshold: got %f, want 0.70", cfg.Dedup.SimilarityThreshold)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/chrispian/Projects-apps/vanta-conduit && go test ./internal/config/ -v`
Expected: FAIL — package doesn't exist

- [ ] **Step 3: Implement config loading**

Create `internal/config/config.go`:

```go
package config

import (
	"os"

	"gopkg.in/yaml.v3"
)

// Config holds the top-level conduit configuration loaded from config.yaml.
type Config struct {
	Embedding EmbeddingConfig `yaml:"embedding"`
	Dedup     DedupConfig     `yaml:"dedup"`
}

// EmbeddingConfig configures the embedding provider and model.
type EmbeddingConfig struct {
	Provider string `yaml:"provider"`
	Model    string `yaml:"model"`
}

// DedupConfig configures semantic dedup behavior.
type DedupConfig struct {
	SimilarityThreshold float64 `yaml:"similarity_threshold"`
}

// Defaults returns a Config with sensible defaults.
func Defaults() Config {
	return Config{
		Embedding: EmbeddingConfig{
			Provider: "openai",
			Model:    "text-embedding-3-large",
		},
		Dedup: DedupConfig{
			SimilarityThreshold: 0.85,
		},
	}
}

// Load reads a config file from path. If the file does not exist, returns
// defaults. Partial configs are merged over defaults.
func Load(path string) (Config, error) {
	cfg := Defaults()

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, err
	}

	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return cfg, err
	}

	// Re-apply defaults for zero values that shouldn't be zero.
	if cfg.Embedding.Model == "" {
		cfg.Embedding.Model = Defaults().Embedding.Model
	}
	if cfg.Dedup.SimilarityThreshold == 0 {
		cfg.Dedup.SimilarityThreshold = Defaults().Dedup.SimilarityThreshold
	}

	return cfg, nil
}
```

- [ ] **Step 4: Run tests**

Run: `cd /Users/chrispian/Projects-apps/vanta-conduit && go test ./internal/config/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat: add config file loading for embedding and dedup settings"
```

---

### Task 4: Wire Config into main.go + Add WithDedupThreshold

**Files:**
- Modify: `cmd/contextd/main.go`
- Modify: `conduit.go`
- Modify: `internal/memory/store.go`

- [ ] **Step 1: Add WithDedupThreshold option to conduit.go**

In `conduit.go`, add to the `options` struct:

```go
type options struct {
	embedder       provider.Embedder
	embeddingModel string
	logger         func(string, ...any)
	queue          queue.Queue
	dedupThreshold float64
}
```

Add the option function:

```go
// WithDedupThreshold sets the similarity threshold for semantic dedup.
func WithDedupThreshold(t float64) Option {
	return func(o *options) { o.dedupThreshold = t }
}
```

In `Open()`, pass `dedupThreshold` to `memory.NewStore`:

```go
	memStore := memory.NewStore(store.DB(), o.embedder, o.embeddingModel, o.dedupThreshold, jobQueue)
```

- [ ] **Step 2: Add dedupThreshold to memory.Store**

In `internal/memory/store.go`:

```go
type Store struct {
	db             *sql.DB
	embedder       provider.Embedder
	embeddingModel string
	dedupThreshold float64
	queue          JobQueue
}

func NewStore(db *sql.DB, embedder provider.Embedder, embeddingModel string, dedupThreshold float64, queue JobQueue) *Store {
	if queue == nil {
		queue = NoopQueue{}
	}
	return &Store{db: db, embedder: embedder, embeddingModel: embeddingModel, dedupThreshold: dedupThreshold, queue: queue}
}
```

- [ ] **Step 3: Update all NewStore call sites**

There are ~7 call sites that call `NewStore`. Each needs the new `dedupThreshold` parameter (pass `0` for tests that don't care about dedup — the dedup logic will use the store's threshold only when dedup is requested):

- `conduit.go` — pass `o.dedupThreshold`
- `cmd/contextd/main.go` — pass `cfg.Dedup.SimilarityThreshold`
- `internal/memory/write_test.go` `newTestStore` — pass `0`
- `internal/memory/embed_test.go` `newTestStoreWithEmbedder` and `newTestStoreNoEmbedder` — pass `0`
- `internal/mcpadapter/memory_tools_test.go` — pass `0`
- `tests/integration/memory_test.go` — pass `0`

- [ ] **Step 4: Wire config loading in main.go**

In `cmd/contextd/main.go`, in the `run` function, after determining `root`:

```go
	conduitCfg, cfgErr := config.Load(filepath.Join(root, "config.yaml"))
	if cfgErr != nil {
		log.Printf("warning: config load failed: %v (using defaults)", cfgErr)
		conduitCfg = config.Defaults()
	}
```

Import `"github.com/hollis-labs/tesseract/internal/config"`.

In `runMCP`, create the OpenAI embedder based on config and pass it to `memory.NewStore`:

```go
	var embedder provider.Embedder
	if conduitCfg.Embedding.Provider == "openai" {
		apiKey := os.Getenv("OPENAI_API_KEY")
		if apiKey != "" {
			embedder = provider.NewOpenAI(apiKey)
		}
	}

	memStore := memory.NewStore(store.DB(), embedder, conduitCfg.Embedding.Model, conduitCfg.Dedup.SimilarityThreshold, queueAdapter)
```

Note: `provider.NewOpenAI` is from `github.com/hollis-labs/go-providers/provider`. Check the exact constructor name — it may be `openai.New()` or `provider.NewOpenAIEmbedder()`. Read the go-providers source to confirm the constructor.

Also update the embed handler call to pass the model:
```go
	worker.Register("embed", conduit.NewEmbedHandler(memStore, conduitCfg.Embedding.Model, log.Printf))
```

The config must be passed from `run` into `runMCP`. Either add it as a parameter to `runMCP` or make it accessible. The cleanest approach: pass `conduitCfg` as a parameter to `runMCP`.

- [ ] **Step 5: Run full test suite**

Run: `cd /Users/chrispian/Projects-apps/vanta-conduit && go test ./... -count=1 2>&1 | tail -20`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add conduit.go internal/memory/store.go cmd/contextd/main.go internal/memory/write_test.go internal/memory/embed_test.go internal/mcpadapter/memory_tools_test.go tests/integration/memory_test.go
git commit -m "feat: wire config file loading and dedup threshold into conduit"
```

---

### Task 5: Backfill CLI Command

**Files:**
- Create: `cmd/contextd/backfill.go`
- Modify: `cmd/contextd/main.go`

- [ ] **Step 1: Create the backfill command**

Create `cmd/contextd/backfill.go`:

```go
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/hollis-labs/tesseract/internal/config"
	"github.com/hollis-labs/tesseract/internal/contextstore"
	"github.com/hollis-labs/tesseract/internal/memory"
)

func runBackfill(ctx context.Context, store *contextstore.Store, cfg config.Config, args []string, stdout, stderr *os.File) int {
	fs := flag.NewFlagSet("backfill-embeddings", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	namespace := fs.String("namespace", "", "filter by namespace (optional)")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}

	embedder := createEmbedder(cfg)
	if embedder == nil {
		fmt.Fprintln(stderr, "error: no embedding provider configured (check config.yaml and OPENAI_API_KEY)")
		return 1
	}

	ms := memory.NewStore(store.DB(), embedder, cfg.Embedding.Model, cfg.Dedup.SimilarityThreshold, nil)

	// Query unembedded revisions.
	query := `SELECT revision_id, namespace FROM memory_revisions WHERE embedding_vector IS NULL`
	var queryArgs []interface{}
	if *namespace != "" {
		query += ` AND namespace = ?`
		queryArgs = append(queryArgs, *namespace)
	}
	query += ` ORDER BY created_at ASC`

	rows, err := store.DB().QueryContext(ctx, query, queryArgs...)
	if err != nil {
		fmt.Fprintf(stderr, "error: query unembedded revisions: %v\n", err)
		return 1
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id, ns string
		if err := rows.Scan(&id, &ns); err != nil {
			fmt.Fprintf(stderr, "error: scan: %v\n", err)
			return 1
		}
		ids = append(ids, id)
	}
	if rows.Err() != nil {
		fmt.Fprintf(stderr, "error: %v\n", rows.Err())
		return 1
	}

	total := len(ids)
	if total == 0 {
		fmt.Fprintln(stdout, "No unembedded revisions found.")
		return 0
	}

	fmt.Fprintf(stdout, "Found %d unembedded revision(s). Embedding with %s...\n", total, cfg.Embedding.Model)

	var errCount int
	for i, id := range ids {
		if ctx.Err() != nil {
			fmt.Fprintf(stderr, "\nInterrupted after %d/%d revisions.\n", i, total)
			return 1
		}

		if err := ms.EmbedRevision(ctx, id, cfg.Embedding.Model); err != nil {
			fmt.Fprintf(stderr, "[%d/%d] error embedding %s: %v\n", i+1, total, id, err)
			errCount++
			continue
		}
		fmt.Fprintf(stdout, "[%d/%d] embedded %s\n", i+1, total, id)
	}

	fmt.Fprintf(stdout, "\nEmbedded %d revision(s) (%d errors).\n", total-errCount, errCount)
	return 0
}
```

- [ ] **Step 2: Add route in main.go**

In `cmd/contextd/main.go`, in the `run` function, add before the CLI fallthrough:

```go
	if len(args) > 0 && args[0] == "backfill-embeddings" {
		return runBackfill(ctx, store, conduitCfg, args[1:], stdout, stderr)
	}
```

Also extract the embedder creation into a helper function `createEmbedder(cfg config.Config) provider.Embedder` that both `runMCP` and `runBackfill` can use:

```go
func createEmbedder(cfg config.Config) provider.Embedder {
	if cfg.Embedding.Provider != "openai" {
		return nil
	}
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		return nil
	}
	return provider.NewOpenAI(apiKey) // verify exact constructor
}
```

- [ ] **Step 3: Run build**

Run: `cd /Users/chrispian/Projects-apps/vanta-conduit && go build ./cmd/contextd/`
Expected: BUILD OK

- [ ] **Step 4: Commit**

```bash
git add cmd/contextd/backfill.go cmd/contextd/main.go
git commit -m "feat: add contextd backfill-embeddings CLI command"
```

---

### Task 6: Semantic Dedup in WriteRevision

**Files:**
- Modify: `internal/memory/types.go`
- Create: `internal/memory/dedup.go`
- Create: `internal/memory/dedup_test.go`
- Modify: `internal/memory/write.go`

- [ ] **Step 1: Add Dedup fields to types**

In `internal/memory/types.go`, add to `WriteInput`:

```go
type WriteInput struct {
	// ... existing fields ...
	Dedup          string  // "none" (default), "semantic"
	DedupThreshold float64 // optional per-call override; 0 = use store default
}
```

Add to `Revision`:

```go
type Revision struct {
	// ... existing fields ...
	DedupMatch string `json:"dedup_match,omitempty"`
}
```

- [ ] **Step 2: Write the dedup matching test**

Create `internal/memory/dedup_test.go`:

```go
package memory_test

import (
	"context"
	"testing"

	"github.com/hollis-labs/tesseract/internal/memory"
)

func TestWriteRevision_SemanticDedup_SameKey(t *testing.T) {
	ms, cleanup := newTestStoreWithEmbedder(t)
	defer cleanup()

	ctx := context.Background()
	ns := "user/chrispian/memory"

	// Write and embed a revision.
	rev1, err := ms.WriteRevision(ctx, memory.WriteInput{
		Namespace:  ns,
		MemoryKey:  "dedup_test",
		Status:     memory.StatusDraft,
		Author:     memory.Author{AgentID: "test", AgentVersion: "1.0"},
		Trigger:    memory.TriggerManual,
		SessionID:  "s1",
		Origin:     memory.OriginUser,
		Confidence: 0.9,
		Tags:       []string{},
		Payload:    memory.Payload{Summary: "original memory about Go testing"},
	})
	if err != nil {
		t.Fatalf("write rev1: %v", err)
	}
	if err := ms.EmbedRevision(ctx, rev1.RevisionID, "test-model"); err != nil {
		t.Fatalf("embed rev1: %v", err)
	}

	// Write a similar revision with dedup enabled.
	rev2, err := ms.WriteRevision(ctx, memory.WriteInput{
		Namespace:  ns,
		MemoryKey:  "dedup_test",
		Status:     memory.StatusDraft,
		Author:     memory.Author{AgentID: "test", AgentVersion: "1.0"},
		Trigger:    memory.TriggerManual,
		SessionID:  "s1",
		Origin:     memory.OriginUser,
		Confidence: 0.9,
		Tags:       []string{},
		Payload:    memory.Payload{Summary: "updated memory about Go testing"},
		Dedup:      "semantic",
	})
	if err != nil {
		t.Fatalf("write rev2: %v", err)
	}

	// Same key → should auto-supersede.
	if rev2.Supersedes != rev1.RevisionID {
		t.Errorf("expected Supersedes=%s, got %s", rev1.RevisionID, rev2.Supersedes)
	}
	if rev2.DedupMatch != rev1.RevisionID {
		t.Errorf("expected DedupMatch=%s, got %s", rev1.RevisionID, rev2.DedupMatch)
	}
}

func TestWriteRevision_SemanticDedup_CrossKey(t *testing.T) {
	ms, cleanup := newTestStoreWithEmbedder(t)
	defer cleanup()

	ctx := context.Background()
	ns := "user/chrispian/memory"

	// Write and embed under key "alpha".
	rev1, err := ms.WriteRevision(ctx, memory.WriteInput{
		Namespace:  ns,
		MemoryKey:  "alpha",
		Status:     memory.StatusDraft,
		Author:     memory.Author{AgentID: "test", AgentVersion: "1.0"},
		Trigger:    memory.TriggerManual,
		SessionID:  "s1",
		Origin:     memory.OriginUser,
		Confidence: 0.9,
		Tags:       []string{},
		Payload:    memory.Payload{Summary: "some content"},
	})
	if err != nil {
		t.Fatalf("write rev1: %v", err)
	}
	if err := ms.EmbedRevision(ctx, rev1.RevisionID, "test-model"); err != nil {
		t.Fatalf("embed rev1: %v", err)
	}

	// Write under key "beta" with dedup.
	rev2, err := ms.WriteRevision(ctx, memory.WriteInput{
		Namespace:  ns,
		MemoryKey:  "beta",
		Status:     memory.StatusDraft,
		Author:     memory.Author{AgentID: "test", AgentVersion: "1.0"},
		Trigger:    memory.TriggerManual,
		SessionID:  "s1",
		Origin:     memory.OriginUser,
		Confidence: 0.9,
		Tags:       []string{},
		Payload:    memory.Payload{Summary: "similar content"},
		Dedup:      "semantic",
	})
	if err != nil {
		t.Fatalf("write rev2: %v", err)
	}

	// Cross-key → DedupMatch is set, but Supersedes is NOT set.
	if rev2.DedupMatch != rev1.RevisionID {
		t.Errorf("expected DedupMatch=%s, got %s", rev1.RevisionID, rev2.DedupMatch)
	}
	if rev2.Supersedes != "" {
		t.Errorf("expected empty Supersedes for cross-key dedup, got %s", rev2.Supersedes)
	}
}

func TestWriteRevision_SemanticDedup_NoMatch(t *testing.T) {
	ms, cleanup := newTestStoreWithEmbedder(t)
	defer cleanup()

	ctx := context.Background()

	// Write with dedup but no existing revisions to match against.
	rev, err := ms.WriteRevision(ctx, memory.WriteInput{
		Namespace:  "user/chrispian/memory",
		MemoryKey:  "unique",
		Status:     memory.StatusDraft,
		Author:     memory.Author{AgentID: "test", AgentVersion: "1.0"},
		Trigger:    memory.TriggerManual,
		SessionID:  "s1",
		Origin:     memory.OriginUser,
		Confidence: 0.9,
		Tags:       []string{},
		Payload:    memory.Payload{Summary: "totally unique"},
		Dedup:      "semantic",
	})
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if rev.DedupMatch != "" {
		t.Errorf("expected empty DedupMatch, got %s", rev.DedupMatch)
	}
}

func TestWriteRevision_NoDedup_Default(t *testing.T) {
	ms, cleanup := newTestStoreWithEmbedder(t)
	defer cleanup()

	ctx := context.Background()

	// Write without dedup — should work as before with no dedup fields set.
	rev, err := ms.WriteRevision(ctx, memory.WriteInput{
		Namespace:  "user/chrispian/memory",
		MemoryKey:  "no_dedup",
		Status:     memory.StatusDraft,
		Author:     memory.Author{AgentID: "test", AgentVersion: "1.0"},
		Trigger:    memory.TriggerManual,
		SessionID:  "s1",
		Origin:     memory.OriginUser,
		Confidence: 0.9,
		Tags:       []string{},
		Payload:    memory.Payload{Summary: "no dedup"},
	})
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if rev.DedupMatch != "" {
		t.Errorf("expected empty DedupMatch, got %s", rev.DedupMatch)
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `cd /Users/chrispian/Projects-apps/vanta-conduit && go test ./internal/memory/ -run "TestWriteRevision_SemanticDedup" -v`
Expected: FAIL — `DedupMatch` field doesn't exist, dedup logic not implemented

- [ ] **Step 4: Implement the dedup matching function**

Create `internal/memory/dedup.go`:

```go
package memory

import (
	"context"
	"fmt"
)

// findSemanticMatch searches for a similar revision in the target namespace.
// Returns the matching revision ID and whether a same-key match was found.
// Returns ("", false, nil) if no match above threshold.
func (s *Store) findSemanticMatch(ctx context.Context, namespace, memoryKey string, payloadText string, threshold float64) (matchRevisionID string, sameKey bool, err error) {
	if s.embedder == nil {
		return "", false, ErrEmbedderUnavailable
	}

	if payloadText == "" {
		return "", false, nil
	}

	results, err := s.Recall(ctx, RecallInput{
		Namespaces: []string{namespace},
		Ranking:    RankingSimilarity,
		Query:      payloadText,
		Limit:      1,
	})
	if err != nil {
		return "", false, fmt.Errorf("dedup recall: %w", err)
	}

	if len(results) == 0 {
		return "", false, nil
	}

	top := results[0]
	if top.Score < threshold {
		return "", false, nil
	}

	matchID := top.Revision.RevisionID
	isSameKey := memoryKey != "" && top.Revision.MemoryKey == memoryKey
	return matchID, isSameKey, nil
}
```

- [ ] **Step 5: Wire dedup into WriteRevision**

In `internal/memory/write.go`, add dedup logic **before** the transaction begins (line 41), after validation:

```go
	// Semantic dedup: if requested, search for similar existing revisions.
	var dedupMatch string
	if in.Dedup == "semantic" {
		if s.embedder == nil {
			return Revision{}, ErrEmbedderUnavailable
		}
		threshold := in.DedupThreshold
		if threshold == 0 {
			threshold = s.dedupThreshold
		}
		text := revisionEmbedText(Revision{Payload: in.Payload})
		matchID, sameKey, matchErr := s.findSemanticMatch(ctx, in.Namespace, in.MemoryKey, text, threshold)
		if matchErr != nil {
			return Revision{}, fmt.Errorf("semantic dedup: %w", matchErr)
		}
		if matchID != "" {
			dedupMatch = matchID
			if sameKey && in.Supersedes == "" {
				in.Supersedes = matchID
			}
		}
	}
```

After the revision is built (around line 185), set `DedupMatch`:

```go
	rev := Revision{
		// ... existing fields ...
		DedupMatch: dedupMatch,
	}
```

- [ ] **Step 6: Run dedup tests**

Run: `cd /Users/chrispian/Projects-apps/vanta-conduit && go test ./internal/memory/ -run "TestWriteRevision_SemanticDedup\|TestWriteRevision_NoDedup" -v`
Expected: PASS

- [ ] **Step 7: Run full test suite**

Run: `cd /Users/chrispian/Projects-apps/vanta-conduit && go test ./... -count=1 2>&1 | tail -20`
Expected: PASS

- [ ] **Step 8: Commit**

```bash
git add internal/memory/types.go internal/memory/dedup.go internal/memory/dedup_test.go internal/memory/write.go
git commit -m "feat(memory): add opt-in semantic dedup on write path"
```

---

### Task 7: MCP Tool Updates for Dedup

**Files:**
- Modify: `internal/mcpadapter/memory_tools.go`

- [ ] **Step 1: Add dedup parameters to memory_write tool registration**

In `internal/mcpadapter/memory_tools.go`, in `registerMemoryTools`, add to the `memory_write` tool definition (after the `payload_body` parameter):

```go
		mcp.WithString("dedup", mcp.Description("Dedup mode: none (default) or semantic")),
		mcp.WithNumber("dedup_threshold", mcp.Description("Similarity threshold override for semantic dedup (0 = use config default)")),
```

- [ ] **Step 2: Wire dedup parameters in handleMemoryWrite**

In `handleMemoryWrite`, add to the `WriteInput` construction:

```go
	in := memory.WriteInput{
		// ... existing fields ...
		Dedup:          req.GetString("dedup", ""),
		DedupThreshold: req.GetFloat("dedup_threshold", 0),
	}
```

- [ ] **Step 3: Run MCP adapter tests**

Run: `cd /Users/chrispian/Projects-apps/vanta-conduit && go test ./internal/mcpadapter/ -v`
Expected: PASS

- [ ] **Step 4: Run full test suite**

Run: `cd /Users/chrispian/Projects-apps/vanta-conduit && go test ./... -count=1 2>&1 | tail -20`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/mcpadapter/memory_tools.go
git commit -m "feat(mcp): add dedup and dedup_threshold parameters to memory_write tool"
```

---

### Task 8: Update Conduit Facade for Dedup

**Files:**
- Modify: `conduit.go`

- [ ] **Step 1: Update WriteMemory facade to pass through dedup fields**

The facade method `WriteMemory` already delegates to `memoryStore.WriteRevision(ctx, in)`. Since `WriteInput` now has `Dedup` and `DedupThreshold` fields, the facade works automatically — no code changes needed. Verify by reading the code.

- [ ] **Step 2: Run facade tests**

Run: `cd /Users/chrispian/Projects-apps/vanta-conduit && go test . -run "TestConduit" -v`
Expected: PASS

- [ ] **Step 3: Commit (if any changes were needed)**

Only commit if changes were made. If the facade already passes through correctly, skip this commit.

---

### Task 9: Update Boot Prompt

**Files:**
- Modify: `.agentrc/boot-prompt.md`

- [ ] **Step 1: Update track statuses**

Move "D-deferred. Backfill + semantic dedup" to completed. Update key technical knowledge section to include:
- Config file: `~/.conduit/config.yaml` for embedding provider/model and dedup threshold
- Backfill CLI: `contextd backfill-embeddings [--namespace=...]`
- Semantic dedup: opt-in via `Dedup: "semantic"` on WriteInput
- ULID: monotonic entropy source guarantees ordering
- Timestamps: RFC3339Nano precision

- [ ] **Step 2: Commit**

```bash
git add .agentrc/boot-prompt.md
git commit -m "docs: update boot prompt — backfill and semantic dedup complete"
```
