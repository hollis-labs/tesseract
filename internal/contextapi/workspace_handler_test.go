package contextapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/hollis-labs/tesseract/internal/contextpolicy"
	"github.com/hollis-labs/tesseract/internal/contextstore"
	"github.com/hollis-labs/tesseract/internal/memory"
	"github.com/hollis-labs/tesseract/internal/workspace"
)

const httpWorkspaceNS = "project/tesseract/workspace/http-tests"

func newWorkspaceTestServer(t *testing.T) *Server {
	t.Helper()
	cs, err := contextstore.Open(context.Background(), contextstore.Config{RootDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	srv := NewServer(cs, contextpolicy.New())
	srv.MemoryStore = memory.NewStore(cs.DB(), nil, "", 0, memory.NoopQueue{})
	srv.WorkspaceStore = workspace.NewStore(cs.DB())
	return srv
}

func decodeHTTPJSON(t *testing.T, rrBody []byte) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(rrBody, &body); err != nil {
		t.Fatalf("decode response: %v; body=%s", err, rrBody)
	}
	return body
}

func TestWorkspaceHTTPCreateReplayReadEditRecallTouchDelete(t *testing.T) {
	srv := newWorkspaceTestServer(t)
	createBody := map[string]any{
		"namespace": httpWorkspaceNS, "idempotency_key": "http-retry-1",
		"summary": "alpha draft", "data": map[string]any{"n": 1},
		"author": map[string]any{"agent_id": "http-test"}, "session_id": "http-session",
	}
	created := performJSON(t, srv, http.MethodPost, "/v1/workspace/write", createBody)
	if created.Code != http.StatusOK {
		t.Fatalf("create status=%d body=%s", created.Code, created.Body.String())
	}
	createReceipt := decodeHTTPJSON(t, created.Body.Bytes())
	itemID, _ := createReceipt["item_id"].(string)
	version, _ := createReceipt["version_token"].(string)
	if createReceipt["status"] != "created" || itemID == "" || version == "" {
		t.Fatalf("create receipt=%v", createReceipt)
	}

	replayed := performJSON(t, srv, http.MethodPost, "/v1/workspace/write", createBody)
	replayReceipt := decodeHTTPJSON(t, replayed.Body.Bytes())
	if replayed.Code != http.StatusOK || replayReceipt["status"] != "replayed" || replayReceipt["item_id"] != itemID || replayReceipt["availability"] != "live" {
		t.Fatalf("replay status=%d receipt=%v", replayed.Code, replayReceipt)
	}
	if _, ok := replayReceipt["version_token"]; ok {
		t.Fatalf("replay exposed stale version token: %v", replayReceipt)
	}

	changedCreate := map[string]any{}
	for k, v := range createBody {
		changedCreate[k] = v
	}
	changedCreate["summary"] = "different"
	conflict := performJSON(t, srv, http.MethodPost, "/v1/workspace/write", changedCreate)
	if conflict.Code != http.StatusConflict || decodeHTTPJSON(t, conflict.Body.Bytes())["code"] != "idempotency_conflict" {
		t.Fatalf("changed retry status=%d body=%s", conflict.Code, conflict.Body.String())
	}

	read := performJSON(t, srv, http.MethodGet, "/v1/items/"+itemID, nil)
	item := decodeHTTPJSON(t, read.Body.Bytes())
	if read.Code != http.StatusOK || item["domain"] != "workspace" || item["item_id"] != itemID {
		t.Fatalf("item read status=%d body=%v", read.Code, item)
	}
	if _, hasRevision := item["revision_id"]; hasRevision {
		t.Fatalf("workspace read fabricated revision_id: %v", item)
	}
	payload, _ := item["payload"].(map[string]any)
	if payload["summary"] != "alpha draft" {
		t.Fatalf("nested payload=%v", payload)
	}

	edit := performJSON(t, srv, http.MethodPost, "/v1/workspace/write", map[string]any{
		"item_id": itemID, "version_token": version, "key": "renamed-key", "summary": "alpha final",
		"author": map[string]any{"agent_id": "http-test"}, "session_id": "http-session",
	})
	editReceipt := decodeHTTPJSON(t, edit.Body.Bytes())
	currentVersion, _ := editReceipt["version_token"].(string)
	if edit.Code != http.StatusOK || editReceipt["status"] != "updated" || currentVersion == "" || currentVersion == version {
		t.Fatalf("edit status=%d receipt=%v", edit.Code, editReceipt)
	}
	stale := performJSON(t, srv, http.MethodPost, "/v1/workspace/write", map[string]any{
		"item_id": itemID, "version_token": version, "summary": "stale",
		"author": map[string]any{"agent_id": "http-test"}, "session_id": "http-session",
	})
	if stale.Code != http.StatusConflict || decodeHTTPJSON(t, stale.Body.Bytes())["code"] != "version_conflict" {
		t.Fatalf("stale edit status=%d body=%s", stale.Code, stale.Body.String())
	}

	keyRead := performJSON(t, srv, http.MethodGet, "/v1/workspace/current?namespace="+httpWorkspaceNS+"&key=renamed-key", nil)
	if keyRead.Code != http.StatusOK || decodeHTTPJSON(t, keyRead.Body.Bytes())["item_id"] != itemID {
		t.Fatalf("key read status=%d body=%s", keyRead.Code, keyRead.Body.String())
	}
	history := performJSON(t, srv, http.MethodGet, "/v1/items/"+itemID+"/history", nil)
	if history.Code != http.StatusBadRequest || decodeHTTPJSON(t, history.Body.Bytes())["code"] != "history_unavailable" {
		t.Fatalf("history status=%d body=%s", history.Code, history.Body.String())
	}

	recall := performJSON(t, srv, http.MethodPost, "/v1/memory/recall", map[string]any{
		"namespaces": []string{httpWorkspaceNS}, "ranking": "relevance", "search_mode": "lexical", "query": "alpha",
		"filters": map[string]any{"domains": []string{"workspace"}}, "payload_mode": "full",
	})
	recallBody := decodeHTTPJSON(t, recall.Body.Bytes())
	if recall.Code != http.StatusOK {
		t.Fatalf("recall status=%d body=%s", recall.Code, recall.Body.String())
	}
	results, _ := recallBody["results"].([]any)
	if len(results) != 1 || results[0].(map[string]any)["item"] == nil || results[0].(map[string]any)["revision"] != nil {
		t.Fatalf("typed recall results=%v", results)
	}

	touch := performJSON(t, srv, http.MethodPost, "/v1/memory/touch", map[string]any{"item_ids": []string{itemID, "missing-item"}})
	touchBody := decodeHTTPJSON(t, touch.Body.Bytes())
	if touch.Code != http.StatusOK || int(touchBody["touched"].(float64)) != 1 {
		t.Fatalf("touch status=%d body=%v", touch.Code, touchBody)
	}
	emptyTouch := performJSON(t, srv, http.MethodPost, "/v1/memory/touch", map[string]any{"item_ids": []string{}})
	if emptyTouch.Code != http.StatusBadRequest || decodeHTTPJSON(t, emptyTouch.Body.Bytes())["code"] != "validation_error" {
		t.Fatalf("empty touch status=%d body=%s", emptyTouch.Code, emptyTouch.Body.String())
	}

	deleted := performJSON(t, srv, http.MethodPost, "/v1/workspace/delete", map[string]any{"item_id": itemID, "version_token": currentVersion})
	if deleted.Code != http.StatusOK || decodeHTTPJSON(t, deleted.Body.Bytes())["status"] != "deleted" {
		t.Fatalf("delete status=%d body=%s", deleted.Code, deleted.Body.String())
	}
	repeated := performJSON(t, srv, http.MethodPost, "/v1/workspace/delete", map[string]any{"item_id": itemID, "version_token": "already-gone"})
	if repeated.Code != http.StatusOK || decodeHTTPJSON(t, repeated.Body.Bytes())["status"] != "deleted" {
		t.Fatalf("repeat delete status=%d body=%s", repeated.Code, repeated.Body.String())
	}
	gone := performJSON(t, srv, http.MethodGet, "/v1/items/"+itemID, nil)
	if gone.Code != http.StatusGone || decodeHTTPJSON(t, gone.Body.Bytes())["code"] != "deleted" {
		t.Fatalf("deleted read status=%d body=%s", gone.Code, gone.Body.String())
	}
}
