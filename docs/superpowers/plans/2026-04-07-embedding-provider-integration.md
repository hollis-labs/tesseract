> **Note:** This document was written when the project was named Cortex. It has since been renamed to Vanta Conduit.

# Embedding Provider Integration — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Wire Cortex to real embedding providers via the shared `provider.Embedder` interface from Nanite's `pkg/provider`, consolidate duplicate interfaces, and expose library-level embed/search methods for embedded hosts.

**Architecture:** Cortex imports `github.com/hollis-labs/nanite/pkg/provider` directly. A top-level `cortex.Open()` with functional options becomes the library entry point. MCP adapter delegates to library methods. Memory subsystem uses the same shared provider type.

**Tech Stack:** Go, SQLite, `github.com/hollis-labs/nanite/pkg/provider`

---

## File Map

### New files
- `cortex.go` — top-level library handle (`Cortex` struct, `Open()`, `Close()`, functional options)
- `cortex_test.go` — tests for `Open()`, `Close()`, option wiring
- `embed.go` — library-level `Embed()` and `Search()` methods on `*Cortex`
- `embed_test.go` — tests for `Embed()` and `Search()` library methods

### Deleted files
- `internal/memory/embedder.go` — `Embedder` interface, `NoopEmbedder`, `ErrEmbedderUnavailable`
- `internal/embedding/provider.go` — local `Provider` interface
- `internal/embedding/ollama.go` — `OllamaProvider` (shared package handles this)

### Modified files
- `go.mod` — add `github.com/hollis-labs/nanite` dependency (replace directive for local dev)
- `internal/embedding/mock.go` — update `MockProvider` to satisfy `provider.Embedder` interface
- `internal/embedding/embedding_test.go` — update mock provider tests for new signature
- `internal/memory/store.go` — change `NewStore()` to accept `provider.Embedder` instead of `memory.Embedder`
- `internal/memory/stubs_test.go` — remove `TestNoopEmbedderReturnsUnavailable`, keep queue test
- `internal/memory/write_test.go` — update `NewStore()` call (nil instead of `NoopEmbedder{}`)
- `internal/mcpadapter/adapter.go` — change `EmbeddingProvider` field type, add `Cortex` handle reference
- `internal/mcpadapter/embedding_tools.go` — update to use model-parametric calls
- `internal/mcpadapter/embedding_tools_test.go` — update mock provider usage
- `internal/mcpadapter/bulk_tools.go` — update embedding calls to model-parametric
- `internal/mcpadapter/rag_tools.go` — update embedding calls to model-parametric
- `internal/mcpadapter/typed_tools.go` — update embedding calls to model-parametric
- `internal/mcpadapter/memory_tools_test.go` — update `NewStore()` call
- `tests/integration/memory_test.go` — update `NewStore()` call
- `cmd/contextd/main.go` — wire through `cortex.Open()` or update `NewStore()` call

---

### Task 1: Add shared provider dependency

**Files:**
- Modify: `go.mod`

- [ ] **Step 1: Add nanite dependency with replace directive**

Add the nanite module to `go.mod` with a local replace directive for development:

```
require (
    github.com/hollis-labs/nanite v0.0.0
)

replace github.com/hollis-labs/nanite => ../../nanite
```

- [ ] **Step 2: Run go mod tidy**

Run: `go mod tidy`
Expected: Module resolves successfully. The `nanite` module is found via the replace directive.

- [ ] **Step 3: Verify the import resolves**

Run: `go list github.com/hollis-labs/nanite/pkg/provider`
Expected: `github.com/hollis-labs/nanite/pkg/provider` — no errors.

- [ ] **Step 4: Commit**

```bash
git add go.mod go.sum
git commit -m "build: add nanite dependency for shared provider package"
```

---

### Task 2: Update MockProvider to satisfy shared Embedder interface

**Files:**
- Modify: `internal/embedding/mock.go`
- Modify: `internal/embedding/embedding_test.go`

- [ ] **Step 1: Write the failing test — MockProvider satisfies provider.Embedder**

Update `internal/embedding/embedding_test.go`. Add a compile-time assertion and update the test to use the new signature:

```go
package embedding

import (
    "context"
    "math"
    "testing"

    "github.com/hollis-labs/nanite/pkg/provider"
)

// Compile-time assertion.
var _ provider.Embedder = (*MockProvider)(nil)

func TestMockProvider_Deterministic(t *testing.T) {
    p := NewMockProvider(768)

    v1, err := p.Embed(context.Background(), "hello world", "mock-embed")
    if err != nil {
        t.Fatalf("embed: %v", err)
    }
    if len(v1.Embedding) != 768 {
        t.Fatalf("len = %d, want 768", len(v1.Embedding))
    }

    // Same text + model → same vector.
    v2, err := p.Embed(context.Background(), "hello world", "mock-embed")
    if err != nil {
        t.Fatalf("embed: %v", err)
    }
    for i := range v1.Embedding {
        if v1.Embedding[i] != v2.Embedding[i] {
            t.Fatalf("non-deterministic at index %d: %f != %f", i, v1.Embedding[i], v2.Embedding[i])
        }
    }

    // Different text → different vector.
    v3, err := p.Embed(context.Background(), "goodbye world", "mock-embed")
    if err != nil {
        t.Fatalf("embed: %v", err)
    }
    same := true
    for i := range v1.Embedding {
        if v1.Embedding[i] != v3.Embedding[i] {
            same = false
            break
        }
    }
    if same {
        t.Error("different texts produced identical vectors")
    }
}

func TestMockProvider_UnitVector(t *testing.T) {
    p := NewMockProvider(128)
    r, err := p.Embed(context.Background(), "test", "mock-embed")
    if err != nil {
        t.Fatalf("embed: %v", err)
    }

    var norm float64
    for _, f := range r.Embedding {
        norm += float64(f) * float64(f)
    }
    norm = math.Sqrt(norm)
    if math.Abs(norm-1.0) > 0.001 {
        t.Errorf("vector norm = %f, want ~1.0", norm)
    }
}

func TestMockProvider_EmbedBatch(t *testing.T) {
    p := NewMockProvider(128)
    results, err := p.EmbedBatch(context.Background(), []string{"hello", "world"}, "mock-embed")
    if err != nil {
        t.Fatalf("embed batch: %v", err)
    }
    if len(results) != 2 {
        t.Fatalf("got %d results, want 2", len(results))
    }
    if len(results[0].Embedding) != 128 {
        t.Errorf("result[0] dimensions = %d, want 128", len(results[0].Embedding))
    }
}

func TestMockProvider_EmbeddingDimensions(t *testing.T) {
    p := NewMockProvider(768)
    if d := p.EmbeddingDimensions("mock-embed"); d != 768 {
        t.Errorf("dimensions = %d, want 768", d)
    }
    // Unknown model returns the configured default.
    if d := p.EmbeddingDimensions("unknown"); d != 768 {
        t.Errorf("unknown model dimensions = %d, want 768", d)
    }
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /Users/chrispian/Projects-apps/fragments-engine/cortex && go test ./internal/embedding/ -run TestMockProvider -v`
Expected: FAIL — `MockProvider` does not have the new method signatures.

