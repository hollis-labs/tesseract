package contextstore

// Backup format.
//
// Format v1 was a single JSON file that hand-listed the tables it captured:
// records (7 of its 15 columns), audit_events and auth_tokens. Everything else
// — heads, record_tags, namespace_policies, embeddings, memory_state,
// memory_revisions plus its FTS5 index, pointer_verifications, schema_version —
// was silently absent, so a "successful" backup quietly dropped the entire
// typed-record layer and the whole memory/knowledge domain, which is this
// product's primary data. The three SELECTs also ran untransacted across pooled
// connections, so a concurrent writer produced a torn snapshot. See
// CW-20260904-0079.
//
// Format v2 is a directory:
//
//	<out>/
//	  manifest.json   format + schema version, per-file checksums, omissions
//	  main.db         whole-database snapshot (SQLite VACUUM INTO)
//	  records/        the payload tree
//	  config.yaml     when the caller supplies one
//
// Two properties are what make it a contract rather than another hand-list:
//
//   - Coverage is not enumerated by hand. VACUUM INTO copies every table the
//     database has, including tables a future migration adds, so the format
//     cannot drift behind the schema the way v1 did.
//   - Anything deliberately left out has to be declared. The manifest carries an
//     `omitted` list, and a restore refuses an omission it does not know how to
//     rebuild. Today the list is empty: VACUUM INTO copies whole tables anyway,
//     so keeping the derived data (embeddings, the FTS5 shadow tables) is
//     cheaper and safer than a deterministic rebuild.
//
// The manifest also records the schema version, which is what lets a restore
// refuse or gate on a mismatch instead of producing a hybrid store.

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/hollis-labs/tesseract/internal/fsperm"
	"github.com/hollis-labs/tesseract/internal/sqlitedsn"
)

const (
	// BackupFormatVersion is the on-disk backup format this build writes.
	BackupFormatVersion = 2

	backupManifestName = "manifest.json"
	backupDBName       = "main.db"
	backupRecordsName  = "records"
	backupConfigName   = "config.yaml"

	// A backup embeds auth_tokens.token_hash — the verifier for every issued
	// token — so it is owner-only. v1 wrote the same secrets at 0o644.
	backupDirMode  os.FileMode = 0o700
	backupFileMode os.FileMode = 0o600

	// Modes for material restored back into a live store. A restored tree must
	// be indistinguishable from a grown one, so these track what AppendRecord
	// writes rather than restating a policy: fsperm owns the answer.
	storeDirMode  os.FileMode = fsperm.DirMode
	storeFileMode os.FileMode = fsperm.FileMode
)

