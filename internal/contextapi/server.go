package contextapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hollis-labs/go-apppaths/paths"
	llmcontracts "github.com/hollis-labs/go-llm-contracts"
	"github.com/hollis-labs/go-modelsdev/modelsdev"
	feotel "github.com/hollis-labs/go-otel"
	"github.com/hollis-labs/tesseract/internal/config"
	"github.com/hollis-labs/tesseract/internal/contextpolicy"
	"github.com/hollis-labs/tesseract/internal/contextstore"
	"github.com/hollis-labs/tesseract/internal/contexttypes"
	"github.com/hollis-labs/tesseract/internal/knowledge"
	"github.com/hollis-labs/tesseract/internal/memory"
	"gopkg.in/yaml.v3"
)

type contextKey int

const tokenClaimsKey contextKey = iota

// getTokenClaims extracts token claims from the request context (set by authorizeMutatingRequest).
// Returns zero-value AuthToken when no managed-auth session (full access assumed).
func getTokenClaims(r *http.Request) (contextstore.AuthToken, bool) {
	v, ok := r.Context().Value(tokenClaimsKey).(contextstore.AuthToken)
	return v, ok
}

// requireScope checks that the request's token claims include the given scope.
// Returns false and writes a 403 response if the check fails.
func requireScope(w http.ResponseWriter, r *http.Request, scope string) bool {
	claims, ok := getTokenClaims(r)
	if !ok {
		return true // no managed-auth; pass through
	}
	for _, s := range claims.Scopes {
		if s == scope {
			return true
		}
	}
	writeError(w, http.StatusForbidden, "insufficient_scope", "token does not have required scope", map[string]any{
		"required":     scope,
		"token_client": claims.ClientID,
	})
	return false
}

// requireNamespaceAccess checks that the request's token namespace_globs permit the given namespace.
// Returns false and writes a 403 response if denied.
// Globs ending with /* match all sub-paths (e.g. app/test/* matches app/test/session/task-001).
func requireNamespaceAccess(w http.ResponseWriter, r *http.Request, namespace string) bool {
	claims, ok := getTokenClaims(r)
	if !ok {
		return true // no managed-auth; pass through
	}
	for _, glob := range claims.NamespaceGlobs {
		if glob == "*" || glob == namespace {
			return true
		}
		if matched, _ := path.Match(glob, namespace); matched {
			return true
		}
		// Hierarchical match: "app/test/*" should match "app/test/session/task-001".
		if strings.HasSuffix(glob, "/*") {
			prefix := strings.TrimSuffix(glob, "*") // "app/test/"
			if strings.HasPrefix(namespace, prefix) {
				return true
			}
		}
	}
	writeError(w, http.StatusForbidden, "namespace_not_permitted",
		"token is not permitted to access this namespace", map[string]any{
			"namespace":   namespace,
			"token_globs": claims.NamespaceGlobs,
		})
	return false
}

// Server exposes HTTP handlers for the context API.
type Server struct {
	Store  *contextstore.Store
	Policy *contextpolicy.Engine
	// AuthToken enables bearer-token auth on mutating routes when non-empty.
	AuthToken string
	// ManagedAuth enables token lifecycle validation using store-backed auth_tokens.
	ManagedAuth bool
	// Planner holds server-side caps for context plan validation.
	// (Renamed from Broker to avoid confusion with the universal ContextBroker.)
	Planner PlannerConfig
	// EnableMetrics exposes /v1/metrics and records per-route request/latency counters.
	EnableMetrics bool
	// EnableRequestLogging emits one structured line per request.
	EnableRequestLogging bool
	// RequestLogMode controls query logging detail: redacted|full.
	RequestLogMode string
	// Layout is the resolved go-apppaths layout for admin/setup introspection.
	Layout paths.Layout
	// ConfigFile is the loaded config.yaml path for admin/setup introspection.
	ConfigFile string
	// QueueDBPath is the memory embedding queue DB path.
	QueueDBPath string
	// QueueDB is the open SQLite queue database used for admin queue health.
	QueueDB *sql.DB
	// RuntimeConfig is the loaded Tesseract config, merged with defaults.
	RuntimeConfig config.Config
	// TypeRegistry manages context types and views. May be nil (defaults will be used).
	TypeRegistry *contexttypes.Registry
	// MemoryStore backs the /v1/memory/* and /v1/knowledge/* routes. When
	// nil, those routes respond with 503 service_unavailable.
	MemoryStore *memory.Store
	// KnowledgeStore backs /v1/knowledge/* routes. Wired by cmd/tesseract to
	// knowledge.New(MemoryStore).
	KnowledgeStore *knowledge.Store
	// SynthesisProvider is the LLM Provider used by /v1/synthesis/ask.
	// When nil, the synthesis route returns 503 service_unavailable. Wired by
	// cmd/tesseract from config.Synthesis settings.
	SynthesisProvider llmcontracts.Provider
	// SynthesisConfig carries the model id, system prompt, max output tokens,
	// and temperature used for synthesis calls. Honoured only when
	// SynthesisProvider is non-nil.
	SynthesisConfig config.SynthesisConfig
	// ModelsDev is the cached client used to look up per-model pricing for
	// synthesis cost reporting. May be nil — when absent, cost fields are
	// returned as null.
	ModelsDev *modelsdev.Client
	// LogWriter is the destination for structured request logs when enabled.
	LogWriter  io.Writer
	startupErr error
	metrics    *apiMetrics
	requestSeq uint64
	logMu      sync.Mutex
}

