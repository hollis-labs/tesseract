package contextapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/tesseract/domains"
	"github.com/hollis-labs/tesseract/internal/config"
	"github.com/hollis-labs/tesseract/internal/contextpolicy"
	"github.com/hollis-labs/tesseract/internal/contextstore"
	"github.com/hollis-labs/tesseract/internal/memory"
)

func newTestServer(t *testing.T) *Server {
	srv, _ := newTestServerWithRoot(t)
	return srv
}

func newTestServerWithRoot(t *testing.T) (*Server, string) {
	t.Helper()
	root := t.TempDir()
	s, err := contextstore.Open(context.Background(), contextstore.Config{RootDir: root})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return NewServer(s, contextpolicy.New()), root
}

func TestWriteAndHistorySuccess(t *testing.T) {
	srv := newTestServer(t)

	for i := 0; i < 2; i++ {
		body := map[string]any{
			"client_id": "editor",
			"actor":     "app:editor",
			"namespace": "app/editor/session",
			"key":       "summary",
			"payload":   map[string]any{"n": i + 1},
		}
		res := performJSON(t, srv, http.MethodPost, "/v1/context/write", body)
		if res.Code != http.StatusOK {
			t.Fatalf("write status=%d body=%s", res.Code, res.Body.String())
		}
	}

	res := performJSON(t, srv, http.MethodGet, "/v1/context/history?namespace=app/editor/session&key=summary", nil)
	if res.Code != http.StatusOK {
		t.Fatalf("history status=%d body=%s", res.Code, res.Body.String())
	}

	var payload struct {
		Items []struct {
			Revision int64 `json:"Revision"`
		} `json:"items"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(payload.Items) != 2 || payload.Items[0].Revision != 1 || payload.Items[1].Revision != 2 {
		t.Fatalf("unexpected history ordering: %+v", payload.Items)
	}
}

func TestPolicyDeniedWrite(t *testing.T) {
	srv := newTestServer(t)
	res := performJSON(t, srv, http.MethodPost, "/v1/context/write", map[string]any{
		"client_id": "editor",
		"actor":     "app:editor",
		"namespace": "app/other/session",
		"key":       "summary",
		"payload":   map[string]any{"ok": true},
	})
	if res.Code != http.StatusForbidden {
		t.Fatalf("expected 403 got %d body=%s", res.Code, res.Body.String())
	}
}

func TestValidationError(t *testing.T) {
	srv := newTestServer(t)
	res := performJSON(t, srv, http.MethodGet, "/v1/context/head?namespace=app/editor/session", nil)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 got %d body=%s", res.Code, res.Body.String())
	}
}

func TestViewDeterministicOrdering(t *testing.T) {
	srv := newTestServer(t)

	writes := []map[string]any{
		{"client_id": "editor", "actor": "app:editor", "namespace": "app/editor/session", "key": "b", "payload": map[string]any{"v": 1}},
		{"client_id": "editor", "actor": "app:editor", "namespace": "app/editor/session", "key": "a", "payload": map[string]any{"v": 2}},
		{"client_id": "editor", "actor": "app:editor", "namespace": "app/editor/session", "key": "a", "payload": map[string]any{"v": 3}},
	}
	for _, w := range writes {
		res := performJSON(t, srv, http.MethodPost, "/v1/context/write", w)
		if res.Code != http.StatusOK {
			t.Fatalf("write status=%d body=%s", res.Code, res.Body.String())
		}
	}

	selector := map[string]any{
		"selector": map[string]any{
			"namespaces":     []string{"app/editor/*"},
			"revision_scope": "all",
			"order":          []string{"namespace", "key", "revision"},
		},
		"include_payload": false,
	}

	a := performJSON(t, srv, http.MethodPost, "/v1/views/evaluate", selector)
	b := performJSON(t, srv, http.MethodPost, "/v1/views/evaluate", selector)
	if a.Code != http.StatusOK || b.Code != http.StatusOK {
		t.Fatalf("view failed: %d/%d", a.Code, b.Code)
	}

	var pa, pb map[string]any
	if err := json.Unmarshal(a.Body.Bytes(), &pa); err != nil {
		t.Fatalf("unmarshal A: %v", err)
	}
	if err := json.Unmarshal(b.Body.Bytes(), &pb); err != nil {
		t.Fatalf("unmarshal B: %v", err)
	}
	if !reflect.DeepEqual(pa, pb) {
		t.Fatalf("view response not deterministic")
	}
}

func TestMutatingEndpointsRequireBearerTokenWhenConfigured(t *testing.T) {
	srv := newTestServer(t)
	srv.AuthToken = "secret-token"

	withoutToken := performJSON(t, srv, http.MethodPost, "/v1/context/write", map[string]any{
		"client_id": "editor",
		"actor":     "app:editor",
		"namespace": "app/editor/session",
		"key":       "summary",
		"payload":   map[string]any{"n": 1},
	})
	if withoutToken.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without token, got %d body=%s", withoutToken.Code, withoutToken.Body.String())
	}

	withBadToken := performJSONWithHeaders(t, srv, http.MethodPost, "/v1/context/write", map[string]any{
		"client_id": "editor",
		"actor":     "app:editor",
		"namespace": "app/editor/session",
		"key":       "summary",
		"payload":   map[string]any{"n": 1},
	}, map[string]string{"Authorization": "Bearer wrong-token"})
	if withBadToken.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 with bad token, got %d body=%s", withBadToken.Code, withBadToken.Body.String())
	}

	withGoodToken := performJSONWithHeaders(t, srv, http.MethodPost, "/v1/context/write", map[string]any{
		"client_id": "editor",
		"actor":     "app:editor",
		"namespace": "app/editor/session",
		"key":       "summary",
		"payload":   map[string]any{"n": 1},
	}, map[string]string{"Authorization": "Bearer secret-token"})
	if withGoodToken.Code != http.StatusOK {
		t.Fatalf("expected 200 with good token, got %d body=%s", withGoodToken.Code, withGoodToken.Body.String())
	}
}

func TestViewRejectsExcessiveLimit(t *testing.T) {
	srv := newTestServer(t)
	res := performJSON(t, srv, http.MethodPost, "/v1/views/evaluate", map[string]any{
		"selector": map[string]any{
			"namespaces": []string{"app/editor/*"},
			"limit":      100000,
		},
	})
	if res.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for excessive limit, got %d body=%s", res.Code, res.Body.String())
	}
}

func TestConsistencyScanAndRepairEndpoints(t *testing.T) {
	srv, root := newTestServerWithRoot(t)
	if _, err := srv.Store.AppendRecord(context.Background(), contextstore.AppendInput{
		Namespace: "app/editor/session",
		Key:       "summary",
		Actor:     "app:editor",
		Payload:   json.RawMessage(`{"v":1}`),
	}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if _, err := srv.Store.AppendRecord(context.Background(), contextstore.AppendInput{
		Namespace: "app/editor/session",
		Key:       "summary",
		Actor:     "app:editor",
		Payload:   json.RawMessage(`{"v":2}`),
	}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := os.Remove(filepath.Join(root, "data", "records", "app", "editor", "session", "summary", "2.json")); err != nil {
		t.Fatalf("remove payload: %v", err)
	}

	scan := performJSON(t, srv, http.MethodGet, "/v1/context/consistency/scan", nil)
	if scan.Code != http.StatusOK {
		t.Fatalf("scan status=%d body=%s", scan.Code, scan.Body.String())
	}
	var scanPayload struct {
		Count int `json:"count"`
	}
	if err := json.Unmarshal(scan.Body.Bytes(), &scanPayload); err != nil {
		t.Fatalf("unmarshal scan: %v", err)
	}
	if scanPayload.Count == 0 {
		t.Fatalf("expected at least one issue after payload removal")
	}

	srv.AuthToken = "repair-secret"
	repairUnauthorized := performJSON(t, srv, http.MethodPost, "/v1/context/consistency/repair", map[string]any{})
	if repairUnauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without token, got %d body=%s", repairUnauthorized.Code, repairUnauthorized.Body.String())
	}
	repair := performJSONWithHeaders(t, srv, http.MethodPost, "/v1/context/consistency/repair", map[string]any{}, map[string]string{
		"Authorization": "Bearer repair-secret",
	})
	if repair.Code != http.StatusOK {
		t.Fatalf("repair status=%d body=%s", repair.Code, repair.Body.String())
	}
}

func TestAuditEndpointIncludesWriteAndPromoteEvents(t *testing.T) {
	srv := newTestServer(t)

	write := performJSON(t, srv, http.MethodPost, "/v1/context/write", map[string]any{
		"client_id": "editor",
		"actor":     "app:editor",
		"namespace": "app/editor/session",
		"key":       "summary",
		"payload":   map[string]any{"v": 1},
	})
	if write.Code != http.StatusOK {
		t.Fatalf("write status=%d body=%s", write.Code, write.Body.String())
	}

	doGatedPromote(t, srv, "editor", "app/editor/session", "summary", "user/notes", "daily")

	audit := performJSON(t, srv, http.MethodGet, "/v1/context/audit?limit=10", nil)
	if audit.Code != http.StatusOK {
		t.Fatalf("audit status=%d body=%s", audit.Code, audit.Body.String())
	}

	var payload struct {
		Items []struct {
			EventType string `json:"event_type"`
			Actor     string `json:"actor"`
			Namespace string `json:"namespace"`
			Key       string `json:"key"`
			Revision  int64  `json:"revision"`
		} `json:"items"`
	}
	if err := json.Unmarshal(audit.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal audit: %v", err)
	}
	if len(payload.Items) < 2 {
		t.Fatalf("expected at least 2 audit events, got %d", len(payload.Items))
	}

	foundWrite := false
	foundPromote := false
	for _, item := range payload.Items {
		if item.EventType == "write" && item.Namespace == "app/editor/session" && item.Key == "summary" && item.Revision > 0 {
			foundWrite = true
		}
		if item.EventType == "promote" && item.Namespace == "user/notes" && item.Key == "daily" && item.Revision > 0 {
			foundPromote = true
		}
	}
	if !foundWrite || !foundPromote {
		t.Fatalf("expected write+promote audit events, got %+v", payload.Items)
	}
}

func TestAuditEndpointFiltersAndPagination(t *testing.T) {
	srv := newTestServer(t)

	for i := 0; i < 3; i++ {
		write := performJSON(t, srv, http.MethodPost, "/v1/context/write", map[string]any{
			"client_id": "editor",
			"actor":     "app:editor",
			"namespace": "app/editor/session",
			"key":       "summary",
			"payload":   map[string]any{"v": i + 1},
		})
		if write.Code != http.StatusOK {
			t.Fatalf("write status=%d body=%s", write.Code, write.Body.String())
		}
	}
	doGatedPromote(t, srv, "editor", "app/editor/session", "summary", "user/notes", "daily")

	pageOne := performJSON(t, srv, http.MethodGet, "/v1/context/audit?limit=2", nil)
	if pageOne.Code != http.StatusOK {
		t.Fatalf("audit page one status=%d body=%s", pageOne.Code, pageOne.Body.String())
	}
	var one struct {
		Count int `json:"count"`
		Items []struct {
			ID        int64  `json:"id"`
			EventType string `json:"event_type"`
			Namespace string `json:"namespace"`
		} `json:"items"`
		NextCursor *int64 `json:"next_cursor"`
	}
	if err := json.Unmarshal(pageOne.Body.Bytes(), &one); err != nil {
		t.Fatalf("unmarshal page one: %v", err)
	}
	if one.Count != 2 || one.NextCursor == nil {
		t.Fatalf("unexpected page one payload: %+v", one)
	}
	if one.Items[0].ID <= one.Items[1].ID {
		t.Fatalf("expected newest-first ordering: %+v", one.Items)
	}

	pageTwo := performJSON(t, srv, http.MethodGet, "/v1/context/audit?limit=2&cursor="+strconv.FormatInt(*one.NextCursor, 10), nil)
	if pageTwo.Code != http.StatusOK {
		t.Fatalf("audit page two status=%d body=%s", pageTwo.Code, pageTwo.Body.String())
	}
	var two struct {
		Count int `json:"count"`
		Items []struct {
			ID int64 `json:"id"`
		} `json:"items"`
	}
	if err := json.Unmarshal(pageTwo.Body.Bytes(), &two); err != nil {
		t.Fatalf("unmarshal page two: %v", err)
	}
	if two.Count == 0 {
		t.Fatalf("expected additional page items")
	}
	if two.Items[0].ID >= one.Items[len(one.Items)-1].ID {
		t.Fatalf("expected cursor window to move older: first_two=%d cursor_anchor=%d", two.Items[0].ID, one.Items[len(one.Items)-1].ID)
	}

	filtered := performJSON(t, srv, http.MethodGet, "/v1/context/audit?event_type=promote&namespace=user/notes", nil)
	if filtered.Code != http.StatusOK {
		t.Fatalf("filtered audit status=%d body=%s", filtered.Code, filtered.Body.String())
	}
	var fp struct {
		Count int `json:"count"`
		Items []struct {
			EventType string `json:"event_type"`
			Namespace string `json:"namespace"`
		} `json:"items"`
	}
	if err := json.Unmarshal(filtered.Body.Bytes(), &fp); err != nil {
		t.Fatalf("unmarshal filtered audit: %v", err)
	}
	if fp.Count != 1 || len(fp.Items) != 1 {
		t.Fatalf("expected exactly one filtered promote event, got %+v", fp)
	}
	if fp.Items[0].EventType != "promote" || fp.Items[0].Namespace != "user/notes" {
		t.Fatalf("unexpected filtered event: %+v", fp.Items[0])
	}
}

func TestManagedAuthRejectsRevokedAndExpiredTokens(t *testing.T) {
	srv := newTestServer(t)
	srv.ManagedAuth = true

	activeToken, _, err := srv.Store.IssueAuthToken(context.Background(), "active", time.Hour)
	if err != nil {
		t.Fatalf("issue active token: %v", err)
	}
	expiredToken, _, err := srv.Store.IssueAuthToken(context.Background(), "expired", time.Nanosecond)
	if err != nil {
		t.Fatalf("issue expired token: %v", err)
	}
	time.Sleep(2 * time.Millisecond)
	revokedToken, _, err := srv.Store.IssueAuthToken(context.Background(), "revoked", time.Hour)
	if err != nil {
		t.Fatalf("issue revoked token: %v", err)
	}
	if err := srv.Store.RevokeAuthToken(context.Background(), revokedToken); err != nil {
		t.Fatalf("revoke token: %v", err)
	}

	reqBody := map[string]any{
		"client_id": "editor",
		"actor":     "app:editor",
		"namespace": "app/editor/session",
		"key":       "summary",
		"payload":   map[string]any{"v": 1},
	}

	ok := performJSONWithHeaders(t, srv, http.MethodPost, "/v1/context/write", reqBody, map[string]string{
		"Authorization": "Bearer " + activeToken,
	})
	if ok.Code != http.StatusOK {
		t.Fatalf("active token expected 200 got %d body=%s", ok.Code, ok.Body.String())
	}

	expired := performJSONWithHeaders(t, srv, http.MethodPost, "/v1/context/write", reqBody, map[string]string{
		"Authorization": "Bearer " + expiredToken,
	})
	if expired.Code != http.StatusUnauthorized {
		t.Fatalf("expired token expected 401 got %d body=%s", expired.Code, expired.Body.String())
	}

	revoked := performJSONWithHeaders(t, srv, http.MethodPost, "/v1/context/write", reqBody, map[string]string{
		"Authorization": "Bearer " + revokedToken,
	})
	if revoked.Code != http.StatusUnauthorized {
		t.Fatalf("revoked token expected 401 got %d body=%s", revoked.Code, revoked.Body.String())
	}
}

func TestNamespaceSchemaRegistrationRetrievalAndWriteValidation(t *testing.T) {
	srv := newTestServer(t)
	reg := performJSON(t, srv, http.MethodPost, "/v1/namespaces/register", map[string]any{
		"namespace":  "app/editor/session",
		"owner_type": "app",
		"owner_id":   "editor",
		"policy": map[string]any{
			"required_keys": []string{"title", "summary"},
		},
	})
	if reg.Code != http.StatusOK {
		t.Fatalf("register status=%d body=%s", reg.Code, reg.Body.String())
	}

	get := performJSON(t, srv, http.MethodGet, "/v1/namespaces/get?namespace=app/editor/session", nil)
	if get.Code != http.StatusOK {
		t.Fatalf("get status=%d body=%s", get.Code, get.Body.String())
	}

	invalid := performJSON(t, srv, http.MethodPost, "/v1/context/write", map[string]any{
		"client_id": "editor",
		"actor":     "app:editor",
		"namespace": "app/editor/session",
		"key":       "summary",
		"payload":   map[string]any{"title": "only-title"},
	})
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for schema mismatch got %d body=%s", invalid.Code, invalid.Body.String())
	}

	valid := performJSON(t, srv, http.MethodPost, "/v1/context/write", map[string]any{
		"client_id": "editor",
		"actor":     "app:editor",
		"namespace": "app/editor/session",
		"key":       "summary",
		"payload":   map[string]any{"title": "t", "summary": "s"},
	})
	if valid.Code != http.StatusOK {
		t.Fatalf("expected 200 for schema-valid payload got %d body=%s", valid.Code, valid.Body.String())
	}
}

func TestReadinessEndpoint(t *testing.T) {
	srv := newTestServer(t)
	if _, err := srv.Store.AppendRecord(context.Background(), contextstore.AppendInput{
		Namespace: "app/editor/session",
		Key:       "summary",
		Actor:     "app:editor",
		Payload:   json.RawMessage(`{"v":1}`),
	}); err != nil {
		t.Fatalf("append: %v", err)
	}
	res := performJSON(t, srv, http.MethodGet, "/v1/health/readiness", nil)
	if res.Code != http.StatusOK {
		t.Fatalf("readiness status=%d body=%s", res.Code, res.Body.String())
	}
	var report struct {
		Healthy bool   `json:"healthy"`
		Status  string `json:"status"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &report); err != nil {
		t.Fatalf("unmarshal readiness: %v", err)
	}
	if !report.Healthy || report.Status != "healthy" {
		t.Fatalf("expected healthy readiness")
	}
}

