package contextstore

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/hollis-labs/libs/harness/core/adapters/runtimeevents"
	"github.com/hollis-labs/libs/security/auth"
)

func readEnvelope(t *testing.T, e OutboxEntry) runtimeevents.Event {
	t.Helper()
	var ev runtimeevents.Event
	if err := json.Unmarshal(e.Envelope, &ev); err != nil {
		t.Fatalf("envelope json: %v", err)
	}
	return ev
}

func TestEmitWritesAuditRecordedEnvelope(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if err := s.EmitWrite(ctx, "cli", "app/x/session", "k", 3, "rec-1", nil); err != nil {
		t.Fatalf("emit: %v", err)
	}
	got, err := s.ReadOutbox(ctx, 0, 10)
	if err != nil || len(got) != 1 {
		t.Fatalf("ReadOutbox = %v, %v", got, err)
	}
	ev := readEnvelope(t, got[0])
	if err := ev.Validate(); err != nil {
		t.Fatalf("envelope invalid: %v", err)
	}
	if string(ev.Kind) != "audit.recorded" || ev.Subject != "msg://context/tesseract/app/x/session/k" {
		t.Fatalf("kind/subject = %s %s", ev.Kind, ev.Subject)
	}
	if !strings.HasPrefix(ev.ID, "evt_") || len(ev.ID) != 36 || ev.ID != got[0].EventID {
		t.Fatalf("id = %q (entry %q)", ev.ID, got[0].EventID)
	}
	if ev.Sequence != uint64(got[0].Seq) || ev.Sequence == 0 {
		t.Fatalf("sequence = %d, seq = %d", ev.Sequence, got[0].Seq)
	}
	b, _ := s.OutboxBounds(ctx)
	if ev.Generation != b.Generation || ev.Generation == 0 {
		t.Fatalf("generation = %d, bounds = %d", ev.Generation, b.Generation)
	}
	if ev.Cursor != "" || ev.RecordedAt != nil {
		t.Fatalf("producer must not set cursor/recorded_at: %q %v", ev.Cursor, ev.RecordedAt)
	}
	var p map[string]any
	_ = json.Unmarshal(ev.Payload, &p)
	if p["action"] != "write" || p["outcome"] != "ok" || p["ref"] != "rec-1" {
		t.Fatalf("payload = %v", p)
	}
	if ev.Actor == nil || ev.Actor.Kind != "user" || ev.Actor.Role != "operator" || ev.Actor.Verified != "claimed" {
		t.Fatalf("cli actor = %+v", ev.Actor)
	}
}

func TestOutboxActorFromVerifiedPrincipalNotRequestActor(t *testing.T) {
	s := newTestStore(t)
	ctx := auth.WithPrincipal(context.Background(), auth.Principal{Verified: true, ID: "msg://agent/agent-mux/agt_x", Kind: "agent"})
	if err := s.EmitWrite(ctx, "someone-else", "app/x/session", "k", 1, "r", nil); err != nil {
		t.Fatalf("emit: %v", err)
	}
	got, _ := s.ReadOutbox(context.Background(), 0, 10)
	ev := readEnvelope(t, got[0])
	if ev.Actor.URN != "msg://agent/agent-mux/agt_x" || ev.Actor.Kind != "agent" || ev.Actor.Verified != "token" {
		t.Fatalf("actor = %+v", ev.Actor)
	}
	var p map[string]any
	_ = json.Unmarshal(ev.Payload, &p)
	if p["claimed_actor"] != "someone-else" {
		t.Fatalf("request actor must survive only as claimed_actor: %v", p)
	}
}

func TestOutboxUnverifiedActorIsSystemNone(t *testing.T) {
	s := newTestStore(t)
	if err := s.EmitWrite(context.Background(), "anonymous", "app/x/session", "k", 1, "r", nil); err != nil {
		t.Fatalf("emit: %v", err)
	}
	got, _ := s.ReadOutbox(context.Background(), 0, 10)
	ev := readEnvelope(t, got[0])
	if ev.Actor.Kind != "service" || ev.Actor.Role != "system" || ev.Actor.Verified != "none" {
		t.Fatalf("actor = %+v", ev.Actor)
	}
}

