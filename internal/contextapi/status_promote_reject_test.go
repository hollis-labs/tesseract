package contextapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/hollis-labs/tesseract/internal/contextstore"
)

// A rejected status transition has to come back as a validation error, over
// every surface that offers one.
//
// This was missing when CW-20260909-0034 landed, and the gap is instructive:
// the three promote handlers had tests for transitions that SUCCEED and none
// for one that fails, so a `nil` error being dereferenced on the rejection path
// compiled, passed the whole suite, and shipped. `ValidateContextTransition`
// was covered — but at the registry, not through the handler that reports it,
// and the defect lived in the reporting.
//
// The parallel tests are internal/contextcli/status_promote_reject_test.go and
// internal/mcpadapter/status_promote_reject_test.go. Three surfaces, one rule;
// each needs its own, because each formats the rejection its own way.
func TestStatusPromote_InvalidTransitionIsAValidationError(t *testing.T) {
	srv := newTestServer(t)

	if _, err := srv.Store.AppendRecord(context.Background(), contextstore.AppendInput{
		Namespace: "app/test/status", Key: "doc", Actor: "test",
		Payload: json.RawMessage(`{"v":1}`), Status: "draft",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// draft -> canonical skips reviewed, so the lifecycle refuses it.
	res := performJSON(t, srv, http.MethodPost, "/v1/context/status/promote", map[string]any{
		"namespace": "app/test/status",
		"key":       "doc",
		"to_status": "canonical",
	})

	if res.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", res.Code, res.Body.String())
	}

	var body struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body %q: %v", res.Body.String(), err)
	}
	if body.Code != "validation_error" {
		t.Errorf("error code = %q, want validation_error", body.Code)
	}
	// The message must describe the transition that was refused. An empty or
	// unrelated message is the symptom of reporting the wrong error value.
	if !strings.Contains(body.Message, "draft") || !strings.Contains(body.Message, "canonical") {
		t.Errorf("message %q does not name the refused transition", body.Message)
	}

	// And the record did not move.
	head, err := srv.Store.Head(context.Background(), "app/test/status", "doc")
	if err != nil {
		t.Fatalf("head: %v", err)
	}
	if head.Status != "draft" {
		t.Errorf("status = %q after a refused promotion, want draft", head.Status)
	}
}
