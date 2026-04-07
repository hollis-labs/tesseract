package contextcli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/hollis-labs/vanta-conduit/internal/contextpolicy"
	"github.com/hollis-labs/vanta-conduit/internal/contextstore"
)

func newTestCLI(t *testing.T) (*CLI, *bytes.Buffer, *bytes.Buffer) {
	cli, out, errOut, _ := newTestCLIWithRoot(t)
	return cli, out, errOut
}

func newTestCLIWithRoot(t *testing.T) (*CLI, *bytes.Buffer, *bytes.Buffer, string) {
	t.Helper()
	root := t.TempDir()
	store, err := contextstore.Open(context.Background(), contextstore.Config{RootDir: root})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	return &CLI{Store: store, Policy: contextpolicy.New(), Stdout: out, Stderr: errOut}, out, errOut, root
}

func TestValidationMissingArgs(t *testing.T) {
	cli, _, errOut := newTestCLI(t)
	code := cli.Run(context.Background(), []string{"context", "get", "--namespace", "app/editor/session"})
	if code == 0 {
		t.Fatalf("expected non-zero exit")
	}
	if !strings.Contains(errOut.String(), "key") {
		t.Fatalf("expected key validation error, got: %s", errOut.String())
	}
}

func TestPutGetHistoryAndTableOutput(t *testing.T) {
	cli, out, errOut := newTestCLI(t)

	for i := 1; i <= 2; i++ {
		out.Reset()
		errOut.Reset()
		code := cli.Run(context.Background(), []string{
			"context", "put",
			"--client-id", "editor",
			"--actor", "app:editor",
			"--namespace", "app/editor/session",
			"--key", "summary",
			"--json", `{"n":` + string(rune('0'+i)) + `}`,
		})
		if code != 0 {
			t.Fatalf("put %d failed: %s", i, errOut.String())
		}
	}

	out.Reset()
	if code := cli.Run(context.Background(), []string{
		"context", "history",
		"--namespace", "app/editor/session",
		"--key", "summary",
		"--output", "table",
	}); code != 0 {
		t.Fatalf("history failed: %s", errOut.String())
	}

	table := out.String()
	if !strings.Contains(table, "NAMESPACE") || !strings.Contains(table, "app/editor/session") {
		t.Fatalf("unexpected table output: %s", table)
	}
}

func TestViewDeterministicJSONOutput(t *testing.T) {
	cli, out, errOut := newTestCLI(t)
	writes := [][]string{
		{"--client-id", "editor", "--actor", "app:editor", "--namespace", "app/editor/session", "--key", "b", "--json", `{"v":1}`},
		{"--client-id", "editor", "--actor", "app:editor", "--namespace", "app/editor/session", "--key", "a", "--json", `{"v":2}`},
	}
	for _, flags := range writes {
		args := append([]string{"context", "put"}, flags...)
		if code := cli.Run(context.Background(), args); code != 0 {
			t.Fatalf("put failed: %s", errOut.String())
		}
	}

	selector := `{"namespaces":["app/editor/*"],"revision_scope":"all","order":["namespace","key","revision"]}`
	out.Reset()
	if code := cli.Run(context.Background(), []string{"context", "view", "--selector", selector, "--output", "json"}); code != 0 {
		t.Fatalf("view A failed: %s", errOut.String())
	}
	a := out.String()

	out.Reset()
	if code := cli.Run(context.Background(), []string{"context", "view", "--selector", selector, "--output", "json"}); code != 0 {
		t.Fatalf("view B failed: %s", errOut.String())
	}
	b := out.String()

	var pa, pb any
	if err := json.Unmarshal([]byte(a), &pa); err != nil {
		t.Fatalf("unmarshal A: %v", err)
	}
	if err := json.Unmarshal([]byte(b), &pb); err != nil {
		t.Fatalf("unmarshal B: %v", err)
	}
	if strings.TrimSpace(a) == "" || strings.TrimSpace(b) == "" {
		t.Fatalf("empty output")
	}
	if !strings.EqualFold("", "") && pa == nil && pb == nil {
		t.Fatalf("impossible guard")
	}
	if !equalJSON(pa, pb) {
		t.Fatalf("view output not deterministic")
	}
}

func equalJSON(a, b any) bool {
	ab, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	return string(ab) == string(bb)
}