func TestAdminSetupEndpoint(t *testing.T) {
	srv, root := newTestServerWithRoot(t)
	srv.EnableMetrics = true
	srv.EnableRequestLogging = true
	srv.RequestLogMode = "redacted"
	srv.ConfigFile = filepath.Join(root, "config.yaml")
	srv.QueueDBPath = filepath.Join(root, "queue.db")

	res := performJSON(t, srv, http.MethodGet, "/v1/admin/setup", nil)
	if res.Code != http.StatusOK {
		t.Fatalf("admin setup status=%d body=%s", res.Code, res.Body.String())
	}
	var payload struct {
		App  string `json:"app"`
		Auth struct {
			Mode string `json:"mode"`
		} `json:"auth"`
		Runtime struct {
			MetricsEnabled        bool   `json:"metrics_enabled"`
			RequestLoggingEnabled bool   `json:"request_logging_enabled"`
			RequestLogMode        string `json:"request_log_mode"`
		} `json:"runtime"`
		Config struct {
			EmbeddingModel           string  `json:"embedding_model"`
			DedupSimilarityThreshold float64 `json:"dedup_similarity_threshold"`
		} `json:"config"`
		Paths []struct {
			Label  string `json:"label"`
			Path   string `json:"path"`
			Exists bool   `json:"exists"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal admin setup: %v", err)
	}
	if payload.App != "tesseract" {
		t.Fatalf("app: got %q", payload.App)
	}
	if payload.Auth.Mode != "open" {
		t.Fatalf("auth mode: got %q", payload.Auth.Mode)
	}
	if !payload.Runtime.MetricsEnabled || !payload.Runtime.RequestLoggingEnabled || payload.Runtime.RequestLogMode != "redacted" {
		t.Fatalf("runtime: %+v", payload.Runtime)
	}
	if payload.Config.EmbeddingModel == "" || payload.Config.DedupSimilarityThreshold == 0 {
		t.Fatalf("config defaults not reported: %+v", payload.Config)
	}
	labels := map[string]bool{}
	for _, p := range payload.Paths {
		labels[p.Label] = p.Exists
	}
	if _, ok := labels["records"]; !ok {
		t.Fatalf("expected records path in %+v", payload.Paths)
	}
	if _, ok := labels["main-db"]; !ok {
		t.Fatalf("expected main-db path in %+v", payload.Paths)
	}
	if _, ok := labels["config-file"]; !ok {
		t.Fatalf("expected config-file path in %+v", payload.Paths)
	}
}

func TestAdminSettingsEndpoint(t *testing.T) {
	srv, root := newTestServerWithRoot(t)
	srv.EnableMetrics = true
	srv.EnableRequestLogging = true
	srv.RequestLogMode = "redacted"
	srv.ConfigFile = filepath.Join(root, "config.yaml")
	srv.QueueDBPath = filepath.Join(root, "queue.db")
	srv.RuntimeConfig = config.Config{
		Embedding: config.EmbeddingConfig{
			Provider: "openai",
			Model:    "text-embedding-3-large",
		},
		Dedup: config.DedupConfig{
			SimilarityThreshold: 0.91,
		},
		Synthesis: config.SynthesisConfig{
			Provider:    "anthropic",
			Model:       "claude-sonnet-4-5",
			MaxTokens:   2048,
			Temperature: 0.2,
		},
	}

	res := performJSON(t, srv, http.MethodGet, "/v1/admin/settings", nil)
	if res.Code != http.StatusOK {
		t.Fatalf("admin settings status=%d body=%s", res.Code, res.Body.String())
	}
	var payload struct {
		App        string `json:"app"`
		ConfigFile string `json:"config_file"`
		Auth       struct {
			Mode string `json:"mode"`
		} `json:"auth"`
		Runtime struct {
			MetricsEnabled bool `json:"metrics_enabled"`
			QueueEnabled   bool `json:"queue_enabled"`
			WebUIEmbedded  bool `json:"webui_embedded"`
		} `json:"runtime"`
		Config struct {
			EmbeddingProvider        string  `json:"embedding_provider"`
			DedupSimilarityThreshold float64 `json:"dedup_similarity_threshold"`
			SynthesisProvider        string  `json:"synthesis_provider"`
			SynthesisSystemPrompt    string  `json:"synthesis_system_prompt"`
		} `json:"config"`
		Providers struct {
			Embedding struct {
				Kind       string `json:"kind"`
				Provider   string `json:"provider"`
				EnvVar     string `json:"env_var"`
				Supported  bool   `json:"supported"`
				EnvPresent bool   `json:"env_present"`
				Available  bool   `json:"available"`
			} `json:"embedding"`
			Synthesis struct {
				Kind       string `json:"kind"`
				Provider   string `json:"provider"`
				EnvVar     string `json:"env_var"`
				Supported  bool   `json:"supported"`
				EnvPresent bool   `json:"env_present"`
				Available  bool   `json:"available"`
				Reason     string `json:"reason"`
			} `json:"synthesis"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal admin settings: %v", err)
	}
	if payload.App != "tesseract" {
		t.Fatalf("app: got %q", payload.App)
	}
	if payload.ConfigFile != srv.ConfigFile {
		t.Fatalf("config file: got %q want %q", payload.ConfigFile, srv.ConfigFile)
	}
	if payload.Auth.Mode != "open" {
		t.Fatalf("auth mode: got %q", payload.Auth.Mode)
	}
	if !payload.Runtime.MetricsEnabled || payload.Runtime.QueueEnabled || !payload.Runtime.WebUIEmbedded {
		t.Fatalf("runtime: %+v", payload.Runtime)
	}
	if payload.Config.EmbeddingProvider != "openai" || payload.Config.DedupSimilarityThreshold != 0.91 || payload.Config.SynthesisProvider != "anthropic" {
		t.Fatalf("config payload: %+v", payload.Config)
	}
	if payload.Config.SynthesisSystemPrompt != config.DefaultSynthesisSystemPrompt {
		t.Fatalf("expected normalized synthesis prompt, got %q", payload.Config.SynthesisSystemPrompt)
	}
	if payload.Providers.Embedding.Kind != "embedding" || payload.Providers.Embedding.Provider != "openai" || payload.Providers.Embedding.EnvVar != "OPENAI_API_KEY" || !payload.Providers.Embedding.Supported {
		t.Fatalf("embedding provider payload: %+v", payload.Providers.Embedding)
	}
	if payload.Providers.Synthesis.Kind != "synthesis" || payload.Providers.Synthesis.Provider != "anthropic" || payload.Providers.Synthesis.EnvVar != "ANTHROPIC_API_KEY" || !payload.Providers.Synthesis.Supported {
		t.Fatalf("synthesis provider payload: %+v", payload.Providers.Synthesis)
	}
	if payload.Providers.Synthesis.EnvPresent || payload.Providers.Synthesis.Available || payload.Providers.Synthesis.Reason == "" {
		t.Fatalf("expected missing env to be reflected in synthesis provider payload: %+v", payload.Providers.Synthesis)
	}
}

func TestAdminSettingsPreviewEndpoint(t *testing.T) {
	srv, root := newTestServerWithRoot(t)
	srv.ConfigFile = filepath.Join(root, "config.yaml")
	srv.RuntimeConfig = config.Config{
		Embedding: config.EmbeddingConfig{
			Provider: "openai",
			Model:    "text-embedding-3-large",
		},
		Dedup: config.DedupConfig{
			SimilarityThreshold: 0.85,
		},
	}
	req := map[string]any{
		"config": map[string]any{
			"embedding_provider":         "openai",
			"embedding_model":            "text-embedding-3-small",
			"dedup_similarity_threshold": 0.9,
			"synthesis_provider":         "anthropic",
			"synthesis_model":            "claude-sonnet-4-5",
			"synthesis_max_tokens":       2048,
			"synthesis_temperature":      0.2,
			"synthesis_system_prompt":    "Answer precisely.",
		},
	}

	res := performJSON(t, srv, http.MethodPost, "/v1/admin/settings/preview", req)
	if res.Code != http.StatusOK {
		t.Fatalf("preview status=%d body=%s", res.Code, res.Body.String())
	}
	var payload struct {
		Config struct {
			EmbeddingModel        string `json:"embedding_model"`
			SynthesisProvider     string `json:"synthesis_provider"`
			SynthesisSystemPrompt string `json:"synthesis_system_prompt"`
		} `json:"config"`
		ChangedFields   []string `json:"changed_fields"`
		Warnings        []string `json:"warnings"`
		RestartRequired bool     `json:"restart_required"`
		Applied         bool     `json:"applied"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal preview: %v", err)
	}
	if payload.Config.EmbeddingModel != "text-embedding-3-small" || payload.Config.SynthesisProvider != "anthropic" || payload.Config.SynthesisSystemPrompt != "Answer precisely." {
		t.Fatalf("unexpected preview config: %+v", payload.Config)
	}
	if len(payload.ChangedFields) == 0 || !payload.RestartRequired || payload.Applied {
		t.Fatalf("unexpected preview metadata: %+v", payload)
	}
	if len(payload.Warnings) == 0 {
		t.Fatalf("expected preview warnings")
	}
}

func TestAdminSettingsApplyEndpointWritesConfig(t *testing.T) {
	srv, root := newTestServerWithRoot(t)
	srv.ConfigFile = filepath.Join(root, "config.yaml")
	srv.RuntimeConfig = config.Defaults()
	req := map[string]any{
		"config": map[string]any{
			"embedding_provider":         "openai",
			"embedding_model":            "text-embedding-3-small",
			"dedup_similarity_threshold": 0.93,
			"synthesis_provider":         "openai",
			"synthesis_model":            "gpt-4.1-mini",
			"synthesis_max_tokens":       1024,
			"synthesis_temperature":      0.1,
			"synthesis_system_prompt":    "Use only supplied sources.",
		},
	}

	res := performJSON(t, srv, http.MethodPost, "/v1/admin/settings/apply", req)
	if res.Code != http.StatusOK {
		t.Fatalf("apply status=%d body=%s", res.Code, res.Body.String())
	}
	var payload struct {
		Applied bool `json:"applied"`
		Config  struct {
			EmbeddingModel string  `json:"embedding_model"`
			DedupThreshold float64 `json:"dedup_similarity_threshold"`
		} `json:"config"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal apply: %v", err)
	}
	if !payload.Applied || payload.Config.EmbeddingModel != "text-embedding-3-small" || payload.Config.DedupThreshold != 0.93 {
		t.Fatalf("unexpected apply payload: %+v", payload)
	}
	got, err := config.Load(srv.ConfigFile)
	if err != nil {
		t.Fatalf("load written config: %v", err)
	}
	if got.Embedding.Model != "text-embedding-3-small" || got.Dedup.SimilarityThreshold != 0.93 || got.Synthesis.Provider != "openai" {
		t.Fatalf("written config mismatch: %+v", got)
	}
	if srv.RuntimeConfig.Embedding.Model != "text-embedding-3-small" || srv.SynthesisConfig.Provider != "openai" {
		t.Fatalf("server runtime config not updated: runtime=%+v synthesis=%+v", srv.RuntimeConfig, srv.SynthesisConfig)
	}
}

func TestAdminSettingsPreviewEndpointRejectsInvalidThreshold(t *testing.T) {
	srv, root := newTestServerWithRoot(t)
	srv.ConfigFile = filepath.Join(root, "config.yaml")
	req := map[string]any{
		"config": map[string]any{
			"embedding_provider":         "openai",
			"embedding_model":            "text-embedding-3-large",
			"dedup_similarity_threshold": 2,
		},
	}
	res := performJSON(t, srv, http.MethodPost, "/v1/admin/settings/preview", req)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", res.Code, res.Body.String())
	}
}

