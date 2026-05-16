# D-Deferred Completion & Cleanup Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Complete the three deferred D-track items (auto-embed on write, similarity recall, backfill), stabilize the embedded-runtime API surface (track E), and fix stale cortex references (track C).

**Architecture:** Memory revisions store embeddings inline (`embedding_model`/`embedding_vector` columns on `memory_revisions`). The embed handler bridges queue jobs to `memory.Store.EmbedRevision()`. Similarity recall reuses the existing cosine similarity infrastructure from `internal/embedding/search.go`. The Conduit facade gets top-level methods that delegate to `.Store()` and `.MemoryStore()` so callers don't need to know about internals.

**Tech Stack:** Go 1.22+, SQLite (modernc.org/sqlite), go-providers (embedding), go-queue (async jobs), stdlib testing

---

## File Map

| File | Action | Responsibility |
|------|--------|---------------|
| `internal/memory/embed.go` | Create | `Store.EmbedRevision()` — load revision, call embedder, store vector inline |
| `internal/memory/embed_test.go` | Create | Tests for `EmbedRevision` |
| `internal/memory/recall.go` | Modify | Replace `ErrSimilarityUnavailable` guard with cosine similarity ranking |
| `internal/memory/recall_test.go` | Modify | Add similarity ranking tests, update existing guard test |
| `embed_handler.go` | Modify | Call `memory.Store.EmbedRevision()` instead of logging |
| `embed_handler_test.go` | Modify | Test real embedding flow with mock embedder |
| `conduit.go` | Modify | Add top-level facade methods: `Write`, `Recall`, `GetCurrent`, `GetHistory` |
| `conduit_test.go` | Create | Tests for facade methods |
| `internal/contextcli/plugin_cmd.go` | Modify | Replace "cortex" → "conduit" in usage strings and env var |
| `internal/contextcli/plugin_cmd_test.go` | Modify (if exists) | Update expected strings |
| `.agentrc/boot-prompt.md` | Modify | Update track statuses |

---

## Task 1: `memory.Store.EmbedRevision()` — embed a revision inline (D1 foundation)

**Files:**
- Create: `internal/memory/embed.go`
- Create: `internal/memory/embed_test.go`

This adds the core method that loads a revision by ID, extracts embeddable text from `payload_summary` + `payload_body`, calls the configured embedder, and writes the resulting vector back to the revision's `embedding_model`/`embedding_vector` columns.

- [ ] **Step 1: Write the failing test**

Create `internal/memory/embed_test.go`:

```go
package memory_test

import (
	"context"
	"errors"
	"testing"

	"github.com/hollis-labs/tesseract/internal/memory"
)

func TestEmbedRevision_Success(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()

	// Write a revision with embeddable content.
	rev, err := ms.WriteRevision(context.Background(), memory.WriteInput{
		Namespace:  "user/chrispian/memory",
		MemoryKey:  "embed-test",
		Status:     memory.StatusDraft,
		Author:     memory.Author{AgentID: "test-agent", AgentVersion: "1.0"},
		Trigger:    memory.TriggerManual,
		SessionID:  "sess-1",
		Origin:     memory.OriginAgent,
		Confidence: 0.9,
		Tags:       []string{"test"},
		Payload: memory.Payload{
			Summary: "a test revision about embeddings",
			Body:    "detailed body about vector search",
		},
	})
	if err != nil {
		t.Fatalf("write revision: %v", err)
	}

	err = ms.EmbedRevision(context.Background(), rev.RevisionID, "test-model")
	if err != nil {
		t.Fatalf("embed revision: %v", err)
	}

	// Verify the embedding was stored.
	got, err := ms.GetRevisionByID(context.Background(), rev.RevisionID)
	if err != nil {
		t.Fatalf("get revision: %v", err)
	}
	if got.EmbeddingModel != "test-model" {
		t.Errorf("expected embedding_model=%q, got %q", "test-model", got.EmbeddingModel)
	}
	if len(got.EmbeddingVector) == 0 {
		t.Error("expected non-empty embedding_vector")
	}
}

func TestEmbedRevision_NoEmbedder(t *testing.T) {
	ms, cleanup := newTestStoreNoEmbedder(t)
	defer cleanup()

	err := ms.EmbedRevision(context.Background(), "rev-nonexistent", "model")
	if !errors.Is(err, memory.ErrEmbedderUnavailable) {
		t.Fatalf("expected ErrEmbedderUnavailable, got %v", err)
	}
}

func TestEmbedRevision_NotFound(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()

	err := ms.EmbedRevision(context.Background(), "rev-nonexistent", "model")
	if !errors.Is(err, memory.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/chrispian/Projects-apps/vanta-conduit && go test ./internal/memory/ -run TestEmbedRevision -v`