func TestDoctorAndRepairHeads(t *testing.T) {
	cli, out, errOut, root := newTestCLIWithRoot(t)

	if code := cli.Run(context.Background(), []string{
		"context", "put",
		"--client-id", "editor",
		"--actor", "app:editor",
		"--namespace", "app/editor/session",
		"--key", "summary",
		"--json", `{"v":1}`,
	}); code != 0 {
		t.Fatalf("put failed: %s", errOut.String())
	}

	if err := os.Remove(filepath.Join(root, "data", "records", "app", "editor", "session", "summary", "1.json")); err != nil {
		t.Fatalf("remove payload: %v", err)
	}

	out.Reset()
	if code := cli.Run(context.Background(), []string{"context", "doctor", "--output", "json"}); code != 0 {
		t.Fatalf("doctor failed: %s", errOut.String())
	}
	if !strings.Contains(out.String(), "missing_payload") {
		t.Fatalf("expected missing_payload finding, got: %s", out.String())
	}

	out.Reset()
	if code := cli.Run(context.Background(), []string{"context", "repair-heads", "--output", "table"}); code != 0 {
		t.Fatalf("repair-heads failed: %s", errOut.String())
	}
	if !strings.Contains(out.String(), "REBUILT_HEADS") {
		t.Fatalf("unexpected repair table output: %s", out.String())
	}
}

func TestAuditCommandShowsWriteAndPromote(t *testing.T) {
	cli, out, errOut, _ := newTestCLIWithRoot(t)

	if code := cli.Run(context.Background(), []string{
		"context", "put",
		"--client-id", "editor",
		"--actor", "app:editor",
		"--namespace", "app/editor/session",
		"--key", "summary",
		"--json", `{"v":1}`,
	}); code != 0 {
		t.Fatalf("put failed: %s", errOut.String())
	}

	out.Reset()
	if code := cli.Run(context.Background(), []string{
		"context", "promote", "request",
		"--client-id", "editor",
		"--actor", "app:editor",
		"--source-namespace", "app/editor/session",
		"--source-key", "summary",
		"--target-namespace", "user/notes",
		"--target-key", "daily",
	}); code != 0 {
		t.Fatalf("promote request failed: %s", errOut.String())
	}
	requestID := extractCLIRequestID(t, out.String())
	out.Reset()
	if code := cli.Run(context.Background(), []string{
		"context", "promote", "accept", requestID, "--actor", "user",
	}); code != 0 {
		t.Fatalf("promote accept failed: %s", errOut.String())
	}

	out.Reset()
	if code := cli.Run(context.Background(), []string{"context", "audit", "--limit", "10", "--output", "table"}); code != 0 {
		t.Fatalf("audit failed: %s", errOut.String())
	}
	table := out.String()
	if !strings.Contains(table, "EVENT") || !strings.Contains(table, "write") || !strings.Contains(table, "promote") {
		t.Fatalf("expected write/promote audit rows, got: %s", table)
	}
}

func TestAuditCommandSupportsFiltersAndCursor(t *testing.T) {
	cli, out, errOut, _ := newTestCLIWithRoot(t)

	for i := 1; i <= 2; i++ {
		if code := cli.Run(context.Background(), []string{
			"context", "put",
			"--client-id", "editor",
			"--actor", "app:editor",
			"--namespace", "app/editor/session",
			"--key", "summary",
			"--json", `{"v":` + string(rune('0'+i)) + `}`,
		}); code != 0 {
			t.Fatalf("put %d failed: %s", i, errOut.String())
		}
	}
	out.Reset()
	if code := cli.Run(context.Background(), []string{
		"context", "promote", "request",
		"--client-id", "editor",
		"--actor", "app:editor",
		"--source-namespace", "app/editor/session",
		"--source-key", "summary",
		"--target-namespace", "user/notes",
		"--target-key", "daily",
	}); code != 0 {
		t.Fatalf("promote request failed: %s", errOut.String())
	}
	requestID := extractCLIRequestID(t, out.String())
	out.Reset()
	if code := cli.Run(context.Background(), []string{
		"context", "promote", "accept", requestID, "--actor", "user",
	}); code != 0 {
		t.Fatalf("promote accept failed: %s", errOut.String())
	}

	out.Reset()
	if code := cli.Run(context.Background(), []string{"context", "audit", "--limit", "1", "--output", "json"}); code != 0 {
		t.Fatalf("audit page one failed: %s", errOut.String())
	}
	var pageOne struct {
		Count      int                       `json:"count"`
		Items      []contextstore.AuditEvent `json:"items"`
		NextCursor *int64                    `json:"next_cursor"`
	}
	if err := json.Unmarshal(out.Bytes(), &pageOne); err != nil {
		t.Fatalf("unmarshal page one: %v", err)
	}
	if pageOne.Count != 1 || pageOne.NextCursor == nil {
		t.Fatalf("expected one item + next cursor, got %+v", pageOne)
	}

	out.Reset()
	if code := cli.Run(context.Background(), []string{
		"context", "audit",
		"--cursor", strconv.FormatInt(*pageOne.NextCursor, 10),
		"--limit", "10",
		"--namespace", "user/notes",
		"--event-type", "promote",
		"--output", "json",
	}); code != 0 {
		t.Fatalf("audit filtered failed: %s", errOut.String())
	}
	var filtered struct {
		Count int                       `json:"count"`
		Items []contextstore.AuditEvent `json:"items"`
	}
	if err := json.Unmarshal(out.Bytes(), &filtered); err != nil {
		t.Fatalf("unmarshal filtered: %v", err)
	}
	if filtered.Count > 1 {
		t.Fatalf("expected at most one filtered promote event, got %+v", filtered)
	}
}

