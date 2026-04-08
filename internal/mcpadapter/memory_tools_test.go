package mcpadapter

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/hollis-labs/vanta-conduit/internal/contextstore"
	"github.com/hollis-labs/vanta-conduit/internal/memory"
	"github.com/mark3labs/mcp-go/mcp"
)

// newMemoryAdapter creates an Adapter wired to both context and memory stores,
// with an auth token carrying the supplied scopes.
func newMemoryAdapter(t *testing.T, scopes ...string) *Adapter {
	t.Helper()
	cs := newTestStore(t)
	ms := memory.NewStore(cs.DB(), nil, "", 0, memory.NoopQueue{})

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

	a := New(cs, tok)
	a.MemoryStore = ms
	return a
}

// writeViaHandler is a helper that calls handleMemoryWrite and returns the parsed result.
func writeViaHandler(t *testing.T, a *Adapter, args map[string]any) map[string]any {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	res, err := a.handleMemoryWrite(context.Background(), req)
	if err != nil {
		t.Fatalf("handleMemoryWrite: %v", err)
	}
	return parseResult(t, res)
}

// ── memory_write ─────────────────────────────────────────────────────────────

func TestMemoryWrite_Success(t *testing.T) {
	a := newMemoryAdapter(t, "memory:write", "memory:read")
	body := writeViaHandler(t, a, map[string]any{
		"namespace":       "user/chrispian/memory",
		"memory_key":      "user.prefs",
		"author_agent_id": "claude",
		"trigger":         "explicit",
		"session_id":      "sess-001",
		"origin":          "user",
		"confidence":      0.9,
		"payload_summary": "User prefers dark mode",
	})
	if body["revision_id"] == nil || body["revision_id"] == "" {
		t.Errorf("expected revision_id, got %v", body)
	}
	if body["namespace"] != "user/chrispian/memory" {
		t.Errorf("namespace = %v", body["namespace"])
	}
}

func TestMemoryWrite_MissingRequired(t *testing.T) {
	a := newMemoryAdapter(t, "memory:write")
	// Missing payload_summary, session_id, etc.
	body := writeViaHandler(t, a, map[string]any{
		"namespace":       "user/chrispian/memory",
		"author_agent_id": "claude",
	})
	if body["code"] != "validation_error" {
		t.Errorf("expected validation_error, got %v", body)
	}
}

// ── memory_get ───────────────────────────────────────────────────────────────

func TestMemoryGet_AfterWrite(t *testing.T) {
	a := newMemoryAdapter(t, "memory:write", "memory:read")
	writeViaHandler(t, a, map[string]any{
		"namespace":       "user/chrispian/memory",
		"memory_key":      "user.prefs",
		"author_agent_id": "claude",
		"trigger":         "explicit",
		"session_id":      "sess-001",
		"origin":          "user",
		"confidence":      0.9,
		"payload_summary": "User prefers dark mode",
	})

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"namespace":  "user/chrispian/memory",
		"memory_key": "user.prefs",
	}
	res, err := a.handleMemoryGet(context.Background(), req)
	if err != nil {
		t.Fatalf("handleMemoryGet: %v", err)
	}
	body := parseResult(t, res)
	if body["revision_id"] == nil || body["revision_id"] == "" {
		t.Errorf("expected revision_id, got %v", body)
	}
}

func TestMemoryGet_NotFound(t *testing.T) {
	a := newMemoryAdapter(t, "memory:read")
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"namespace":  "user/chrispian/memory",
		"memory_key": "nonexistent",
	}
	res, err := a.handleMemoryGet(context.Background(), req)
	if err != nil {
		t.Fatalf("handleMemoryGet: %v", err)
	}
	body := parseResult(t, res)
	if body["code"] != "not_found" {
		t.Errorf("expected not_found, got %v", body)
	}
}

// ── memory_history ───────────────────────────────────────────────────────────

func TestMemoryHistory_TwoRevisions(t *testing.T) {
	a := newMemoryAdapter(t, "memory:write", "memory:read")

	// Write first revision.
	writeViaHandler(t, a, map[string]any{
		"namespace":       "user/chrispian/memory",
		"memory_key":      "user.prefs",
		"author_agent_id": "claude",
		"trigger":         "explicit",
		"session_id":      "sess-001",
		"origin":          "user",
		"confidence":      0.8,
		"payload_summary": "First version",
	})

	// Write second revision (same key, will create new revision on same memory).
	writeViaHandler(t, a, map[string]any{
		"namespace":       "user/chrispian/memory",
		"memory_key":      "user.prefs",
		"author_agent_id": "claude",
		"trigger":         "explicit",
		"session_id":      "sess-002",
		"origin":          "user",
		"confidence":      0.9,
		"payload_summary": "Second version",
	})

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"namespace":  "user/chrispian/memory",
		"memory_key": "user.prefs",
	}
	res, err := a.handleMemoryHistory(context.Background(), req)
	if err != nil {
		t.Fatalf("handleMemoryHistory: %v", err)
	}

	// Parse as array.
	textContent, ok := res.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent")
	}
	var revs []map[string]any
	if err := json.Unmarshal([]byte(textContent.Text), &revs); err != nil {
		t.Fatalf("unmarshal history: %v", err)
	}
	if len(revs) != 2 {
		t.Errorf("expected 2 revisions, got %d", len(revs))
	}
}

