package mcpadapter

import (
	"context"
	"strings"
	"testing"
)

// The MCP arm of the rejected-transition contract. See the header on
// internal/contextapi/status_promote_reject_test.go for why all three surfaces
// need their own: the rule is shared, the reporting is not, and the reporting
// is where the defect was.
func TestStatusSet_InvalidTransitionIsAToolError(t *testing.T) {
	s, a := statusFixture(t)

	// draft -> canonical skips reviewed, so the lifecycle refuses it.
	body := mustCall(t, a.handleStatusSet, map[string]any{
		"namespace": "app/test/status",
		"key":       "doc",
		"status":    "canonical",
	})
	wantErrorCode(t, body, "validation_error")

	// The message has to describe the transition. An empty or unrelated
	// message is the symptom of reporting the wrong error value.
	msg, _ := body["message"].(string)
	if !strings.Contains(msg, "draft") || !strings.Contains(msg, "canonical") {
		t.Errorf("tool error %q does not name the refused transition; full body: %v", msg, body)
	}

	head, err := s.Head(context.Background(), "app/test/status", "doc")
	if err != nil {
		t.Fatalf("head: %v", err)
	}
	if head.Status != "draft" {
		t.Errorf("status = %q after a refused promotion, want draft", head.Status)
	}
}