func TestTokenLifecycleCommands(t *testing.T) {
	cli, out, errOut, _ := newTestCLIWithRoot(t)

	out.Reset()
	if code := cli.Run(context.Background(), []string{"context", "token", "issue", "--label", "admin", "--ttl", "1h", "--output", "json"}); code != 0 {
		t.Fatalf("token issue failed: %s", errOut.String())
	}
	var issuePayload struct {
		Token string `json:"token"`
		Meta  struct {
			TokenID string `json:"token_id"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(out.Bytes(), &issuePayload); err != nil {
		t.Fatalf("unmarshal issue: %v", err)
	}
	if issuePayload.Token == "" || issuePayload.Meta.TokenID == "" {
		t.Fatalf("expected issued token + metadata")
	}

	out.Reset()
	if code := cli.Run(context.Background(), []string{"context", "token", "list", "--output", "table"}); code != 0 {
		t.Fatalf("token list failed: %s", errOut.String())
	}
	if !strings.Contains(out.String(), "ID") || !strings.Contains(out.String(), "STATUS") {
		t.Fatalf("unexpected token list output: %s", out.String())
	}

	out.Reset()
	if code := cli.Run(context.Background(), []string{"context", "token", "rotate", "--token", issuePayload.Token, "--label", "admin-rotated", "--ttl", "1h", "--output", "json"}); code != 0 {
		t.Fatalf("token rotate failed: %s", errOut.String())
	}
	var rotatePayload struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(out.Bytes(), &rotatePayload); err != nil {
		t.Fatalf("unmarshal rotate: %v", err)
	}
	if rotatePayload.Token == "" || rotatePayload.Token == issuePayload.Token {
		t.Fatalf("expected rotated token distinct from original")
	}

	if code := cli.Run(context.Background(), []string{"context", "token", "revoke", "--token", rotatePayload.Token}); code != 0 {
		t.Fatalf("token revoke failed: %s", errOut.String())
	}
}

func TestNamespaceSchemaShowAndPutValidation(t *testing.T) {
	cli, out, errOut, _ := newTestCLIWithRoot(t)

	if code := cli.Run(context.Background(), []string{
		"context", "namespace", "register",
		"--namespace", "app/editor/session",
		"--owner-type", "app",
		"--owner-id", "editor",
	}); code != 0 {
		t.Fatalf("namespace register failed: %s", errOut.String())
	}

	// Persist schema-required keys policy for this test.
	if err := cli.Store.UpsertNamespacePolicy(context.Background(), contextstore.NamespacePolicyEntry{
		Namespace: "app/editor/session",
		OwnerType: "app",
		OwnerID:   "editor",
		Policy: map[string]any{
			"required_keys": []any{"title", "summary"},
		},
	}); err != nil {
		t.Fatalf("persist schema policy: %v", err)
	}
	if err := cli.Policy.RegisterNamespace("app/editor/session", "app", "editor", map[string]any{
		"required_keys": []any{"title", "summary"},
	}); err != nil {
		t.Fatalf("register with schema policy: %v", err)
	}

	out.Reset()
	if code := cli.Run(context.Background(), []string{"context", "namespace", "show", "--namespace", "app/editor/session"}); code != 0 {
		t.Fatalf("namespace show failed: %s", errOut.String())
	}
	if !strings.Contains(out.String(), "required_keys") {
		t.Fatalf("expected policy in namespace show output, got: %s", out.String())
	}

	errOut.Reset()
	if code := cli.Run(context.Background(), []string{
		"context", "put",
		"--client-id", "editor",
		"--actor", "app:editor",
		"--namespace", "app/editor/session",
		"--key", "summary",
		"--json", `{"title":"only-title"}`,
	}); code == 0 {
		t.Fatalf("expected schema validation failure")
	}
	if !strings.Contains(errOut.String(), "missing required keys") {
		t.Fatalf("expected schema error text, got: %s", errOut.String())
	}
}

func TestHealthCommandOutput(t *testing.T) {
	cli, out, errOut, _ := newTestCLIWithRoot(t)
	if code := cli.Run(context.Background(), []string{
		"context", "put",
		"--client-id", "editor",
		"--actor", "app:editor",
		"--namespace", "app/editor/session",
		"--key", "summary",
		"--json", `{"v":1}`,
	}); code != 0 {
		t.Fatalf("put failed: %s", errOut.String())
	}

	out.Reset()
	if code := cli.Run(context.Background(), []string{"context", "health", "--output", "table"}); code != 0 {
		t.Fatalf("health failed: %s", errOut.String())
	}
	if !strings.Contains(out.String(), "HEALTHY") || !strings.Contains(out.String(), "STATUS") {
		t.Fatalf("unexpected health output: %s", out.String())
	}
}

func TestHealthSummaryStatusTiers(t *testing.T) {
	cli, out, errOut, root := newTestCLIWithRoot(t)
	if code := cli.Run(context.Background(), []string{
		"context", "put",
		"--client-id", "editor",
		"--actor", "app:editor",
		"--namespace", "app/editor/session",
		"--key", "summary",
		"--json", `{"v":1}`,
	}); code != 0 {
		t.Fatalf("put failed: %s", errOut.String())
	}

	out.Reset()
	if code := cli.Run(context.Background(), []string{"context", "health", "--summary", "--output", "json"}); code != 0 {
		t.Fatalf("health summary healthy failed: %s", errOut.String())
	}
	if !strings.Contains(out.String(), `"status":"healthy"`) {
		t.Fatalf("expected healthy summary, got: %s", out.String())
	}

	if err := os.Remove(filepath.Join(root, "data", "records", "app", "editor", "session", "summary", "1.json")); err != nil {
		t.Fatalf("remove payload: %v", err)
	}
	out.Reset()
	if code := cli.Run(context.Background(), []string{"context", "health", "--summary", "--output", "json"}); code != 0 {
		t.Fatalf("health summary degraded failed: %s", errOut.String())
	}
	if !strings.Contains(out.String(), `"status":"degraded"`) {
		t.Fatalf("expected degraded summary, got: %s", out.String())
	}

	if err := os.RemoveAll(filepath.Join(root, "data", "records")); err != nil {
		t.Fatalf("remove records dir: %v", err)
	}
	out.Reset()
	if code := cli.Run(context.Background(), []string{"context", "health", "--summary", "--output", "table"}); code != 0 {
		t.Fatalf("health summary failing failed: %s", errOut.String())
	}
	if !strings.Contains(out.String(), "failing") {
		t.Fatalf("expected failing summary table, got: %s", out.String())
	}
}

func TestBackupVerifyCommand(t *testing.T) {
	cli, out, errOut, root := newTestCLIWithRoot(t)
	if code := cli.Run(context.Background(), []string{
		"context", "put",
		"--client-id", "editor",
		"--actor", "app:editor",
		"--namespace", "app/editor/session",
		"--key", "summary",
		"--json", `{"v":1}`,
	}); code != 0 {
		t.Fatalf("put failed: %s", errOut.String())
	}

	backupPath := filepath.Join(root, "backup.json")
	if code := cli.Run(context.Background(), []string{"context", "backup", "export", "--out", backupPath}); code != 0 {
		t.Fatalf("backup export failed: %s", errOut.String())
	}

	out.Reset()
	if code := cli.Run(context.Background(), []string{"context", "backup", "verify", "--in", backupPath}); code != 0 {
		t.Fatalf("backup verify failed: %s", errOut.String())
	}
	if !strings.Contains(out.String(), "\"verified\":true") {
		t.Fatalf("unexpected verify output: %s", out.String())
	}
}

func TestBootstrapCommandIdempotent(t *testing.T) {
	cli, out, errOut, _ := newTestCLIWithRoot(t)

	if code := cli.Run(context.Background(), []string{"context", "bootstrap", "--default-app", "editor", "--output", "json"}); code != 0 {
		t.Fatalf("bootstrap first run failed: %s", errOut.String())
	}
	first := out.String()
	if !strings.Contains(first, "\"bootstrapped\":true") {
		t.Fatalf("unexpected bootstrap output: %s", first)
	}

	out.Reset()
	if code := cli.Run(context.Background(), []string{"context", "bootstrap", "--default-app", "editor", "--output", "json"}); code != 0 {
		t.Fatalf("bootstrap second run failed: %s", errOut.String())
	}
	second := out.String()
	if !strings.Contains(second, "\"bootstrapped\":true") {
		t.Fatalf("unexpected second bootstrap output: %s", second)
	}

	out.Reset()
	if code := cli.Run(context.Background(), []string{"context", "namespace", "show", "--namespace", "user/goals"}); code != 0 {
		t.Fatalf("namespace show failed: %s", errOut.String())
	}
	if !strings.Contains(out.String(), "\"owner_type\":\"user\"") {
		t.Fatalf("expected user namespace from bootstrap, got: %s", out.String())
	}
}

func TestCompactCommand(t *testing.T) {
	cli, out, errOut, _ := newTestCLIWithRoot(t)
	for i := 1; i <= 4; i++ {
		if code := cli.Run(context.Background(), []string{
			"context", "put",
			"--client-id", "editor",
			"--actor", "app:editor",
			"--namespace", "app/editor/session",
			"--key", "summary",
			"--json", `{"n":` + string(rune('0'+i)) + `}`,
		}); code != 0 {
			t.Fatalf("put %d failed: %s", i, errOut.String())
		}
	}
	out.Reset()
	if code := cli.Run(context.Background(), []string{"context", "compact", "--keep-revisions", "2", "--keep-audit", "2", "--output", "table"}); code != 0 {
		t.Fatalf("compact failed: %s", errOut.String())
	}
	if !strings.Contains(out.String(), "DELETED_REVISIONS") {
		t.Fatalf("unexpected compact output: %s", out.String())
	}
}

func TestContractListAndRunDryMode(t *testing.T) {
	cli, out, errOut, _ := newTestCLIWithRoot(t)

	out.Reset()
	if code := cli.Run(context.Background(), []string{"context", "contract", "list", "--output", "json"}); code != 0 {
		t.Fatalf("contract list failed: %s", errOut.String())
	}
	if !strings.Contains(out.String(), "\"count\"") || !strings.Contains(out.String(), "\"api\"") {
		t.Fatalf("unexpected contract list output: %s", out.String())
	}

	out.Reset()
	if code := cli.Run(context.Background(), []string{"context", "contract", "run", "--suite", "api", "--output", "json"}); code != 0 {
		t.Fatalf("contract run dry failed: %s", errOut.String())
	}
	if !strings.Contains(out.String(), "\"executed\":false") || !strings.Contains(out.String(), "APIContract") {
		t.Fatalf("unexpected contract run dry output: %s", out.String())
	}
}

func TestContractRunExecuteUsesInjectedRunner(t *testing.T) {
	cli, out, errOut, _ := newTestCLIWithRoot(t)
	var called int
	cli.ExecCommand = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		called++
		return []byte(fmt.Sprintf("ran %s %s", name, strings.Join(args, " "))), nil
	}

	out.Reset()
	if code := cli.Run(context.Background(), []string{"context", "contract", "run", "--suite", "metrics", "--execute", "--output", "json"}); code != 0 {
		t.Fatalf("contract run execute failed: %s", errOut.String())
	}
	if called != 1 {
		t.Fatalf("expected injected runner to be called once, got %d", called)
	}
	if !strings.Contains(out.String(), "\"executed\":true") || !strings.Contains(out.String(), "\"ok\":true") {
		t.Fatalf("unexpected execute output: %s", out.String())
	}
}

// extractCLIRequestID parses the request_id from the output of "context promote request".
// The output contains a line like "  Request ID:  req-...".
func extractCLIRequestID(t *testing.T, output string) string {
	t.Helper()
	for _, line := range strings.Split(output, "\n") {
		if strings.Contains(line, "Request ID:") {
			parts := strings.Fields(line)
			if len(parts) >= 3 {
				return parts[len(parts)-1]
			}
		}
	}
	t.Fatalf("request_id not found in promote request output: %s", output)
	return ""
}