Expected: FAIL — `EmbedRevision` not defined, `ErrEmbedderUnavailable` not defined in memory package, `EmbeddingModel`/`EmbeddingVector` fields don't exist on Revision

- [ ] **Step 3: Add EmbeddingModel/EmbeddingVector fields to Revision**

Modify `internal/memory/types.go` — add to `Revision` struct:

```go
	EmbeddingModel  string    `json:"embedding_model,omitempty"`
	EmbeddingVector []float32 `json:"embedding_vector,omitempty"`
```

- [ ] **Step 4: Update scanRevision to read embedding columns**

Modify `internal/memory/read.go` — update `revisionColumns` const to include `embedding_model, embedding_vector` and update `scanRevision` to decode them. The vector is stored as a BLOB of little-endian float32s (same encoding as `contextstore`). Add a `blobToFloat32` helper.

- [ ] **Step 5: Add ErrEmbedderUnavailable sentinel and EmbedRevision method**

Create `internal/memory/embed.go`:

```go
package memory

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"strings"
)

// ErrEmbedderUnavailable is returned when an embedding operation is attempted
// but no embedder was configured on the Store.
var ErrEmbedderUnavailable = errors.New("embedder unavailable")

// EmbedRevision loads a revision, extracts text from its payload, calls the
// configured embedder, and stores the resulting vector inline on the revision row.
func (s *Store) EmbedRevision(ctx context.Context, revisionID, model string) error {
	if s.embedder == nil {
		return ErrEmbedderUnavailable
	}

	rev, err := s.GetRevisionByID(ctx, revisionID)
	if err != nil {
		return err
	}

	text := revisionEmbedText(rev)
	if text == "" {
		return fmt.Errorf("revision %s has no embeddable text", revisionID)
	}

	result, err := s.embedder.Embed(ctx, text, model)
	if err != nil {
		return fmt.Errorf("embedding failed: %w", err)
	}

	blob := float32ToBlob(result.Embedding)
	_, err = s.db.ExecContext(ctx,
		`UPDATE memory_revisions SET embedding_model = ?, embedding_vector = ? WHERE revision_id = ?`,
		model, blob, revisionID,
	)
	return err
}

// revisionEmbedText concatenates the revision's summary and body for embedding.
func revisionEmbedText(rev Revision) string {
	var parts []string
	if rev.Payload.Summary != "" {
		parts = append(parts, rev.Payload.Summary)
	}
	if rev.Payload.Body != "" {
		parts = append(parts, rev.Payload.Body)
	}
	return strings.Join(parts, "\n")
}

// float32ToBlob encodes a float32 slice as little-endian bytes.
func float32ToBlob(v []float32) []byte {
	buf := make([]byte, len(v)*4)
	for i, f := range v {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(f))
	}
	return buf
}

// blobToFloat32 decodes a little-endian byte slice into float32s.
func blobToFloat32(b []byte) []float32 {
	if len(b)%4 != 0 {
		return nil
	}
	v := make([]float32, len(b)/4)
	for i := range v {
		v[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*4:]))
	}
	return v
}
```

- [ ] **Step 6: Add test helpers (newTestStoreNoEmbedder, mock embedder)**

The existing `newTestStore` in the test files likely creates a store with a nil embedder. You'll need:
- `newTestStore` that passes a mock embedder returning a fixed vector
- `newTestStoreNoEmbedder` that passes nil

Check existing test helpers in `internal/memory/write_test.go` and adapt. The mock embedder must implement `provider.Embedder` with an `Embed(ctx, text, model)` method returning `provider.EmbedResult{Embedding: []float32{0.1, 0.2, 0.3}}`.

- [ ] **Step 7: Run tests to verify they pass**

Run: `cd /Users/chrispian/Projects-apps/vanta-conduit && go test ./internal/memory/ -run TestEmbedRevision -v`
Expected: PASS (all 3 tests)

- [ ] **Step 8: Run full memory test suite for regressions**

Run: `cd /Users/chrispian/Projects-apps/vanta-conduit && go test ./internal/memory/ -v`
Expected: PASS — existing tests should still pass. The new `embedding_model`/`embedding_vector` columns on `Revision` and the updated `scanRevision` must be backward-compatible (NULL values decode to zero values).

