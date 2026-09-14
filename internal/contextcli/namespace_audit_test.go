package contextcli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/tesseract/internal/contextcli"
	"github.com/hollis-labs/tesseract/internal/contextpolicy"
	"github.com/hollis-labs/tesseract/internal/contextstore"
)

func TestNamespaceAudit_ReportMetricsAndReadOnly(t *testing.T) {
	// Synthetic, disposable fixture in t.TempDir(). Never touch live store.
	dir := t.TempDir()
	cs, err := contextstore.Open(context.Background(), contextstore.Config{RootDir: dir})
	if err != nil {
		t.Fatalf("contextstore.Open: %v", err)
	}
	defer cs.Close()

	ctx := context.Background()

	// 1. Seed the 18 Cerberus projects.
	var seeded int
	seeded, err = cs.SeedCerberusProjects(ctx)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if seeded != 18 {
		t.Fatalf("expected 18 seeded, got %d", seeded)
	}

	// 2. Insert declared namespace:
	now := time.Now().UTC().Format(time.RFC3339)
	if err = cs.UpsertNamespacePolicy(ctx, contextstore.NamespacePolicyEntry{
		Namespace: "project/torque/custom",
		OwnerType: "project",
		OwnerID:   "torque",
		Policy:    map[string]any{"source": "declared", "tier": "special"},
	}); err != nil {
		t.Fatalf("insert declared: %v", err)
	}

	// 3. Insert active inferred namespace (updated after 2026-09-12):
	if _, err = cs.DB().ExecContext(ctx, `
INSERT INTO namespace_policies (namespace, owner_type, owner_id, policy_json, updated_at)
VALUES (?, ?, ?, ?, ?)`, "user/chrispian/memory/notes", "user", "chrispian", `{"source":"inferred"}`, "2026-09-13T12:00:00Z"); err != nil {
		t.Fatalf("insert active inferred: %v", err)
	}

	// 4. Insert inactive inferred namespace (updated before 2026-09-12):
	if _, err = cs.DB().ExecContext(ctx, `
INSERT INTO namespace_policies (namespace, owner_type, owner_id, policy_json, updated_at)
VALUES (?, ?, ?, ?, ?)`, "app/legacy-app/state", "app", "legacy-app", `{"source":"inferred"}`, "2026-09-10T12:00:00Z"); err != nil {
		t.Fatalf("insert inactive inferred: %v", err)
	}

	// 5. Insert legacy nested namespace (updated before 2026-09-12):
	if _, err = cs.DB().ExecContext(ctx, `
INSERT INTO namespace_policies (namespace, owner_type, owner_id, policy_json, updated_at)
VALUES (?, ?, ?, ?, ?)`, "user/chrispian/project/loom/memory/notes", "user", "chrispian", `{"source":"inferred"}`, "2026-09-08T12:00:00Z"); err != nil {
		t.Fatalf("insert legacy nested: %v", err)
	}

	// 6. Insert suspicious namespaces:
	// 6a. Single segment
	if _, err = cs.DB().ExecContext(ctx, `
INSERT INTO namespace_policies (namespace, owner_type, owner_id, policy_json, updated_at)
VALUES (?, ?, ?, ?, ?)`, "mentat", "system", "mentat", `{"source":"inferred"}`, now); err != nil {
		t.Fatalf("insert single segment: %v", err)
	}
	// 6b. Typo in scope head
	if _, err = cs.DB().ExecContext(ctx, `
INSERT INTO namespace_policies (namespace, owner_type, owner_id, policy_json, updated_at)
VALUES (?, ?, ?, ?, ?)`, "projcet/typo/notes", "system", "projcet/typo/notes", `{"source":"inferred"}`, now); err != nil {
		t.Fatalf("insert typo: %v", err)
	}
	// 6c. Malformed (double slash)
	if _, err = cs.DB().ExecContext(ctx, `
INSERT INTO namespace_policies (namespace, owner_type, owner_id, policy_json, updated_at)
VALUES (?, ?, ?, ?, ?)`, "user//double-slash", "system", "user//double-slash", `{"source":"inferred"}`, now); err != nil {
		t.Fatalf("insert malformed: %v", err)
	}

	// Record database row count before audit to prove zero mutations.
	var countBefore int
	if err = cs.DB().QueryRowContext(ctx, `SELECT count(*) FROM namespace_policies`).Scan(&countBefore); err != nil {
		t.Fatalf("count before: %v", err)
	}
	const expectedTotal = 18 + 1 + 1 + 1 + 1 + 3 // 25
	if countBefore != expectedTotal {
		t.Fatalf("expected %d rows in DB before audit, got %d", expectedTotal, countBefore)
	}

	cli := &contextcli.CLI{
		Store:  cs,
		Policy: contextpolicy.New(),
	}

	// Run ComputeNamespaceAudit directly
	report, err := cli.ComputeNamespaceAudit(ctx)
	if err != nil {
		t.Fatalf("ComputeNamespaceAudit: %v", err)
	}

	if report.TotalNamespaces != 25 {
		t.Errorf("TotalNamespaces: got %d, want 25", report.TotalNamespaces)
	}
	if report.Seeded != 18 {
		t.Errorf("Seeded: got %d, want 18", report.Seeded)
	}
	if report.Declared != 1 {
		t.Errorf("Declared: got %d, want 1", report.Declared)
	}
	if report.Inferred != 6 {
		t.Errorf("Inferred: got %d, want 6", report.Inferred)
	}
	if report.InferredInactive != 2 {
		t.Errorf("InferredInactive: got %d, want 2 (legacy-app and loom)", report.InferredInactive)
	}
	if report.DeclaredPolicy != 19 { // 18 seeded + 1 declared
		t.Errorf("DeclaredPolicy: got %d, want 19", report.DeclaredPolicy)
	}
	if report.UndeclaredPolicy != 6 {
		t.Errorf("UndeclaredPolicy: got %d, want 6", report.UndeclaredPolicy)
	}
	if report.LegacyNested != 1 {
		t.Errorf("LegacyNested: got %d, want 1", report.LegacyNested)
	}
	if report.Suspicious != 3 {
		t.Errorf("Suspicious: got %d, want 3", report.Suspicious)
	}

	// Verify read-only guarantee: row count unchanged
	var countAfter int
	if err = cs.DB().QueryRowContext(ctx, `SELECT count(*) FROM namespace_policies`).Scan(&countAfter); err != nil {
		t.Fatalf("count after: %v", err)
	}
	if countAfter != countBefore {
		t.Fatalf("read-only violation: count changed from %d to %d", countBefore, countAfter)
	}

	// Test CLI JSON output
	stdoutJSON := &bytes.Buffer{}
	stderrJSON := &bytes.Buffer{}
	cliJSON := &contextcli.CLI{
		Store:  cs,
		Policy: contextpolicy.New(),
		Stdout: stdoutJSON,
		Stderr: stderrJSON,
	}
	exitCode := cliJSON.Run(ctx, []string{"context", "namespace", "audit", "--format", "json"})
	if exitCode != 0 {
		t.Fatalf("cli run --format json failed with code %d: %s", exitCode, stderrJSON.String())
	}
	var fromJSON contextcli.NamespaceAuditReport
	if err := json.Unmarshal(stdoutJSON.Bytes(), &fromJSON); err != nil {
		t.Fatalf("unmarshal json output: %v\nOutput: %s", err, stdoutJSON.String())
	}
	if fromJSON != report {
		t.Errorf("JSON output mismatch:\ngot  %+v\nwant %+v", fromJSON, report)
	}

	// Test CLI Table output
	stdoutTable := &bytes.Buffer{}
	stderrTable := &bytes.Buffer{}
	cliTable := &contextcli.CLI{
		Store:  cs,
		Policy: contextpolicy.New(),
		Stdout: stdoutTable,
		Stderr: stderrTable,
	}
	exitCode = cliTable.Run(ctx, []string{"context", "namespace", "audit", "--format", "table"})
	if exitCode != 0 {
		t.Fatalf("cli run --format table failed with code %d: %s", exitCode, stderrTable.String())
	}
	tableOut := stdoutTable.String()
	if !strings.Contains(tableOut, "METRIC") || !strings.Contains(tableOut, "total_namespaces") {
		t.Fatalf("table output missing headers or metrics:\n%s", tableOut)
	}
	if !strings.Contains(tableOut, "25") {
		t.Errorf("table output missing total count 25:\n%s", tableOut)
	}
}
