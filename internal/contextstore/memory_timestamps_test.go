package contextstore

import (
	"context"
	"strings"
	"testing"
)

func TestSchema16NormalizesEveryMemoryTimestampAndPreservesIndexedOrdering(t *testing.T) {
	ctx := context.Background()
	s, openErr := Open(ctx, Config{RootDir: t.TempDir()})
	if openErr != nil {
		t.Fatalf("Open: %v", openErr)
	}
	defer s.Close()

	db := s.DB()
	if _, err := db.ExecContext(ctx, `
INSERT INTO memory_state (
    memory_id, namespace, memory_key, domain, current_revision,
    created_at, last_accessed_at, last_decayed_at
) VALUES (
    'mem-prefix', 'user/test/memory/notes', 'prefix.key', 'memory', 'rev-new',
    '2026-09-04 13:34:55', '2026-09-04T13:34:55Z', '2026-09-04T13:34:55.0000001Z'
)`); err != nil {
		t.Fatalf("seed memory_state: %v", err)
	}
	insertRevision := func(id, createdAt, expiresAt, resolvedAt string) {
		t.Helper()
		if _, err := db.ExecContext(ctx, `
INSERT INTO memory_revisions (
    revision_id, memory_id, domain, namespace, memory_key, status, created_at,
    author_agent_id, author_version, "trigger", session_id, origin, confidence,
    tags, expires_at, payload_summary, facet_kind, facet_source,
    facet_pointer_scheme, facet_pointer_locator, facet_pointer_resolved_at
) VALUES (?, 'mem-prefix', 'memory', 'user/test/memory/notes', 'prefix.key',
    'canonical', ?, 'test', '1', 'manual', 'sess', 'user', 0.9, '[]', ?,
    'prefix timestamp', 'note', 'manual', 'https', 'https://example.test', ?)`,
			id, createdAt, expiresAt, resolvedAt); err != nil {
			t.Fatalf("seed revision %s: %v", id, err)
		}
	}
	insertRevision("rev-old", "2026-09-04T13:34:55.09234Z", "2026-09-04T13:34:55.09234Z", "2026-09-04T08:34:55.1234-05:00")
	insertRevision("rev-new", "2026-09-04T13:34:55.092342Z", "2026-09-04T13:34:55.092342987Z", "2026-09-04T13:34:55Z")

	for _, checkedAt := range []string{
		"2026-09-04T13:34:55.09234Z",
		"2026-09-04T13:34:55.092342Z",
	} {
		if _, err := db.ExecContext(ctx, `
INSERT INTO pointer_verifications (revision_id, scheme, locator, outcome, checked_at)
VALUES ('rev-new', 'https', 'https://example.test', 'resolved', ?)`, checkedAt); err != nil {
			t.Fatalf("seed pointer verification: %v", err)
		}
	}

	// Simulate opening the unreleased schema-15 database that case 16 upgrades.
	if _, err := db.ExecContext(ctx, `DELETE FROM schema_version WHERE version = 16`); err != nil {
		t.Fatalf("roll schema version back: %v", err)
	}
	if err := s.migrate(ctx); err != nil {
		t.Fatalf("migrate schema 16: %v", err)
	}

	assertText := func(query, want string, args ...any) {
		t.Helper()
		var got string
		if err := db.QueryRowContext(ctx, query, args...).Scan(&got); err != nil {
			t.Fatalf("query %q: %v", query, err)
		}
		if got != want {
			t.Fatalf("query %q = %q, want %q", query, got, want)
		}
	}
	assertText(`SELECT created_at FROM memory_state WHERE memory_id = 'mem-prefix'`, "2026-09-04T13:34:55.000000000Z")
	assertText(`SELECT last_accessed_at FROM memory_state WHERE memory_id = 'mem-prefix'`, "2026-09-04T13:34:55.000000000Z")
	assertText(`SELECT last_decayed_at FROM memory_state WHERE memory_id = 'mem-prefix'`, "2026-09-04T13:34:55.000000100Z")
	assertText(`SELECT created_at FROM memory_revisions WHERE revision_id = 'rev-old'`, "2026-09-04T13:34:55.092340000Z")
	assertText(`SELECT expires_at FROM memory_revisions WHERE revision_id = 'rev-new'`, "2026-09-04T13:34:55.092342987Z")
	assertText(`SELECT facet_pointer_resolved_at FROM memory_revisions WHERE revision_id = 'rev-old'`, "2026-09-04T13:34:55.123400000Z")
	assertText(`SELECT MAX(checked_at) FROM pointer_verifications`, "2026-09-04T13:34:55.092342000Z")

	// The production SQL primitives now give chronological answers directly.
	assertText(`SELECT revision_id FROM memory_revisions WHERE memory_id = 'mem-prefix' ORDER BY created_at DESC, revision_id DESC LIMIT 1`, "rev-new")
	assertText(`SELECT revision_id FROM memory_revisions WHERE expires_at <= ? ORDER BY expires_at DESC LIMIT 1`, "rev-old", "2026-09-04T13:34:55.092340001Z")

	rows, queryErr := db.QueryContext(ctx, `EXPLAIN QUERY PLAN
SELECT revision_id FROM memory_revisions
WHERE expires_at IS NOT NULL AND expires_at <= ? AND status != ?`,
		"2026-09-04T13:34:55.092340001Z", "deprecated")
	if queryErr != nil {
		t.Fatalf("explain TTL query: %v", queryErr)
	}
	var plan strings.Builder
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatalf("scan query plan: %v", err)
		}
		plan.WriteString(detail)
		plan.WriteByte('\n')
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate query plan: %v", err)
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("close TTL query plan: %v", err)
	}
	if !strings.Contains(plan.String(), "idx_memory_revisions_expires_at") {
		t.Fatalf("TTL query stopped using expires_at index:\n%s", plan.String())
	}

	rows, queryErr = db.QueryContext(ctx, `EXPLAIN QUERY PLAN
SELECT COUNT(*) FROM pointer_verifications WHERE checked_at >= ?`,
		"2026-09-04T13:34:55.092340001Z")
	if queryErr != nil {
		t.Fatalf("explain pointer observation range query: %v", queryErr)
	}
	plan.Reset()
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatalf("scan pointer observation query plan: %v", err)
		}
		plan.WriteString(detail)
		plan.WriteByte('\n')
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate pointer observation query plan: %v", err)
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("close pointer observation query plan: %v", err)
	}
	if !strings.Contains(plan.String(), "idx_pointer_verifications_checked_at") {
		t.Fatalf("pointer observation range query stopped using checked_at index:\n%s", plan.String())
	}
}

