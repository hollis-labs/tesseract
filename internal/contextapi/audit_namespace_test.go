package contextapi

import (
	"context"
	"net/http"
	"testing"

	"github.com/hollis-labs/tesseract/internal/contextstore"
)

// CW-20261016-0149: a valid packet request whose selector carries no
// namespaces used to commit its work and then lose its audit row to
// "namespace required". It must produce exactly one audit row.
func TestPacketWithEmptyNamespacesKeepsItsAuditRow(t *testing.T) {
	s := newTestServer(t)
	seedBigRecord(t, s, "user/memory/audit-ns", "doc", 10)

	res := performJSON(t, s, "POST", "/v1/context/packet", map[string]any{
		"selector": map[string]any{"revision_scope": "head"},
		"assembly": map[string]any{"include_pins": false},
	})
	if res.Code != http.StatusOK {
		t.Fatalf("packet status = %d: %s", res.Code, res.Body)
	}
	events, _, err := s.Store.QueryAuditEvents(context.Background(), contextstore.AuditQuery{EventType: contextstore.EventPacket, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("packet audit rows = %d, want exactly 1", len(events))
	}
	if events[0].Namespace == "" {
		t.Fatalf("audit row has empty namespace")
	}
}
