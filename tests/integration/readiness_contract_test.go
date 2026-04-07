package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/vanta-conduit/internal/contextapi"
	"github.com/hollis-labs/vanta-conduit/internal/contextpolicy"
	"github.com/hollis-labs/vanta-conduit/internal/contextstore"
)

type readinessGolden struct {
	RequiredKeys   []string `json:"required_keys"`
	HealthyStatus  string   `json:"healthy_status"`
	DegradedStatus string   `json:"degraded_status"`
	FailingStatus  string   `json:"failing_status"`
}

func TestReadinessContractStatusTransitions(t *testing.T) {
	golden := loadReadinessGolden(t)
	root := t.TempDir()
	s, err := contextstore.Open(context.Background(), contextstore.Config{RootDir: root})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()
	srv := contextapi.NewServer(s, contextpolicy.New())

	rec, err := s.AppendRecord(context.Background(), contextstore.AppendInput{
		Namespace: "app/editor/session",
		Key:       "summary",
		Actor:     "app:editor",
		Payload:   json.RawMessage(`{"v":1}`),
	})
	if err != nil {
		t.Fatalf("append: %v", err)
	}

	healthy := callJSON(t, srv, http.MethodGet, "/v1/health/readiness", nil)
	checkKeys(t, healthy, golden.RequiredKeys)
	if healthy["status"] != golden.HealthyStatus || healthy["healthy"] != true {
		t.Fatalf("expected healthy readiness, got %v", healthy)
	}

	payloadPath := filepath.Join(root, "data", "records", "app", "editor", "session", "summary", "1.json")
	if rec.Revision != 1 {
		payloadPath = filepath.Join(root, "data", "records", "app", "editor", "session", "summary", "1.json")
	}
	if err := os.Remove(payloadPath); err != nil {
		t.Fatalf("remove payload: %v", err)
	}
	degraded := callJSON(t, srv, http.MethodGet, "/v1/health/readiness", nil)
	checkKeys(t, degraded, golden.RequiredKeys)
	if degraded["status"] != golden.DegradedStatus || degraded["healthy"] != false {
		t.Fatalf("expected degraded readiness, got %v", degraded)
	}

	if err := os.RemoveAll(filepath.Join(root, "data", "records")); err != nil {
		t.Fatalf("remove records dir: %v", err)
	}
	failing := callJSON(t, srv, http.MethodGet, "/v1/health/readiness", nil)
	checkKeys(t, failing, golden.RequiredKeys)
	if failing["status"] != golden.FailingStatus || failing["healthy"] != false {
		t.Fatalf("expected failing readiness, got %v", failing)
	}
}

func loadReadinessGolden(t *testing.T) readinessGolden {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("fixtures", "readiness_contract_golden.json"))
	if err != nil {
		t.Fatalf("read readiness golden: %v", err)
	}
	var out readinessGolden
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal readiness golden: %v", err)
	}
	return out
}