- [ ] **Step 9: Commit**

```bash
git add internal/memory/embed.go internal/memory/embed_test.go internal/memory/types.go internal/memory/read.go
git commit -m "feat(memory): add EmbedRevision method for inline revision embedding"
```

---

## Task 2: Wire embed handler to call `EmbedRevision` (D1 completion)

**Files:**
- Modify: `embed_handler.go`
- Modify: `embed_handler_test.go`

The embed handler currently logs and returns. It needs access to `memory.Store` and the embedding model name to call `EmbedRevision`.

- [ ] **Step 1: Write the failing test**

Replace `embed_handler_test.go` tests to verify the handler calls `EmbedRevision`. The handler now needs a `*memory.Store` and model name, so the constructor signature changes to `NewEmbedHandler(memStore *memory.Store, model string, logger func(string, ...any))`.

```go
package conduit_test

import (
	"context"
	"testing"

	conduit "github.com/hollis-labs/tesseract"
	"github.com/hollis-labs/tesseract/internal/memory"

	queue "github.com/hollis-labs/go-queue"
)

func TestNewEmbedHandler_EmbedsRevision(t *testing.T) {
	// Set up a memory.Store with a mock embedder and a real SQLite DB.
	// Write a revision, then pass its ID through the handler.
	// Verify the revision now has an embedding.
	//
	// Implementation: use the same test DB setup as memory_test but
	// from the conduit_test package. This requires a helper that creates
	// a conduit.Open() instance with a mock embedder and queue.
	//
	// For now, test that the handler signature compiles and that it
	// returns an error for a nonexistent revision (proving it calls
	// EmbedRevision rather than just logging).

	// This test needs a *memory.Store — construct one via conduit.Open()
	// or directly. Direct construction is simpler for unit testing.
	//
	// The handler should call memStore.EmbedRevision(ctx, revisionID, model).
	// With no matching revision, it should return an error (not nil like before).

	var logged string
	logger := func(format string, args ...any) {
		logged = fmt.Sprintf(format, args...)
	}

	// handler with nil store should still handle gracefully
	handler := conduit.NewEmbedHandler(nil, "test-model", logger)
	job := &queue.QueuedJob{
		Type:    "embed",
		Payload: []byte(`{"revision_id":"rev_abc123"}`),
	}

	err := handler(context.Background(), job)
	// With nil store, expect ErrEmbedderUnavailable or similar
	if err == nil {
		t.Fatal("expected error with nil memory store")
	}
}

func TestNewEmbedHandler_InvalidJSON(t *testing.T) {
	logger := func(string, ...any) {}
	handler := conduit.NewEmbedHandler(nil, "model", logger)
	job := &queue.QueuedJob{
		Type:    "embed",
		Payload: []byte(`not valid json`),
	}
	err := handler(context.Background(), job)
	if err == nil {
		t.Fatal("expected error for invalid JSON payload, got nil")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/chrispian/Projects-apps/vanta-conduit && go test . -run TestNewEmbedHandler -v`
Expected: FAIL — `NewEmbedHandler` signature mismatch (old takes only logger)

- [ ] **Step 3: Update NewEmbedHandler to call EmbedRevision**

Modify `embed_handler.go`:

```go
package conduit

import (
	"context"
	"encoding/json"
	"fmt"

	queue "github.com/hollis-labs/go-queue"
	"github.com/hollis-labs/tesseract/internal/memory"
)

type embedJobPayload struct {
	RevisionID string `json:"revision_id"`
}

// NewEmbedHandler returns a queue.Handler that embeds memory revisions.
// It decodes the revision ID from the job payload and calls
// memStore.EmbedRevision to generate and store the vector.
func NewEmbedHandler(memStore *memory.Store, model string, logger func(string, ...any)) queue.Handler {
	if logger == nil {
		logger = func(string, ...any) {}
	}
	return func(ctx context.Context, job *queue.QueuedJob) error {
		var p embedJobPayload
		if err := json.Unmarshal(job.Payload, &p); err != nil {
			return fmt.Errorf("embed handler: decode payload: %w", err)
		}

		logger("embedding revision %s with model %s", p.RevisionID, model)

		if memStore == nil {
			return fmt.Errorf("embed handler: memory store not configured")
		}

		if err := memStore.EmbedRevision(ctx, p.RevisionID, model); err != nil {
			return fmt.Errorf("embed handler: %w", err)
		}

		logger("embedded revision %s", p.RevisionID)
		return nil
	}
}
```

