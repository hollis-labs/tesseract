package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/vanta-conduit/internal/contextapi"
	"github.com/hollis-labs/vanta-conduit/internal/contextpolicy"
	"github.com/hollis-labs/vanta-conduit/internal/contextstore"
)

type contractSpec struct {
	Keys               []string `json:"keys"`
	RecordKeys         []string `json:"record_keys"`
	EvaluationMetaKeys []string `json:"evaluation_meta_keys"`
}

type contractGolden map[string]contractSpec

func TestAPIContractAgainstGolden(t *testing.T) {
	golden := loadGolden(t)
	srv := newContractServer(t)

	checkKeys(t, callJSON(t, srv, http.MethodPost, "/v1/context/write", map[string]any{
		"client_id": "editor",
		"actor":     "app:editor",
		"namespace": "app/editor/session",
		"key":       "summary",
		"payload":   map[string]any{"v": 1},
	}), golden["write"].Keys)

	checkKeys(t, callJSON(t, srv, http.MethodPost, "/v1/context/write", map[string]any{
		"client_id": "editor",
		"actor":     "app:editor",
		"namespace": "app/editor/session",
		"key":       "summary",
		"payload":   map[string]any{"v": 2},
	}), golden["write"].Keys)

	head := callJSON(t, srv, http.MethodGet, "/v1/context/head?namespace=app/editor/session&key=summary", nil)
	checkKeys(t, head, golden["head"].Keys)
	if rec, ok := head["record"].(map[string]any); ok {
		for _, key := range golden["head"].RecordKeys {
			if _, ok := rec[key]; !ok {
				t.Fatalf("head.record missing key %q", key)
			}
		}
	} else {
		t.Fatalf("head.record not object")
	}

	history := callJSON(t, srv, http.MethodGet, "/v1/context/history?namespace=app/editor/session&key=summary", nil)
	checkKeys(t, history, golden["history"].Keys)

	view := callJSON(t, srv, http.MethodPost, "/v1/views/evaluate", map[string]any{
		"selector": map[string]any{"namespaces": []string{"app/editor/*"}, "revision_scope": "all"},
	})
	checkKeys(t, view, golden["view"].Keys)
	if meta, ok := view["evaluation_meta"].(map[string]any); ok {
		for _, key := range golden["view"].EvaluationMetaKeys {
			if _, ok := meta[key]; !ok {
				t.Fatalf("view.evaluation_meta missing key %q", key)
			}
		}
	} else {
		t.Fatalf("view.evaluation_meta not object")
	}

	checkKeys(t, callJSON(t, srv, http.MethodGet, "/v1/context/audit?limit=5", nil), golden["audit"].Keys)
	checkKeys(t, callJSON(t, srv, http.MethodGet, "/v1/health/readiness", nil), golden["readiness"].Keys)
	checkKeys(t, callJSON(t, srv, http.MethodGet, "/v1/context/consistency/scan", nil), golden["consistency_scan"].Keys)
}

func loadGolden(t *testing.T) contractGolden {
	t.Helper()
	path := filepath.Join("fixtures", "api_contract_golden.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	var out contractGolden
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal golden: %v", err)
	}
	return out
}

func newContractServer(t *testing.T) *contextapi.Server {
	t.Helper()
	s, err := contextstore.Open(context.Background(), contextstore.Config{RootDir: t.TempDir()})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return contextapi.NewServer(s, contextpolicy.New())
}

func callJSON(t *testing.T, h http.Handler, method, path string, body any) map[string]any {
	t.Helper()
	var payload []byte
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		payload = b
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(payload))
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)
	if res.Code < 200 || res.Code >= 300 {
		t.Fatalf("%s %s status=%d body=%s", method, path, res.Code, res.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	return out
}

func checkKeys(t *testing.T, payload map[string]any, keys []string) {
	t.Helper()
	for _, key := range keys {
		if _, ok := payload[key]; !ok {
			t.Fatalf("missing key %q in payload %v", key, payload)
		}
	}
}
