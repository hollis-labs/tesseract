package fsperm_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/tesseract/internal/fsperm"
)

// These tests are independent of the caller's umask by construction. Every
// assertion is made against a mode the code under test set with an explicit
// Chmod, and a chmod is not masked — so 0700/0600 is 0700/0600 whatever the
// process was started with. requireLooseModesVisible covers the other
// direction: under a umask tight enough to produce 0700/0600 on its own, a
// passing assertion would prove nothing, so the test skips instead of
// pretending.
func requireLooseModesVisible(t *testing.T) {
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
		t.Skipf("ambient umask masks loose modes (probe dir is %04o, not 0755); "+
			"this test cannot tell a tightened path from an already-tight one", info.Mode().Perm())
	}
}

// mkLoose creates a directory at the world-readable mode an install from
// before CW-20260904-0078 would have. It chmods rather than trusting Mkdir's
// mode, so the starting state is the same under any umask.
func mkLoose(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec // deliberately the pre-hardening 0755/0644; the test asserts it gets tightened
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	if err := os.Chmod(dir, 0o755); err != nil { //nolint:gosec // deliberately the pre-hardening 0755/0644; the test asserts it gets tightened
		t.Fatalf("chmod %s: %v", dir, err)
	}
}

func writeLoose(t *testing.T, path string, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil { //nolint:gosec // deliberately the pre-hardening 0755/0644; the test asserts it gets tightened
		t.Fatalf("write %s: %v", path, err)
	}
	if err := os.Chmod(path, 0o644); err != nil { //nolint:gosec // deliberately the pre-hardening 0755/0644; the test asserts it gets tightened
		t.Fatalf("chmod %s: %v", path, err)
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

func TestEnsureDirTightensADirectoryThatAlreadyExisted(t *testing.T) {
	requireLooseModesVisible(t)
	dir := filepath.Join(t.TempDir(), "state")
	mkLoose(t, dir)

	// MkdirAll alone is a no-op on an existing directory, so this only passes
	// because EnsureDir chmods afterwards.
	if err := fsperm.EnsureDir(dir); err != nil {
		t.Fatalf("EnsureDir: %v", err)
	}
	assertPerm(t, dir, fsperm.DirMode)
}

func TestWriteFileTightensAFileThatAlreadyExisted(t *testing.T) {
	requireLooseModesVisible(t)
	path := filepath.Join(t.TempDir(), "config.yaml")
	writeLoose(t, path, "embedding:\n  provider: legacy\n")

	// O_CREATE's mode is ignored for an existing file, so this only passes
	// because WriteFile chmods afterwards.
	if err := fsperm.WriteFile(path, []byte("embedding:\n  provider: new\n")); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	assertPerm(t, path, fsperm.FileMode)

	body, err := os.ReadFile(path) //nolint:gosec // t.TempDir path, not caller input
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(body) != "embedding:\n  provider: new\n" {
		t.Fatalf("content not written: %q", string(body))
	}
}

func TestEnsureTreeConvertsALegacyTreeInPlace(t *testing.T) {
	requireLooseModesVisible(t)
	root := filepath.Join(t.TempDir(), "records")
	nested := filepath.Join(root, "app", "editor")
	mkLoose(t, nested)
	payload := filepath.Join(nested, "1.json")
	writeLoose(t, payload, `{"n":1}`)

	if err := fsperm.EnsureTree(root); err != nil {
		t.Fatalf("EnsureTree: %v", err)
	}
	assertPerm(t, root, fsperm.DirMode)
	assertPerm(t, filepath.Join(root, "app"), fsperm.DirMode)
	assertPerm(t, nested, fsperm.DirMode)
	assertPerm(t, payload, fsperm.FileMode)
}

// The ordering inside EnsureTree is load-bearing: tightening the root first
// would make the root's mode claim the tree below it was already converted.
// This is the regression guard for that, expressed as the behavior a caller
// depends on rather than as a call-order assertion.
func TestEnsureTreeTightensChildrenNotJustTheRoot(t *testing.T) {
	requireLooseModesVisible(t)
	root := filepath.Join(t.TempDir(), "records")
	mkLoose(t, filepath.Join(root, "app"))
	payload := filepath.Join(root, "app", "1.json")
	writeLoose(t, payload, `{"n":1}`)

	if err := fsperm.EnsureTree(root); err != nil {
		t.Fatalf("EnsureTree: %v", err)
	}
	assertPerm(t, payload, fsperm.FileMode)
}

// The root's mode is TightenTree's marker for "this tree is already
// converted". That is what keeps a store with hundreds of thousands of payload
// files from being walked on every open, so it is asserted rather than assumed.
func TestTightenTreeSkipsTheWalkOnceTheRootIsTight(t *testing.T) {
	requireLooseModesVisible(t)
	root := filepath.Join(t.TempDir(), "records")
	if err := fsperm.EnsureDir(root); err != nil {
		t.Fatalf("EnsureDir: %v", err)
	}
	stray := filepath.Join(root, "stray.json")
	writeLoose(t, stray, `{"n":1}`)

	if err := fsperm.TightenTree(root); err != nil {
		t.Fatalf("TightenTree: %v", err)
	}
	assertPerm(t, stray, 0o644)
}

func TestTightenPathIgnoresMissingPaths(t *testing.T) {
	if err := fsperm.TightenPath(filepath.Join(t.TempDir(), "context.db-wal")); err != nil {
		t.Fatalf("TightenPath on a missing path: %v", err)
	}
}

// chmod follows symlinks, so a link pointing out of a Tesseract-owned tree
// would let TightenTree change the mode of a file Tesseract does not own.
func TestTightenPathLeavesSymlinksAndTheirTargetsAlone(t *testing.T) {
	requireLooseModesVisible(t)
	dir := t.TempDir()
	outsider := filepath.Join(dir, "outsider.txt")
	writeLoose(t, outsider, "not ours")
	link := filepath.Join(dir, "link.txt")
	if err := os.Symlink(outsider, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	if err := fsperm.TightenPath(link); err != nil {
		t.Fatalf("TightenPath: %v", err)
	}
	assertPerm(t, outsider, 0o644)
}
