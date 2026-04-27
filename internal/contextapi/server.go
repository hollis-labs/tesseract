package contextapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hollis-labs/vanta-conduit/internal/config"
	"github.com/hollis-labs/vanta-conduit/internal/contextpolicy"
	"github.com/hollis-labs/vanta-conduit/internal/contextstore"
	"github.com/hollis-labs/vanta-conduit/internal/contexttypes"
	"github.com/hollis-labs/vanta-conduit/internal/knowledge"
	"github.com/hollis-labs/vanta-conduit/internal/memory"
	feotel "github.com/hollis-labs/go-otel"
	"github.com/hollis-labs/go-modelsdev/modelsdev"
	"github.com/hollis-labs/go-providers/provider"
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
	// TypeRegistry manages context types and views. May be nil (defaults will be used).
	TypeRegistry *contexttypes.Registry
	// MemoryStore backs the /v1/memory/* and /v1/knowledge/* routes. When
	// nil, those routes respond with 503 service_unavailable.
	MemoryStore *memory.Store
	// KnowledgeStore backs /v1/knowledge/* routes. Wired by cmd/contextd to
	// knowledge.New(MemoryStore).
	KnowledgeStore *knowledge.Store
	// SynthesisProvider is the go-providers Provider used by /v1/synthesis/ask.
	// When nil, the synthesis route returns 503 service_unavailable. Wired by
	// cmd/contextd from config.Synthesis settings.
	SynthesisProvider provider.Provider
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
	case r.Method == http.MethodPost && r.URL.Path == "/v1/conduit/lookup":
		s.handleConduitLookup(w, r)
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
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(value)
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