// NewServer creates an API server.
func NewServer(store *contextstore.Store, policy *contextpolicy.Engine) *Server {
	if policy == nil {
		policy = contextpolicy.New()
	}
	s := &Server{
		Store:   store,
		Policy:  policy,
		metrics: newAPIMetrics(),
	}
	s.startupErr = s.reloadPolicies(context.Background())
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	requestID := strings.TrimSpace(r.Header.Get("X-Request-Id"))
	if requestID == "" {
		requestID = s.nextRequestID()
	}
	w.Header().Set("X-Request-Id", requestID)

	started := time.Now()
	sw := &statusWriter{ResponseWriter: w}
	defer func() {
		status := sw.status
		if status == 0 {
			status = http.StatusOK
		}
		if s.EnableMetrics {
			s.metrics.Record(r.Method, r.URL.Path, status, time.Since(started), requestID)
		}
		if s.EnableRequestLogging {
			s.writeRequestLog(requestID, r.Method, r.URL.Path, r.URL.RawQuery, status, time.Since(started))
		}
	}()
	w = sw

	if s.startupErr != nil {
		writeError(w, http.StatusInternalServerError, "startup_failed", s.startupErr.Error(), nil)
		return
	}
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/v1/metrics":
		if !s.EnableMetrics {
			writeError(w, http.StatusNotFound, "not_found", "endpoint not found", nil)
			return
		}
		s.handleMetrics(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/namespaces/register":
		if r = s.authorizeMutatingRequest(w, r); r == nil {
			return
		}
		s.handleNamespaceRegister(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/admin/namespaces/preview":
		if r = s.authorizeMutatingRequest(w, r); r == nil {
			return
		}
		s.handleAdminNamespacePreview(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/admin/namespaces/update":
		if r = s.authorizeMutatingRequest(w, r); r == nil {
			return
		}
		s.handleAdminNamespaceUpdate(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/v1/admin/namespaces/history":
		s.handleAdminNamespaceHistory(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/v1/namespaces/list":
		s.handleNamespacesList(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/v1/namespaces/get":
		s.handleNamespaceGet(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/context/write":
		if r = s.authorizeMutatingRequest(w, r); r == nil {
			return
		}
		s.handleWrite(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/context/promote":
		s.handlePromoteDeprecated(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/context/promote/request":
		if r = s.authorizeMutatingRequest(w, r); r == nil {
			return
		}
		s.handlePromoteRequest(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/context/promote/approve":
		if r = s.authorizeMutatingRequest(w, r); r == nil {
			return
		}
		s.handlePromoteApprove(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/context/promote/apply":
		if r = s.authorizeMutatingRequest(w, r); r == nil {
			return
		}
		s.handlePromoteApply(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/v1/context/head":
		s.handleHead(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/v1/context/history":
		s.handleHistory(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/v1/health/readiness":
		s.handleReadiness(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/v1/admin/setup":
		s.handleAdminSetup(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/v1/admin/settings":
		s.handleAdminSettings(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/admin/settings/preview":
		if r = s.authorizeMutatingRequest(w, r); r == nil {
			return
		}
		s.handleAdminSettingsPreview(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/admin/settings/apply":
		if r = s.authorizeMutatingRequest(w, r); r == nil {
			return
		}
		s.handleAdminSettingsApply(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/v1/admin/config/backups":
		s.handleAdminConfigBackups(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/admin/config/backup":
		if r = s.authorizeMutatingRequest(w, r); r == nil {
			return
		}
		s.handleAdminConfigBackup(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/admin/config/restore":
		if r = s.authorizeMutatingRequest(w, r); r == nil {
			return
		}
		s.handleAdminConfigRestore(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/v1/admin/queue":
		s.handleAdminQueue(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/v1/admin/queue/failures":
		s.handleAdminQueueFailures(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/admin/queue/retry-failed":
		if r = s.authorizeMutatingRequest(w, r); r == nil {
			return
		}
		s.handleAdminQueueRetryFailed(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/admin/queue/backfill":
		if r = s.authorizeMutatingRequest(w, r); r == nil {
			return
		}
		s.handleAdminQueueBackfill(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/v1/admin/storage":
		s.handleAdminStorage(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/v1/context/audit":
		s.handleAudit(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/views/evaluate":
		s.handleView(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/v1/context/consistency/scan":
		s.handleConsistencyScan(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/context/consistency/repair":
		if r = s.authorizeMutatingRequest(w, r); r == nil {
			return
		}
		s.handleConsistencyRepair(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/maintenance/trim":
		if r = s.authorizeMutatingRequest(w, r); r == nil {
			return
		}
		s.handleMaintenanceTrim(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/maintenance/compact":
		if r = s.authorizeMutatingRequest(w, r); r == nil {
			return
		}
		s.handleMaintenanceCompact(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/auth/tokens/create":
		if r = s.authorizeMutatingRequest(w, r); r == nil {
			return
		}
		s.handleTokenCreate(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/v1/auth/tokens/list":
		s.handleTokenList(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/auth/tokens/revoke":
		if r = s.authorizeMutatingRequest(w, r); r == nil {
			return
		}
		s.handleTokenRevoke(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/broker/plan":
		// Legacy route kept for backward compatibility; internally renamed to context plan.
		s.handleContextPlan(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/context/plan":
		s.handleContextPlan(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/context/packet":
		s.handlePacket(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/context/estimate":
		s.handleEstimate(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/context/typed-write":
		if r = s.authorizeMutatingRequest(w, r); r == nil {
			return
		}
		s.handleTypedWrite(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/context/bulk-ingest":
		if r = s.authorizeMutatingRequest(w, r); r == nil {
			return
		}
		s.handleBulkIngest(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/context/status/promote":
		if r = s.authorizeMutatingRequest(w, r); r == nil {
			return
		}
		s.handleStatusPromote(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/context/status/deprecate":
		if r = s.authorizeMutatingRequest(w, r); r == nil {
			return
		}
		s.handleStatusDeprecate(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/context/typed-view":
		s.handleTypedView(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/v1/context/types":
		s.handleTypesList(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/v1/context/views":
		s.handleViewsList(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/maintenance/ttl-cleanup":
		if r = s.authorizeMutatingRequest(w, r); r == nil {
			return
		}
		s.handleTTLCleanup(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/context/pack":
		s.handleContextPack(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/memory/write":
		if r = s.authorizeMutatingRequest(w, r); r == nil {
			return
		}
		s.handleMemoryWrite(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/memory/recall":
		s.handleMemoryRecall(w, r)
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/memory/revisions/"):
		s.handleMemoryGetRevision(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/v1/memory/current":
		s.handleMemoryGetCurrent(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/v1/memory/history":
		s.handleMemoryHistory(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/memory/touch":
		if r = s.authorizeMutatingRequest(w, r); r == nil {
			return
		}
		s.handleMemoryTouch(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/memory/deprecate":
		if r = s.authorizeMutatingRequest(w, r); r == nil {
			return
		}
		s.handleMemoryDeprecate(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/memory/promote":
		if r = s.authorizeMutatingRequest(w, r); r == nil {
			return
		}
		s.handleMemoryPromote(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/knowledge/write":
		if r = s.authorizeMutatingRequest(w, r); r == nil {
			return
		}
		s.handleKnowledgeWrite(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/v1/knowledge/current":
		s.handleKnowledgeGetCurrent(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/v1/knowledge/history":
		s.handleKnowledgeGetHistory(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/tesseract/lookup":
		s.handleTesseractLookup(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/v1/recall":
		s.handleRecall(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/synthesis/ask":
		s.handleSynthesisAsk(w, r)
	default:
		writeError(w, http.StatusNotFound, "not_found", "endpoint not found", nil)
	}
}

func (s *Server) nextRequestID() string {
	n := atomic.AddUint64(&s.requestSeq, 1)
	return fmt.Sprintf("req-%012d", n)
}

func (s *Server) writeRequestLog(requestID, method, path, rawQuery string, status int, dur time.Duration) {
	dest := s.LogWriter
	if dest == nil {
		return
	}
	mode := strings.ToLower(strings.TrimSpace(s.RequestLogMode))
	if mode == "" {
		mode = "redacted"
	}
	payload := map[string]any{
		"event":      "request",
		"request_id": requestID,
		"method":     method,
		"path":       path,
		"status":     status,
		"latency_ms": dur.Milliseconds(),
	}
	if strings.TrimSpace(rawQuery) != "" {
		if mode == "full" {
			payload["query"] = rawQuery
		} else {
			payload["query"] = "[REDACTED]"
		}
	}
	b, _ := json.Marshal(payload)
	line := string(b) + "\n"
	s.logMu.Lock()
	defer s.logMu.Unlock()
	_, _ = io.WriteString(dest, line)
}

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	_ = r
	writeJSON(w, http.StatusOK, s.metrics.Snapshot())
}

// authorizeMutatingRequest validates auth and returns the (possibly claims-enriched) request.
// Returns nil if auth failed (response already written).
func (s *Server) authorizeMutatingRequest(w http.ResponseWriter, r *http.Request) *http.Request {
	if s.ManagedAuth {
		authHeader := strings.TrimSpace(r.Header.Get("Authorization"))
		if !strings.HasPrefix(authHeader, "Bearer ") {
			writeError(w, http.StatusUnauthorized, "auth_required", "missing or invalid bearer token", nil)
			return nil
		}
		token := strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))
		if token == "" {
			writeError(w, http.StatusUnauthorized, "auth_required", "missing or invalid bearer token", nil)
			return nil
		}
		claims, err := s.Store.ValidateAuthTokenWithClaims(r.Context(), token)
		if err == nil {
			// Attach claims to context for scope/namespace checking downstream.
			ctx := context.WithValue(r.Context(), tokenClaimsKey, claims)
			return r.WithContext(ctx)
		}
		switch {
		case errors.Is(err, contextstore.ErrAuthTokenInvalid),
			errors.Is(err, contextstore.ErrAuthTokenRevoked),
			errors.Is(err, contextstore.ErrAuthTokenExpired):
			writeError(w, http.StatusUnauthorized, "auth_required", "missing or invalid bearer token", map[string]any{
				"reason": err.Error(),
			})
			return nil
		default:
			writeError(w, http.StatusInternalServerError, "auth_failed", err.Error(), nil)
			return nil
		}
	}

	if strings.TrimSpace(s.AuthToken) == "" {
		return r
	}
	authHeader := strings.TrimSpace(r.Header.Get("Authorization"))
	if !strings.HasPrefix(authHeader, "Bearer ") {
		writeError(w, http.StatusUnauthorized, "auth_required", "missing or invalid bearer token", nil)
		return nil
	}
	token := strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))
	if token == "" || token != s.AuthToken {
		writeError(w, http.StatusUnauthorized, "auth_required", "missing or invalid bearer token", nil)
		return nil
	}
	return r
}

type issueTokenRequest struct {
	Label string `json:"label"`
	TTL   string `json:"ttl"`
}

func (s *Server) handleIssueToken(w http.ResponseWriter, r *http.Request) {
	var req issueTokenRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", err.Error(), nil)
		return
	}
	var ttl time.Duration
	if strings.TrimSpace(req.TTL) != "" {
		parsed, err := time.ParseDuration(req.TTL)
		if err != nil {
			writeError(w, http.StatusBadRequest, "validation_error", "ttl must be a valid duration", nil)
			return
		}
		ttl = parsed
	}
	token, meta, err := s.Store.IssueAuthToken(r.Context(), req.Label, ttl)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "auth_failed", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"token": token,
		"meta":  meta,
	})
}

// --- Token management endpoints ---

type tokenCreateRequest struct {
	Name           string   `json:"name"`
	ClientID       string   `json:"client_id"`
	Scopes         []string `json:"scopes"`
	NamespaceGlobs []string `json:"namespace_globs"`
	TTL            string   `json:"ttl"`
	ExpiresAt      string   `json:"expires_at"`
}

func (s *Server) handleTokenCreate(w http.ResponseWriter, r *http.Request) {
	var req tokenCreateRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", err.Error(), nil)
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		writeError(w, http.StatusBadRequest, "validation_error", "name required", nil)
		return
	}

	var ttl time.Duration
	if strings.TrimSpace(req.ExpiresAt) != "" {
		t, err := time.Parse(time.RFC3339, req.ExpiresAt)
		if err != nil {
			// try date-only
			t, err = time.Parse("2006-01-02", req.ExpiresAt)
			if err != nil {
				writeError(w, http.StatusBadRequest, "validation_error", "expires_at must be RFC3339 or YYYY-MM-DD", nil)
				return
			}
			t = t.UTC()
		}
		ttl = time.Until(t)
		if ttl <= 0 {
			writeError(w, http.StatusBadRequest, "validation_error", "expires_at must be in the future", nil)
			return
		}
	} else if strings.TrimSpace(req.TTL) != "" {
		parsed, err := time.ParseDuration(req.TTL)
		if err != nil {
			writeError(w, http.StatusBadRequest, "validation_error", "ttl must be a valid duration", nil)
			return
		}
		ttl = parsed
	}

	token, meta, err := s.Store.CreateAuthToken(r.Context(), contextstore.TokenCreateInput{
		Label:          req.Name,
		ClientID:       req.ClientID,
		Scopes:         req.Scopes,
		NamespaceGlobs: req.NamespaceGlobs,
		TTL:            ttl,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "auth_failed", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"token":           token,
		"id":              meta.TokenID,
		"name":            meta.Label,
		"client_id":       meta.ClientID,
		"scopes":          meta.Scopes,
		"namespace_globs": meta.NamespaceGlobs,
		"created_at":      meta.CreatedAt,
		"expires_at":      meta.ExpiresAt,
	})
}

func (s *Server) handleTokenList(w http.ResponseWriter, r *http.Request) {
	tokens, err := s.Store.ListAuthTokens(r.Context(), 100)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list_failed", err.Error(), nil)
		return
	}
	out := make([]map[string]any, 0, len(tokens))
	for _, t := range tokens {
		out = append(out, map[string]any{
			"id":              t.TokenID,
			"name":            t.Label,
			"client_id":       t.ClientID,
			"scopes":          t.Scopes,
			"namespace_globs": t.NamespaceGlobs,
			"created_at":      t.CreatedAt,
			"expires_at":      t.ExpiresAt,
			"revoked":         t.RevokedAt != "",
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"tokens": out})
}

type tokenRevokeRequest struct {
	ID string `json:"id"`
}

func (s *Server) handleTokenRevoke(w http.ResponseWriter, r *http.Request) {
	var req tokenRevokeRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", err.Error(), nil)
		return
	}
	if strings.TrimSpace(req.ID) == "" {
		writeError(w, http.StatusBadRequest, "validation_error", "id required", nil)
		return
	}
	if err := s.Store.RevokeAuthTokenByID(r.Context(), req.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "revoke_failed", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": req.ID, "revoked": true})
}

type namespaceRegisterRequest struct {
	Namespace string         `json:"namespace"`
	OwnerType string         `json:"owner_type"`
	OwnerID   string         `json:"owner_id"`
	Policy    map[string]any `json:"policy"`
}

type adminNamespacePreviewResponse struct {
	Entry         contextstore.NamespacePolicyEntry `json:"entry"`
	Exists        bool                              `json:"exists"`
	ChangedFields []string                          `json:"changed_fields"`
	Warnings      []string                          `json:"warnings"`
}

type adminNamespaceHistoryResponse struct {
	Namespace string                    `json:"namespace"`
	Items     []contextstore.AuditEvent `json:"items"`
	Count     int                       `json:"count"`
}

func (s *Server) handleNamespaceRegister(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, "namespace.register") {
		return
	}
	var req namespaceRegisterRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", err.Error(), nil)
		return
	}
	entry := contextstore.NamespacePolicyEntry{
		Namespace: req.Namespace,
		OwnerType: req.OwnerType,
		OwnerID:   req.OwnerID,
		Policy:    req.Policy,
	}
	if err := s.Store.UpsertNamespacePolicy(r.Context(), entry); err != nil {
		writeError(w, http.StatusInternalServerError, "write_failed", err.Error(), nil)
		return
	}
	if err := s.Policy.RegisterNamespace(req.Namespace, req.OwnerType, req.OwnerID, req.Policy); err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"namespace":  req.Namespace,
		"owner_type": req.OwnerType,
		"owner_id":   req.OwnerID,
		"policy":     req.Policy,
	})
}

func (s *Server) handleAdminNamespacePreview(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, "namespace.register") {
		return
	}
	resp, status, code, message := s.buildAdminNamespacePreview(r.Context(), r)
	if code != "" {
		writeError(w, status, code, message, nil)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleAdminNamespaceUpdate(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, "namespace.register") {
		return
	}
	resp, status, code, message := s.buildAdminNamespacePreview(r.Context(), r)
	if code != "" {
		writeError(w, status, code, message, nil)
		return
	}
	entry := resp.Entry
	if err := s.Store.UpsertNamespacePolicy(r.Context(), entry); err != nil {
		writeError(w, http.StatusInternalServerError, "write_failed", err.Error(), nil)
		return
	}
	if err := s.Policy.RegisterNamespace(entry.Namespace, entry.OwnerType, entry.OwnerID, entry.Policy); err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", err.Error(), nil)
		return
	}
	meta, _ := json.Marshal(map[string]any{
		"owner_type":     entry.OwnerType,
		"owner_id":       entry.OwnerID,
		"policy":         entry.Policy,
		"changed_fields": resp.ChangedFields,
		"exists_before":  resp.Exists,
	})
	if resp.Exists {
		_ = s.Store.EmitNamespaceUpdate(r.Context(), "system", entry.Namespace, meta)
	} else {
		_ = s.Store.EmitNamespaceRegister(r.Context(), "system", entry.Namespace, meta)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"entry":          entry,
		"exists":         resp.Exists,
		"changed_fields": resp.ChangedFields,
		"warnings":       resp.Warnings,
	})
}

func (s *Server) handleAdminNamespaceHistory(w http.ResponseWriter, r *http.Request) {
	namespace := strings.TrimSpace(r.URL.Query().Get("namespace"))
	if namespace == "" {
		writeError(w, http.StatusBadRequest, "validation_error", "namespace is required", nil)
		return
	}
	limit := 25
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			writeError(w, http.StatusBadRequest, "validation_error", "limit must be a positive integer", nil)
			return
		}
		if n > 100 {
			n = 100
		}
		limit = n
	}
	events, err := s.Store.ListAuditEvents(r.Context(), 200)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "read_failed", err.Error(), nil)
		return
	}
	items := make([]contextstore.AuditEvent, 0, limit)
	for _, event := range events {
		if event.Namespace != namespace {
			continue
		}
		if event.EventType != contextstore.EventNamespaceRegister && event.EventType != contextstore.EventNamespaceUpdate {
			continue
		}
		items = append(items, event)
		if len(items) >= limit {
			break
		}
	}
	writeJSON(w, http.StatusOK, adminNamespaceHistoryResponse{
		Namespace: namespace,
		Items:     items,
		Count:     len(items),
	})
}

func (s *Server) handleNamespacesList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	prefix := strings.TrimSpace(q.Get("prefix"))

	const defaultLimit = 200
	const maxLimit = 1000
	limit := defaultLimit
	if raw := strings.TrimSpace(q.Get("limit")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			writeError(w, http.StatusBadRequest, "validation_error", "limit must be a positive integer", nil)
			return
		}
		if n > maxLimit {
			n = maxLimit
		}
		limit = n
	}

	entries, err := s.Store.ListNamespacePolicies(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "read_failed", err.Error(), nil)
		return
	}

	type item struct {
		Namespace string         `json:"namespace"`
		OwnerType string         `json:"owner_type"`
		OwnerID   string         `json:"owner_id"`
		Policy    map[string]any `json:"policy,omitempty"`
		UpdatedAt string         `json:"updated_at,omitempty"`
	}

	items := make([]item, 0, len(entries))
	matched := 0
	for _, entry := range entries {
		if prefix != "" && !strings.HasPrefix(entry.Namespace, prefix) {
			continue
		}
		matched++
		if len(items) >= limit {
			continue
		}
		items = append(items, item{
			Namespace: entry.Namespace,
			OwnerType: entry.OwnerType,
			OwnerID:   entry.OwnerID,
			Policy:    entry.Policy,
			UpdatedAt: entry.UpdatedAt,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"items":     items,
		"count":     matched,
		"truncated": matched > len(items),
	})
}

func (s *Server) handleNamespaceGet(w http.ResponseWriter, r *http.Request) {
	namespace := strings.TrimSpace(r.URL.Query().Get("namespace"))
	if namespace == "" {
		writeError(w, http.StatusBadRequest, "validation_error", "namespace is required", nil)
		return
	}
	entry, err := s.Store.GetNamespacePolicy(r.Context(), namespace)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "not_found", "namespace not registered", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "read_failed", err.Error(), nil)
		return
	}
	_ = s.Policy.RegisterNamespace(entry.Namespace, entry.OwnerType, entry.OwnerID, entry.Policy)
	writeJSON(w, http.StatusOK, map[string]any{
		"namespace":  namespace,
		"owner_type": entry.OwnerType,
		"owner_id":   entry.OwnerID,
		"policy":     entry.Policy,
	})
}

type writeRequest struct {
	ClientID  string          `json:"client_id"`
	Actor     string          `json:"actor"`
	Namespace string          `json:"namespace"`
	Key       string          `json:"key"`
	Payload   json.RawMessage `json:"payload"`
	Metadata  json.RawMessage `json:"metadata,omitempty"`
	Reason    string          `json:"reason"`
}

func (s *Server) handleWrite(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, "write") {
		return
	}
	var req writeRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", err.Error(), nil)
		return
	}
	if !requireNamespaceAccess(w, r, req.Namespace) {
		return
	}

	ctx, span := feotel.MemoryWriteSpan(r.Context(), req.Namespace, req.Key)
	defer span.End()

	if err := s.Policy.CanWrite(req.ClientID, req.Actor, req.Namespace); err != nil {
		writeError(w, http.StatusForbidden, "policy_denied", err.Error(), nil)
		return
	}
	if err := s.Policy.ValidateTierPolicy(req.Namespace, "write", len(req.Payload), req.Payload); err != nil {
		writeError(w, http.StatusForbidden, "policy_violation", err.Error(), nil)
		return
	}
	if err := s.Policy.ValidatePayload(req.Namespace, req.Payload); err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", err.Error(), nil)
		return
	}
	rec, err := s.Store.AppendRecord(ctx, contextstore.AppendInput{
		Namespace: req.Namespace,
		Key:       req.Key,
		Actor:     req.Actor,
		Payload:   req.Payload,
		Metadata:  req.Metadata,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, "write_failed", err.Error(), nil)
		return
	}
	_ = s.Store.EmitWrite(ctx, req.Actor, req.Namespace, req.Key, rec.Revision, rec.RecordID,
		json.RawMessage(`{"source":"http","reason":`+quoteJSON(req.Reason)+`}`))
	writeJSON(w, http.StatusOK, map[string]any{
		"record_id":     rec.RecordID,
		"revision":      rec.Revision,
		"head_revision": rec.Revision,
		"timestamp":     rec.CreatedAt,
	})
}

type promoteRequest struct {
	ClientID       string `json:"client_id"`
	Actor          string `json:"actor"`
	FromNamespace  string `json:"from_namespace"`
	FromKey        string `json:"from_key"`
	ToNamespace    string `json:"to_namespace"`
	ToKey          string `json:"to_key"`
	SourceRevision *int64 `json:"source_revision,omitempty"`
}

func (s *Server) handlePromote(w http.ResponseWriter, r *http.Request) {
	var req promoteRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", err.Error(), nil)
		return
	}
	if err := s.Policy.CanPromote(req.Actor, req.ToNamespace); err != nil {
		writeError(w, http.StatusForbidden, "policy_denied", err.Error(), nil)
		return
	}

	src, err := s.Store.Head(r.Context(), req.FromNamespace, req.FromKey)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "not_found", "source head not found", nil)
			return
		}
		writeError(w, http.StatusBadRequest, "read_failed", err.Error(), nil)
		return
	}
	if req.SourceRevision != nil && src.Revision != *req.SourceRevision {
		writeError(w, http.StatusConflict, "revision_mismatch", "source revision does not match", nil)
		return
	}

	if err := s.Policy.CanWrite(req.ClientID, req.Actor, req.ToNamespace); err != nil {
		writeError(w, http.StatusForbidden, "policy_denied", err.Error(), nil)
		return
	}
	if err := s.Policy.ValidateTierPolicy(req.ToNamespace, "write", len(src.Payload), src.Payload); err != nil {
		writeError(w, http.StatusForbidden, "policy_violation", err.Error(), nil)
		return
	}
	if err := s.Policy.ValidatePayload(req.ToNamespace, src.Payload); err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", err.Error(), nil)
		return
	}
	out, err := s.Store.AppendRecord(r.Context(), contextstore.AppendInput{
		Namespace: req.ToNamespace,
		Key:       req.ToKey,
		Actor:     req.Actor,
		Payload:   src.Payload,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, "promote_failed", err.Error(), nil)
		return
	}
	_ = s.Store.EmitPromote(r.Context(), contextstore.EventPromote, req.Actor, req.ToNamespace, req.ToKey, out.Revision, out.RecordID,
		json.RawMessage(`{"source":"http","from_namespace":`+quoteJSON(req.FromNamespace)+`,"from_key":`+quoteJSON(req.FromKey)+`}`))
	writeJSON(w, http.StatusOK, map[string]any{
		"promoted_record_id": out.RecordID,
		"target_revision":    out.Revision,
	})
}

func (s *Server) handleHead(w http.ResponseWriter, r *http.Request) {
	namespace := strings.TrimSpace(r.URL.Query().Get("namespace"))
	key := strings.TrimSpace(r.URL.Query().Get("key"))
	if namespace == "" || key == "" {
		writeError(w, http.StatusBadRequest, "validation_error", "namespace and key are required", nil)
		return
	}

	ctx, span := feotel.MemoryReadSpan(r.Context(), namespace, key)
	defer span.End()

	rec, err := s.Store.Head(ctx, namespace, key)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "not_found", "head not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "read_failed", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"record": rec})
}

func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	namespace := strings.TrimSpace(r.URL.Query().Get("namespace"))
	key := strings.TrimSpace(r.URL.Query().Get("key"))
	if namespace == "" || key == "" {
		writeError(w, http.StatusBadRequest, "validation_error", "namespace and key are required", nil)
		return
	}
	limit := 0
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			writeError(w, http.StatusBadRequest, "validation_error", "limit must be a non-negative integer", nil)
			return
		}
		limit = n
	}

	ctx, span := feotel.MemoryReadSpan(r.Context(), namespace, key)
	defer span.End()

	items, err := s.Store.History(ctx, namespace, key, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "read_failed", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": nil})
}

func (s *Server) handleAudit(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			writeError(w, http.StatusBadRequest, "validation_error", "limit must be a non-negative integer", nil)
			return
		}
		limit = n
	}
	cursor := int64(0)
	if raw := strings.TrimSpace(r.URL.Query().Get("cursor")); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || n <= 0 {
			writeError(w, http.StatusBadRequest, "validation_error", "cursor must be a positive integer", nil)
			return
		}
		cursor = n
	}
	namespace := strings.TrimSpace(r.URL.Query().Get("namespace"))
	eventType := strings.TrimSpace(r.URL.Query().Get("event_type"))
	actor := strings.TrimSpace(r.URL.Query().Get("actor"))
	since := strings.TrimSpace(r.URL.Query().Get("since"))
	until := strings.TrimSpace(r.URL.Query().Get("until"))

	// Validate timestamp shapes when provided. We don't store nanosecond
	// precision in audit_events.created_at but RFC3339 / RFC3339Nano both
	// sort lexically; rejecting anything else avoids silently passing
	// non-comparable strings down to the SQL layer.
	if since != "" {
		if _, err := time.Parse(time.RFC3339, since); err != nil {
			if _, err2 := time.Parse(time.RFC3339Nano, since); err2 != nil {
				writeError(w, http.StatusBadRequest, "validation_error", "since must be RFC3339 (e.g. 2026-04-26T00:00:00Z)", nil)
				return
			}
		}
	}
	if until != "" {
		if _, err := time.Parse(time.RFC3339, until); err != nil {
			if _, err2 := time.Parse(time.RFC3339Nano, until); err2 != nil {
				writeError(w, http.StatusBadRequest, "validation_error", "until must be RFC3339 (e.g. 2026-04-26T23:59:59Z)", nil)
				return
			}
		}
	}

	events, nextCursor, err := s.Store.QueryAuditEvents(r.Context(), contextstore.AuditQuery{
		Limit:     limit,
		Cursor:    cursor,
		Namespace: namespace,
		EventType: eventType,
		Actor:     actor,
		Since:     since,
		Until:     until,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "read_failed", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items":       events,
		"count":       len(events),
		"next_cursor": nextCursor,
	})
}

func (s *Server) handleReadiness(w http.ResponseWriter, r *http.Request) {
	report, err := s.Store.Readiness(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "readiness_failed", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, report)
}

type adminPathInfo struct {
	Label    string `json:"label"`
	Path     string `json:"path"`
	Exists   bool   `json:"exists"`
	Kind     string `json:"kind"`
	Writable bool   `json:"writable"`
}

type adminSetupResponse struct {
	App     string           `json:"app"`
	Paths   []adminPathInfo  `json:"paths"`
	Auth    adminAuthInfo    `json:"auth"`
	Runtime adminRuntimeInfo `json:"runtime"`
	Config  adminConfigInfo  `json:"config"`
}

type adminSettingsResponse struct {
	App        string                   `json:"app"`
	ConfigFile string                   `json:"config_file"`
	Paths      []adminPathInfo          `json:"paths"`
	Auth       adminAuthInfo            `json:"auth"`
	Runtime    adminSettingsRuntimeInfo `json:"runtime"`
	Config     adminSettingsConfigInfo  `json:"config"`
	Providers  adminSettingsProviders   `json:"providers"`
}

type adminAuthInfo struct {
	Mode string `json:"mode"`
}

type adminRuntimeInfo struct {
	MetricsEnabled        bool   `json:"metrics_enabled"`
	RequestLoggingEnabled bool   `json:"request_logging_enabled"`
	RequestLogMode        string `json:"request_log_mode"`
	MemoryStoreEnabled    bool   `json:"memory_store_enabled"`
	KnowledgeStoreEnabled bool   `json:"knowledge_store_enabled"`
	SynthesisEnabled      bool   `json:"synthesis_enabled"`
}

type adminConfigInfo struct {
	EmbeddingProvider        string  `json:"embedding_provider"`
	EmbeddingModel           string  `json:"embedding_model"`
	DedupSimilarityThreshold float64 `json:"dedup_similarity_threshold"`
	SynthesisProvider        string  `json:"synthesis_provider"`
	SynthesisModel           string  `json:"synthesis_model"`
	SynthesisMaxTokens       int     `json:"synthesis_max_tokens"`
	SynthesisTemperature     float64 `json:"synthesis_temperature"`
	SynthesisSystemPromptSet bool    `json:"synthesis_system_prompt_set"`
}

type adminSettingsRuntimeInfo struct {
	MetricsEnabled        bool   `json:"metrics_enabled"`
	RequestLoggingEnabled bool   `json:"request_logging_enabled"`
	RequestLogMode        string `json:"request_log_mode"`
	MemoryStoreEnabled    bool   `json:"memory_store_enabled"`
	KnowledgeStoreEnabled bool   `json:"knowledge_store_enabled"`
	SynthesisEnabled      bool   `json:"synthesis_enabled"`
	QueueEnabled          bool   `json:"queue_enabled"`
	WebUIEmbedded         bool   `json:"webui_embedded"`
}

type adminSettingsConfigInfo struct {
	EmbeddingProvider        string  `json:"embedding_provider"`
	EmbeddingModel           string  `json:"embedding_model"`
	DedupSimilarityThreshold float64 `json:"dedup_similarity_threshold"`
	SynthesisProvider        string  `json:"synthesis_provider"`
	SynthesisModel           string  `json:"synthesis_model"`
	SynthesisMaxTokens       int     `json:"synthesis_max_tokens"`
	SynthesisTemperature     float64 `json:"synthesis_temperature"`
	SynthesisSystemPrompt    string  `json:"synthesis_system_prompt"`
	SynthesisSystemPromptSet bool    `json:"synthesis_system_prompt_set"`
}

type adminSettingsProviders struct {
	Embedding adminProviderStatus `json:"embedding"`
	Synthesis adminProviderStatus `json:"synthesis"`
}

type adminSettingsUpdateRequest struct {
	Config adminSettingsConfigInfo `json:"config"`
}

type adminSettingsMutationResponse struct {
	ConfigFile      string                  `json:"config_file"`
	Config          adminSettingsConfigInfo `json:"config"`
	Providers       adminSettingsProviders  `json:"providers"`
	ChangedFields   []string                `json:"changed_fields"`
	Warnings        []string                `json:"warnings"`
	RestartRequired bool                    `json:"restart_required"`
	Applied         bool                    `json:"applied"`
}

type adminConfigBackupInfo struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	Size      int64  `json:"size"`
	Source    string `json:"source"`
	CreatedAt string `json:"created_at"`
}

type adminConfigBackupsResponse struct {
	ConfigFile string                  `json:"config_file"`
	BackupDir  string                  `json:"backup_dir"`
	Items      []adminConfigBackupInfo `json:"items"`
}

type adminConfigBackupResponse struct {
	ConfigFile string                `json:"config_file"`
	BackupDir  string                `json:"backup_dir"`
	Backup     adminConfigBackupInfo `json:"backup"`
}

type adminConfigRestoreRequest struct {
	Path string `json:"path"`
}

type adminConfigRestoreResponse struct {
	ConfigFile       string                  `json:"config_file"`
	RestoredFrom     adminConfigBackupInfo   `json:"restored_from"`
	PreRestoreBackup adminConfigBackupInfo   `json:"pre_restore_backup"`
	Config           adminSettingsConfigInfo `json:"config"`
	Providers        adminSettingsProviders  `json:"providers"`
	RestartRequired  bool                    `json:"restart_required"`
}

type adminProviderStatus struct {
	Kind         string `json:"kind"`
	Provider     string `json:"provider"`
	Model        string `json:"model"`
	EnvVar       string `json:"env_var,omitempty"`
	Configured   bool   `json:"configured"`
	Supported    bool   `json:"supported"`
	EnvPresent   bool   `json:"env_present"`
	RuntimeReady bool   `json:"runtime_ready"`
	Available    bool   `json:"available"`
	Reason       string `json:"reason,omitempty"`
}

type adminQueueResponse struct {
	Enabled       bool                 `json:"enabled"`
	Queue         string               `json:"queue"`
	Path          string               `json:"path"`
	Worker        adminQueueWorkerInfo `json:"worker"`
	Total         int64                `json:"total"`
	Available     int64                `json:"available"`
	Delayed       int64                `json:"delayed"`
	Reserved      int64                `json:"reserved"`
	Failed        int64                `json:"failed"`
	OldestCreated string               `json:"oldest_created_at,omitempty"`
	NextAvailable string               `json:"next_available_at,omitempty"`
	ActiveByType  []adminQueueTypeInfo `json:"active_by_type"`
	GeneratedAt   string               `json:"generated_at"`
}

type adminQueueWorkerInfo struct {
	Configured   bool   `json:"configured"`
	Concurrency  int    `json:"concurrency"`
	MaxTries     int    `json:"max_tries"`
	RetryAfter   string `json:"retry_after"`
	PollInterval string `json:"poll_interval"`
}

type adminQueueTypeInfo struct {
	Type  string `json:"type"`
	Count int64  `json:"count"`
}

type adminQueueFailureInfo struct {
	ID       int64  `json:"id"`
	Queue    string `json:"queue"`
	Type     string `json:"type"`
	Error    string `json:"error"`
	Attempts int    `json:"attempts"`
	FailedAt string `json:"failed_at"`
	Payload  string `json:"payload,omitempty"`
}

type adminQueueFailuresResponse struct {
	Items []adminQueueFailureInfo `json:"items"`
	Count int                     `json:"count"`
}

type adminQueueRetryFailedRequest struct {
	ID int64 `json:"id"`
}

type adminQueueRetryFailedResponse struct {
	Retried int `json:"retried"`
}

type adminQueueBackfillRequest struct {
	Namespace string `json:"namespace"`
	Limit     int    `json:"limit"`
}

type adminQueueBackfillResponse struct {
	Queued    int    `json:"queued"`
	Namespace string `json:"namespace,omitempty"`
	Limit     int    `json:"limit"`
}

type adminStorageResponse struct {
	GeneratedAt     string                     `json:"generated_at"`
	TotalBytes      int64                      `json:"total_bytes"`
	Paths           []adminStoragePathInfo     `json:"paths"`
	Records         adminStorageRecordInfo     `json:"records"`
	NamespacePolicy adminNamespacePolicyInfo   `json:"namespace_policy"`
	TopNamespaces   []adminNamespaceRecordInfo `json:"top_namespaces"`
}

type adminStoragePathInfo struct {
	Label  string `json:"label"`
	Path   string `json:"path"`
	Exists bool   `json:"exists"`
	Kind   string `json:"kind"`
	Bytes  int64  `json:"bytes"`
	Error  string `json:"error,omitempty"`
}

type adminStorageRecordInfo struct {
	Revisions int64  `json:"revisions"`
	Heads     int64  `json:"heads"`
	Expired   int64  `json:"expired"`
	Oldest    string `json:"oldest_created_at,omitempty"`
	Newest    string `json:"newest_created_at,omitempty"`
}

type adminNamespacePolicyInfo struct {
	Namespaces          int `json:"namespaces"`
	WithRetention       int `json:"with_retention"`
	WithMaxRevisions    int `json:"with_max_revisions"`
	WithMaxBytesPerKey  int `json:"with_max_bytes_per_key"`
	WithoutPolicyLimits int `json:"without_policy_limits"`
}

type adminNamespaceRecordInfo struct {
	Namespace string `json:"namespace"`
	Revisions int64  `json:"revisions"`
	Keys      int64  `json:"keys"`
	Oldest    string `json:"oldest_created_at,omitempty"`
	Newest    string `json:"newest_created_at,omitempty"`
}

func (s *Server) handleAdminSetup(w http.ResponseWriter, r *http.Request) {
	cfg := s.effectiveRuntimeConfig()
	writeJSON(w, http.StatusOK, adminSetupResponse{
		App:   config.AppName,
		Paths: s.adminPaths(r.Context()),
		Auth:  adminAuthInfo{Mode: s.adminAuthMode()},
		Runtime: adminRuntimeInfo{
			MetricsEnabled:        s.EnableMetrics,
			RequestLoggingEnabled: s.EnableRequestLogging,
			RequestLogMode:        s.adminRequestLogMode(),
			MemoryStoreEnabled:    s.MemoryStore != nil,
			KnowledgeStoreEnabled: s.KnowledgeStore != nil,
			SynthesisEnabled:      s.SynthesisProvider != nil,
		},
		Config: adminConfigInfo{
			EmbeddingProvider:        cfg.Embedding.Provider,
			EmbeddingModel:           cfg.Embedding.Model,
			DedupSimilarityThreshold: cfg.Dedup.SimilarityThreshold,
			SynthesisProvider:        cfg.Synthesis.Provider,
			SynthesisModel:           cfg.Synthesis.Model,
			SynthesisMaxTokens:       cfg.Synthesis.MaxTokens,
			SynthesisTemperature:     cfg.Synthesis.Temperature,
			SynthesisSystemPromptSet: cfg.Synthesis.SystemPrompt != "",
		},
	})
}

func (s *Server) handleAdminSettings(w http.ResponseWriter, r *http.Request) {
	cfg := s.effectiveRuntimeConfig()
	configFile := s.adminConfigFile()
	writeJSON(w, http.StatusOK, adminSettingsResponse{
		App:        config.AppName,
		ConfigFile: configFile,
		Paths:      s.adminPaths(r.Context()),
		Auth:       adminAuthInfo{Mode: s.adminAuthMode()},
		Runtime: adminSettingsRuntimeInfo{
			MetricsEnabled:        s.EnableMetrics,
			RequestLoggingEnabled: s.EnableRequestLogging,
			RequestLogMode:        s.adminRequestLogMode(),
			MemoryStoreEnabled:    s.MemoryStore != nil,
			KnowledgeStoreEnabled: s.KnowledgeStore != nil,
			SynthesisEnabled:      s.SynthesisProvider != nil,
			QueueEnabled:          s.QueueDB != nil,
			WebUIEmbedded:         true,
		},
		Config: adminSettingsConfigInfo{
			EmbeddingProvider:        cfg.Embedding.Provider,
			EmbeddingModel:           cfg.Embedding.Model,
			DedupSimilarityThreshold: cfg.Dedup.SimilarityThreshold,
			SynthesisProvider:        cfg.Synthesis.Provider,
			SynthesisModel:           cfg.Synthesis.Model,
			SynthesisMaxTokens:       cfg.Synthesis.MaxTokens,
			SynthesisTemperature:     cfg.Synthesis.Temperature,
			SynthesisSystemPrompt:    cfg.Synthesis.SystemPrompt,
			SynthesisSystemPromptSet: cfg.Synthesis.SystemPrompt != "",
		},
		Providers: adminSettingsProviders{
			Embedding: adminEmbeddingProviderStatus(cfg, s.MemoryStore != nil),
			Synthesis: adminSynthesisProviderStatus(cfg, s.SynthesisProvider != nil),
		},
	})
}

func (s *Server) handleAdminSettingsPreview(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, "admin") {
		return
	}
	resp, status, code, message := s.buildAdminSettingsMutationResponse(r)
	if code != "" {
		writeError(w, status, code, message, nil)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleAdminSettingsApply(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, "admin") {
		return
	}
	resp, status, code, message := s.buildAdminSettingsMutationResponse(r)
	if code != "" {
		writeError(w, status, code, message, nil)
		return
	}
	if err := config.Save(resp.ConfigFile, adminSettingsConfigToConfig(s.effectiveRuntimeConfig(), resp.Config)); err != nil {
		writeError(w, http.StatusInternalServerError, "config_write_failed", err.Error(), nil)
		return
	}
	s.RuntimeConfig = adminSettingsConfigToConfig(s.effectiveRuntimeConfig(), resp.Config)
	s.SynthesisConfig = s.RuntimeConfig.Synthesis
	resp.Applied = true
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleAdminConfigBackups(w http.ResponseWriter, r *http.Request) {
	configFile := s.adminConfigFile()
	backupDir := s.adminConfigBackupDir()
	items, err := s.readAdminConfigBackups()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "config_backup_list_failed", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, adminConfigBackupsResponse{
		ConfigFile: configFile,
		BackupDir:  backupDir,
		Items:      items,
	})
}

func (s *Server) handleAdminConfigBackup(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, "admin") {
		return
	}
	backup, err := s.createAdminConfigBackup("manual")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "config_backup_failed", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, adminConfigBackupResponse{
		ConfigFile: s.adminConfigFile(),
		BackupDir:  s.adminConfigBackupDir(),
		Backup:     backup,
	})
}

func (s *Server) handleAdminConfigRestore(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, "admin") {
		return
	}
	var req adminConfigRestoreRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", err.Error(), nil)
		return
	}
	target, err := s.resolveAdminBackupPath(req.Path)
	if err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", err.Error(), nil)
		return
	}
	restoredInfo, err := s.statAdminBackup(target, "manual")
	if err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", err.Error(), nil)
		return
	}
	preRestore, err := s.createAdminConfigBackup("pre-restore")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "config_backup_failed", err.Error(), nil)
		return
	}
	data, err := os.ReadFile(target)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "config_restore_failed", err.Error(), nil)
		return
	}
	configFile := s.adminConfigFile()
	if configFile == "" {
		writeError(w, http.StatusInternalServerError, "config_file_unavailable", "config file path is not available", nil)
		return
	}
	if err := os.MkdirAll(filepath.Dir(configFile), 0o755); err != nil {
		writeError(w, http.StatusInternalServerError, "config_restore_failed", err.Error(), nil)
		return
	}
	if err := os.WriteFile(configFile, data, 0o644); err != nil {
		writeError(w, http.StatusInternalServerError, "config_restore_failed", err.Error(), nil)
		return
	}
	cfg, err := config.Load(configFile)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "config_restore_failed", err.Error(), nil)
		return
	}
	s.RuntimeConfig = cfg
	s.SynthesisConfig = cfg.Synthesis
	writeJSON(w, http.StatusOK, adminConfigRestoreResponse{
		ConfigFile:       configFile,
		RestoredFrom:     restoredInfo,
		PreRestoreBackup: preRestore,
		Config:           adminSettingsConfigFromConfig(cfg),
		Providers:        adminSettingsProvidersFromConfig(cfg, s),
		RestartRequired:  true,
	})
}

func (s *Server) buildAdminSettingsMutationResponse(r *http.Request) (adminSettingsMutationResponse, int, string, string) {
	var req adminSettingsUpdateRequest
	if err := decodeJSON(r, &req); err != nil {
		return adminSettingsMutationResponse{}, http.StatusBadRequest, "validation_error", err.Error()
	}
	next, warnings, err := validateAdminSettingsUpdate(req.Config)
	if err != nil {
		return adminSettingsMutationResponse{}, http.StatusBadRequest, "validation_error", err.Error()
	}
	configFile := s.adminConfigFile()
	if strings.TrimSpace(configFile) == "" {
		return adminSettingsMutationResponse{}, http.StatusInternalServerError, "config_file_unavailable", "config file path is not available"
	}
	current := adminSettingsConfigFromConfig(s.effectiveRuntimeConfig())
	next = adminSettingsConfigFromConfig(adminSettingsConfigToConfig(s.effectiveRuntimeConfig(), next))
	return adminSettingsMutationResponse{
		ConfigFile:      configFile,
		Config:          next,
		Providers:       adminSettingsProvidersFromConfig(adminSettingsConfigToConfig(s.effectiveRuntimeConfig(), next), s),
		ChangedFields:   adminSettingsChangedFields(current, next),
		Warnings:        warnings,
		RestartRequired: true,
	}, http.StatusOK, "", ""
}

func (s *Server) effectiveRuntimeConfig() config.Config {
	return config.Normalize(s.RuntimeConfig)
}

func (s *Server) adminConfigFile() string {
	configFile := s.ConfigFile
	if configFile == "" && s.Layout.ConfigDir() != "" {
		configFile = filepath.Join(s.Layout.ConfigDir(), "config.yaml")
	}
	return configFile
}

func (s *Server) adminQueueDBPath() string {
	queueDBPath := s.QueueDBPath
	if queueDBPath == "" && s.Layout.StateDir() != "" {
		queueDBPath = filepath.Join(s.Layout.StateDir(), "queue.db")
	}
	return queueDBPath
}

func (s *Server) adminConfigBackupDir() string {
	configFile := s.adminConfigFile()
	if configFile != "" {
		return filepath.Join(filepath.Dir(configFile), "backups")
	}
	if s.Layout.ConfigDir() != "" {
		return filepath.Join(s.Layout.ConfigDir(), "backups")
	}
	return ""
}

func (s *Server) adminAuthMode() string {
	switch {
	case s.ManagedAuth:
		return "managed"
	case strings.TrimSpace(s.AuthToken) != "":
		return "static-token"
	default:
		return "open"
	}
}

func (s *Server) adminRequestLogMode() string {
	requestLogMode := s.RequestLogMode
	if requestLogMode == "" {
		requestLogMode = "redacted"
	}
	return requestLogMode
}

func (s *Server) adminPaths(ctx context.Context) []adminPathInfo {
	layout := s.Layout
	configFile := s.adminConfigFile()
	queueDBPath := s.adminQueueDBPath()

	paths := make([]adminPathInfo, 0, 8)
	if layout.DataDir() != "" {
		paths = append(paths,
			adminPath("data", layout.DataDir()),
			adminPath("state", layout.StateDir()),
			adminPath("cache", layout.CacheDir()),
			adminPath("config", layout.ConfigDir()),
			adminPath("workspace", layout.Workspace().Dir),
			adminPath("main-db", layout.MainDB()),
		)
	}
	if configFile != "" {
		paths = append(paths, adminPath("config-file", configFile))
	}
	if queueDBPath != "" {
		paths = append(paths, adminPath("queue-db", queueDBPath))
	}
	if report, err := s.Store.Readiness(ctx); err == nil {
		paths = append(paths, adminPath("records", report.RecordsDir))
		if layout.MainDB() == "" && report.DBPath != "" {
			paths = append(paths, adminPath("main-db", report.DBPath))
		}
	}
	return paths
}

func adminEmbeddingProviderStatus(cfg config.Config, runtimeReady bool) adminProviderStatus {
	name := strings.ToLower(strings.TrimSpace(cfg.Embedding.Provider))
	status := adminProviderStatus{
		Kind:         "embedding",
		Provider:     name,
		Model:        strings.TrimSpace(cfg.Embedding.Model),
		Configured:   name != "",
		Supported:    name == "openai",
		RuntimeReady: runtimeReady,
	}
	switch name {
	case "":
		status.Reason = "no embedding provider configured"
	case "openai":
		status.EnvVar = "OPENAI_API_KEY"
		status.EnvPresent = os.Getenv(status.EnvVar) != ""
	default:
		status.Reason = "unsupported embedding provider"
	}
	if status.Configured && status.Supported && status.EnvPresent {
		status.Available = true
	}
	if status.Configured && status.Supported && !status.EnvPresent {
		status.Reason = status.EnvVar + " not set"
	}
	return status
}

func adminSynthesisProviderStatus(cfg config.Config, runtimeReady bool) adminProviderStatus {
	name := strings.ToLower(strings.TrimSpace(cfg.Synthesis.Provider))
	status := adminProviderStatus{
		Kind:         "synthesis",
		Provider:     name,
		Model:        strings.TrimSpace(cfg.Synthesis.Model),
		Configured:   name != "",
		Supported:    name == "openai" || name == "anthropic",
		RuntimeReady: runtimeReady,
	}
	switch name {
	case "":
		status.Reason = "no synthesis provider configured"
	case "openai":
		status.EnvVar = "OPENAI_API_KEY"
		status.EnvPresent = os.Getenv(status.EnvVar) != ""
	case "anthropic":
		status.EnvVar = "ANTHROPIC_API_KEY"
		status.EnvPresent = os.Getenv(status.EnvVar) != ""
	default:
		status.Reason = "unsupported synthesis provider"
	}
	if status.Configured && status.Supported && status.EnvPresent {
		status.Available = true
	}
	if status.Configured && status.Supported && !status.EnvPresent {
		status.Reason = status.EnvVar + " not set"
	}
	return status
}

func adminSettingsConfigFromConfig(cfg config.Config) adminSettingsConfigInfo {
	cfg = config.Normalize(cfg)
	return adminSettingsConfigInfo{
		EmbeddingProvider:        cfg.Embedding.Provider,
		EmbeddingModel:           cfg.Embedding.Model,
		DedupSimilarityThreshold: cfg.Dedup.SimilarityThreshold,
		SynthesisProvider:        cfg.Synthesis.Provider,
		SynthesisModel:           cfg.Synthesis.Model,
		SynthesisMaxTokens:       cfg.Synthesis.MaxTokens,
		SynthesisTemperature:     cfg.Synthesis.Temperature,
		SynthesisSystemPrompt:    cfg.Synthesis.SystemPrompt,
		SynthesisSystemPromptSet: cfg.Synthesis.SystemPrompt != "",
	}
}

// adminSettingsConfigToConfig folds the admin-editable fields in info onto
// base.
//
// base matters: the admin settings surface exposes only a subset of
// config.Config, so rebuilding from config.Defaults() here would silently
// reset every section the surface does not know about — read.payload_mode
// among them — on any settings apply. Folding onto the current config keeps
// unexposed sections intact.
func adminSettingsConfigToConfig(base config.Config, info adminSettingsConfigInfo) config.Config {
	cfg := base
	cfg.Embedding.Provider = strings.ToLower(strings.TrimSpace(info.EmbeddingProvider))
	cfg.Embedding.Model = strings.TrimSpace(info.EmbeddingModel)
	cfg.Dedup.SimilarityThreshold = info.DedupSimilarityThreshold
	cfg.Synthesis.Provider = strings.ToLower(strings.TrimSpace(info.SynthesisProvider))
	cfg.Synthesis.Model = strings.TrimSpace(info.SynthesisModel)
	cfg.Synthesis.MaxTokens = info.SynthesisMaxTokens
	cfg.Synthesis.Temperature = info.SynthesisTemperature
	cfg.Synthesis.SystemPrompt = strings.TrimSpace(info.SynthesisSystemPrompt)
	if cfg.Synthesis.Provider == "" {
		cfg.Synthesis = config.SynthesisConfig{}
	}
	return config.Normalize(cfg)
}

func adminSettingsProvidersFromConfig(cfg config.Config, s *Server) adminSettingsProviders {
	return adminSettingsProviders{
		Embedding: adminEmbeddingProviderStatus(cfg, s.MemoryStore != nil),
		Synthesis: adminSynthesisProviderStatus(cfg, s.SynthesisProvider != nil),
	}
}

func validateAdminSettingsUpdate(info adminSettingsConfigInfo) (adminSettingsConfigInfo, []string, error) {
	info.EmbeddingProvider = strings.ToLower(strings.TrimSpace(info.EmbeddingProvider))
	info.EmbeddingModel = strings.TrimSpace(info.EmbeddingModel)
	info.SynthesisProvider = strings.ToLower(strings.TrimSpace(info.SynthesisProvider))
	info.SynthesisModel = strings.TrimSpace(info.SynthesisModel)
	info.SynthesisSystemPrompt = strings.TrimSpace(info.SynthesisSystemPrompt)

	if info.EmbeddingProvider == "" {
		return info, nil, errors.New("embedding_provider is required")
	}
	if info.EmbeddingProvider != "openai" {
		return info, nil, errors.New("embedding_provider must be openai")
	}
	if info.EmbeddingModel == "" {
		return info, nil, errors.New("embedding_model is required")
	}
	if info.DedupSimilarityThreshold <= 0 || info.DedupSimilarityThreshold > 1 {
		return info, nil, errors.New("dedup_similarity_threshold must be between 0 and 1")
	}
	if info.SynthesisProvider != "" && info.SynthesisProvider != "openai" && info.SynthesisProvider != "anthropic" {
		return info, nil, errors.New("synthesis_provider must be empty, openai, or anthropic")
	}
	// The two folds below pass config.Defaults() rather than the live config
	// on purpose, and unlike the apply path that difference is safe: these
	// round-trip through adminSettingsConfigFromConfig only to normalize the
	// admin-editable fields, and the config.Config they build is discarded
	// immediately. No unexposed section can escape, so there is nothing to
	// preserve. Threading the live config here would also be wrong —
	// validateAdminSettingsUpdate is a pure function of its input, and
	// reading server state would make preview results depend on load order.
	if info.SynthesisProvider == "" {
		info.SynthesisModel = ""
		info.SynthesisMaxTokens = 0
		info.SynthesisTemperature = 0
		info.SynthesisSystemPrompt = ""
		return adminSettingsConfigFromConfig(adminSettingsConfigToConfig(config.Defaults(), info)), []string{
			"synthesis is disabled until a provider is configured and the matching API key is set",
		}, nil
	}
	if info.SynthesisModel == "" {
		return info, nil, errors.New("synthesis_model is required when synthesis_provider is set")
	}
	if info.SynthesisMaxTokens < 0 {
		return info, nil, errors.New("synthesis_max_tokens must be zero or a positive integer")
	}
	if info.SynthesisTemperature < 0 || info.SynthesisTemperature > 2 {
		return info, nil, errors.New("synthesis_temperature must be between 0 and 2")
	}

	warnings := []string{
		"restart required for provider/runtime changes to take effect in the active daemon",
	}
	return adminSettingsConfigFromConfig(adminSettingsConfigToConfig(config.Defaults(), info)), warnings, nil
}

func adminSettingsChangedFields(current, next adminSettingsConfigInfo) []string {
	type field struct {
		name string
		same bool
	}
	fields := []field{
		{name: "embedding.provider", same: current.EmbeddingProvider == next.EmbeddingProvider},
		{name: "embedding.model", same: current.EmbeddingModel == next.EmbeddingModel},
		{name: "dedup.similarity_threshold", same: current.DedupSimilarityThreshold == next.DedupSimilarityThreshold},
		{name: "synthesis.provider", same: current.SynthesisProvider == next.SynthesisProvider},
		{name: "synthesis.model", same: current.SynthesisModel == next.SynthesisModel},
		{name: "synthesis.max_tokens", same: current.SynthesisMaxTokens == next.SynthesisMaxTokens},
		{name: "synthesis.temperature", same: current.SynthesisTemperature == next.SynthesisTemperature},
		{name: "synthesis.system_prompt", same: current.SynthesisSystemPrompt == next.SynthesisSystemPrompt},
	}
	changed := make([]string, 0, len(fields))
	for _, field := range fields {
		if !field.same {
			changed = append(changed, field.name)
		}
	}
	return changed
}

func (s *Server) buildAdminNamespacePreview(ctx context.Context, r *http.Request) (adminNamespacePreviewResponse, int, string, string) {
	var req namespaceRegisterRequest
	if err := decodeJSON(r, &req); err != nil {
		return adminNamespacePreviewResponse{}, http.StatusBadRequest, "validation_error", err.Error()
	}
	entry, warnings, err := validateNamespacePolicyEntry(req)
	if err != nil {
		return adminNamespacePreviewResponse{}, http.StatusBadRequest, "validation_error", err.Error()
	}
	if err := contextpolicy.New().RegisterNamespace(entry.Namespace, entry.OwnerType, entry.OwnerID, entry.Policy); err != nil {
		return adminNamespacePreviewResponse{}, http.StatusBadRequest, "validation_error", err.Error()
	}
	current, err := s.Store.GetNamespacePolicy(ctx, entry.Namespace)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return adminNamespacePreviewResponse{}, http.StatusInternalServerError, "read_failed", err.Error()
	}
	exists := err == nil
	changedFields := adminNamespaceChangedFields(current, entry, exists)
	return adminNamespacePreviewResponse{
		Entry:         entry,
		Exists:        exists,
		ChangedFields: changedFields,
		Warnings:      warnings,
	}, http.StatusOK, "", ""
}

func validateNamespacePolicyEntry(req namespaceRegisterRequest) (contextstore.NamespacePolicyEntry, []string, error) {
	entry := contextstore.NamespacePolicyEntry{
		Namespace: strings.TrimSpace(req.Namespace),
		OwnerType: strings.TrimSpace(req.OwnerType),
		OwnerID:   strings.TrimSpace(req.OwnerID),
		Policy:    normalizeNamespacePolicy(req.Policy),
	}
	if entry.Namespace == "" {
		return entry, nil, errors.New("namespace required")
	}
	if entry.OwnerType == "" {
		return entry, nil, errors.New("owner_type required")
	}
	if entry.OwnerID == "" {
		return entry, nil, errors.New("owner_id required")
	}
	tierPolicy := contextpolicy.ParseTierPolicy(entry.Policy)
	if tierPolicy.Retention != "" {
		if _, err := time.ParseDuration(tierPolicy.Retention); err != nil {
			return entry, nil, errors.New("retention must be a valid duration (e.g. 720h)")
		}
	}
	if tierPolicy.MaxRevisions < 0 {
		return entry, nil, errors.New("max_revisions must be zero or a positive integer")
	}
	if tierPolicy.MaxBytesPerKey < 0 {
		return entry, nil, errors.New("max_bytes_per_key must be zero or a positive integer")
	}
	warnings := make([]string, 0, 2)
	if tierPolicy.Retention == "" && tierPolicy.MaxRevisions == 0 && tierPolicy.MaxBytesPerKey == 0 {
		warnings = append(warnings, "namespace has no retention or revision limits")
	}
	if tierPolicy.Tier == "" {
		warnings = append(warnings, "namespace tier is not set")
	}
	entry.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	return entry, warnings, nil
}

func normalizeNamespacePolicy(policy map[string]any) map[string]any {
	if policy == nil {
		return map[string]any{}
	}
	normalized := make(map[string]any, len(policy))
	for key, value := range policy {
		switch v := value.(type) {
		case string:
			trimmed := strings.TrimSpace(v)
			if trimmed != "" {
				normalized[key] = trimmed
			}
		default:
			normalized[key] = value
		}
	}
	return normalized
}

func adminNamespaceChangedFields(current, next contextstore.NamespacePolicyEntry, exists bool) []string {
	if !exists {
		return []string{"namespace", "owner_type", "owner_id", "policy"}
	}
	changed := make([]string, 0, 4)
	if current.OwnerType != next.OwnerType {
		changed = append(changed, "owner_type")
	}
	if current.OwnerID != next.OwnerID {
		changed = append(changed, "owner_id")
	}
	if !reflect.DeepEqual(current.Policy, next.Policy) {
		changed = append(changed, "policy")
	}
	return changed
}

func (s *Server) readAdminConfigBackups() ([]adminConfigBackupInfo, error) {
	backupDir := s.adminConfigBackupDir()
	if backupDir == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []adminConfigBackupInfo{}, nil
		}
		return nil, err
	}
	items := make([]adminConfigBackupInfo, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := s.statAdminBackup(filepath.Join(backupDir, entry.Name()), backupSourceFromName(entry.Name()))
		if err != nil {
			continue
		}
		items = append(items, info)
	}
	slices.SortFunc(items, func(a, b adminConfigBackupInfo) int {
		return strings.Compare(b.CreatedAt, a.CreatedAt)
	})
	return items, nil
}

func (s *Server) createAdminConfigBackup(source string) (adminConfigBackupInfo, error) {
	backupDir := s.adminConfigBackupDir()
	if backupDir == "" {
		return adminConfigBackupInfo{}, errors.New("config backup directory is not available")
	}
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		return adminConfigBackupInfo{}, err
	}
	configFile := s.adminConfigFile()
	var data []byte
	if configFile != "" {
		if b, err := os.ReadFile(configFile); err == nil {
			data = b
		} else if !os.IsNotExist(err) {
			return adminConfigBackupInfo{}, err
		}
	}
	if len(data) == 0 {
		cfg := s.effectiveRuntimeConfig()
		var err error
		data, err = yaml.Marshal(cfg)
		if err != nil {
			return adminConfigBackupInfo{}, err
		}
		if source == "manual" {
			source = "runtime-snapshot"
		}
	}
	name := fmt.Sprintf("config-%s-%s.yaml", source, time.Now().UTC().Format("20060102T150405Z"))
	target := filepath.Join(backupDir, name)
	if err := os.WriteFile(target, data, 0o644); err != nil {
		return adminConfigBackupInfo{}, err
	}
	return s.statAdminBackup(target, source)
}

func (s *Server) resolveAdminBackupPath(raw string) (string, error) {
	backupDir := s.adminConfigBackupDir()
	if backupDir == "" {
		return "", errors.New("config backup directory is not available")
	}
	name := strings.TrimSpace(raw)
	if name == "" {
		return "", errors.New("path is required")
	}
	candidate := name
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(backupDir, candidate)
	}
	cleanDir := filepath.Clean(backupDir)
	cleanPath := filepath.Clean(candidate)
	rel, err := filepath.Rel(cleanDir, cleanPath)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("backup path must remain within the config backup directory")
	}
	return cleanPath, nil
}

func (s *Server) statAdminBackup(path string, source string) (adminConfigBackupInfo, error) {
	stat, err := os.Stat(path)
	if err != nil {
		return adminConfigBackupInfo{}, err
	}
	return adminConfigBackupInfo{
		Name:      filepath.Base(path),
		Path:      path,
		Size:      stat.Size(),
		Source:    source,
		CreatedAt: stat.ModTime().UTC().Format(time.RFC3339),
	}, nil
}

func backupSourceFromName(name string) string {
	base := strings.TrimSuffix(filepath.Base(name), filepath.Ext(name))
	parts := strings.Split(base, "-")
	if len(parts) >= 3 {
		return strings.Join(parts[1:len(parts)-1], "-")
	}
	return "manual"
}

func (s *Server) retryFailedQueueJobs(ctx context.Context, id int64) (int, error) {
	tx, err := s.QueueDB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	query := `
SELECT id, queue, type, payload, attempts
FROM failed_jobs
WHERE queue = ?`
	args := []any{"tesseract"}
	if id > 0 {
		query += " AND id = ?"
		args = append(args, id)
	}
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	type failedJob struct {
		ID       int64
		Queue    string
		Type     string
		Payload  []byte
		Attempts int
	}
	var jobs []failedJob
	for rows.Next() {
		var job failedJob
		if err := rows.Scan(&job.ID, &job.Queue, &job.Type, &job.Payload, &job.Attempts); err != nil {
			return 0, err
		}
		jobs = append(jobs, job)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	now := time.Now().Unix()
	for _, job := range jobs {
		if _, err := tx.ExecContext(ctx, `
INSERT INTO jobs (queue, type, payload, attempts, max_tries, reserved_at, available_at, created_at)
VALUES (?, ?, ?, 0, 3, NULL, ?, ?)`,
			job.Queue, job.Type, job.Payload, now, now); err != nil {
			return 0, err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM failed_jobs WHERE id = ?`, job.ID); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return len(jobs), nil
}

func (s *Server) enqueueEmbeddingBackfill(ctx context.Context, namespace string, limit int) (int, error) {
	query := `SELECT revision_id FROM memory_revisions WHERE embedding_vector IS NULL`
	args := make([]any, 0, 2)
	if namespace != "" {
		query += ` AND namespace = ?`
		args = append(args, namespace)
	}
	query += ` ORDER BY created_at ASC`
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.Store.DB().QueryContext(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	now := time.Now().Unix()
	queued := 0
	for rows.Next() {
		var revisionID string
		if err := rows.Scan(&revisionID); err != nil {
			return queued, err
		}
		payload := []byte(fmt.Sprintf(`{"revision_id":%q}`, revisionID))
		if _, err := s.QueueDB.ExecContext(ctx, `
INSERT INTO jobs (queue, type, payload, attempts, max_tries, reserved_at, available_at, created_at)
VALUES (?, 'embed', ?, 0, 3, NULL, ?, ?)`,
			"tesseract", payload, now, now); err != nil {
			return queued, err
		}
		queued++
	}
	if err := rows.Err(); err != nil {
		return queued, err
	}
	return queued, nil
}

func (s *Server) handleAdminQueue(w http.ResponseWriter, r *http.Request) {
	const queueName = "tesseract"
	resp := adminQueueResponse{
		Enabled: s.QueueDB != nil,
		Queue:   queueName,
		Path:    s.QueueDBPath,
		Worker: adminQueueWorkerInfo{
			Configured:   s.QueueDB != nil && s.MemoryStore != nil,
			Concurrency:  1,
			MaxTries:     3,
			RetryAfter:   (30 * time.Second).String(),
			PollInterval: (3 * time.Second).String(),
		},
		ActiveByType: []adminQueueTypeInfo{},
		GeneratedAt:  time.Now().UTC().Format(time.RFC3339),
	}
	if s.QueueDB == nil {
		writeJSON(w, http.StatusOK, resp)
		return
	}
	now := time.Now().Unix()
	var err error
	if resp.Total, err = adminQueueCount(r.Context(), s.QueueDB, "SELECT COUNT(*) FROM jobs WHERE queue = ?", queueName); err != nil {
		writeError(w, http.StatusInternalServerError, "queue_stats_failed", err.Error(), nil)
		return
	}
	if resp.Available, err = adminQueueCount(r.Context(), s.QueueDB, "SELECT COUNT(*) FROM jobs WHERE queue = ? AND reserved_at IS NULL AND available_at <= ?", queueName, now); err != nil {
		writeError(w, http.StatusInternalServerError, "queue_stats_failed", err.Error(), nil)
		return
	}
	if resp.Delayed, err = adminQueueCount(r.Context(), s.QueueDB, "SELECT COUNT(*) FROM jobs WHERE queue = ? AND reserved_at IS NULL AND available_at > ?", queueName, now); err != nil {
		writeError(w, http.StatusInternalServerError, "queue_stats_failed", err.Error(), nil)
		return
	}
	if resp.Reserved, err = adminQueueCount(r.Context(), s.QueueDB, "SELECT COUNT(*) FROM jobs WHERE queue = ? AND reserved_at IS NOT NULL", queueName); err != nil {
		writeError(w, http.StatusInternalServerError, "queue_stats_failed", err.Error(), nil)
		return
	}
	if resp.Failed, err = adminQueueCount(r.Context(), s.QueueDB, "SELECT COUNT(*) FROM failed_jobs WHERE queue = ?", queueName); err != nil {
		writeError(w, http.StatusInternalServerError, "queue_stats_failed", err.Error(), nil)
		return
	}
	resp.OldestCreated, err = adminQueueTime(r.Context(), s.QueueDB, "SELECT MIN(created_at) FROM jobs WHERE queue = ?", queueName)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "queue_stats_failed", err.Error(), nil)
		return
	}
	resp.NextAvailable, err = adminQueueTime(r.Context(), s.QueueDB, "SELECT MIN(available_at) FROM jobs WHERE queue = ? AND reserved_at IS NULL", queueName)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "queue_stats_failed", err.Error(), nil)
		return
	}
	resp.ActiveByType, err = adminQueueActiveByType(r.Context(), s.QueueDB, queueName)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "queue_stats_failed", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleAdminQueueFailures(w http.ResponseWriter, r *http.Request) {
	if s.QueueDB == nil {
		writeJSON(w, http.StatusOK, adminQueueFailuresResponse{Items: []adminQueueFailureInfo{}, Count: 0})
		return
	}
	limit := 25
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			writeError(w, http.StatusBadRequest, "validation_error", "limit must be a positive integer", nil)
			return
		}
		if n > 100 {
			n = 100
		}
		limit = n
	}
	rows, err := s.QueueDB.QueryContext(r.Context(), `
SELECT id, queue, type, COALESCE(error, ''), attempts, failed_at, COALESCE(payload, '')
FROM failed_jobs
WHERE queue = ?
ORDER BY failed_at DESC, id DESC
LIMIT ?`, "tesseract", limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "queue_failures_failed", err.Error(), nil)
		return
	}
	defer rows.Close()
	items := make([]adminQueueFailureInfo, 0, limit)
	for rows.Next() {
		var item adminQueueFailureInfo
		var failedAt int64
		if err := rows.Scan(&item.ID, &item.Queue, &item.Type, &item.Error, &item.Attempts, &failedAt, &item.Payload); err != nil {
			writeError(w, http.StatusInternalServerError, "queue_failures_failed", err.Error(), nil)
			return
		}
		item.FailedAt = time.Unix(failedAt, 0).UTC().Format(time.RFC3339)
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "queue_failures_failed", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, adminQueueFailuresResponse{Items: items, Count: len(items)})
}

func (s *Server) handleAdminQueueRetryFailed(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, "repair") {
		return
	}
	if s.QueueDB == nil {
		writeError(w, http.StatusServiceUnavailable, "queue_unavailable", "queue database is not configured", nil)
		return
	}
	var req adminQueueRetryFailedRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", err.Error(), nil)
		return
	}
	retried, err := s.retryFailedQueueJobs(r.Context(), req.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "queue_retry_failed", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, adminQueueRetryFailedResponse{Retried: retried})
}

func (s *Server) handleAdminQueueBackfill(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, "repair") {
		return
	}
	if s.QueueDB == nil {
		writeError(w, http.StatusServiceUnavailable, "queue_unavailable", "queue database is not configured", nil)
		return
	}
	var req adminQueueBackfillRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", err.Error(), nil)
		return
	}
	if req.Limit < 0 {
		writeError(w, http.StatusBadRequest, "validation_error", "limit must be zero or a positive integer", nil)
		return
	}
	queued, err := s.enqueueEmbeddingBackfill(r.Context(), strings.TrimSpace(req.Namespace), req.Limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "queue_backfill_failed", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, adminQueueBackfillResponse{
		Queued:    queued,
		Namespace: strings.TrimSpace(req.Namespace),
		Limit:     req.Limit,
	})
}

func (s *Server) handleAdminStorage(w http.ResponseWriter, r *http.Request) {
	report, err := s.Store.Readiness(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "storage_stats_failed", err.Error(), nil)
		return
	}

	paths := []adminStoragePathInfo{
		adminStoragePath("main-db", report.DBPath),
		adminStoragePath("records", report.RecordsDir),
	}
	if s.QueueDBPath != "" {
		paths = append(paths, adminStoragePath("queue-db", s.QueueDBPath))
	}
	if s.ConfigFile != "" {
		paths = append(paths, adminStoragePath("config-file", s.ConfigFile))
	}

	var total int64
	for _, pathInfo := range paths {
		total += pathInfo.Bytes
	}
	recordInfo, err := s.adminStorageRecordInfo(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "storage_stats_failed", err.Error(), nil)
		return
	}
	policyInfo, err := s.adminNamespacePolicyInfo(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "storage_stats_failed", err.Error(), nil)
		return
	}
	topNamespaces, err := s.adminTopNamespaces(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "storage_stats_failed", err.Error(), nil)
		return
	}

	writeJSON(w, http.StatusOK, adminStorageResponse{
		GeneratedAt:     time.Now().UTC().Format(time.RFC3339),
		TotalBytes:      total,
		Paths:           paths,
		Records:         recordInfo,
		NamespacePolicy: policyInfo,
		TopNamespaces:   topNamespaces,
	})
}

func adminStoragePath(label, p string) adminStoragePathInfo {
	info := adminStoragePathInfo{Label: label, Path: p}
	if p == "" {
		info.Kind = "missing"
		return info
	}
	stat, err := os.Stat(p)
	if err != nil {
		info.Kind = "missing"
		if !errors.Is(err, os.ErrNotExist) {
			info.Error = err.Error()
		}
		return info
	}
	info.Exists = true
	if stat.IsDir() {
		info.Kind = "dir"
		size, walkErr := adminDirSize(p)
		info.Bytes = size
		if walkErr != nil {
			info.Error = walkErr.Error()
		}
		return info
	}
	info.Kind = "file"
	info.Bytes = stat.Size()
	return info
}

func adminDirSize(root string) (int64, error) {
	var total int64
	err := filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		total += info.Size()
		return nil
	})
	return total, err
}

func (s *Server) adminStorageRecordInfo(ctx context.Context) (adminStorageRecordInfo, error) {
	var info adminStorageRecordInfo
	if err := s.Store.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM records`).Scan(&info.Revisions); err != nil {
		return info, err
	}
	if err := s.Store.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM heads`).Scan(&info.Heads); err != nil {
		return info, err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if err := s.Store.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM records WHERE ttl != '' AND ttl < ?`, now).Scan(&info.Expired); err != nil {
		return info, err
	}
	if err := s.Store.DB().QueryRowContext(ctx, `SELECT COALESCE(MIN(created_at), ''), COALESCE(MAX(created_at), '') FROM records`).Scan(&info.Oldest, &info.Newest); err != nil {
		return info, err
	}
	return info, nil
}

func (s *Server) adminNamespacePolicyInfo(ctx context.Context) (adminNamespacePolicyInfo, error) {
	entries, err := s.Store.ListNamespacePolicies(ctx)
	if err != nil {
		return adminNamespacePolicyInfo{}, err
	}
	info := adminNamespacePolicyInfo{Namespaces: len(entries)}
	for _, entry := range entries {
		hasLimit := false
		if _, ok := entry.Policy["retention"]; ok {
			info.WithRetention++
			hasLimit = true
		}
		if _, ok := entry.Policy["max_revisions"]; ok {
			info.WithMaxRevisions++
			hasLimit = true
		}
		if _, ok := entry.Policy["max_bytes_per_key"]; ok {
			info.WithMaxBytesPerKey++
			hasLimit = true
		}
		if !hasLimit {
			info.WithoutPolicyLimits++
		}
	}
	return info, nil
}

func (s *Server) adminTopNamespaces(ctx context.Context) ([]adminNamespaceRecordInfo, error) {
	rows, err := s.Store.DB().QueryContext(ctx, `
		SELECT namespace, COUNT(*) AS revisions, COUNT(DISTINCT key_name) AS keys,
		       COALESCE(MIN(created_at), ''), COALESCE(MAX(created_at), '')
		  FROM records
		 GROUP BY namespace
		 ORDER BY revisions DESC, namespace ASC
		 LIMIT 10
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []adminNamespaceRecordInfo{}
	for rows.Next() {
		var item adminNamespaceRecordInfo
		if err := rows.Scan(&item.Namespace, &item.Revisions, &item.Keys, &item.Oldest, &item.Newest); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

func adminQueueCount(ctx context.Context, db *sql.DB, query string, args ...any) (int64, error) {
	var count int64
	if err := db.QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func adminQueueTime(ctx context.Context, db *sql.DB, query string, args ...any) (string, error) {
	var value sql.NullInt64
	if err := db.QueryRowContext(ctx, query, args...).Scan(&value); err != nil {
		return "", err
	}
	if !value.Valid || value.Int64 <= 0 {
		return "", nil
	}
	return time.Unix(value.Int64, 0).UTC().Format(time.RFC3339), nil
}

func adminQueueActiveByType(ctx context.Context, db *sql.DB, queueName string) ([]adminQueueTypeInfo, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT type, COUNT(*)
		  FROM jobs
		 WHERE queue = ?
		 GROUP BY type
		 ORDER BY COUNT(*) DESC, type ASC
		 LIMIT 10
	`, queueName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	types := []adminQueueTypeInfo{}
	for rows.Next() {
		var item adminQueueTypeInfo
		if err := rows.Scan(&item.Type, &item.Count); err != nil {
			return nil, err
		}
		types = append(types, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return types, nil
}

func adminPath(label, p string) adminPathInfo {
	info := adminPathInfo{Label: label, Path: p, Kind: "missing"}
	if p == "" {
		return info
	}
	stat, err := os.Stat(p)
	if err == nil {
		info.Exists = true
		if stat.IsDir() {
			info.Kind = "dir"
			info.Writable = stat.Mode().Perm()&0o200 != 0
		} else {
			info.Kind = "file"
			info.Writable = stat.Mode().Perm()&0o200 != 0
		}
		return info
	}
	parent := filepath.Dir(p)
	if parentStat, parentErr := os.Stat(parent); parentErr == nil && parentStat.IsDir() {
		info.Writable = parentStat.Mode().Perm()&0o200 != 0
	}
	return info
}

type viewRequest struct {
	Selector       contextstore.Selector `json:"selector"`
	IncludePayload bool                  `json:"include_payload"`
	Limit          int                   `json:"limit"`
}

func (s *Server) handleView(w http.ResponseWriter, r *http.Request) {
	var req viewRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", err.Error(), nil)
		return
	}
	result, err := s.Store.Evaluate(r.Context(), req.Selector, req.IncludePayload, req.Limit)
	if err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items": result.Items,
		"evaluation_meta": map[string]any{
			"sort_keys":        result.SortKeys,
			"matched_count":    result.MatchedCount,
			"truncated":        result.Truncated,
			"normalized_scope": result.NormalizedScope,
		},
	})
}

func (s *Server) handleConsistencyScan(w http.ResponseWriter, r *http.Request) {
	issues, err := s.Store.ScanConsistency(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "scan_failed", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"issues": issues,
		"count":  len(issues),
	})
}

func (s *Server) handleConsistencyRepair(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, "repair") {
		return
	}
	rebuilt, err := s.Store.RebuildHeads(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "repair_failed", err.Error(), nil)
		return
	}
	issues, err := s.Store.ScanConsistency(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "scan_failed", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"rebuilt_heads":    rebuilt,
		"remaining_issues": len(issues),
		"issues":           issues,
	})
}

func decodeJSON(r *http.Request, dst any) error {
	defer r.Body.Close()
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	return nil
}

func writeJSON(w http.ResponseWriter, code int, value any) {
	body, err := json.Marshal(value)
	if err != nil {
		marshalErr := err
		code = http.StatusInternalServerError
		body, err = json.Marshal(map[string]any{
			"code":    "serialization_failed",
			"message": "failed to serialize response",
			"details": map[string]any{"error": marshalErr.Error()},
		})
		if err != nil {
			// The fallback contains only string keys and values and is always
			// supported by encoding/json. Retain a valid JSON response even if
			// that guarantee changes in the future.
			body = []byte(`{"code":"serialization_failed","message":"failed to serialize response","details":null}`)
		}
	}
	body = append(body, '\n')
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	// A ResponseWriter can fail only after the status has necessarily been
	// committed. There is no second HTTP response available at that point;
	// the server/transport owns reporting such connection write failures.
	_, _ = w.Write(body)
}

func writeError(w http.ResponseWriter, status int, code, message string, details map[string]any) {
	writeJSON(w, status, map[string]any{
		"code":    code,
		"message": message,
		"details": details,
	})
}

func quoteJSON(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func (s *Server) reloadPolicies(ctx context.Context) error {
	entries, err := s.Store.ListNamespacePolicies(ctx)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := s.Policy.RegisterNamespace(entry.Namespace, entry.OwnerType, entry.OwnerID, entry.Policy); err != nil {
			return err
		}
	}
	return nil
}

// Write is used by tests needing isolated request context timeouts.
func (s *Server) Write(ctx context.Context, in writeRequest) (contextstore.Record, error) {
	if err := s.Policy.CanWrite(in.ClientID, in.Actor, in.Namespace); err != nil {
		return contextstore.Record{}, err
	}
	return s.Store.AppendRecord(ctx, contextstore.AppendInput{
		Namespace: in.Namespace,
		Key:       in.Key,
		Actor:     in.Actor,
		Payload:   in.Payload,
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(b)
}

type apiMetrics struct {
	mu     sync.Mutex
	routes map[string]*routeMetric
}

type routeMetric struct {
	Method           string           `json:"method"`
	Path             string           `json:"path"`
	Requests         int64            `json:"requests"`
	Errors           int64            `json:"errors"`
	LatencyNsTotal   int64            `json:"latency_ns_total"`
	LatencyNsAvg     int64            `json:"latency_ns_avg"`
	StatusCounts     map[string]int64 `json:"status_counts"`
	RecentRequestIDs []string         `json:"recent_request_ids"`
}

func newAPIMetrics() *apiMetrics {
	return &apiMetrics{
		routes: make(map[string]*routeMetric),
	}
}

func (m *apiMetrics) Record(method, path string, status int, dur time.Duration, requestID string) {
	key := method + " " + path
	m.mu.Lock()
	defer m.mu.Unlock()
	entry, ok := m.routes[key]
	if !ok {
		entry = &routeMetric{
			Method:       method,
			Path:         path,
			StatusCounts: make(map[string]int64),
		}
		m.routes[key] = entry
	}
	entry.Requests++
	if status >= 400 {
		entry.Errors++
	}
	entry.StatusCounts[strconv.Itoa(status)]++
	if strings.TrimSpace(requestID) != "" {
		entry.RecentRequestIDs = append(entry.RecentRequestIDs, requestID)
		if len(entry.RecentRequestIDs) > 5 {
			entry.RecentRequestIDs = entry.RecentRequestIDs[len(entry.RecentRequestIDs)-5:]
		}
	}
	entry.LatencyNsTotal += dur.Nanoseconds()
	entry.LatencyNsAvg = entry.LatencyNsTotal / entry.Requests
}

func (m *apiMetrics) Snapshot() map[string]any {
	m.mu.Lock()
	defer m.mu.Unlock()

	routes := make([]routeMetric, 0, len(m.routes))
	var totalReq int64
	var totalErr int64
	for _, entry := range m.routes {
		routes = append(routes, *entry)
		totalReq += entry.Requests
		totalErr += entry.Errors
	}
	sortRouteMetrics(routes)
	return map[string]any{
		"enabled": true,
		"routes":  routes,
		"totals": map[string]any{
			"requests": totalReq,
			"errors":   totalErr,
		},
	}
}

func sortRouteMetrics(items []routeMetric) {
	for i := 0; i < len(items)-1; i++ {
		for j := i + 1; j < len(items); j++ {
			if items[j].Method < items[i].Method || (items[j].Method == items[i].Method && items[j].Path < items[i].Path) {
				items[i], items[j] = items[j], items[i]
			}
		}
	}
}

type maintenanceTrimRequest struct {
	NamespacePattern string `json:"namespace_pattern"`
	Retention        string `json:"retention"`
	DryRun           bool   `json:"dry_run"`
}

func (s *Server) handleMaintenanceTrim(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, "repair") {
		return
	}
	var req maintenanceTrimRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", err.Error(), nil)
		return
	}
	if strings.TrimSpace(req.NamespacePattern) == "" {
		req.NamespacePattern = "user/cache/%"
	}
	retention := req.Retention
	if strings.TrimSpace(retention) == "" {
		retention = "72h"
	}
	dur, err := time.ParseDuration(retention)
	if err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", "retention must be a valid duration (e.g. 72h)", nil)
		return
	}
	cutoff := time.Now().UTC().Add(-dur).Format(time.RFC3339)
	start := time.Now()
	trimmed, err := s.Store.TrimRecords(r.Context(), req.NamespacePattern, cutoff, req.DryRun)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "maintenance_failed", err.Error(), nil)
		return
	}
	_ = s.Store.EmitMaintenance(r.Context(), contextstore.EventMaintenanceTrim, "system", req.NamespacePattern,
		json.RawMessage(fmt.Sprintf(`{"records_affected":%d,"dry_run":%t}`, trimmed, req.DryRun)))
	writeJSON(w, http.StatusOK, map[string]any{
		"trimmed":           trimmed,
		"namespace_pattern": req.NamespacePattern,
		"duration_ms":       time.Since(start).Milliseconds(),
		"dry_run":           req.DryRun,
	})
}

