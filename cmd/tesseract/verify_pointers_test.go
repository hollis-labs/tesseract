package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/tesseract/internal/contextstore"
	"github.com/hollis-labs/tesseract/internal/knowledge"
	"github.com/hollis-labs/tesseract/internal/memory"

	_ "modernc.org/sqlite"
)

// TestVerifyDSN_DryRunHandleIsReadOnly proves the dry-run connection cannot
// write. The dry-run code path does not attempt writes; this asserts the
// stronger property that even a wrong code path could not mutate the store.
func TestVerifyDSN_DryRunHandleIsReadOnly(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "probe.db")

	seed, err := sql.Open("sqlite", verifyDSN(path, true))
	if err != nil {
		t.Fatalf("open writable: %v", err)
	}
	if _, err = seed.ExecContext(ctx, `CREATE TABLE t (id INTEGER PRIMARY KEY, v TEXT)`); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err = seed.ExecContext(ctx, `INSERT INTO t (id, v) VALUES (1, 'before')`); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err = seed.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	ro, err := sql.Open("sqlite", verifyDSN(path, false))
	if err != nil {
		t.Fatalf("open read-only: %v", err)
	}
	defer func() { _ = ro.Close() }()

	var v string
	if err = ro.QueryRowContext(ctx, `SELECT v FROM t WHERE id = 1`).Scan(&v); err != nil {
		t.Fatalf("read through read-only handle: %v", err)
	}
	if v != "before" {
		t.Fatalf("read %q, want \"before\"", v)
	}
	if _, err = ro.ExecContext(ctx, `UPDATE t SET v = 'after' WHERE id = 1`); err == nil {
		t.Error("read-only handle accepted an UPDATE")
	}
	if _, err = ro.ExecContext(ctx,
		`INSERT INTO pointer_verifications (revision_id, scheme, locator, outcome, checked_at) VALUES ('x','file','/x','resolved','now')`); err == nil {
		t.Error("read-only handle accepted an INSERT into the verification log")
	}
}

// seedVerifyStore builds a real store with one live and one dead file pointer
// and returns the DB path.
func seedVerifyStore(t *testing.T) (dbPath, alivePath, deadPath string) {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	cs, err := contextstore.Open(ctx, contextstore.Config{RootDir: root})
	if err != nil {
		t.Fatalf("contextstore.Open: %v", err)
	}
	ms := memory.NewStore(cs.DB(), nil, "", 0, memory.NoopQueue{})
	ks := knowledge.New(ms)

	files := t.TempDir()
	alivePath = filepath.Join(files, "alive.md")
	if err = os.WriteFile(alivePath, []byte("x"), 0o600); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	deadPath = filepath.Join(files, "gone.md")

	for key, loc := range map[string]string{"cli.alive": alivePath, "cli.dead": deadPath} {
		if _, err = ks.Write(ctx, knowledge.WriteInput{
			Namespace: "user/tester/knowledge/cli",
			Key:       key,
			Kind:      "note",
			Source:    "manual",
			Pointer:   memory.Pointer{Scheme: "file", Locator: loc},
			Summary:   "cli fixture " + key,
			Author:    memory.Author{AgentID: "test", AgentVersion: "1"},
			SessionID: "test:cli",
		}); err != nil {
			t.Fatalf("knowledge write %s: %v", key, err)
		}
	}
	if err = cs.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	// contextstore.Open resolves the DB under RootDir when DBPath is empty.
	matches, err := filepath.Glob(filepath.Join(root, "**", "*.db"))
	if err != nil || len(matches) == 0 {
		matches, _ = filepath.Glob(filepath.Join(root, "*.db"))
	}
	if len(matches) == 0 {
		if err := filepath.Walk(root, func(p string, info os.FileInfo, _ error) error {
			if info != nil && !info.IsDir() && strings.HasSuffix(p, ".db") {
				matches = append(matches, p)
			}
			return nil
		}); err != nil {
			t.Fatalf("walk: %v", err)
		}
	}
	if len(matches) == 0 {
		t.Fatal("could not locate the seeded SQLite file")
	}
	return matches[0], alivePath, deadPath
}

func fileDigest(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path) //nolint:gosec // test-controlled temp path
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// TestVerifyPointers_DryRunReportsWithoutWriting is the property the gate on
// this ticket depends on: a dry-run against a copy tells you what it would do
// and leaves the file byte-identical.
func TestVerifyPointers_DryRunReportsWithoutWriting(t *testing.T) {
	dbPath, _, deadPath := seedVerifyStore(t)
	before := fileDigest(t, dbPath)

	stdout, stderr, cleanup := captureOutput(t)
	defer cleanup()
	code := runVerifyPointers(context.Background(), "", []string{"--db", dbPath}, stdout, stderr)
	out, errOut := readCaptured(t, stdout, stderr)

	if code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errOut)
	}
	if fileDigest(t, dbPath) != before {
		t.Error("dry-run modified the database file")
	}
	if !strings.Contains(out, "nothing written") {
		t.Errorf("dry-run output does not say it wrote nothing:\n%s", out)
	}
	if !strings.Contains(out, deadPath) {
		t.Errorf("dry-run did not name the dead pointer %s:\n%s", deadPath, out)
	}
	if !strings.Contains(out, string(memory.OutcomeUnresolvable)) {
		t.Errorf("dry-run did not report an unresolvable outcome:\n%s", out)
	}
	// It must also report the live one, or the probe is a rubber stamp.
	if !strings.Contains(out, string(memory.OutcomeResolved)) {
		t.Errorf("dry-run reported no resolved pointers; a probe that cannot say ALIVE is not discriminating:\n%s", out)
	}
}

