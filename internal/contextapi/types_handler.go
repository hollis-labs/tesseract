package contextapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/hollis-labs/cortex/internal/contextstore"
	"github.com/hollis-labs/cortex/internal/contexttypes"
)

// TypedWriteRequest is the body for typed context writes.
type TypedWriteRequest struct {
	ClientID       string          `json:"client_id"`
	Actor          string          `json:"actor"`
	Namespace      string          `json:"namespace"`
	Key            string          `json:"key"`
	Payload        json.RawMessage `json:"payload"`
	Metadata       json.RawMessage `json:"metadata,omitempty"`
	RecordType     string          `json:"record_type"`
	Status         string          `json:"status"`
	TTL            string          `json:"ttl,omitempty"`
	ContentVersion int64           `json:"content_version,omitempty"`
	Pointers       []string        `json:"pointers,omitempty"`
	Provenance     json.RawMessage `json:"provenance,omitempty"`
	Reason         string          `json:"reason,omitempty"`
}

func (s *Server) handleTypedWrite(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, "write") {
		return
	}
	var req TypedWriteRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error(), nil)
		return
	}
	if !requireNamespaceAccess(w, r, req.Namespace) {
		return
	}
	if err := s.Policy.CanWrite(req.ClientID, req.Actor, req.Namespace); err != nil {
		writeError(w, http.StatusForbidden, "policy_denied", err.Error(), nil)
		return
	}

	reg := s.TypeRegistry
	if reg == nil {
		reg = contexttypes.NewRegistry()
	}

	// Validate type.
	if err := reg.ValidateType(req.RecordType); err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", err.Error(), nil)
		return
	}

	// Default status to draft.
	status := req.Status
	if status == "" {
		status = "draft"
	}
	if err := reg.ValidateStatus(req.RecordType, status); err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", err.Error(), nil)
		return
	}

	// Apply default TTL from type registry if not specified.
	ttl := req.TTL
	if ttl == "" && req.RecordType != "" {
		ct, ok := reg.GetType(req.RecordType)
		if ok {
			defaultTTL := ct.ParseDefaultTTL()
			if defaultTTL > 0 {
				ttl = time.Now().UTC().Add(defaultTTL).Format(time.RFC3339)
			}
		}
	}

	rec, err := s.Store.AppendRecord(r.Context(), contextstore.AppendInput{
		Namespace:      req.Namespace,
		Key:            req.Key,
		Actor:          req.Actor,
		Payload:        req.Payload,
		Metadata:       req.Metadata,
		RecordType:     req.RecordType,
		Status:         status,
		TTL:            ttl,
		ContentVersion: req.ContentVersion,
		Pointers:       req.Pointers,
		Provenance:     req.Provenance,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, "write_failed", err.Error(), nil)
		return
	}
	_ = s.Store.RecordAuditEvent(r.Context(), contextstore.AuditEvent{
		EventType: "typed_write",
		Actor:     req.Actor,
		Namespace: req.Namespace,
		Key:       req.Key,
		Revision:  rec.Revision,
		RecordID:  rec.RecordID,
		Metadata: json.RawMessage(fmt.Sprintf(
			`{"source":"http","record_type":%s,"status":%s,"reason":%s}`,
			quoteJSON(req.RecordType), quoteJSON(status), quoteJSON(req.Reason))),
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"record_id":       rec.RecordID,
		"revision":        rec.Revision,
		"head_revision":   rec.Revision,
		"timestamp":       rec.CreatedAt,
		"record_type":     rec.RecordType,
		"status":          rec.Status,
		"ttl":             rec.TTL,
		"content_version": rec.ContentVersion,
	})
}

// StatusPromoteRequest is the body for promoting status.
type StatusPromoteRequest struct {
	Actor     string `json:"actor"`
	Namespace string `json:"namespace"`
	Key       string `json:"key"`
	ToStatus  string `json:"to_status,omitempty"` // if empty, use next in chain
}

