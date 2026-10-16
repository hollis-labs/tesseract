package contextstore

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func auditsOfType(s *Store, typ string) []AuditEvent {
	evs, _ := s.ListAuditEvents(context.Background(), 100)
	var out []AuditEvent
	for _, e := range evs {
		if e.EventType == typ {
			out = append(out, e)
		}
	}
	return out
}

func TestNamespaceRegistrationAndItsEventCommitTogether(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	inserted, err := s.ensureNamespaceRegistered(ctx, "app/x/session", "inferred")
	if err != nil || !inserted {
		t.Fatalf("inserted = %v, err = %v", inserted, err)
	}
	if evs := auditsOfType(s, EventNamespaceRegister); len(evs) != 1 || evs[0].Actor != "system" {
		t.Fatalf("register events = %+v", evs)
	}
	out, _ := s.ReadOutbox(ctx, 0, 10)
	if len(out) != 1 || out[0].Subject != "msg://namespace/tesseract/app/x/session" {
		t.Fatalf("outbox = %+v", out)
	}
	if ev := readEnvelope(t, out[0]); ev.Actor == nil || ev.Actor.Role != "system" || ev.Actor.Verified != "none" {
		t.Fatalf("internal registration actor = %+v", ev.Actor)
	}
	// Idempotent: a second call writes neither row nor event.
	inserted, err = s.ensureNamespaceRegistered(ctx, "app/x/session", "inferred")
	if err != nil || inserted {
		t.Fatalf("second call inserted = %v, err = %v", inserted, err)
	}
	if evs := auditsOfType(s, EventNamespaceRegister); len(evs) != 1 {
		t.Fatalf("register events after a no-op = %d", len(evs))
	}
}

func TestUpsertNamespacePolicyAuditedRegistersThenUpdates(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	entry := NamespacePolicyEntry{Namespace: "app/y/session", OwnerType: "app", OwnerID: "y"}
	if err := s.UpsertNamespacePolicyAudited(ctx, entry, false, json.RawMessage(`{"exists_before":false}`)); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertNamespacePolicyAudited(ctx, entry, true, json.RawMessage(`{"exists_before":true}`)); err != nil {
		t.Fatal(err)
	}
	if len(auditsOfType(s, EventNamespaceRegister)) != 1 || len(auditsOfType(s, EventNamespaceUpdate)) != 1 {
		t.Fatalf("events: register %d update %d", len(auditsOfType(s, EventNamespaceRegister)), len(auditsOfType(s, EventNamespaceUpdate)))
	}
	// An invalid entry is refused before any event is written.
	if err := s.UpsertNamespacePolicyAudited(ctx, NamespacePolicyEntry{Namespace: "app/z"}, false, nil); err == nil {
		t.Fatal("entry without owner must be refused")
	}
	if len(auditsOfType(s, EventNamespaceRegister)) != 1 {
		t.Fatal("a refused upsert left an event behind")
	}
}

