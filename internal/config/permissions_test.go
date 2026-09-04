package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/tesseract/internal/config"
	"github.com/hollis-labs/tesseract/internal/fsperm"
)

// config.yaml carries provider credentials, so it and its directory are
// owner-only (CW-20260904-0078). The assertions are against modes Save set
// with an explicit Chmod, which no umask can widen or narrow; the probe below
// skips when the ambient umask would make an untightened path look tightened.
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

func TestSaveCreatesAnOwnerOnlyConfig(t *testing.T) {
	requirePermTestable(t)
	dir := filepath.Join(t.TempDir(), "tesseract")
	path := filepath.Join(dir, "config.yaml")

	if err := config.Save(path, config.Config{}); err != nil {
		t.Fatalf("save: %v", err)
	}
	assertMode(t, dir, fsperm.DirMode)
	assertMode(t, path, fsperm.FileMode)
}

// A config directory laid down by go-apppaths (0755) holding a config.yaml
// written by an older build (0644) is the state on every existing install.
// O_CREATE's mode is ignored for a file that already exists, so without the
// explicit chmod the credentials would stay world-readable forever.
func TestSaveTightensAConfigItInherits(t *testing.T) {
	requirePermTestable(t)
	dir := filepath.Join(t.TempDir(), "tesseract")
	if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec // deliberately the pre-hardening 0755/0644; the test asserts it gets tightened
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Chmod(dir, 0o755); err != nil { //nolint:gosec // deliberately the pre-hardening 0755/0644; the test asserts it gets tightened
		t.Fatalf("chmod dir: %v", err)
	}
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("embedding:\n  provider: legacy\n"), 0o644); err != nil { //nolint:gosec // deliberately the pre-hardening 0755/0644; the test asserts it gets tightened
		t.Fatalf("write legacy config: %v", err)
	}
	if err := os.Chmod(path, 0o644); err != nil { //nolint:gosec // deliberately the pre-hardening 0755/0644; the test asserts it gets tightened
		t.Fatalf("chmod file: %v", err)
	}

	if err := config.Save(path, config.Config{}); err != nil {
		t.Fatalf("save: %v", err)
	}
	assertMode(t, dir, fsperm.DirMode)
	assertMode(t, path, fsperm.FileMode)
}

// TestResolveLayoutTightensTheRootsItInherits pins the fix for the gap
// go-apppaths leaves: it materializes the four XDG roots at 0755, the
// workspace directory with them, and exposes no mode option. Every package
// below applies the policy on the way in, so without this the directories
// would stay world-listable however carefully their contents were written.
//
// The roots are loosened first so the assertion is about ResolveLayout
// tightening a pre-policy layout rather than about the ambient umask.
func TestResolveLayoutTightensTheRootsItInherits(t *testing.T) {
	requirePermTestable(t)

	root := t.TempDir()
	for _, key := range []string{"XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME", "XDG_CONFIG_HOME"} {
		t.Setenv(key, root)
	}

	layout, err := config.ResolveLayout()
	if err != nil {
		t.Fatalf("resolve layout: %v", err)
	}

	roots := []string{
		layout.DataDir(),
		layout.StateDir(),
		layout.CacheDir(),
		layout.ConfigDir(),
		layout.Workspace().Dir,
	}
	for _, dir := range roots {
		if err := os.Chmod(dir, 0o755); err != nil { //nolint:gosec // models a pre-policy install, so it must be loose
			t.Fatalf("loosen %s: %v", dir, err)
		}
	}

	if _, err := config.ResolveLayout(); err != nil {
		t.Fatalf("re-resolve layout: %v", err)
	}
	for _, dir := range roots {
		assertMode(t, dir, fsperm.DirMode)
	}
}
