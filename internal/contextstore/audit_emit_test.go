package contextstore

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	s, err := Open(context.Background(), Config{
		RootDir:    dir,
		RecordsDir: filepath.Join(dir, "records"),
		DBPath:     filepath.Join(dir, "index", "context.db"),
	})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func lastEventType(t *testing.T, s *Store) string {
	t.Helper()
	evs, err := s.ListAuditEvents(context.Background(), 1)
	if err != nil {
		t.Fatalf("list audit: %v", err)
	}
	if len(evs) == 0 {
		t.Fatalf("no audit events recorded")
	}
	return evs[0].EventType
}

func TestEmitWrite(t *testing.T) {
	s := newTestStore(t)
	if err := s.EmitWrite(context.Background(), "user", "app/x/session", "k", 1, "rec-1", nil); err != nil {
		t.Fatalf("emit: %v", err)
	}
	if got := lastEventType(t, s); got != EventWrite {
		t.Fatalf("event_type: got %q, want %q", got, EventWrite)
	}
}

func TestEmitPromote(t *testing.T) {
	cases := []struct {
		name   string
		evType string
	}{
		{"mcp_request", EventPromoteRequest},
		{"mcp_approve", EventPromoteApprove},
		{"apply", EventPromote},
		{"http_request", EventPromoteRequestCreated},
		{"http_approve", EventPromoteRequestApproved},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestStore(t)
			if err := s.EmitPromote(context.Background(), tc.evType, "user", "user/notes", "k", 1, "rec", nil); err != nil {
				t.Fatalf("emit: %v", err)
			}
			if got := lastEventType(t, s); got != tc.evType {
				t.Fatalf("event_type: got %q, want %q", got, tc.evType)
			}
		})
	}
}

func TestEmitPromoteRejectsUnknownEventType(t *testing.T) {
	s := newTestStore(t)
	err := s.EmitPromote(context.Background(), "bogus", "user", "user/notes", "k", 1, "rec", nil)
	if err == nil {
		t.Fatal("expected error for unknown promote event type")
	}
}

func TestEmitTypedWrite(t *testing.T) {
	s := newTestStore(t)
	if err := s.EmitTypedWrite(context.Background(), "user", "app/x/typed", "k", 1, "rec", nil); err != nil {
		t.Fatalf("emit: %v", err)
	}
	if got := lastEventType(t, s); got != EventTypedWrite {
		t.Fatalf("event_type: got %q, want %q", got, EventTypedWrite)
	}
}

func TestEmitStatusPromote(t *testing.T) {
	s := newTestStore(t)
	if err := s.EmitStatusPromote(context.Background(), "user", "app/x/typed", "k", 1, "rec", nil); err != nil {
		t.Fatalf("emit: %v", err)
	}
	if got := lastEventType(t, s); got != EventStatusPromote {
		t.Fatalf("event_type: got %q, want %q", got, EventStatusPromote)
	}
}

func TestEmitStatusDeprecate(t *testing.T) {
	s := newTestStore(t)
	if err := s.EmitStatusDeprecate(context.Background(), "user", "app/x/typed", "k", 1, "rec", nil); err != nil {
		t.Fatalf("emit: %v", err)
	}
	if got := lastEventType(t, s); got != EventStatusDeprecate {
		t.Fatalf("event_type: got %q, want %q", got, EventStatusDeprecate)
	}
}

func TestEmitBulkIngest(t *testing.T) {
	s := newTestStore(t)
	if err := s.EmitBulkIngest(context.Background(), "user", "app/x/bulk", "k", 1, "rec", json.RawMessage(`{"batch":0}`)); err != nil {
		t.Fatalf("emit: %v", err)
	}
	if got := lastEventType(t, s); got != EventBulkIngest {
		t.Fatalf("event_type: got %q, want %q", got, EventBulkIngest)
	}
}

func TestEmitChunkedIngest(t *testing.T) {
	s := newTestStore(t)
	if err := s.EmitChunkedIngest(context.Background(), "user", "app/x/chunk", "k", 1, "rec", nil); err != nil {
		t.Fatalf("emit: %v", err)
	}
	if got := lastEventType(t, s); got != EventChunkedIngest {
		t.Fatalf("event_type: got %q, want %q", got, EventChunkedIngest)
	}
}

func TestEmitSessionSnapshot(t *testing.T) {
	s := newTestStore(t)
	if err := s.EmitSessionSnapshot(context.Background(), "user", "app/x/sess", "k", 1, "rec", nil); err != nil {
		t.Fatalf("emit: %v", err)
	}
	if got := lastEventType(t, s); got != EventSessionSnapshot {
		t.Fatalf("event_type: got %q, want %q", got, EventSessionSnapshot)
	}
}

func TestEmitPacket(t *testing.T) {
	s := newTestStore(t)
	// Packet uses the request_id as Key; revision/recordID are not meaningful.
	// The helper must internally satisfy the store's key/revision validation.
	if err := s.EmitPacket(context.Background(), "user", "app/x/foo,app/y/bar", "req-abc123", json.RawMessage(`{"items_returned":3}`)); err != nil {
		t.Fatalf("emit: %v", err)
	}
	if got := lastEventType(t, s); got != EventPacket {
		t.Fatalf("event_type: got %q, want %q", got, EventPacket)
	}
}

func TestEmitMaintenance(t *testing.T) {
	for _, evType := range []string{EventMaintenanceTrim, EventMaintenanceCompact} {
		t.Run(evType, func(t *testing.T) {
			s := newTestStore(t)
			if err := s.EmitMaintenance(context.Background(), evType, "cli", "app/x/session", nil); err != nil {
				t.Fatalf("emit: %v", err)
			}
			if got := lastEventType(t, s); got != evType {
				t.Fatalf("event_type: got %q, want %q", got, evType)
			}
		})
	}
}

func TestEmitMaintenanceRejectsUnknownOp(t *testing.T) {
	s := newTestStore(t)
	err := s.EmitMaintenance(context.Background(), "bogus", "cli", "app/x/session", nil)
	if err == nil {
		t.Fatal("expected error for unknown maintenance op")
	}
}

func TestEmitFillsCreatedAtWhenEmpty(t *testing.T) {
	s := newTestStore(t)
	if err := s.EmitWrite(context.Background(), "user", "app/x/session", "k", 1, "rec", nil); err != nil {
		t.Fatalf("emit: %v", err)
	}
	evs, err := s.ListAuditEvents(context.Background(), 1)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if evs[0].CreatedAt == "" {
		t.Fatal("CreatedAt was not populated by helper")
	}
}
