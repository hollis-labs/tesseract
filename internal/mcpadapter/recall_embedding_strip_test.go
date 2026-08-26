package mcpadapter

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	embedcontracts "github.com/hollis-labs/go-embed-contracts"
	"github.com/hollis-labs/tesseract/internal/contextstore"
	"github.com/hollis-labs/tesseract/internal/memory"
	"github.com/mark3labs/mcp-go/mcp"
)

// BLG-20260416-037 guard. memory_recall and tesseract_lookup must not ship
// the embedding_vector field in their JSON responses. A 3072-dim vector
// per record (text-embedding-3-large — see docs/DEV.md) inflates each
// result by ~39KB, which blows Claude Code's 200K context budget on
// recalls of 5+.

// stripTestEmbedder returns a fixed vector large enough to prove the
// leak would be material if the fix regressed.
type stripTestEmbedder struct {
	vector []float32
}

func (e *stripTestEmbedder) Embed(_ context.Context, _ string, _ string) (*embedcontracts.EmbeddingResult, error) {
	return &embedcontracts.EmbeddingResult{Embedding: e.vector, TokenCount: 1}, nil
}

func (e *stripTestEmbedder) EmbedBatch(_ context.Context, texts []string, _ string) ([]embedcontracts.EmbeddingResult, error) {
	out := make([]embedcontracts.EmbeddingResult, len(texts))
	for i := range texts {
		out[i] = embedcontracts.EmbeddingResult{Embedding: e.vector, TokenCount: 1}
	}
	return out, nil
}

func (e *stripTestEmbedder) EmbeddingDimensions(_ string) int { return len(e.vector) }

// newEmbeddingAdapter builds an Adapter wired to a MemoryStore with an
// embedder, so tests can exercise the recall path against revisions
// whose EmbeddingVector is populated.
func newEmbeddingAdapter(t *testing.T, vec []float32, scopes ...string) *Adapter {
	t.Helper()
	cs := newTestStore(t)
	embedder := &stripTestEmbedder{vector: vec}
	ms := memory.NewStore(cs.DB(), embedder, "test-model", 0.85, memory.NoopQueue{})

	var tok string
	if len(scopes) > 0 {
		var err error
		tok, _, err = cs.CreateAuthToken(context.Background(), contextstore.TokenCreateInput{
			Label:  "test",
			Scopes: scopes,
		})
		if err != nil {
			t.Fatalf("create token: %v", err)
		}
	}

	return &Adapter{
		Store:             cs,
		Token:             tok,
		EmbeddingProvider: embedder,
		EmbeddingModel:    "test-model",
		MemoryStore:       ms,
	}
}

func writeAndEmbed(t *testing.T, a *Adapter, args map[string]any) string {
	t.Helper()
	body := writeViaHandler(t, a, args)
	revID, ok := body["revision_id"].(string)
	if !ok || revID == "" {
		t.Fatalf("expected revision_id, got %v", body)
	}
	if err := a.MemoryStore.EmbedRevision(context.Background(), revID, "test-model"); err != nil {
		t.Fatalf("EmbedRevision: %v", err)
	}
	return revID
}

func TestMemoryRecall_OmitsEmbeddingVector(t *testing.T) {
	vec := make([]float32, 3072)
	for i := range vec {
		vec[i] = 0.01
	}
	a := newEmbeddingAdapter(t, vec, "memory:write", "memory:read")

	writeAndEmbed(t, a, map[string]any{
		"namespace":       "user/chrispian/memory/notes",
		"memory_key":      "user.prefs",
		"author_agent_id": "claude",
		"trigger":         "explicit",
		"session_id":      "sess-001",
		"origin":          "user",
		"confidence":      0.9,
		"payload_summary": "Dark mode preference",
	})

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"namespaces": `["user/chrispian/memory/notes"]`,
	}
	res, err := a.handleTesseractRecall(context.Background(), req)
	if err != nil {
		t.Fatalf("handleTesseractRecall: %v", err)
	}
	textContent, ok := res.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent")
	}
	if strings.Contains(textContent.Text, "embedding_vector") {
		t.Fatalf("embedding_vector must not appear in memory_recall response")
	}
	if len(textContent.Text) > 4096 {
		t.Fatalf("recall response unexpectedly large (%d bytes); vector may still be leaking", len(textContent.Text))
	}
	var results []map[string]any
	if err := json.Unmarshal(recallResultsJSON(t, textContent.Text), &results); err != nil {
		t.Fatalf("unmarshal recall: %v", err)
	}
	if len(results) == 0 {
		t.Fatalf("expected at least one recall result")
	}
}

func TestTesseractLookup_OmitsEmbeddingVector(t *testing.T) {
	vec := make([]float32, 3072)
	for i := range vec {
		vec[i] = 0.02
	}
	a := newEmbeddingAdapter(t, vec, "memory:write", "memory:read")

	writeAndEmbed(t, a, map[string]any{
		"namespace":       "user/chrispian/memory/notes",
		"memory_key":      "user.prefs",
		"author_agent_id": "claude",
		"trigger":         "explicit",
		"session_id":      "sess-001",
		"origin":          "user",
		"confidence":      0.9,
		"payload_summary": "Dark mode preference",
	})

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"namespaces": `["user/chrispian/memory/notes"]`,
	}
	res, err := a.handleTesseractRecall(context.Background(), req)
	if err != nil {
		t.Fatalf("handleTesseractRecall: %v", err)
	}
	textContent, ok := res.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent")
	}
	if strings.Contains(textContent.Text, "embedding_vector") {
		t.Fatalf("embedding_vector must not appear in tesseract_lookup response")
	}
	if len(textContent.Text) > 4096 {
		t.Fatalf("lookup response unexpectedly large (%d bytes); vector may still be leaking", len(textContent.Text))
	}
}

// Similarity ranking must keep working after the fix: vector is still
// read from the struct internally, just not serialized to the wire.
func TestMemoryRecall_SimilarityStillRanks(t *testing.T) {
	vec := make([]float32, 3072)
	for i := range vec {
		vec[i] = 0.01
	}
	a := newEmbeddingAdapter(t, vec, "memory:write", "memory:read")

	writeAndEmbed(t, a, map[string]any{
		"namespace":       "user/chrispian/memory/notes",
		"memory_key":      "user.prefs",
		"author_agent_id": "claude",
		"trigger":         "explicit",
		"session_id":      "sess-001",
		"origin":          "user",
		"confidence":      0.9,
		"payload_summary": "Dark mode preference",
	})

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"namespaces": `["user/chrispian/memory/notes"]`,
		"ranking":    "similarity",
		"query":      "dark mode preference",
	}
	res, err := a.handleTesseractRecall(context.Background(), req)
	if err != nil {
		t.Fatalf("handleTesseractRecall: %v", err)
	}
	textContent, ok := res.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent")
	}
	if strings.Contains(textContent.Text, "embedding_vector") {
		t.Fatalf("embedding_vector must not appear even with ranking=similarity")
	}
	var results []map[string]any
	if err := json.Unmarshal(recallResultsJSON(t, textContent.Text), &results); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(results) == 0 {
		t.Fatalf("similarity ranking returned no results; ranking path may depend on serialized vector")
	}
}
