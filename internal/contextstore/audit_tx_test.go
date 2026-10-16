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

type droppedRow struct{ EntityType, ItemID, RecordID, Action, Class, Repaired string }

func droppedRows(t *testing.T, s *Store) []droppedRow {
	t.Helper()
	rows, err := s.db.Query(`SELECT entity_type, item_id, record_id, action, class, COALESCE(repaired_at, '') FROM audit_dropped ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var out []droppedRow
	for rows.Next() {
		var d droppedRow
		if err := rows.Scan(&d.EntityType, &d.ItemID, &d.RecordID, &d.Action, &d.Class, &d.Repaired); err != nil {
			t.Fatal(err)
		}
		out = append(out, d)
	}
	return out
}

// Ordinary mutation (architect ruling, precedence mutation > audit > outbox): a
// failing audit insert never rejects the mutation. It commits, one audit_dropped
// row (bounded class, ids only) records the gap, and there is no audit row.
func TestOrdinaryMutationCommitsWhenItsAuditFailsAndRecordsAuditDropped(t *testing.T) {
	cases := []struct {
		name, class string
		setup       func(t *testing.T, s *Store) AuditSpec
	}{
		{"invalid audit row (validate)", dropClassValidate, func(t *testing.T, s *Store) AuditSpec {
			// MetadataFor runs inside the tx, after the up-front check, so it is
			// the way an invalid audit row can still reach the insert.
			return AuditSpec{EventType: EventWrite, MetadataFor: func(string, int64) json.RawMessage { return json.RawMessage(`{not json`) }}
		}},
		{"database error confined to the audit insert (db)", dropClassDB, func(t *testing.T, s *Store) AuditSpec {
			if _, err := s.db.Exec(`CREATE TRIGGER fail_audit BEFORE INSERT ON audit_events
				WHEN NEW.event_type = 'write' BEGIN SELECT RAISE(ABORT, 'injected audit failure'); END`); err != nil {
				t.Fatal(err)
			}
			return AuditSpec{EventType: EventWrite}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := newTestStore(t)
			spec := c.setup(t, s)
			before := AuditDropped()
			rec, err := appendWithAudit(t, s, &spec)
			if err != nil {
				t.Fatalf("an ordinary mutation must commit despite an audit failure: %v", err)
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
			d := droppedRows(t, s)
			if len(d) != 1 || d[0].Action != EventWrite || d[0].Class != c.class || d[0].RecordID != rec.RecordID || d[0].Repaired != "" {
				t.Fatalf("audit_dropped = %+v, want one %s row for %s", d, c.class, rec.RecordID)
			}
			// Entity type and a stable id, never key text.
			if d[0].EntityType != "context" || d[0].ItemID != contextSubjectID("app/x/session", "k") ||
				!strings.HasPrefix(d[0].ItemID, "ctx_") || strings.Contains(d[0].ItemID, "session") {
				t.Fatalf("audit_dropped entity = %s %q", d[0].EntityType, d[0].ItemID)
			}
			if AuditDropped()-before != 1 {
				t.Fatalf("AuditDropped moved by %d", AuditDropped()-before)
			}
		})
	}
}

// Security-class audits fail CLOSED: the mutation is rejected and nothing is
// left behind (no record, no audit, no outbox, no audit_dropped).
func TestSecurityClassAuditFailureRejectsTheMutation(t *testing.T) {
	for _, typ := range []string{"credential.rotate", EventNamespaceUpdate, EventNamespaceRegister} {
		t.Run(typ, func(t *testing.T) {
			s := newTestStore(t)
			// Register the namespace first so only the mutation under test remains.
			if _, err := s.ensureNamespaceRegistered(context.Background(), "app/x/session", "inferred"); err != nil {
				t.Fatal(err)
			}
			baseAudits, baseOutbox := len(mutationAudits(s)), len(mutationOutbox(s))
			_, err := appendWithAudit(t, s, &AuditSpec{EventType: typ, MetadataFor: func(string, int64) json.RawMessage { return json.RawMessage(`{not json`) }})
			if err == nil {
				t.Fatal("a security-class mutation must be rejected when its audit cannot be recorded")
			}
			if _, herr := s.Head(context.Background(), "app/x/session", "k"); herr == nil {
				t.Fatal("rejected mutation left its record behind")
			}
			if n := len(mutationAudits(s)); n != baseAudits {
				t.Fatalf("audit rows = %d, want %d", n, baseAudits)
			}
			if n := len(mutationOutbox(s)); n != baseOutbox {
				t.Fatalf("outbox rows = %d, want %d", n, baseOutbox)
			}
			if d := droppedRows(t, s); len(d) != 0 {
				t.Fatalf("audit_dropped = %+v, want none (a rejected mutation has no gap)", d)
			}
		})
	}
}

func TestSecurityClassListIsExplicit(t *testing.T) {
	for typ, want := range map[string]bool{
		"credential.rotate": true, "credential.revoke": true, EventNamespaceRegister: true, EventNamespaceUpdate: true,
		"token.mint": true, "grant.revoke": true,
		EventWrite: false, EventTypedWrite: false, "memory.write": false, EventMaintenanceTrim: false, EventPromote: false,
	} {
		if got := isSecurityClassAudit(typ); got != want {
			t.Errorf("isSecurityClassAudit(%q) = %v, want %v", typ, got, want)
		}
	}
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

// If the repair record cannot be written either, the ordinary mutation still
// commits and the gap is logged and counted (architect).
func TestAuditDroppedInsertFailureStillCommitsTheMutation(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.db.Exec(`DROP TABLE audit_dropped`); err != nil {
		t.Fatal(err)
	}
	before := AuditDropped()
	if _, err := appendWithAudit(t, s, &AuditSpec{EventType: EventWrite, MetadataFor: func(string, int64) json.RawMessage { return json.RawMessage(`{not json`) }}); err != nil {
		t.Fatalf("mutation must commit even when audit_dropped cannot be written: %v", err)
	}
	if _, err := s.Head(context.Background(), "app/x/session", "k"); err != nil {
		t.Fatalf("record missing: %v", err)
	}
	if AuditDropped()-before != 1 {
		t.Fatalf("AuditDropped moved by %d", AuditDropped()-before)
	}
}

// Caller-supplied metadata that is not JSON is refused BEFORE the transaction
// opens: the caller gets an error (a 400 at the surfaces), nothing is written
// and nothing is silently dropped.
func TestBadAuditMetadataIsRefusedUpFront(t *testing.T) {
	s := newTestStore(t)
	before := AuditDropped()
	_, err := appendWithAudit(t, s, &AuditSpec{EventType: EventWrite, Metadata: json.RawMessage(`{not json`)})
	if err == nil {
		t.Fatal("non-JSON audit metadata must be refused")
	}
	if _, herr := s.Head(context.Background(), "app/x/session", "k"); herr == nil {
		t.Fatal("refused call left a record behind")
	}
	if d := droppedRows(t, s); len(d) != 0 || AuditDropped() != before {
		t.Fatalf("audit_dropped = %+v, dropped moved %d: a refused call is not a silent drop", d, AuditDropped()-before)
	}
}