// BackupFile describes one file inside a v2 backup directory.
type BackupFile struct {
	// Path is slash-separated and relative to the backup directory.
	Path   string `json:"path"`
	Kind   string `json:"kind"` // db | record | config
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// BackupOmission names data deliberately excluded from a backup. Restore
// refuses any omission it cannot rebuild deterministically, so adding an entry
// here is a commitment to implement its rebuild.
type BackupOmission struct {
	Name    string `json:"name"`
	Reason  string `json:"reason"`
	Rebuild string `json:"rebuild,omitempty"`
}

// BackupManifest indexes a v2 backup directory.
type BackupManifest struct {
	FormatVersion int    `json:"format_version"`
	SchemaVersion int    `json:"schema_version"`
	CreatedAt     string `json:"created_at"`
	RecordCount   int64  `json:"record_count"`
	// Tables lists the tables present in main.db. It is documentation, not a
	// contract: the snapshot is whole-database, and this is what makes the
	// coverage of a given backup readable without opening the database.
	Tables   []string         `json:"tables"`
	Contents []BackupFile     `json:"contents"`
	Omitted  []BackupOmission `json:"omitted"`
	Checksum string           `json:"checksum"`
}

// ExportBackupOptions tunes what ExportBackup captures beyond the store itself.
type ExportBackupOptions struct {
	// ConfigPath, when non-empty and pointing at an existing file, is copied
	// into the backup as config.yaml. The store does not know where the
	// daemon's configuration lives — that is layout, not storage — so the
	// caller supplies it.
	ConfigPath string
}

// ExportBackup writes a format v2 backup directory at outPath.
func (s *Store) ExportBackup(ctx context.Context, outPath string) error {
	return s.ExportBackupWithOptions(ctx, outPath, ExportBackupOptions{})
}

// ExportBackupWithOptions writes a format v2 backup directory at outPath.
//
// outPath must not exist, or must be an empty directory: the manifest indexes
// exactly the files the export wrote, and a manifest that shares a directory
// with material it did not produce is not a manifest. A failed export removes
// the directory rather than leaving a partial one that looks restorable.
func (s *Store) ExportBackupWithOptions(ctx context.Context, outPath string, opts ExportBackupOptions) (err error) {
	if strings.TrimSpace(outPath) == "" {
		return errors.New("backup output path required")
	}
	outPath = filepath.Clean(outPath)

	if err = prepareBackupDir(outPath); err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(outPath)
		}
	}()

	schemaVer, err := currentSchemaVersion(ctx, s.db)
	if err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}

	// VACUUM INTO is the whole coverage guarantee in one statement. SQLite
	// writes a consistent point-in-time copy of every table from inside a read
	// transaction, so a concurrent writer cannot tear it and uncheckpointed WAL
	// content is folded in — neither of which the v1 exporter's three loose
	// SELECTs could claim. It also copies tables nobody remembered to list,
	// which is the exact drift that left twelve tables out of v1.
	dbDest := filepath.Join(outPath, backupDBName)
	if _, err = s.db.ExecContext(ctx, `VACUUM INTO ?`, dbDest); err != nil {
		return fmt.Errorf("snapshot database: %w", err)
	}
	if err = os.Chmod(dbDest, backupFileMode); err != nil {
		return err
	}

	// The payload tree is copied after the database snapshot on purpose.
	// Records are append-only, so a write landing between the two adds a file
	// the snapshot's index does not reference — an inert orphan. The reverse
	// order would index a payload the copy never saw, which is a dangling
	// record: a consistency fault in every restored store.
	recordFiles, err := copyTreeHashed(s.recordsDir, filepath.Join(outPath, backupRecordsName), backupRecordsName, "record", backupDirMode, backupFileMode)
	if err != nil {
		return fmt.Errorf("copy payload tree: %w", err)
	}

	contents := make([]BackupFile, 0, len(recordFiles)+2)
	dbSize, dbSum, err := hashFile(dbDest)
	if err != nil {
		return err
	}
	contents = append(contents, BackupFile{Path: backupDBName, Kind: "db", Size: dbSize, SHA256: dbSum})
	contents = append(contents, recordFiles...)

	if cfgPath := strings.TrimSpace(opts.ConfigPath); cfgPath != "" {
		if _, statErr := os.Stat(cfgPath); statErr == nil {
			dest := filepath.Join(outPath, backupConfigName)
			size, sum, cerr := copyFileHashed(cfgPath, dest, backupFileMode)
			if cerr != nil {
				return fmt.Errorf("copy config: %w", cerr)
			}
			contents = append(contents, BackupFile{Path: backupConfigName, Kind: "config", Size: size, SHA256: sum})
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return statErr
		}
	}

	sort.Slice(contents, func(i, j int) bool { return contents[i].Path < contents[j].Path })

	tables, err := snapshotTableNames(ctx, dbDest)
	if err != nil {
		return err
	}
	var recordCount int64
	if err = s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM records`).Scan(&recordCount); err != nil {
		return err
	}

	manifest := BackupManifest{
		FormatVersion: BackupFormatVersion,
		SchemaVersion: schemaVer,
		CreatedAt:     time.Now().UTC().Format(time.RFC3339),
		RecordCount:   recordCount,
		Tables:        tables,
		Contents:      contents,
		// Nothing is omitted. Declaring it explicitly (rather than leaving the
		// field absent) is the point of the field.
		Omitted: []BackupOmission{},
	}
	if manifest.Checksum, err = backupManifestChecksum(manifest); err != nil {
		return err
	}
	return writeBackupManifest(filepath.Join(outPath, backupManifestName), manifest)
}

// BackupInfo summarizes a backup that passed verification.
type BackupInfo struct {
	Path          string `json:"path"`
	FormatVersion int    `json:"format_version"`
	// SchemaVersion is 0 for v1 snapshots, which did not record one.
	SchemaVersion int    `json:"schema_version"`
	CreatedAt     string `json:"created_at"`
	Files         int    `json:"files"`
	RecordCount   int64  `json:"record_count"`
}

// VerifyBackup validates backup integrity without mutating any state. It
// accepts both a v2 backup directory and a legacy v1 snapshot file.
func (s *Store) VerifyBackup(inPath string) error {
	_, err := InspectBackup(inPath)
	return err
}

// InspectBackup verifies a backup and returns a summary of it.
func InspectBackup(inPath string) (BackupInfo, error) {
	b, err := loadBackup(inPath)
	if err != nil {
		return BackupInfo{}, err
	}
	return b.info(), nil
}

// loadedBackup is a verified backup, in whichever format it was written.
type loadedBackup struct {
	path     string
	format   int
	manifest BackupManifest        // format 2
	files    map[string]BackupFile // format 2, keyed by manifest path
	snapshot *backupSnapshot       // format 1
}

func (b *loadedBackup) info() BackupInfo {
	info := BackupInfo{Path: b.path, FormatVersion: b.format}
	switch b.format {
	case BackupFormatVersion:
		info.SchemaVersion = b.manifest.SchemaVersion
		info.CreatedAt = b.manifest.CreatedAt
		info.Files = len(b.manifest.Contents)
		info.RecordCount = b.manifest.RecordCount
	case 1:
		info.CreatedAt = b.snapshot.ExportedAt
		info.Files = 1
		info.RecordCount = int64(len(b.snapshot.Records))
	}
	return info
}

// loadBackup detects the backup format and verifies it end to end. A directory
// is v2; a regular file is a legacy v1 snapshot. Detection is by shape rather
// than by extension so an operator cannot get the wrong reader by renaming.
func loadBackup(inPath string) (*loadedBackup, error) {
	if strings.TrimSpace(inPath) == "" {
		return nil, errors.New("backup input path required")
	}
	inPath = filepath.Clean(inPath)
	fi, err := os.Stat(inPath)
	if err != nil {
		return nil, err
	}
	if fi.IsDir() {
		return loadBackupV2(inPath)
	}
	return loadBackupV1(inPath)
}

func loadBackupV2(dir string) (*loadedBackup, error) {
	manifestPath := filepath.Join(dir, backupManifestName)
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%s is not a backup: no %s", dir, backupManifestName)
		}
		return nil, err
	}
	var manifest BackupManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return nil, fmt.Errorf("parse %s: %w", backupManifestName, err)
	}
	if manifest.FormatVersion != BackupFormatVersion {
		return nil, fmt.Errorf("unsupported backup format version %d (this build reads 1 and %d)", manifest.FormatVersion, BackupFormatVersion)
	}
	if err := verifyBackupManifestChecksum(manifest); err != nil {
		return nil, err
	}
	// An omission this build cannot rebuild would restore as silent data loss,
	// which is the failure the format exists to prevent. Fail closed.
	for _, om := range manifest.Omitted {
		return nil, fmt.Errorf("backup omits %q (%s) and this build does not know how to rebuild it", om.Name, om.Reason)
	}

	files := make(map[string]BackupFile, len(manifest.Contents))
	for _, entry := range manifest.Contents {
		if err := validateBackupEntryPath(entry.Path); err != nil {
			return nil, err
		}
		if _, dup := files[entry.Path]; dup {
			return nil, fmt.Errorf("backup manifest lists %q twice", entry.Path)
		}
		files[entry.Path] = entry
	}
	if _, ok := files[backupDBName]; !ok {
		return nil, fmt.Errorf("backup manifest does not list %s", backupDBName)
	}

	// Every file present must be listed and every file listed must match. The
	// first direction catches a manifest that under-reports (the v1 failure in
	// miniature); the second catches tampering and bit rot.
	present := map[string]struct{}{}
	walkErr := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(dir, path)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		if rel == backupManifestName {
			return nil
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("backup contains a non-regular file: %s", rel)
		}
		present[rel] = struct{}{}
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	for rel := range present {
		if _, ok := files[rel]; !ok {
			return nil, fmt.Errorf("backup contains %q, which the manifest does not list", rel)
		}
	}
	for rel, entry := range files {
		if _, ok := present[rel]; !ok {
			return nil, fmt.Errorf("backup manifest lists %q, which is missing", rel)
		}
		size, sum, herr := hashFile(filepath.Join(dir, filepath.FromSlash(rel)))
		if herr != nil {
			return nil, herr
		}
		if size != entry.Size || sum != entry.SHA256 {
			return nil, fmt.Errorf("backup file %q does not match its manifest checksum", rel)
		}
	}

	if err := verifyBackupDB(dir, manifest, files); err != nil {
		return nil, err
	}
	return &loadedBackup{path: dir, format: BackupFormatVersion, manifest: manifest, files: files}, nil
}

// verifyBackupDB opens the snapshot read-only and checks it against the
// manifest: the database must be structurally sound, agree on the schema
// version, and every payload it indexes must be present with the checksum the
// index claims. This is the check that would have caught a v1 backup restoring
// into a store full of dangling records.
func verifyBackupDB(dir string, manifest BackupManifest, files map[string]BackupFile) error {
	dbPath := filepath.Join(dir, backupDBName)

	// Reading a WAL-mode database — even read-only — makes SQLite materialize a
	// -shm beside it. Our own snapshots are never in WAL mode (VACUUM INTO
	// writes a rollback-journal database), but a snapshot that arrived some
	// other way could be, and a stray sidecar would make the next verification
	// of the same backup fail as "a file the manifest does not list". Note
	// which sidecars existed before opening and drop only the ones we caused.
	preexisting := map[string]bool{}
	for _, suffix := range []string{"-wal", "-shm"} {
		if _, err := os.Lstat(dbPath + suffix); err == nil {
			preexisting[suffix] = true
		}
	}
	defer func() {
		for _, suffix := range []string{"-wal", "-shm"} {
			if !preexisting[suffix] {
				_ = os.Remove(dbPath + suffix)
			}
		}
	}()

	db, err := openBackupDB(dbPath)
	if err != nil {
		return err
	}
	defer db.Close()

	var integrity string
	if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil {
		return fmt.Errorf("backup database integrity check: %w", err)
	}
	if integrity != "ok" {
		return fmt.Errorf("backup database failed integrity check: %s", integrity)
	}

	ctx := context.Background()
	schemaVer, err := currentSchemaVersion(ctx, db)
	if err != nil {
		return fmt.Errorf("read backup schema version: %w", err)
	}
	if schemaVer != manifest.SchemaVersion {
		return fmt.Errorf("backup manifest declares schema version %d but the snapshot is at %d", manifest.SchemaVersion, schemaVer)
	}

	rows, err := db.QueryContext(ctx, `SELECT record_id, file_path, COALESCE(checksum, '') FROM records ORDER BY record_id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var recordID, filePath, checksum string
		if err := rows.Scan(&recordID, &filePath, &checksum); err != nil {
			return err
		}
		// A backup is untrusted input: its file paths become writes into the
		// live payload tree at restore time, so they are checked here, before
		// any of them is used to build a path.
		clean, err := sanitizePathPart(filePath)
		if err != nil {
			return fmt.Errorf("backup record %s has an unsafe payload path %q: %w", recordID, filePath, err)
		}
		rel := backupRecordsName + "/" + filepath.ToSlash(clean)
		entry, ok := files[rel]
		if !ok {
			return fmt.Errorf("backup record %s indexes payload %q, which the backup does not contain", recordID, filePath)
		}
		if checksum != "" && entry.SHA256 != checksum {
			return fmt.Errorf("backup record %s payload checksum disagrees with its index entry", recordID)
		}
	}
	return rows.Err()
}