- [ ] **Step 3: Update MockProvider implementation**

Rewrite `internal/embedding/mock.go`:

```go
package embedding

import (
    "context"
    "crypto/sha256"
    "encoding/binary"
    "math"

    "github.com/hollis-labs/nanite/pkg/provider"
)

// MockProvider generates deterministic embeddings for testing.
// The same text always produces the same vector, enabling reproducible tests.
// Satisfies provider.Embedder.
type MockProvider struct {
    dimensions int
}

// NewMockProvider creates a mock that generates deterministic vectors.
func NewMockProvider(dimensions int) *MockProvider {
    return &MockProvider{dimensions: dimensions}
}

func (p *MockProvider) EmbeddingDimensions(_ string) int { return p.dimensions }

func (p *MockProvider) Embed(_ context.Context, text string, _ string) (*provider.EmbeddingResult, error) {
    vec := deterministicVector(text, p.dimensions)
    return &provider.EmbeddingResult{Embedding: vec, TokenCount: len(text) / 4}, nil
}

func (p *MockProvider) EmbedBatch(_ context.Context, texts []string, model string) ([]provider.EmbeddingResult, error) {
    results := make([]provider.EmbeddingResult, len(texts))
    for i, text := range texts {
        vec := deterministicVector(text, p.dimensions)
        results[i] = provider.EmbeddingResult{Embedding: vec, TokenCount: len(text) / 4}
    }
    return results, nil
}

func deterministicVector(text string, dimensions int) []float32 {
    hash := sha256.Sum256([]byte(text))
    vec := make([]float32, dimensions)
    for i := range vec {
        idx := (i * 4) % len(hash)
        bits := binary.LittleEndian.Uint32(hash[idx : idx+4])
        vec[i] = float32(bits)/float32(math.MaxUint32)*2 - 1
    }
    var norm float32
    for _, v := range vec {
        norm += v * v
    }
    norm = float32(math.Sqrt(float64(norm)))
    if norm > 0 {
        for i := range vec {
            vec[i] /= norm
        }
    }
    return vec
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/chrispian/Projects-apps/fragments-engine/cortex && go test ./internal/embedding/ -run TestMockProvider -v`
Expected: All `TestMockProvider_*` tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/embedding/mock.go internal/embedding/embedding_test.go
git commit -m "feat(embedding): update MockProvider to satisfy provider.Embedder interface"
```

---

### Task 3: Delete Cortex-local provider code

**Files:**
- Delete: `internal/memory/embedder.go`
- Delete: `internal/embedding/provider.go`
- Delete: `internal/embedding/ollama.go`
- Modify: `internal/memory/stubs_test.go` — remove embedder test
- Modify: `internal/memory/store.go` — update `NewStore()` signature
- Modify: `internal/memory/write_test.go` — update `NewStore()` call

- [ ] **Step 1: Delete the three files**

```bash
cd /Users/chrispian/Projects-apps/fragments-engine/cortex
rm internal/memory/embedder.go
rm internal/embedding/provider.go
rm internal/embedding/ollama.go
```

- [ ] **Step 2: Update memory.Store to accept provider.Embedder**

Replace `internal/memory/store.go`:

```go
package memory

import (
    "database/sql"

    "github.com/hollis-labs/nanite/pkg/provider"
)

// Store is the memory subsystem's storage handle. It shares a *sql.DB with
// contextstore.Store but owns its own read/write paths against the memory_*
// tables.
type Store struct {
    db       *sql.DB
    embedder provider.Embedder
    queue    JobQueue
}

// NewStore constructs a memory.Store bound to the given database. The
// embedder may be nil (disables embedding). The queue parameter may be
// NoopQueue{} during D-core; real implementations are swapped in when
// Track I lands.
func NewStore(db *sql.DB, embedder provider.Embedder, queue JobQueue) *Store {
    if queue == nil {
        queue = NoopQueue{}
    }
    return &Store{db: db, embedder: embedder, queue: queue}
}

// DB returns the underlying *sql.DB. Used by tests that need direct DB access
// (e.g., to backdate timestamps for decay testing).
func (s *Store) DB() *sql.DB { return s.db }
```

- [ ] **Step 3: Update stubs_test.go — remove embedder test, keep queue test**

Replace `internal/memory/stubs_test.go`:

```go
package memory_test

import (
    "context"
    "testing"

    "github.com/hollis-labs/cortex/internal/memory"
)

