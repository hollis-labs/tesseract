package mcpadapter

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/hollis-labs/vanta-conduit/internal/contextstore"
	"github.com/mark3labs/mcp-go/mcp"
)

// newTestStore opens a temp-dir store for testing.
func newTestStore(t *testing.T) *contextstore.Store {
	t.Helper()
	root := t.TempDir()
	s, err := contextstore.Open(context.Background(), contextstore.Config{RootDir: root})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// toolResult decodes a tool result into a map for assertions.
func parseResult(t *testing.T, res *mcp.CallToolResult) map[string]any {
	t.Helper()
	if res == nil || len(res.Content) == 0 {
		t.Fatal("empty tool result")
	}
	textContent, ok := res.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", res.Content[0])
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(textContent.Text), &m); err != nil {
		t.Fatalf("unmarshal result %q: %v", textContent.Text, err)
	}
	return m
}

// writeRecord is a test helper that writes a record directly to the store.
func writeRecord(t *testing.T, s *contextstore.Store, ns, key, payload string) contextstore.Record {
	t.Helper()
	rec, err := s.AppendRecord(context.Background(), contextstore.AppendInput{
		Namespace: ns,
		Key:       key,
		Actor:     "test",
		Payload:   json.RawMessage(payload),
	})
	if err != nil {
		t.Fatalf("write %s/%s: %v", ns, key, err)
	}
	return rec
}

// --- TASK-020: Foundation ---

func TestAdapterNew(t *testing.T) {
	s := newTestStore(t)
	a := New(s, "")
	if a.Store != s {
		t.Error("store not set")
	}
	if a.Token != "" {
		t.Error("token should be empty")
	}
}

// --- TASK-021: Read tools ---

func TestContextHead_Found(t *testing.T) {
	s := newTestStore(t)
	writeRecord(t, s, "user/memory/task-001", "state", `{"phase":"start"}`)

	a := New(s, "")
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"namespace": "user/memory/task-001", "key": "state"}

	res, err := a.handleHead(context.Background(), req)
	if err != nil {
		t.Fatalf("handleHead: %v", err)
	}
	body := parseResult(t, res)
	if body["namespace"] != "user/memory/task-001" {
		t.Errorf("namespace = %v", body["namespace"])
	}
	if body["key"] != "state" {
		t.Errorf("key = %v", body["key"])
	}
	if body["record_id"] == "" {
		t.Error("record_id should be set")
	}
}

func TestContextHead_NotFound(t *testing.T) {
	s := newTestStore(t)
	a := New(s, "")
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"namespace": "user/memory/nope", "key": "missing"}

	res, err := a.handleHead(context.Background(), req)
	if err != nil {
		t.Fatalf("handleHead: %v", err)
	}
	body := parseResult(t, res)
	if body["code"] != "not_found" {
		t.Errorf("expected not_found, got %v", body["code"])
	}
}

func TestContextHead_MissingArgs(t *testing.T) {
	a := New(newTestStore(t), "")
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"namespace": "user/memory/task-001"} // missing key
	res, _ := a.handleHead(context.Background(), req)
	body := parseResult(t, res)
	if body["code"] != "validation_error" {
		t.Errorf("expected validation_error, got %v", body["code"])
	}
}

func TestContextHistory(t *testing.T) {
	s := newTestStore(t)
	for i := 0; i < 3; i++ {
		writeRecord(t, s, "user/memory/task-001", "state", `{"rev":`+string(rune('0'+i))+`}`)
	}

	a := New(s, "")
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"namespace": "user/memory/task-001", "key": "state", "limit": float64(10)}

	res, err := a.handleHistory(context.Background(), req)
	if err != nil {
		t.Fatalf("handleHistory: %v", err)
	}
	body := parseResult(t, res)
	count, _ := body["count"].(float64)
	if int(count) != 3 {
		t.Errorf("expected 3 records, got %v", body["count"])
	}
}

func TestContextView(t *testing.T) {
	s := newTestStore(t)
	writeRecord(t, s, "user/memory/task-001", "state", `{"v":1}`)
	writeRecord(t, s, "user/memory/task-002", "state", `{"v":2}`)
	writeRecord(t, s, "app/test/session", "state", `{"v":3}`)

	a := New(s, "")
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"namespaces":     "user/memory/*",
		"revision_scope": "head",
	}

	res, err := a.handleView(context.Background(), req)
	if err != nil {
		t.Fatalf("handleView: %v", err)
	}
	body := parseResult(t, res)
	count, _ := body["count"].(float64)
	if int(count) != 2 {
		t.Errorf("expected 2 records, got %v", body["count"])
	}
}