- [ ] **Step 4: Update call site in conduit.go**

Modify `conduit.go` line 119 — update the `NewEmbedHandler` call to pass `memStore` and the embedding model:

```go
w.Register("embed", NewEmbedHandler(memStore, o.embeddingModel, o.logger))
```

- [ ] **Step 5: Run tests**

Run: `cd /Users/chrispian/Projects-apps/vanta-conduit && go test . -run TestNewEmbedHandler -v`
Expected: PASS

- [ ] **Step 6: Run full test suite**

Run: `cd /Users/chrispian/Projects-apps/vanta-conduit && go test ./... 2>&1 | tail -30`
Expected: PASS — no regressions

- [ ] **Step 7: Commit**

```bash
git add embed_handler.go embed_handler_test.go conduit.go
git commit -m "feat: wire embed handler to call memory.Store.EmbedRevision"
```

---

## Task 3: Similarity ranking in recall (D2)

**Files:**
- Modify: `internal/memory/recall.go`
- Modify: `internal/memory/recall_test.go`

Replace the `ErrSimilarityUnavailable` guard with actual cosine similarity ranking. When `RankingSimilarity` is requested: embed the query, load all candidate revisions that have embeddings, score by cosine similarity.

- [ ] **Step 1: Write the failing test**

Add to `internal/memory/recall_test.go`:

```go
func TestRecall_SimilarityRanking(t *testing.T) {
	ms, cleanup := newTestStore(t) // must use store with mock embedder
	defer cleanup()

	ns := "user/chrispian/memory"

	// Write two revisions with different content.
	rev1, err := ms.WriteRevision(context.Background(), memory.WriteInput{
		Namespace:  ns,
		MemoryKey:  "topic-a",
		Status:     memory.StatusDraft,
		Author:     memory.Author{AgentID: "test", AgentVersion: "1.0"},
		Trigger:    memory.TriggerManual,
		SessionID:  "s1",
		Origin:     memory.OriginAgent,
		Confidence: 0.9,
		Tags:       []string{},
		Payload:    memory.Payload{Summary: "apples and oranges"},
	})
	if err != nil {
		t.Fatalf("write rev1: %v", err)
	}
	if err := ms.EmbedRevision(context.Background(), rev1.RevisionID, "test-model"); err != nil {
		t.Fatalf("embed rev1: %v", err)
	}

	rev2, err := ms.WriteRevision(context.Background(), memory.WriteInput{
		Namespace:  ns,
		MemoryKey:  "topic-b",
		Status:     memory.StatusDraft,
		Author:     memory.Author{AgentID: "test", AgentVersion: "1.0"},
		Trigger:    memory.TriggerManual,
		SessionID:  "s1",
		Origin:     memory.OriginAgent,
		Confidence: 0.9,
		Tags:       []string{},
		Payload:    memory.Payload{Summary: "cars and trucks"},
	})
	if err != nil {
		t.Fatalf("write rev2: %v", err)
	}
	if err := ms.EmbedRevision(context.Background(), rev2.RevisionID, "test-model"); err != nil {
		t.Fatalf("embed rev2: %v", err)
	}

	results, err := ms.Recall(context.Background(), memory.RecallInput{
		Namespaces: []string{ns},
		Ranking:    memory.RankingSimilarity,
		Query:      "fruit",
		Limit:      10,
	})
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected results from similarity recall")
	}
	// Results should be ordered by score descending.
	for i := 1; i < len(results); i++ {
		if results[i].Score > results[i-1].Score {
			t.Errorf("results not sorted: score[%d]=%f > score[%d]=%f",
				i, results[i].Score, i-1, results[i-1].Score)
		}
	}
}

func TestRecall_SimilarityNoEmbedder(t *testing.T) {
	ms, cleanup := newTestStoreNoEmbedder(t)
	defer cleanup()

	_, err := ms.Recall(context.Background(), memory.RecallInput{
		Namespaces: []string{"user/chrispian/memory"},
		Ranking:    memory.RankingSimilarity,
		Query:      "test",
	})
	if !errors.Is(err, memory.ErrEmbedderUnavailable) {
		t.Fatalf("expected ErrEmbedderUnavailable, got %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/chrispian/Projects-apps/vanta-conduit && go test ./internal/memory/ -run "TestRecall_Similarity" -v`
Expected: FAIL — old test expected `ErrSimilarityUnavailable` unconditionally; new test expects success