func TestNoopQueueSwallowsJobs(t *testing.T) {
    var q memory.JobQueue = memory.NoopQueue{}
    err := q.Enqueue(context.Background(), memory.Job{Kind: "embed", Payload: []byte("{}")})
    if err != nil {
        t.Fatalf("NoopQueue.Enqueue should never error, got %v", err)
    }
    if err := q.Enqueue(context.Background(), memory.Job{}); err != nil {
        t.Fatalf("NoopQueue.Enqueue on empty job should not error, got %v", err)
    }
}
```

- [ ] **Step 4: Update write_test.go NewStore call**

In `internal/memory/write_test.go`, change the `NewStore` call from:

```go
ms := memory.NewStore(cs.DB(), memory.NoopEmbedder{}, memory.NoopQueue{})
```

to:

```go
ms := memory.NewStore(cs.DB(), nil, memory.NoopQueue{})
```

- [ ] **Step 5: Update memory_tools_test.go NewStore call**

In `internal/mcpadapter/memory_tools_test.go:18`, change from:

```go
ms := memory.NewStore(cs.DB(), memory.NoopEmbedder{}, memory.NoopQueue{})
```

to:

```go
ms := memory.NewStore(cs.DB(), nil, memory.NoopQueue{})
```

- [ ] **Step 6: Update integration test NewStore call**

In `tests/integration/memory_test.go:21`, change from:

```go
ms := memory.NewStore(cs.DB(), memory.NoopEmbedder{}, memory.NoopQueue{})
```

to:

```go
ms := memory.NewStore(cs.DB(), nil, memory.NoopQueue{})
```

- [ ] **Step 7: Update main.go NewStore call**

In `cmd/contextd/main.go:150`, change from:

```go
memStore := memory.NewStore(store.DB(), memory.NoopEmbedder{}, memory.NoopQueue{})
```

to:

```go
memStore := memory.NewStore(store.DB(), nil, memory.NoopQueue{})
```

- [ ] **Step 8: Run all memory tests**

Run: `cd /Users/chrispian/Projects-apps/fragments-engine/cortex && go test ./internal/memory/... -v`
Expected: All tests PASS. The `TestNoopEmbedderReturnsUnavailable` test is gone. Queue test still passes.

- [ ] **Step 9: Run full test suite to check nothing else broke**

Run: `cd /Users/chrispian/Projects-apps/fragments-engine/cortex && go test ./... 2>&1 | tail -30`
Expected: Some embedding/mcpadapter tests may fail (expected — those are updated in Task 4). Memory and integration tests pass.

- [ ] **Step 10: Commit**

```bash
git add -A
git commit -m "refactor: delete Cortex-local provider interfaces, adopt shared provider.Embedder