// --- TASK-021: context_packet ---

func TestContextPacket_WithPins(t *testing.T) {
	s := newTestStore(t)
	writeRecord(t, s, "user/pins/project", "context", `{"project":"test"}`)
	writeRecord(t, s, "app/test/session/task-001", "state", `{"status":"active"}`)

	a := New(s, "")
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"namespaces":   "app/test/session/task-001",
		"include_pins": true,
		"max_items":    float64(50),
	}

	res, err := a.handlePacket(context.Background(), req)
	if err != nil {
		t.Fatalf("handlePacket: %v", err)
	}
	body := parseResult(t, res)
	manifest := body["manifest"].(map[string]any)
	pins, _ := manifest["pins_included"].(float64)
	if int(pins) != 1 {
		t.Errorf("expected 1 pin, got %v", manifest["pins_included"])
	}
	items := body["items"].([]any)
	if len(items) != 2 {
		t.Errorf("expected 2 items (1 pin + 1 record), got %d", len(items))
	}
}

func TestContextPacket_EmptyResult(t *testing.T) {
	s := newTestStore(t)
	a := New(s, "")
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"namespaces":   "user/memory/*",
		"include_pins": false,
	}
	res, err := a.handlePacket(context.Background(), req)
	if err != nil {
		t.Fatalf("handlePacket: %v", err)
	}
	body := parseResult(t, res)
	items := body["items"].([]any)
	if len(items) != 0 {
		t.Errorf("expected 0 items, got %d", len(items))
	}
}

// --- TASK-022: Write tools ---

func TestContextWrite_NoToken(t *testing.T) {
	s := newTestStore(t)
	a := New(s, "") // no token
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"namespace": "app/test/session/task-001",
		"key":       "state",
		"payload":   `{"phase":"start"}`,
	}
	res, err := a.handleWrite(context.Background(), req)
	if err != nil {
		t.Fatalf("handleWrite: %v", err)
	}
	body := parseResult(t, res)
	if body["code"] != "auth_required" {
		t.Errorf("expected auth_required, got %v", body["code"])
	}
}

func TestContextWrite_InsufficientScope(t *testing.T) {
	s := newTestStore(t)
	// Issue a token with only "packet" scope (not "write").
	tok, _, err := s.CreateAuthToken(context.Background(), contextstore.TokenCreateInput{
		Label:  "read-only",
		Scopes: []string{"packet"},
	})
	if err != nil {
		t.Fatalf("create token: %v", err)
	}

	a := New(s, tok)
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"namespace": "app/test/session/task-001",
		"key":       "state",
		"payload":   `{"phase":"start"}`,
	}
	res, _ := a.handleWrite(context.Background(), req)
	body := parseResult(t, res)
	if body["code"] != "insufficient_scope" {
		t.Errorf("expected insufficient_scope, got %v", body["code"])
	}
}

func TestContextWrite_Success(t *testing.T) {
	s := newTestStore(t)
	tok, _, err := s.CreateAuthToken(context.Background(), contextstore.TokenCreateInput{
		Label:  "agent",
		Scopes: []string{"write"},
	})
	if err != nil {
		t.Fatalf("create token: %v", err)
	}

	a := New(s, tok)
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"namespace": "app/test/session/task-001",
		"key":       "state",
		"payload":   `{"phase":"start","status":"active"}`,
		"actor":     "app:my-agent",
	}
	res, err := a.handleWrite(context.Background(), req)
	if err != nil {
		t.Fatalf("handleWrite: %v", err)
	}
	body := parseResult(t, res)
	if body["record_id"] == nil || body["record_id"] == "" {
		t.Errorf("expected record_id in response, got %v", body)
	}
	if body["revision"] == nil {
		t.Error("expected revision in response")
	}
	// Verify the record was actually written.
	rec, err := s.Head(context.Background(), "app/test/session/task-001", "state")
	if err != nil {
		t.Fatalf("head after write: %v", err)
	}
	if rec.Actor != "app:my-agent" {
		t.Errorf("actor = %v", rec.Actor)
	}
}

