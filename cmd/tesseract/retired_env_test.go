package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The retired single-root environment variable. Before the go-apppaths
// migration (CW-20260517-0066) it was a single base directory that the whole
// on-disk layout nested under; the migration replaced it with the independent
// XDG data/state/cache/config roots, and a one-release deprecation shim in
// run() kept it working by mapping it onto all four $XDG_*_HOME vars before
// any path resolution. CW-20260825-0013 removed that shim.
//
// This file is the reason the removal is observable. Deleting the shim source
// file satisfies "the shim is gone" but asserts nothing: a future edit could
// reintroduce the mapping under any name and no test would notice. What has to
// stay true is the BEHAVIOR — that setting this variable does not move a
// single resolved path — so that is what is asserted here.
//
// The name is spelled out in full deliberately. tests/parity's binary-name
// guard scans this file like any other and allows exactly this one token; if a
// second reference to the retired identity ever appears here, the guard fails.
const retiredRootEnvVar = "CONTEXTD_ROOT"

// resolvedPaths runs `tesseract path` and returns its stdout. That subcommand
// is the introspection surface for the resolved layout — every root, the
// active workspace, the main DB, the config file, the records tree and the
// queue DB — so comparing its whole output compares every path the daemon
// would use, not a hand-picked subset.
func resolvedPaths(t *testing.T) string {
	t.Helper()

	stdout, err := os.CreateTemp(t.TempDir(), "stdout-*.log")
	if err != nil {
		t.Fatalf("create stdout temp: %v", err)
	}
	defer func() { _ = stdout.Close() }()
	stderr, err := os.CreateTemp(t.TempDir(), "stderr-*.log")
	if err != nil {
		t.Fatalf("create stderr temp: %v", err)
	}
	defer func() { _ = stderr.Close() }()

	if code := run(context.Background(), []string{"path"}, stdout, stderr); code != 0 {
		t.Fatalf("`path` exited %d, want 0", code)
	}
	if _, err := stdout.Seek(0, 0); err != nil {
		t.Fatalf("seek stdout: %v", err)
	}
	buf := &bytes.Buffer{}
	if _, err := buf.ReadFrom(stdout); err != nil {
		t.Fatalf("read stdout: %v", err)
	}
	if buf.Len() == 0 {
		t.Fatal("`path` produced no output — nothing was compared, so any equality below would be vacuous")
	}
	return buf.String()
}

// TestRetiredRootEnvVarDoesNotMoveResolvedPaths is the behavioral assertion
// that the deprecation shim is gone: the variable is no longer honored.
//
// The positive control is the point of the second half. "Setting X changed
// nothing" is a claim about a measurement, and it is equally satisfied by a
// measurement that cannot detect anything at all — a `path` subcommand that
// ignored the environment wholesale would pass the first half trivially.
// Re-pointing XDG_DATA_HOME must therefore move the output; that proves the
// comparison has the sensitivity the first half relies on.
func TestRetiredRootEnvVarDoesNotMoveResolvedPaths(t *testing.T) {
	hermeticLayout(t)
	baseline := resolvedPaths(t)

	decoy := t.TempDir()
	t.Setenv(retiredRootEnvVar, decoy)
	withVar := resolvedPaths(t)

	if withVar != baseline {
		t.Errorf("setting %s changed the resolved layout — the retired single-root variable is still honored somewhere.\n"+
			"baseline:\n%s\nwith %s=%s:\n%s", retiredRootEnvVar, baseline, retiredRootEnvVar, decoy, withVar)
	}
	for _, line := range strings.Split(withVar, "\n") {
		if strings.Contains(line, decoy) {
			t.Errorf("resolved layout line %q nests under %s — no path may derive from the retired variable", line, retiredRootEnvVar)
		}
	}

	// Positive control: an override that IS honored must move the output, or
	// the equality above proves nothing about this variable in particular.
	moved := t.TempDir()
	t.Setenv("XDG_DATA_HOME", filepath.Join(moved, "data"))
	if control := resolvedPaths(t); control == baseline {
		t.Fatalf("re-pointing XDG_DATA_HOME did not change `path` output — this test cannot detect an honored "+
			"environment variable, so its %s assertion is vacuous", retiredRootEnvVar)
	}
}

// capturedStderr runs the binary's dispatcher and returns what it wrote to
// stderr, so a test can assert on it. The exit code comes back too, because
// some of the runs that produce stderr are the ones that fail.
func capturedStderr(t *testing.T, args ...string) (string, int) {
	t.Helper()

	stdout, err := os.CreateTemp(t.TempDir(), "stdout-*.log")
	if err != nil {
		t.Fatalf("create stdout temp: %v", err)
	}
	defer func() { _ = stdout.Close() }()
	stderr, err := os.CreateTemp(t.TempDir(), "stderr-*.log")
	if err != nil {
		t.Fatalf("create stderr temp: %v", err)
	}
	defer func() { _ = stderr.Close() }()

	code := run(context.Background(), args, stdout, stderr)

	if _, err := stderr.Seek(0, 0); err != nil {
		t.Fatalf("seek stderr: %v", err)
	}
	buf := &bytes.Buffer{}
	if _, err := buf.ReadFrom(stderr); err != nil {
		t.Fatalf("read stderr: %v", err)
	}
	return buf.String(), code
}

// TestRetiredRootEnvVarProducesNoDeprecationWarning closes the other half of
// the removal. The shim announced itself on stderr every run; a caller that
// still sets the variable must now get silence rather than a warning about a
// mapping that no longer happens.
//
// The positive control is not optional here, and for a sharper reason than in
// the sibling test above. stderr is legitimately EMPTY on this run, so
// "it does not contain the name" and "nothing was captured at all" are the
// same observation — a harness that silently captured nothing would pass this
// test forever while being incapable of ever failing it. The control drives a
// command that is known to write to stderr through the identical capture path,
// so the silence being asserted is measured silence rather than absence of
// measurement.
func TestRetiredRootEnvVarProducesNoDeprecationWarning(t *testing.T) {
	hermeticLayout(t)
	t.Setenv(retiredRootEnvVar, t.TempDir())

	out, code := capturedStderr(t, "path")
	if code != 0 {
		t.Fatalf("`path` exited %d, want 0", code)
	}
	if strings.Contains(out, retiredRootEnvVar) {
		t.Errorf("stderr still mentions %s: %s", retiredRootEnvVar, out)
	}

	// Positive control: the same capture path, on a command whose whole job on
	// this input is to write an error to stderr.
	ctrl, ctrlCode := capturedStderr(t, "serve", "--managed-auth", "--static-token", "x")
	if ctrlCode == 0 {
		t.Fatalf("control run exited 0; it was supposed to reject mutually exclusive auth flags")
	}
	if !strings.Contains(ctrl, "mutually exclusive") {
		t.Fatalf("the capture path did not pick up a known stderr write (got %q) — it cannot observe "+
			"stderr at all, so the %s assertion above is vacuous", ctrl, retiredRootEnvVar)
	}
}
