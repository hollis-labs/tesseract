package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/hollis-labs/tesseract/internal/contextapi"
	"github.com/hollis-labs/tesseract/internal/contextpolicy"
	"github.com/hollis-labs/tesseract/internal/contextstore"
)

type auditContractGolden struct {
	TopKeys         []string `json:"top_keys"`
	ItemKeys        []string `json:"item_keys"`
	PageSize        int      `json:"page_size"`
	FilterNamespace string   `json:"filter_namespace"`
	FilterEventType string   `json:"filter_event_type"`
}

func TestAuditContractAgainstGolden(t *testing.T) {
	golden := loadAuditGolden(t)
	srv := newAuditContractServer(t)

	for i := 0; i < 3; i++ {
		res := callJSON(t, srv, http.MethodPost, "/v1/context/write", map[string]any{
			"client_id": "editor",
			"actor":     "app:editor",
			"namespace": "app/editor/session",
			"key":       "summary",
			"payload":   map[string]any{"v": i + 1},
		})
		checkKeys(t, res, []string{"record_id", "revision"})
	}

	reqRes := callJSON(t, srv, http.MethodPost, "/v1/context/promote/request", map[string]any{
		"actor":            "app:editor",
		"client_id":        "editor",
		"source_namespace": "app/editor/session",
		"source_key":       "summary",
		"target_namespace": "user/notes",
		"target_key":       "daily",
	})
	requestID := reqRes["request_id"].(string)
	callJSON(t, srv, http.MethodPost, "/v1/context/promote/approve", map[string]any{
		"actor":      "user",
		"request_id": requestID,
	})
	callJSON(t, srv, http.MethodPost, "/v1/context/promote/apply", map[string]any{
		"actor":      "user",
		"request_id": requestID,
	})

	pageOne := callJSON(t, srv, http.MethodGet, "/v1/context/audit?limit="+strconv.Itoa(golden.PageSize), nil)
	checkKeys(t, pageOne, golden.TopKeys)
	assertAuditItems(t, pageOne, golden.ItemKeys)

	next := extractNextCursor(t, pageOne)
	if next == nil {
		t.Fatalf("expected non-nil next_cursor for first page")
	}

	pageTwo := callJSON(t, srv, http.MethodGet, "/v1/context/audit?limit="+strconv.Itoa(golden.PageSize)+"&cursor="+strconv.FormatInt(*next, 10), nil)
	checkKeys(t, pageTwo, golden.TopKeys)
	assertAuditItems(t, pageTwo, golden.ItemKeys)

	firstID := extractFirstID(t, pageOne)
	secondID := extractFirstID(t, pageTwo)
	if secondID >= firstID {
		t.Fatalf("expected cursor page to move older (second=%d first=%d)", secondID, firstID)
	}

	filtered := callJSON(t, srv, http.MethodGet,
		"/v1/context/audit?namespace="+golden.FilterNamespace+"&event_type="+golden.FilterEventType+"&limit=10", nil)
	checkKeys(t, filtered, golden.TopKeys)
	items := extractItems(t, filtered)
	if len(items) != 1 {
		t.Fatalf("expected one filtered item, got %d", len(items))
	}
	if items[0]["event_type"] != golden.FilterEventType || items[0]["namespace"] != golden.FilterNamespace {
		t.Fatalf("unexpected filtered item: %+v", items[0])
	}
}

func loadAuditGolden(t *testing.T) auditContractGolden {
	t.Helper()
	path := filepath.Join("fixtures", "audit_contract_golden.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read audit golden: %v", err)
	}
	var out auditContractGolden
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal audit golden: %v", err)
	}
	return out
}

func newAuditContractServer(t *testing.T) *contextapi.Server {
	t.Helper()
	s, err := contextstore.Open(context.Background(), contextstore.Config{RootDir: t.TempDir()})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return contextapi.NewServer(s, contextpolicy.New())
}

func assertAuditItems(t *testing.T, payload map[string]any, keys []string) {
	t.Helper()
	items := extractItems(t, payload)
	if len(items) == 0 {
		t.Fatalf("expected non-empty audit items")
	}
	for _, item := range items {
		for _, key := range keys {
			if _, ok := item[key]; !ok {
				t.Fatalf("missing audit item key %q in %v", key, item)
			}
		}
	}
}

func extractItems(t *testing.T, payload map[string]any) []map[string]any {
	t.Helper()
	raw, ok := payload["items"].([]any)
	if !ok {
		t.Fatalf("items not array: %T", payload["items"])
	}
	out := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		m, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("item not object: %T", item)
		}
		out = append(out, m)
	}
	return out
}

func extractNextCursor(t *testing.T, payload map[string]any) *int64 {
	t.Helper()
	v, ok := payload["next_cursor"]
	if !ok || v == nil {
		return nil
	}
	n, ok := v.(float64)
	if !ok {
		t.Fatalf("next_cursor not number: %T", v)
	}
	out := int64(n)
	return &out
}

func extractFirstID(t *testing.T, payload map[string]any) int64 {
	t.Helper()
	items := extractItems(t, payload)
	if len(items) == 0 {
		t.Fatalf("no items")
	}
	n, ok := items[0]["id"].(float64)
	if !ok {
		t.Fatalf("id not number: %T", items[0]["id"])
	}
	return int64(n)
}