func TestContextWrite_InvalidPayload(t *testing.T) {
	s := newTestStore(t)
	tok, _, err := s.CreateAuthToken(context.Background(), contextstore.TokenCreateInput{
		Label:  "agent",
		Scopes: []string{"write"},
	})
	if err != nil {
		t.Fatalf("create token: %v", err)
	}

	a := New(s, tok)
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"namespace": "app/test/session/task-001",
		"key":       "state",
		"payload":   "not json",
	}
	res, _ := a.handleWrite(context.Background(), req)
	body := parseResult(t, res)
	if body["code"] != "validation_error" {
		t.Errorf("expected validation_error, got %v", body["code"])
	}
}

func TestContextPromoteRequest_Success(t *testing.T) {
	s := newTestStore(t)
	tok, _, err := s.CreateAuthToken(context.Background(), contextstore.TokenCreateInput{
		Label:  "agent",
		Scopes: []string{"write", "promote.request"},
	})
	if err != nil {
		t.Fatalf("create token: %v", err)
	}

	// Write source record.
	writeRecord(t, s, "app/test/draft", "user-preference", `{"preference":"verbose output"}`)

	a := New(s, tok)
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"source_namespace": "app/test/draft",
		"source_key":       "user-preference",
		"target_namespace": "user/memory/preferences",
		"target_key":       "test-preference",
		"reason":           "high confidence preference",
		"actor":            "app:my-agent",
	}
	res, err := a.handlePromoteRequest(context.Background(), req)
	if err != nil {
		t.Fatalf("handlePromoteRequest: %v", err)
	}
	body := parseResult(t, res)
	if body["request_id"] == nil || body["request_id"] == "" {
		t.Errorf("expected request_id, got %v", body)
	}
	if body["status"] != "pending" {
		t.Errorf("expected status=pending, got %v", body["status"])
	}
}

func TestContextPromoteRequest_NoToken(t *testing.T) {
	s := newTestStore(t)
	a := New(s, "")
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"source_namespace": "app/test/draft",
		"source_key":       "pref",
		"target_namespace": "user/memory/prefs",
		"target_key":       "pref",
	}
	res, _ := a.handlePromoteRequest(context.Background(), req)
	body := parseResult(t, res)
	if body["code"] != "auth_required" {
		t.Errorf("expected auth_required, got %v", body["code"])
	}
}

// writeToken creates a scoped token for testing.
func writeToken(t *testing.T, s *contextstore.Store, scopes, globs []string) string {
	t.Helper()
	tok, _, err := s.CreateAuthToken(context.Background(), contextstore.TokenCreateInput{
		Label:          "test-token",
		Scopes:         scopes,
		NamespaceGlobs: globs,
	})
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	return tok
}

// parseItems extracts "items" from a result body as []any.
func parseItems(t *testing.T, body map[string]any) []any {
	t.Helper()
	items, ok := body["items"].([]any)
	if !ok {
		t.Fatalf("expected items array, got %T: %v", body["items"], body["items"])
	}
	return items
}

// --- P0a: Namespace glob filtering in reads ---

func TestContextView_TokenFiltersToMatchingNamespaces(t *testing.T) {
	s := newTestStore(t)
	writeRecord(t, s, "app/test/session", "state", `{"v":1}`)
	writeRecord(t, s, "user/memory/task-001", "state", `{"v":2}`)

	// Token scoped only to app/test/*
	tok := writeToken(t, s, []string{}, []string{"app/test/*"})
	a := New(s, tok)
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"namespaces":     "app/test/*,user/memory/*",
		"revision_scope": "head",
	}
	res, err := a.handleView(context.Background(), req)
	if err != nil {
		t.Fatalf("handleView: %v", err)
	}
	body := parseResult(t, res)
	items := parseItems(t, body)
	if len(items) != 1 {
		t.Errorf("expected 1 item (token scoped to app/test/*), got %d", len(items))
	}
}

func TestContextView_NoToken_ReturnsAll(t *testing.T) {
	s := newTestStore(t)
	writeRecord(t, s, "app/test/session", "state", `{"v":1}`)
	writeRecord(t, s, "user/memory/task-001", "state", `{"v":2}`)

	a := New(s, "") // no token → no filtering
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"namespaces":     "app/test/*,user/memory/*",
		"revision_scope": "head",
	}
	res, _ := a.handleView(context.Background(), req)
	body := parseResult(t, res)
	items := parseItems(t, body)
	if len(items) != 2 {
		t.Errorf("expected 2 items (no token), got %d", len(items))
	}
}

