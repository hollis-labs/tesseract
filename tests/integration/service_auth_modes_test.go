package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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

	// Reads are no longer open once a token mode is configured. Readiness is
	// the exception (above); content routes are not.
	unauthRead := serviceJSON(t, srv, http.MethodGet, "/v1/context/head?namespace=app/editor/session&key=summary", nil, nil)
	if unauthRead.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 on read without static token, got %d body=%s", unauthRead.Code, unauthRead.Body.String())
	}

	authedRead := serviceJSON(t, srv, http.MethodGet, "/v1/context/head?namespace=app/editor/session&key=summary", nil,
		map[string]string{"Authorization": "Bearer static-secret"})
	if authedRead.Code != http.StatusNotFound {
		t.Fatalf("expected 404 (no such head) with static token, got %d body=%s", authedRead.Code, authedRead.Body.String())
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

	// Managed auth guards reads too: without a token this is 401, and only a
	// valid token gets as far as the 404 that says the head does not exist.
	read := serviceJSON(t, srv, http.MethodGet, "/v1/context/head?namespace=app/editor/session&key=summary", nil, nil)
	if read.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 on read without managed token, got %d body=%s", read.Code, read.Body.String())
	}
	authedRead := serviceJSON(t, srv, http.MethodGet, "/v1/context/head?namespace=app/editor/session&key=summary", nil,
		map[string]string{"Authorization": "Bearer " + active})
	if authedRead.Code != http.StatusNotFound {
		t.Fatalf("expected 404 (no such head) with managed token, got %d body=%s", authedRead.Code, authedRead.Body.String())
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

// TestServiceModeTokenInventoryRequiresAuth pins the most severe of the reads
// that used to answer anonymously: /v1/auth/tokens/list returns every token's
// id, client, scopes, namespace globs and expiry — the full map of who can do
// what — and it did so to any caller that could reach the port.
func TestServiceModeTokenInventoryRequiresAuth(t *testing.T) {
	srv := newServiceAuthServer(t)
	srv.ManagedAuth = true

	admin, _, err := srv.Store.IssueAuthToken(context.Background(), "admin", time.Hour)
	if err != nil {
		t.Fatalf("issue admin token: %v", err)
	}

	anon := serviceJSON(t, srv, http.MethodGet, "/v1/auth/tokens/list", nil, nil)
	if anon.Code != http.StatusUnauthorized {
		t.Fatalf("token inventory answered %d without credentials: %s", anon.Code, anon.Body.String())
	}
	if strings.Contains(anon.Body.String(), "token_id") {
		t.Fatalf("token inventory leaked into an unauthenticated response: %s", anon.Body.String())
	}

	authed := serviceJSON(t, srv, http.MethodGet, "/v1/auth/tokens/list", nil,
		map[string]string{"Authorization": "Bearer " + admin})
	if authed.Code != http.StatusOK {
		t.Fatalf("token inventory rejected a valid token: %d %s", authed.Code, authed.Body.String())
	}
}

// TestServiceModeAdminReadsRequireAuth covers the admin GETs that disclosed
// filesystem layout, configuration, queue state and namespace history.
func TestServiceModeAdminReadsRequireAuth(t *testing.T) {
	srv := newServiceAuthServer(t)
	srv.AuthToken = "admin-secret"

	adminReads := []string{
		"/v1/admin/setup",
		"/v1/admin/settings",
		"/v1/admin/storage",
		"/v1/admin/queue",
		"/v1/admin/queue/failures",
		"/v1/admin/config/backups",
		"/v1/admin/namespaces/history?namespace=app/editor/session",
	}
	for _, path := range adminReads {
		res := serviceJSON(t, srv, http.MethodGet, path, nil, nil)
		if res.Code != http.StatusUnauthorized {
			t.Errorf("GET %s answered %d without credentials (want 401): %s", path, res.Code, res.Body.String())
		}
	}
}
