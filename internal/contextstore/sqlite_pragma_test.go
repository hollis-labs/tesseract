package contextstore

import (
	"context"
	"database/sql"
	"strings"
	"sync"
	"testing"
)

// TestOpenAppliesConnectionPragmas asserts that connections handed out by the
// pool that Open builds actually carry busy_timeout and foreign_keys.
//
// This deliberately reads the pragmas back off live connections rather than
// inspecting the DSN string. The defect this guards against was a DSN written
// in mattn/go-sqlite3 syntax (_busy_timeout=, _fk=) while the driver is
// modernc.org/sqlite, which ignores unrecognized parameters silently. The
// string looked correct and applied nothing, so any assertion over the string
// would have passed while the pragmas were off.
//
// It checks several simultaneously-pinned connections, because the pragmas are
// per-connection: a pool whose first connection is configured and whose later
// ones are not would still be broken.
func TestOpenAppliesConnectionPragmas(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, Config{RootDir: t.TempDir()})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	const conns = 4
	pinned := make([]*sql.Conn, 0, conns)
	for i := 0; i < conns; i++ {
		c, err := s.DB().Conn(ctx)
		if err != nil {
			t.Fatalf("pin conn %d: %v", i, err)
		}
		pinned = append(pinned, c)
	}
	// Assert presence before properties: an empty set would make the loop
	// below vacuously pass.
	if len(pinned) != conns {
		t.Fatalf("pinned %d connections, want %d", len(pinned), conns)
	}
	t.Cleanup(func() {
		for _, c := range pinned {
			_ = c.Close()
		}
	})

	for i, c := range pinned {
		var busyTimeout int
		if err := c.QueryRowContext(ctx, `PRAGMA busy_timeout`).Scan(&busyTimeout); err != nil {
			t.Fatalf("conn %d: read busy_timeout: %v", i, err)
		}
		if busyTimeout != 5000 {
			t.Errorf("conn %d: busy_timeout = %d, want 5000", i, busyTimeout)
		}

		var foreignKeys int
		if err := c.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil {
			t.Fatalf("conn %d: read foreign_keys: %v", i, err)
		}
		if foreignKeys != 1 {
			t.Errorf("conn %d: foreign_keys = %d, want 1 (enforcement off)", i, foreignKeys)
		}
	}
}

// TestOpenEnforcesForeignKeys proves enforcement is actually live, not merely
// that the pragma reads back as 1. Without this the pragma assertion above
// could pass against a driver that reported the setting without honoring it.
func TestOpenEnforcesForeignKeys(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, Config{RootDir: t.TempDir()})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	db := s.DB()
	if _, err := db.ExecContext(ctx, `CREATE TABLE fk_parent (id TEXT PRIMARY KEY)`); err != nil {
		t.Fatalf("create parent: %v", err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE fk_child (
		id TEXT PRIMARY KEY,
		parent_id TEXT NOT NULL REFERENCES fk_parent(id)
	)`); err != nil {
		t.Fatalf("create child: %v", err)
	}

	_, err = db.ExecContext(ctx, `INSERT INTO fk_child (id, parent_id) VALUES ('c1', 'no-such-parent')`)
	if err == nil {
		t.Fatal("inserting a child row with no parent succeeded; foreign keys are not enforced")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "foreign key") {
		t.Fatalf("insert failed but not with a foreign key error: %v", err)
	}
}

// TestConcurrentWritersDoNotFailBusy reproduces the production failure mode:
// concurrent writers on a pool with more than one connection. SQLite allows a
// single writer at a time, so the loser of a write race must wait for
// busy_timeout. With busy_timeout unset it returns SQLITE_BUSY immediately.
//
// This needs genuine concurrency, not repetition. A serial workload is served
// by one reused connection, never grows the pool, and never contends — which
// is why -count alone does not reproduce this.
func TestConcurrentWritersDoNotFailBusy(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, Config{RootDir: t.TempDir()})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	db := s.DB()
	if _, err := db.ExecContext(ctx, `CREATE TABLE busy_probe (id INTEGER PRIMARY KEY, v TEXT)`); err != nil {
		t.Fatalf("create probe table: %v", err)
	}

	const writers, perWriter = 8, 40
	var wg sync.WaitGroup
	errCh := make(chan error, writers*perWriter)
	start := make(chan struct{})

	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start // release together, so the writes actually overlap
			for j := 0; j < perWriter; j++ {
				if _, err := db.ExecContext(ctx,
					`INSERT INTO busy_probe (v) VALUES (?)`, "w"); err != nil {
					errCh <- err
					return
				}
			}
		}(i)
	}
	close(start)
	wg.Wait()
	close(errCh)

	var busy int
	var first error
	for err := range errCh {
		if first == nil {
			first = err
		}
		if strings.Contains(strings.ToLower(err.Error()), "database is locked") {
			busy++
		}
	}
	if first != nil {
		t.Fatalf("%d concurrent writers hit %d SQLITE_BUSY failures; first error: %v", writers, busy, first)
	}

	// Positive control: if the workload never actually wrote, the absence of
	// errors above would prove nothing.
	var got int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM busy_probe`).Scan(&got); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if want := writers * perWriter; got != want {
		t.Fatalf("wrote %d rows, want %d", got, want)
	}
}