func TestAdminConfigBackupAndRestoreEndpoints(t *testing.T) {
	srv, root := newTestServerWithRoot(t)
	srv.ConfigFile = filepath.Join(root, "config.yaml")
	if err := config.Save(srv.ConfigFile, config.Config{
		Embedding: config.EmbeddingConfig{Provider: "openai", Model: "text-embedding-3-large"},
		Dedup:     config.DedupConfig{SimilarityThreshold: 0.85},
	}); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	srv.RuntimeConfig, _ = config.Load(srv.ConfigFile)

	listRes := performJSON(t, srv, http.MethodGet, "/v1/admin/config/backups", nil)
	if listRes.Code != http.StatusOK {
		t.Fatalf("list backups status=%d body=%s", listRes.Code, listRes.Body.String())
	}
	var initial struct {
		Items []adminConfigBackupInfo `json:"items"`
	}
	if err := json.Unmarshal(listRes.Body.Bytes(), &initial); err != nil {
		t.Fatalf("unmarshal initial backups: %v", err)
	}
	if len(initial.Items) != 0 {
		t.Fatalf("expected no backups, got %+v", initial.Items)
	}

	backupRes := performJSON(t, srv, http.MethodPost, "/v1/admin/config/backup", map[string]any{})
	if backupRes.Code != http.StatusOK {
		t.Fatalf("create backup status=%d body=%s", backupRes.Code, backupRes.Body.String())
	}
	var backupPayload struct {
		Backup adminConfigBackupInfo `json:"backup"`
	}
	if err := json.Unmarshal(backupRes.Body.Bytes(), &backupPayload); err != nil {
		t.Fatalf("unmarshal backup: %v", err)
	}
	if backupPayload.Backup.Path == "" || backupPayload.Backup.Source == "" {
		t.Fatalf("unexpected backup payload: %+v", backupPayload.Backup)
	}

	if err := config.Save(srv.ConfigFile, config.Config{
		Embedding: config.EmbeddingConfig{Provider: "openai", Model: "text-embedding-3-small"},
		Dedup:     config.DedupConfig{SimilarityThreshold: 0.91},
		Synthesis: config.SynthesisConfig{Provider: "openai", Model: "gpt-4.1-mini"},
	}); err != nil {
		t.Fatalf("mutate config before restore: %v", err)
	}

	restoreRes := performJSON(t, srv, http.MethodPost, "/v1/admin/config/restore", map[string]any{
		"path": backupPayload.Backup.Path,
	})
	if restoreRes.Code != http.StatusOK {
		t.Fatalf("restore backup status=%d body=%s", restoreRes.Code, restoreRes.Body.String())
	}
	var restorePayload struct {
		Config struct {
			EmbeddingModel string `json:"embedding_model"`
		} `json:"config"`
		RestoredFrom     adminConfigBackupInfo `json:"restored_from"`
		PreRestoreBackup adminConfigBackupInfo `json:"pre_restore_backup"`
		RestartRequired  bool                  `json:"restart_required"`
	}
	if err := json.Unmarshal(restoreRes.Body.Bytes(), &restorePayload); err != nil {
		t.Fatalf("unmarshal restore payload: %v", err)
	}
	if restorePayload.Config.EmbeddingModel != "text-embedding-3-large" || !restorePayload.RestartRequired {
		t.Fatalf("unexpected restore payload: %+v", restorePayload)
	}
	if restorePayload.RestoredFrom.Path != backupPayload.Backup.Path || restorePayload.PreRestoreBackup.Path == "" {
		t.Fatalf("unexpected restore metadata: %+v", restorePayload)
	}
	got, err := config.Load(srv.ConfigFile)
	if err != nil {
		t.Fatalf("load restored config: %v", err)
	}
	if got.Embedding.Model != "text-embedding-3-large" {
		t.Fatalf("restore did not write expected config: %+v", got)
	}
}

