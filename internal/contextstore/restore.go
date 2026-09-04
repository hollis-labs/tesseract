package contextstore

// Failure-atomic restore.
//
// The restore this replaces was destructive from its first statement: it wiped
// the payload tree with os.RemoveAll before opening a transaction, wrote files
// inside a transaction that could not roll them back, deleted records with
// foreign keys on (cascading every embedding row away and never restoring
// them), rebuilt heads in a separate transaction after the commit, and used
// file paths straight out of the backup without sanitizing them. Any failure
// anywhere left a store that was neither the old one nor the new one. See
// CW-20260904-0080.
//
// The replacement never mutates anything the live store depends on until the
// whole replacement exists beside it and has been verified:
//
//	1. validate — manifest, per-file checksums, schema version and every record
//	              path are checked with the live store untouched.
//	2. stage    — the replacement database and payload tree are materialized as
//	              <db>.restore-staging and <records>.restore-staging.
//	3. swap     — a journal is written, the handle is closed, the live copies
//	              are renamed aside and the staged copies renamed in.
//	4. commit   — the handle is reopened and the parked copies removed.
//
// A failure in 1 or 2 removes the staging area and returns; the store is
// unchanged and still open. Step 3 is the point of no return, and it is
// deliberately nothing but renames — so the window in which a crash can land
// mid-swap is a few syscalls wide, and a crash inside it is resolved by
// finishInterruptedRestore on the next Open.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	restoreStagingSuffix = ".restore-staging"
	restoreOldSuffix     = ".restore-old"
	restoreJournalSuffix = ".restore-journal"
	restoreBuildSuffix   = ".restore-build"

	restoreStateSwap = "swap"
)

// sqliteSidecarSuffixes are the files SQLite keeps beside a database. They must
// travel with it: a database renamed away from its WAL, or renamed in next to
// somebody else's, is corruption rather than a swap.
var sqliteSidecarSuffixes = []string{"-wal", "-shm"}

// restoreFaultHook is a test-only injection point, nil in production. It lives
// in the production file so the stage names sit next to the code that reaches
// them; the internal restore tests set it to fail a named stage and then assert
// the store came through intact.
var restoreFaultHook func(stage string) error

func restoreFault(stage string) error {
	if restoreFaultHook == nil {
		return nil
	}
	return restoreFaultHook(stage)
}

// restorePaths names every scratch path a restore uses. They are siblings of
// the live paths so that every move is a same-filesystem rename, which is the
// only reason the swap can be as close to atomic as a filesystem allows.
type restorePaths struct {
	dbPath      string
	recordsDir  string
	stagingDB   string
	buildDB     string
	stagingRecs string
	oldDB       string
	oldRecs     string
	journal     string
}

func newRestorePaths(dbPath, recordsDir string) restorePaths {
	return restorePaths{
		dbPath:      dbPath,
		recordsDir:  recordsDir,
		stagingDB:   dbPath + restoreStagingSuffix,
		buildDB:     dbPath + restoreBuildSuffix,
		stagingRecs: recordsDir + restoreStagingSuffix,
		oldDB:       dbPath + restoreOldSuffix,
		oldRecs:     recordsDir + restoreOldSuffix,
		journal:     dbPath + restoreJournalSuffix,
	}
}

// restoreJournal marks a swap as in flight. Its existence is the whole signal;
// the fields are there so a half-finished restore can be understood by a human
// and so recovery can refuse a journal that belongs to a different layout.
type restoreJournal struct {
	State      string `json:"state"`
	DBPath     string `json:"db_path"`
	RecordsDir string `json:"records_dir"`
	Source     string `json:"source"`
	StartedAt  string `json:"started_at"`
}

