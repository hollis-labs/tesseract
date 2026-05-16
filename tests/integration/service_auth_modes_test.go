package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hollis-labs/tesseract/internal/contextapi"
	"github.com/hollis-labs/tesseract/internal/contextpolicy"
	"github.com/hollis-labs/tesseract/internal/contextstore"
)

func TestServiceModeStaticAuthAndReadRouteAccess(t *testing.T) {
	srv := newServiceAuthServer(t)
	srv.AuthToken = "static-secret"

	read := serviceJSON(t, srv, http.MethodGet, "/v1/health/readiness", nil, nil)
	if read.Code != http.StatusOK {
		t.Fatalf("readiness should be accessible without token, got %d body=%s", read.Code, read.Body.String())
	}

	unauth := serviceJSON(t, srv, http.MethodPost, "/v1/context/write", map[string]any{
		"client_id": "editor",
		"actor":     "app:editor",
		"namespace": "app/editor/session",
		"key":       "summary",
		"payload":   map[string]any{"v": 1},
	}, nil)
	if unauth.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without static token, got %d body=%s", unauth.Code, unauth.Body.String())
	}

	authed := serviceJSON(t, srv, http.MethodPost, "/v1/context/write", map[string]any{
		"client_id": "editor",
		"actor":     "app:editor",
		"namespace": "app/editor/session",
		"key":       "summary",
		"payload":   map[string]any{"v": 1},
	}, map[string]string{"Authorization": "Bearer static-secret"})
	if authed.Code != http.StatusOK {
		t.Fatalf("expected 200 with static token, got %d body=%s", authed.Code, authed.Body.String())
	}
}

func TestServiceModeManagedAuthAcceptRejectAndReadRouteAccess(t *testing.T) {
	srv := newServiceAuthServer(t)
	srv.ManagedAuth = true

	active, _, err := srv.Store.IssueAuthToken(context.Background(), "active", time.Hour)
	if err != nil {
		t.Fatalf("issue active token: %v", err)
	}
	revoked, _, err := srv.Store.IssueAuthToken(context.Background(), "revoked", time.Hour)
	if err != nil {
		t.Fatalf("issue revoked token: %v", err)
	}
	if err := srv.Store.RevokeAuthToken(context.Background(), revoked); err != nil {
		t.Fatalf("revoke token: %v", err)
	}

	read := serviceJSON(t, srv, http.MethodGet, "/v1/context/head?namespace=app/editor/session&key=summary", nil, nil)
	if read.Code != http.StatusNotFound {
		t.Fatalf("expected read endpoint to be accessible without token (404 head), got %d body=%s", read.Code, read.Body.String())
	}

	missing := serviceJSON(t, srv, http.MethodPost, "/v1/context/write", map[string]any{
		"client_id": "editor",
		"actor":     "app:editor",
		"namespace": "app/editor/session",
		"key":       "summary",
		"payload":   map[string]any{"v": 1},
	}, nil)
	if missing.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without managed token, got %d body=%s", missing.Code, missing.Body.String())
	}

	deny := serviceJSON(t, srv, http.MethodPost, "/v1/context/write", map[string]any{
		"client_id": "editor",
		"actor":     "app:editor",
		"namespace": "app/editor/session",
		"key":       "summary",
		"payload":   map[string]any{"v": 1},
	}, map[string]string{"Authorization": "Bearer " + revoked})
	if deny.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 with revoked token, got %d body=%s", deny.Code, deny.Body.String())
	}

	allow := serviceJSON(t, srv, http.MethodPost, "/v1/context/write", map[string]any{
		"client_id": "editor",
		"actor":     "app:editor",
		"namespace": "app/editor/session",
		"key":       "summary",
		"payload":   map[string]any{"v": 1},
	}, map[string]string{"Authorization": "Bearer " + active})
	if allow.Code != http.StatusOK {
		t.Fatalf("expected 200 with active managed token, got %d body=%s", allow.Code, allow.Body.String())
	}
}

func newServiceAuthServer(t *testing.T) *contextapi.Server {
	t.Helper()
	s, err := contextstore.Open(context.Background(), contextstore.Config{RootDir: t.TempDir()})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return contextapi.NewServer(s, contextpolicy.New())
}

func serviceJSON(t *testing.T, h http.Handler, method, path string, body any, headers map[string]string) *httptest.ResponseRecorder {
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