func TestAdminConfigRestoreRejectsPathTraversal(t *testing.T) {
	srv, root := newTestServerWithRoot(t)
	srv.ConfigFile = filepath.Join(root, "config.yaml")
	res := performJSON(t, srv, http.MethodPost, "/v1/admin/config/restore", map[string]any{
		"path": "../outside.yaml",
	})
	if res.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", res.Code, res.Body.String())
	}
}

func TestAdminNamespacePreviewUpdateAndHistory(t *testing.T) {
	srv := newTestServer(t)
	seed := contextstore.NamespacePolicyEntry{
		Namespace: "app/demo/session",
		OwnerType: "app",
		OwnerID:   "demo",
		Policy: map[string]any{
			"tier":          "session",
			"retention":     "720h",
			"max_revisions": float64(10),
		},
	}
	if err := srv.Store.UpsertNamespacePolicy(context.Background(), seed); err != nil {
		t.Fatalf("seed namespace policy: %v", err)
	}
	if err := srv.Policy.RegisterNamespace(seed.Namespace, seed.OwnerType, seed.OwnerID, seed.Policy); err != nil {
		t.Fatalf("register seed namespace: %v", err)
	}

	previewRes := performJSON(t, srv, http.MethodPost, "/v1/admin/namespaces/preview", map[string]any{
		"namespace":  "app/demo/session",
		"owner_type": "app",
		"owner_id":   "demo",
		"policy": map[string]any{
			"tier":              "session",
			"retention":         "1440h",
			"max_revisions":     25,
			"max_bytes_per_key": 4096,
		},
	})
	if previewRes.Code != http.StatusOK {
		t.Fatalf("preview namespace status=%d body=%s", previewRes.Code, previewRes.Body.String())
	}
	var previewPayload struct {
		Exists        bool     `json:"exists"`
		ChangedFields []string `json:"changed_fields"`
		Entry         struct {
			Policy map[string]any `json:"policy"`
		} `json:"entry"`
	}
	if err := json.Unmarshal(previewRes.Body.Bytes(), &previewPayload); err != nil {
		t.Fatalf("unmarshal namespace preview: %v", err)
	}
	if !previewPayload.Exists || len(previewPayload.ChangedFields) == 0 {
		t.Fatalf("unexpected namespace preview payload: %+v", previewPayload)
	}

	updateRes := performJSON(t, srv, http.MethodPost, "/v1/admin/namespaces/update", map[string]any{
		"namespace":  "app/demo/session",
		"owner_type": "app",
		"owner_id":   "demo",
		"policy": map[string]any{
			"tier":              "session",
			"retention":         "1440h",
			"max_revisions":     25,
			"max_bytes_per_key": 4096,
		},
	})
	if updateRes.Code != http.StatusOK {
		t.Fatalf("update namespace status=%d body=%s", updateRes.Code, updateRes.Body.String())
	}
	got, err := srv.Store.GetNamespacePolicy(context.Background(), "app/demo/session")
	if err != nil {
		t.Fatalf("get updated namespace policy: %v", err)
	}
	if got.Policy["retention"] != "1440h" {
		t.Fatalf("expected updated retention, got %+v", got.Policy)
	}

	historyRes := performJSON(t, srv, http.MethodGet, "/v1/admin/namespaces/history?namespace=app/demo/session", nil)
	if historyRes.Code != http.StatusOK {
		t.Fatalf("namespace history status=%d body=%s", historyRes.Code, historyRes.Body.String())
	}
	var historyPayload struct {
		Count int `json:"count"`
		Items []struct {
			EventType string `json:"event_type"`
		} `json:"items"`
	}
	if err := json.Unmarshal(historyRes.Body.Bytes(), &historyPayload); err != nil {
		t.Fatalf("unmarshal namespace history: %v", err)
	}
	if historyPayload.Count == 0 || historyPayload.Items[0].EventType != contextstore.EventNamespaceUpdate {
		t.Fatalf("unexpected namespace history payload: %+v", historyPayload)
	}
}

func TestAdminNamespacePreviewRejectsBadRetention(t *testing.T) {
	srv := newTestServer(t)
	res := performJSON(t, srv, http.MethodPost, "/v1/admin/namespaces/preview", map[string]any{
		"namespace":  "app/demo/session",
		"owner_type": "app",
		"owner_id":   "demo",
		"policy": map[string]any{
			"retention": "not-a-duration",
		},
	})
	if res.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", res.Code, res.Body.String())
	}
}

func TestAdminQueueEndpoint(t *testing.T) {
	srv, root := newTestServerWithRoot(t)
	queueDBPath := filepath.Join(root, "queue.db")
	db, err := sql.Open("sqlite", queueDBPath)
	if err != nil {
		t.Fatalf("open queue db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`
CREATE TABLE jobs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    queue TEXT NOT NULL DEFAULT 'default',
    type TEXT NOT NULL,
    payload BLOB,
    attempts INTEGER NOT NULL DEFAULT 0,
    max_tries INTEGER NOT NULL DEFAULT 0,
    reserved_at INTEGER,
    available_at INTEGER NOT NULL,
    created_at INTEGER NOT NULL
);
CREATE TABLE failed_jobs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    queue TEXT NOT NULL,
    type TEXT NOT NULL,
    payload BLOB,
    error TEXT NOT NULL,
    attempts INTEGER NOT NULL,
    failed_at INTEGER NOT NULL
);`); err != nil {
		t.Fatalf("create queue schema: %v", err)
	}
	now := time.Now().Unix()
	if _, err := db.Exec(`
INSERT INTO jobs (queue, type, attempts, max_tries, reserved_at, available_at, created_at)
VALUES
  ('tesseract', 'embed', 0, 3, NULL, ?, ?),
  ('tesseract', 'embed', 1, 3, NULL, ?, ?),
  ('tesseract', 'embed', 1, 3, ?, ?, ?),
  ('other', 'embed', 0, 3, NULL, ?, ?);
INSERT INTO failed_jobs (queue, type, payload, error, attempts, failed_at)
VALUES ('tesseract', 'embed', NULL, 'boom', 3, ?);`,
		now-10, now-100,
		now+300, now-50,
		now-5, now-5, now-80,
		now-10, now-20,
		now-1,
	); err != nil {
		t.Fatalf("seed queue: %v", err)
	}
	srv.QueueDBPath = queueDBPath
	srv.QueueDB = db

	res := performJSON(t, srv, http.MethodGet, "/v1/admin/queue", nil)
	if res.Code != http.StatusOK {
		t.Fatalf("admin queue status=%d body=%s", res.Code, res.Body.String())
	}
	var payload struct {
		Enabled    bool `json:"enabled"`
		Total      int  `json:"total"`
		Available  int  `json:"available"`
		Delayed    int  `json:"delayed"`
		Reserved   int  `json:"reserved"`
		Failed     int  `json:"failed"`
		ActiveType []struct {
			Type  string `json:"type"`
			Count int    `json:"count"`
		} `json:"active_by_type"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal admin queue: %v", err)
	}
	if !payload.Enabled || payload.Total != 3 || payload.Available != 1 || payload.Delayed != 1 || payload.Reserved != 1 || payload.Failed != 1 {
		t.Fatalf("unexpected queue payload: %+v", payload)
	}
	if len(payload.ActiveType) != 1 || payload.ActiveType[0].Type != "embed" || payload.ActiveType[0].Count != 3 {
		t.Fatalf("unexpected active types: %+v", payload.ActiveType)
	}
}

