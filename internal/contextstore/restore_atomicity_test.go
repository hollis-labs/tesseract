package contextstore

// Failure-atomicity coverage for RestoreBackup (CW-20260904-0080).
//
// These live inside the package because they drive restoreFaultHook, the
// injection point that makes a named stage fail. The property under test is
// always the same one: a restore that fails must leave the store exactly as it
// was, still open and still usable — and a restore that is interrupted after
// the point of no return must resolve deterministically on the next Open.

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newRestoreTestStore(t *testing.T, root string) *Store {
	t.Helper()
	s, err := Open(context.Background(), Config{RootDir: root})
	if err != nil {
		t.Fatalf("open store at %s: %v", root, err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// seedRestoreFixture writes a couple of records plus an audit event and returns
// the store.
func seedRestoreFixture(t *testing.T, s *Store, ns, key string, revisions int) {
	t.Helper()
	ctx := context.Background()
	for i := 1; i <= revisions; i++ {
		rec, err := s.AppendRecord(ctx, AppendInput{
			Namespace:  ns,
			Key:        key,
			Actor:      "test",
			Payload:    json.RawMessage([]byte(`{"i":` + string(rune('0'+i)) + `}`)),
			Metadata:   json.RawMessage([]byte(`{"tags":["t` + string(rune('0'+i)) + `"]}`)),
			RecordType: "task/spec",
			Status:     "draft",
		})
		if err != nil {
			t.Fatalf("append: %v", err)
		}
		if err := s.EmitWrite(ctx, rec.Actor, rec.Namespace, rec.Key, rec.Revision, rec.RecordID, nil); err != nil {
			t.Fatalf("emit: %v", err)
		}
	}
}

// storeFingerprint captures enough of a store's observable state that any of
// the old restore's partial failures would change it.
type storeFingerprint struct {
	Records   []string
	Heads     []string
	Audit     int
	Tokens    int
	Payloads  []string
	Embedding int
}

func fingerprintStore(t *testing.T, s *Store) storeFingerprint {
	t.Helper()
	ctx := context.Background()
	fp := storeFingerprint{}

	collect := func(query string) []string {
		rows, err := s.db.QueryContext(ctx, query)
		if err != nil {
			t.Fatalf("fingerprint query %q: %v", query, err)
		}
		defer rows.Close()
		var out []string
		for rows.Next() {
			var v string
			if err := rows.Scan(&v); err != nil {
				t.Fatalf("fingerprint scan: %v", err)
			}
			out = append(out, v)
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("fingerprint rows: %v", err)
		}
		return out
	}
	fp.Records = collect(`SELECT record_id || '|' || namespace || '|' || key_name || '|' || revision || '|' || COALESCE(checksum,'') || '|' || COALESCE(record_type,'') FROM records ORDER BY record_id`)
	fp.Heads = collect(`SELECT namespace || '|' || key_name || '|' || head_revision FROM heads ORDER BY namespace, key_name`)

	count := func(query string) int {
		var n int
		if err := s.db.QueryRowContext(ctx, query).Scan(&n); err != nil {
			t.Fatalf("fingerprint count %q: %v", query, err)
		}
		return n
	}
	fp.Audit = count(`SELECT COUNT(1) FROM audit_events`)
	fp.Tokens = count(`SELECT COUNT(1) FROM auth_tokens`)
	fp.Embedding = count(`SELECT COUNT(1) FROM embeddings`)

	err := filepath.Walk(s.recordsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(s.recordsDir, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		fp.Payloads = append(fp.Payloads, rel+"="+string(data))
		return nil
	})
	if err != nil {
		t.Fatalf("fingerprint payloads: %v", err)
	}
	return fp
}

func (fp storeFingerprint) String() string {
	b, _ := json.Marshal(fp)
	return string(b)
}

// TestRestoreFaultAtEveryStageLeavesTheStoreIntact injects a failure at each
// pre-commit stage and requires the destination store to be byte-for-byte the
// store it was, still open, with no scratch material left behind.
func TestRestoreFaultAtEveryStageLeavesTheStoreIntact(t *testing.T) {
	ctx := context.Background()

	src := newRestoreTestStore(t, t.TempDir())
	seedRestoreFixture(t, src, "app/src/ns", "k", 2)
	backupDir := filepath.Join(t.TempDir(), "backup")
	if err := src.ExportBackup(ctx, backupDir); err != nil {
		t.Fatalf("export: %v", err)
	}
	v1Path := filepath.Join(t.TempDir(), "legacy.json")
	writeV1Fixture(t, v1Path)

	// "populate" only exists on the v1 path — a v2 restore has nothing to
	// populate, it copies a finished database.
	cases := []struct {
		stage  string
		source string
	}{
		{stage: "validate", source: backupDir},
		{stage: "stage-db", source: backupDir},
		{stage: "stage-records", source: backupDir},
		{stage: "pre-swap", source: backupDir},
		{stage: "populate", source: v1Path},
	}

	for _, tc := range cases {
		t.Run(tc.stage, func(t *testing.T) {
			root := t.TempDir()
			dst := newRestoreTestStore(t, root)
			seedRestoreFixture(t, dst, "app/dst/ns", "own", 3)
			before := fingerprintStore(t, dst)

			restoreFaultHook = func(stage string) error {
				if stage == tc.stage {
					return errInjected
				}
				return nil
			}
			t.Cleanup(func() { restoreFaultHook = nil })

			err := dst.RestoreBackup(ctx, tc.source)
			if err == nil {
				t.Fatalf("restore should have failed at stage %q", tc.stage)
			}
			if !strings.Contains(err.Error(), "injected") {
				t.Fatalf("restore failed for the wrong reason: %v", err)
			}
			restoreFaultHook = nil

			// Still open on the original handle, still serving reads.
			if after := fingerprintStore(t, dst); after.String() != before.String() {
				t.Fatalf("store changed under a failed restore:\nbefore=%s\nafter =%s", before, after)
			}
			if _, err := dst.Head(ctx, "app/dst/ns", "own"); err != nil {
				t.Fatalf("store unusable after a failed restore: %v", err)
			}
			// Still writable: the handle was never closed.
			if _, err := dst.AppendRecord(ctx, AppendInput{
				Namespace: "app/dst/ns", Key: "own", Actor: "test",
				Payload: json.RawMessage([]byte(`{"after":true}`)),
			}); err != nil {
				t.Fatalf("store not writable after a failed restore: %v", err)
			}
			assertNoRestoreScratch(t, dst)
		})
	}
}

// TestRestoreInterruptedMidSwapIsResolvedOnNextOpen covers the one window a
// restore cannot roll back from. A crash between the two renames must not leave
// an ambiguous store: the journal records that a swap is owed, and the next
// Open rolls it forward to the verified replacement.
func TestRestoreInterruptedMidSwapIsResolvedOnNextOpen(t *testing.T) {
	ctx := context.Background()

	src := newRestoreTestStore(t, t.TempDir())
	seedRestoreFixture(t, src, "app/src/ns", "k", 2)
	backupDir := filepath.Join(t.TempDir(), "backup")
	if err := src.ExportBackup(ctx, backupDir); err != nil {
		t.Fatalf("export: %v", err)
	}
	want := fingerprintStore(t, src)

	root := t.TempDir()
	dst := newRestoreTestStore(t, root)
	seedRestoreFixture(t, dst, "app/dst/ns", "own", 1)

	restoreFaultHook = func(stage string) error {
		if stage == "swap-db-moved" {
			return errInjected
		}
		return nil
	}
	if err := dst.RestoreBackup(ctx, backupDir); err == nil {
		t.Fatal("restore should have failed mid-swap")
	}
	restoreFaultHook = nil
	_ = dst.Close()

	// The journal is the marker that recovery is owed.
	journal := filepath.Join(root, "data", "index", "context.db"+restoreJournalSuffix)
	if _, err := os.Stat(journal); err != nil {
		t.Fatalf("an interrupted swap left no journal: %v", err)
	}

	// Reopening is the recovery: no repair command, no operator decision.
	reopened := newRestoreTestStore(t, root)
	if _, err := os.Stat(journal); err == nil {
		t.Fatal("journal survived recovery")
	}
	if got := fingerprintStore(t, reopened); got.String() != want.String() {
		t.Fatalf("recovery did not land on the backup:\nwant=%s\ngot =%s", want, got)
	}
	if _, err := reopened.Head(ctx, "app/src/ns", "k"); err != nil {
		t.Fatalf("recovered store cannot serve the restored data: %v", err)
	}
	assertNoRestoreScratch(t, reopened)
}

// TestRestoreRefusesWhileAnInterruptedRestoreIsPending guards the other order:
// a second restore must not bury a swap that is still owed.
func TestRestoreRefusesWhileAnInterruptedRestoreIsPending(t *testing.T) {
	ctx := context.Background()
	src := newRestoreTestStore(t, t.TempDir())
	seedRestoreFixture(t, src, "app/src/ns", "k", 1)
	backupDir := filepath.Join(t.TempDir(), "backup")
	if err := src.ExportBackup(ctx, backupDir); err != nil {
		t.Fatalf("export: %v", err)
	}

	root := t.TempDir()
	dst := newRestoreTestStore(t, root)
	seedRestoreFixture(t, dst, "app/dst/ns", "own", 1)

	if err := writeRestoreJournal(dst.dbPath+restoreJournalSuffix, restoreJournal{
		State: restoreStateSwap, DBPath: dst.dbPath, RecordsDir: dst.recordsDir,
		Source: backupDir, StartedAt: time.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("write journal: %v", err)
	}
	err := dst.RestoreBackup(ctx, backupDir)
	if err == nil || !strings.Contains(err.Error(), "interrupted restore is pending") {
		t.Fatalf("expected a pending-restore refusal, got %v", err)
	}
	_ = os.Remove(dst.dbPath + restoreJournalSuffix)
}

// TestRestoreRefusesNewerSchemaVersion covers the gate that stops a restore
// from producing a store the running build cannot read. The old restore did not
// look at schema versions at all.
func TestRestoreRefusesNewerSchemaVersion(t *testing.T) {
	ctx := context.Background()
	src := newRestoreTestStore(t, t.TempDir())
	seedRestoreFixture(t, src, "app/src/ns", "k", 1)
	backupDir := filepath.Join(t.TempDir(), "backup")
	if err := src.ExportBackup(ctx, backupDir); err != nil {
		t.Fatalf("export: %v", err)
	}
	restampBackupSchemaVersion(t, backupDir, schemaVersion+1)

	// It has to be a coherent backup, or the test would be proving that the
	// checksum check works rather than that the version gate does.
	if err := src.VerifyBackup(backupDir); err != nil {
		t.Fatalf("restamped backup should still verify: %v", err)
	}

	root := t.TempDir()
	dst := newRestoreTestStore(t, root)
	seedRestoreFixture(t, dst, "app/dst/ns", "own", 1)
	before := fingerprintStore(t, dst)

	err := dst.RestoreBackup(ctx, backupDir)
	if err == nil {
		t.Fatal("expected a schema-version refusal")
	}
	if !strings.Contains(err.Error(), "schema version") {
		t.Fatalf("error does not name the schema version: %v", err)
	}
	if after := fingerprintStore(t, dst); after.String() != before.String() {
		t.Fatalf("a refused restore changed the store:\nbefore=%s\nafter =%s", before, after)
	}
	assertNoRestoreScratch(t, dst)
}

// TestRestoreRejectsTraversingRecordPath is the hostile-backup case: a record
// whose file_path climbs out of the payload tree. The old restore joined that
// path onto recordsDir and wrote wherever it pointed.
func TestRestoreRejectsTraversingRecordPath(t *testing.T) {
	ctx := context.Background()
	src := newRestoreTestStore(t, t.TempDir())
	seedRestoreFixture(t, src, "app/src/ns", "k", 1)
	backupDir := filepath.Join(t.TempDir(), "backup")
	if err := src.ExportBackup(ctx, backupDir); err != nil {
		t.Fatalf("export: %v", err)
	}
	rewriteBackupDB(t, backupDir, func(db *sql.DB) {
		if _, err := db.Exec(`UPDATE records SET file_path = '../../../escaped.json'`); err != nil {
			t.Fatalf("rewrite file_path: %v", err)
		}
	})

	if err := src.VerifyBackup(backupDir); err == nil {
		t.Fatal("verification accepted a traversing record path")
	} else if !strings.Contains(err.Error(), "unsafe payload path") {
		t.Fatalf("verification rejected the hostile backup for the wrong reason: %v", err)
	}

	root := t.TempDir()
	dst := newRestoreTestStore(t, root)
	seedRestoreFixture(t, dst, "app/dst/ns", "own", 1)
	before := fingerprintStore(t, dst)

	if err := dst.RestoreBackup(ctx, backupDir); err == nil {
		t.Fatal("restore accepted a traversing record path")
	} else if !strings.Contains(err.Error(), "unsafe payload path") {
		t.Fatalf("restore rejected the hostile backup for the wrong reason: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "escaped.json")); err == nil {
		t.Fatal("restore wrote outside the records directory")
	}
	if after := fingerprintStore(t, dst); after.String() != before.String() {
		t.Fatalf("a rejected restore changed the store:\nbefore=%s\nafter =%s", before, after)
	}
	assertNoRestoreScratch(t, dst)
}

// TestRestoreRejectsTraversingManifestPath covers the same attack expressed in
// the manifest instead of the database.
func TestRestoreRejectsTraversingManifestPath(t *testing.T) {
	ctx := context.Background()
	src := newRestoreTestStore(t, t.TempDir())
	seedRestoreFixture(t, src, "app/src/ns", "k", 1)
	backupDir := filepath.Join(t.TempDir(), "backup")
	if err := src.ExportBackup(ctx, backupDir); err != nil {
		t.Fatalf("export: %v", err)
	}

	manifest := readTestManifest(t, backupDir)
	for i := range manifest.Contents {
		if manifest.Contents[i].Kind == "record" {
			manifest.Contents[i].Path = "records/../../escaped.json"
			break
		}
	}
	writeTestManifest(t, backupDir, manifest)

	if err := src.VerifyBackup(backupDir); err == nil {
		t.Fatal("verification accepted a traversing manifest path")
	} else if !strings.Contains(err.Error(), "unsafe path entry") {
		t.Fatalf("verification rejected the hostile manifest for the wrong reason: %v", err)
	}
	dst := newRestoreTestStore(t, t.TempDir())
	if err := dst.RestoreBackup(ctx, backupDir); err == nil {
		t.Fatal("restore accepted a traversing manifest path")
	} else if !strings.Contains(err.Error(), "unsafe path entry") {
		t.Fatalf("restore rejected the hostile manifest for the wrong reason: %v", err)
	}
}

// TestRestoreDeclaredOmissionIsRefused pins the manifest's other half: data may
// be left out of a backup only if this build knows how to rebuild it, and
// nothing is rebuildable today.
func TestRestoreDeclaredOmissionIsRefused(t *testing.T) {
	ctx := context.Background()
	src := newRestoreTestStore(t, t.TempDir())
	seedRestoreFixture(t, src, "app/src/ns", "k", 1)
	backupDir := filepath.Join(t.TempDir(), "backup")
	if err := src.ExportBackup(ctx, backupDir); err != nil {
		t.Fatalf("export: %v", err)
	}

	manifest := readTestManifest(t, backupDir)
	manifest.Omitted = []BackupOmission{{Name: "embeddings", Reason: "size"}}
	writeTestManifest(t, backupDir, manifest)

	if err := src.VerifyBackup(backupDir); err == nil {
		t.Fatal("verification accepted an omission this build cannot rebuild")
	} else if !strings.Contains(err.Error(), "does not know how to rebuild") {
		t.Fatalf("verification rejected the omission for the wrong reason: %v", err)
	}
	dst := newRestoreTestStore(t, t.TempDir())
	if err := dst.RestoreBackup(ctx, backupDir); err == nil {
		t.Fatal("restore accepted an omission this build cannot rebuild")
	}
}

// TestExportRefusesNonEmptyDestination keeps the manifest honest: it indexes
// exactly what the export wrote.
func TestExportRefusesNonEmptyDestination(t *testing.T) {
	ctx := context.Background()
	s := newRestoreTestStore(t, t.TempDir())
	dir := filepath.Join(t.TempDir(), "backup")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "stray.txt"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write stray: %v", err)
	}
	if err := s.ExportBackup(ctx, dir); err == nil {
		t.Fatal("export accepted a non-empty destination")
	}
}

// ── fixtures ────────────────────────────────────────────────────────────────

type injectedError struct{}

func (injectedError) Error() string { return "injected restore fault" }

var errInjected = injectedError{}

func assertNoRestoreScratch(t *testing.T, s *Store) {
	t.Helper()
	p := newRestorePaths(s.dbPath, s.recordsDir)
	for _, path := range []string{p.stagingDB, p.buildDB, p.stagingRecs, p.oldDB, p.oldRecs, p.journal} {
		if _, err := os.Lstat(path); err == nil {
			t.Errorf("restore scratch left behind: %s", path)
		}
	}
}

func readTestManifest(t *testing.T, dir string) BackupManifest {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, backupManifestName))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var m BackupManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("parse manifest: %v", err)
	}
	return m
}

// writeTestManifest re-signs the manifest, so a test that mutates it is testing
// the rule it changed rather than the checksum.
func writeTestManifest(t *testing.T, dir string, m BackupManifest) {
	t.Helper()
	sum, err := backupManifestChecksum(m)
	if err != nil {
		t.Fatalf("checksum manifest: %v", err)
	}
	m.Checksum = sum
	if err := writeBackupManifest(filepath.Join(dir, backupManifestName), m); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
}

// rewriteBackupDB mutates a backup's snapshot and re-signs the manifest so the
// result is a coherent — if hostile — backup.
func rewriteBackupDB(t *testing.T, dir string, mutate func(*sql.DB)) {
	t.Helper()
	dbPath := filepath.Join(dir, backupDBName)
	if err := os.Chmod(dbPath, 0o600); err != nil {
		t.Fatalf("chmod snapshot: %v", err)
	}
	db, err := openStoreDB(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open snapshot: %v", err)
	}
	mutate(db)
	// Put the file back into the shape VACUUM INTO produces: a rollback-journal
	// database with no sidecars, so the fixture is a realistic backup.
	if _, err := db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		t.Fatalf("checkpoint: %v", err)
	}
	var mode string
	if err := db.QueryRow(`PRAGMA journal_mode=DELETE`).Scan(&mode); err != nil {
		t.Fatalf("reset journal mode: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close snapshot: %v", err)
	}
	for _, suffix := range sqliteSidecarSuffixes {
		_ = os.Remove(dbPath + suffix)
	}

	manifest := readTestManifest(t, dir)
	size, sum, err := hashFile(dbPath)
	if err != nil {
		t.Fatalf("hash snapshot: %v", err)
	}
	for i := range manifest.Contents {
		if manifest.Contents[i].Path == backupDBName {
			manifest.Contents[i].Size = size
			manifest.Contents[i].SHA256 = sum
		}
	}
	writeTestManifest(t, dir, manifest)
}

