package contextstore

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/tesseract/internal/fsperm"
)

// Permission coverage for CW-20260904-0078. Every assertion here is against a
// mode the store set with an explicit Chmod, so the caller's umask cannot
// change the answer; requirePermTestable skips when the ambient umask is tight
// enough that an untightened path would look tightened anyway.
func requirePermTestable(t *testing.T) {
	t.Helper()
	if !fsperm.Supported {
		t.Skip("POSIX file modes are not meaningful on this platform")
	}
	probe := filepath.Join(t.TempDir(), "probe")
	if err := os.Mkdir(probe, 0o755); err != nil { //nolint:gosec // the probe exists to observe the ambient umask, so it must be loose
		t.Fatalf("probe mkdir: %v", err)
	}
	info, err := os.Stat(probe)
	if err != nil {
		t.Fatalf("probe stat: %v", err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Skipf("ambient umask masks loose modes (probe dir is %04o, not 0755)", info.Mode().Perm())
	}
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s has mode %04o, want %04o", path, got, want)
	}
}

func TestOpenCreatesAnOwnerOnlyStore(t *testing.T) {
	requirePermTestable(t)
	root := t.TempDir()
	recordsDir := filepath.Join(root, "data", "records")
	dbPath := filepath.Join(root, "data", "index", "context.db")

	s, err := Open(context.Background(), Config{RootDir: root})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = s.Close() }()

	assertMode(t, recordsDir, fsperm.DirMode)
	assertMode(t, filepath.Dir(dbPath), fsperm.DirMode)
	// The SQLite driver creates the database, so its mode can only be fixed
	// after the fact — that correction is what this asserts.
	assertMode(t, dbPath, fsperm.FileMode)

	if _, err := s.AppendRecord(context.Background(), AppendInput{
		Namespace: "app/perm",
		Key:       "summary",
		Actor:     "app:test",
		Payload:   json.RawMessage(`{"n":1}`),
	}); err != nil {
		t.Fatalf("append record: %v", err)
	}
	assertMode(t, filepath.Join(recordsDir, "app"), fsperm.DirMode)
	assertMode(t, filepath.Join(recordsDir, "app", "perm", "summary"), fsperm.DirMode)
	assertMode(t, filepath.Join(recordsDir, "app", "perm", "summary", "1.json"), fsperm.FileMode)
}

// An install created before CW-20260904-0078 has a 0755/0644 store on disk and
// nothing else will ever fix it, so Open converts what it finds.
func TestOpenTightensAWorldReadableStoreItInherits(t *testing.T) {
	requirePermTestable(t)
	root := t.TempDir()
	recordsDir := filepath.Join(root, "data", "records")
	dbPath := filepath.Join(root, "data", "index", "context.db")

	s, err := Open(context.Background(), Config{RootDir: root})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	for i := 0; i < 2; i++ {
		if _, err = s.AppendRecord(context.Background(), AppendInput{
			Namespace: "app/legacy",
			Key:       "summary",
			Actor:     "app:test",
			Payload:   json.RawMessage(`{"n":1}`),
		}); err != nil {
			t.Fatalf("append record %d: %v", i, err)
		}
	}
	if err = s.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	// Rewind the whole layout to what an older build left behind.
	loosen(t, recordsDir)
	loosen(t, filepath.Dir(dbPath))
	assertMode(t, filepath.Join(recordsDir, "app", "legacy", "summary", "1.json"), 0o644)

	s2, err := Open(context.Background(), Config{RootDir: root})
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer func() { _ = s2.Close() }()

	assertMode(t, recordsDir, fsperm.DirMode)
	assertMode(t, filepath.Join(recordsDir, "app"), fsperm.DirMode)
	assertMode(t, filepath.Join(recordsDir, "app", "legacy", "summary"), fsperm.DirMode)
	// The nested payloads are the point: the records tree is Tesseract-owned
	// end to end, so it is the one tree Open is allowed to walk.
	assertMode(t, filepath.Join(recordsDir, "app", "legacy", "summary", "1.json"), fsperm.FileMode)
	assertMode(t, filepath.Join(recordsDir, "app", "legacy", "summary", "2.json"), fsperm.FileMode)
	assertMode(t, filepath.Dir(dbPath), fsperm.DirMode)
	assertMode(t, dbPath, fsperm.FileMode)
}

// The database's directory is a path the embedding caller chose via DBPath, so
// Open tightens the directory it created and the database files by name, but
// never walks it: a caller may have pointed DBPath at a directory holding
// files that are not Tesseract's.
func TestOpenDoesNotWalkTheDatabaseDirectory(t *testing.T) {
	requirePermTestable(t)
	root := t.TempDir()
	dbDir := filepath.Join(root, "shared")
	if err := os.MkdirAll(dbDir, 0o755); err != nil { //nolint:gosec // deliberately 0755: an operator-supplied path Tesseract must not tighten
		t.Fatalf("mkdir: %v", err)
	}
	neighbor := filepath.Join(dbDir, "someone-elses.txt")
	if err := os.WriteFile(neighbor, []byte("not ours"), 0o644); err != nil { //nolint:gosec // deliberately 0755: an operator-supplied path Tesseract must not tighten
		t.Fatalf("write neighbor: %v", err)
	}
	if err := os.Chmod(neighbor, 0o644); err != nil { //nolint:gosec // deliberately 0755/0644: a path Tesseract must not tighten
		t.Fatalf("chmod neighbor: %v", err)
	}

	s, err := Open(context.Background(), Config{
		RootDir:    root,
		RecordsDir: filepath.Join(root, "records"),
		DBPath:     filepath.Join(dbDir, "context.db"),
	})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = s.Close() }()

	assertMode(t, neighbor, 0o644)
}

// loosen puts a tree back to the world-readable modes an older build produced.
func loosen(t *testing.T, root string) {
	t.Helper()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return os.Chmod(path, 0o755) //nolint:gosec // deliberately the pre-hardening 0755/0644; the test asserts it gets tightened
		}
		return os.Chmod(path, 0o644) //nolint:gosec // deliberately the pre-hardening 0755/0644; the test asserts it gets tightened
	})
	if err != nil {
		t.Fatalf("loosen %s: %v", root, err)
	}
}