func TestAdminQueueFailuresRetryAndBackfillEndpoints(t *testing.T) {
	srv, root := newTestServerWithRoot(t)
	queueDBPath := filepath.Join(root, "queue.db")
	db, err := sql.Open("sqlite", queueDBPath)
	if err != nil {
		t.Fatalf("open queue db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`
CREATE TABLE jobs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    queue TEXT NOT NULL DEFAULT 'default',
    type TEXT NOT NULL,
    payload BLOB,
    attempts INTEGER NOT NULL DEFAULT 0,
    max_tries INTEGER NOT NULL DEFAULT 0,
    reserved_at INTEGER,
    available_at INTEGER NOT NULL,
    created_at INTEGER NOT NULL
);
CREATE TABLE failed_jobs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    queue TEXT NOT NULL,
    type TEXT NOT NULL,
    payload BLOB,
    error TEXT NOT NULL,
    attempts INTEGER NOT NULL,
    failed_at INTEGER NOT NULL
);`); err != nil {
		t.Fatalf("create queue schema: %v", err)
	}
	now := time.Now().Unix()
	if _, err := db.Exec(`
INSERT INTO failed_jobs (queue, type, payload, error, attempts, failed_at)
VALUES
  ('tesseract', 'embed', '{"revision_id":"rev-failed"}', 'boom', 3, ?),
  ('other', 'embed', '{"revision_id":"rev-other"}', 'ignore', 2, ?);`,
		now-5, now-10,
	); err != nil {
		t.Fatalf("seed failed queue: %v", err)
	}
	srv.QueueDBPath = queueDBPath
	srv.QueueDB = db

	memStore := memory.NewStore(srv.Store.DB(), nil, "", 0, nil)
	if _, err := memStore.WriteRevision(context.Background(), memory.WriteInput{
		Domain:     domains.Memory,
		Namespace:  "user/chrispian/memory/notes",
		MemoryKey:  "rev_1",
		Author:     memory.Author{AgentID: "app:test"},
		Trigger:    memory.TriggerExplicit,
		SessionID:  "manual:01HXXXXX",
		Origin:     memory.OriginUser,
		Confidence: 0.9,
		Status:     memory.StatusDraft,
		Payload:    memory.Payload{Summary: "demo one"},
	}); err != nil {
		t.Fatalf("seed memory revision 1: %v", err)
	}
	if _, err := memStore.WriteRevision(context.Background(), memory.WriteInput{
		Domain:     domains.Memory,
		Namespace:  "user/chrispian/memory/notes",
		MemoryKey:  "rev_2",
		Author:     memory.Author{AgentID: "app:test"},
		Trigger:    memory.TriggerExplicit,
		SessionID:  "manual:01HXXXXX",
		Origin:     memory.OriginUser,
		Confidence: 0.9,
		Status:     memory.StatusDraft,
		Payload:    memory.Payload{Summary: "demo two"},
	}); err != nil {
		t.Fatalf("seed memory revision 2: %v", err)
	}
	rev3, err := memStore.WriteRevision(context.Background(), memory.WriteInput{
		Domain:     domains.Memory,
		Namespace:  "user/chrispian/memory/notes",
		MemoryKey:  "rev_3",
		Author:     memory.Author{AgentID: "app:test"},
		Trigger:    memory.TriggerExplicit,
		SessionID:  "manual:01HXXXXX",
		Origin:     memory.OriginUser,
		Confidence: 0.9,
		Status:     memory.StatusDraft,
		Payload:    memory.Payload{Summary: "other three"},
	})
	if err != nil {
		t.Fatalf("seed memory revision 3: %v", err)
	}
	if _, err := srv.Store.DB().Exec(
		`UPDATE memory_revisions SET embedding_model = ?, embedding_vector = ? WHERE revision_id = ?`,
		"test-model", []byte{1, 2}, rev3.RevisionID,
	); err != nil {
		t.Fatalf("mark embedded revision: %v", err)
	}

	failuresRes := performJSON(t, srv, http.MethodGet, "/v1/admin/queue/failures?limit=10", nil)
	if failuresRes.Code != http.StatusOK {
		t.Fatalf("queue failures status=%d body=%s", failuresRes.Code, failuresRes.Body.String())
	}
	var failuresPayload struct {
		Count int `json:"count"`
		Items []struct {
			ID    int64  `json:"id"`
			Error string `json:"error"`
			Type  string `json:"type"`
		} `json:"items"`
	}
	if err := json.Unmarshal(failuresRes.Body.Bytes(), &failuresPayload); err != nil {
		t.Fatalf("unmarshal queue failures: %v", err)
	}
	if failuresPayload.Count != 1 || len(failuresPayload.Items) != 1 || failuresPayload.Items[0].Type != "embed" || failuresPayload.Items[0].Error != "boom" {
		t.Fatalf("unexpected failures payload: %+v", failuresPayload)
	}

	retryRes := performJSON(t, srv, http.MethodPost, "/v1/admin/queue/retry-failed", map[string]any{
		"id": failuresPayload.Items[0].ID,
	})
	if retryRes.Code != http.StatusOK {
		t.Fatalf("queue retry status=%d body=%s", retryRes.Code, retryRes.Body.String())
	}
	var retryPayload struct {
		Retried int `json:"retried"`
	}
	if err := json.Unmarshal(retryRes.Body.Bytes(), &retryPayload); err != nil {
		t.Fatalf("unmarshal queue retry: %v", err)
	}
	if retryPayload.Retried != 1 {
		t.Fatalf("unexpected retry payload: %+v", retryPayload)
	}
	var jobCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM jobs WHERE queue = 'tesseract' AND type = 'embed'`).Scan(&jobCount); err != nil {
		t.Fatalf("count retried jobs: %v", err)
	}
	if jobCount != 1 {
		t.Fatalf("expected 1 retried job, got %d", jobCount)
	}

	backfillRes := performJSON(t, srv, http.MethodPost, "/v1/admin/queue/backfill", map[string]any{
		"namespace": "user/chrispian/memory/notes",
		"limit":     1,
	})
	if backfillRes.Code != http.StatusOK {
		t.Fatalf("queue backfill status=%d body=%s", backfillRes.Code, backfillRes.Body.String())
	}
	var backfillPayload struct {
		Queued int `json:"queued"`
	}
	if err := json.Unmarshal(backfillRes.Body.Bytes(), &backfillPayload); err != nil {
		t.Fatalf("unmarshal queue backfill: %v", err)
	}
	if backfillPayload.Queued != 1 {
		t.Fatalf("unexpected backfill payload: %+v", backfillPayload)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM jobs WHERE queue = 'tesseract' AND type = 'embed'`).Scan(&jobCount); err != nil {
		t.Fatalf("count jobs after backfill: %v", err)
	}
	if jobCount != 2 {
		t.Fatalf("expected 2 total jobs after backfill, got %d", jobCount)
	}
}

func TestAdminStorageEndpoint(t *testing.T) {
	srv, root := newTestServerWithRoot(t)
	srv.QueueDBPath = filepath.Join(root, "queue.db")
	srv.ConfigFile = filepath.Join(root, "config.yaml")
	if err := os.WriteFile(srv.ConfigFile, []byte("embedding:\n  provider: mock\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.WriteFile(srv.QueueDBPath, []byte("queue"), 0o644); err != nil {
		t.Fatalf("write queue db: %v", err)
	}
	for i := 0; i < 2; i++ {
		if _, err := srv.Store.AppendRecord(context.Background(), contextstore.AppendInput{
			Namespace: "app/admin/storage",
			Key:       "summary",
			Actor:     "app:admin",
			Payload:   json.RawMessage(`{"v":1}`),
		}); err != nil {
			t.Fatalf("append record %d: %v", i, err)
		}
	}
	if err := srv.Store.UpsertNamespacePolicy(context.Background(), contextstore.NamespacePolicyEntry{
		Namespace: "app/admin/storage",
		OwnerType: "app",
		OwnerID:   "admin",
		Policy: map[string]any{
			"retention":     "720h",
			"max_revisions": float64(10),
		},
	}); err != nil {
		t.Fatalf("upsert namespace policy: %v", err)
	}

	res := performJSON(t, srv, http.MethodGet, "/v1/admin/storage", nil)
	if res.Code != http.StatusOK {
		t.Fatalf("admin storage status=%d body=%s", res.Code, res.Body.String())
	}
	var payload struct {
		TotalBytes int `json:"total_bytes"`
		Records    struct {
			Revisions int `json:"revisions"`
			Heads     int `json:"heads"`
		} `json:"records"`
		NamespacePolicy struct {
			Namespaces       int `json:"namespaces"`
			WithRetention    int `json:"with_retention"`
			WithMaxRevisions int `json:"with_max_revisions"`
		} `json:"namespace_policy"`
		TopNamespaces []struct {
			Namespace string `json:"namespace"`
			Revisions int    `json:"revisions"`
			Keys      int    `json:"keys"`
		} `json:"top_namespaces"`
		Paths []struct {
			Label  string `json:"label"`
			Exists bool   `json:"exists"`
			Bytes  int    `json:"bytes"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal admin storage: %v", err)
	}
	if payload.TotalBytes <= 0 || payload.Records.Revisions != 2 || payload.Records.Heads != 1 {
		t.Fatalf("unexpected storage payload: %+v", payload)
	}
	if payload.NamespacePolicy.Namespaces == 0 || payload.NamespacePolicy.WithRetention == 0 || payload.NamespacePolicy.WithMaxRevisions == 0 {
		t.Fatalf("unexpected namespace policy payload: %+v", payload.NamespacePolicy)
	}
	if len(payload.TopNamespaces) == 0 || payload.TopNamespaces[0].Namespace != "app/admin/storage" || payload.TopNamespaces[0].Revisions != 2 || payload.TopNamespaces[0].Keys != 1 {
		t.Fatalf("unexpected top namespaces: %+v", payload.TopNamespaces)
	}
	labels := map[string]bool{}
	for _, path := range payload.Paths {
		labels[path.Label] = path.Exists && path.Bytes > 0
	}
	for _, label := range []string{"main-db", "records", "queue-db", "config-file"} {
		if !labels[label] {
			t.Fatalf("expected non-empty %s path in %+v", label, payload.Paths)
		}
	}
}

func TestMetricsEndpointDisabledByDefault(t *testing.T) {
	srv := newTestServer(t)
	res := performJSON(t, srv, http.MethodGet, "/v1/metrics", nil)
	if res.Code != http.StatusNotFound {
		t.Fatalf("expected 404 when metrics disabled, got %d body=%s", res.Code, res.Body.String())
	}
}

func TestMetricsEndpointCapturesRouteCountersAndErrors(t *testing.T) {
	srv := newTestServer(t)
	srv.EnableMetrics = true

	okWrite := performJSON(t, srv, http.MethodPost, "/v1/context/write", map[string]any{
		"client_id": "editor",
		"actor":     "app:editor",
		"namespace": "app/editor/session",
		"key":       "summary",
		"payload":   map[string]any{"v": 1},
	})
	if okWrite.Code != http.StatusOK {
		t.Fatalf("write status=%d body=%s", okWrite.Code, okWrite.Body.String())
	}

	badHead := performJSON(t, srv, http.MethodGet, "/v1/context/head?namespace=app/editor/session", nil)
	if badHead.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid head request, got %d body=%s", badHead.Code, badHead.Body.String())
	}

	metrics := performJSON(t, srv, http.MethodGet, "/v1/metrics", nil)
	if metrics.Code != http.StatusOK {
		t.Fatalf("metrics status=%d body=%s", metrics.Code, metrics.Body.String())
	}

	var payload struct {
		Enabled bool `json:"enabled"`
		Routes  []struct {
			Method         string `json:"method"`
			Path           string `json:"path"`
			Requests       int64  `json:"requests"`
			Errors         int64  `json:"errors"`
			LatencyNsTotal int64  `json:"latency_ns_total"`
			LatencyNsAvg   int64  `json:"latency_ns_avg"`
		} `json:"routes"`
		Totals struct {
			Requests int64 `json:"requests"`
			Errors   int64 `json:"errors"`
		} `json:"totals"`
	}
	if err := json.Unmarshal(metrics.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal metrics: %v", err)
	}
	if !payload.Enabled {
		t.Fatalf("expected enabled=true")
	}
	if payload.Totals.Requests < 2 {
		t.Fatalf("expected at least 2 requests counted, got %+v", payload.Totals)
	}
	if payload.Totals.Errors < 1 {
		t.Fatalf("expected at least one error counted, got %+v", payload.Totals)
	}

	foundWrite := false
	foundBadHead := false
	for _, route := range payload.Routes {
		if route.Method == http.MethodPost && route.Path == "/v1/context/write" {
			foundWrite = true
			if route.Requests < 1 || route.Errors != 0 {
				t.Fatalf("unexpected write route metrics: %+v", route)
			}
		}
		if route.Method == http.MethodGet && route.Path == "/v1/context/head" {
			foundBadHead = true
			if route.Errors < 1 {
				t.Fatalf("expected head route to record errors: %+v", route)
			}
		}
	}
	if !foundWrite || !foundBadHead {
		t.Fatalf("expected write/head route metrics, got %+v", payload.Routes)
	}
}

func TestRequestIDPropagationFromHeader(t *testing.T) {
	srv := newTestServer(t)
	srv.EnableRequestLogging = true
	logBuf := &bytes.Buffer{}
	srv.LogWriter = logBuf

	res := performJSONWithHeaders(t, srv, http.MethodGet, "/v1/health/readiness", nil, map[string]string{
		"X-Request-Id": "req-from-client",
	})
	if res.Code != http.StatusOK {
		t.Fatalf("readiness status=%d body=%s", res.Code, res.Body.String())
	}
	if got := res.Header().Get("X-Request-Id"); got != "req-from-client" {
		t.Fatalf("expected request id to propagate, got %q", got)
	}
	if !bytes.Contains(logBuf.Bytes(), []byte(`"request_id":"req-from-client"`)) {
		t.Fatalf("expected request id in log line, got: %s", logBuf.String())
	}
}

func TestRequestIDGeneratedWhenMissing(t *testing.T) {
	srv := newTestServer(t)
	srv.EnableRequestLogging = true
	logBuf := &bytes.Buffer{}
	srv.LogWriter = logBuf

	res := performJSON(t, srv, http.MethodGet, "/v1/health/readiness", nil)
	if res.Code != http.StatusOK {
		t.Fatalf("readiness status=%d body=%s", res.Code, res.Body.String())
	}
	got := res.Header().Get("X-Request-Id")
	if got == "" {
		t.Fatalf("expected generated request id header")
	}
	if !bytes.HasPrefix([]byte(got), []byte("req-")) {
		t.Fatalf("unexpected generated request id: %q", got)
	}
	if !bytes.Contains(logBuf.Bytes(), []byte(`"event":"request"`)) {
		t.Fatalf("expected structured request log, got: %s", logBuf.String())
	}
}