func TestOutboxSubjectMapping(t *testing.T) {
	cases := []struct {
		name string
		ev   AuditEvent
		want string
	}{
		{"namespace", AuditEvent{EventType: EventNamespaceRegister, Namespace: "project/x", Key: "project/x"}, "msg://namespace/tesseract/project/x"},
		{"memory", AuditEvent{EventType: "memory.write", Namespace: "project/x", Key: "note"}, "msg://memory/tesseract/project/x/note"},
		{"context", AuditEvent{EventType: EventPacket, Namespace: "project/x", Key: "req-1"}, "msg://context/tesseract/project/x/req-1"},
		{"empty namespace", AuditEvent{EventType: EventPacket, Key: "req-1"}, "msg://context/tesseract/req-1"},
	}
	for _, c := range cases {
		typ, id := auditSubject(c.ev)
		if got := "msg://" + typ + "/tesseract/" + id; got != c.want {
			t.Errorf("%s: subject = %s, want %s", c.name, got, c.want)
		}
	}
}

// A failure in the outbox half must leave no audit row, and the emit must say so.
func TestEmitIsAtomicWithAuditRow(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, err := s.db.ExecContext(ctx, `DELETE FROM store_identity`); err != nil {
		t.Fatal(err)
	}
	err := s.EmitWrite(ctx, "cli", "app/x/session", "k", 1, "r", nil)
	if err == nil {
		t.Fatal("emit must fail loudly when the outbox cannot be written")
	}
	evs, _ := s.ListAuditEvents(ctx, 10)
	if len(evs) != 0 {
		t.Fatalf("half-state: %d audit rows without an outbox row", len(evs))
	}
	if out, _ := s.ReadOutbox(ctx, 0, 10); len(out) != 0 {
		t.Fatalf("half-state: %d outbox rows", len(out))
	}
}

func TestReadOutboxAscendingCursor(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	for i := 1; i <= 5; i++ {
		if err := s.EmitWrite(ctx, "cli", "app/x/session", "k", int64(i), "r", nil); err != nil {
			t.Fatal(err)
		}
	}
	first, _ := s.ReadOutbox(ctx, 0, 2)
	if len(first) != 2 || first[0].Seq >= first[1].Seq {
		t.Fatalf("first page = %+v", first)
	}
	rest, _ := s.ReadOutbox(ctx, first[1].Seq, 10)
	if len(rest) != 3 || rest[0].Seq != first[1].Seq+1 {
		t.Fatalf("resume must continue strictly after the cursor: %+v", rest)
	}
	// Redelivery is identical: same id, same bytes.
	again, _ := s.ReadOutbox(ctx, 0, 2)
	if again[0].EventID != first[0].EventID || string(again[0].Envelope) != string(first[0].Envelope) {
		t.Fatal("a re-read must return the same event id and envelope")
	}
	b, _ := s.OutboxBounds(ctx)
	if b.Earliest != first[0].Seq || b.HighWater != rest[len(rest)-1].Seq {
		t.Fatalf("bounds = %+v", b)
	}
	if _, err := s.ReadOutbox(ctx, -1, 1); err == nil {
		t.Fatal("negative cursor must be rejected")
	}
}

func TestGenerationStableAcrossReopenAndSetOnMigration(t *testing.T) {
	s := newTestStore(t)
	b1, err := s.OutboxBounds(context.Background())
	if err != nil || b1.Generation == 0 {
		t.Fatalf("bounds = %+v, %v", b1, err)
	}
	// Replaying migration 30 (tests roll schema_version back) keeps it.
	if _, err := s.db.Exec(`DELETE FROM schema_version WHERE version = 30`); err != nil {
		t.Fatal(err)
	}
	if err := s.migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	b2, _ := s.OutboxBounds(context.Background())
	if b1.Generation != b2.Generation {
		t.Fatalf("generation changed on replay: %d -> %d", b1.Generation, b2.Generation)
	}
}