// openBackupDB opens a snapshot strictly read-only, so verification cannot
// mutate — or create sidecar files beside — the backup it is inspecting.
func openBackupDB(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", sqlitedsn.DSN(path)+"&mode=ro")
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("open backup database: %w", err)
	}
	return db, nil
}

func currentSchemaVersion(ctx context.Context, db *sql.DB) (int, error) {
	var v int
	if err := db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_version`).Scan(&v); err != nil {
		return 0, err
	}
	return v, nil
}

func snapshotTableNames(ctx context.Context, dbPath string) ([]string, error) {
	db, err := openBackupDB(dbPath)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type IN ('table','view') AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	return out, rows.Err()
}

// ── format v1 (read-only) ───────────────────────────────────────────────────
//
// v1 is still read, so backups taken before v2 remain restorable. It is never
// written.

type backupRecord struct {
	RecordID  string          `json:"record_id"`
	Namespace string          `json:"namespace"`
	Key       string          `json:"key"`
	Revision  int64           `json:"revision"`
	Actor     string          `json:"actor"`
	CreatedAt string          `json:"created_at"`
	FilePath  string          `json:"file_path"`
	Payload   json.RawMessage `json:"payload"`
}

type backupAuthToken struct {
	TokenID        string `json:"token_id"`
	TokenHash      string `json:"token_hash"`
	Label          string `json:"label"`
	ClientID       string `json:"client_id,omitempty"`
	Scopes         string `json:"scopes,omitempty"`
	NamespaceGlobs string `json:"namespace_globs,omitempty"`
	CreatedAt      string `json:"created_at"`
	ExpiresAt      string `json:"expires_at,omitempty"`
	RevokedAt      string `json:"revoked_at,omitempty"`
}

type backupSnapshot struct {
	Version     int               `json:"version"`
	ExportedAt  string            `json:"exported_at"`
	Records     []backupRecord    `json:"records"`
	AuditEvents []AuditEvent      `json:"audit_events"`
	AuthTokens  []backupAuthToken `json:"auth_tokens"`
	Checksum    string            `json:"checksum"`
}

func loadBackupV1(path string) (*loadedBackup, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var snap backupSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, err
	}
	if err := verifyBackupChecksum(snap); err != nil {
		return nil, err
	}
	if snap.Version != 1 {
		return nil, fmt.Errorf("unsupported backup version %d", snap.Version)
	}
	// Same reasoning as the v2 path: reject traversal before any of these
	// becomes a write target.
	for _, rec := range snap.Records {
		if _, err := sanitizePathPart(rec.FilePath); err != nil {
			return nil, fmt.Errorf("backup record %s has an unsafe payload path %q: %w", rec.RecordID, rec.FilePath, err)
		}
	}
	return &loadedBackup{path: path, format: 1, snapshot: &snap}, nil
}

func verifyBackupChecksum(snap backupSnapshot) error {
	if strings.TrimSpace(snap.Checksum) == "" {
		return errors.New("backup checksum missing")
	}
	got, err := backupChecksum(snap)
	if err != nil {
		return err
	}
	if got != snap.Checksum {
		return errors.New("backup checksum mismatch")
	}
	return nil
}

func backupChecksum(snap backupSnapshot) (string, error) {
	tmp := snap
	tmp.Checksum = ""
	b, err := json.Marshal(tmp)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// ── manifest + file helpers ─────────────────────────────────────────────────

func backupManifestChecksum(m BackupManifest) (string, error) {
	tmp := m
	tmp.Checksum = ""
	b, err := json.Marshal(tmp)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

func verifyBackupManifestChecksum(m BackupManifest) error {
	if strings.TrimSpace(m.Checksum) == "" {
		return errors.New("backup manifest checksum missing")
	}
	got, err := backupManifestChecksum(m)
	if err != nil {
		return err
	}
	if got != m.Checksum {
		return errors.New("backup manifest checksum mismatch")
	}
	return nil
}

func writeBackupManifest(path string, m BackupManifest) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), backupFileMode)
}

// validateBackupEntryPath rejects any manifest path that could escape the
// backup directory when joined, or that names something the format does not
// define.
func validateBackupEntryPath(p string) error {
	if p == "" {
		return errors.New("backup manifest has an empty path entry")
	}
	if p != filepath.ToSlash(filepath.Clean(p)) || strings.HasPrefix(p, "/") || strings.HasPrefix(p, "../") || p == ".." {
		return fmt.Errorf("backup manifest has an unsafe path entry: %q", p)
	}
	for _, part := range strings.Split(p, "/") {
		if part == ".." {
			return fmt.Errorf("backup manifest has an unsafe path entry: %q", p)
		}
	}
	switch {
	case p == backupDBName, p == backupConfigName:
		return nil
	case strings.HasPrefix(p, backupRecordsName+"/"):
		return nil
	}
	return fmt.Errorf("backup manifest has an unexpected path entry: %q", p)
}

func prepareBackupDir(outPath string) error {
	entries, err := os.ReadDir(outPath)
	switch {
	case err == nil:
		if len(entries) > 0 {
			return fmt.Errorf("backup destination %s is not empty", outPath)
		}
	case errors.Is(err, os.ErrNotExist):
		// fresh destination
	default:
		if fi, ferr := os.Stat(outPath); ferr == nil && !fi.IsDir() {
			return fmt.Errorf("backup destination %s exists and is not a directory", outPath)
		}
		return err
	}
	if err := os.MkdirAll(outPath, backupDirMode); err != nil {
		return err
	}
	// MkdirAll asks for a mode; umask decides what it gets. A backup holds
	// token hashes, so the mode is asserted rather than requested.
	return os.Chmod(outPath, backupDirMode)
}

// copyTreeHashed copies srcRoot into dstRoot, hashing as it goes, and returns a
// manifest entry per file with paths prefixed by relPrefix. A missing srcRoot
// yields no entries; anything that is not a directory or a regular file is an
// error, because following a symlink out of the tree would leak whatever it
// points at into the backup.
func copyTreeHashed(srcRoot, dstRoot, relPrefix, kind string, dirMode, fileMode os.FileMode) ([]BackupFile, error) {
	if _, err := os.Stat(srcRoot); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	if err := os.MkdirAll(dstRoot, dirMode); err != nil {
		return nil, err
	}
	if err := os.Chmod(dstRoot, dirMode); err != nil {
		return nil, err
	}

	var out []BackupFile
	err := filepath.WalkDir(srcRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(srcRoot, path)
		if relErr != nil {
			return relErr
		}
		if rel == "." {
			return nil
		}
		dst := filepath.Join(dstRoot, rel)
		if d.IsDir() {
			if err := os.MkdirAll(dst, dirMode); err != nil {
				return err
			}
			return os.Chmod(dst, dirMode)
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("payload tree contains a non-regular file: %s", rel)
		}
		size, sum, cerr := copyFileHashed(path, dst, fileMode)
		if cerr != nil {
			return cerr
		}
		entry := BackupFile{Path: filepath.ToSlash(rel), Kind: kind, Size: size, SHA256: sum}
		if relPrefix != "" {
			entry.Path = relPrefix + "/" + entry.Path
		}
		out = append(out, entry)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

func copyFileHashed(src, dst string, mode os.FileMode) (int64, string, error) {
	in, err := os.Open(src)
	if err != nil {
		return 0, "", err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), backupDirMode); err != nil {
		return 0, "", err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return 0, "", err
	}
	h := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(out, h), in)
	if copyErr != nil {
		_ = out.Close()
		return 0, "", copyErr
	}
	// O_CREATE's mode is masked by umask; assert it.
	if err := out.Chmod(mode); err != nil {
		_ = out.Close()
		return 0, "", err
	}
	if err := out.Sync(); err != nil {
		_ = out.Close()
		return 0, "", err
	}
	if err := out.Close(); err != nil {
		return 0, "", err
	}
	return n, hex.EncodeToString(h.Sum(nil)), nil
}

func hashFile(path string) (int64, string, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, "", err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return 0, "", err
	}
	return n, hex.EncodeToString(h.Sum(nil)), nil
}