func TestMetricsIncludeStatusAndRecentRequestIDsForCorrelation(t *testing.T) {
	srv := newTestServer(t)
	srv.EnableMetrics = true
	srv.EnableRequestLogging = true
	srv.LogWriter = &bytes.Buffer{}

	res1 := performJSONWithHeaders(t, srv, http.MethodGet, "/v1/health/readiness", nil, map[string]string{
		"X-Request-Id": "corr-a",
	})
	if res1.Code != http.StatusOK {
		t.Fatalf("readiness status=%d body=%s", res1.Code, res1.Body.String())
	}
	res2 := performJSONWithHeaders(t, srv, http.MethodGet, "/v1/context/head?namespace=app/editor/session", nil, map[string]string{
		"X-Request-Id": "corr-b",
	})
	if res2.Code != http.StatusBadRequest {
		t.Fatalf("head status=%d body=%s", res2.Code, res2.Body.String())
	}

	metrics := performJSON(t, srv, http.MethodGet, "/v1/metrics", nil)
	if metrics.Code != http.StatusOK {
		t.Fatalf("metrics status=%d body=%s", metrics.Code, metrics.Body.String())
	}
	var payload struct {
		Routes []struct {
			Method           string           `json:"method"`
			Path             string           `json:"path"`
			StatusCounts     map[string]int64 `json:"status_counts"`
			RecentRequestIDs []string         `json:"recent_request_ids"`
		} `json:"routes"`
	}
	if err := json.Unmarshal(metrics.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal metrics: %v", err)
	}

	foundReadiness := false
	foundHead := false
	for _, route := range payload.Routes {
		if route.Method == http.MethodGet && route.Path == "/v1/health/readiness" {
			foundReadiness = true
			if route.StatusCounts["200"] < 1 {
				t.Fatalf("expected status count for 200 on readiness: %+v", route.StatusCounts)
			}
			if len(route.RecentRequestIDs) == 0 || route.RecentRequestIDs[len(route.RecentRequestIDs)-1] != "corr-a" {
				t.Fatalf("expected recent request ids to include corr-a: %+v", route.RecentRequestIDs)
			}
		}
		if route.Method == http.MethodGet && route.Path == "/v1/context/head" {
			foundHead = true
			if route.StatusCounts["400"] < 1 {
				t.Fatalf("expected status count for 400 on head: %+v", route.StatusCounts)
			}
			if len(route.RecentRequestIDs) == 0 || route.RecentRequestIDs[len(route.RecentRequestIDs)-1] != "corr-b" {
				t.Fatalf("expected recent request ids to include corr-b: %+v", route.RecentRequestIDs)
			}
		}
	}
	if !foundReadiness || !foundHead {
		t.Fatalf("expected readiness and head routes in metrics: %+v", payload.Routes)
	}
}

func TestRequestLogsRedactQueryByDefault(t *testing.T) {
	srv := newTestServer(t)
	srv.EnableRequestLogging = true
	logBuf := &bytes.Buffer{}
	srv.LogWriter = logBuf

	res := performJSON(t, srv, http.MethodGet, "/v1/health/readiness?token=secret&session=abc", nil)
	if res.Code != http.StatusOK {
		t.Fatalf("readiness status=%d body=%s", res.Code, res.Body.String())
	}
	if !bytes.Contains(logBuf.Bytes(), []byte(`"query":"[REDACTED]"`)) {
		t.Fatalf("expected redacted query log, got: %s", logBuf.String())
	}
	if bytes.Contains(logBuf.Bytes(), []byte("token=secret")) {
		t.Fatalf("did not expect raw query in redacted mode: %s", logBuf.String())
	}
}

func TestRequestLogsFullModeIncludesQuery(t *testing.T) {
	srv := newTestServer(t)
	srv.EnableRequestLogging = true
	srv.RequestLogMode = "full"
	logBuf := &bytes.Buffer{}
	srv.LogWriter = logBuf

	res := performJSON(t, srv, http.MethodGet, "/v1/health/readiness?token=visible&session=abc", nil)
	if res.Code != http.StatusOK {
		t.Fatalf("readiness status=%d body=%s", res.Code, res.Body.String())
	}
	if !bytes.Contains(logBuf.Bytes(), []byte(`"query":"token=visible\u0026session=abc"`)) {
		t.Fatalf("expected raw query in full mode, got: %s", logBuf.String())
	}
}

func performJSON(t *testing.T, h http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	return performJSONWithHeaders(t, h, method, path, body, nil)
}

func performJSONWithHeaders(t *testing.T, h http.Handler, method, path string, body any, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var reqBody []byte
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reqBody = b
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(reqBody))
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

// doGatedPromote performs the full 3-step gated promotion flow (request → approve → apply).
// The source record must already exist. clientID determines the app/* promotions namespace.
func doGatedPromote(t *testing.T, s *Server, clientID, fromNS, fromKey, toNS, toKey string) {
	t.Helper()
	reqRes := performJSON(t, s, http.MethodPost, "/v1/context/promote/request", map[string]any{
		"actor":            "app:" + clientID,
		"client_id":        clientID,
		"source_namespace": fromNS,
		"source_key":       fromKey,
		"target_namespace": toNS,
		"target_key":       toKey,
	})
	if reqRes.Code != http.StatusOK {
		t.Fatalf("doGatedPromote/request: %s", reqRes.Body)
	}
	var reqResp map[string]any
	json.NewDecoder(reqRes.Body).Decode(&reqResp)
	requestID := reqResp["request_id"].(string)

	apprRes := performJSON(t, s, http.MethodPost, "/v1/context/promote/approve", map[string]any{
		"actor":      "user",
		"request_id": requestID,
	})
	if apprRes.Code != http.StatusOK {
		t.Fatalf("doGatedPromote/approve: %s", apprRes.Body)
	}

	applyRes := performJSON(t, s, http.MethodPost, "/v1/context/promote/apply", map[string]any{
		"actor":      "user",
		"request_id": requestID,
	})
	if applyRes.Code != http.StatusOK {
		t.Fatalf("doGatedPromote/apply: %s", applyRes.Body)
	}
}