func TestVerifyPointers_ApplyRecordsThenRefusesStaleExpectation(t *testing.T) {
	dbPath, _, _ := seedVerifyStore(t)
	ctx := context.Background()

	// Apply with matching expectations.
	stdout, stderr, cleanup := captureOutput(t)
	code := runVerifyPointers(ctx, "", []string{"--db", dbPath, "--apply", "--expect-rows", "2"}, stdout, stderr)
	out, errOut := readCaptured(t, stdout, stderr)
	cleanup()
	if code != 0 {
		t.Fatalf("apply exit %d, stderr: %s", code, errOut)
	}
	if !strings.Contains(out, "Recorded: 2 pointer observation(s)") {
		t.Errorf("apply did not report 2 observations:\n%s", out)
	}

	db, err := sql.Open("sqlite", verifyDSN(dbPath, false))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()
	var n int
	if err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pointer_verifications`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 2 {
		t.Errorf("verification log holds %d row(s), want 2", n)
	}

	// A wrong row expectation must refuse without writing.
	stdout2, stderr2, cleanup2 := captureOutput(t)
	code = runVerifyPointers(ctx, "", []string{"--db", dbPath, "--apply", "--expect-rows", "99"}, stdout2, stderr2)
	_, errOut2 := readCaptured(t, stdout2, stderr2)
	cleanup2()
	if code != exitVerifyRefusedExpectation {
		t.Errorf("exit %d, want %d (refused on expectation)", code, exitVerifyRefusedExpectation)
	}
	if !strings.Contains(errOut2, "refusing to apply") {
		t.Errorf("stderr does not explain the refusal: %s", errOut2)
	}
	if err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pointer_verifications`).Scan(&n); err != nil {
		t.Fatalf("recount: %v", err)
	}
	if n != 2 {
		t.Errorf("a refused apply wrote rows: log holds %d, want 2", n)
	}
}

func TestVerifyPointers_RefusesWrongDigest(t *testing.T) {
	dbPath, _, _ := seedVerifyStore(t)
	stdout, stderr, cleanup := captureOutput(t)
	defer cleanup()
	code := runVerifyPointers(context.Background(), "",
		[]string{"--db", dbPath, "--apply", "--expect-digest", "deadbeef"}, stdout, stderr)
	_, errOut := readCaptured(t, stdout, stderr)
	if code != exitVerifyRefusedExpectation {
		t.Errorf("exit %d, want %d", code, exitVerifyRefusedExpectation)
	}
	if !strings.Contains(errOut, "digest") {
		t.Errorf("refusal does not mention the digest: %s", errOut)
	}
}

func TestParseSchemeList(t *testing.T) {
	got, err := parseSchemeList("https, file ,file")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if strings.Join(got, ",") != "file,https" {
		t.Errorf("got %v, want [file https] — sorted and de-duplicated", got)
	}
	// A scheme with no resolver is rejected at the flag, rather than producing
	// a run that records unverifiable for everything and looks like it worked.
	if _, err = parseSchemeList("conduit"); err == nil {
		t.Error("accepted a scheme with no resolver")
	}
	if _, err = parseSchemeList("  "); err == nil {
		t.Error("accepted an empty scheme list")
	}
}

func TestVerifyPointers_RejectsBadScope(t *testing.T) {
	dbPath, _, _ := seedVerifyStore(t)
	stdout, stderr, cleanup := captureOutput(t)
	defer cleanup()
	code := runVerifyPointers(context.Background(), "", []string{"--db", dbPath, "--scope", "everything"}, stdout, stderr)
	_, errOut := readCaptured(t, stdout, stderr)
	if code == 0 {
		t.Error("accepted an unknown scope")
	}
	if !strings.Contains(errOut, "scope") {
		t.Errorf("error does not name the flag: %s", errOut)
	}
}

// captureOutput swaps stdout/stderr for temp files, since runVerifyPointers
// takes *os.File to match the other subcommands.
func captureOutput(t *testing.T) (stdout, stderr *os.File, cleanup func()) {
	t.Helper()
	dir := t.TempDir()
	o, err := os.Create(filepath.Join(dir, "stdout")) //nolint:gosec // t.TempDir path, not caller input
	if err != nil {
		t.Fatalf("create stdout: %v", err)
	}
	e, err := os.Create(filepath.Join(dir, "stderr")) //nolint:gosec // t.TempDir path, not caller input
	if err != nil {
		t.Fatalf("create stderr: %v", err)
	}
	return o, e, func() { _ = o.Close(); _ = e.Close() }
}

func readCaptured(t *testing.T, stdout, stderr *os.File) (string, string) {
	t.Helper()
	o, err := os.ReadFile(stdout.Name()) //nolint:gosec // test-controlled temp path
	if err != nil {
		t.Fatalf("read stdout: %v", err)
	}
	e, err := os.ReadFile(stderr.Name()) //nolint:gosec // test-controlled temp path
	if err != nil {
		t.Fatalf("read stderr: %v", err)
	}
	return string(o), string(e)
}