func TestContextPacket_TokenFiltersToMatchingNamespaces(t *testing.T) {
	s := newTestStore(t)
	writeRecord(t, s, "app/agent/session", "state", `{"v":1}`)
	writeRecord(t, s, "user/memory/task-001", "state", `{"v":2}`)
	// Pin is in user/pins/* — also should be filtered if not in globs.
	writeRecord(t, s, "user/pins/brief", "context", `{"brief":"test"}`)

	// Token scoped only to app/agent/*
	tok := writeToken(t, s, []string{}, []string{"app/agent/*"})
	a := New(s, tok)
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"namespaces":   "app/agent/*,user/memory/*",
		"include_pins": true,
	}
	res, err := a.handlePacket(context.Background(), req)
	if err != nil {
		t.Fatalf("handlePacket: %v", err)
	}
	body := parseResult(t, res)
	items := parseItems(t, body)
	// Only app/agent/session record should be returned; user/* filtered out.
	if len(items) != 1 {
		t.Errorf("expected 1 item (token scoped to app/agent/*), got %d: %v", len(items), body)
	}
}

// --- P0b: Namespace glob check in writes ---

func TestContextWrite_NamespaceNotInTokenGlobs(t *testing.T) {
	s := newTestStore(t)
	// Token has write scope but scoped only to app/agent/*
	tok := writeToken(t, s, []string{"write"}, []string{"app/agent/*"})
	a := New(s, tok)
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"namespace": "user/memory/task-001", // outside token globs
		"key":       "state",
		"payload":   `{"phase":"start"}`,
	}
	res, err := a.handleWrite(context.Background(), req)
	if err != nil {
		t.Fatalf("handleWrite: %v", err)
	}
	body := parseResult(t, res)
	if body["code"] != "namespace_not_permitted" {
		t.Errorf("expected namespace_not_permitted, got %v", body["code"])
	}
}

func TestContextWrite_NamespaceInTokenGlobs_Succeeds(t *testing.T) {
	s := newTestStore(t)
	tok := writeToken(t, s, []string{"write"}, []string{"app/agent/*"})
	a := New(s, tok)
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"namespace": "app/agent/session",
		"key":       "state",
		"payload":   `{"phase":"start"}`,
	}
	res, err := a.handleWrite(context.Background(), req)
	if err != nil {
		t.Fatalf("handleWrite: %v", err)
	}
	body := parseResult(t, res)
	if body["code"] != nil {
		t.Errorf("expected success, got error: %v", body)
	}
	if body["record_id"] == nil {
		t.Error("expected record_id in response")
	}
}

// --- P1: Promotion lifecycle ---

func TestContextPromoteList_Empty(t *testing.T) {
	s := newTestStore(t)
	a := New(s, "")
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{}

	res, err := a.handlePromoteList(context.Background(), req)
	if err != nil {
		t.Fatalf("handlePromoteList: %v", err)
	}
	body := parseResult(t, res)
	items := parseItems(t, body)
	if len(items) != 0 {
		t.Errorf("expected 0 items, got %d", len(items))
	}
}

func TestContextPromoteList_ReturnsPendingRequests(t *testing.T) {
	s := newTestStore(t)
	tok := writeToken(t, s, []string{"write", "promote.request"}, []string{"app/agent/*"})
	writeRecord(t, s, "app/agent/session", "summary", `{"text":"ready"}`)

	a := New(s, tok)

	// Create a promotion request.
	writeReq := mcp.CallToolRequest{}
	writeReq.Params.Arguments = map[string]any{
		"source_namespace": "app/agent/session",
		"source_key":       "summary",
		"target_namespace": "user/memory/agent",
		"target_key":       "summary",
	}
	_, _ = a.handlePromoteRequest(context.Background(), writeReq)

	// List should return it.
	listReq := mcp.CallToolRequest{}
	listReq.Params.Arguments = map[string]any{"status": "pending"}
	res, err := a.handlePromoteList(context.Background(), listReq)
	if err != nil {
		t.Fatalf("handlePromoteList: %v", err)
	}
	body := parseResult(t, res)
	items := parseItems(t, body)
	if len(items) != 1 {
		t.Errorf("expected 1 pending request, got %d", len(items))
	}
}

func TestContextPromoteApprove_NoToken(t *testing.T) {
	s := newTestStore(t)
	a := New(s, "")
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"request_id": "req-abc"}

	res, err := a.handlePromoteApprove(context.Background(), req)
	if err != nil {
		t.Fatalf("handlePromoteApprove: %v", err)
	}
	body := parseResult(t, res)
	if body["code"] != "auth_required" {
		t.Errorf("expected auth_required, got %v", body["code"])
	}
}

