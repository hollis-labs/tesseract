package contextapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/hollis-labs/cortex/internal/contextstore"
)

// handlePromoteDeprecated returns 410 Gone for the old direct-promote endpoint.
func (s *Server) handlePromoteDeprecated(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusGone, "deprecated",
		"Use /v1/context/promote/request, /v1/context/promote/approve, /v1/context/promote/apply", nil)
}

// --- Request ---

type promoteRequestReq struct {
	Actor           string `json:"actor"`
	ClientID        string `json:"client_id"`
	SourceNamespace string `json:"source_namespace"`
	SourceKey       string `json:"source_key"`
	SourceRevisionID string `json:"source_revision_id"`
	TargetNamespace string `json:"target_namespace"`
	TargetKey       string `json:"target_key"`
	Reason          string `json:"reason,omitempty"`
	ProposedSummary string `json:"proposed_summary,omitempty"`
}

func (s *Server) handlePromoteRequest(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, "promote.request") {
		return
	}
	var req promoteRequestReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error(), nil)
		return
	}
	if !requireNamespaceAccess(w, r, req.SourceNamespace) {
		return
	}
	if req.SourceNamespace == "" || req.SourceKey == "" {
		writeError(w, http.StatusBadRequest, "validation_error", "source_namespace and source_key required", nil)
		return
	}
	if req.TargetNamespace == "" || req.TargetKey == "" {
		writeError(w, http.StatusBadRequest, "validation_error", "target_namespace and target_key required", nil)
		return
	}

	ctx := r.Context()

	// Validate source record exists.
	srcHead, err := s.Store.Head(ctx, req.SourceNamespace, req.SourceKey)
	if err != nil {
		writeError(w, http.StatusBadRequest, "validation_error",
			fmt.Sprintf("source record not found: %v", err), nil)
		return
	}
	// If caller specified a revision_id, verify it matches.
	if req.SourceRevisionID != "" && srcHead.RecordID != req.SourceRevisionID {
		writeError(w, http.StatusBadRequest, "validation_error",
			"source_revision_id does not match current head", nil)
		return
	}

	actor := req.Actor
	if actor == "" {
		actor = "anonymous"
	}
	clientID := req.ClientID
	if clientID == "" {
		clientID = "unknown"
	}

	requestID := "req-" + newRequestID()[4:] // reuse random generator
	now := time.Now().UTC().Format(time.RFC3339)

	pr := contextstore.PromoteRequest{
		Type:             "promote.request",
		RequestID:        requestID,
		SourceNamespace:  srcHead.Namespace,
		SourceKey:        srcHead.Key,
		SourceRevisionID: srcHead.RecordID,
		SourceChecksum:   srcHead.Checksum,
		TargetNamespace:  req.TargetNamespace,
		TargetKey:        req.TargetKey,
		Reason:           req.Reason,
		ProposedSummary:  req.ProposedSummary,
		Status:           "pending",
		RequestedAt:      now,
		RequestedBy:      actor,
	}
	payload, _ := json.Marshal(pr)

	namespace := "app/" + clientID + "/promotions"
	reqRec, err := s.Store.AppendRecord(ctx, contextstore.AppendInput{
		Namespace: namespace,
		Key:       requestID,
		Actor:     actor,
		Payload:   payload,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "write_failed", err.Error(), nil)
		return
	}

	_ = s.Store.RecordAuditEvent(ctx, contextstore.AuditEvent{
		EventType: "promote.request.created",
		Actor:     actor,
		Namespace: namespace,
		Key:       requestID,
		RecordID:  reqRec.RecordID,
		Revision:  reqRec.Revision,
		Metadata: json.RawMessage(fmt.Sprintf(
			`{"request_id":%q,"source_namespace":%q,"source_key":%q,"target_namespace":%q,"target_key":%q}`,
			requestID, pr.SourceNamespace, pr.SourceKey, pr.TargetNamespace, pr.TargetKey,
		)),
	})

	writeJSON(w, http.StatusOK, map[string]any{
		"request_id": requestID,
		"status":     "pending",
	})
}

// --- Approve ---

type promoteApproveReq struct {
	Actor     string `json:"actor"`
	RequestID string `json:"request_id"`
	Notes     string `json:"notes,omitempty"`
}

