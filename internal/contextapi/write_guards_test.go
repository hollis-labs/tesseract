package contextapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func guardPost(t *testing.T, srv *Server, path string, body map[string]any) (int, map[string]any) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	var got map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode %s response %q: %v", path, rr.Body.String(), err)
	}
	return rr.Code, got
}

func guardKnowledgeBody(key string, extra map[string]any) map[string]any {
	body := map[string]any{
		"namespace":  "user/chrispian/knowledge/framework",
		"actor":      "user",
		"key":        key,
		"kind":       "note",
		"source":     "manual",
		"pointer":    map[string]any{"scheme": "nil", "locator": "guards/" + key},
		"summary":    "guard test " + key,
		"author":     map[string]any{"agent_id": "test", "agent_version": "1"},
		"session_id": "sess-guards",
	}
	for k, v := range extra {
		body[k] = v
	}
	return body
}

// A failed guard is a 409 that names the head it lost to in structured form, so
// a caller can re-read and retry without a second lookup that could race.
func TestKnowledgeWrite_GuardsAnswer409WithDetails(t *testing.T) {
	srv := newKnowledgeTestServer(t)

	status, first := guardPost(t, srv, "/v1/knowledge/write", guardKnowledgeBody("guard.http", nil))
	if status != http.StatusOK {
		t.Fatalf("first write status = %d; body=%v", status, first)
	}
	if first["write_outcome"] != "created" {
		t.Errorf("write_outcome = %v, want created", first["write_outcome"])
	}
	head, _ := first["revision_id"].(string)
	item, _ := first["item_id"].(string)

	// create_only onto the existing key.
	status, dup := guardPost(t, srv, "/v1/knowledge/write", guardKnowledgeBody("guard.http", map[string]any{"create_only": true}))
	if status != http.StatusConflict || dup["code"] != "key_conflict" {
		t.Fatalf("create_only on an existing key: status=%d code=%v, want 409 key_conflict; body=%v", status, dup["code"], dup)
	}
	details, _ := dup["details"].(map[string]any)
	if details["current_revision_id"] != head || details["item_id"] != item {
		t.Errorf("key_conflict details = %v, want item_id=%s current_revision_id=%s", details, item, head)
	}

	// A stale expected_revision_id.
	status, stale := guardPost(t, srv, "/v1/knowledge/write",
		guardKnowledgeBody("guard.http", map[string]any{"expected_revision_id": "01HZZZZZZZZZZZZZZZZZZZZZZZ"}))
	if status != http.StatusConflict || stale["code"] != "revision_conflict" {
		t.Fatalf("stale expected_revision_id: status=%d code=%v, want 409 revision_conflict; body=%v", status, stale["code"], stale)
	}
	details, _ = stale["details"].(map[string]any)
	if details["current_revision_id"] != head || details["expected_revision_id"] != "01HZZZZZZZZZZZZZZZZZZZZZZZ" {
		t.Errorf("revision_conflict details = %v, want current_revision_id=%s and the expected id echoed", details, head)
	}

	// The current head is accepted; the response says what happened.
	status, next := guardPost(t, srv, "/v1/knowledge/write",
		guardKnowledgeBody("guard.http", map[string]any{"expected_revision_id": head, "supersedes": head}))
	if status != http.StatusOK {
		t.Fatalf("guarded edit status = %d; body=%v", status, next)
	}
	if next["write_outcome"] != "appended" || next["previous_revision_id"] != head {
		t.Errorf("guarded edit write_outcome=%v previous_revision_id=%v, want appended and %s", next["write_outcome"], next["previous_revision_id"], head)
	}

	// The two guards contradict each other.
	status, both := guardPost(t, srv, "/v1/knowledge/write",
		guardKnowledgeBody("guard.http.two", map[string]any{"create_only": true, "expected_revision_id": head}))
	if status != http.StatusBadRequest || both["code"] != "validation_error" {
		t.Errorf("both guards: status=%d code=%v, want 400 validation_error", status, both["code"])
	}
}

func TestKnowledgeWrite_StatusAndDerivedFromOverHTTP(t *testing.T) {
	srv := newKnowledgeTestServer(t)

	status, defaults := guardPost(t, srv, "/v1/knowledge/write", guardKnowledgeBody("defaults", nil))
	if status != http.StatusOK || defaults["status"] != "canonical" || defaults["derived_from"] != "reference" {
		t.Fatalf("defaults: status=%d status=%v derived_from=%v, want 200 canonical reference", status, defaults["status"], defaults["derived_from"])
	}

	status, chosen := guardPost(t, srv, "/v1/knowledge/write",
		guardKnowledgeBody("chosen", map[string]any{"status": "draft", "derived_from": "observation"}))
	if status != http.StatusOK || chosen["status"] != "draft" || chosen["derived_from"] != "observation" {
		t.Errorf("chosen: status=%d status=%v derived_from=%v, want 200 draft observation", status, chosen["status"], chosen["derived_from"])
	}

	for _, bad := range []map[string]any{{"status": "bogus"}, {"derived_from": "bogus"}} {
		status, got := guardPost(t, srv, "/v1/knowledge/write", guardKnowledgeBody("bad", bad))
		if status != http.StatusBadRequest || got["code"] != "validation_error" {
			t.Errorf("%v: status=%d code=%v, want 400 validation_error", bad, status, got["code"])
		}
	}
}

func TestMemoryWrite_CreateOnlyAnswers409OverHTTP(t *testing.T) {
	srv := newMemoryTestServer(t)
	body := func(extra map[string]any) map[string]any {
		m := map[string]any{
			"namespace": "user/chrispian/memory/notes", "actor": "user", "memory_key": "guards.http.memory",
			"author": map[string]any{"agent_id": "claude", "agent_version": "1"}, "trigger": "explicit",
			"session_id": "sess-guards", "derived_from": "user", "confidence": 0.9, "summary": "guard test",
		}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}

	status, first := guardPost(t, srv, "/v1/memory/write", body(map[string]any{"create_only": true}))
	if status != http.StatusOK || first["write_outcome"] != "created" {
		t.Fatalf("first write: status=%d write_outcome=%v; body=%v", status, first["write_outcome"], first)
	}
	status, dup := guardPost(t, srv, "/v1/memory/write", body(map[string]any{"create_only": true}))
	if status != http.StatusConflict || dup["code"] != "key_conflict" {
		t.Fatalf("duplicate: status=%d code=%v, want 409 key_conflict; body=%v", status, dup["code"], dup)
	}
}

// write_outcome and previous_revision_id describe a write, so a read of the
// same revision must not carry them.
func TestKnowledgeGetCurrent_CarriesNoWriteOnlyFields(t *testing.T) {
	srv := newKnowledgeTestServer(t)
	if status, wrote := guardPost(t, srv, "/v1/knowledge/write", guardKnowledgeBody("read.me", nil)); status != http.StatusOK {
		t.Fatalf("write status = %d; body=%v", status, wrote)
	}

	req := httptest.NewRequest(http.MethodGet,
		"/v1/knowledge/current?namespace=user/chrispian/knowledge/framework&key=read.me", nil)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("get status = %d; body=%s", rr.Code, rr.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, field := range []string{"write_outcome", "previous_revision_id"} {
		if _, present := got[field]; present {
			t.Errorf("a read carries write-only field %q", field)
		}
	}
}
