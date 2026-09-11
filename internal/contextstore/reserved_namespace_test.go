package contextstore

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// TestAppendRecordRefusesNewEntriesInClaimedNamespaces is the acceptance test
// for CW-20260909-0013 stated as code: a caller who writes to a curated
// namespace through the context store is TOLD why the entry will not be
// recallable, instead of getting a successful write that recall cannot see.
func TestAppendRecordRefusesNewEntriesInClaimedNamespaces(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	for _, tc := range []struct {
		ns       string
		wantTool string
	}{
		{"user/chrispian/memory/decisions", "memory_write"},
		{"user/chrispian/knowledge/portfolio", "knowledge_write"},
		{"user/chrispian/event/session", "event_write"},
	} {
		_, err := s.AppendRecord(ctx, AppendInput{
			Namespace: tc.ns,
			Key:       "some_entry",
			Actor:     "user",
			Payload:   json.RawMessage(`{"body":"x"}`),
		})
		if err == nil {
			t.Errorf("AppendRecord(%q) succeeded; want refusal", tc.ns)
			continue
		}
		if !errors.Is(err, ErrReservedNamespace) {
			t.Errorf("AppendRecord(%q) error = %v, want ErrReservedNamespace", tc.ns, err)
		}
		// The refusal has to name the write surface that WOULD have worked.
		// An error that only says "no" leaves the caller exactly as stuck as
		// the silent success did.
		if !strings.Contains(err.Error(), tc.wantTool) {
			t.Errorf("AppendRecord(%q) error = %q, want it to name %s", tc.ns, err, tc.wantTool)
		}
		if !strings.Contains(err.Error(), "tesseract_recall") {
			t.Errorf("AppendRecord(%q) error = %q, want it to explain the recall consequence", tc.ns, err)
		}
	}
}

// TestRefusedWriteLeavesNothingBehind checks the guard's placement, not just
// its verdict. It runs before ensureNamespaceRegistered, so a rejected write
// must not seed the policy registry with a namespace no record will occupy.
func TestRefusedWriteLeavesNothingBehind(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	const ns = "user/chrispian/memory/decisions"

	if _, err := s.AppendRecord(ctx, AppendInput{
		Namespace: ns, Key: "k", Actor: "user", Payload: json.RawMessage(`{"a":1}`),
	}); !errors.Is(err, ErrReservedNamespace) {
		t.Fatalf("AppendRecord error = %v, want ErrReservedNamespace", err)
	}

	var n int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM namespace_policies WHERE namespace = ?`, ns).Scan(&n); err != nil {
		t.Fatalf("query registry: %v", err)
	}
	if n != 0 {
		t.Errorf("refused write registered namespace %q (%d rows); want none", ns, n)
	}
}

// TestExistingClaimedEntriesStayWritable is the constraint the prompt set:
// whatever this task decides must not strand the nineteen rows already sitting
// in claimed namespaces. Their re-filing (CW-20260911-0005) has to deprecate
// each record it copies, and a status change appends a revision through this
// same path — so freezing them would have made the cleanup impossible.
func TestExistingClaimedEntriesStayWritable(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	const ns = "user/chrispian/memory/decisions"
	const key = "tether_composition_source_adapter"

	// Stand in for a row that predates the guard. Restore does exactly this —
	// a direct INSERT, never AppendRecord — which is why a backup carrying
	// these rows still replays.
	seedLegacyRecord(t, s, ns, key)

	rec, err := s.AppendRecord(ctx, AppendInput{
		Namespace: ns, Key: key, Actor: "user",
		Payload: json.RawMessage(`{"body":"revision two"}`),
		Status:  "deprecated",
	})
	if err != nil {
		t.Fatalf("AppendRecord on an existing claimed entry = %v, want success", err)
	}
	if rec.Revision != 2 {
		t.Errorf("revision = %d, want 2", rec.Revision)
	}

	// A DIFFERENT key in the same claimed namespace is still a new entry, and
	// still refused. The exemption is per-entry, not per-namespace.
	if _, err := s.AppendRecord(ctx, AppendInput{
		Namespace: ns, Key: "a_brand_new_decision", Actor: "user",
		Payload: json.RawMessage(`{"body":"x"}`),
	}); !errors.Is(err, ErrReservedNamespace) {
		t.Errorf("new key in a claimed namespace = %v, want ErrReservedNamespace", err)
	}
}

// TestAppendRecordStillServesContextNamespaces guards against the guard: these
// are namespaces that hold live records today, and every one must keep working.
func TestAppendRecordStillServesContextNamespaces(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	for _, ns := range []string{
		"app/editor/session",
		"system/sessions/PRJ-20260419-0001",
		"app/nanite/diagnostics",
		"user/memory",
	} {
		if _, err := s.AppendRecord(ctx, AppendInput{
			Namespace: ns, Key: "summary", Actor: "app:editor",
			Payload: json.RawMessage(`{"body":"x"}`),
		}); err != nil {
			t.Errorf("AppendRecord(%q) = %v, want success", ns, err)
		}
	}
}

// seedLegacyRecord inserts a revision-1 row the way restore.go does, bypassing
// AppendRecord and therefore the guard.
func seedLegacyRecord(t *testing.T, s *Store, ns, key string) {
	t.Helper()
	_, err := s.db.ExecContext(context.Background(), `
INSERT INTO records (record_id, namespace, key_name, revision, actor, created_at, checksum, file_path)
VALUES (?, ?, ?, 1, 'user', '2026-09-04T21:41:22Z', '', ?)`,
		"rec_legacy_1", ns, key, ns+"/"+key+"/1.json")
	if err != nil {
		t.Fatalf("seed legacy record: %v", err)
	}
}