func TestContextPromoteApprove_Success(t *testing.T) {
	s := newTestStore(t)
	tok := writeToken(t, s, []string{"write", "promote.request", "promote.approve"}, []string{"*"})
	writeRecord(t, s, "app/agent/session", "summary", `{"text":"ready"}`)

	a := New(s, tok)

	// Create a request first.
	writeReq := mcp.CallToolRequest{}
	writeReq.Params.Arguments = map[string]any{
		"source_namespace": "app/agent/session",
		"source_key":       "summary",
		"target_namespace": "user/memory/agent",
		"target_key":       "summary",
	}
	promRes, _ := a.handlePromoteRequest(context.Background(), writeReq)
	promBody := parseResult(t, promRes)
	requestID := promBody["request_id"].(string)

	// Approve it.
	approveReq := mcp.CallToolRequest{}
	approveReq.Params.Arguments = map[string]any{
		"request_id": requestID,
		"notes":      "looks good",
	}
	res, err := a.handlePromoteApprove(context.Background(), approveReq)
	if err != nil {
		t.Fatalf("handlePromoteApprove: %v", err)
	}
	body := parseResult(t, res)
	if body["code"] != nil {
		t.Errorf("expected success, got: %v", body)
	}
	if body["approval_id"] == nil {
		t.Error("expected approval_id in response")
	}
	if body["status"] != "approved" {
		t.Errorf("expected status=approved, got %v", body["status"])
	}
}

func TestContextPromoteApprove_AlreadyApproved(t *testing.T) {
	s := newTestStore(t)
	tok := writeToken(t, s, []string{"write", "promote.request", "promote.approve"}, []string{"*"})
	writeRecord(t, s, "app/agent/session", "summary", `{"text":"ready"}`)
	a := New(s, tok)

	writeReq := mcp.CallToolRequest{}
	writeReq.Params.Arguments = map[string]any{
		"source_namespace": "app/agent/session",
		"source_key":       "summary",
		"target_namespace": "user/memory/agent",
		"target_key":       "summary",
	}
	promRes, _ := a.handlePromoteRequest(context.Background(), writeReq)
	requestID := parseResult(t, promRes)["request_id"].(string)

	// Approve once.
	approveReq := mcp.CallToolRequest{}
	approveReq.Params.Arguments = map[string]any{"request_id": requestID}
	a.handlePromoteApprove(context.Background(), approveReq) //nolint

	// Approve again — should fail.
	res, _ := a.handlePromoteApprove(context.Background(), approveReq)
	body := parseResult(t, res)
	if body["code"] != "invalid_state" {
		t.Errorf("expected invalid_state, got %v", body["code"])
	}
}

func TestContextPromoteApply_NoToken(t *testing.T) {
	s := newTestStore(t)
	a := New(s, "")
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"request_id": "req-abc"}

	res, _ := a.handlePromoteApply(context.Background(), req)
	body := parseResult(t, res)
	if body["code"] != "auth_required" {
		t.Errorf("expected auth_required, got %v", body["code"])
	}
}

func TestContextPromoteApply_NotApproved(t *testing.T) {
	s := newTestStore(t)
	tok := writeToken(t, s, []string{"write", "promote.request", "promote.apply"}, []string{"*"})
	writeRecord(t, s, "app/agent/session", "summary", `{"text":"ready"}`)
	a := New(s, tok)

	writeReq := mcp.CallToolRequest{}
	writeReq.Params.Arguments = map[string]any{
		"source_namespace": "app/agent/session",
		"source_key":       "summary",
		"target_namespace": "user/memory/agent",
		"target_key":       "summary",
	}
	promRes, _ := a.handlePromoteRequest(context.Background(), writeReq)
	requestID := parseResult(t, promRes)["request_id"].(string)

	// Try to apply without approving first.
	applyReq := mcp.CallToolRequest{}
	applyReq.Params.Arguments = map[string]any{"request_id": requestID}
	res, _ := a.handlePromoteApply(context.Background(), applyReq)
	body := parseResult(t, res)
	if body["code"] != "invalid_state" {
		t.Errorf("expected invalid_state, got %v", body["code"])
	}
}

