package contextcli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/hollis-labs/tesseract/internal/contextstore"
)

// The CLI arm of the rejected-transition contract. See the header on
// internal/contextapi/status_promote_reject_test.go for why all three surfaces
// need their own: the rule is shared, the reporting is not, and the reporting
// is where the defect was.
func TestStatusPromote_InvalidTransitionFailsWithoutPanicking(t *testing.T) {
	cli, _, errOut := newTestCLI(t)

	if _, err := cli.Store.AppendRecord(context.Background(), contextstore.AppendInput{
		Namespace: "app/test/status", Key: "doc", Actor: "test",
		Payload: json.RawMessage(`{"v":1}`), Status: "draft",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// draft -> canonical skips reviewed, so the lifecycle refuses it.
	code := cli.Run(context.Background(), []string{
		"context", "status-promote",
		"--namespace", "app/test/status",
		"--key", "doc",
		"--to", "canonical",
	})
	if code == 0 {
		t.Fatal("a refused transition exited 0")
	}

	// The diagnostic has to describe the transition. An empty stderr is the
	// symptom of reporting the wrong error value.
	msg := errOut.String()
	if !strings.Contains(msg, "draft") || !strings.Contains(msg, "canonical") {
		t.Errorf("stderr %q does not name the refused transition", msg)
	}

	head, err := cli.Store.Head(context.Background(), "app/test/status", "doc")
	if err != nil {
		t.Fatalf("head: %v", err)
	}
	if head.Status != "draft" {
		t.Errorf("status = %q after a refused promotion, want draft", head.Status)
	}
}