func TestSchema16TimestampNormalizationRollsBackAtomicallyOnInvalidData(t *testing.T) {
	ctx := context.Background()
	s, openErr := Open(ctx, Config{RootDir: t.TempDir()})
	if openErr != nil {
		t.Fatalf("Open: %v", openErr)
	}
	defer s.Close()

	db := s.DB()
	if _, err := db.ExecContext(ctx, `
INSERT INTO memory_state (memory_id, namespace, memory_key, domain)
VALUES ('mem-invalid', 'user/test/memory/notes', 'invalid.key', 'memory')`); err != nil {
		t.Fatalf("seed memory_state: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
INSERT INTO memory_revisions (
    revision_id, memory_id, domain, namespace, memory_key, status, created_at,
    author_agent_id, author_version, "trigger", session_id, origin, confidence,
    tags, expires_at, payload_summary
) VALUES (
    'rev-invalid', 'mem-invalid', 'memory', 'user/test/memory/notes', 'invalid.key',
    'canonical', '2026-09-04T13:34:55.09234Z', 'test', '1', 'manual',
    'sess', 'user', 0.9, '[]', 'not-a-time', 'invalid timestamp fixture'
)`); err != nil {
		t.Fatalf("seed invalid revision: %v", err)
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM schema_version WHERE version = 16`); err != nil {
		t.Fatalf("roll schema version back: %v", err)
	}

	migrateErr := s.migrate(ctx)
	if migrateErr == nil {
		t.Fatal("migration accepted a nonempty invalid timestamp")
	}
	if !strings.Contains(migrateErr.Error(), "memory_revisions.expires_at") ||
		!strings.Contains(migrateErr.Error(), "not-a-time") {
		t.Fatalf("migration error lacks column/value context: %v", migrateErr)
	}

	var createdAt string
	if err := db.QueryRowContext(ctx,
		`SELECT created_at FROM memory_revisions WHERE revision_id = 'rev-invalid'`).Scan(&createdAt); err != nil {
		t.Fatalf("read rolled-back timestamp: %v", err)
	}
	if createdAt != "2026-09-04T13:34:55.09234Z" {
		t.Fatalf("migration partially normalized before failure: created_at = %q", createdAt)
	}
	var version int
	if err := db.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_version`).Scan(&version); err != nil {
		t.Fatalf("read schema version: %v", err)
	}
	if version != 15 {
		t.Fatalf("failed migration recorded schema version %d, want 15", version)
	}
}