func TestContextPromoteApply_Success(t *testing.T) {
	s := newTestStore(t)
	tok := writeToken(t, s, []string{"write", "promote.request", "promote.approve", "promote.apply"}, []string{"*"})
	writeRecord(t, s, "app/agent/session", "summary", `{"text":"approved content"}`)
	a := New(s, tok)

	// Request.
	writeReq := mcp.CallToolRequest{}
	writeReq.Params.Arguments = map[string]any{
		"source_namespace": "app/agent/session",
		"source_key":       "summary",
		"target_namespace": "user/memory/agent",
		"target_key":       "summary",
	}
	promRes, _ := a.handlePromoteRequest(context.Background(), writeReq)
	requestID := parseResult(t, promRes)["request_id"].(string)

	// Approve.
	approveReq := mcp.CallToolRequest{}
	approveReq.Params.Arguments = map[string]any{"request_id": requestID}
	a.handlePromoteApprove(context.Background(), approveReq) //nolint

	// Apply.
	applyReq := mcp.CallToolRequest{}
	applyReq.Params.Arguments = map[string]any{"request_id": requestID}
	res, err := a.handlePromoteApply(context.Background(), applyReq)
	if err != nil {
		t.Fatalf("handlePromoteApply: %v", err)
	}
	body := parseResult(t, res)
	if body["code"] != nil {
		t.Errorf("expected success, got: %v", body)
	}
	if body["record_id"] == nil {
		t.Error("expected record_id in response")
	}
	if body["status"] != "applied" {
		t.Errorf("expected status=applied, got %v", body["status"])
	}

	// Verify the record landed in the target namespace.
	rec, err := s.Head(context.Background(), "user/memory/agent", "summary")
	if err != nil {
		t.Fatalf("head at target: %v", err)
	}
	var payload map[string]any
	_ = json.Unmarshal(rec.Payload, &payload)
	if payload["text"] != "approved content" {
		t.Errorf("target record payload = %v", payload)
	}
}

// --- P3: Context planner tools ---

func TestContextPlan_BootProject(t *testing.T) {
	a := New(newTestStore(t), "")
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"intent": "boot_project",
	}
	res, err := a.handleContextPlan(context.Background(), req)
	if err != nil {
		t.Fatalf("handleContextPlan: %v", err)
	}
	body := parseResult(t, res)
	if body["code"] != nil {
		t.Fatalf("unexpected error: %v", body)
	}
	plan := body["plan"].(map[string]any)
	ns := plan["namespaces"].([]any)
	if len(ns) == 0 {
		t.Error("expected namespaces in plan")
	}
	// boot_project must include user/memory/* and user/pins/*
	found := map[string]bool{}
	for _, n := range ns {
		found[n.(string)] = true
	}
	if !found["user/memory/*"] {
		t.Errorf("boot_project plan missing user/memory/*, got %v", ns)
	}
	if !found["user/pins/*"] {
		t.Errorf("boot_project plan missing user/pins/*, got %v", ns)
	}
	if body["rationale"] == "" || body["rationale"] == nil {
		t.Error("expected non-empty rationale")
	}
}

func TestContextPlan_ResumeTask_ExtractsKeywords(t *testing.T) {
	a := New(newTestStore(t), "")
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"intent":  "resume_task",
		"summary": "implementing authentication middleware for the API",
	}
	res, _ := a.handleContextPlan(context.Background(), req)
	body := parseResult(t, res)
	plan := body["plan"].(map[string]any)
	ns := plan["namespaces"].([]any)
	// Should derive keyword-based patterns from "implementing authentication middleware api"
	if len(ns) == 0 {
		t.Error("expected namespaces from keyword extraction")
	}
}

func TestContextPlan_Custom_ReturnsUserStar(t *testing.T) {
	a := New(newTestStore(t), "")
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"intent": "custom"}
	res, _ := a.handleContextPlan(context.Background(), req)
	body := parseResult(t, res)
	plan := body["plan"].(map[string]any)
	ns := plan["namespaces"].([]any)
	found := false
	for _, n := range ns {
		if n.(string) == "user/*" {
			found = true
		}
	}
	if !found {
		t.Errorf("custom intent should return user/*, got %v", ns)
	}
}

