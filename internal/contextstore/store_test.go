package contextstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestAppendHeadAndHistory(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(context.Background(), Config{RootDir: dir})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	for i := 1; i <= 3; i++ {
		payload := json.RawMessage([]byte(`{"n":` + string(rune('0'+i)) + `}`))
		if _, err := s.AppendRecord(context.Background(), AppendInput{
			Namespace: "app/editor/session",
			Key:       "summary",
			Actor:     "app:editor",
			Payload:   payload,
		}); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}

	head, err := s.Head(context.Background(), "app/editor/session", "summary")
	if err != nil {
		t.Fatalf("head: %v", err)
	}
	if head.Revision != 3 {
		t.Fatalf("expected head revision 3, got %d", head.Revision)
	}

	history, err := s.History(context.Background(), "app/editor/session", "summary", 0)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(history) != 3 {
		t.Fatalf("expected 3 history rows, got %d", len(history))
	}
	for i := range history {
		want := int64(i + 1)
		if history[i].Revision != want {
			t.Fatalf("history[%d] revision=%d want=%d", i, history[i].Revision, want)
		}
	}
}

func TestHistoryDeterministicOrdering(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(context.Background(), Config{RootDir: dir})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	writes := []string{`{"v":1}`, `{"v":2}`, `{"v":3}`}
	for _, raw := range writes {
		if _, err := s.AppendRecord(context.Background(), AppendInput{
			Namespace: "user/goals",
			Key:       "focus",
			Actor:     "user",
			Payload:   json.RawMessage(raw),
		}); err != nil {
			t.Fatalf("append: %v", err)
		}
	}

	a, err := s.History(context.Background(), "user/goals", "focus", 0)
	if err != nil {
		t.Fatalf("history A: %v", err)
	}
	b, err := s.History(context.Background(), "user/goals", "focus", 0)
	if err != nil {
		t.Fatalf("history B: %v", err)
	}

	if !reflect.DeepEqual(a, b) {
		t.Fatalf("history ordering/content not deterministic")
	}
}

func TestMissingPayloadReturnsConsistencyFault(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(context.Background(), Config{RootDir: dir})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	rec, err := s.AppendRecord(context.Background(), AppendInput{
		Namespace: "app/editor/session",
		Key:       "summary",
		Actor:     "app:editor",
		Payload:   json.RawMessage(`{"ok":true}`),
	})
	if err != nil {
		t.Fatalf("append: %v", err)
	}

	if err := os.Remove(filepath.Join(dir, "data", "records", rec.Namespace, rec.Key, "1.json")); err != nil {
		t.Fatalf("remove payload: %v", err)
	}

	_, err = s.Head(context.Background(), rec.Namespace, rec.Key)
	if err == nil {
		t.Fatalf("expected consistency fault")
	}
	if !errors.Is(err, ErrConsistencyFault) {
		t.Fatalf("expected ErrConsistencyFault, got %v", err)
	}
}

func TestSelectValidationAndLimits(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(context.Background(), Config{RootDir: dir})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	for i := 0; i < DefaultSelectLimit+20; i++ {
		if _, err := s.AppendRecord(context.Background(), AppendInput{
			Namespace: "app/editor/session",
			Key:       fmt.Sprintf("k-%03d", i),
			Actor:     "app:editor",
			Payload:   json.RawMessage(`{"ok":true}`),
		}); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}

	// Limit=0 should apply the default bound.
	rows, err := s.Select(context.Background(), Selector{
		Namespaces:    []string{"app/editor/*"},
		RevisionScope: "all",
	})
	if err != nil {
		t.Fatalf("select default limit: %v", err)
	}
	if len(rows) != DefaultSelectLimit {
		t.Fatalf("expected default limit %d rows, got %d", DefaultSelectLimit, len(rows))
	}

	_, err = s.Select(context.Background(), Selector{
		Namespaces: []string{"app/editor/*"},
		Limit:      MaxSelectLimit + 1,
	})
	if err == nil {
		t.Fatalf("expected limit validation error")
	}

	_, err = s.Select(context.Background(), Selector{
		Namespaces: []string{"["},
	})
	if err == nil {
		t.Fatalf("expected namespace pattern validation error")
	}
}

func BenchmarkSelectRepresentativeWorkload(b *testing.B) {
	dir := b.TempDir()
	s, err := Open(context.Background(), Config{RootDir: dir})
	if err != nil {
		b.Fatalf("open: %v", err)
	}
	defer s.Close()

	for i := 0; i < 600; i++ {
		ns := "app/editor/session"
		if i%2 == 0 {
			ns = "user/goals"
		}
		if _, err := s.AppendRecord(context.Background(), AppendInput{
			Namespace: ns,
			Key:       fmt.Sprintf("k-%03d", i),
			Actor:     "user",
			Payload:   json.RawMessage(`{"v":1}`),
		}); err != nil {
			b.Fatalf("append %d: %v", i, err)
		}
	}

	sel := Selector{
		Namespaces:    []string{"app/editor/*", "user/*"},
		RevisionScope: "all",
		Order:         []string{"namespace", "key", "revision"},
		Limit:         300,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rows, err := s.Select(context.Background(), sel)
		if err != nil {
			b.Fatalf("select: %v", err)
		}
		if len(rows) > sel.Limit {
			b.Fatalf("expected bounded results <= %d, got %d", sel.Limit, len(rows))
		}
	}
}