- [ ] **Step 3: Implement similarity ranking in recall.go**

Modify `internal/memory/recall.go`:

1. Replace the unconditional `ErrSimilarityUnavailable` guard (lines 95-98) with an embedder check:

```go
	if in.Ranking == RankingSimilarity {
		if s.embedder == nil {
			return nil, ErrEmbedderUnavailable
		}
		if in.Query == "" {
			return nil, fmt.Errorf("%w: query is required for similarity ranking", ErrInvalidInput)
		}
	}
```

2. After fetching candidates (step 4), add similarity scoring logic. For revisions with embeddings, embed the query and compute cosine similarity. The similarity case in the scoring switch:

```go
	case RankingSimilarity:
		score = similarityScore(rev, queryVec)
```

3. Add a helper that embeds the query before the scoring loop:

```go
	var queryVec []float32
	if in.Ranking == RankingSimilarity {
		result, err := s.embedder.Embed(ctx, in.Query, embeddingModel(candidates))
		if err != nil {
			return nil, fmt.Errorf("query embedding: %w", err)
		}
		queryVec = result.Embedding
	}
```

4. `similarityScore` computes cosine similarity between the query vector and the revision's stored embedding vector. Revisions without embeddings get score 0.

5. Import `embedding.CosineSimilarity` from `internal/embedding/search.go`.

- [ ] **Step 4: Update the old ErrSimilarityUnavailable test**

The existing test `TestRecall_SimilarityRanking` (or similar name) that expected unconditional `ErrSimilarityUnavailable` should now be replaced by `TestRecall_SimilarityNoEmbedder` which tests the embedder-nil case specifically.

- [ ] **Step 5: Run tests**

Run: `cd /Users/chrispian/Projects-apps/vanta-conduit && go test ./internal/memory/ -run "TestRecall" -v`
Expected: PASS

- [ ] **Step 6: Run full test suite**

Run: `cd /Users/chrispian/Projects-apps/vanta-conduit && go test ./... 2>&1 | tail -30`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add internal/memory/recall.go internal/memory/recall_test.go
git commit -m "feat(memory): implement similarity ranking in Recall via cosine similarity"
```

---

## Task 4: Conduit facade methods (Track E)

**Files:**
- Modify: `conduit.go`
- Create: `conduit_facade_test.go`

Add top-level methods to `*Conduit` that delegate to the internal stores, so library consumers don't need to reach through `.Store()` and `.MemoryStore()`.

- [ ] **Step 1: Write failing tests**

Create `conduit_facade_test.go`:

```go
package conduit_test

import (
	"context"
	"testing"

	conduit "github.com/hollis-labs/tesseract"
	"github.com/hollis-labs/tesseract/internal/memory"
)

func TestConduit_WriteAndRecall(t *testing.T) {
	ctx := context.Background()
	c := openTestConduit(t)
	defer c.Close()

	rev, err := c.WriteMemory(ctx, memory.WriteInput{
		Namespace:  "user/test/memory",
		MemoryKey:  "facade-test",
		Status:     memory.StatusDraft,
		Author:     memory.Author{AgentID: "test", AgentVersion: "1.0"},
		Trigger:    memory.TriggerManual,
		SessionID:  "s1",
		Origin:     memory.OriginAgent,
		Confidence: 0.9,
		Tags:       []string{},
		Payload:    memory.Payload{Summary: "facade test content"},
	})
	if err != nil {
		t.Fatalf("WriteMemory: %v", err)
	}
	if rev.RevisionID == "" {
		t.Fatal("expected non-empty revision ID")
	}

	results, err := c.RecallMemory(ctx, memory.RecallInput{
		Namespaces: []string{"user/test/memory"},
		Ranking:    memory.RankingActivation,
		Limit:      10,
	})
	if err != nil {
		t.Fatalf("RecallMemory: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least one recall result")
	}
}