// Step 2 builds on AppendOutbox: several events in one caller tx, one shared
// correlation id, one workstream each, all gone if the caller rolls back.
func TestAppendOutboxSharesTxAndCorrelation(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	ids, err := s.AppendOutbox(ctx, tx,
		OutboxRecord{Action: "touch", SubjectType: "memory", SubjectID: "item-1", WorkstreamID: "ws-a", CorrelationID: "corr-1", Actor: "cli"},
		OutboxRecord{Action: "touch", SubjectType: "memory", SubjectID: "item-2", WorkstreamID: "ws-b", CorrelationID: "corr-1", Actor: "cli"},
	)
	if err != nil || len(ids) != 2 || ids[0] == ids[1] {
		t.Fatalf("ids = %v, %v", ids, err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if out, _ := s.ReadOutbox(ctx, 0, 10); len(out) != 0 {
		t.Fatalf("rolled-back tx left %d outbox rows", len(out))
	}

	tx, _ = s.db.BeginTx(ctx, nil)
	if _, err := s.AppendOutbox(ctx, tx,
		OutboxRecord{Action: "touch", SubjectType: "memory", SubjectID: "item-1", WorkstreamID: "ws-a", CorrelationID: "corr-1"},
		OutboxRecord{Action: "touch", SubjectType: "memory", SubjectID: "item-2", WorkstreamID: "ws-b", CorrelationID: "corr-1"},
	); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	out, _ := s.ReadOutbox(ctx, 0, 10)
	if len(out) != 2 {
		t.Fatalf("committed rows = %d", len(out))
	}
	for i, ws := range []string{"ws-a", "ws-b"} {
		ev := readEnvelope(t, out[i])
		if err := ev.Validate(); err != nil {
			t.Fatal(err)
		}
		if ev.Scope == nil || ev.Scope.WorkstreamID != ws || ev.Corr == nil || ev.Corr.CorrelationID != "corr-1" || out[i].WorkstreamID != ws {
			t.Fatalf("event %d scope/corr = %+v %+v (row ws %q)", i, ev.Scope, ev.Corr, out[i].WorkstreamID)
		}
	}
	if _, err := s.AppendOutbox(ctx, nil, OutboxRecord{}); err == nil {
		t.Fatal("nil tx must be refused")
	}
}

// A mapping failure must not lose the audit row: the row commits, the outbox
// is skipped, the counter moves. (Database errors still roll back both; see
// TestEmitIsAtomicWithAuditRow.)
func TestMappingFailureKeepsAuditRowAndCounts(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	outboxMapFault = func() error { return errors.New("injected mapping failure") }
	t.Cleanup(func() { outboxMapFault = nil })
	before := OutboxSkipped()

	if err := s.EmitWrite(ctx, "cli", "app/x/session", "k", 1, "r", nil); err != nil {
		t.Fatalf("a mapping failure must not fail the emit: %v", err)
	}
	evs, _ := s.ListAuditEvents(ctx, 10)
	if len(evs) != 1 {
		t.Fatalf("audit rows = %d, want 1 (the audit row must survive)", len(evs))
	}
	if out, _ := s.ReadOutbox(ctx, 0, 10); len(out) != 0 {
		t.Fatalf("outbox rows = %d, want 0", len(out))
	}
	if got := OutboxSkipped() - before; got != 1 {
		t.Fatalf("skipped counter moved by %d, want 1", got)
	}
	// AppendOutbox (caller-owned tx) still reports the mapping error.
	tx, _ := s.db.BeginTx(ctx, nil)
	defer func() { _ = tx.Rollback() }()
	if _, err := s.AppendOutbox(ctx, tx, OutboxRecord{Action: "touch", SubjectType: "memory", SubjectID: "i"}); err == nil {
		t.Fatal("AppendOutbox must return the mapping error to its caller")
	}
}
