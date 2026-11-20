package contextstore

import (
	"context"
	"database/sql"
	"testing"
)

func TestMigration32AddsStatusReceiptsWithoutTrustBackfill(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, Config{RootDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	db := s.DB()
	for _, stmt := range []string{
		`INSERT INTO memory_state (memory_id,domain,namespace) VALUES ('status-old-item','event','project/test/event/reasoning')`,
		`INSERT INTO memory_revisions (revision_id,memory_id,domain,namespace,status,author_agent_id,author_version,"trigger",session_id,derived_from,confidence,tags,payload_summary) VALUES ('status-old-rev','status-old-item','event','project/test/event/reasoning','canonical','trusted-dispatcher','','manual','s','observation',1,'[]','before migration')`,
		`DROP TABLE event_write_receipts`,
		`DROP INDEX idx_event_author_time`,
		`DROP INDEX idx_memory_revisions_state_state`,
		`DROP INDEX idx_memory_revisions_state_subject_urn`,
		`DROP INDEX idx_memory_revisions_state_task`,
		`DROP INDEX idx_memory_revisions_state_run`,
		`ALTER TABLE memory_revisions DROP COLUMN writer_principal`,
		`DELETE FROM schema_version WHERE version >= 32`,
	} {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("prepare schema31: %s: %v", stmt, err)
		}
	}
	if err := s.migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var principal sql.NullString
	var author string
	if err := db.QueryRowContext(ctx, `SELECT writer_principal, author_agent_id FROM memory_revisions WHERE revision_id='status-old-rev'`).Scan(&principal, &author); err != nil {
		t.Fatal(err)
	}
	if principal.Valid || author != "trusted-dispatcher" {
		t.Fatalf("historical author became verified: principal=%+v author=%q", principal, author)
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM event_write_receipts`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("unexpected historical receipts: %d %v", count, err)
	}
}
