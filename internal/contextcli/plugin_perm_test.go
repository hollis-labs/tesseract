package contextcli

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The plugins directory sits on the far side of the owned-vs-operator-supplied
// line drawn in internal/fsperm: it defaults to ./plugins relative to the
// working directory and is replaced wholesale by TESSERACT_PLUGINS_DIR, so it
// may be a directory shared with other tools or other people. Tesseract creates
// it and stops there. These are the guards that keep a future change from
// quietly extending the owner-only policy across that line.

// stubGit puts a git that always fails first on PATH, so pluginInstall reaches
// its MkdirAll and its clone without touching the network.
func stubGit(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell stub for git is not portable to Windows")
	}
	bin := t.TempDir()
	script := "#!/bin/sh\necho 'stub git: refusing to clone' >&2\nexit 1\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o700); err != nil { //nolint:gosec // an executable stub needs the execute bit
		t.Fatalf("write git stub: %v", err)
	}
	t.Setenv("PATH", bin)
}

// devNull gives pluginInstall the *os.File pair it wants without polluting the
// test output.
func devNull(t *testing.T) *os.File {
	t.Helper()
	f, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open %s: %v", os.DevNull, err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

func TestPluginInstallLeavesAnOperatorSuppliedDirectoryAlone(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX file modes are not meaningful on this platform")
	}
	stubGit(t)

	pluginsDir := filepath.Join(t.TempDir(), "shared-plugins")
	if err := os.Mkdir(pluginsDir, 0o755); err != nil { //nolint:gosec // deliberately 0755: an operator-supplied path Tesseract must not tighten
		t.Fatalf("mkdir plugins dir: %v", err)
	}
	// Chmod rather than trusting Mkdir's mode, so the starting state is 0755
	// under any umask.
	if err := os.Chmod(pluginsDir, 0o755); err != nil { //nolint:gosec // deliberately 0755: an operator-supplied path Tesseract must not tighten
		t.Fatalf("chmod plugins dir: %v", err)
	}
	neighbor := filepath.Join(pluginsDir, "someone-elses.txt")
	if err := os.WriteFile(neighbor, []byte("not ours"), 0o644); err != nil { //nolint:gosec // deliberately 0755: an operator-supplied path Tesseract must not tighten
		t.Fatalf("write neighbor: %v", err)
	}
	if err := os.Chmod(neighbor, 0o644); err != nil { //nolint:gosec // deliberately 0755: an operator-supplied path Tesseract must not tighten
		t.Fatalf("chmod neighbor: %v", err)
	}

	null := devNull(t)
	if code := pluginInstall(pluginsDir, "does-not-exist", null, null); code != 1 {
		t.Fatalf("expected the stubbed clone to fail, got exit %d", code)
	}

	info, err := os.Stat(pluginsDir)
	if err != nil {
		t.Fatalf("stat plugins dir: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o755 {
		t.Fatalf("plugins dir mode changed to %04o; an operator-supplied path must not be tightened", got)
	}
	ninfo, err := os.Stat(neighbor)
	if err != nil {
		t.Fatalf("stat neighbor: %v", err)
	}
	if got := ninfo.Mode().Perm(); got != 0o644 {
		t.Fatalf("neighboring file mode changed to %04o; an operator-supplied tree must not be walked", got)
	}
}

// The MkdirAll here used to drop its error, so an uncreatable plugins
// directory surfaced as a confusing clone failure instead of the real cause.
func TestPluginInstallReportsAnUncreatablePluginsDir(t *testing.T) {
	stubGit(t)

	root := t.TempDir()
	blocker := filepath.Join(root, "blocker")
	if err := os.WriteFile(blocker, []byte("i am a file"), 0o600); err != nil {
		t.Fatalf("write blocker: %v", err)
	}
	pluginsDir := filepath.Join(blocker, "plugins")

	stderr, err := os.CreateTemp(t.TempDir(), "stderr")
	if err != nil {
		t.Fatalf("temp stderr: %v", err)
	}
	defer func() { _ = stderr.Close() }()

	if code := pluginInstall(pluginsDir, "anything", devNull(t), stderr); code != 1 {
		t.Fatalf("expected exit 1, got %d", code)
	}
	out, err := os.ReadFile(stderr.Name()) //nolint:gosec // t.TempDir path, not caller input
	if err != nil {
		t.Fatalf("read stderr: %v", err)
	}
	if !strings.Contains(string(out), "Failed to create plugins directory") {
		t.Fatalf("expected the mkdir failure to be reported, got: %q", string(out))
	}
}