func (s *Server) handlePromoteApprove(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, "promote.approve") {
		return
	}
	var req promoteApproveReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error(), nil)
		return
	}
	if req.RequestID == "" {
		writeError(w, http.StatusBadRequest, "validation_error", "request_id required", nil)
		return
	}

	ctx := r.Context()
	actor := req.Actor
	if actor == "" {
		actor = "user"
	}

	pr, reqNamespace, err := s.Store.GetPromoteRequest(ctx, req.RequestID)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", err.Error(), nil)
		return
	}
	if pr.Status != "pending" {
		writeError(w, http.StatusConflict, "already_approved",
			fmt.Sprintf("request has status %q, cannot approve", pr.Status), nil)
		return
	}

	approvalID := "appr-" + newRequestID()[4:]
	now := time.Now().UTC().Format(time.RFC3339)

	pa := contextstore.PromoteApproval{
		Type:             "promote.approve",
		ApprovalID:       approvalID,
		RequestID:        req.RequestID,
		RequestNamespace: reqNamespace,
		ApprovedAt:       now,
		ApprovedBy:       actor,
		Notes:            req.Notes,
	}
	approvalPayload, _ := json.Marshal(pa)
	_, err = s.Store.AppendRecord(ctx, contextstore.AppendInput{
		Namespace: "user/promotions",
		Key:       approvalID,
		Actor:     actor,
		Payload:   approvalPayload,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "write_failed", err.Error(), nil)
		return
	}

	// Update promote.request status to approved (new revision).
	pr.Status = "approved"
	pr.ApprovalID = approvalID
	pr.ApprovedBy = actor
	updatedPayload, _ := json.Marshal(pr)
	updRec, err := s.Store.AppendRecord(ctx, contextstore.AppendInput{
		Namespace: reqNamespace,
		Key:       req.RequestID,
		Actor:     actor,
		Payload:   updatedPayload,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "write_failed", err.Error(), nil)
		return
	}

	_ = s.Store.RecordAuditEvent(ctx, contextstore.AuditEvent{
		EventType: "promote.request.approved",
		Actor:     actor,
		Namespace: reqNamespace,
		Key:       req.RequestID,
		RecordID:  updRec.RecordID,
		Revision:  updRec.Revision,
		Metadata:  json.RawMessage(fmt.Sprintf(`{"request_id":%q,"approval_id":%q}`, req.RequestID, approvalID)),
	})

	writeJSON(w, http.StatusOK, map[string]any{
		"approval_id": approvalID,
		"request_id":  req.RequestID,
		"status":      "approved",
	})
}

// --- Apply ---

type promoteApplyReq struct {
	Actor     string `json:"actor"`
	RequestID string `json:"request_id"`
}

func (s *Server) handlePromoteApply(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, "promote.apply") {
		return
	}
	var req promoteApplyReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error(), nil)
		return
	}
	if req.RequestID == "" {
		writeError(w, http.StatusBadRequest, "validation_error", "request_id required", nil)
		return
	}

	ctx := r.Context()
	actor := req.Actor
	if actor == "" {
		actor = "user"
	}

	pr, reqNamespace, err := s.Store.GetPromoteRequest(ctx, req.RequestID)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", err.Error(), nil)
		return
	}
	if pr.Status != "approved" {
		writeError(w, http.StatusBadRequest, "not_approved",
			fmt.Sprintf("request has status %q; must be approved before applying", pr.Status), nil)
		return
	}

	// Verify the approval record exists.
	pa, err := s.Store.GetPromoteApproval(ctx, req.RequestID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "not_approved", err.Error(), nil)
		return
	}

	// Fetch source record by record_id.
	srcRec, err := s.Store.GetByRecordID(ctx, pr.SourceRevisionID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "source_not_found", err.Error(), nil)
		return
	}

	// Enforce actor policy: writes to user/* namespace require actor=user.
	if strings.HasPrefix(pr.TargetNamespace, "user/") && actor != "user" {
		writeError(w, http.StatusForbidden, "policy_denied",
			fmt.Sprintf("writes to protected namespace %q require actor=user", pr.TargetNamespace), nil)
		return
	}

	// Enforce policy: validate tier policy allows write to target namespace.
	if err := s.Policy.ValidateTierPolicy(pr.TargetNamespace, "promote.apply", len(srcRec.Payload), srcRec.Payload); err != nil {
		writeError(w, http.StatusForbidden, "policy_violation", err.Error(), nil)
		return
	}

	// Apply: write source payload to target namespace/key.
	newRec, err := s.Store.AppendRecord(ctx, contextstore.AppendInput{
		Namespace: pr.TargetNamespace,
		Key:       pr.TargetKey,
		Actor:     actor,
		Payload:   srcRec.Payload,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "apply_failed", err.Error(), nil)
		return
	}

	// Update promote.request status to applied.
	pr.Status = "applied"
	appliedPayload, _ := json.Marshal(pr)
	_, _ = s.Store.AppendRecord(ctx, contextstore.AppendInput{
		Namespace: reqNamespace,
		Key:       req.RequestID,
		Actor:     actor,
		Payload:   appliedPayload,
	})

	_ = s.Store.RecordAuditEvent(ctx, contextstore.AuditEvent{
		EventType: "promote",
		Actor:     actor,
		Namespace: pr.TargetNamespace,
		Key:       pr.TargetKey,
		RecordID:  newRec.RecordID,
		Revision:  newRec.Revision,
		Metadata: json.RawMessage(fmt.Sprintf(
			`{"request_id":%q,"approval_id":%q,"record_id":%q}`,
			req.RequestID, pa.ApprovalID, newRec.RecordID,
		)),
	})

	writeJSON(w, http.StatusOK, map[string]any{
		"record_id":   newRec.RecordID,
		"request_id":  req.RequestID,
		"approval_id": pa.ApprovalID,
	})
}