func (s *Server) handleStatusPromote(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, "write") {
		return
	}
	var req StatusPromoteRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error(), nil)
		return
	}
	if req.Namespace == "" || req.Key == "" {
		writeError(w, http.StatusBadRequest, "validation_error", "namespace and key are required", nil)
		return
	}
	actor := req.Actor
	if actor == "" {
		actor = "user"
	}

	head, err := s.Store.Head(r.Context(), req.Namespace, req.Key)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", err.Error(), nil)
		return
	}

	reg := s.TypeRegistry
	if reg == nil {
		reg = contexttypes.NewRegistry()
	}

	oldStatus := head.Status
	if oldStatus == "" {
		oldStatus = "draft"
	}

	newStatus := req.ToStatus
	if newStatus == "" {
		newStatus = contexttypes.NextPromotionStatus(oldStatus)
		if newStatus == "" {
			writeError(w, http.StatusBadRequest, "validation_error",
				fmt.Sprintf("cannot promote from status %q", oldStatus), nil)
			return
		}
	}

	if err := reg.ValidateTransition(head.RecordType, oldStatus, newStatus, actor); err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", err.Error(), nil)
		return
	}

	rec, err := s.Store.UpdateRecordStatus(r.Context(), req.Namespace, req.Key, actor, newStatus)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "promote_failed", err.Error(), nil)
		return
	}

	_ = s.Store.RecordAuditEvent(r.Context(), contextstore.AuditEvent{
		EventType: "status_promote",
		Actor:     actor,
		Namespace: req.Namespace,
		Key:       req.Key,
		Revision:  rec.Revision,
		RecordID:  rec.RecordID,
		Metadata: json.RawMessage(fmt.Sprintf(
			`{"from_status":%s,"to_status":%s,"record_type":%s}`,
			quoteJSON(oldStatus), quoteJSON(newStatus), quoteJSON(head.RecordType))),
	})

	writeJSON(w, http.StatusOK, map[string]any{
		"record_id":   rec.RecordID,
		"revision":    rec.Revision,
		"from_status": oldStatus,
		"to_status":   newStatus,
		"record_type": head.RecordType,
	})
}

// StatusDeprecateRequest is the body for deprecating an item.
type StatusDeprecateRequest struct {
	Actor     string `json:"actor"`
	Namespace string `json:"namespace"`
	Key       string `json:"key"`
}

func (s *Server) handleStatusDeprecate(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, "write") {
		return
	}
	var req StatusDeprecateRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error(), nil)
		return
	}
	if req.Namespace == "" || req.Key == "" {
		writeError(w, http.StatusBadRequest, "validation_error", "namespace and key are required", nil)
		return
	}
	actor := req.Actor
	if actor == "" {
		actor = "user"
	}

	head, err := s.Store.Head(r.Context(), req.Namespace, req.Key)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", err.Error(), nil)
		return
	}

	oldStatus := head.Status
	if oldStatus == "" {
		oldStatus = "draft"
	}
	if oldStatus == "deprecated" {
		writeError(w, http.StatusBadRequest, "validation_error", "item is already deprecated", nil)
		return
	}

	rec, err := s.Store.UpdateRecordStatus(r.Context(), req.Namespace, req.Key, actor, "deprecated")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "deprecate_failed", err.Error(), nil)
		return
	}

	_ = s.Store.RecordAuditEvent(r.Context(), contextstore.AuditEvent{
		EventType: "status_deprecate",
		Actor:     actor,
		Namespace: req.Namespace,
		Key:       req.Key,
		Revision:  rec.Revision,
		RecordID:  rec.RecordID,
		Metadata: json.RawMessage(fmt.Sprintf(
			`{"from_status":%s,"record_type":%s}`,
			quoteJSON(oldStatus), quoteJSON(head.RecordType))),
	})

	writeJSON(w, http.StatusOK, map[string]any{
		"record_id":   rec.RecordID,
		"revision":    rec.Revision,
		"from_status": oldStatus,
		"to_status":   "deprecated",
		"record_type": head.RecordType,
	})
}

