package contextstore

import (
	"context"
	"encoding/json"
	"testing"
)

// TestDeriveNamespaceOwner exercises the owner-derivation rule for both the
// well-formed user/* and app/* shapes and the sentinel cases (single-segment,
// missing owner_id, non-tier prefix). CW-20260428-0005.
func TestDeriveNamespaceOwner(t *testing.T) {
	cases := []struct {
		ns        string
		wantType  string
		wantOwner string
	}{
		{"user/chrispian/memory", "user", "chrispian"},
		{"user/chrispian/project/nanite/memory", "user", "chrispian"},
		{"app/tesseract", "app", "tesseract"},
		{"app/tesseract/identity", "app", "tesseract"},
		{"mentat", "system", "mentat"},
		{"adr", "system", "adr"},
		{"user/", "system", "user/"},
		{"user//memory", "system", "user//memory"},
		{"foo/bar", "system", "foo/bar"},
	}
	for _, tc := range cases {
		gotType, gotOwner := DeriveNamespaceOwner(tc.ns)
		if gotType != tc.wantType || gotOwner != tc.wantOwner {
			t.Errorf("DeriveNamespaceOwner(%q) = (%q, %q), want (%q, %q)",
				tc.ns, gotType, gotOwner, tc.wantType, tc.wantOwner)
		}
	}
}

// TestEnsureNamespaceRegistered_InsertsAndIsIdempotent validates the helper's
// happy path: well-formed user namespace, derived owner, source=inferred
// metadata, audit emission on first call, no-op on second call.
func TestEnsureNamespaceRegistered_InsertsAndIsIdempotent(t *testing.T) {
	s, err := Open(context.Background(), Config{RootDir: t.TempDir()})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	ctx := context.Background()
	const ns = "user/chrispian/memory"

	if err := s.EnsureNamespaceRegistered(ctx, ns); err != nil {
		t.Fatalf("first ensure: %v", err)
	}

	entry, err := s.GetNamespacePolicy(ctx, ns)
	if err != nil {
		t.Fatalf("get policy: %v", err)
	}
	if entry.OwnerType != "user" || entry.OwnerID != "chrispian" {
		t.Fatalf("derived owner mismatch: got (%q, %q)", entry.OwnerType, entry.OwnerID)
	}
	src, _ := entry.Policy["source"].(string)
	if src != "inferred" {
		t.Fatalf("expected source=inferred, got %q", src)
	}

	events, err := s.ListAuditEvents(ctx, 10)
	if err != nil {
		t.Fatalf("list audit: %v", err)
	}
	var registerCount int
	for _, ev := range events {
		if ev.EventType == EventNamespaceRegister && ev.Namespace == ns {
			registerCount++
			if ev.Actor != "system" {
				t.Errorf("expected actor=system, got %q", ev.Actor)
			}
			var meta map[string]any
			if err := json.Unmarshal(ev.Metadata, &meta); err != nil {
				t.Fatalf("unmarshal metadata: %v", err)
			}
			if meta["source"] != "inferred" {
				t.Errorf("expected metadata source=inferred, got %v", meta["source"])
			}
		}
	}
	if registerCount != 1 {
		t.Fatalf("expected exactly 1 namespace.register event, got %d", registerCount)
	}

	// Second call must be a no-op: no new policy row mutation, no new audit event.
	if err := s.EnsureNamespaceRegistered(ctx, ns); err != nil {
		t.Fatalf("second ensure: %v", err)
	}
	events2, err := s.ListAuditEvents(ctx, 10)
	if err != nil {
		t.Fatalf("list audit second: %v", err)
	}
	var registerCount2 int
	for _, ev := range events2 {
		if ev.EventType == EventNamespaceRegister && ev.Namespace == ns {
			registerCount2++
		}
	}
	if registerCount2 != 1 {
		t.Fatalf("expected idempotent ensure to keep 1 register event, got %d", registerCount2)
	}
}

// TestEnsureNamespaceRegistered_MalformedSentinel verifies that namespaces
// that don't fit the user/* or app/* shape get registered with the sentinel
// owner (system, <namespace>) instead of being rejected.
func TestEnsureNamespaceRegistered_MalformedSentinel(t *testing.T) {
	s, err := Open(context.Background(), Config{RootDir: t.TempDir()})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	ctx := context.Background()
	const ns = "mentat"

	if err := s.EnsureNamespaceRegistered(ctx, ns); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	entry, err := s.GetNamespacePolicy(ctx, ns)
	if err != nil {
		t.Fatalf("get policy: %v", err)
	}
	if entry.OwnerType != "system" || entry.OwnerID != ns {
		t.Fatalf("sentinel owner mismatch: got (%q, %q)", entry.OwnerType, entry.OwnerID)
	}
}

// TestEnsureNamespaceRegistered_PreservesExplicit makes sure the auto-register
// path does not overwrite a row that was created via the explicit
// UpsertNamespacePolicy path. The helper's idempotent fast path (SELECT then
// INSERT-OR-IGNORE) returns immediately when a row exists.
func TestEnsureNamespaceRegistered_PreservesExplicit(t *testing.T) {
	s, err := Open(context.Background(), Config{RootDir: t.TempDir()})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	ctx := context.Background()
	const ns = "app/editor"

	// Explicit registration with a tier policy.
	explicit := NamespacePolicyEntry{
		Namespace: ns,
		OwnerType: "app",
		OwnerID:   "editor",
		Policy:    map[string]any{"tier": "memory", "max_revisions": float64(50)},
	}
	if err := s.UpsertNamespacePolicy(ctx, explicit); err != nil {
		t.Fatalf("upsert explicit: %v", err)
	}

	if err := s.EnsureNamespaceRegistered(ctx, ns); err != nil {
		t.Fatalf("ensure on explicit: %v", err)
	}
	entry, err := s.GetNamespacePolicy(ctx, ns)
	if err != nil {
		t.Fatalf("get policy: %v", err)
	}
	if entry.Policy["tier"] != "memory" {
		t.Fatalf("ensure overwrote explicit tier policy: %+v", entry.Policy)
	}
	if _, hasSource := entry.Policy["source"]; hasSource {
		t.Fatalf("ensure should not have stamped source on explicit row: %+v", entry.Policy)
	}
}

