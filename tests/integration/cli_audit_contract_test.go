package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/hollis-labs/vanta-conduit/internal/contextcli"
	"github.com/hollis-labs/vanta-conduit/internal/contextpolicy"
	"github.com/hollis-labs/vanta-conduit/internal/contextstore"
)

type cliAuditGolden struct {
	TopKeys           []string `json:"top_keys"`
	ItemKeys          []string `json:"item_keys"`
	FilteredNamespace string   `json:"filtered_namespace"`
	FilteredEventType string   `json:"filtered_event_type"`
}

func TestCLIAuditContractAgainstGolden(t *testing.T) {
	golden := loadCLIAuditGolden(t)
	store, err := contextstore.Open(context.Background(), contextstore.Config{RootDir: t.TempDir()})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	cli := &contextcli.CLI{Store: store, Policy: contextpolicy.New(), Stdout: out, Stderr: errOut}

	for i := 1; i <= 2; i++ {
		if code := cli.Run(context.Background(), []string{
			"context", "put",
			"--client-id", "editor",
			"--actor", "app:editor",
			"--namespace", "app/editor/session",
			"--key", "summary",
			"--json", `{"v":` + strconv.Itoa(i) + `}`,
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
	requestID := extractIntegrationRequestID(t, out.String())
	out.Reset()
	if code := cli.Run(context.Background(), []string{
		"context", "promote", "accept", requestID, "--actor", "user",
	}); code != 0 {
		t.Fatalf("promote accept failed: %s", errOut.String())
	}
	out.Reset()

	pageOne := runCLIAuditJSON(t, cli, out, errOut, []string{"--limit", "1"})
	checkKeys(t, pageOne, golden.TopKeys)
	items := mustItems(t, pageOne)
	if len(items) != 1 {
		t.Fatalf("expected one item on first page, got %d", len(items))
	}
	for _, key := range golden.ItemKeys {
		if _, ok := items[0][key]; !ok {
			t.Fatalf("missing item key %q in %v", key, items[0])
		}
	}
	next := mustNextCursor(t, pageOne)

	filtered := runCLIAuditJSON(t, cli, out, errOut, []string{
		"--cursor", strconv.FormatInt(next, 10),
		"--namespace", golden.FilteredNamespace,
		"--event-type", golden.FilteredEventType,
		"--output", "json",
	})
	checkKeys(t, filtered, golden.TopKeys)
	filteredItems := mustItems(t, filtered)
	if len(filteredItems) > 1 {
		t.Fatalf("expected <=1 filtered item, got %d", len(filteredItems))
	}
	if len(filteredItems) == 1 {
		if filteredItems[0]["namespace"] != golden.FilteredNamespace || filteredItems[0]["event_type"] != golden.FilteredEventType {
			t.Fatalf("unexpected filtered item: %v", filteredItems[0])
		}
	}
}

func runCLIAuditJSON(t *testing.T, cli *contextcli.CLI, out, errOut *bytes.Buffer, flags []string) map[string]any {
	t.Helper()
	out.Reset()
	errOut.Reset()
	args := append([]string{"context", "audit", "--output", "json"}, flags...)
	if code := cli.Run(context.Background(), args); code != 0 {
		t.Fatalf("audit command failed: %s", errOut.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal audit json: %v", err)
	}
	return payload
}

func loadCLIAuditGolden(t *testing.T) cliAuditGolden {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("fixtures", "cli_audit_contract_golden.json"))
	if err != nil {
		t.Fatalf("read cli audit golden: %v", err)
	}
	var out cliAuditGolden
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal cli audit golden: %v", err)
	}
	return out
}

func mustItems(t *testing.T, payload map[string]any) []map[string]any {
	t.Helper()
	raw, ok := payload["items"].([]any)
	if !ok {
		t.Fatalf("items not array: %T", payload["items"])
	}
	items := make([]map[string]any, 0, len(raw))
	for _, v := range raw {
		m, ok := v.(map[string]any)
		if !ok {
			t.Fatalf("item not object: %T", v)
		}
		items = append(items, m)
	}
	return items
}

// extractIntegrationRequestID parses the request_id from "context promote request" output.
func extractIntegrationRequestID(t *testing.T, output string) string {
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

func mustNextCursor(t *testing.T, payload map[string]any) int64 {
	t.Helper()
	v, ok := payload["next_cursor"]
	if !ok || v == nil {
		t.Fatalf("expected non-nil next_cursor in payload: %v", payload)
	}
	f, ok := v.(float64)
	if !ok {
		t.Fatalf("next_cursor not number: %T", v)
	}
	return int64(f)
}