func TestPacketEndpointBudgetCutoff(t *testing.T) {
	s := newTestServer(t)

	// Write 10 records to user/memory/packet-test
	for i := 0; i < 10; i++ {
		res := performJSON(t, s, "POST", "/v1/context/write", map[string]any{
			"actor":     "user",
			"namespace": "user/memory/packet-test",
			"key":       "item-" + strconv.Itoa(i),
			"payload":   map[string]any{"n": i},
		})
		if res.Code != http.StatusOK {
			t.Fatalf("write %d: %s", i, res.Body)
		}
	}

	// Request packet with max_items=5
	res := performJSON(t, s, "POST", "/v1/context/packet", map[string]any{
		"selector": map[string]any{
			"namespaces":     []string{"user/memory/packet-test"},
			"revision_scope": "head",
		},
		"assembly": map[string]any{
			"include_pins": false,
			"budget": map[string]any{
				"max_items": 5,
			},
		},
	})
	if res.Code != http.StatusOK {
		t.Fatalf("packet: %s", res.Body)
	}
	var resp map[string]any
	if err := json.NewDecoder(res.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	manifest := resp["manifest"].(map[string]any)
	items := resp["items"].([]any)
	if len(items) != 5 {
		t.Errorf("want 5 items, got %d", len(items))
	}
	if manifest["truncated"] != true {
		t.Errorf("want truncated=true, got %v", manifest["truncated"])
	}
	if got := int(manifest["items_total"].(float64)); got != 10 {
		t.Errorf("want items_total=10, got %d", got)
	}
}

func TestEstimateEndpoint(t *testing.T) {
	s := newTestServer(t)

	for i := 0; i < 3; i++ {
		performJSON(t, s, "POST", "/v1/context/write", map[string]any{
			"actor":     "user",
			"namespace": "user/memory/est-test",
			"key":       "k" + strconv.Itoa(i),
			"payload":   map[string]any{"data": "hello"},
		})
	}

	res := performJSON(t, s, "POST", "/v1/context/estimate", map[string]any{
		"selector": map[string]any{
			"namespaces":     []string{"user/memory/est-test"},
			"revision_scope": "head",
		},
	})
	if res.Code != http.StatusOK {
		t.Fatalf("estimate: %s", res.Body)
	}
	var resp map[string]any
	if err := json.NewDecoder(res.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if int(resp["record_count"].(float64)) != 3 {
		t.Errorf("want record_count=3, got %v", resp["record_count"])
	}
	if int(resp["total_bytes"].(float64)) == 0 {
		t.Errorf("want total_bytes > 0")
	}
}

func TestGatedPromotionHappyPath(t *testing.T) {
	s := newTestServer(t)

	// Write a source record in app namespace.
	writeRes := performJSON(t, s, "POST", "/v1/context/write", map[string]any{
		"actor":     "app:myapp",
		"client_id": "myapp",
		"namespace": "app/myapp/draft",
		"key":       "pref-v2",
		"payload":   map[string]any{"v": 1},
	})
	if writeRes.Code != http.StatusOK {
		t.Fatalf("write: %s", writeRes.Body)
	}
	var writeResp map[string]any
	json.NewDecoder(writeRes.Body).Decode(&writeResp)
	srcRecordID := writeResp["record_id"].(string)

	// Create a promotion request.
	reqRes := performJSON(t, s, "POST", "/v1/context/promote/request", map[string]any{
		"actor":              "app:myapp",
		"client_id":          "myapp",
		"source_namespace":   "app/myapp/draft",
		"source_key":         "pref-v2",
		"source_revision_id": srcRecordID,
		"target_namespace":   "user/memory/preferences",
		"target_key":         "user-prefs",
		"reason":             "user confirmed",
	})
	if reqRes.Code != http.StatusOK {
		t.Fatalf("promote/request: %s", reqRes.Body)
	}
	var reqResp map[string]any
	json.NewDecoder(reqRes.Body).Decode(&reqResp)
	requestID := reqResp["request_id"].(string)
	if reqResp["status"] != "pending" {
		t.Errorf("want status=pending, got %v", reqResp["status"])
	}

	// Approve the request.
	apprRes := performJSON(t, s, "POST", "/v1/context/promote/approve", map[string]any{
		"actor":      "user",
		"request_id": requestID,
		"notes":      "LGTM",
	})
	if apprRes.Code != http.StatusOK {
		t.Fatalf("promote/approve: %s", apprRes.Body)
	}
	var apprResp map[string]any
	json.NewDecoder(apprRes.Body).Decode(&apprResp)
	approvalID := apprResp["approval_id"].(string)
	if apprResp["status"] != "approved" {
		t.Errorf("want status=approved, got %v", apprResp["status"])
	}

	// Apply the approved promotion.
	applyRes := performJSON(t, s, "POST", "/v1/context/promote/apply", map[string]any{
		"actor":      "user",
		"request_id": requestID,
	})
	if applyRes.Code != http.StatusOK {
		t.Fatalf("promote/apply: %s", applyRes.Body)
	}
	var applyResp map[string]any
	json.NewDecoder(applyRes.Body).Decode(&applyResp)
	if applyResp["request_id"] != requestID {
		t.Errorf("apply: want request_id=%s, got %v", requestID, applyResp["request_id"])
	}
	if applyResp["approval_id"] != approvalID {
		t.Errorf("apply: want approval_id=%s, got %v", approvalID, applyResp["approval_id"])
	}

	// Verify the record appears in the target namespace.
	headRes := performJSON(t, s, "GET", "/v1/context/head?namespace=user/memory/preferences&key=user-prefs", nil)
	if headRes.Code != http.StatusOK {
		t.Fatalf("head: %s", headRes.Body)
	}
}

func TestGatedPromotionApplyWithoutApprovalFails(t *testing.T) {
	s := newTestServer(t)

	performJSON(t, s, "POST", "/v1/context/write", map[string]any{
		"actor":     "app:myapp",
		"client_id": "myapp",
		"namespace": "app/myapp/draft",
		"key":       "pref-v3",
		"payload":   map[string]any{"v": 1},
	})

	reqRes := performJSON(t, s, "POST", "/v1/context/promote/request", map[string]any{
		"actor":            "app:myapp",
		"client_id":        "myapp",
		"source_namespace": "app/myapp/draft",
		"source_key":       "pref-v3",
		"target_namespace": "user/memory/preferences",
		"target_key":       "user-prefs",
	})
	var reqResp map[string]any
	json.NewDecoder(reqRes.Body).Decode(&reqResp)
	requestID := reqResp["request_id"].(string)

	// Try to apply without approving first.
	applyRes := performJSON(t, s, "POST", "/v1/context/promote/apply", map[string]any{
		"actor":      "user",
		"request_id": requestID,
	})
	if applyRes.Code == http.StatusOK {
		t.Errorf("expected non-200 when applying unapproved request, got 200")
	}
}

func TestOldPromoteEndpointReturnsGone(t *testing.T) {
	s := newTestServer(t)
	res := performJSON(t, s, "POST", "/v1/context/promote", map[string]any{})
	if res.Code != http.StatusGone {
		t.Errorf("want 410 Gone, got %d: %s", res.Code, res.Body)
	}
}

func TestTokenCreateListRevoke(t *testing.T) {
	s := newTestServer(t)

	// Create a scoped token.
	createRes := performJSON(t, s, "POST", "/v1/auth/tokens/create", map[string]any{
		"name":            "test-agent",
		"client_id":       "app:test",
		"scopes":          []string{"write", "packet"},
		"namespace_globs": []string{"app/test/*"},
		"ttl":             "8760h",
	})
	if createRes.Code != http.StatusOK {
		t.Fatalf("token create: %d %s", createRes.Code, createRes.Body)
	}
	var createResp map[string]any
	json.NewDecoder(createRes.Body).Decode(&createResp)
	if createResp["token"] == "" || createResp["token"] == nil {
		t.Errorf("expected raw token in create response, got %v", createResp)
	}
	if createResp["id"] == "" || createResp["id"] == nil {
		t.Errorf("expected id in create response")
	}
	tokenID := createResp["id"].(string)
	if createResp["name"] != "test-agent" {
		t.Errorf("expected name=test-agent, got %v", createResp["name"])
	}
	scopes, _ := createResp["scopes"].([]any)
	if len(scopes) != 2 {
		t.Errorf("expected 2 scopes, got %v", scopes)
	}

	// List tokens — raw value must NOT be returned.
	listRes := performJSON(t, s, "GET", "/v1/auth/tokens/list", nil)
	if listRes.Code != http.StatusOK {
		t.Fatalf("token list: %d %s", listRes.Code, listRes.Body)
	}
	var listResp map[string]any
	json.NewDecoder(listRes.Body).Decode(&listResp)
	tokens, _ := listResp["tokens"].([]any)
	if len(tokens) == 0 {
		t.Fatalf("expected at least one token in list")
	}
	found := false
	for _, raw := range tokens {
		entry := raw.(map[string]any)
		if entry["id"] == tokenID {
			found = true
			if entry["name"] != "test-agent" {
				t.Errorf("name mismatch: %v", entry["name"])
			}
			if entry["client_id"] != "app:test" {
				t.Errorf("client_id mismatch: %v", entry["client_id"])
			}
			if _, hasToken := entry["token"]; hasToken {
				t.Errorf("list must never return raw token value")
			}
		}
	}
	if !found {
		t.Errorf("created token %s not found in list", tokenID)
	}

	// Revoke by ID.
	revokeRes := performJSON(t, s, "POST", "/v1/auth/tokens/revoke", map[string]any{
		"id": tokenID,
	})
	if revokeRes.Code != http.StatusOK {
		t.Fatalf("token revoke: %d %s", revokeRes.Code, revokeRes.Body)
	}
	var revokeResp map[string]any
	json.NewDecoder(revokeRes.Body).Decode(&revokeResp)
	if revokeResp["revoked"] != true {
		t.Errorf("expected revoked=true, got %v", revokeResp)
	}
}

func TestTokenCreateValidation(t *testing.T) {
	s := newTestServer(t)

	// Missing name.
	res := performJSON(t, s, "POST", "/v1/auth/tokens/create", map[string]any{
		"client_id": "app:test",
	})
	if res.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing name, got %d: %s", res.Code, res.Body)
	}

	// Invalid expires_at.
	res2 := performJSON(t, s, "POST", "/v1/auth/tokens/create", map[string]any{
		"name":       "bad-expiry",
		"expires_at": "not-a-date",
	})
	if res2.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid expires_at, got %d: %s", res2.Code, res2.Body)
	}
}

func TestTokenRevokeInvalidID(t *testing.T) {
	s := newTestServer(t)
	res := performJSON(t, s, "POST", "/v1/auth/tokens/revoke", map[string]any{
		"id": "tok-nonexistent",
	})
	// Should return 500 (internal error) since the ID doesn't exist.
	if res.Code == http.StatusOK {
		t.Errorf("expected non-200 for nonexistent token ID, got 200")
	}
}

// issueTokenWithScopes creates a managed-auth token with given scopes and namespace globs.
func issueTokenWithScopes(t *testing.T, srv *Server, label string, scopes, namespaceGlobs []string) string {
	t.Helper()
	rawToken, _, err := srv.Store.CreateAuthToken(context.Background(), contextstore.TokenCreateInput{
		Label:          label,
		Scopes:         scopes,
		NamespaceGlobs: namespaceGlobs,
		TTL:            time.Hour,
	})
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	return rawToken
}

func TestScopeEnforcementBlocksWriteWithoutWriteScope(t *testing.T) {
	srv := newTestServer(t)
	srv.ManagedAuth = true

	// Token with only "packet" scope — cannot write.
	packetToken := issueTokenWithScopes(t, srv, "packet-only", []string{"packet"}, []string{"*"})

	res := performJSONWithHeaders(t, srv, http.MethodPost, "/v1/context/write", map[string]any{
		"client_id": "editor",
		"actor":     "app:editor",
		"namespace": "app/editor/session",
		"key":       "summary",
		"payload":   map[string]any{"v": 1},
	}, map[string]string{"Authorization": "Bearer " + packetToken})
	if res.Code != http.StatusForbidden {
		t.Errorf("expected 403 insufficient_scope, got %d: %s", res.Code, res.Body)
	}
	var body map[string]any
	json.NewDecoder(res.Body).Decode(&body)
	if body["code"] != "insufficient_scope" {
		t.Errorf("expected code=insufficient_scope, got %v", body["code"])
	}
}

func TestScopeEnforcementAllowsWriteWithWriteScope(t *testing.T) {
	srv := newTestServer(t)
	srv.ManagedAuth = true

	// Full-access token (migration default) can write.
	fullToken := issueTokenWithScopes(t, srv, "full-access", nil, nil) // nil → defaults

	res := performJSONWithHeaders(t, srv, http.MethodPost, "/v1/context/write", map[string]any{
		"client_id": "editor",
		"actor":     "app:editor",
		"namespace": "app/editor/session",
		"key":       "summary",
		"payload":   map[string]any{"v": 1},
	}, map[string]string{"Authorization": "Bearer " + fullToken})
	if res.Code != http.StatusOK {
		t.Errorf("expected 200 with full-access token, got %d: %s", res.Code, res.Body)
	}
}

func TestNamespaceGlobEnforcementBlocksForeignNamespace(t *testing.T) {
	srv := newTestServer(t)
	srv.ManagedAuth = true

	// Token scoped to app/myapp/* only — cannot write to app/editor/*.
	scopedToken := issueTokenWithScopes(t, srv, "myapp-only",
		[]string{"write"}, []string{"app/myapp/*"})

	res := performJSONWithHeaders(t, srv, http.MethodPost, "/v1/context/write", map[string]any{
		"client_id": "editor",
		"actor":     "app:editor",
		"namespace": "app/editor/session",
		"key":       "summary",
		"payload":   map[string]any{"v": 1},
	}, map[string]string{"Authorization": "Bearer " + scopedToken})
	if res.Code != http.StatusForbidden {
		t.Errorf("expected 403 namespace_not_permitted, got %d: %s", res.Code, res.Body)
	}
	var body map[string]any
	json.NewDecoder(res.Body).Decode(&body)
	if body["code"] != "namespace_not_permitted" {
		t.Errorf("expected code=namespace_not_permitted, got %v", body["code"])
	}
}

func TestNamespaceGlobEnforcementAllowsMatchingNamespace(t *testing.T) {
	srv := newTestServer(t)
	srv.ManagedAuth = true

	// Token scoped to app/editor/* — can write to app/editor/session.
	scopedToken := issueTokenWithScopes(t, srv, "editor-scoped",
		[]string{"write"}, []string{"app/editor/*"})

	res := performJSONWithHeaders(t, srv, http.MethodPost, "/v1/context/write", map[string]any{
		"client_id": "editor",
		"actor":     "app:editor",
		"namespace": "app/editor/session",
		"key":       "summary",
		"payload":   map[string]any{"v": 1},
	}, map[string]string{"Authorization": "Bearer " + scopedToken})
	if res.Code != http.StatusOK {
		t.Errorf("expected 200 with matching namespace glob, got %d: %s", res.Code, res.Body)
	}
}

func TestWildcardNamespaceGlobAllowsAnyNamespace(t *testing.T) {
	srv := newTestServer(t)
	srv.ManagedAuth = true

	// Token with namespace_globs: ["*"] — can write anywhere.
	wildcardToken := issueTokenWithScopes(t, srv, "wildcard",
		[]string{"write"}, []string{"*"})

	res := performJSONWithHeaders(t, srv, http.MethodPost, "/v1/context/write", map[string]any{
		"client_id": "editor",
		"actor":     "app:editor",
		"namespace": "app/editor/session",
		"key":       "summary",
		"payload":   map[string]any{"v": 1},
	}, map[string]string{"Authorization": "Bearer " + wildcardToken})
	if res.Code != http.StatusOK {
		t.Errorf("expected 200 with wildcard token, got %d: %s", res.Code, res.Body)
	}
}

