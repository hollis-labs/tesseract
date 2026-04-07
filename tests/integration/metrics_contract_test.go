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

type metricsGolden struct {
	TopKeys    []string `json:"top_keys"`
	TotalsKeys []string `json:"totals_keys"`
	RouteKeys  []string `json:"route_keys"`
}

func TestMetricsContractAgainstGolden(t *testing.T) {
	golden := loadMetricsGolden(t)
	srv := newMetricsServer(t)
	srv.EnableMetrics = true

	if code := callMetricsStatus(t, srv, http.MethodGet, "/v1/health/readiness", nil, nil); code != http.StatusOK {
		t.Fatalf("readiness status=%d", code)
	}
	if code := callMetricsStatus(t, srv, http.MethodGet, "/v1/context/head?namespace=app/editor/session", nil, nil); code != http.StatusBadRequest {
		t.Fatalf("head status=%d", code)
	}

	m1 := callMetricsJSON(t, srv, http.MethodGet, "/v1/metrics", nil, nil)
	checkMetricsShape(t, m1, golden)
	m2 := callMetricsJSON(t, srv, http.MethodGet, "/v1/metrics", nil, nil)
	checkMetricsShape(t, m2, golden)

	tot1 := mustMap(t, m1["totals"], "totals")
	tot2 := mustMap(t, m2["totals"], "totals")
	req1 := intFromAny(t, tot1["requests"], "totals.requests")
	req2 := intFromAny(t, tot2["requests"], "totals.requests")
	err1 := intFromAny(t, tot1["errors"], "totals.errors")
	err2 := intFromAny(t, tot2["errors"], "totals.errors")
	if req2 < req1 || err2 < err1 {
		t.Fatalf("metrics counters not monotonic: before=%v after=%v", tot1, tot2)
	}
}

func loadMetricsGolden(t *testing.T) metricsGolden {
	t.Helper()
	path := filepath.Join("fixtures", "metrics_contract_golden.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	var out metricsGolden
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal golden: %v", err)
	}
	return out
}

func newMetricsServer(t *testing.T) *contextapi.Server {
	t.Helper()
	s, err := contextstore.Open(context.Background(), contextstore.Config{RootDir: t.TempDir()})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return contextapi.NewServer(s, contextpolicy.New())
}

func callMetricsStatus(t *testing.T, h http.Handler, method, path string, body any, headers map[string]string) int {
	t.Helper()
	res := callMetricsRecorder(t, h, method, path, body, headers)
	return res.Code
}

func callMetricsJSON(t *testing.T, h http.Handler, method, path string, body any, headers map[string]string) map[string]any {
	t.Helper()
	res := callMetricsRecorder(t, h, method, path, body, headers)
	if res.Code < 200 || res.Code >= 300 {
		t.Fatalf("%s %s status=%d body=%s", method, path, res.Code, res.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	return out
}

func callMetricsRecorder(t *testing.T, h http.Handler, method, path string, body any, headers map[string]string) *httptest.ResponseRecorder {
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
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)
	return res
}

func checkMetricsShape(t *testing.T, payload map[string]any, golden metricsGolden) {
	t.Helper()
	for _, key := range golden.TopKeys {
		if _, ok := payload[key]; !ok {
			t.Fatalf("missing top key %q in payload %v", key, payload)
		}
	}
	totals := mustMap(t, payload["totals"], "totals")
	for _, key := range golden.TotalsKeys {
		if _, ok := totals[key]; !ok {
			t.Fatalf("missing totals key %q in %v", key, totals)
		}
	}
	routes, ok := payload["routes"].([]any)
	if !ok {
		t.Fatalf("routes not array: %T", payload["routes"])
	}
	if len(routes) == 0 {
		t.Fatalf("routes should not be empty")
	}
	for _, item := range routes {
		route := mustMap(t, item, "route")
		for _, key := range golden.RouteKeys {
			if _, ok := route[key]; !ok {
				t.Fatalf("missing route key %q in %v", key, route)
			}
		}
	}
}

func mustMap(t *testing.T, value any, name string) map[string]any {
	t.Helper()
	out, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("%s not object: %T", name, value)
	}
	return out
}

func intFromAny(t *testing.T, value any, name string) int64 {
	t.Helper()
	f, ok := value.(float64)
	if !ok {
		t.Fatalf("%s not number: %T", name, value)
	}
	return int64(f)
}
