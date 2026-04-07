package integration

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type makeSmokeGolden struct {
	Markers []string `json:"markers"`
}

func TestMakeSmokeInvalidTokenContractAgainstGolden(t *testing.T) {
	golden := loadMakeSmokeGolden(t)

	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/health/readiness":
			writeJSONResponse(w, http.StatusOK, map[string]any{"healthy": true})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/context/write":
			auth := strings.TrimSpace(r.Header.Get("Authorization"))
			if auth != "Bearer valid-token" {
				writeJSONResponse(w, http.StatusUnauthorized, map[string]any{"code": "auth_required"})
				return
			}
			writeJSONResponse(w, http.StatusOK, map[string]any{"record_id": "r1"})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/context/head":
			writeJSONResponse(w, http.StatusOK, map[string]any{"record": map[string]any{}})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/context/history":
			writeJSONResponse(w, http.StatusOK, map[string]any{"items": []any{}})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/views/evaluate":
			writeJSONResponse(w, http.StatusOK, map[string]any{"evaluation_meta": map[string]any{}})
		default:
			writeJSONResponse(w, http.StatusNotFound, map[string]any{"code": "not_found"})
		}
	})
	srv := httptest.NewServer(h)
	defer srv.Close()

	cmd := exec.Command("make", "smoke-invalid-token",
		"BASE_URL="+srv.URL,
		"AUTH_MODE=static",
		"TOKEN=valid-token",
	)
	cmd.Dir = filepath.Join("..", "..")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("make smoke-invalid-token failed: %v\noutput:\n%s", err, string(out))
	}
	output := string(out)
	pos := 0
	for _, marker := range golden.Markers {
		next := strings.Index(output[pos:], marker)
		if next < 0 {
			t.Fatalf("missing marker %q in output:\n%s", marker, output)
		}
		pos += next + len(marker)
	}
}

func loadMakeSmokeGolden(t *testing.T) makeSmokeGolden {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("fixtures", "make_smoke_invalid_token_contract_golden.json"))
	if err != nil {
		t.Fatalf("read make smoke golden: %v", err)
	}
	var out makeSmokeGolden
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal make smoke golden: %v", err)
	}
	return out
}