func TestContextFetch_ReturnsPacketWithManifest(t *testing.T) {
	s := newTestStore(t)
	writeRecord(t, s, "user/memory/task-001", "state", `{"v":1}`)
	writeRecord(t, s, "user/pins/brief", "context", `{"brief":"project"}`)

	a := New(s, "")
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"intent":       "boot_project",
		"budget_items": float64(50),
	}
	res, err := a.handleContextFetch(context.Background(), req)
	if err != nil {
		t.Fatalf("handleContextFetch: %v", err)
	}
	body := parseResult(t, res)
	if body["code"] != nil {
		t.Fatalf("unexpected error: %v", body)
	}
	items := parseItems(t, body)
	if len(items) == 0 {
		t.Error("expected items from boot_project fetch")
	}
	manifest := body["manifest"].(map[string]any)
	if manifest["request_id"] == nil || manifest["request_id"] == "" {
		t.Error("expected request_id in manifest")
	}
	if body["rationale"] == nil || body["rationale"] == "" {
		t.Error("expected rationale in response")
	}
}

func TestContextFetch_Empty(t *testing.T) {
	a := New(newTestStore(t), "")
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"intent": "boot_project"}
	res, _ := a.handleContextFetch(context.Background(), req)
	body := parseResult(t, res)
	items := parseItems(t, body)
	if len(items) != 0 {
		t.Errorf("expected 0 items in empty store, got %d", len(items))
	}
}

// --- P4a: Namespace management ---

func TestContextNamespaceRegister_Success(t *testing.T) {
	s := newTestStore(t)
	tok := writeToken(t, s, []string{"namespace.admin"}, []string{"*"})
	a := New(s, tok)
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"namespace":  "app/my-agent/session",
		"owner_type": "app",
		"owner_id":   "my-agent",
	}
	res, err := a.handleNamespaceRegister(context.Background(), req)
	if err != nil {
		t.Fatalf("handleNamespaceRegister: %v", err)
	}
	body := parseResult(t, res)
	if body["code"] != nil {
		t.Fatalf("unexpected error: %v", body)
	}
	if body["namespace"] != "app/my-agent/session" {
		t.Errorf("namespace = %v", body["namespace"])
	}
	// Verify it persisted.
	entry, err := s.GetNamespacePolicy(context.Background(), "app/my-agent/session")
	if err != nil {
		t.Fatalf("GetNamespacePolicy: %v", err)
	}
	if entry.OwnerID != "my-agent" {
		t.Errorf("owner_id = %v", entry.OwnerID)
	}
}

func TestContextNamespaceRegister_NoToken(t *testing.T) {
	a := New(newTestStore(t), "")
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"namespace": "app/agent/session", "owner_type": "app", "owner_id": "agent",
	}
	res, _ := a.handleNamespaceRegister(context.Background(), req)
	body := parseResult(t, res)
	if body["code"] != "auth_required" {
		t.Errorf("expected auth_required, got %v", body["code"])
	}
}

func TestContextNamespaceRegister_InsufficientScope(t *testing.T) {
	s := newTestStore(t)
	tok := writeToken(t, s, []string{"write"}, []string{"*"}) // write scope, not namespace.admin
	a := New(s, tok)
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"namespace": "app/agent/session", "owner_type": "app", "owner_id": "agent",
	}
	res, _ := a.handleNamespaceRegister(context.Background(), req)
	body := parseResult(t, res)
	if body["code"] != "insufficient_scope" {
		t.Errorf("expected insufficient_scope, got %v", body["code"])
	}
}

func TestContextNamespaceRegister_MissingFields(t *testing.T) {
	s := newTestStore(t)
	tok := writeToken(t, s, []string{"namespace.admin"}, []string{"*"})
	a := New(s, tok)
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"namespace": "app/agent/session",
		// missing owner_type and owner_id
	}
	res, _ := a.handleNamespaceRegister(context.Background(), req)
	body := parseResult(t, res)
	if body["code"] != "validation_error" {
		t.Errorf("expected validation_error, got %v", body["code"])
	}
}

func TestContextNamespaceShow_Found(t *testing.T) {
	s := newTestStore(t)
	// Pre-register a namespace directly via store.
	if err := s.UpsertNamespacePolicy(context.Background(), contextstore.NamespacePolicyEntry{
		Namespace: "app/my-agent/session",
		OwnerType: "app",
		OwnerID:   "my-agent",
	}); err != nil {
		t.Fatalf("UpsertNamespacePolicy: %v", err)
	}

	a := New(s, "")
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"namespace": "app/my-agent/session"}
	res, err := a.handleNamespaceShow(context.Background(), req)
	if err != nil {
		t.Fatalf("handleNamespaceShow: %v", err)
	}
	body := parseResult(t, res)
	if body["namespace"] != "app/my-agent/session" {
		t.Errorf("namespace = %v", body["namespace"])
	}
	if body["owner_type"] != "app" {
		t.Errorf("owner_type = %v", body["owner_type"])
	}
	if body["owner_id"] != "my-agent" {
		t.Errorf("owner_id = %v", body["owner_id"])
	}
}