func seedOldRecords(t *testing.T, s *Store, ns string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if _, err := s.AppendRecord(context.Background(), AppendInput{
			Namespace: ns, Key: "k", Actor: "tester", Payload: json.RawMessage(`{"v":1}`),
		}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestTrimRecordsAuditsInItsTxOnlyWhenRequested(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedOldRecords(t, s, "user/cache/a", 3)
	future := "2999-01-01T00:00:00Z"

	// No spec: no maintenance event (callers that don't ask get none).
	n, err := s.TrimRecords(ctx, "user/cache/zzz%", future, false)
	if err != nil || n != 0 || len(auditsOfType(s, EventMaintenanceTrim)) != 0 {
		t.Fatalf("n=%d err=%v events=%d", n, err, len(auditsOfType(s, EventMaintenanceTrim)))
	}

	// Dry run with a spec: a standalone event that says dry_run.
	spec := MaintenanceAudit{EventType: EventMaintenanceTrim, Actor: "system", Pattern: "user/cache/%"}
	spec.DryRun = true
	n, err = s.TrimRecords(WithMaintenanceAudit(ctx, spec), "user/cache/%", future, true)
	if err != nil || n != 3 {
		t.Fatalf("dry run n=%d err=%v", n, err)
	}
	evs := auditsOfType(s, EventMaintenanceTrim)
	if len(evs) != 1 || !strings.Contains(string(evs[0].Metadata), `"dry_run":true`) || !strings.Contains(string(evs[0].Metadata), `"records_affected":3`) {
		t.Fatalf("dry-run events = %+v", evs)
	}

	// Real run: the event is written in the deleting tx with the real count.
	spec.DryRun = false
	n, err = s.TrimRecords(WithMaintenanceAudit(ctx, spec), "user/cache/%", future, false)
	if err != nil || n != 3 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	evs = auditsOfType(s, EventMaintenanceTrim)
	if len(evs) != 2 || !strings.Contains(string(evs[0].Metadata), `"dry_run":false`) || !strings.Contains(string(evs[0].Metadata), `"records_affected":3`) {
		t.Fatalf("events = %+v", evs)
	}
	var left int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM records WHERE namespace = 'user/cache/a'`).Scan(&left)
	if left != 0 {
		t.Fatalf("records left = %d", left)
	}
}

func TestMaintenanceOnlyWhenAffectedSkipsEmptyAndDryRuns(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedOldRecords(t, s, "user/cache/b", 2)
	spec := MaintenanceAudit{EventType: EventMaintenanceTrim, Actor: "cli", Pattern: "user/cache/%", OnlyWhenAffected: true}
	spec.DryRun = true
	if _, err := s.TrimRecords(WithMaintenanceAudit(ctx, spec), "user/cache/%", "2999-01-01T00:00:00Z", true); err != nil {
		t.Fatal(err)
	}
	if n := len(auditsOfType(s, EventMaintenanceTrim)); n != 0 {
		t.Fatalf("CLI dry run recorded %d events, want none", n)
	}
	spec.DryRun = false
	if _, err := s.TrimRecords(WithMaintenanceAudit(ctx, spec), "user/cache/%", "2999-01-01T00:00:00Z", false); err != nil {
		t.Fatal(err)
	}
	if n := len(auditsOfType(s, EventMaintenanceTrim)); n != 1 {
		t.Fatalf("CLI real run recorded %d events, want 1", n)
	}
}

func TestCompactNamespaceAuditsInItsTx(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedOldRecords(t, s, "user/cache/c", 4)
	spec := MaintenanceAudit{EventType: EventMaintenanceCompact, Actor: "system", Pattern: "user/cache/%"}
	n, err := s.CompactNamespace(WithMaintenanceAudit(ctx, spec), "user/cache/%", 1, false)
	if err != nil || n != 3 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	evs := auditsOfType(s, EventMaintenanceCompact)
	if len(evs) != 1 || !strings.Contains(string(evs[0].Metadata), `"records_affected":3`) {
		t.Fatalf("events = %+v", evs)
	}
}

// An audit failure never fails the maintenance operation.
func TestMaintenanceAuditFailureDoesNotFailTheDelete(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedOldRecords(t, s, "user/cache/d", 2)
	before := AuditDropped()
	spec := MaintenanceAudit{EventType: EventMaintenanceTrim, Actor: "", Pattern: "user/cache/%"} // empty actor: invalid audit row
	n, err := s.TrimRecords(WithMaintenanceAudit(ctx, spec), "user/cache/%", "2999-01-01T00:00:00Z", false)
	if err != nil || n != 2 {
		t.Fatalf("n=%d err=%v: the delete must commit", n, err)
	}
	if AuditDropped()-before != 1 {
		t.Fatalf("AuditDropped moved by %d", AuditDropped()-before)
	}
}

// Namespace register/update are security-class (architect ruling): if their
// audit cannot be recorded the registry change is rejected, leaving nothing.
func TestNamespaceChangeFailsClosedWhenItsAuditCannotBeRecorded(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, err := s.db.Exec(`CREATE TRIGGER fail_ns_audit BEFORE INSERT ON audit_events
		WHEN NEW.event_type LIKE 'namespace.%' BEGIN SELECT RAISE(ABORT, 'injected'); END`); err != nil {
		t.Fatal(err)
	}
	entry := NamespacePolicyEntry{Namespace: "app/sec/session", OwnerType: "app", OwnerID: "sec"}
	if err := s.UpsertNamespacePolicyAudited(ctx, entry, false, nil); err == nil {
		t.Fatal("namespace registration must fail closed")
	}
	var n int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM namespace_policies WHERE namespace = 'app/sec/session'`).Scan(&n)
	if n != 0 {
		t.Fatalf("registry rows = %d, want 0 (rejected)", n)
	}
	if d := droppedRows(t, s); len(d) != 0 {
		t.Fatalf("audit_dropped = %+v, want none", d)
	}
	if inserted, err := s.ensureNamespaceRegistered(ctx, "app/sec2/session", "inferred"); err == nil || inserted {
		t.Fatalf("inferred registration must fail closed too: inserted=%v err=%v", inserted, err)
	}
}