func TestScanConsistencyAndRepairHeads(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(context.Background(), Config{RootDir: dir})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	if _, err := s.AppendRecord(context.Background(), AppendInput{
		Namespace: "app/editor/session",
		Key:       "summary",
		Actor:     "app:editor",
		Payload:   json.RawMessage(`{"v":1}`),
	}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if _, err := s.AppendRecord(context.Background(), AppendInput{
		Namespace: "app/editor/session",
		Key:       "summary",
		Actor:     "app:editor",
		Payload:   json.RawMessage(`{"v":2}`),
	}); err != nil {
		t.Fatalf("append: %v", err)
	}

	// Force a head mismatch for repair verification.
	if _, err := s.db.Exec(`UPDATE heads SET head_revision = 1`); err != nil {
		t.Fatalf("tamper head: %v", err)
	}

	issues, err := s.ScanConsistency(context.Background())
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	foundHeadMismatch := false
	for _, issue := range issues {
		if issue.Type == "head_mismatch" {
			foundHeadMismatch = true
		}
	}
	if !foundHeadMismatch {
		t.Fatalf("expected head_mismatch issue, got %+v", issues)
	}

	rebuilt, err := s.RebuildHeads(context.Background())
	if err != nil {
		t.Fatalf("rebuild heads: %v", err)
	}
	if rebuilt != 1 {
		t.Fatalf("expected one rebuilt head, got %d", rebuilt)
	}

	issuesAfter, err := s.ScanConsistency(context.Background())
	if err != nil {
		t.Fatalf("scan after repair: %v", err)
	}
	for _, issue := range issuesAfter {
		if issue.Type == "head_mismatch" {
			t.Fatalf("expected head mismatch repaired, still present: %+v", issue)
		}
	}
}

func TestAuthTokenLifecycle(t *testing.T) {
	s, err := Open(context.Background(), Config{RootDir: t.TempDir()})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	token, meta, err := s.IssueAuthToken(context.Background(), "cli-admin", time.Hour)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if meta.TokenID == "" || token == "" {
		t.Fatalf("expected issued token and metadata")
	}
	if err := s.ValidateAuthToken(context.Background(), token); err != nil {
		t.Fatalf("validate issued token: %v", err)
	}

	rotated, _, err := s.RotateAuthToken(context.Background(), token, "rotated", time.Hour)
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if err := s.ValidateAuthToken(context.Background(), token); !errors.Is(err, ErrAuthTokenRevoked) {
		t.Fatalf("expected revoked old token, got %v", err)
	}
	if err := s.ValidateAuthToken(context.Background(), rotated); err != nil {
		t.Fatalf("validate rotated token: %v", err)
	}

	if err := s.RevokeAuthToken(context.Background(), rotated); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if err := s.ValidateAuthToken(context.Background(), rotated); !errors.Is(err, ErrAuthTokenRevoked) {
		t.Fatalf("expected revoked rotated token, got %v", err)
	}
}

func TestReadinessHealthyAndDegraded(t *testing.T) {
	root := t.TempDir()
	s, err := Open(context.Background(), Config{RootDir: root})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	if _, err := s.AppendRecord(context.Background(), AppendInput{
		Namespace: "app/editor/session",
		Key:       "summary",
		Actor:     "app:editor",
		Payload:   json.RawMessage(`{"v":1}`),
	}); err != nil {
		t.Fatalf("append: %v", err)
	}

	healthy, err := s.Readiness(context.Background())
	if err != nil {
		t.Fatalf("readiness healthy: %v", err)
	}
	if !healthy.Healthy || healthy.ConsistencyIssues != 0 || healthy.Status != "healthy" {
		t.Fatalf("expected healthy readiness, got %+v", healthy)
	}

	if err := os.Remove(filepath.Join(root, "data", "records", "app", "editor", "session", "summary", "1.json")); err != nil {
		t.Fatalf("remove payload: %v", err)
	}
	degraded, err := s.Readiness(context.Background())
	if err != nil {
		t.Fatalf("readiness degraded: %v", err)
	}
	if degraded.Healthy || degraded.ConsistencyIssues == 0 || degraded.Status != "degraded" {
		t.Fatalf("expected degraded readiness, got %+v", degraded)
	}

	if err := os.RemoveAll(filepath.Join(root, "data", "records")); err != nil {
		t.Fatalf("remove records dir: %v", err)
	}
	failing, err := s.Readiness(context.Background())
	if err != nil {
		t.Fatalf("readiness failing: %v", err)
	}
	if failing.Status != "failing" || failing.RecordsDirExists {
		t.Fatalf("expected failing readiness, got %+v", failing)
	}
}

func TestNamespacePolicyPersistenceMethods(t *testing.T) {
	s, err := Open(context.Background(), Config{RootDir: t.TempDir()})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	entry := NamespacePolicyEntry{
		Namespace: "app/editor/session",
		OwnerType: "app",
		OwnerID:   "editor",
		Policy: map[string]any{
			"required_keys": []string{"title", "summary"},
		},
	}
	if err := s.UpsertNamespacePolicy(context.Background(), entry); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	got, err := s.GetNamespacePolicy(context.Background(), entry.Namespace)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.OwnerID != entry.OwnerID || got.OwnerType != entry.OwnerType {
		t.Fatalf("unexpected entry: %+v", got)
	}
	list, err := s.ListNamespacePolicies(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 || list[0].Namespace != entry.Namespace {
		t.Fatalf("unexpected list: %+v", list)
	}
}

func TestCompactionPreservesHeadsAndTrimsAudit(t *testing.T) {
	s, err := Open(context.Background(), Config{RootDir: t.TempDir()})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	for i := 1; i <= 5; i++ {
		rec, err := s.AppendRecord(context.Background(), AppendInput{
			Namespace: "app/editor/session",
			Key:       "summary",
			Actor:     "app:editor",
			Payload:   json.RawMessage([]byte(`{"n":` + string(rune('0'+i)) + `}`)),
		})
		if err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
		if err := s.recordAuditEvent(context.Background(), AuditEvent{
			EventType: EventWrite,
			Actor:     rec.Actor,
			Namespace: rec.Namespace,
			Key:       rec.Key,
			Revision:  rec.Revision,
			RecordID:  rec.RecordID,
		}); err != nil {
			t.Fatalf("audit %d: %v", i, err)
		}
	}

	deleted, err := s.CompactRevisions(context.Background(), 2)
	if err != nil {
		t.Fatalf("compact revisions: %v", err)
	}
	if deleted != 3 {
		t.Fatalf("expected 3 deleted revisions, got %d", deleted)
	}
	history, err := s.History(context.Background(), "app/editor/session", "summary", 0)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(history) != 2 || history[0].Revision != 4 || history[1].Revision != 5 {
		t.Fatalf("unexpected history after compaction: %+v", history)
	}
	head, err := s.Head(context.Background(), "app/editor/session", "summary")
	if err != nil {
		t.Fatalf("head after compaction: %v", err)
	}
	if head.Revision != 5 {
		t.Fatalf("expected head revision 5 after compaction, got %d", head.Revision)
	}

	trimmed, err := s.TrimAuditEvents(context.Background(), 2)
	if err != nil {
		t.Fatalf("trim audit: %v", err)
	}
	if trimmed != 3 {
		t.Fatalf("expected 3 trimmed audit rows, got %d", trimmed)
	}
	events, err := s.ListAuditEvents(context.Background(), 10)
	if err != nil {
		t.Fatalf("list audit: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 audit rows after trim, got %d", len(events))
	}
}

func TestQueryAuditEventsFiltersAndCursor(t *testing.T) {
	s, err := Open(context.Background(), Config{RootDir: t.TempDir()})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	seed := []AuditEvent{
		{EventType: EventWrite, Actor: "app:editor", Namespace: "app/editor/session", Key: "summary", Revision: 1, RecordID: "r1"},
		{EventType: EventWrite, Actor: "app:editor", Namespace: "app/editor/session", Key: "summary", Revision: 2, RecordID: "r2"},
		{EventType: EventPromote, Actor: "user", Namespace: "user/notes", Key: "daily", Revision: 1, RecordID: "r3"},
	}
	for i := range seed {
		if err := s.recordAuditEvent(context.Background(), seed[i]); err != nil {
			t.Fatalf("record audit %d: %v", i, err)
		}
	}

	pageOne, next, err := s.QueryAuditEvents(context.Background(), AuditQuery{Limit: 2})
	if err != nil {
		t.Fatalf("query page one: %v", err)
	}
	if len(pageOne) != 2 || next == nil {
		t.Fatalf("expected 2 items and next cursor, got len=%d next=%v", len(pageOne), next)
	}
	pageTwo, nextTwo, err := s.QueryAuditEvents(context.Background(), AuditQuery{Limit: 2, Cursor: *next})
	if err != nil {
		t.Fatalf("query page two: %v", err)
	}
	if len(pageTwo) != 1 || nextTwo != nil {
		t.Fatalf("expected final page len=1 and nil cursor, got len=%d next=%v", len(pageTwo), nextTwo)
	}
	if pageTwo[0].ID >= pageOne[len(pageOne)-1].ID {
		t.Fatalf("cursor did not advance to older window")
	}

	filtered, _, err := s.QueryAuditEvents(context.Background(), AuditQuery{
		Limit:     10,
		Namespace: "user/notes",
		EventType: "promote",
	})
	if err != nil {
		t.Fatalf("query filtered: %v", err)
	}
	if len(filtered) != 1 || filtered[0].EventType != "promote" || filtered[0].Namespace != "user/notes" {
		t.Fatalf("unexpected filtered result: %+v", filtered)
	}
}

func TestTrimRecordsIdempotent(t *testing.T) {
	s, err := Open(context.Background(), Config{RootDir: t.TempDir()})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	// Write 5 cache records.
	for i := 0; i < 5; i++ {
		_, err := s.AppendRecord(context.Background(), AppendInput{
			Namespace: "user/cache/test",
			Key:       fmt.Sprintf("key-%d", i),
			Actor:     "user",
			Payload:   []byte(`{"v":1}`),
		})
		if err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}

	// Trim with a future cutoff — should trim all 5.
	future := time.Now().UTC().Add(1 * time.Hour).Format(time.RFC3339)
	trimmed, err := s.TrimRecords(context.Background(), "user/cache/%", future, false)
	if err != nil {
		t.Fatalf("trim: %v", err)
	}
	if trimmed != 5 {
		t.Fatalf("expected 5 trimmed, got %d", trimmed)
	}

	// Second trim with same cutoff — should trim 0 (idempotent).
	trimmed2, err := s.TrimRecords(context.Background(), "user/cache/%", future, false)
	if err != nil {
		t.Fatalf("trim2: %v", err)
	}
	if trimmed2 != 0 {
		t.Fatalf("expected 0 trimmed on second run, got %d", trimmed2)
	}
}

func TestTrimRecordsDryRun(t *testing.T) {
	s, err := Open(context.Background(), Config{RootDir: t.TempDir()})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	for i := 0; i < 3; i++ {
		_, _ = s.AppendRecord(context.Background(), AppendInput{
			Namespace: "user/cache/dry",
			Key:       fmt.Sprintf("k%d", i),
			Actor:     "user",
			Payload:   []byte(`{"v":1}`),
		})
	}
	future := time.Now().UTC().Add(1 * time.Hour).Format(time.RFC3339)
	count, err := s.TrimRecords(context.Background(), "user/cache/%", future, true)
	if err != nil {
		t.Fatalf("dry-run trim: %v", err)
	}
	if count != 3 {
		t.Fatalf("expected dry-run count 3, got %d", count)
	}
	// Records must still exist after dry-run.
	recs, err := s.Select(context.Background(), Selector{
		Namespaces:    []string{"user/cache/dry"},
		RevisionScope: "head",
		Order:         []string{"namespace", "key", "revision"},
	})
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	if len(recs) != 3 {
		t.Fatalf("dry-run should not delete records; got %d remaining", len(recs))
	}
}

func TestCompactNamespace(t *testing.T) {
	s, err := Open(context.Background(), Config{RootDir: t.TempDir()})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	// Write 4 revisions for one key.
	for i := 0; i < 4; i++ {
		_, _ = s.AppendRecord(context.Background(), AppendInput{
			Namespace: "app/test/draft",
			Key:       "pref",
			Actor:     "app:test",
			Payload:   []byte(fmt.Sprintf(`{"rev":%d}`, i)),
		})
	}

	// Compact to keep 2 revisions.
	compacted, err := s.CompactNamespace(context.Background(), "app/test/%", 2, false)
	if err != nil {
		t.Fatalf("compact: %v", err)
	}
	if compacted != 2 {
		t.Fatalf("expected 2 compacted, got %d", compacted)
	}

	// Verify 2 revisions remain.
	hist, err := s.History(context.Background(), "app/test/draft", "pref", 0)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(hist) != 2 {
		t.Fatalf("expected 2 remaining revisions, got %d", len(hist))
	}
}

func TestChecksumOnWrite(t *testing.T) {
	s, err := Open(context.Background(), Config{RootDir: t.TempDir()})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	rec, err := s.AppendRecord(context.Background(), AppendInput{
		Namespace: "user/memory/test",
		Key:       "checksum-test",
		Actor:     "user",
		Payload:   []byte(`{"hello":"world"}`),
	})
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	if rec.Checksum == "" {
		t.Fatal("expected non-empty checksum on new record")
	}
	// SHA-256 hex digest is always 64 hex characters.
	if len(rec.Checksum) != 64 {
		t.Fatalf("checksum wrong length: got %d want 64", len(rec.Checksum))
	}
}

func TestScanConsistencyChecksumCases(t *testing.T) {
	root := t.TempDir()
	s, err := Open(context.Background(), Config{RootDir: root})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	// Write 3 records.
	recs := make([]Record, 3)
	for i := range recs {
		recs[i], err = s.AppendRecord(context.Background(), AppendInput{
			Namespace: "user/memory/scan-test",
			Key:       fmt.Sprintf("key-%d", i),
			Actor:     "user",
			Payload:   []byte(`{"v":` + string(rune('0'+i)) + `}`),
		})
		if err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}

	// Confirm clean scan — no corrupted issues.
	issues, err := s.ScanConsistency(context.Background())
	if err != nil {
		t.Fatalf("clean scan: %v", err)
	}
	for _, iss := range issues {
		if iss.Type == "corrupted" {
			t.Errorf("unexpected corrupted issue on clean store: %+v", iss)
		}
	}

	// Corrupt one file on disk. Store uses <root>/data/records/<namespace>/<key>/<revision>.json
	corruptPath := filepath.Join(root, "data", "records", recs[1].Namespace, recs[1].Key, fmt.Sprintf("%d.json", recs[1].Revision))
	if err := os.WriteFile(corruptPath, []byte(`{"v":"tampered"}`), 0o644); err != nil {
		t.Fatalf("corrupt file: %v", err)
	}

	issues, err = s.ScanConsistency(context.Background())
	if err != nil {
		t.Fatalf("scan after corrupt: %v", err)
	}

	var foundCorrupted bool
	for _, iss := range issues {
		if iss.Type == "corrupted" && iss.Key == "key-1" {
			foundCorrupted = true
		}
	}
	if !foundCorrupted {
		t.Errorf("expected corrupted issue for key-1, got: %+v", issues)
	}

	// Records key-0 and key-2 should not be corrupted.
	for _, iss := range issues {
		if iss.Type == "corrupted" && (iss.Key == "key-0" || iss.Key == "key-2") {
			t.Errorf("unexpected corrupted for %s: %+v", iss.Key, iss)
		}
	}
}

func TestEstimateTokens(t *testing.T) {
	if got := EstimateTokens([]byte{}); got != 0 {
		t.Errorf("empty payload: want 0, got %d", got)
	}
	// 1000 bytes → ceil(1000/4) = 250
	payload1000 := make([]byte, 1000)
	if got := EstimateTokens(payload1000); got != 250 {
		t.Errorf("1000-byte payload: want 250, got %d", got)
	}
	// 1001 bytes → ceil(1001/4) = 251
	payload1001 := make([]byte, 1001)
	if got := EstimateTokens(payload1001); got != 251 {
		t.Errorf("1001-byte payload: want 251, got %d", got)
	}
}

func TestTagsAnySelector(t *testing.T) {
	root := t.TempDir()
	s, err := Open(context.Background(), Config{RootDir: root})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	// Write 3 records: tags [a,b], [b,c], [c,d]
	tagSets := [][]string{{"a", "b"}, {"b", "c"}, {"c", "d"}}
	for i, tags := range tagSets {
		tagsJSON, _ := json.Marshal(tags)
		meta := json.RawMessage(`{"tags":` + string(tagsJSON) + `}`)
		_, err := s.AppendRecord(context.Background(), AppendInput{
			Namespace: "user/memory/tags-test",
			Key:       fmt.Sprintf("key-%d", i),
			Actor:     "tester",
			Payload:   json.RawMessage(`{"i":` + fmt.Sprintf("%d", i) + `}`),
			Metadata:  meta,
		})
		if err != nil {
			t.Fatalf("append key-%d: %v", i, err)
		}
	}

	checkCount := func(tagsAny []string, want int) {
		t.Helper()
		recs, err := s.Select(context.Background(), Selector{
			Namespaces:    []string{"user/memory/tags-test"},
			RevisionScope: "head",
			TagsAny:       tagsAny,
		})
		if err != nil {
			t.Fatalf("select tags_any=%v: %v", tagsAny, err)
		}
		if got := len(recs); got != want {
			t.Errorf("tags_any=%v: want %d records, got %d", tagsAny, want, got)
		}
	}

	checkCount([]string{"a"}, 1)      // only key-0
	checkCount([]string{"b"}, 2)      // key-0 and key-1
	checkCount([]string{"x"}, 0)      // no match
	checkCount([]string{"a", "c"}, 3) // all three have a or c
}

func TestAppendRecordWithMetadata(t *testing.T) {
	root := t.TempDir()
	s, err := Open(context.Background(), Config{RootDir: root})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	rec, err := s.AppendRecord(context.Background(), AppendInput{
		Namespace: "user/memory/meta-test",
		Key:       "key-meta",
		Actor:     "tester",
		Payload:   json.RawMessage(`{"v":1}`),
		Metadata:  json.RawMessage(`{"tags":["foo","bar"],"source":"test"}`),
	})
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	if rec.RecordID == "" {
		t.Error("expected non-empty RecordID")
	}

	// Tag filter should find the record.
	recs, err := s.Select(context.Background(), Selector{
		Namespaces:    []string{"user/memory/meta-test"},
		RevisionScope: "head",
		TagsAny:       []string{"foo"},
	})
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	if len(recs) != 1 {
		t.Errorf("want 1 record, got %d", len(recs))
	}
}

func TestAppendRecordWithTypeAndStatus(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(context.Background(), Config{RootDir: dir})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	rec, err := s.AppendRecord(context.Background(), AppendInput{
		Namespace:      "user/memory/arch",
		Key:            "system-map",
		Actor:          "user",
		Payload:        json.RawMessage(`{"title":"System Map"}`),
		RecordType:     "system/map",
		Status:         "draft",
		ContentVersion: 1,
		Pointers:       []string{"docs/architecture.md", "https://example.com/api"},
	})
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	if rec.RecordType != "system/map" {
		t.Errorf("expected record_type system/map, got %s", rec.RecordType)
	}
	if rec.Status != "draft" {
		t.Errorf("expected status draft, got %s", rec.Status)
	}
	if rec.ContentVersion != 1 {
		t.Errorf("expected content_version 1, got %d", rec.ContentVersion)
	}
	if len(rec.Pointers) != 2 {
		t.Errorf("expected 2 pointers, got %d", len(rec.Pointers))
	}

	// Verify Head returns the typed fields.
	head, err := s.Head(context.Background(), "user/memory/arch", "system-map")
	if err != nil {
		t.Fatalf("head: %v", err)
	}
	if head.RecordType != "system/map" {
		t.Errorf("head record_type: want system/map, got %s", head.RecordType)
	}
	if head.Status != "draft" {
		t.Errorf("head status: want draft, got %s", head.Status)
	}
	if head.ContentVersion != 1 {
		t.Errorf("head content_version: want 1, got %d", head.ContentVersion)
	}
	if len(head.Pointers) != 2 {
		t.Errorf("head pointers: want 2, got %d", len(head.Pointers))
	}
}

func TestSelectByType(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(context.Background(), Config{RootDir: dir})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	// Write records with different types.
	for _, tc := range []struct {
		key string
		typ string
	}{
		{"task1", "task/spec"},
		{"adr1", "decision/adr"},
		{"map1", "system/map"},
		{"note1", "note/volatile"},
	} {
		if _, err := s.AppendRecord(context.Background(), AppendInput{
			Namespace:  "user/memory/project",
			Key:        tc.key,
			Actor:      "user",
			Payload:    json.RawMessage(`{"key":"` + tc.key + `"}`),
			RecordType: tc.typ,
			Status:     "draft",
		}); err != nil {
			t.Fatalf("append %s: %v", tc.key, err)
		}
	}

	// Select by single type.
	recs, err := s.Select(context.Background(), Selector{
		Namespaces:    []string{"user/memory/project"},
		RevisionScope: "head",
		Types:         []string{"task/spec"},
	})
	if err != nil {
		t.Fatalf("select by type: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("want 1 task/spec record, got %d", len(recs))
	}
	if recs[0].Key != "task1" {
		t.Errorf("want key task1, got %s", recs[0].Key)
	}

	// Select by multiple types (view-like).
	recs, err = s.Select(context.Background(), Selector{
		Namespaces:    []string{"user/memory/project"},
		RevisionScope: "head",
		Types:         []string{"task/spec", "decision/adr", "system/map"},
	})
	if err != nil {
		t.Fatalf("select by multi-type: %v", err)
	}
	if len(recs) != 3 {
		t.Fatalf("want 3 records, got %d", len(recs))
	}
}

func TestSelectByStatus(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(context.Background(), Config{RootDir: dir})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	for _, st := range []string{"draft", "reviewed", "canonical"} {
		if _, err := s.AppendRecord(context.Background(), AppendInput{
			Namespace:  "user/memory/docs",
			Key:        "doc-" + st,
			Actor:      "user",
			Payload:    json.RawMessage(`{"status":"` + st + `"}`),
			RecordType: "decision/adr",
			Status:     st,
		}); err != nil {
			t.Fatalf("append %s: %v", st, err)
		}
	}

	recs, err := s.Select(context.Background(), Selector{
		Namespaces:    []string{"user/memory/docs"},
		RevisionScope: "head",
		Statuses:      []string{"canonical"},
	})
	if err != nil {
		t.Fatalf("select by status: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("want 1 canonical record, got %d", len(recs))
	}
	if recs[0].Status != "canonical" {
		t.Errorf("want canonical, got %s", recs[0].Status)
	}
}

func TestTTLCleanup(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(context.Background(), Config{RootDir: dir})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	// Write a record with an expired TTL.
	expired := time.Now().UTC().Add(-1 * time.Hour).Format(time.RFC3339)
	if _, err := s.AppendRecord(context.Background(), AppendInput{
		Namespace:  "user/cache/temp",
		Key:        "expired-note",
		Actor:      "user",
		Payload:    json.RawMessage(`{"note":"will expire"}`),
		RecordType: "note/volatile",
		Status:     "draft",
		TTL:        expired,
	}); err != nil {
		t.Fatalf("append expired: %v", err)
	}

	// Write a record with a future TTL.
	future := time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339)
	if _, err := s.AppendRecord(context.Background(), AppendInput{
		Namespace:  "user/cache/temp",
		Key:        "fresh-note",
		Actor:      "user",
		Payload:    json.RawMessage(`{"note":"still fresh"}`),
		RecordType: "note/volatile",
		Status:     "draft",
		TTL:        future,
	}); err != nil {
		t.Fatalf("append fresh: %v", err)
	}

	// Write a record with no TTL.
	if _, err := s.AppendRecord(context.Background(), AppendInput{
		Namespace:  "user/cache/temp",
		Key:        "no-ttl-note",
		Actor:      "user",
		Payload:    json.RawMessage(`{"note":"no expiry"}`),
		RecordType: "decision/adr",
		Status:     "canonical",
	}); err != nil {
		t.Fatalf("append no-ttl: %v", err)
	}

	cleaned, err := s.CleanupExpiredTTL(context.Background())
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if cleaned != 1 {
		t.Fatalf("want 1 cleaned, got %d", cleaned)
	}

	// Verify the expired record is gone.
	_, err = s.Head(context.Background(), "user/cache/temp", "expired-note")
	if err == nil {
		t.Error("expected expired record to be gone")
	}

	// Verify the fresh record still exists.
	head, err := s.Head(context.Background(), "user/cache/temp", "fresh-note")
	if err != nil {
		t.Fatalf("fresh note should still exist: %v", err)
	}
	if head.TTL != future {
		t.Errorf("fresh note TTL mismatch: want %s, got %s", future, head.TTL)
	}

	// Verify no-ttl record still exists.
	_, err = s.Head(context.Background(), "user/cache/temp", "no-ttl-note")
	if err != nil {
		t.Fatalf("no-ttl note should still exist: %v", err)
	}
}

func TestUpdateRecordStatus(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(context.Background(), Config{RootDir: dir})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	// Write initial record.
	if _, err := s.AppendRecord(context.Background(), AppendInput{
		Namespace:      "user/memory/decisions",
		Key:            "adr-001",
		Actor:          "user",
		Payload:        json.RawMessage(`{"title":"Use gRPC"}`),
		RecordType:     "decision/adr",
		Status:         "draft",
		ContentVersion: 1,
	}); err != nil {
		t.Fatalf("append: %v", err)
	}

	// Promote to reviewed.
	rec, err := s.UpdateRecordStatus(context.Background(), "user/memory/decisions", "adr-001", "user", "reviewed")
	if err != nil {
		t.Fatalf("update status: %v", err)
	}
	if rec.Status != "reviewed" {
		t.Errorf("want status reviewed, got %s", rec.Status)
	}
	if rec.ContentVersion != 2 {
		t.Errorf("want content_version 2, got %d", rec.ContentVersion)
	}
	if rec.Revision != 2 {
		t.Errorf("want revision 2, got %d", rec.Revision)
	}

	// Verify head reflects the new status.
	head, err := s.Head(context.Background(), "user/memory/decisions", "adr-001")
	if err != nil {
		t.Fatalf("head: %v", err)
	}
	if head.Status != "reviewed" {
		t.Errorf("head status: want reviewed, got %s", head.Status)
	}
}

func TestHistoryPreservesTypeFields(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(context.Background(), Config{RootDir: dir})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	// Write two revisions with different statuses.
	if _, err := s.AppendRecord(context.Background(), AppendInput{
		Namespace:  "user/memory/docs",
		Key:        "typed-doc",
		Actor:      "user",
		Payload:    json.RawMessage(`{"v":1}`),
		RecordType: "strategy/goal",
		Status:     "draft",
	}); err != nil {
		t.Fatalf("append r1: %v", err)
	}
	if _, err := s.AppendRecord(context.Background(), AppendInput{
		Namespace:  "user/memory/docs",
		Key:        "typed-doc",
		Actor:      "user",
		Payload:    json.RawMessage(`{"v":2}`),
		RecordType: "strategy/goal",
		Status:     "reviewed",
	}); err != nil {
		t.Fatalf("append r2: %v", err)
	}

	history, err := s.History(context.Background(), "user/memory/docs", "typed-doc", 0)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(history) != 2 {
		t.Fatalf("want 2 revisions, got %d", len(history))
	}
	if history[0].Status != "draft" {
		t.Errorf("rev 1 status: want draft, got %s", history[0].Status)
	}
	if history[1].Status != "reviewed" {
		t.Errorf("rev 2 status: want reviewed, got %s", history[1].Status)
	}
	if history[0].RecordType != "strategy/goal" {
		t.Errorf("rev 1 type: want strategy/goal, got %s", history[0].RecordType)
	}
}

func TestProvenanceOnWrite(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(context.Background(), Config{RootDir: dir})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	prov := json.RawMessage(`{"source":"carrier","generated_by":"agent-v1"}`)
	rec, err := s.AppendRecord(context.Background(), AppendInput{
		Namespace:  "app/carrier/session",
		Key:        "summary",
		Actor:      "app:carrier",
		Payload:    json.RawMessage(`{"title":"Session Summary"}`),
		RecordType: "brief/summary",
		Status:     "draft",
		Provenance: prov,
	})
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	if len(rec.Provenance) == 0 {
		t.Fatal("provenance should be set on the returned record")
	}

	head, err := s.Head(context.Background(), "app/carrier/session", "summary")
	if err != nil {
		t.Fatalf("head: %v", err)
	}
	if len(head.Provenance) == 0 {
		t.Fatal("provenance should be set on head record")
	}
	var p map[string]string
	if err := json.Unmarshal(head.Provenance, &p); err != nil {
		t.Fatalf("provenance unmarshal: %v", err)
	}
	if p["source"] != "carrier" {
		t.Errorf("provenance source: want carrier, got %s", p["source"])
	}
}

func TestMigrationCreatesMemoryTables(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(context.Background(), Config{RootDir: dir})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	db := s.DB()
	if db == nil {
		t.Fatal("DB() returned nil")
	}

	for _, table := range []string{"memory_state", "memory_revisions"} {
		var name string
		err := db.QueryRow(
			`SELECT name FROM sqlite_master WHERE type='table' AND name=?`,
			table,
		).Scan(&name)
		if err != nil {
			t.Errorf("expected table %s to exist: %v", table, err)
		}
	}

	for _, idx := range []string{
		"idx_memory_state_namespace",
		"idx_memory_state_activation",
		"idx_memory_revisions_memory_id",
		"idx_memory_revisions_namespace",
		"idx_memory_revisions_created_at",
		"idx_memory_revisions_status",
		"idx_memory_revisions_expires_at",
	} {
		var name string
		err := db.QueryRow(
			`SELECT name FROM sqlite_master WHERE type='index' AND name=?`,
			idx,
		).Scan(&name)
		if err != nil {
			t.Errorf("expected index %s to exist: %v", idx, err)
		}
	}
}

// TestMigrationCreatesFTS5Index verifies the case-12 migration created the
// FTS5 virtual table and its sync triggers over memory_revisions content
// columns.
func TestMigrationCreatesFTS5Index(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(context.Background(), Config{RootDir: dir})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	db := s.DB()

	var name string
	if err := db.QueryRow(
		`SELECT name FROM sqlite_master WHERE type='table' AND name='memory_revisions_fts'`,
	).Scan(&name); err != nil {
		t.Fatalf("expected memory_revisions_fts virtual table: %v", err)
	}

	for _, trg := range []string{
		"memory_revisions_fts_ai",
		"memory_revisions_fts_ad",
	} {
		var tname string
		if err := db.QueryRow(
			`SELECT name FROM sqlite_master WHERE type='trigger' AND name=?`,
			trg,
		).Scan(&tname); err != nil {
			t.Errorf("expected trigger %s: %v", trg, err)
		}
	}
}

// TestFTS5TriggersMirrorRevisionWrites exercises the AFTER INSERT and
// AFTER DELETE triggers by writing a memory_revisions row, running an
// FTS5 MATCH search, and confirming the row is indexed and removed on
// delete. Content (payload_summary, payload_body, tags) is indexed;
// status is intentionally NOT indexed — status filtering lives at query
// time via JOIN, keeping the BM25 arm deterministic.
func TestFTS5TriggersMirrorRevisionWrites(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(context.Background(), Config{RootDir: dir})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	db := s.DB()
	ctx := context.Background()

	if _, err := db.ExecContext(ctx,
		`INSERT INTO memory_state (memory_id, namespace, memory_key, domain)
		 VALUES (?, ?, ?, 'memory')`,
		"mem-1", "user/test", "k1",
	); err != nil {
		t.Fatalf("insert memory_state: %v", err)
	}

	if _, err := db.ExecContext(ctx, `
INSERT INTO memory_revisions (
    revision_id, memory_id, domain, namespace, memory_key, status,
    author_agent_id, author_version, "trigger", session_id, origin, confidence,
    tags, payload_summary, payload_body
) VALUES (?, ?, 'memory', ?, ?, 'canonical',
    'agent-1', 'v1', 'manual', 'sess-1', 'agent', 0.9,
    '["hybrid","fts5"]', 'Hybrid relevance sprint kickoff', 'Reciprocal rank fusion across dense and lexical signals')`,
		"rev-1", "mem-1", "user/test", "k1",
	); err != nil {
		t.Fatalf("insert revision: %v", err)
	}

	var rowid int64
	if err := db.QueryRowContext(ctx,
		`SELECT rowid FROM memory_revisions_fts WHERE memory_revisions_fts MATCH ?`,
		"kickoff",
	).Scan(&rowid); err != nil {
		t.Fatalf("fts match after insert: %v", err)
	}

	var gotID string
	if err := db.QueryRowContext(ctx,
		`SELECT revision_id FROM memory_revisions WHERE rowid = ?`,
		rowid,
	).Scan(&gotID); err != nil {
		t.Fatalf("lookup revision by rowid: %v", err)
	}
	if gotID != "rev-1" {
		t.Fatalf("expected rev-1 via FTS rowid, got %s", gotID)
	}

	if _, err := db.ExecContext(ctx,
		`DELETE FROM memory_revisions WHERE revision_id = ?`, "rev-1",
	); err != nil {
		t.Fatalf("delete revision: %v", err)
	}

	var count int
	if err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM memory_revisions_fts WHERE memory_revisions_fts MATCH ?`,
		"kickoff",
	).Scan(&count); err != nil {
		t.Fatalf("fts count after delete: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 FTS rows after delete, got %d", count)
	}
}

// TestFTS5BackfillsExistingRevisions asserts that when the migration runs
// on a database that already contains memory_revisions rows (upgrade
// path from schema v11 → v12), those rows are backfilled into the FTS
// index, not only new inserts.
func TestFTS5BackfillsExistingRevisions(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(context.Background(), Config{RootDir: dir})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	db := s.DB()
	ctx := context.Background()

	// Sanity: we open the store and immediately have FTS plumbing. Simulate
	// "upgrade" by dropping the FTS index + triggers and re-running migrate.
	if _, err := db.ExecContext(ctx, `DROP TRIGGER IF EXISTS memory_revisions_fts_ai`); err != nil {
		t.Fatalf("drop ai trigger: %v", err)
	}
	if _, err := db.ExecContext(ctx, `DROP TRIGGER IF EXISTS memory_revisions_fts_ad`); err != nil {
		t.Fatalf("drop ad trigger: %v", err)
	}
	if _, err := db.ExecContext(ctx, `DROP TABLE IF EXISTS memory_revisions_fts`); err != nil {
		t.Fatalf("drop fts table: %v", err)
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM schema_version WHERE version = 12`); err != nil {
		t.Fatalf("roll schema_version back: %v", err)
	}

	// Seed a revision while the FTS index is absent.
	if _, err := db.ExecContext(ctx,
		`INSERT INTO memory_state (memory_id, namespace, memory_key, domain)
		 VALUES (?, ?, ?, 'memory')`,
		"mem-legacy", "user/test", "k-legacy",
	); err != nil {
		t.Fatalf("insert memory_state: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
INSERT INTO memory_revisions (
    revision_id, memory_id, domain, namespace, memory_key, status,
    author_agent_id, author_version, "trigger", session_id, origin, confidence,
    tags, payload_summary, payload_body
) VALUES (?, ?, 'memory', ?, ?, 'canonical',
    'agent-legacy', 'v1', 'manual', 'sess-legacy', 'agent', 0.8,
    '[]', 'Legacy revision predates FTS5', '')`,
		"rev-legacy", "mem-legacy", "user/test", "k-legacy",
	); err != nil {
		t.Fatalf("insert legacy revision: %v", err)
	}

	// Re-run migration; case 12 should backfill.
	if err := s.migrate(ctx); err != nil {
		t.Fatalf("re-migrate: %v", err)
	}

	var rowid int64
	if err := db.QueryRowContext(ctx,
		`SELECT rowid FROM memory_revisions_fts WHERE memory_revisions_fts MATCH ?`,
		"legacy",
	).Scan(&rowid); err != nil {
		t.Fatalf("fts match after backfill: %v", err)
	}

	var gotID string
	if err := db.QueryRowContext(ctx,
		`SELECT revision_id FROM memory_revisions WHERE rowid = ?`,
		rowid,
	).Scan(&gotID); err != nil {
		t.Fatalf("lookup revision by rowid: %v", err)
	}
	if gotID != "rev-legacy" {
		t.Errorf("expected rev-legacy via FTS rowid, got %s", gotID)
	}
}
