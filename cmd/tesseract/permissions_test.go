package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/tesseract/internal/config"
	"github.com/hollis-labs/tesseract/internal/contextstore"
	"github.com/hollis-labs/tesseract/internal/fsperm"
)

// go-apppaths materializes the XDG roots at 0755 and cannot be told otherwise,
// so the daemon tightens the Tesseract-owned paths after resolution. This is
// the end-to-end check that it actually happens on the real layout rather than
// only in the store's own tests. CW-20260904-0078.
//
// The assertions are against modes the daemon set with an explicit Chmod, so
// the caller's umask cannot change them; the probe skips when the ambient
// umask is tight enough that an untightened path would look tightened.
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

func assertPerm(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s has mode %04o, want %04o", path, got, want)
	}
}

func TestRuntimeStateIsOwnerOnly(t *testing.T) {
	requirePermTestable(t)
	layout := hermeticLayout(t)

	// ResolveLayout already tightened the roots go-apppaths materializes, so
	// loosen StateDir back to what an install created by an older build looks
	// like. That is the case worth pinning: the assertion below is then about
	// the runtime re-tightening a legacy path, not about the umask, and not
	// about a directory that was already correct when it was created.
	if err := os.Chmod(layout.StateDir(), 0o755); err != nil { //nolint:gosec // models a pre-policy install, so it must be loose
		t.Fatalf("loosen state dir: %v", err)
	}
	assertPerm(t, layout.StateDir(), 0o755)

	recordsDir := filepath.Join(layout.StateDir(), "records")
	store, err := contextstore.Open(context.Background(), contextstore.Config{
		RootDir:    layout.DataDir(),
		RecordsDir: recordsDir,
		DBPath:     layout.MainDB(),
	})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	assertPerm(t, recordsDir, fsperm.DirMode)
	assertPerm(t, filepath.Dir(layout.MainDB()), fsperm.DirMode)
	assertPerm(t, layout.MainDB(), fsperm.FileMode)

	mem, err := setupMemorySubsystem(context.Background(), store, nil, layout, config.Defaults())
	if err != nil {
		t.Fatalf("setup memory subsystem: %v", err)
	}
	t.Cleanup(func() { _ = mem.Close() })

	// StateDir itself, tightened by the memory subsystem, plus the queue
	// database the SQLite driver created inside it.
	assertPerm(t, layout.StateDir(), fsperm.DirMode)
	assertPerm(t, mem.QueueDBPath, fsperm.FileMode)
}
