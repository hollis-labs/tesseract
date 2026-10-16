package contextstore

import (
	"context"
	"testing"
)

// A mapper failure while writing touch events must not reject the touch: the
// savepoint rolls the events back, the transaction (and the work in it) commits.
func TestRecordTouchesMapperFailureNeverRejectsTheTouch(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	outboxRecordHook = func(r *OutboxRecord) { r.SubjectID = "" }
	defer func() { outboxRecordHook = nil }()
	before := OutboxSkipped()

	tx, err := s.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO store_identity (key, value) VALUES ('touch_probe', 'kept')`); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordTouches(ctx, tx, "cor_fail", []string{"i1", "i2"}, []string{"ws-a", ""}); err != nil {
		t.Fatalf("RecordTouches returned %v; a mapper failure must be swallowed", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	var v string
	if err := s.DB().QueryRowContext(ctx, `SELECT value FROM store_identity WHERE key = 'touch_probe'`).Scan(&v); err != nil || v != "kept" {
		t.Fatalf("the mutation's own write did not commit: %v %q", err, v)
	}
	entries, err := s.ReadOutbox(ctx, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("failed touch events left %d outbox rows", len(entries))
	}
	if got := OutboxSkipped() - before; got != 2 {
		t.Fatalf("OutboxSkipped grew by %d, want 2", got)
	}
}
