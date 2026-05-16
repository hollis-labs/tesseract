package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/tesseract/internal/contextapi"
	"github.com/hollis-labs/tesseract/internal/contextpolicy"
	"github.com/hollis-labs/tesseract/internal/contextstore"
)

type logMetricsGolden struct {
	LogRequiredKeys          []string `json:"log_required_keys"`
	MetricsRouteRequiredKeys []string `json:"metrics_route_required_keys"`
	RedactedQueryValue       string   `json:"redacted_query_value"`
}

func TestLogMetricsContractByRequestLogMode(t *testing.T) {
	golden := loadLogMetricsGolden(t)

	redactedLog := runLogMetricsModeCase(t, "redacted")
	assertLogEnvelope(t, redactedLog.entry, golden.LogRequiredKeys)
	if redactedLog.entry["query"] != golden.RedactedQueryValue {
		t.Fatalf("expected redacted query, got %v", redactedLog.entry["query"])
	}
	assertMetricsRouteEnvelope(t, redactedLog.metrics, golden.MetricsRouteRequiredKeys)

	fullLog := runLogMetricsModeCase(t, "full")
	assertLogEnvelope(t, fullLog.entry, golden.LogRequiredKeys)
	if fullLog.entry["query"] == golden.RedactedQueryValue {
		t.Fatalf("expected full query, got redacted payload: %v", fullLog.entry)
	}
	q, _ := fullLog.entry["query"].(string)
	if !strings.Contains(q, "token=visible") {
		t.Fatalf("expected full query to include token=visible, got %q", q)
	}
	assertMetricsRouteEnvelope(t, fullLog.metrics, golden.MetricsRouteRequiredKeys)
}

type modeResult struct {
	entry   map[string]any
	metrics map[string]any
}

func runLogMetricsModeCase(t *testing.T, mode string) modeResult {
	t.Helper()
	s, err := contextstore.Open(context.Background(), contextstore.Config{RootDir: t.TempDir()})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	buf := &bytes.Buffer{}
	srv := contextapi.NewServer(s, contextpolicy.New())
	srv.EnableMetrics = true
	srv.EnableRequestLogging = true
	srv.RequestLogMode = mode
	srv.LogWriter = buf

	res := callJSON(t, srv, http.MethodGet, "/v1/health/readiness?token=visible&session=abc", nil)
	checkKeys(t, res, []string{"healthy", "status"})

	line := strings.TrimSpace(buf.String())
	if line == "" {
		t.Fatalf("expected request log line")
	}
	var entry map[string]any
	if err := json.Unmarshal([]byte(line), &entry); err != nil {
		t.Fatalf("unmarshal request log: %v", err)
	}

	metrics := callJSON(t, srv, http.MethodGet, "/v1/metrics", nil)
	return modeResult{entry: entry, metrics: metrics}
}

func assertLogEnvelope(t *testing.T, entry map[string]any, keys []string) {
	t.Helper()
	for _, key := range keys {
		if _, ok := entry[key]; !ok {
			t.Fatalf("missing log key %q in %v", key, entry)
		}
	}
}

func assertMetricsRouteEnvelope(t *testing.T, metrics map[string]any, keys []string) {
	t.Helper()
	routesRaw, ok := metrics["routes"].([]any)
	if !ok || len(routesRaw) == 0 {
		t.Fatalf("metrics routes missing or empty: %v", metrics)
	}
	route, ok := routesRaw[0].(map[string]any)
	if !ok {
		t.Fatalf("metrics route shape invalid: %T", routesRaw[0])
	}
	for _, key := range keys {
		if _, ok := route[key]; !ok {
			t.Fatalf("missing metrics route key %q in %v", key, route)
		}
	}
}

func loadLogMetricsGolden(t *testing.T) logMetricsGolden {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("fixtures", "log_metrics_contract_golden.json"))
	if err != nil {
		t.Fatalf("read log/metrics golden: %v", err)
	}
	var out logMetricsGolden
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal log/metrics golden: %v", err)
	}
	return out
}