// handleTypedView evaluates a named view from the type registry.
func (s *Server) handleTypedView(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ViewID         string   `json:"view_id"`
		Namespaces     []string `json:"namespaces,omitempty"`
		MaxItems       int      `json:"max_items,omitempty"`
		IncludePayload bool     `json:"include_payload"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error(), nil)
		return
	}
	if strings.TrimSpace(req.ViewID) == "" {
		writeError(w, http.StatusBadRequest, "validation_error", "view_id is required", nil)
		return
	}

	reg := s.TypeRegistry
	if reg == nil {
		reg = contexttypes.NewRegistry()
	}

	viewDef, ok := reg.GetView(req.ViewID)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found",
			fmt.Sprintf("view %q not found", req.ViewID), nil)
		return
	}

	maxItems := viewDef.MaxItems
	if req.MaxItems > 0 {
		maxItems = req.MaxItems
	}
	if maxItems <= 0 {
		maxItems = contextstore.DefaultSelectLimit
	}

	namespaces := req.Namespaces
	if len(namespaces) == 0 {
		namespaces = []string{"*"}
	}

	// Select records matching the view's types, excluding deprecated by default.
	statuses := []string{"draft", "reviewed", "canonical"}
	items, err := s.Store.Select(r.Context(), contextstore.Selector{
		Namespaces:    namespaces,
		RevisionScope: "head",
		Types:         viewDef.Types,
		Statuses:      statuses,
		Limit:         maxItems,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, "selector_error", err.Error(), nil)
		return
	}

	// Apply rank weights: sort by type rank bias * status weight.
	type rankedRecord struct {
		rec   contextstore.Record
		score float64
	}
	ranked := make([]rankedRecord, len(items))
	for i, rec := range items {
		typeScore := 1.0
		if ct, ok := reg.GetType(rec.RecordType); ok {
			if ct.RetrievalRankBias > 0 {
				typeScore = ct.RetrievalRankBias
			}
		}
		statusScore := 0.5
		if w, ok := viewDef.RankWeights[rec.Status]; ok {
			statusScore = w
		}
		ranked[i] = rankedRecord{rec: rec, score: typeScore * statusScore}
	}

	// Sort by score descending (stable sort for determinism).
	for i := 0; i < len(ranked)-1; i++ {
		for j := i + 1; j < len(ranked); j++ {
			if ranked[j].score > ranked[i].score ||
				(ranked[j].score == ranked[i].score && ranked[j].rec.RecordID < ranked[i].rec.RecordID) {
				ranked[i], ranked[j] = ranked[j], ranked[i]
			}
		}
	}

	// Build response.
	resultItems := make([]map[string]any, len(ranked))
	totalBytes := 0
	for i, rr := range ranked {
		item := map[string]any{
			"record_id":       rr.rec.RecordID,
			"namespace":       rr.rec.Namespace,
			"key":             rr.rec.Key,
			"revision":        rr.rec.Revision,
			"actor":           rr.rec.Actor,
			"created_at":      rr.rec.CreatedAt,
			"record_type":     rr.rec.RecordType,
			"status":          rr.rec.Status,
			"content_version": rr.rec.ContentVersion,
			"rank_score":      rr.score,
		}
		if len(rr.rec.Pointers) > 0 {
			item["pointers"] = rr.rec.Pointers
		}
		if req.IncludePayload {
			item["payload"] = json.RawMessage(rr.rec.Payload)
		}
		totalBytes += len(rr.rec.Payload)
		resultItems[i] = item
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"view":           req.ViewID,
		"items":          resultItems,
		"count":          len(resultItems),
		"types":          viewDef.Types,
		"token_estimate": contextstore.EstimateTokens(make([]byte, totalBytes)),
	})
}

// handleTypesListRequest returns all registered types.
func (s *Server) handleTypesList(w http.ResponseWriter, r *http.Request) {
	_ = r
	reg := s.TypeRegistry
	if reg == nil {
		reg = contexttypes.NewRegistry()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"types": reg.ListTypes(),
	})
}

// handleViewsList returns all registered views.
func (s *Server) handleViewsList(w http.ResponseWriter, r *http.Request) {
	_ = r
	reg := s.TypeRegistry
	if reg == nil {
		reg = contexttypes.NewRegistry()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"views": reg.ListViews(),
	})
}

// BulkIngestRequest is the body for the bulk ingestion endpoint.
type BulkIngestRequest struct {
	ClientID    string           `json:"client_id"`
	Actor       string           `json:"actor"`
	Items       []BulkIngestItem `json:"items"`
	StopOnError bool             `json:"stop_on_error"`
}

// BulkIngestItem represents a single item in a bulk ingestion batch.
type BulkIngestItem struct {
	Namespace  string          `json:"namespace"`
	Key        string          `json:"key"`
	Payload    json.RawMessage `json:"payload"`
	RecordType string          `json:"record_type"`
	Status     string          `json:"status"`
	TTL        string          `json:"ttl,omitempty"`
	Pointers   []string        `json:"pointers,omitempty"`
	Actor      string          `json:"actor,omitempty"`
}

func (s *Server) handleBulkIngest(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, "write") {
		return
	}
	var req BulkIngestRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error(), nil)
		return
	}
	if len(req.Items) == 0 {
		writeError(w, http.StatusBadRequest, "validation_error", "items array is empty", nil)
		return
	}
	if len(req.Items) > 100 {
		writeError(w, http.StatusBadRequest, "validation_error", "max 100 items per batch", nil)
		return
	}

	reg := s.TypeRegistry
	if reg == nil {
		reg = contexttypes.NewRegistry()
	}

	type itemResult struct {
		Index    int    `json:"index"`
		RecordID string `json:"record_id,omitempty"`
		Status   string `json:"status"`
		Error    string `json:"error,omitempty"`
	}

	results := make([]itemResult, 0, len(req.Items))
	written := 0
	errCount := 0

	for i, item := range req.Items {
		res := itemResult{Index: i}

		if item.Namespace == "" || item.Key == "" {
			res.Status = "error"
			res.Error = "namespace and key are required"
			results = append(results, res)
			errCount++
			if req.StopOnError {
				break
			}
			continue
		}

		if !requireNamespaceAccess(w, r, item.Namespace) {
			// Response already written by requireNamespaceAccess.
			return
		}

		actor := item.Actor
		if actor == "" {
			actor = req.Actor
		}
		if actor == "" {
			actor = "api"
		}

		if err := s.Policy.CanWrite(req.ClientID, actor, item.Namespace); err != nil {
			res.Status = "error"
			res.Error = "policy_denied: " + err.Error()
			results = append(results, res)
			errCount++
			if req.StopOnError {
				break
			}
			continue
		}

		if err := reg.ValidateType(item.RecordType); err != nil {
			res.Status = "error"
			res.Error = err.Error()
			results = append(results, res)
			errCount++
			if req.StopOnError {
				break
			}
			continue
		}

		status := item.Status
		if status == "" {
			status = "draft"
		}
		if err := reg.ValidateStatus(item.RecordType, status); err != nil {
			res.Status = "error"
			res.Error = err.Error()
			results = append(results, res)
			errCount++
			if req.StopOnError {
				break
			}
			continue
		}

		// Required fields validation.
		if item.RecordType != "" {
			var payloadMap map[string]any
			if err := json.Unmarshal(item.Payload, &payloadMap); err == nil {
				if err := reg.ValidateRequiredFields(item.RecordType, payloadMap); err != nil {
					res.Status = "error"
					res.Error = err.Error()
					results = append(results, res)
					errCount++
					if req.StopOnError {
						break
					}
					continue
				}
			}
		}

		ttl := item.TTL
		if ttl == "" && item.RecordType != "" {
			ct, ok := reg.GetType(item.RecordType)
			if ok {
				defaultTTL := ct.ParseDefaultTTL()
				if defaultTTL > 0 {
					ttl = time.Now().UTC().Add(defaultTTL).Format(time.RFC3339)
				}
			}
		}

		rec, err := s.Store.AppendRecord(r.Context(), contextstore.AppendInput{
			Namespace:  item.Namespace,
			Key:        item.Key,
			Actor:      actor,
			Payload:    item.Payload,
			RecordType: item.RecordType,
			Status:     status,
			TTL:        ttl,
			Pointers:   item.Pointers,
		})
		if err != nil {
			res.Status = "error"
			res.Error = "write_failed: " + err.Error()
			results = append(results, res)
			errCount++
			if req.StopOnError {
				break
			}
			continue
		}

		res.RecordID = rec.RecordID
		res.Status = "written"
		written++

		_ = s.Store.RecordAuditEvent(r.Context(), contextstore.AuditEvent{
			EventType: "bulk_ingest",
			Actor:     actor,
			Namespace: item.Namespace,
			Key:       item.Key,
			Revision:  rec.Revision,
			RecordID:  rec.RecordID,
			Metadata: json.RawMessage(fmt.Sprintf(
				`{"source":"http","record_type":%s,"status":%s,"batch_index":%d}`,
				quoteJSON(item.RecordType), quoteJSON(status), i)),
		})

		results = append(results, res)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"total":   len(req.Items),
		"written": written,
		"errors":  errCount,
		"results": results,
	})
}

// handleTTLCleanup triggers cleanup of expired TTL records.
func (s *Server) handleTTLCleanup(w http.ResponseWriter, r *http.Request) {
	cleaned, err := s.Store.CleanupExpiredTTL(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "cleanup_failed", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"cleaned": cleaned,
	})
}

// handleContextPack generates a bounded context pack for a view.
func (s *Server) handleContextPack(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ViewID     string   `json:"view_id"`
		Namespaces []string `json:"namespaces,omitempty"`
		MaxItems   int      `json:"max_items,omitempty"`
		MaxTokens  int      `json:"max_tokens,omitempty"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error(), nil)
		return
	}
	if strings.TrimSpace(req.ViewID) == "" {
		writeError(w, http.StatusBadRequest, "validation_error", "view_id is required", nil)
		return
	}

	reg := s.TypeRegistry
	if reg == nil {
		reg = contexttypes.NewRegistry()
	}

	viewDef, ok := reg.GetView(req.ViewID)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found",
			fmt.Sprintf("view %q not found", req.ViewID), nil)
		return
	}

	maxItems := viewDef.MaxItems
	if req.MaxItems > 0 {
		maxItems = req.MaxItems
	}
	if maxItems <= 0 {
		maxItems = 50
	}

	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 8000
	}

	namespaces := req.Namespaces
	if len(namespaces) == 0 {
		namespaces = []string{"*"}
	}

	statuses := []string{"draft", "reviewed", "canonical"}
	items, err := s.Store.Select(r.Context(), contextstore.Selector{
		Namespaces:    namespaces,
		RevisionScope: "head",
		Types:         viewDef.Types,
		Statuses:      statuses,
		Limit:         maxItems * 2, // fetch more for ranking/trimming
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, "selector_error", err.Error(), nil)
		return
	}

	// Rank items.
	type rankedItem struct {
		rec   contextstore.Record
		score float64
	}
	ranked := make([]rankedItem, len(items))
	for i, rec := range items {
		typeScore := 1.0
		if ct, ok := reg.GetType(rec.RecordType); ok && ct.RetrievalRankBias > 0 {
			typeScore = ct.RetrievalRankBias
		}
		statusScore := 0.5
		if w, ok := viewDef.RankWeights[rec.Status]; ok {
			statusScore = w
		}
		ranked[i] = rankedItem{rec: rec, score: typeScore * statusScore}
	}

	// Sort by score desc.
	for i := 0; i < len(ranked)-1; i++ {
		for j := i + 1; j < len(ranked); j++ {
			if ranked[j].score > ranked[i].score {
				ranked[i], ranked[j] = ranked[j], ranked[i]
			}
		}
	}

	// Build pack with budget.
	var packItems []map[string]any
	tokensSoFar := 0
	for _, rr := range ranked {
		if len(packItems) >= maxItems {
			break
		}
		tokens := contextstore.EstimateTokens(rr.rec.Payload)
		if maxTokens > 0 && tokensSoFar+tokens > maxTokens {
			break
		}
		packItems = append(packItems, map[string]any{
			"record_id":       rr.rec.RecordID,
			"namespace":       rr.rec.Namespace,
			"key":             rr.rec.Key,
			"record_type":     rr.rec.RecordType,
			"status":          rr.rec.Status,
			"content_version": rr.rec.ContentVersion,
			"payload":         json.RawMessage(rr.rec.Payload),
		})
		tokensSoFar += tokens
	}

	if packItems == nil {
		packItems = []map[string]any{}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"view":           req.ViewID,
		"generated_at":   time.Now().UTC().Format(time.RFC3339),
		"token_estimate": tokensSoFar,
		"items":          packItems,
		"count":          len(packItems),
	})
}
