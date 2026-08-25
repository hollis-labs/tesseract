package main

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// TestKindsDSN_DryRunHandleIsReadOnly proves the dry-run connection cannot
// write. The dry-run code path does not attempt writes, so this asserts the
// stronger property: even a wrong code path could not mutate the store.
func TestKindsDSN_DryRunHandleIsReadOnly(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "probe.db")

	// Seed with a writable handle.
	seed, openErr := sql.Open("sqlite", kindsDSN(path, true))
	if openErr != nil {
		t.Fatalf("open writable: %v", openErr)
	}
	if _, execErr := seed.ExecContext(ctx, `CREATE TABLE t (id INTEGER PRIMARY KEY, v TEXT)`); execErr != nil {
		t.Fatalf("create table: %v", execErr)
	}
	if _, execErr := seed.ExecContext(ctx, `INSERT INTO t (id, v) VALUES (1, 'before')`); execErr != nil {
		t.Fatalf("insert: %v", execErr)
	}
	if closeErr := seed.Close(); closeErr != nil {
		t.Fatalf("close writable: %v", closeErr)
	}

	ro, roErr := sql.Open("sqlite", kindsDSN(path, false))
	if roErr != nil {
		t.Fatalf("open read-only: %v", roErr)
	}
	defer func() { _ = ro.Close() }()

	// Reads must work — the dry-run has to be able to build a plan.
	var v string
	if readErr := ro.QueryRowContext(ctx, `SELECT v FROM t WHERE id = 1`).Scan(&v); readErr != nil {
		t.Fatalf("read through read-only handle: %v", readErr)
	}
	if v != "before" {
		t.Fatalf("read %q, want \"before\"", v)
	}

	// Writes must be refused by the connection itself.
	if _, writeErr := ro.ExecContext(ctx, `UPDATE t SET v = 'after' WHERE id = 1`); writeErr == nil {
		t.Error("read-only handle accepted an UPDATE")
	} else if !strings.Contains(strings.ToLower(writeErr.Error()), "readonly") {
		t.Logf("write rejected (message: %v)", writeErr)
	}

	// And the value on disk is unchanged.
	check, checkErr := sql.Open("sqlite", kindsDSN(path, true))
	if checkErr != nil {
		t.Fatalf("reopen: %v", checkErr)
	}
	defer func() { _ = check.Close() }()
	if rereadErr := check.QueryRowContext(ctx, `SELECT v FROM t WHERE id = 1`).Scan(&v); rereadErr != nil {
		t.Fatalf("reread: %v", rereadErr)
	}
	if v != "before" {
		t.Errorf("value = %q, want \"before\" — the read-only handle mutated the store", v)
	}
}

// TestKindsDSN_ApplyHandleIsWritable is the control for the test above: the
// same helper must still yield a writable connection for --apply.
func TestKindsDSN_ApplyHandleIsWritable(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "probe.db")

	db, openErr := sql.Open("sqlite", kindsDSN(path, true))
	if openErr != nil {
		t.Fatalf("open writable: %v", openErr)
	}
	defer func() { _ = db.Close() }()

	if _, execErr := db.ExecContext(ctx, `CREATE TABLE t (id INTEGER PRIMARY KEY)`); execErr != nil {
		t.Fatalf("writable handle refused a write: %v", execErr)
	}
}
