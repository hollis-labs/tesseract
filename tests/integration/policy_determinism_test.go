package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/hollis-labs/vanta-conduit/internal/contextapi"
	"github.com/hollis-labs/vanta-conduit/internal/contextpolicy"
	"github.com/hollis-labs/vanta-conduit/internal/contextstore"
)

func TestPolicyAndDeterminismEndToEnd(t *testing.T) {
	srv := newServer(t)

	if got := perform(t, srv, http.MethodPost, "/v1/context/write", map[string]any{
		"client_id": "editor",
		"actor":     "app:editor",
		"namespace": "app/other/session",
		"key":       "k",
		"payload":   map[string]any{"v": 1},
	}); got.Code != http.StatusForbidden {
		t.Fatalf("cross-namespace write expected 403 got %d body=%s", got.Code, got.Body.String())
	}

	for _, payload := range []map[string]any{{"v": 1}, {"v": 2}} {
		if got := perform(t, srv, http.MethodPost, "/v1/context/write", map[string]any{
			"client_id": "editor",
			"actor":     "app:editor",
			"namespace": "app/editor/session",
			"key":       "summary",
			"payload":   payload,
		}); got.Code != http.StatusOK {
			t.Fatalf("valid app write expected 200 got %d body=%s", got.Code, got.Body.String())
		}
	}

	// Gated promotion: request by app actor, approve + apply by user.
	reqRes := perform(t, srv, http.MethodPost, "/v1/context/promote/request", map[string]any{
		"actor":            "app:editor",
		"client_id":        "editor",
		"source_namespace": "app/editor/session",
		"source_key":       "summary",
		"target_namespace": "user/notes",
		"target_key":       "daily",
	})
	if reqRes.Code != http.StatusOK {
		t.Fatalf("promote/request expected 200 got %d body=%s", reqRes.Code, reqRes.Body.String())
	}
	var reqResp map[string]any
	json.Unmarshal(reqRes.Body.Bytes(), &reqResp)
	requestID := reqResp["request_id"].(string)

	apprRes := perform(t, srv, http.MethodPost, "/v1/context/promote/approve", map[string]any{
		"actor":      "user",
		"request_id": requestID,
	})
	if apprRes.Code != http.StatusOK {
		t.Fatalf("promote/approve expected 200 got %d body=%s", apprRes.Code, apprRes.Body.String())
	}

	// Apply without user actor to user/* target must be forbidden.
	if got := perform(t, srv, http.MethodPost, "/v1/context/promote/apply", map[string]any{
		"actor":      "app:editor",
		"request_id": requestID,
	}); got.Code != http.StatusForbidden {
		t.Fatalf("promote/apply without user actor expected 403 got %d body=%s", got.Code, got.Body.String())
	}

	// Apply with user actor must succeed.
	if got := perform(t, srv, http.MethodPost, "/v1/context/promote/apply", map[string]any{
		"actor":      "user",
		"request_id": requestID,
	}); got.Code != http.StatusOK {
		t.Fatalf("promote/apply with user actor expected 200 got %d body=%s", got.Code, got.Body.String())
	}

	headBefore := perform(t, srv, http.MethodGet, "/v1/context/head?namespace=app/editor/session&key=summary", nil)
	if headBefore.Code != http.StatusOK {
		t.Fatalf("head before view expected 200 got %d body=%s", headBefore.Code, headBefore.Body.String())
	}

	selectorReq := map[string]any{
		"selector": map[string]any{
			"namespaces":     []string{"app/editor/*", "user/*"},
			"revision_scope": "all",
			"order":          []string{"namespace", "key", "revision"},
		},
		"include_payload": true,
	}
	viewA := perform(t, srv, http.MethodPost, "/v1/views/evaluate", selectorReq)
	viewB := perform(t, srv, http.MethodPost, "/v1/views/evaluate", selectorReq)
	if viewA.Code != http.StatusOK || viewB.Code != http.StatusOK {
		t.Fatalf("view expected 200 got %d/%d", viewA.Code, viewB.Code)
	}

	var a, b map[string]any
	if err := json.Unmarshal(viewA.Body.Bytes(), &a); err != nil {
		t.Fatalf("unmarshal viewA: %v", err)
	}
	if err := json.Unmarshal(viewB.Body.Bytes(), &b); err != nil {
		t.Fatalf("unmarshal viewB: %v", err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("view responses differ across identical requests")
	}

	headAfter := perform(t, srv, http.MethodGet, "/v1/context/head?namespace=app/editor/session&key=summary", nil)
	if headAfter.Code != http.StatusOK {
		t.Fatalf("head after view expected 200 got %d body=%s", headAfter.Code, headAfter.Body.String())
	}
	if headBefore.Body.String() != headAfter.Body.String() {
		t.Fatalf("view should be side-effect free, head changed")
	}
}

func TestAuthGateForWriteAndPromote(t *testing.T) {
	srv := newServer(t)
	srv.AuthToken = "top-secret"

	withoutToken := perform(t, srv, http.MethodPost, "/v1/context/write", map[string]any{
		"client_id": "editor",
		"actor":     "app:editor",
		"namespace": "app/editor/session",
		"key":       "summary",
		"payload":   map[string]any{"v": 1},
	})
	if withoutToken.Code != http.StatusUnauthorized {
		t.Fatalf("write without token expected 401 got %d body=%s", withoutToken.Code, withoutToken.Body.String())
	}

	withToken := performWithHeaders(t, srv, http.MethodPost, "/v1/context/write", map[string]any{
		"client_id": "editor",
		"actor":     "app:editor",
		"namespace": "app/editor/session",
		"key":       "summary",
		"payload":   map[string]any{"v": 1},
	}, map[string]string{"Authorization": "Bearer top-secret"})
	if withToken.Code != http.StatusOK {
		t.Fatalf("write with token expected 200 got %d body=%s", withToken.Code, withToken.Body.String())
	}

	promoteDenied := performWithHeaders(t, srv, http.MethodPost, "/v1/context/promote/request", map[string]any{
		"actor":            "app:editor",
		"client_id":        "editor",
		"source_namespace": "app/editor/session",
		"source_key":       "summary",
		"target_namespace": "user/notes",
		"target_key":       "daily",
	}, map[string]string{"Authorization": "Bearer wrong"})
	if promoteDenied.Code != http.StatusUnauthorized {
		t.Fatalf("promote/request with wrong token expected 401 got %d body=%s", promoteDenied.Code, promoteDenied.Body.String())
	}

	promoteAllowed := performWithHeaders(t, srv, http.MethodPost, "/v1/context/promote/request", map[string]any{
		"actor":            "app:editor",
		"client_id":        "editor",
		"source_namespace": "app/editor/session",
		"source_key":       "summary",
		"target_namespace": "user/notes",
		"target_key":       "daily",
	}, map[string]string{"Authorization": "Bearer top-secret"})
	if promoteAllowed.Code != http.StatusOK {
		t.Fatalf("promote/request with token expected 200 got %d body=%s", promoteAllowed.Code, promoteAllowed.Body.String())
	}
}

func TestViewDefaultLimitIsBounded(t *testing.T) {
	srv := newServer(t)
	for i := 0; i < contextstore.DefaultSelectLimit+30; i++ {
		got := perform(t, srv, http.MethodPost, "/v1/context/write", map[string]any{
			"client_id": "editor",
			"actor":     "app:editor",
			"namespace": "app/editor/session",
			"key":       fmt.Sprintf("k-%03d", i),
			"payload":   map[string]any{"v": i},
		})
		if got.Code != http.StatusOK {
			t.Fatalf("write %d expected 200 got %d body=%s", i, got.Code, got.Body.String())
		}
	}

	view := perform(t, srv, http.MethodPost, "/v1/views/evaluate", map[string]any{
		"selector": map[string]any{
			"namespaces":     []string{"app/editor/*"},
			"revision_scope": "all",
		},
	})
	if view.Code != http.StatusOK {
		t.Fatalf("view expected 200 got %d body=%s", view.Code, view.Body.String())
	}

	var payload struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(view.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(payload.Items) != contextstore.DefaultSelectLimit {
		t.Fatalf("expected default bounded result size %d got %d", contextstore.DefaultSelectLimit, len(payload.Items))
	}
}

func newServer(t *testing.T) *contextapi.Server {
	t.Helper()
	s, err := contextstore.Open(context.Background(), contextstore.Config{RootDir: t.TempDir()})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return contextapi.NewServer(s, contextpolicy.New())
}

func perform(t *testing.T, h http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	return performWithHeaders(t, h, method, path, body, nil)
}

func performWithHeaders(t *testing.T, h http.Handler, method, path string, body any, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var payload []byte
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
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
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}
