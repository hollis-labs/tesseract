package memory_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hollis-labs/tesseract/internal/memory"
)

// TestRevisionJSON_OmitsEmbeddingVector guards BLG-20260416-037: the
// embedding_vector field must never be serialized over the MCP wire.
// A populated 3072-dim vector (text-embedding-3-large — see docs/DEV.md)
// inflates a single Revision to ~39KB; a recall of 5 can blow the 200K
// context budget.
//
// EmbeddingVector stays on the struct (ranking + filtering read it
// directly), but json:"-" keeps it out of every marshaled response.
func TestRevisionJSON_OmitsEmbeddingVector(t *testing.T) {
	vec := make([]float32, 3072)
	for i := range vec {
		vec[i] = 0.01
	}
	rev := memory.Revision{
		RevisionID:      "rev-1",
		MemoryID:        "mem-1",
		Namespace:       "user/test/memory/notes",
		EmbeddingModel:  "text-embedding-3-large",
		EmbeddingVector: vec,
	}
	data, err := json.Marshal(rev)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if strings.Contains(string(data), "embedding_vector") {
		t.Fatalf("embedding_vector must not appear in JSON output, got: %s", data)
	}
	// embedding_model is small metadata; keep it for observability.
	if !strings.Contains(string(data), "embedding_model") {
		t.Fatalf("embedding_model should still appear in JSON output, got: %s", data)
	}
	// Sanity: the vector absence must actually shrink the payload well
	// below the per-record budget.
	if len(data) > 4096 {
		t.Fatalf("marshaled Revision unexpectedly large (%d bytes) — embedding_vector may still be leaking", len(data))
	}
}