// ── memory_recall ────────────────────────────────────────────────────────────

func TestMemoryRecall_ReturnsResults(t *testing.T) {
	a := newMemoryAdapter(t, "memory:write", "memory:read")

	writeViaHandler(t, a, map[string]any{
		"namespace":       "user/chrispian/memory",
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
		"namespaces": `["user/chrispian/memory"]`,
	}
	res, err := a.handleMemoryRecall(context.Background(), req)
	if err != nil {
		t.Fatalf("handleMemoryRecall: %v", err)
	}
	textContent, ok := res.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent")
	}
	var results []map[string]any
	if err := json.Unmarshal([]byte(textContent.Text), &results); err != nil {
		t.Fatalf("unmarshal recall: %v", err)
	}
	if len(results) == 0 {
		t.Error("expected at least one recall result")
	}
}

func TestMemoryRecall_SimilarityUnavailable(t *testing.T) {
	a := newMemoryAdapter(t, "memory:read")
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"namespaces": `["user/chrispian/memory"]`,
		"ranking":    "similarity",
		"query":      "dark mode",
	}
	res, err := a.handleMemoryRecall(context.Background(), req)
	if err != nil {
		t.Fatalf("handleMemoryRecall: %v", err)
	}
	body := parseResult(t, res)
	if body["code"] != "similarity_unavailable" {
		t.Errorf("expected similarity_unavailable, got %v", body)
	}
}

// ── memory_promote ───────────────────────────────────────────────────────────

func TestMemoryPromote_SessionToUser(t *testing.T) {
	a := newMemoryAdapter(t, "memory:write", "memory:read")

	// Write a memory in session scope.
	written := writeViaHandler(t, a, map[string]any{
		"namespace":       "user/chrispian/session/sess-001/memory",
		"memory_key":      "insight.dark_mode",
		"author_agent_id": "claude",
		"trigger":         "explicit",
		"session_id":      "sess-001",
		"origin":          "observation",
		"confidence":      0.85,
		"payload_summary": "User prefers dark mode consistently",
	})

	memoryID, ok := written["memory_id"].(string)
	if !ok || memoryID == "" {
		t.Fatalf("expected memory_id from write, got %v", written["memory_id"])
	}

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"source_namespace": "user/chrispian/session/sess-001/memory",
		"source_memory_id": memoryID,
		"target_namespace": "user/chrispian/memory",
		"actor_agent_id":   "claude",
	}
	res, err := a.handleMemoryPromote(context.Background(), req)
	if err != nil {
		t.Fatalf("handleMemoryPromote: %v", err)
	}
	body := parseResult(t, res)
	if body["revision_id"] == nil || body["revision_id"] == "" {
		t.Errorf("expected promoted revision_id, got %v", body)
	}
	if body["namespace"] != "user/chrispian/memory" {
		t.Errorf("expected target namespace, got %v", body["namespace"])
	}
}

// ── memory_deprecate ─────────────────────────────────────────────────────────

func TestMemoryDeprecate_Success(t *testing.T) {
	a := newMemoryAdapter(t, "memory:write", "memory:read")

	written := writeViaHandler(t, a, map[string]any{
		"namespace":       "user/chrispian/memory",
		"memory_key":      "user.prefs",
		"author_agent_id": "claude",
		"trigger":         "explicit",
		"session_id":      "sess-001",
		"origin":          "user",
		"confidence":      0.9,
		"payload_summary": "To be deprecated",
	})
	revisionID, ok := written["revision_id"].(string)
	if !ok || revisionID == "" {
		t.Fatalf("expected revision_id, got %v", written["revision_id"])
	}

	// Deprecate the only revision.
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"revision_id": revisionID,
	}
	res, err := a.handleMemoryDeprecate(context.Background(), req)
	if err != nil {
		t.Fatalf("handleMemoryDeprecate: %v", err)
	}
	body := parseResult(t, res)
	if body["status"] != "deprecated" {
		t.Errorf("expected status=deprecated, got %v", body["status"])
	}

	// Subsequent get should return not_found (only revision was deprecated).
	getReq := mcp.CallToolRequest{}
	getReq.Params.Arguments = map[string]any{
		"namespace":  "user/chrispian/memory",
		"memory_key": "user.prefs",
	}
	getRes, err := a.handleMemoryGet(context.Background(), getReq)
	if err != nil {
		t.Fatalf("handleMemoryGet after deprecate: %v", err)
	}
	getBody := parseResult(t, getRes)
	if getBody["code"] != "not_found" {
		t.Errorf("expected not_found after deprecating only revision, got %v", getBody)
	}
}