Removes memory.Embedder, embedding.Provider, and OllamaProvider.
memory.NewStore now accepts provider.Embedder (nil for no-embed mode)."
```

---

### Task 4: Update MCP adapter to use shared provider interface

**Files:**
- Modify: `internal/mcpadapter/adapter.go`
- Modify: `internal/mcpadapter/embedding_tools.go`
- Modify: `internal/mcpadapter/embedding_tools_test.go`
- Modify: `internal/mcpadapter/bulk_tools.go`
- Modify: `internal/mcpadapter/rag_tools.go`
- Modify: `internal/mcpadapter/typed_tools.go`

This task updates all MCP adapter code from the old `embedding.Provider` interface (single-model, `Embed(ctx, text) ([]float32, error)`) to the shared `provider.Embedder` interface (model-parametric, `Embed(ctx, text, model) (*EmbeddingResult, error)`).

- [ ] **Step 1: Update adapter.go — change field type and add model config**

In `internal/mcpadapter/adapter.go`, replace the imports and struct:

Change the import from:
```go
"github.com/hollis-labs/cortex/internal/embedding"
```
to:
```go
"github.com/hollis-labs/nanite/pkg/provider"
```

Change the `Adapter` struct fields from:
```go
EmbeddingProvider embedding.Provider    // optional; nil disables context_embed/context_search
VectorIndex       embedding.VectorIndex // optional; nil uses brute-force search via Store
```
to:
```go
EmbeddingProvider provider.Embedder         // optional; nil disables context_embed/context_search
EmbeddingModel    string                    // default model for embedding calls
VectorIndex       embedding.VectorIndex     // optional; nil uses brute-force search via Store
```

Keep the `embedding` import — it's still needed for `VectorIndex`, `SearchOptions`, etc.

- [ ] **Step 2: Update embedding_tools.go — model-parametric calls**

In `internal/mcpadapter/embedding_tools.go`:

Update imports — add `"github.com/hollis-labs/nanite/pkg/provider"` (may not be needed if only using types through the `a.EmbeddingProvider` field, but needed if referencing `provider.Embedder` directly).

In `handleEmbed`, change:
```go
vec, err := a.EmbeddingProvider.Embed(ctx, text)
```
to:
```go
result, err := a.EmbeddingProvider.Embed(ctx, text, model)
```

And change:
```go
model := req.GetString("model", a.EmbeddingProvider.Model())
```
to (move before the Embed call):
```go
model := req.GetString("model", a.EmbeddingModel)
```

And change the store call from `vec` to `result.Embedding`:
```go
if err := a.Store.UpsertEmbedding(ctx, contextstore.EmbeddingRow{
    RecordID:   recordID,
    Model:      model,
    Dimensions: len(result.Embedding),
    Vector:     result.Embedding,
}); err != nil {
```

And update the response:
```go
return toolJSON(map[string]any{
    "record_id":  recordID,
    "namespace":  ns,
    "key":        key,
    "model":      model,
    "dimensions": len(result.Embedding),
    "status":     "stored",
}), nil
```

In `handleSearch`, change:
```go
Model: a.EmbeddingProvider.Model(),
```
to:
```go
Model: a.EmbeddingModel,
```

And change:
```go
queryVec, err := a.EmbeddingProvider.Embed(ctx, query)
```
to:
```go
queryResult, err := a.EmbeddingProvider.Embed(ctx, query, a.EmbeddingModel)
```

And use `queryResult.Embedding` instead of `queryVec` in the ranking call.

- [ ] **Step 3: Update bulk_tools.go — model-parametric calls**

In `internal/mcpadapter/bulk_tools.go`, find all occurrences of `a.EmbeddingProvider.Embed(ctx, ...)` and `a.EmbeddingProvider.Model()`.

Around line 262, change:
```go
vec, err := a.EmbeddingProvider.Embed(ctx, text)
if err == nil {
    model := a.EmbeddingProvider.Model()
```
to:
```go
result, err := a.EmbeddingProvider.Embed(ctx, text, a.EmbeddingModel)
if err == nil {
    model := a.EmbeddingModel
```

And use `result.Embedding` instead of `vec`:
```go
_ = a.Store.UpsertEmbedding(ctx, contextstore.EmbeddingRow{
    RecordID:   rec.RecordID,
    Model:      model,
    Dimensions: len(result.Embedding),
    Vector:     result.Embedding,
})
```

Apply the same pattern around line 382-385 for chunked ingest.

- [ ] **Step 4: Update rag_tools.go — model-parametric calls**

In `internal/mcpadapter/rag_tools.go`:

Change line 55:
```go
Model:     a.EmbeddingProvider.Model(),
```
to:
```go
Model:     a.EmbeddingModel,
```

Change line 72:
```go
queryVec, err := a.EmbeddingProvider.Embed(ctx, query)
```
to:
```go
queryResult, err := a.EmbeddingProvider.Embed(ctx, query, a.EmbeddingModel)
```
And use `queryResult.Embedding` instead of `queryVec`.

Change line 83:
```go
Model: a.EmbeddingProvider.Model(),
```
to:
```go
Model: a.EmbeddingModel,
```

Change line 98:
```go
queryVec, err := a.EmbeddingProvider.Embed(ctx, query)
```
to:
```go
queryResult, err := a.EmbeddingProvider.Embed(ctx, query, a.EmbeddingModel)
```
And use `queryResult.Embedding`.

- [ ] **Step 5: Update typed_tools.go — model-parametric calls**

In `internal/mcpadapter/typed_tools.go` around line 198:

Change:
```go
vec, err := a.EmbeddingProvider.Embed(ctx, text)
if err == nil {
    model := a.EmbeddingProvider.Model()
```
to:
```go
result, err := a.EmbeddingProvider.Embed(ctx, text, a.EmbeddingModel)
if err == nil {
    model := a.EmbeddingModel
```
And use `result.Embedding` instead of `vec`.

- [ ] **Step 6: Update embedding_tools_test.go**

In `internal/mcpadapter/embedding_tools_test.go`:

Change import from `"github.com/hollis-labs/cortex/internal/embedding"` to keep it (still need `embedding.NewMockProvider`).

Every test that sets `a.EmbeddingProvider = embedding.NewMockProvider(128)` also needs to set the model:
```go
a.EmbeddingProvider = embedding.NewMockProvider(128)
a.EmbeddingModel = "mock-embed"
```

For tests that use `provider := embedding.NewMockProvider(128)` and then `a.EmbeddingProvider = provider`, add:
```go
a.EmbeddingModel = "mock-embed"
```

- [ ] **Step 7: Run all MCP adapter tests**

Run: `cd /Users/chrispian/Projects-apps/fragments-engine/cortex && go test ./internal/mcpadapter/... -v`
Expected: All tests PASS.

- [ ] **Step 8: Run full test suite**

Run: `cd /Users/chrispian/Projects-apps/fragments-engine/cortex && go test ./... 2>&1 | tail -30`
Expected: All tests PASS.

- [ ] **Step 9: Commit**

```bash
git add internal/mcpadapter/
git commit -m "refactor(mcpadapter): update all embedding calls to model-parametric provider.Embedder"
```

---

### Task 5: Update cosine similarity tests for new MockProvider

**Files:**
- Modify: `internal/embedding/embedding_test.go`

The `CosineSimilarity` and `RankByCosineSimilarity` tests in `embedding_test.go` use raw `[]float32` vectors and don't depend on the provider interface. Verify they still compile and pass after the earlier changes. If the test file was fully replaced in Task 2, re-add the similarity tests.

- [ ] **Step 1: Verify similarity tests are present in embedding_test.go**

Check that `TestCosineSimilarity_*` and `TestRankByCosineSimilarity*` tests are in the file. If Task 2 replaced the file, they may be missing.

- [ ] **Step 2: Ensure all similarity tests are present**

The following tests must exist in `internal/embedding/embedding_test.go` (they don't depend on provider types — they test pure math functions):

```go
func TestCosineSimilarity_Identical(t *testing.T) {
    a := []float32{1, 0, 0}
    s := CosineSimilarity(a, a)
    if math.Abs(s-1.0) > 0.0001 {
        t.Errorf("identical vectors: similarity = %f, want 1.0", s)
    }
}

func TestCosineSimilarity_Orthogonal(t *testing.T) {
    a := []float32{1, 0, 0}
    b := []float32{0, 1, 0}
    s := CosineSimilarity(a, b)
    if math.Abs(s) > 0.0001 {
        t.Errorf("orthogonal vectors: similarity = %f, want 0.0", s)
    }
}

func TestCosineSimilarity_Opposite(t *testing.T) {
    a := []float32{1, 0, 0}
    b := []float32{-1, 0, 0}
    s := CosineSimilarity(a, b)
    if math.Abs(s+1.0) > 0.0001 {
        t.Errorf("opposite vectors: similarity = %f, want -1.0", s)
    }
}

func TestCosineSimilarity_EmptyVectors(t *testing.T) {
    s := CosineSimilarity(nil, nil)
    if s != 0 {
        t.Errorf("empty vectors: similarity = %f, want 0.0", s)
    }
}

func TestCosineSimilarity_MismatchedLengths(t *testing.T) {
    a := []float32{1, 0}
    b := []float32{1, 0, 0}
    s := CosineSimilarity(a, b)
    if s != 0 {
        t.Errorf("mismatched lengths: similarity = %f, want 0.0", s)
    }
}

func TestRankByCosineSimilarity(t *testing.T) {
    query := []float32{1, 0, 0}
    vectors := [][]float32{
        {0, 1, 0},
        {1, 0, 0},
        {0.7, 0.7, 0},
        {-1, 0, 0},
        {0.9, 0.4, 0},
    }
    ids := []string{"a", "b", "c", "d", "e"}
    nss := []string{"ns/a", "ns/b", "ns/c", "ns/d", "ns/e"}
    keys := []string{"ka", "kb", "kc", "kd", "ke"}

    results := RankByCosineSimilarity(query, vectors, ids, nss, keys, 3, 0.5)

    if len(results) != 3 {
        t.Fatalf("got %d results, want 3", len(results))
    }
    if results[0].RecordID != "b" {
        t.Errorf("results[0].RecordID = %q, want b", results[0].RecordID)
    }
    if results[1].RecordID != "e" {
        t.Errorf("results[1].RecordID = %q, want e", results[1].RecordID)
    }
    if results[2].RecordID != "c" {
        t.Errorf("results[2].RecordID = %q, want c", results[2].RecordID)
    }
}

func TestRankByCosineSimilarity_ThresholdFiltering(t *testing.T) {
    query := []float32{1, 0, 0}
    vectors := [][]float32{
        {0.5, 0.5, 0.5},
        {0.9, 0.1, 0},
    }
    ids := []string{"a", "b"}
    nss := []string{"ns/a", "ns/b"}
    keys := []string{"ka", "kb"}

    results := RankByCosineSimilarity(query, vectors, ids, nss, keys, 10, 0.9)
    if len(results) != 1 {
        t.Fatalf("got %d results, want 1 (threshold filter)", len(results))
    }
    if results[0].RecordID != "b" {
        t.Errorf("expected record b, got %q", results[0].RecordID)
    }
}

func TestRankByCosineSimilarity_Empty(t *testing.T) {
    query := []float32{1, 0, 0}
    results := RankByCosineSimilarity(query, nil, nil, nil, nil, 10, 0.5)
    if len(results) != 0 {
        t.Errorf("expected 0 results for empty candidates, got %d", len(results))
    }
}
```

- [ ] **Step 3: Run all embedding tests**

Run: `cd /Users/chrispian/Projects-apps/fragments-engine/cortex && go test ./internal/embedding/... -v`
Expected: All tests PASS.

- [ ] **Step 4: Commit (if changes were needed)**

```bash
git add internal/embedding/embedding_test.go
git commit -m "test(embedding): ensure similarity tests preserved after provider interface migration"
```

---

### Task 6: Create cortex.Open() library entry point

**Files:**
- Create: `cortex.go`
- Create: `cortex_test.go`

- [ ] **Step 1: Write the failing test for Open/Close**

Create `cortex_test.go` in the project root (package `cortex`):

```go
package cortex

import (
    "context"
    "testing"

    "github.com/hollis-labs/cortex/internal/embedding"
)

func TestOpen_MinimalConfig(t *testing.T) {
    dir := t.TempDir()
    c, err := Open(context.Background(), Config{RootDir: dir})
    if err != nil {
        t.Fatalf("Open: %v", err)
    }
    defer c.Close()

    if c.store == nil {
        t.Error("expected store to be initialized")
    }
    if c.memoryStore == nil {
        t.Error("expected memory store to be initialized")
    }
    // No embedder configured — should be nil.
    if c.embedder != nil {
        t.Error("expected embedder to be nil without WithEmbedder")
    }
}

func TestOpen_WithEmbedder(t *testing.T) {
    dir := t.TempDir()
    mock := embedding.NewMockProvider(128)

    c, err := Open(context.Background(), Config{RootDir: dir},
        WithEmbedder(mock),
        WithEmbeddingModel("mock-embed"),
    )
    if err != nil {
        t.Fatalf("Open: %v", err)
    }
    defer c.Close()

    if c.embedder == nil {
        t.Error("expected embedder to be set")
    }
    if c.embeddingModel != "mock-embed" {
        t.Errorf("embeddingModel = %q, want mock-embed", c.embeddingModel)
    }
}

func TestOpen_MissingRootDir(t *testing.T) {
    _, err := Open(context.Background(), Config{})
    if err == nil {
        t.Fatal("expected error for empty RootDir")
    }
}

func TestClose_StopsDecay(t *testing.T) {
    dir := t.TempDir()
    c, err := Open(context.Background(), Config{RootDir: dir})
    if err != nil {
        t.Fatalf("Open: %v", err)
    }
    // Close should not panic or error.
    if err := c.Close(); err != nil {
        t.Fatalf("Close: %v", err)
    }
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /Users/chrispian/Projects-apps/fragments-engine/cortex && go test . -run TestOpen -v`
Expected: FAIL — `Open`, `Config`, `WithEmbedder`, etc. not defined.

- [ ] **Step 3: Implement cortex.go**

Create `cortex.go` in the project root:

```go
package cortex

import (
    "context"
    "errors"
    "log"
    "os"
    "path/filepath"
    "time"

    "github.com/hollis-labs/cortex/internal/contextstore"
    "github.com/hollis-labs/cortex/internal/memory"
    "github.com/hollis-labs/nanite/pkg/provider"
)

// Config holds required configuration for opening a Cortex instance.
type Config struct {
    RootDir string // data directory (e.g., ~/.cortex)
}

// Option configures optional Cortex capabilities.
type Option func(*options)

type options struct {
    embedder       provider.Embedder
    embeddingModel string
    logger         func(string, ...any)
}

// WithEmbedder enables embedding and semantic search.
func WithEmbedder(e provider.Embedder) Option {
    return func(o *options) { o.embedder = e }
}

// WithEmbeddingModel sets the default model for embedding calls.
func WithEmbeddingModel(model string) Option {
    return func(o *options) { o.embeddingModel = model }
}

// WithLogger sets a structured logger for Cortex operations.
func WithLogger(fn func(string, ...any)) Option {
    return func(o *options) { o.logger = fn }
}

// Cortex is the top-level library handle. Create one with Open().
type Cortex struct {
    store          *contextstore.Store
    memoryStore    *memory.Store
    embedder       provider.Embedder
    embeddingModel string
    logger         func(string, ...any)
    cancel         context.CancelFunc // stops the decay goroutine
}

// Open creates and initializes a Cortex instance. The caller must call
// Close() when done. The embedder is optional — if nil, embedding and
// search calls return ErrEmbedderUnavailable.
func Open(ctx context.Context, cfg Config, opts ...Option) (*Cortex, error) {
    if cfg.RootDir == "" {
        return nil, errors.New("cortex: RootDir is required")
    }

    var o options
    for _, opt := range opts {
        opt(&o)
    }

    if o.logger == nil {
        o.logger = log.Printf
    }

    // Ensure data directories exist.
    dataDir := filepath.Join(cfg.RootDir, "data")
    recordsDir := filepath.Join(dataDir, "records")
    indexDir := filepath.Join(dataDir, "index")
    for _, dir := range []string{recordsDir, indexDir} {
        if err := os.MkdirAll(dir, 0o755); err != nil {
            return nil, err
        }
    }

    store, err := contextstore.Open(ctx, contextstore.Config{
        RootDir:    cfg.RootDir,
        RecordsDir: recordsDir,
        DBPath:     filepath.Join(indexDir, "context.db"),
    })
    if err != nil {
        return nil, err
    }

    memStore := memory.NewStore(store.DB(), o.embedder, memory.NoopQueue{})

    decayCtx, cancel := context.WithCancel(ctx)
    decayJob := &memory.DecayJob{
        Store:    memStore,
        Interval: 1 * time.Hour,
        Logger:   o.logger,
    }
    go decayJob.Run(decayCtx)

    return &Cortex{
        store:          store,
        memoryStore:    memStore,
        embedder:       o.embedder,
        embeddingModel: o.embeddingModel,
        logger:         o.logger,
        cancel:         cancel,
    }, nil
}

// Close releases all resources held by the Cortex instance. It does NOT
// close the embedder — the host application owns that lifecycle.
func (c *Cortex) Close() error {
    c.cancel()
    return c.store.Close()
}

// Store returns the underlying context store. Used by the MCP adapter
// and other internal consumers during the transition period.
func (c *Cortex) Store() *contextstore.Store { return c.store }

// MemoryStore returns the memory subsystem store.
func (c *Cortex) MemoryStore() *memory.Store { return c.memoryStore }
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/chrispian/Projects-apps/fragments-engine/cortex && go test . -run TestOpen -v && go test . -run TestClose -v`
Expected: All tests PASS.

- [ ] **Step 5: Commit**

```bash
git add cortex.go cortex_test.go
git commit -m "feat: add cortex.Open() library entry point with functional options"
```

---

### Task 7: Add library-level Embed() and Search() methods

**Files:**
- Create: `embed.go`
- Create: `embed_test.go`

- [ ] **Step 1: Write the failing tests**

Create `embed_test.go` in the project root:

```go
package cortex

import (
    "context"
    "errors"
    "testing"

    "github.com/hollis-labs/cortex/internal/contextstore"
    "github.com/hollis-labs/cortex/internal/embedding"
)

func openTestCortex(t *testing.T, opts ...Option) *Cortex {
    t.Helper()
    dir := t.TempDir()
    c, err := Open(context.Background(), Config{RootDir: dir}, opts...)
    if err != nil {
        t.Fatalf("Open: %v", err)
    }
    t.Cleanup(func() { _ = c.Close() })
    return c
}

func writeTestRecord(t *testing.T, c *Cortex, ns, key, payload string) contextstore.Record {
    t.Helper()
    rec, err := c.store.Write(context.Background(), contextstore.WriteInput{
        Namespace: ns,
        Key:       key,
        Payload:   []byte(payload),
    })
    if err != nil {
        t.Fatalf("write: %v", err)
    }
    return rec
}

func TestEmbed_NoProvider(t *testing.T) {
    c := openTestCortex(t)
    rec := writeTestRecord(t, c, "user/test", "doc", `{"content":"hello"}`)

    err := c.Embed(context.Background(), rec.RecordID)
    if !errors.Is(err, ErrEmbedderUnavailable) {
        t.Errorf("expected ErrEmbedderUnavailable, got %v", err)
    }
}

func TestEmbed_Success(t *testing.T) {
    mock := embedding.NewMockProvider(128)
    c := openTestCortex(t, WithEmbedder(mock), WithEmbeddingModel("mock-embed"))
    rec := writeTestRecord(t, c, "user/test", "doc", `{"content":"hello world"}`)

    err := c.Embed(context.Background(), rec.RecordID)
    if err != nil {
        t.Fatalf("Embed: %v", err)
    }

    // Verify the embedding was stored.
    embeddings, _, err := c.store.ListEmbeddings(context.Background(), contextstore.EmbeddingFilter{
        Model: "mock-embed",
    })
    if err != nil {
        t.Fatalf("ListEmbeddings: %v", err)
    }
    if len(embeddings) != 1 {
        t.Fatalf("expected 1 embedding, got %d", len(embeddings))
    }
    if embeddings[0].RecordID != rec.RecordID {
        t.Errorf("embedding record_id = %q, want %q", embeddings[0].RecordID, rec.RecordID)
    }
}

func TestEmbed_Idempotent(t *testing.T) {
    mock := embedding.NewMockProvider(128)
    c := openTestCortex(t, WithEmbedder(mock), WithEmbeddingModel("mock-embed"))
    rec := writeTestRecord(t, c, "user/test", "doc", `{"content":"idempotent"}`)

    for i := 0; i < 3; i++ {
        if err := c.Embed(context.Background(), rec.RecordID); err != nil {
            t.Fatalf("Embed attempt %d: %v", i, err)
        }
    }

    embeddings, _, err := c.store.ListEmbeddings(context.Background(), contextstore.EmbeddingFilter{
        Model: "mock-embed",
    })
    if err != nil {
        t.Fatalf("ListEmbeddings: %v", err)
    }
    // Should still be exactly 1 embedding (upsert, not insert).
    if len(embeddings) != 1 {
        t.Errorf("expected 1 embedding after 3 upserts, got %d", len(embeddings))
    }
}

func TestEmbed_RecordNotFound(t *testing.T) {
    mock := embedding.NewMockProvider(128)
    c := openTestCortex(t, WithEmbedder(mock), WithEmbeddingModel("mock-embed"))

    err := c.Embed(context.Background(), "nonexistent-id")
    if err == nil {
        t.Fatal("expected error for nonexistent record")
    }
}

func TestSearch_NoProvider(t *testing.T) {
    c := openTestCortex(t)

    _, err := c.Search(context.Background(), "test query", SearchOptions{})
    if !errors.Is(err, ErrEmbedderUnavailable) {
        t.Errorf("expected ErrEmbedderUnavailable, got %v", err)
    }
}

func TestSearch_EmptyResults(t *testing.T) {
    mock := embedding.NewMockProvider(128)
    c := openTestCortex(t, WithEmbedder(mock), WithEmbeddingModel("mock-embed"))

    results, err := c.Search(context.Background(), "test query", SearchOptions{})
    if err != nil {
        t.Fatalf("Search: %v", err)
    }
    if len(results) != 0 {
        t.Errorf("expected 0 results, got %d", len(results))
    }
}

func TestSearch_FindsEmbeddedRecords(t *testing.T) {
    mock := embedding.NewMockProvider(128)
    c := openTestCortex(t, WithEmbedder(mock), WithEmbeddingModel("mock-embed"))

    // Write and embed records.
    rec1 := writeTestRecord(t, c, "app/notes", "alpha", `{"content":"machine learning neural networks"}`)
    rec2 := writeTestRecord(t, c, "app/notes", "beta", `{"content":"database schema migration"}`)

    if err := c.Embed(context.Background(), rec1.RecordID); err != nil {
        t.Fatalf("Embed alpha: %v", err)
    }
    if err := c.Embed(context.Background(), rec2.RecordID); err != nil {
        t.Fatalf("Embed beta: %v", err)
    }

    results, err := c.Search(context.Background(), "neural networks", SearchOptions{
        Limit:     10,
        Threshold: -1, // return everything
    })
    if err != nil {
        t.Fatalf("Search: %v", err)
    }
    if len(results) == 0 {
        t.Fatal("expected results, got 0")
    }

    // Verify result structure.
    for _, r := range results {
        if r.RecordID == "" {
            t.Error("result has empty RecordID")
        }
    }
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /Users/chrispian/Projects-apps/fragments-engine/cortex && go test . -run "TestEmbed|TestSearch" -v`
Expected: FAIL — `Embed`, `Search`, `SearchOptions`, `ErrEmbedderUnavailable` not defined.

- [ ] **Step 3: Implement embed.go**

Create `embed.go` in the project root:

```go
package cortex

import (
    "context"
    "encoding/json"
    "errors"
    "fmt"
    "strings"

    "github.com/hollis-labs/cortex/internal/contextstore"
    "github.com/hollis-labs/cortex/internal/embedding"
)

// ErrEmbedderUnavailable is returned when no embedding provider is configured.
var ErrEmbedderUnavailable = errors.New("embedder unavailable")

// SearchOptions controls semantic search behavior.
type SearchOptions struct {
    Limit      int      // max results (default 10)
    Threshold  float64  // minimum similarity (default 0.0)
    Namespaces []string // namespace prefix filters
    Types      []string // record type filters
}

// Embed generates and stores an embedding vector for the given record.
// Returns ErrEmbedderUnavailable if no provider is configured.
// Idempotent: re-embedding overwrites the previous vector.
func (c *Cortex) Embed(ctx context.Context, recordID string) error {
    if c.embedder == nil {
        return ErrEmbedderUnavailable
    }

    rec, err := c.store.GetByRecordID(ctx, recordID)
    if err != nil {
        return fmt.Errorf("cortex: record %s not found: %w", recordID, err)
    }

    text := extractTextForEmbedding(rec)
    if text == "" {
        return fmt.Errorf("cortex: record %s has no embeddable text content", recordID)
    }

    result, err := c.embedder.Embed(ctx, text, c.embeddingModel)
    if err != nil {
        return fmt.Errorf("cortex: embedding failed: %w", err)
    }

    return c.store.UpsertEmbedding(ctx, contextstore.EmbeddingRow{
        RecordID:   recordID,
        Model:      c.embeddingModel,
        Dimensions: len(result.Embedding),
        Vector:     result.Embedding,
    })
}

// Search performs semantic search across embedded records.
// Returns ErrEmbedderUnavailable if no provider is configured.
func (c *Cortex) Search(ctx context.Context, query string, opts SearchOptions) ([]embedding.SearchResult, error) {
    if c.embedder == nil {
        return nil, ErrEmbedderUnavailable
    }

    limit := opts.Limit
    if limit <= 0 {
        limit = 10
    }

    queryResult, err := c.embedder.Embed(ctx, query, c.embeddingModel)
    if err != nil {
        return nil, fmt.Errorf("cortex: query embedding failed: %w", err)
    }

    filter := contextstore.EmbeddingFilter{
        Model: c.embeddingModel,
    }
    if len(opts.Namespaces) > 0 {
        filter.Namespaces = opts.Namespaces
    }
    if len(opts.Types) > 0 {
        filter.Types = opts.Types
    }

    embeddings, records, err := c.store.ListEmbeddings(ctx, filter)
    if err != nil {
        return nil, fmt.Errorf("cortex: failed to load embeddings: %w", err)
    }

    if len(embeddings) == 0 {
        return nil, nil
    }

    vectors := make([][]float32, len(embeddings))
    recordIDs := make([]string, len(embeddings))
    namespaces := make([]string, len(embeddings))
    keys := make([]string, len(embeddings))
    for i, e := range embeddings {
        vectors[i] = e.Vector
        recordIDs[i] = e.RecordID
        namespaces[i] = records[i].Namespace
        keys[i] = records[i].Key
    }

    return embedding.RankByCosineSimilarity(queryResult.Embedding, vectors, recordIDs, namespaces, keys, limit, opts.Threshold), nil
}

// extractTextForEmbedding converts a record payload to embeddable text.
func extractTextForEmbedding(rec contextstore.Record) string {
    if rec.Payload == nil {
        return ""
    }

    var obj map[string]any
    if err := json.Unmarshal(rec.Payload, &obj); err == nil {
        var parts []string
        for _, field := range []string{"title", "summary", "description", "content", "body", "text", "message"} {
            if v, ok := obj[field]; ok {
                if s, ok := v.(string); ok && s != "" {
                    parts = append(parts, s)
                }
            }
        }
        if len(parts) > 0 {
            return strings.Join(parts, "\n")
        }
    }

    return string(rec.Payload)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/chrispian/Projects-apps/fragments-engine/cortex && go test . -run "TestEmbed|TestSearch" -v`
Expected: All tests PASS.

- [ ] **Step 5: Run full test suite**

Run: `cd /Users/chrispian/Projects-apps/fragments-engine/cortex && go test ./... 2>&1 | tail -30`
Expected: All tests PASS.

- [ ] **Step 6: Commit**

```bash
git add embed.go embed_test.go
git commit -m "feat: add library-level Embed() and Search() methods on *Cortex"
```

---

### Task 8: Update MCP adapter to delegate to library methods (optional refactor)

**Files:**
- Modify: `internal/mcpadapter/embedding_tools.go`
- Modify: `internal/mcpadapter/adapter.go`

This task is optional for the initial integration — the MCP adapter already works with the shared provider after Task 4. This refactor makes the adapter a thin wrapper that delegates to the `*Cortex` library methods, completing the D7 design decision. It can be done now or deferred.

- [ ] **Step 1: Add Cortex handle to Adapter**

In `internal/mcpadapter/adapter.go`, add a field:

```go
Cortex *cortex.Cortex // optional; when set, embedding tools delegate to library methods
```

Note: This may introduce an import cycle (`cortex` → `mcpadapter` → `cortex`). If so, the adapter should remain as-is (calling the provider directly) and the `Cortex` handle exposes its own embed/search. The MCP adapter and library methods are parallel paths to the same provider — both valid. Skip this task if an import cycle is detected.

- [ ] **Step 2: If no import cycle, update handleEmbed to delegate**

In `handleEmbed`, replace the record fetch + embed + store logic with:

```go
if err := a.Cortex.Embed(ctx, recordID); err != nil {
    if errors.Is(err, cortex.ErrEmbedderUnavailable) {
        return toolError("embedding_unavailable", "no embedding provider configured"), nil
    }
    return toolError("embedding_error", err.Error()), nil
}
```

- [ ] **Step 3: If no import cycle, update handleSearch to delegate**

Similar delegation pattern.

- [ ] **Step 4: Run tests**

Run: `cd /Users/chrispian/Projects-apps/fragments-engine/cortex && go test ./... 2>&1 | tail -30`
Expected: All tests PASS (or skip this task if import cycle).

- [ ] **Step 5: Commit if changes were made**

```bash
git add internal/mcpadapter/
git commit -m "refactor(mcpadapter): delegate embedding tools to cortex library methods"
```

---

### Task 9: Update main.go to use cortex.Open() (optional)

**Files:**
- Modify: `cmd/contextd/main.go`

This task wires `main.go` to use `cortex.Open()` instead of manually constructing the store and memory subsystem. Optional for the initial integration — the manual wiring from Task 3 already works.

- [ ] **Step 1: Update runMCP to use cortex.Open()**

Replace the manual store/memory/decay setup in `runMCP` with:

```go
func runMCP(ctx context.Context, store *contextstore.Store, stderr *os.File, token string) int {
    _, _ = stderr.WriteString("Cortex MCP adapter starting (stdio)\n")

    // Note: In MCP server mode, no embedder is configured by default.
    // The host application (Nanite) injects one when embedding Cortex as a library.
    memStore := memory.NewStore(store.DB(), nil, memory.NoopQueue{})

    // Start decay job.
    decayInterval := 1 * time.Hour
    if v := os.Getenv("CORTEX_MEMORY_DECAY_INTERVAL"); v != "" {
        if d, err := time.ParseDuration(v); err == nil {
            decayInterval = d
        } else {
            log.Print("warning: invalid CORTEX_MEMORY_DECAY_INTERVAL, using default 1h")
        }
    }
    decayJob := &memory.DecayJob{
        Store:    memStore,
        Interval: decayInterval,
        Logger:   log.Printf,
    }
    go decayJob.Run(ctx)

    adapter := mcpadapter.New(store, token)
    adapter.MemoryStore = memStore
    if err := adapter.Run(ctx); err != nil {
        _, _ = stderr.WriteString("error: " + err.Error() + "\n")
        return 1
    }
    return 0
}
```

This is minimal — just replace `memory.NoopEmbedder{}` with `nil`. The full `cortex.Open()` wiring for `runMCP` makes more sense when standalone Cortex has its own provider config (Track F/G).

- [ ] **Step 2: Verify build**

Run: `cd /Users/chrispian/Projects-apps/fragments-engine/cortex && go build ./cmd/contextd/`
Expected: Build succeeds.

- [ ] **Step 3: Run full test suite**

Run: `cd /Users/chrispian/Projects-apps/fragments-engine/cortex && go test ./... 2>&1 | tail -30`
Expected: All tests PASS.

- [ ] **Step 4: Commit**

```bash
git add cmd/contextd/main.go
git commit -m "refactor(main): remove NoopEmbedder usage, pass nil for no-embed mode"
```

---

### Task 10: Update release roadmap

**Files:**
- Modify: `docs/RELEASE-ROADMAP.md`

- [ ] **Step 1: Update roadmap status**

In `docs/RELEASE-ROADMAP.md`, update the current status section:

Under D, add a line for D-deferred progress:
```
  - D-embedding: provider integration complete (shared provider.Embedder,
    on-demand embed/search via library API). Auto-embed deferred to Track I.
```

Under H, note the shared provider extraction:
```
- [ ] H. Shared AI provider module — **in progress**; pkg/provider extracted in Nanite,
  Cortex consuming via replace directive. Standalone repo extraction pending.
```

Under E, note partial progress:
```
- [ ] E. Embedded-runtime API surface — **partially complete**; cortex.Open()
  with functional options. Full API surface TBD.
```

- [ ] **Step 2: Commit**

```bash
git add docs/RELEASE-ROADMAP.md
git commit -m "docs: update roadmap with embedding integration progress"
```
