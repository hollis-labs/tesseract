package mcpadapter

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/hollis-labs/cortex/internal/contextstore"
	"github.com/hollis-labs/cortex/internal/embedding"
	"github.com/mark3labs/mcp-go/mcp"
)

func TestContextEmbed_NoProvider(t *testing.T) {
	s := newTestStore(t)
	a := New(s, "")
	// EmbeddingProvider is nil by default.

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"record_id": "test-id",
		"namespace": "user/test",
		"key":       "doc",
	}

	res, err := a.handleEmbed(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m := parseResult(t, res)
	if m["code"] != "embedding_unavailable" {
		t.Errorf("expected embedding_unavailable, got %v", m["code"])
	}
}

func TestContextEmbed_Success(t *testing.T) {
	s := newTestStore(t)
	rec := writeRecord(t, s, "user/test", "doc", `{"title":"test doc","content":"hello world"}`)

	a := New(s, "")
	a.EmbeddingProvider = embedding.NewMockProvider(128)
	a.EmbeddingModel = "mock-embed"

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"record_id": rec.RecordID,
		"namespace": "user/test",
		"key":       "doc",
	}

	res, err := a.handleEmbed(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m := parseResult(t, res)
	if m["status"] != "stored" {
		t.Errorf("expected status=stored, got %v", m["status"])
	}
	if int(m["dimensions"].(float64)) != 128 {
		t.Errorf("expected dimensions=128, got %v", m["dimensions"])
	}
}

func TestContextEmbed_RecordNotFound(t *testing.T) {
	s := newTestStore(t)
	a := New(s, "")
	a.EmbeddingProvider = embedding.NewMockProvider(128)
	a.EmbeddingModel = "mock-embed"

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"record_id": "nonexistent",
		"namespace": "user/test",
		"key":       "doc",
	}

	res, err := a.handleEmbed(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m := parseResult(t, res)
	if m["code"] != "not_found" {
		t.Errorf("expected not_found, got %v", m["code"])
	}
}

func TestContextEmbed_Idempotent(t *testing.T) {
	s := newTestStore(t)
	rec := writeRecord(t, s, "user/test", "doc", `{"content":"idempotent test"}`)

	a := New(s, "")
	a.EmbeddingProvider = embedding.NewMockProvider(128)
	a.EmbeddingModel = "mock-embed"

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"record_id": rec.RecordID,
		"namespace": "user/test",
		"key":       "doc",
	}

	// Embed twice — should not error.
	for i := 0; i < 2; i++ {
		res, err := a.handleEmbed(context.Background(), req)
		if err != nil {
			t.Fatalf("embed %d: unexpected error: %v", i, err)
		}
		m := parseResult(t, res)
		if m["status"] != "stored" {
			t.Errorf("embed %d: expected status=stored, got %v", i, m["status"])
		}
	}
}

func TestContextSearch_NoProvider(t *testing.T) {
	s := newTestStore(t)
	a := New(s, "")

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"query": "test"}

	res, err := a.handleSearch(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m := parseResult(t, res)
	if m["code"] != "embedding_unavailable" {
		t.Errorf("expected embedding_unavailable, got %v", m["code"])
	}
}

func TestContextSearch_EmptyResults(t *testing.T) {
	s := newTestStore(t)
	a := New(s, "")
	a.EmbeddingProvider = embedding.NewMockProvider(128)
	a.EmbeddingModel = "mock-embed"

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"query": "test"}

	res, err := a.handleSearch(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m := parseResult(t, res)
	count := int(m["count"].(float64))
	if count != 0 {
		t.Errorf("expected 0 results, got %d", count)
	}
}