// restampBackupSchemaVersion makes a backup claim a schema version, in both the
// snapshot and the manifest, so the version gate is what rejects it.
func restampBackupSchemaVersion(t *testing.T, dir string, version int) {
	t.Helper()
	rewriteBackupDB(t, dir, func(db *sql.DB) {
		if _, err := db.Exec(`INSERT INTO schema_version (version, applied_at) VALUES (?, ?)`,
			version, time.Now().UTC().Format(time.RFC3339)); err != nil {
			t.Fatalf("stamp schema version: %v", err)
		}
	})
	manifest := readTestManifest(t, dir)
	manifest.SchemaVersion = version
	writeTestManifest(t, dir, manifest)
}

// writeV1Fixture emits a minimal legacy snapshot for the v1 restore path.
func writeV1Fixture(t *testing.T, path string) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339)
	snap := backupSnapshot{
		Version:    1,
		ExportedAt: now,
		Records: []backupRecord{
			{RecordID: "rec_v1", Namespace: "app/legacy/ns", Key: "k", Revision: 1, Actor: "test",
				CreatedAt: now, FilePath: filepath.Join("app/legacy/ns", "k", "1.json"),
				Payload: json.RawMessage(`{"legacy":true}`)},
		},
	}
	sum, err := backupChecksum(snap)
	if err != nil {
		t.Fatalf("checksum v1: %v", err)
	}
	snap.Checksum = sum
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		t.Fatalf("marshal v1: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write v1: %v", err)
	}
}