// RestoreBackup replaces this store's contents with a backup, atomically.
//
// It reads both formats: a v2 backup directory and a legacy v1 snapshot file.
//
// Restore is a replacement, not a merge — afterwards the store contains exactly
// what the backup contained, with none of the destination's previous rows left
// behind. Restoring a v1 snapshot therefore drops the memory and knowledge
// tables, because that format could not represent them; v1 backups are for
// recovering v1-era stores, not for topping up a current one.
//
// RestoreBackup replaces the *sql.DB behind Store.DB(): the file the previous
// handle was bound to is no longer the live database. Callers that cached
// Store.DB() (the memory subsystem does, at construction) must re-fetch it.
func (s *Store) RestoreBackup(ctx context.Context, inPath string) error {
	if strings.TrimSpace(inPath) == "" {
		return errors.New("backup input path required")
	}
	p := newRestorePaths(s.dbPath, s.recordsDir)

	// A journal here means a swap is still owed. Open resolves those, so one
	// surviving into a restore call means the process kept running past its own
	// interrupted swap; starting a second restore on top would bury it.
	if _, err := os.Stat(p.journal); err == nil {
		return fmt.Errorf("an interrupted restore is pending (%s); reopen the store to complete it before starting another", p.journal)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	// Anything else left lying around is from a restore that failed before it
	// wrote a journal, which makes it inert.
	if err := clearRestoreStaging(p); err != nil {
		return err
	}

	// ── 1. validate ────────────────────────────────────────────────────────
	if err := restoreFault("validate"); err != nil {
		return err
	}
	backup, err := loadBackup(inPath)
	if err != nil {
		return err
	}
	if backup.format == BackupFormatVersion && backup.manifest.SchemaVersion > schemaVersion {
		return fmt.Errorf("backup was written at schema version %d; this build supports up to %d — restore it with a newer tesseract", backup.manifest.SchemaVersion, schemaVersion)
	}

	// ── 2. stage ───────────────────────────────────────────────────────────
	if err := s.stageRestore(ctx, backup, p); err != nil {
		_ = clearRestoreStaging(p)
		return err
	}
	// The last point at which abandoning the restore costs nothing.
	if err := restoreFault("pre-swap"); err != nil {
		_ = clearRestoreStaging(p)
		return err
	}

	// ── 3/4. swap and commit ───────────────────────────────────────────────
	return s.swapInStagedRestore(ctx, p, inPath)
}

// stageRestore materializes the replacement database and payload tree beside
// the live ones.
func (s *Store) stageRestore(ctx context.Context, backup *loadedBackup, p restorePaths) error {
	if err := os.MkdirAll(filepath.Dir(p.dbPath), storeDirMode); err != nil {
		return err
	}
	switch backup.format {
	case BackupFormatVersion:
		return stageRestoreV2(ctx, backup, p)
	case 1:
		return stageRestoreV1(ctx, backup, p)
	default:
		return fmt.Errorf("unsupported backup format %d", backup.format)
	}
}

// stageRestoreV2 copies the snapshot and payload tree out of the backup,
// re-verifying every checksum on the way in. loadBackup already checked them;
// copying is the moment the bytes actually become this store's, so the second
// pass costs one read and closes the window between verify and use.
func stageRestoreV2(ctx context.Context, backup *loadedBackup, p restorePaths) error {
	if err := restoreFault("stage-db"); err != nil {
		return err
	}
	dbEntry := backup.files[backupDBName]
	size, sum, err := copyFileHashed(filepath.Join(backup.path, backupDBName), p.stagingDB, storeFileMode)
	if err != nil {
		return err
	}
	if size != dbEntry.Size || sum != dbEntry.SHA256 {
		return errors.New("backup database changed while it was being restored")
	}

	// An older backup is brought forward here, in staging, rather than after
	// the swap: a migration that fails while the store is already live would
	// leave the store on a schema the code cannot use, and there would be
	// nothing left to roll back to.
	if backup.manifest.SchemaVersion < schemaVersion {
		if err := migrateStagedDB(ctx, p); err != nil {
			return fmt.Errorf("migrate backup from schema version %d to %d: %w", backup.manifest.SchemaVersion, schemaVersion, err)
		}
	}

	if err := restoreFault("stage-records"); err != nil {
		return err
	}
	if err := os.MkdirAll(p.stagingRecs, storeDirMode); err != nil {
		return err
	}
	for rel, entry := range backup.files {
		if entry.Kind != "record" || !strings.HasPrefix(rel, backupRecordsName+"/") {
			continue
		}
		// validateBackupEntryPath has already rejected traversal; joining a
		// cleaned relative path onto the staging root is what keeps it inside.
		sub := strings.TrimPrefix(rel, backupRecordsName+"/")
		dst := filepath.Join(p.stagingRecs, filepath.FromSlash(sub))
		size, sum, err := copyFileHashed(filepath.Join(backup.path, filepath.FromSlash(rel)), dst, storeFileMode)
		if err != nil {
			return err
		}
		if size != entry.Size || sum != entry.SHA256 {
			return fmt.Errorf("backup file %q changed while it was being restored", rel)
		}
	}
	return nil
}

// stageRestoreV1 rebuilds a store from a legacy JSON snapshot. The staged
// database starts empty at the current schema, so the result is what the
// snapshot described and nothing else — the old restore instead deleted four
// tables out of a live database and left the rest, producing a store that was
// half backup and half destination.
func stageRestoreV1(ctx context.Context, backup *loadedBackup, p restorePaths) (err error) {
	snap := backup.snapshot

	if err = restoreFault("stage-records"); err != nil {
		return err
	}
	if err = os.MkdirAll(p.stagingRecs, storeDirMode); err != nil {
		return err
	}

	if err = restoreFault("stage-db"); err != nil {
		return err
	}
	// Built at a scratch path and then VACUUMed into the staging path, so what
	// the swap moves is a single self-contained file with no WAL beside it.
	if err = removeDBFiles(p.buildDB); err != nil {
		return err
	}
	db, err := openStoreDB(ctx, p.buildDB)
	if err != nil {
		return err
	}
	staging := &Store{db: db, recordsDir: p.stagingRecs, dbPath: p.buildDB}
	defer func() {
		_ = db.Close()
		_ = removeDBFiles(p.buildDB)
	}()
	if err = staging.migrate(ctx); err != nil {
		return err
	}

	if err = restoreFault("populate"); err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	for _, rec := range snap.Records {
		// loadBackupV1 rejected unsafe paths; re-derive the cleaned form here
		// so the value that reaches filepath.Join is the checked one.
		relPath, perr := sanitizePathPart(rec.FilePath)
		if perr != nil {
			err = fmt.Errorf("backup record %s has an unsafe payload path %q: %w", rec.RecordID, rec.FilePath, perr)
			return err
		}
		abs := filepath.Join(p.stagingRecs, relPath)
		if err = os.MkdirAll(filepath.Dir(abs), storeDirMode); err != nil {
			return err
		}
		var compact []byte
		if compact, err = compactJSON(rec.Payload); err != nil {
			err = fmt.Errorf("compact restored payload: %w", err)
			return err
		}
		if err = os.WriteFile(abs, compact, storeFileMode); err != nil {
			return err
		}
		sum := sha256.Sum256(compact)
		if _, err = tx.ExecContext(ctx, `
INSERT INTO records (record_id, namespace, key_name, revision, actor, created_at, checksum, file_path)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			rec.RecordID, rec.Namespace, rec.Key, rec.Revision, rec.Actor, rec.CreatedAt,
			hex.EncodeToString(sum[:]), relPath); err != nil {
			return err
		}
	}

	for _, ev := range snap.AuditEvents {
		if _, err = tx.ExecContext(ctx, `
INSERT INTO audit_events (id, event_type, actor, namespace, key_name, revision, record_id, created_at, metadata_json)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			ev.ID, ev.EventType, ev.Actor, ev.Namespace, ev.Key, ev.Revision, ev.RecordID, ev.CreatedAt, string(ev.Metadata)); err != nil {
			return err
		}
	}

	for _, tk := range snap.AuthTokens {
		scopes := tk.Scopes
		if scopes == "" {
			scopes = defaultTokenScopes
		}
		globs := tk.NamespaceGlobs
		if globs == "" {
			globs = defaultTokenNamespaceGlobs
		}
		if _, err = tx.ExecContext(ctx, `
INSERT INTO auth_tokens (token_id, token_hash, label, client_id, scopes, namespace_globs, created_at, expires_at, revoked_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			tk.TokenID, tk.TokenHash, tk.Label, tk.ClientID, scopes, globs,
			tk.CreatedAt, tk.ExpiresAt, tk.RevokedAt); err != nil {
			return err
		}
	}

	// Heads are derived, so they are rebuilt — but inside staging, before
	// anything is live. The old restore rebuilt them in a separate transaction
	// after the commit, so a failure in between left every head lookup
	// returning not-found until someone ran `context repair-heads` by hand.
	if _, err = tx.ExecContext(ctx, `
INSERT INTO heads (namespace, key_name, head_revision, head_record_id, updated_at)
SELECT r.namespace, r.key_name, r.revision, r.record_id, ?
FROM records r
JOIN (
	SELECT namespace, key_name, MAX(revision) AS max_revision
	FROM records
	GROUP BY namespace, key_name
) latest
ON latest.namespace = r.namespace AND latest.key_name = r.key_name AND latest.max_revision = r.revision
`, time.Now().UTC().Format(time.RFC3339)); err != nil {
		return err
	}

	if err = tx.Commit(); err != nil {
		return err
	}
	if err = os.Remove(p.stagingDB); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if _, err = db.ExecContext(ctx, `VACUUM INTO ?`, p.stagingDB); err != nil {
		return fmt.Errorf("seal staged database: %w", err)
	}
	return os.Chmod(p.stagingDB, storeFileMode)
}

// migrateStagedDB brings a staged snapshot up to the current schema and reseals
// it as a single file, so the swap still only has to move one path.
func migrateStagedDB(ctx context.Context, p restorePaths) error {
	if err := removeDBFiles(p.buildDB); err != nil {
		return err
	}
	if err := os.Rename(p.stagingDB, p.buildDB); err != nil {
		return err
	}
	db, err := openStoreDB(ctx, p.buildDB)
	if err != nil {
		return err
	}
	defer func() {
		_ = db.Close()
		_ = removeDBFiles(p.buildDB)
	}()
	staging := &Store{db: db, recordsDir: p.stagingRecs, dbPath: p.buildDB}
	if err := staging.migrate(ctx); err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, `VACUUM INTO ?`, p.stagingDB); err != nil {
		return fmt.Errorf("seal staged database: %w", err)
	}
	return os.Chmod(p.stagingDB, storeFileMode)
}

// swapInStagedRestore performs the point-of-no-return half of a restore.
func (s *Store) swapInStagedRestore(ctx context.Context, p restorePaths, source string) error {
	journal := restoreJournal{
		State:      restoreStateSwap,
		DBPath:     p.dbPath,
		RecordsDir: p.recordsDir,
		Source:     source,
		StartedAt:  time.Now().UTC().Format(time.RFC3339),
	}
	if err := writeRestoreJournal(p.journal, journal); err != nil {
		_ = clearRestoreStaging(p)
		return err
	}

	// From here the journal owns the outcome: whatever happens, the store is
	// either fully swapped or carries a marker telling the next Open to finish
	// the job.
	if err := s.db.Close(); err != nil {
		return fmt.Errorf("close database for restore swap: %w", err)
	}
	if err := finishInterruptedRestore(p.dbPath, p.recordsDir); err != nil {
		return err
	}

	db, err := openStoreDB(ctx, p.dbPath)
	if err != nil {
		return fmt.Errorf("reopen database after restore: %w", err)
	}
	s.db = db
	return nil
}

// finishInterruptedRestore completes a restore whose rename sequence was cut
// short — by a crash, a kill, or a fault injected in a test. Open calls it
// before touching anything, so a store is never served half-swapped, and the
// normal restore path calls the very same function: the recovery code is not a
// rarely-exercised second implementation, it is the swap.
//
// Recovery always rolls FORWARD, and that is what makes it deterministic rather
// than a judgement call. The journal is written only once the replacement
// database and payload tree exist in staging and have been checksum-verified,
// so at every instant the journal can be observed, the correct end state is
// "staging is live". Rolling back would mean choosing between two plausible
// states with no record of which half had already moved.
func finishInterruptedRestore(dbPath, recordsDir string) error {
	p := newRestorePaths(dbPath, recordsDir)
	raw, err := os.ReadFile(p.journal)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var journal restoreJournal
	if err := json.Unmarshal(raw, &journal); err != nil {
		return fmt.Errorf("restore journal %s is unreadable (%v); resolve it by hand before opening the store", p.journal, err)
	}
	if journal.DBPath != dbPath || journal.RecordsDir != recordsDir {
		return fmt.Errorf("restore journal %s describes a different layout (db %q, records %q); resolve it by hand", p.journal, journal.DBPath, journal.RecordsDir)
	}

	// Database first, then payloads — the same order every time, so a recovery
	// that is itself interrupted resumes on a step boundary.
	if err := adoptStaged(p.stagingDB, p.dbPath, p.oldDB, sqliteSidecarSuffixes); err != nil {
		return err
	}
	if err := restoreFault("swap-db-moved"); err != nil {
		return err
	}
	if err := adoptStaged(p.stagingRecs, p.recordsDir, p.oldRecs, nil); err != nil {
		return err
	}

	// Both halves are in place; the parked copies are now only ballast. The
	// journal goes last: while it exists, recovery is still owed.
	if err := removeRestoreParked(p); err != nil {
		return err
	}
	return os.Remove(p.journal)
}

// adoptStaged moves staged onto live, parking whatever is at live under old.
// sidecars are suffixes that must move with the primary path (SQLite's -wal and
// -shm). It is idempotent: a target already adopted is a no-op.
func adoptStaged(staged, live, old string, sidecars []string) error {
	if _, err := os.Lstat(staged); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		// Already adopted by an earlier attempt — provided live exists.
		if _, liveErr := os.Lstat(live); liveErr == nil {
			return nil
		}
		return fmt.Errorf("interrupted restore cannot be completed: neither %s nor %s exists", live, staged)
	}

	if _, err := os.Lstat(live); err == nil {
		if err := removePathAndSidecars(old, sidecars); err != nil {
			return err
		}
		if err := os.Rename(live, old); err != nil {
			return err
		}
		for _, suffix := range sidecars {
			if err := os.Rename(live+suffix, old+suffix); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	} else {
		// live is gone but its sidecars may not be; a fresh database must never
		// inherit a stale WAL.
		for _, suffix := range sidecars {
			if err := os.RemoveAll(live + suffix); err != nil {
				return err
			}
		}
	}
	return os.Rename(staged, live)
}

func writeRestoreJournal(path string, journal restoreJournal) error {
	data, err := json.MarshalIndent(journal, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, storeFileMode)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(data, '\n')); err != nil {
		_ = f.Close()
		return err
	}
	// The journal is only useful if it survives the crash it exists to
	// describe, so it is flushed before the first rename runs.
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// clearRestoreStaging removes staging and parked material but never the
// journal: a journal that outlives its scratch paths is unrecoverable.
func clearRestoreStaging(p restorePaths) error {
	if err := removePathAndSidecars(p.stagingDB, sqliteSidecarSuffixes); err != nil {
		return err
	}
	if err := removePathAndSidecars(p.buildDB, sqliteSidecarSuffixes); err != nil {
		return err
	}
	if err := os.RemoveAll(p.stagingRecs); err != nil {
		return err
	}
	return removeRestoreParked(p)
}

func removeRestoreParked(p restorePaths) error {
	if err := removePathAndSidecars(p.oldDB, sqliteSidecarSuffixes); err != nil {
		return err
	}
	return os.RemoveAll(p.oldRecs)
}

func removePathAndSidecars(path string, sidecars []string) error {
	if err := os.RemoveAll(path); err != nil {
		return err
	}
	for _, suffix := range sidecars {
		if err := os.RemoveAll(path + suffix); err != nil {
			return err
		}
	}
	return nil
}

func removeDBFiles(path string) error {
	return removePathAndSidecars(path, sqliteSidecarSuffixes)
}

func compactJSON(in []byte) ([]byte, error) {
	var buf bytes.Buffer
	if err := json.Compact(&buf, in); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