func TestContextSearch_RankedResults(t *testing.T) {
	s := newTestStore(t)
	a := New(s, "")
	provider := embedding.NewMockProvider(128)
	a.EmbeddingProvider = provider
	a.EmbeddingModel = "mock-embed"

	// Write and embed several records.
	records := []struct {
		ns, key, payload string
	}{
		{"app/notes", "alpha", `{"content":"machine learning neural networks"}`},
		{"app/notes", "beta", `{"content":"database schema migration"}`},
		{"app/notes", "gamma", `{"content":"deep learning transformers attention"}`},
	}

	for _, r := range records {
		rec := writeRecord(t, s, r.ns, r.key, r.payload)
		embedReq := mcp.CallToolRequest{}
		embedReq.Params.Arguments = map[string]any{
			"record_id": rec.RecordID,
			"namespace": r.ns,
			"key":       r.key,
		}
		embedRes, err := a.handleEmbed(context.Background(), embedReq)
		if err != nil {
			t.Fatalf("embed %s: %v", r.key, err)
		}
		embedM := parseResult(t, embedRes)
		if embedM["status"] != "stored" {
			t.Fatalf("embed %s failed: %v", r.key, embedM)
		}
	}

	// Search.
	searchReq := mcp.CallToolRequest{}
	searchReq.Params.Arguments = map[string]any{
		"query":     "neural networks deep learning",
		"limit":     float64(10),
		"threshold": float64(-1),
	}

	res, err := a.handleSearch(context.Background(), searchReq)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	m := parseResult(t, res)
	count := int(m["count"].(float64))
	if count == 0 {
		t.Fatal("expected results, got 0")
	}

	// Verify results are an array with expected fields.
	results, ok := m["results"].([]any)
	if !ok {
		t.Fatalf("results is not an array: %T", m["results"])
	}
	for i, r := range results {
		result := r.(map[string]any)
		if result["record_id"] == nil || result["namespace"] == nil || result["key"] == nil || result["score"] == nil {
			t.Errorf("result[%d] missing fields: %v", i, result)
		}
	}
}

func TestContextSearch_NamespaceFilter(t *testing.T) {
	s := newTestStore(t)
	a := New(s, "")
	provider := embedding.NewMockProvider(128)
	a.EmbeddingProvider = provider
	a.EmbeddingModel = "mock-embed"

	// Write records in different namespaces.
	rec1 := writeRecord(t, s, "app/notes", "a", `{"content":"test content one"}`)
	rec2 := writeRecord(t, s, "app/logs", "b", `{"content":"test content two"}`)

	for _, r := range []struct {
		rec contextstore.Record
		ns  string
	}{{rec1, "app/notes"}, {rec2, "app/logs"}} {
		req := mcp.CallToolRequest{}
		req.Params.Arguments = map[string]any{
			"record_id": r.rec.RecordID,
			"namespace": r.ns,
			"key":       r.rec.Key,
		}
		if _, err := a.handleEmbed(context.Background(), req); err != nil {
			t.Fatalf("embed: %v", err)
		}
	}

	// Search with namespace filter — should only return app/notes.
	searchReq := mcp.CallToolRequest{}
	searchReq.Params.Arguments = map[string]any{
		"query":     "test content",
		"namespace": "app/notes",
		"threshold": float64(-1),
	}

	res, err := a.handleSearch(context.Background(), searchReq)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	m := parseResult(t, res)
	results := m["results"].([]any)
	for _, r := range results {
		result := r.(map[string]any)
		ns := result["namespace"].(string)
		if ns != "app/notes" {
			t.Errorf("expected namespace app/notes, got %q", ns)
		}
	}
}

func TestExtractTextForEmbedding(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		want    string
	}{
		{"title field", `{"title":"My Title"}`, "My Title"},
		{"content field", `{"content":"hello"}`, "hello"},
		{"multiple fields", `{"title":"T","summary":"S"}`, "T\nS"},
		{"raw text", `"plain text"`, `"plain text"`},
		{"empty", `{}`, "{}"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := contextstore.Record{Payload: json.RawMessage(tt.payload)}
			got := extractTextForEmbedding(rec)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}
