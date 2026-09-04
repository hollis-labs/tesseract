package contextapi

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/tesseract/internal/fsperm"
)

// The admin config backup tree is the sharpest exposure CW-20260904-0078
// closed: every file in it is a verbatim copy of config.yaml, provider
// credentials included, and they were all written 0644 into a 0755 directory.
//
// The assertions are against modes the server set with an explicit Chmod, so
// the caller's umask cannot change them; requirePermTestable skips when the
// ambient umask is tight enough that an untightened path would look tightened.
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

func assertPermMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s has mode %04o, want %04o", path, got, want)
	}
}

func newPermTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	srv, root := newTestServerWithRoot(t)
	configDir := filepath.Join(root, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil { //nolint:gosec // deliberately the pre-hardening 0755/0644; the test asserts it gets tightened
		t.Fatalf("mkdir config dir: %v", err)
	}
	if err := os.Chmod(configDir, 0o755); err != nil { //nolint:gosec // deliberately the pre-hardening 0755/0644; the test asserts it gets tightened
		t.Fatalf("chmod config dir: %v", err)
	}
	srv.ConfigFile = filepath.Join(configDir, "config.yaml")
	if err := os.WriteFile(srv.ConfigFile, []byte("embedding:\n  provider: mock\n"), 0o644); err != nil { //nolint:gosec // deliberately the pre-hardening 0755/0644; the test asserts it gets tightened
		t.Fatalf("write config: %v", err)
	}
	if err := os.Chmod(srv.ConfigFile, 0o644); err != nil { //nolint:gosec // deliberately the pre-hardening 0755/0644; the test asserts it gets tightened
		t.Fatalf("chmod config: %v", err)
	}
	return srv, configDir
}

func TestAdminConfigBackupTreeIsOwnerOnly(t *testing.T) {
	requirePermTestable(t)
	srv, configDir := newPermTestServer(t)

	info, err := srv.createAdminConfigBackup("manual")
	if err != nil {
		t.Fatalf("create backup: %v", err)
	}
	backupDir := filepath.Join(configDir, "backups")
	assertPermMode(t, backupDir, fsperm.DirMode)
	assertPermMode(t, filepath.Join(backupDir, info.Name), fsperm.FileMode)
}

// Backups written 0644 by an older build are still on disk on every existing
// install, so the next backup converts the tree it finds rather than adding a
// tight file to a directory full of readable ones.
func TestAdminConfigBackupTightensBackupsItInherits(t *testing.T) {
	requirePermTestable(t)
	srv, configDir := newPermTestServer(t)

	backupDir := filepath.Join(configDir, "backups")
	if err := os.MkdirAll(backupDir, 0o755); err != nil { //nolint:gosec // deliberately the pre-hardening 0755/0644; the test asserts it gets tightened
		t.Fatalf("mkdir backups: %v", err)
	}
	if err := os.Chmod(backupDir, 0o755); err != nil { //nolint:gosec // deliberately the pre-hardening 0755/0644; the test asserts it gets tightened
		t.Fatalf("chmod backups: %v", err)
	}
	legacy := filepath.Join(backupDir, "config-manual-20200101T000000Z.yaml")
	if err := os.WriteFile(legacy, []byte("embedding:\n  provider: legacy\n"), 0o644); err != nil { //nolint:gosec // deliberately the pre-hardening 0755/0644; the test asserts it gets tightened
		t.Fatalf("write legacy backup: %v", err)
	}
	if err := os.Chmod(legacy, 0o644); err != nil { //nolint:gosec // deliberately the pre-hardening 0755/0644; the test asserts it gets tightened
		t.Fatalf("chmod legacy backup: %v", err)
	}

	if _, err := srv.createAdminConfigBackup("manual"); err != nil {
		t.Fatalf("create backup: %v", err)
	}
	assertPermMode(t, backupDir, fsperm.DirMode)
	assertPermMode(t, legacy, fsperm.FileMode)
}

// The restore handler overwrites an existing config.yaml, which is exactly the
// case where O_CREATE's mode is ignored — so a 0644 config would survive every
// restore unless the mode is forced afterwards.
func TestAdminConfigRestoreWritesAnOwnerOnlyConfig(t *testing.T) {
	requirePermTestable(t)
	srv, configDir := newPermTestServer(t)

	backup, err := srv.createAdminConfigBackup("manual")
	if err != nil {
		t.Fatalf("create backup: %v", err)
	}
	// Put the live config back to the world-readable state a restore has to
	// correct, so the assertion is about the restore and not the backup.
	if err := os.Chmod(srv.ConfigFile, 0o644); err != nil { //nolint:gosec // deliberately the pre-hardening 0755/0644; the test asserts it gets tightened
		t.Fatalf("chmod config: %v", err)
	}

	res := performJSON(t, srv, http.MethodPost, "/v1/admin/config/restore", map[string]any{
		"path": backup.Name,
	})
	if res.Code != http.StatusOK {
		t.Fatalf("restore status=%d body=%s", res.Code, res.Body.String())
	}
	assertPermMode(t, configDir, fsperm.DirMode)
	assertPermMode(t, srv.ConfigFile, fsperm.FileMode)
}
