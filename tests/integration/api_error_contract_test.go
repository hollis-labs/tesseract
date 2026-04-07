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

type errorCase struct {
	Status int    `json:"status"`
	Code   string `json:"code"`
}

type errorGolden struct {
	RequiredKeys    []string  `json:"required_keys"`
	ValidationError errorCase `json:"validation_error"`
	PolicyDenied    errorCase `json:"policy_denied"`
	AuthRequired    errorCase `json:"auth_required"`
	NotFound        errorCase `json:"not_found"`
}

func TestAPIErrorContractAgainstGolden(t *testing.T) {
	golden := loadErrorGolden(t)
	srv := newErrorContractServer(t)

	validationStatus, validation := callJSONError(t, srv, http.MethodGet, "/v1/context/head?namespace=app/editor/session", nil, nil)
	assertErrorEnvelope(t, validationStatus, validation, golden.RequiredKeys, golden.ValidationError)

	policyStatus, policy := callJSONError(t, srv, http.MethodPost, "/v1/context/write", map[string]any{
		"client_id": "editor",
		"actor":     "app:editor",
		"namespace": "app/other/session",
		"key":       "summary",
		"payload":   map[string]any{"v": 1},
	}, nil)
	assertErrorEnvelope(t, policyStatus, policy, golden.RequiredKeys, golden.PolicyDenied)

	srv.AuthToken = "contract-secret"
	authStatus, auth := callJSONError(t, srv, http.MethodPost, "/v1/context/write", map[string]any{
		"client_id": "editor",
		"actor":     "app:editor",
		"namespace": "app/editor/session",
		"key":       "summary",
		"payload":   map[string]any{"v": 1},
	}, nil)
	assertErrorEnvelope(t, authStatus, auth, golden.RequiredKeys, golden.AuthRequired)

	notFoundStatus, notFound := callJSONError(t, srv, http.MethodGet, "/v1/namespaces/get?namespace=user/missing", nil, nil)
	assertErrorEnvelope(t, notFoundStatus, notFound, golden.RequiredKeys, golden.NotFound)
}

func loadErrorGolden(t *testing.T) errorGolden {
	t.Helper()
	path := filepath.Join("fixtures", "api_error_contract_golden.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read error golden: %v", err)
	}
	var out errorGolden
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal error golden: %v", err)
	}
	return out
}

func newErrorContractServer(t *testing.T) *contextapi.Server {
	t.Helper()
	s, err := contextstore.Open(context.Background(), contextstore.Config{RootDir: t.TempDir()})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return contextapi.NewServer(s, contextpolicy.New())
}

func callJSONError(t *testing.T, h http.Handler, method, path string, body any, headers map[string]string) (int, map[string]any) {
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

	var out map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	return res.Code, out
}

func assertErrorEnvelope(t *testing.T, status int, payload map[string]any, required []string, want errorCase) {
	t.Helper()
	if status != want.Status {
		t.Fatalf("status mismatch: got=%d want=%d payload=%v", status, want.Status, payload)
	}
	for _, key := range required {
		if _, ok := payload[key]; !ok {
			t.Fatalf("missing key %q in payload %v", key, payload)
		}
	}
	if code, _ := payload["code"].(string); code != want.Code {
		t.Fatalf("error code mismatch: got=%q want=%q payload=%v", code, want.Code, payload)
	}
}