type maintenanceCompactRequest struct {
	NamespacePattern string `json:"namespace_pattern"`
	MaxRevisions     int    `json:"max_revisions"`
	DryRun           bool   `json:"dry_run"`
}

func (s *Server) handleMaintenanceCompact(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, "repair") {
		return
	}
	var req maintenanceCompactRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", err.Error(), nil)
		return
	}
	if strings.TrimSpace(req.NamespacePattern) == "" {
		writeError(w, http.StatusBadRequest, "validation_error", "namespace_pattern is required", nil)
		return
	}
	if req.MaxRevisions < 1 {
		req.MaxRevisions = 1
	}
	start := time.Now()
	compacted, err := s.Store.CompactNamespace(r.Context(), req.NamespacePattern, req.MaxRevisions, req.DryRun)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "maintenance_failed", err.Error(), nil)
		return
	}
	_ = s.Store.EmitMaintenance(r.Context(), contextstore.EventMaintenanceCompact, "system", req.NamespacePattern,
		json.RawMessage(fmt.Sprintf(`{"records_affected":%d,"dry_run":%t}`, compacted, req.DryRun)))
	writeJSON(w, http.StatusOK, map[string]any{
		"compacted":         compacted,
		"namespace_pattern": req.NamespacePattern,
		"duration_ms":       time.Since(start).Milliseconds(),
		"dry_run":           req.DryRun,
	})
}