func TestContextNamespaceShow_NotFound(t *testing.T) {
	a := New(newTestStore(t), "")
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"namespace": "app/nonexistent/ns"}
	res, _ := a.handleNamespaceShow(context.Background(), req)
	body := parseResult(t, res)
	if body["code"] != "not_found" {
		t.Errorf("expected not_found, got %v", body["code"])
	}
}

func TestContextNamespaceShow_NoAuthRequired(t *testing.T) {
	s := newTestStore(t)
	_ = s.UpsertNamespacePolicy(context.Background(), contextstore.NamespacePolicyEntry{
		Namespace: "app/open/ns", OwnerType: "app", OwnerID: "test",
	})
	a := New(s, "") // no token — should still work
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"namespace": "app/open/ns"}
	res, _ := a.handleNamespaceShow(context.Background(), req)
	body := parseResult(t, res)
	if body["code"] != nil {
		t.Errorf("expected success without token, got %v", body)
	}
}

// --- P4b: Audit ---

func TestContextAudit_NoAuthRequired(t *testing.T) {
	s := newTestStore(t)
	writeRecord(t, s, "app/test/session", "state", `{"v":1}`)
	_ = s.EmitWrite(context.Background(), "test", "app/test/session", "state", 1, "", nil)

	a := New(s, "") // no token
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{}
	res, err := a.handleAudit(context.Background(), req)
	if err != nil {
		t.Fatalf("handleAudit: %v", err)
	}
	body := parseResult(t, res)
	if body["code"] != nil {
		t.Fatalf("unexpected error: %v", body)
	}
	items := parseItems(t, body)
	if len(items) == 0 {
		t.Error("expected at least 1 audit event")
	}
}

func TestContextAudit_FiltersByNamespace(t *testing.T) {
	s := newTestStore(t)
	_ = s.EmitWrite(context.Background(), "test", "app/agent/session", "state", 1, "", nil)
	_ = s.EmitWrite(context.Background(), "test", "user/memory/task", "state", 1, "", nil)

	a := New(s, "")
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"namespace": "app/agent/session"}
	res, _ := a.handleAudit(context.Background(), req)
	body := parseResult(t, res)
	items := parseItems(t, body)
	if len(items) != 1 {
		t.Errorf("expected 1 event for namespace filter, got %d", len(items))
	}
	event := items[0].(map[string]any)
	if event["namespace"] != "app/agent/session" {
		t.Errorf("namespace = %v", event["namespace"])
	}
}

func TestContextAudit_Empty(t *testing.T) {
	a := New(newTestStore(t), "")
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{}
	res, _ := a.handleAudit(context.Background(), req)
	body := parseResult(t, res)
	items := parseItems(t, body)
	if len(items) != 0 {
		t.Errorf("expected 0 items in empty store, got %d", len(items))
	}
}

func TestContextAudit_ProjectsMetadata(t *testing.T) {
	s := newTestStore(t)
	writeRecord(t, s, "app/test/session", "state", `{"v":1}`)
	_ = s.EmitWrite(context.Background(), "tester", "app/test/session", "state", 1, "rec-x",
		json.RawMessage(`{"source":"http","correlation_id":"abc"}`))

	a := New(s, "")
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{}
	res, err := a.handleAudit(context.Background(), req)
	if err != nil {
		t.Fatalf("handleAudit: %v", err)
	}
	body := parseResult(t, res)
	items := parseItems(t, body)
	if len(items) == 0 {
		t.Fatal("expected at least 1 audit event")
	}
	md, ok := items[0].(map[string]any)["metadata"]
	if !ok {
		t.Fatal("expected metadata field on audit item")
	}
	// After round-trip through JSON, metadata comes back as map[string]any.
	mdMap, ok := md.(map[string]any)
	if !ok {
		t.Fatalf("metadata is not a map: %T", md)
	}
	if mdMap["source"] != "http" {
		t.Errorf("metadata.source: got %v, want \"http\"", mdMap["source"])
	}
}