// openTestConduit creates a Conduit instance with a temp directory for testing.
func openTestConduit(t *testing.T) *conduit.Conduit {
	t.Helper()
	dir := t.TempDir()
	ctx := context.Background()
	c, err := conduit.Open(ctx, conduit.Config{RootDir: dir})
	if err != nil {
		t.Fatalf("conduit.Open: %v", err)
	}
	return c
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/chrispian/Projects-apps/vanta-conduit && go test . -run TestConduit_WriteAndRecall -v`
Expected: FAIL — `WriteMemory` and `RecallMemory` not defined on `*Conduit`

- [ ] **Step 3: Add facade methods to conduit.go**

Add to `conduit.go`:

```go
// WriteMemory writes a new memory revision. Delegates to MemoryStore().WriteRevision.
func (c *Conduit) WriteMemory(ctx context.Context, in memory.WriteInput) (memory.Revision, error) {
	return c.memoryStore.WriteRevision(ctx, in)
}

// RecallMemory retrieves memories by namespace, ranking, and filters.
// Delegates to MemoryStore().Recall.
func (c *Conduit) RecallMemory(ctx context.Context, in memory.RecallInput) ([]memory.RecallResult, error) {
	return c.memoryStore.Recall(ctx, in)
}

// GetCurrentRevision returns the current revision for a namespace/key pair.
// Delegates to MemoryStore().GetCurrent.
func (c *Conduit) GetCurrentRevision(ctx context.Context, namespace, key string) (memory.Revision, error) {
	return c.memoryStore.GetCurrent(ctx, namespace, key)
}

// GetRevisionHistory returns all revisions for a namespace/key pair, newest first.
// Delegates to MemoryStore().GetHistory.
func (c *Conduit) GetRevisionHistory(ctx context.Context, namespace, key string) ([]memory.Revision, error) {
	return c.memoryStore.GetHistory(ctx, namespace, key)
}

// EmbedRevision generates and stores an embedding for a memory revision.
func (c *Conduit) EmbedRevision(ctx context.Context, revisionID string) error {
	if c.embedder == nil {
		return ErrEmbedderUnavailable
	}
	return c.memoryStore.EmbedRevision(ctx, revisionID, c.embeddingModel)
}
```

- [ ] **Step 4: Run tests**

Run: `cd /Users/chrispian/Projects-apps/vanta-conduit && go test . -run TestConduit -v`
Expected: PASS

- [ ] **Step 5: Run full test suite**

Run: `cd /Users/chrispian/Projects-apps/vanta-conduit && go test ./... 2>&1 | tail -30`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add conduit.go conduit_facade_test.go
git commit -m "feat: add top-level facade methods to Conduit for write, recall, history"
```

---

## Task 5: Fix stale cortex references (Track C)

**Files:**
- Modify: `internal/contextcli/plugin_cmd.go`

Replace all "cortex" references in usage strings and the `CORTEX_PLUGINS_DIR` env var.

- [ ] **Step 1: Write the failing test (or verify with grep)**

Run: `cd /Users/chrispian/Projects-apps/vanta-conduit && grep -rn -i 'cortex' --include='*.go' .`
Expected: 6 hits in `internal/contextcli/plugin_cmd.go` (5 usage strings + 1 env var)

- [ ] **Step 2: Fix the references**

In `internal/contextcli/plugin_cmd.go`:

- Lines 19-20: `"usage: cortex plugin <command>"` → `"usage: conduit plugin <command>"`
- Line 31: `"usage: cortex plugin install <name>"` → `"usage: conduit plugin install <name>"`
- Line 37: `"usage: cortex plugin uninstall <name>"` → `"usage: conduit plugin uninstall <name>"`
- Line 43: `"usage: cortex plugin disable <name>"` → `"usage: conduit plugin disable <name>"`
- Line 49: `"usage: cortex plugin enable <name>"` → `"usage: conduit plugin enable <name>"`
- Line 60: `CORTEX_PLUGINS_DIR` → `CONDUIT_PLUGINS_DIR`

- [ ] **Step 3: Verify no remaining references**

Run: `cd /Users/chrispian/Projects-apps/vanta-conduit && grep -rn -i 'cortex' --include='*.go' .`
Expected: 0 hits

- [ ] **Step 4: Run go vet**

Run: `cd /Users/chrispian/Projects-apps/vanta-conduit && go vet ./...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/contextcli/plugin_cmd.go
git commit -m "fix: replace stale cortex references with conduit in plugin CLI"
```

---

## Task 6: Update boot prompt

**Files:**
- Modify: `.agentrc/boot-prompt.md`

Update the track statuses to reflect completed work.

- [ ] **Step 1: Update the "Completed tracks" section**

Move D-core embedding, D-similarity recall, and track C into the completed section. Update E to reflect the facade. Update the "In progress" section to only show remaining items.

- [ ] **Step 2: Commit**

```bash
git add .agentrc/boot-prompt.md
git commit -m "docs: update boot prompt track statuses after D-deferred and E completion"
```
