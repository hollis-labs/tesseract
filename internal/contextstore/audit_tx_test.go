package contextstore

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// A new namespace also records its own namespace.register event (a separate,
// pre-existing path); these tests are about the mutation's event, so they look
// at everything else.
func mutationAudits(s *Store) []AuditEvent {
	evs, _ := s.ListAuditEvents(context.Background(), 100)
	var out []AuditEvent
	for _, e := range evs {
		if e.EventType != EventNamespaceRegister {
			out = append(out, e)
		}
	}
	return out
}

func mutationOutbox(s *Store) []OutboxEntry {
	all, _ := s.ReadOutbox(context.Background(), 0, 100)
	var out []OutboxEntry
	for _, e := range all {
		if !strings.HasPrefix(e.Subject, "msg://namespace/") {
			out = append(out, e)
		}
	}
	return out
}

func appendWithAudit(t *testing.T, s *Store, spec *AuditSpec) (Record, error) {
	t.Helper()
	return s.AppendRecord(context.Background(), AppendInput{
		Namespace: "app/x/session", Key: "k", Actor: "tester",
		Payload: json.RawMessage(`{"v":1}`), Audit: spec,
	})
}

func TestAppendRecordWithAuditWritesRecordAuditAndOutboxTogether(t *testing.T) {
	s := newTestStore(t)
	rec, err := appendWithAudit(t, s, &AuditSpec{EventType: EventWrite, Metadata: json.RawMessage(`{"source":"test"}`)})
	if err != nil {
		t.Fatal(err)
	}
	evs := mutationAudits(s)
	out := mutationOutbox(s)
	if len(evs) != 1 || len(out) != 1 {
		t.Fatalf("audit rows %d, outbox rows %d, want 1/1", len(evs), len(out))
	}
	if evs[0].RecordID != rec.RecordID || evs[0].Revision != rec.Revision || evs[0].Actor != "tester" || evs[0].EventType != EventWrite {
		t.Fatalf("audit row = %+v for record %+v", evs[0], rec)
	}
	if out[0].AuditEventID != evs[0].ID {
		t.Fatalf("outbox row links audit %d, want %d", out[0].AuditEventID, evs[0].ID)
	}
}

func TestAppendRecordWithoutAuditSpecWritesNoAudit(t *testing.T) {
	s := newTestStore(t)
	if _, err := appendWithAudit(t, s, nil); err != nil {
		t.Fatal(err)
	}
	if evs := mutationAudits(s); len(evs) != 0 {
		t.Fatalf("audit rows = %d", len(evs))
	}
}

// Recording an event never rejects a valid mutation: a failing audit write
// (here an empty actor in the spec AND an empty mutation actor is impossible, so
// the audit row is made invalid by a bad metadata blob) keeps the record.
func TestAuditFailureNeverRejectsTheMutation(t *testing.T) {
	s := newTestStore(t)
	before := AuditDropped()
	rec, err := appendWithAudit(t, s, &AuditSpec{EventType: EventWrite, Metadata: json.RawMessage(`{not json`)})
	if err != nil {
		t.Fatalf("mutation must commit despite an audit failure: %v", err)
	}
	if _, err := s.Head(context.Background(), "app/x/session", "k"); err != nil {
		t.Fatalf("record missing: %v", err)
	}
	if evs := mutationAudits(s); len(evs) != 0 {
		t.Fatalf("audit rows = %d, want 0", len(evs))
	}
	if out := mutationOutbox(s); len(out) != 0 {
		t.Fatalf("outbox rows = %d, want 0 (rolled back with the audit)", len(out))
	}
	if AuditDropped()-before != 1 {
		t.Fatalf("AuditDropped moved by %d", AuditDropped()-before)
	}
	_ = rec
}

// A mapping failure keeps the record AND the audit row (outbox savepoint).
func TestOutboxMappingFailureInsideMutationKeepsRecordAndAudit(t *testing.T) {
	s := newTestStore(t)
	outboxRecordHook = func(r *OutboxRecord) {
		if r.SubjectType != "namespace" {
			r.SubjectID = ""
		}
	}
	t.Cleanup(func() { outboxRecordHook = nil })
	if _, err := appendWithAudit(t, s, &AuditSpec{EventType: EventWrite}); err != nil {
		t.Fatal(err)
	}
	if evs := mutationAudits(s); len(evs) != 1 {
		t.Fatalf("audit rows = %d, want 1", len(evs))
	}
	if out := mutationOutbox(s); len(out) != 0 {
		t.Fatalf("outbox rows = %d", len(out))
	}
	var n int
	var class string
	if err := s.db.QueryRow(`SELECT COUNT(*), COALESCE(MAX(error), '') FROM outbox_skipped`).Scan(&n, &class); err != nil {
		t.Fatal(err)
	}
	if n != 1 || class != classMapper {
		t.Fatalf("outbox_skipped rows = %d class %q, want 1 mapper", n, class)
	}
}

// A refused mutation leaves no audit or outbox row behind.
func TestRefusedMutationLeavesNoAudit(t *testing.T) {
	s := newTestStore(t)
	_, err := s.AppendRecord(context.Background(), AppendInput{
		Namespace: "app/x/session", Key: "k", Actor: "tester", Payload: json.RawMessage(`{bad`),
		Audit: &AuditSpec{EventType: EventWrite},
	})
	if err == nil {
		t.Fatal("invalid payload must be refused")
	}
	if evs := mutationAudits(s); len(evs) != 0 {
		t.Fatalf("audit rows = %d", len(evs))
	}
	if out := mutationOutbox(s); len(out) != 0 {
		t.Fatalf("outbox rows = %d", len(out))
	}
}

func TestAuditSpecRejectsUnknownPromoteStage(t *testing.T) {
	s := newTestStore(t)
	if _, err := appendWithAudit(t, s, &AuditSpec{EventType: "promote.created"}); err == nil {
		t.Fatal("retired promote spelling must be refused, as EmitPromote refuses it")
	}
}

func TestUpdateRecordStatusAuditedRecordsStatusPromote(t *testing.T) {
	s := newTestStore(t)
	if _, err := appendWithAudit(t, s, nil); err != nil {
		t.Fatal(err)
	}
	rec, err := s.UpdateRecordStatusAudited(context.Background(), "app/x/session", "k", "tester", "canonical", &AuditSpec{EventType: EventStatusPromote})
	if err != nil {
		t.Fatal(err)
	}
	evs := mutationAudits(s)
	if len(evs) != 1 || evs[0].EventType != EventStatusPromote || evs[0].Revision != rec.Revision {
		t.Fatalf("audit = %+v for %+v", evs, rec)
	}
}
