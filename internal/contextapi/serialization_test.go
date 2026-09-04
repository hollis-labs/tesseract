package contextapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWriteJSONMarshalFailureChangesUncommittedStatusTo500(t *testing.T) {
	rr := httptest.NewRecorder()
	writeJSON(rr, http.StatusCreated, map[string]any{
		"payload": json.RawMessage(`{"unfinished":`),
	})

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d; success was committed before serialization failed", rr.Code, http.StatusInternalServerError)
	}
	if got := rr.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q", got)
	}
	var failure struct {
		Code    string         `json:"code"`
		Message string         `json:"message"`
		Details map[string]any `json:"details"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &failure); err != nil {
		t.Fatalf("failure body is not valid JSON: %q: %v", rr.Body.String(), err)
	}
	if failure.Code != "serialization_failed" || failure.Message != "failed to serialize response" {
		t.Fatalf("unexpected failure body: %+v", failure)
	}
	if detail, _ := failure.Details["error"].(string); !strings.Contains(detail, "unexpected end of JSON input") {
		t.Fatalf("details do not retain the marshal cause: %+v", failure.Details)
	}
}

func TestWriteJSONBuffersSuccessfulDocument(t *testing.T) {
	rr := httptest.NewRecorder()
	writeJSON(rr, http.StatusAccepted, map[string]any{"ok": true})

	if rr.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusAccepted)
	}
	if !json.Valid(rr.Body.Bytes()) {
		t.Fatalf("body is not valid JSON: %q", rr.Body.String())
	}
}