func TestContextPlanResumeTask(t *testing.T) {
	s := newTestServer(t)

	res := performJSON(t, s, http.MethodPost, "/v1/broker/plan", map[string]any{
		"task_summary": "implementing capability token enforcement phase four",
		"intent":       "resume_task",
	})
	if res.Code != http.StatusOK {
		t.Fatalf("context plan: %d %s", res.Code, res.Body)
	}
	var resp map[string]any
	json.NewDecoder(res.Body).Decode(&resp)

	plan, _ := resp["plan"].(map[string]any)
	if plan == nil {
		t.Fatalf("expected plan in response, got %v", resp)
	}
	selector, _ := plan["selector"].(map[string]any)
	if selector == nil {
		t.Fatalf("expected selector in plan")
	}
	namespaces, _ := selector["namespaces"].([]any)
	if len(namespaces) == 0 {
		t.Fatalf("expected namespaces in selector")
	}
	// Must include at least one namespace derived from the summary keyword "capability".
	found := false
	for _, ns := range namespaces {
		if strings.Contains(ns.(string), "capability") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected namespace containing 'capability' for resume_task with summary about capability; got %v", namespaces)
	}

	rationale, _ := resp["rationale"].(string)
	if !strings.Contains(rationale, "resume_task") {
		t.Errorf("expected rationale to mention resume_task, got %q", rationale)
	}
}

func TestContextPlanBootProject(t *testing.T) {
	s := newTestServer(t)

	res := performJSON(t, s, http.MethodPost, "/v1/broker/plan", map[string]any{
		"intent": "boot_project",
	})
	if res.Code != http.StatusOK {
		t.Fatalf("context plan boot_project: %d %s", res.Code, res.Body)
	}
	var resp map[string]any
	json.NewDecoder(res.Body).Decode(&resp)

	plan, _ := resp["plan"].(map[string]any)
	selector, _ := plan["selector"].(map[string]any)
	namespaces, _ := selector["namespaces"].([]any)

	hasMemory := false
	hasPins := false
	for _, ns := range namespaces {
		s := ns.(string)
		if strings.HasPrefix(s, "user/memory/") || s == "user/memory/*" {
			hasMemory = true
		}
		if strings.HasPrefix(s, "user/pins/") || s == "user/pins/*" {
			hasPins = true
		}
	}
	if !hasMemory || !hasPins {
		t.Errorf("boot_project plan should include user/memory/* and user/pins/*, got %v", namespaces)
	}
}

func TestContextPlanForbiddenNamespaceStripped(t *testing.T) {
	s := newTestServer(t)
	s.Planner = PlannerConfig{
		ForbiddenNamespacePatterns: []string{"system/*"},
	}

	res := performJSON(t, s, http.MethodPost, "/v1/broker/plan", map[string]any{
		"intent": "custom",
		"constraints": map[string]any{
			"namespaces": []string{"user/memory/*", "system/internal/secrets"},
		},
	})
	if res.Code != http.StatusOK {
		t.Fatalf("context plan forbidden namespace: %d %s", res.Code, res.Body)
	}
	var resp map[string]any
	json.NewDecoder(res.Body).Decode(&resp)

	// Warnings should mention the forbidden namespace.
	warnings, _ := resp["warnings"].([]any)
	foundWarning := false
	for _, w := range warnings {
		if strings.Contains(w.(string), "forbidden") {
			foundWarning = true
		}
	}
	if !foundWarning {
		t.Errorf("expected warning about forbidden namespace, got %v", warnings)
	}

	// system/internal/secrets should be stripped.
	plan, _ := resp["plan"].(map[string]any)
	selector, _ := plan["selector"].(map[string]any)
	for _, ns := range selector["namespaces"].([]any) {
		if strings.Contains(ns.(string), "system") {
			t.Errorf("forbidden namespace should have been stripped, still in plan: %v", ns)
		}
	}
}

func TestContextPlanNoPermittedNamespacesReturns403(t *testing.T) {
	s := newTestServer(t)
	s.Planner = PlannerConfig{
		ForbiddenNamespacePatterns: []string{"user/*"},
	}

	res := performJSON(t, s, http.MethodPost, "/v1/broker/plan", map[string]any{
		"intent": "boot_project",
	})
	if res.Code != http.StatusForbidden {
		t.Errorf("expected 403 plan_forbidden when all namespaces stripped, got %d: %s", res.Code, res.Body)
	}
}

func TestContextPlanBudgetClamped(t *testing.T) {
	s := newTestServer(t)
	s.Planner = PlannerConfig{
		MaxItemsCap:  10,
		MaxTokensCap: 500,
	}

	res := performJSON(t, s, http.MethodPost, "/v1/broker/plan", map[string]any{
		"intent": "custom",
		"constraints": map[string]any{
			"max_items":           300,
			"max_tokens_estimate": 50000,
		},
	})
	if res.Code != http.StatusOK {
		t.Fatalf("context plan budget clamp: %d %s", res.Code, res.Body)
	}
	var resp map[string]any
	json.NewDecoder(res.Body).Decode(&resp)

	// Warnings should mention clamping.
	warnings, _ := resp["warnings"].([]any)
	foundItemsClamp, foundTokensClamp := false, false
	for _, w := range warnings {
		ws := w.(string)
		if strings.Contains(ws, "max_items") {
			foundItemsClamp = true
		}
		if strings.Contains(ws, "max_tokens_estimate") {
			foundTokensClamp = true
		}
	}
	if !foundItemsClamp || !foundTokensClamp {
		t.Errorf("expected clamping warnings, got %v", warnings)
	}
}

func TestBulkIngest_Success(t *testing.T) {
	srv := newTestServer(t)
	body := map[string]any{
		"client_id": "test",
		"actor":     "bulk-test",
		"items": []map[string]any{
			{"namespace": "test/bulk", "key": "item-1", "payload": map[string]any{"title": "First"}, "record_type": "task/spec"},
			{"namespace": "test/bulk", "key": "item-2", "payload": map[string]any{"title": "Second"}, "record_type": "task/spec"},
		},
	}
	res := performJSON(t, srv, http.MethodPost, "/v1/context/bulk-ingest", body)
	if res.Code != http.StatusOK {
		t.Fatalf("bulk ingest status=%d body=%s", res.Code, res.Body.String())
	}
	var resp struct {
		Total   int `json:"total"`
		Written int `json:"written"`
		Errors  int `json:"errors"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Total != 2 || resp.Written != 2 || resp.Errors != 0 {
		t.Errorf("expected total=2, written=2, errors=0; got total=%d, written=%d, errors=%d", resp.Total, resp.Written, resp.Errors)
	}
}

func TestBulkIngest_ValidationErrors(t *testing.T) {
	srv := newTestServer(t)
	body := map[string]any{
		"client_id": "test",
		"actor":     "bulk-test",
		"items": []map[string]any{
			{"namespace": "test/bulk", "key": "item-ok", "payload": map[string]any{"title": "OK"}, "record_type": "task/spec"},
			{"namespace": "", "key": "missing-ns", "payload": map[string]any{}},
			{"namespace": "test/bulk", "key": "bad-type", "payload": map[string]any{}, "record_type": "unknown/type"},
		},
	}
	res := performJSON(t, srv, http.MethodPost, "/v1/context/bulk-ingest", body)
	if res.Code != http.StatusOK {
		t.Fatalf("bulk ingest status=%d body=%s", res.Code, res.Body.String())
	}
	var resp struct {
		Total   int `json:"total"`
		Written int `json:"written"`
		Errors  int `json:"errors"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Written != 1 || resp.Errors != 2 {
		t.Errorf("expected written=1, errors=2; got written=%d, errors=%d", resp.Written, resp.Errors)
	}
}

func TestBulkIngest_RequiredFieldsEnforced(t *testing.T) {
	srv := newTestServer(t)
	body := map[string]any{
		"client_id": "test",
		"actor":     "bulk-test",
		"items": []map[string]any{
			{"namespace": "test/bulk", "key": "no-title", "payload": map[string]any{"description": "missing title"}, "record_type": "task/spec"},
		},
	}
	res := performJSON(t, srv, http.MethodPost, "/v1/context/bulk-ingest", body)
	if res.Code != http.StatusOK {
		t.Fatalf("bulk ingest status=%d body=%s", res.Code, res.Body.String())
	}
	var resp struct {
		Errors  int `json:"errors"`
		Results []struct {
			Error string `json:"error"`
		} `json:"results"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Errors != 1 {
		t.Errorf("expected 1 error for missing required field, got %d", resp.Errors)
	}
}

func TestBulkIngest_Empty(t *testing.T) {
	srv := newTestServer(t)
	body := map[string]any{
		"client_id": "test",
		"items":     []map[string]any{},
	}
	res := performJSON(t, srv, http.MethodPost, "/v1/context/bulk-ingest", body)
	if res.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for empty items, got %d", res.Code)
	}
}

func TestNamespacesList(t *testing.T) {
	srv := newTestServer(t)

	for _, ns := range []string{"user/alice/memory/notes", "user/alice/cache", "app/editor/session"} {
		reg := performJSON(t, srv, http.MethodPost, "/v1/namespaces/register", map[string]any{
			"namespace":  ns,
			"owner_type": "app",
			"owner_id":   "test",
			"policy":     map[string]any{},
		})
		if reg.Code != http.StatusOK {
			t.Fatalf("register %s status=%d body=%s", ns, reg.Code, reg.Body.String())
		}
	}

	all := performJSON(t, srv, http.MethodGet, "/v1/namespaces/list", nil)
	if all.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", all.Code, all.Body.String())
	}
	var allResp struct {
		Items []struct {
			Namespace string `json:"namespace"`
		} `json:"items"`
		Count     int  `json:"count"`
		Truncated bool `json:"truncated"`
	}
	if err := json.Unmarshal(all.Body.Bytes(), &allResp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if allResp.Count != 3 {
		t.Errorf("count: got %d want 3", allResp.Count)
	}
	if len(allResp.Items) != 3 {
		t.Errorf("items: got %d want 3", len(allResp.Items))
	}

	prefixed := performJSON(t, srv, http.MethodGet, "/v1/namespaces/list?prefix=user/", nil)
	if prefixed.Code != http.StatusOK {
		t.Fatalf("prefix list status=%d body=%s", prefixed.Code, prefixed.Body.String())
	}
	var prefResp struct {
		Items []struct{ Namespace string } `json:"items"`
		Count int                          `json:"count"`
	}
	if err := json.Unmarshal(prefixed.Body.Bytes(), &prefResp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if prefResp.Count != 2 {
		t.Errorf("prefix count: got %d want 2", prefResp.Count)
	}

	limited := performJSON(t, srv, http.MethodGet, "/v1/namespaces/list?limit=1", nil)
	if limited.Code != http.StatusOK {
		t.Fatalf("limited list status=%d body=%s", limited.Code, limited.Body.String())
	}
	var limResp struct {
		Items     []struct{ Namespace string } `json:"items"`
		Count     int                          `json:"count"`
		Truncated bool                         `json:"truncated"`
	}
	if err := json.Unmarshal(limited.Body.Bytes(), &limResp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(limResp.Items) != 1 {
		t.Errorf("limited items: got %d want 1", len(limResp.Items))
	}
	if limResp.Count != 3 {
		t.Errorf("limited count (matched): got %d want 3", limResp.Count)
	}
	if !limResp.Truncated {
		t.Errorf("expected truncated=true")
	}

	bad := performJSON(t, srv, http.MethodGet, "/v1/namespaces/list?limit=abc", nil)
	if bad.Code != http.StatusBadRequest {
		t.Errorf("bad limit: got %d want 400", bad.Code)
	}
}