// TestReconcileNamespaceRegistry_BackfillsFromMemoryAndContext seeds the DB
// with namespaces that have data but no policy row (mirroring the live drift
// observed in CW-20260428-0005), then verifies ReconcileNamespaceRegistry
// inserts policy rows for each, marks them with source=inferred-backfill, and
// stays idempotent on a second invocation.
func TestReconcileNamespaceRegistry_BackfillsFromMemoryAndContext(t *testing.T) {
	s, err := Open(context.Background(), Config{RootDir: t.TempDir()})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	ctx := context.Background()

	// Seed memory_revisions directly so it bypasses the AppendRecord
	// auto-register path. This simulates the legacy DB state where memory
	// data exists but namespace_policies has no row for it.
	seedMemory := func(memID, namespace, key string) {
		t.Helper()
		if _, err := s.db.ExecContext(ctx, `
INSERT INTO memory_state (memory_id, domain, namespace, memory_key, activation, access_count)
VALUES (?, ?, ?, ?, 1.0, 0)`,
			memID, "memory", namespace, key); err != nil {
			t.Fatalf("seed memory_state: %v", err)
		}
		if _, err := s.db.ExecContext(ctx, `
INSERT INTO memory_revisions (
    revision_id, memory_id, domain, namespace, memory_key, status, created_at,
    author_agent_id, author_version, trigger, session_id, origin, confidence,
    tags, payload_summary
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			"r-"+memID, memID, "memory", namespace, key, "canonical",
			"2026-04-28T00:00:00Z", "test", "", "explicit", "test-session",
			"observation", 0.9, "[]", "test"); err != nil {
			t.Fatalf("seed memory_revisions: %v", err)
		}
	}

	seedMemory("m1", "user/chrispian/memory", "test.key")
	seedMemory("m2", "user/chrispian/project/nanite/memory", "test.key")
	seedMemory("m3", "mentat", "test.key") // malformed → sentinel

	registered, err := s.ReconcileNamespaceRegistry(ctx)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if registered != 3 {
		t.Fatalf("expected 3 registrations, got %d", registered)
	}

	// All three should now appear in the policy registry.
	for _, want := range []struct {
		ns        string
		ownerType string
		ownerID   string
	}{
		{"user/chrispian/memory", "user", "chrispian"},
		{"user/chrispian/project/nanite/memory", "user", "chrispian"},
		{"mentat", "system", "mentat"},
	} {
		entry, err := s.GetNamespacePolicy(ctx, want.ns)
		if err != nil {
			t.Fatalf("get %s: %v", want.ns, err)
		}
		if entry.OwnerType != want.ownerType || entry.OwnerID != want.ownerID {
			t.Errorf("%s: got (%q, %q), want (%q, %q)", want.ns,
				entry.OwnerType, entry.OwnerID, want.ownerType, want.ownerID)
		}
		if src, _ := entry.Policy["source"].(string); src != "inferred-backfill" {
			t.Errorf("%s: expected source=inferred-backfill, got %q", want.ns, src)
		}
	}

	// Idempotent: second reconcile registers nothing.
	registered2, err := s.ReconcileNamespaceRegistry(ctx)
	if err != nil {
		t.Fatalf("reconcile second: %v", err)
	}
	if registered2 != 0 {
		t.Fatalf("expected idempotent reconcile to register 0, got %d", registered2)
	}
}

// TestAppendRecord_AutoRegistersNamespace is the integration-level evidence
// that the bug from CW-20260428-0005 is closed: a context write to a fresh
// namespace makes that namespace surface in ListNamespacePolicies (which
// backs context_namespaces_list and the K&M view).
func TestAppendRecord_AutoRegistersNamespace(t *testing.T) {
	s, err := Open(context.Background(), Config{RootDir: t.TempDir()})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	ctx := context.Background()
	const ns = "app/editor/session"

	if _, err := s.AppendRecord(ctx, AppendInput{
		Namespace: ns,
		Key:       "summary",
		Actor:     "app:editor",
		Payload:   []byte(`{"v":1}`),
	}); err != nil {
		t.Fatalf("append: %v", err)
	}

	entries, err := s.ListNamespacePolicies(ctx)
	if err != nil {
		t.Fatalf("list policies: %v", err)
	}
	var found bool
	for _, e := range entries {
		if e.Namespace == ns {
			found = true
			if e.OwnerType != "app" || e.OwnerID != "editor" {
				t.Errorf("derived owner mismatch: got (%q, %q)", e.OwnerType, e.OwnerID)
			}
			if src, _ := e.Policy["source"].(string); src != "inferred" {
				t.Errorf("expected source=inferred, got %q", src)
			}
		}
	}
	if !found {
		t.Fatalf("namespace %q not surfaced in ListNamespacePolicies after AppendRecord (the bug)", ns)
	}
}
