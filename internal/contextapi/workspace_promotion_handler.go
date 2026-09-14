package contextapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/hollis-labs/tesseract/domains"
	"github.com/hollis-labs/tesseract/internal/memory"
	"github.com/hollis-labs/tesseract/internal/workspace"
	"github.com/hollis-labs/tesseract/internal/workspacepromotion"
)

type workspacePromotionTargetRequest struct {
	Domain             domains.Domain     `json:"domain,omitempty"`
	Namespace          *string            `json:"namespace,omitempty"`
	Key                *string            `json:"key,omitempty"`
	ItemID             *string            `json:"item_id,omitempty"`
	ExpectedRevisionID *string            `json:"expected_revision_id,omitempty"`
	Author             memory.Author      `json:"author"`
	SessionID          string             `json:"session_id"`
	Tags               []string           `json:"tags,omitempty"`
	ConsumerState      *json.RawMessage   `json:"consumer_state,omitempty"`
	Confidence         float64            `json:"confidence,omitempty"`
	TTLSeconds         int64              `json:"ttl_seconds,omitempty"`
	DataSchemaHash     string             `json:"data_schema_hash,omitempty"`
	WorkstreamID       presentString      `json:"workstream_id,omitempty"`
	Status             memory.Status      `json:"status,omitempty"`
	Trigger            memory.Trigger     `json:"trigger,omitempty"`
	DerivedFrom        memory.DerivedFrom `json:"derived_from,omitempty"`
	Kind               string             `json:"kind,omitempty"`
	Source             string             `json:"source,omitempty"`
	Pointer            *memory.Pointer    `json:"pointer,omitempty"`
	domainPresent      bool
}

type workspacePromotionRequestHTTP struct {
	SourceItemID       string                          `json:"source_item_id"`
	SourceVersionToken string                          `json:"source_version_token"`
	Actor              string                          `json:"actor"`
	Reason             string                          `json:"reason,omitempty"`
	Target             workspacePromotionTargetRequest `json:"target"`
}

type workspacePromotionApproveHTTP struct {
	RequestID string `json:"request_id"`
	Actor     string `json:"actor"`
	Notes     string `json:"notes,omitempty"`
}

type workspacePromotionApplyHTTP struct {
	RequestID string `json:"request_id"`
	Actor     string `json:"actor"`
}

var (
	promotionTargetFields = promotionFieldSet(
		"domain", "namespace", "key", "item_id", "expected_revision_id", "author", "session_id", "tags",
		"consumer_state", "confidence", "ttl_seconds", "data_schema_hash", "workstream_id", "status", "trigger",
		"derived_from", "kind", "source", "pointer",
	)
	promotionRequestFields = promotionFieldSet("source_item_id", "source_version_token", "actor", "reason", "target")
	promotionApproveFields = promotionFieldSet("request_id", "actor", "notes")
	promotionApplyFields   = promotionFieldSet("request_id", "actor")
	promotionAuthorFields  = promotionFieldSet("agent_id", "agent_version")
	promotionPointerFields = promotionFieldSet("scheme", "locator", "resolved_at")
)

func promotionFieldSet(names ...string) map[string]struct{} {
	out := make(map[string]struct{}, len(names))
	for _, name := range names {
		out[name] = struct{}{}
	}
	return out
}

func validatePromotionFields(raw []byte, allowed map[string]struct{}) (map[string]json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	for name, value := range fields {
		if _, ok := allowed[name]; !ok {
			return nil, fmt.Errorf("json: unknown field %q", name)
		}
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return nil, fmt.Errorf("%s must not be null; omit it instead", name)
		}
	}
	return fields, nil
}

func decodePromotionObject(raw []byte, dst any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return errors.New("request body must contain one JSON object")
	}
	return nil
}

func (v *workspacePromotionTargetRequest) UnmarshalJSON(raw []byte) error {
	fields, err := validatePromotionFields(raw, promotionTargetFields)
	if err != nil {
		return err
	}
	type plain workspacePromotionTargetRequest
	var decoded plain
	if err := decodePromotionObject(raw, &decoded); err != nil {
		return err
	}
	if value, ok := fields["author"]; ok {
		if _, err := validatePromotionFields(value, promotionAuthorFields); err != nil {
			return fmt.Errorf("author.%w", err)
		}
	}
	if value, ok := fields["pointer"]; ok {
		if _, err := validatePromotionFields(value, promotionPointerFields); err != nil {
			return fmt.Errorf("pointer.%w", err)
		}
	}
	*v = workspacePromotionTargetRequest(decoded)
	_, v.domainPresent = fields["domain"]
	return nil
}

func (v *workspacePromotionRequestHTTP) UnmarshalJSON(raw []byte) error {
	if _, err := validatePromotionFields(raw, promotionRequestFields); err != nil {
		return err
	}
	type plain workspacePromotionRequestHTTP
	var decoded plain
	if err := decodePromotionObject(raw, &decoded); err != nil {
		return err
	}
	*v = workspacePromotionRequestHTTP(decoded)
	return nil
}

func (v *workspacePromotionApproveHTTP) UnmarshalJSON(raw []byte) error {
	if _, err := validatePromotionFields(raw, promotionApproveFields); err != nil {
		return err
	}
	type plain workspacePromotionApproveHTTP
	var decoded plain
	if err := decodePromotionObject(raw, &decoded); err != nil {
		return err
	}
	*v = workspacePromotionApproveHTTP(decoded)
	return nil
}

func (v *workspacePromotionApplyHTTP) UnmarshalJSON(raw []byte) error {
	if _, err := validatePromotionFields(raw, promotionApplyFields); err != nil {
		return err
	}
	type plain workspacePromotionApplyHTTP
	var decoded plain
	if err := decodePromotionObject(raw, &decoded); err != nil {
		return err
	}
	*v = workspacePromotionApplyHTTP(decoded)
	return nil
}

func (s *Server) handleWorkspacePromoteRequest(w http.ResponseWriter, r *http.Request) {
	if s.WorkspacePromotionStore == nil {
		writeError(w, http.StatusServiceUnavailable, "domain_unavailable", "workspace promotion store is not wired", nil)
		return
	}
	if !requireScope(w, r, "promote.request") {
		return
	}
	var req workspacePromotionRequestHTTP
	if !decodeRequestBody(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.SourceItemID) == "" || strings.TrimSpace(req.SourceVersionToken) == "" || strings.TrimSpace(req.Actor) == "" {
		writeError(w, http.StatusBadRequest, "validation_error", "source_item_id, source_version_token, and actor are required", nil)
		return
	}

	source, err := s.itemService().LookupMetadata(r.Context(), req.SourceItemID)
	if err != nil {
		writeWorkspacePromotionError(w, err)
		return
	}
	if !s.authorizeWorkspacePromotion(w, r, "promote.request", source.Namespace, source.Namespace) {
		return
	}
	if source.Domain != workspace.Domain {
		writeError(w, http.StatusBadRequest, "validation_error", "source_item_id must identify a workspace item", nil)
		return
	}
	targetNamespace := stringValue(req.Target.Namespace)
	if req.Target.ItemID != nil {
		if req.Target.domainPresent || req.Target.Namespace != nil || req.Target.Key != nil {
			writeError(w, http.StatusBadRequest, "validation_error", "existing target item_id cannot be combined with domain, namespace, or key", nil)
			return
		}
		targetMeta, targetErr := s.itemService().LookupMetadata(r.Context(), *req.Target.ItemID)
		if targetErr != nil {
			writeWorkspacePromotionError(w, targetErr)
			return
		}
		targetNamespace = targetMeta.Namespace
		if !s.authorizeWorkspacePromotion(w, r, "promote.request", targetNamespace, targetNamespace) {
			return
		}
		if targetMeta.Domain == workspace.Domain || targetMeta.Domain == "context" {
			writeError(w, http.StatusBadRequest, "validation_error", "target item must identify a revisioned memory, knowledge, or event item", nil)
			return
		}
	} else {
		if !s.authorizeWorkspacePromotion(w, r, "promote.request", targetNamespace, targetNamespace) {
			return
		}
	}

	target := workspacepromotion.Target{
		Domain: req.Target.Domain, Namespace: stringValue(req.Target.Namespace), Key: stringValue(req.Target.Key),
		ItemID: stringValue(req.Target.ItemID), ExpectedRevisionID: stringValue(req.Target.ExpectedRevisionID),
		Author: req.Target.Author, SessionID: req.Target.SessionID, Tags: req.Target.Tags,
		Confidence: req.Target.Confidence, TTLSeconds: req.Target.TTLSeconds, DataSchemaHash: req.Target.DataSchemaHash,
		WorkstreamID: req.Target.WorkstreamID.Pointer(), Status: req.Target.Status, Trigger: req.Target.Trigger,
		DerivedFrom: req.Target.DerivedFrom, Kind: req.Target.Kind, Source: req.Target.Source, Pointer: req.Target.Pointer,
	}
	if req.Target.ConsumerState != nil {
		target.ConsumerState = append([]byte(nil), (*req.Target.ConsumerState)...)
	}
	receipt, err := s.WorkspacePromotionStore.Request(r.Context(), workspacepromotion.RequestInput{
		SourceItemID: req.SourceItemID, SourceVersionToken: req.SourceVersionToken, Actor: req.Actor, Reason: req.Reason, Target: target,
	})
	if err != nil {
		writeWorkspacePromotionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, receipt)
}

func (s *Server) handleWorkspacePromoteApprove(w http.ResponseWriter, r *http.Request) {
	if s.WorkspacePromotionStore == nil {
		writeError(w, http.StatusServiceUnavailable, "domain_unavailable", "workspace promotion store is not wired", nil)
		return
	}
	if !requireScope(w, r, "promote.approve") {
		return
	}
	var req workspacePromotionApproveHTTP
	if !decodeRequestBody(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.RequestID) == "" || strings.TrimSpace(req.Actor) == "" {
		writeError(w, http.StatusBadRequest, "validation_error", "request_id and actor are required", nil)
		return
	}
	meta, err := s.WorkspacePromotionStore.LookupMetadata(r.Context(), req.RequestID)
	if err != nil {
		writeWorkspacePromotionError(w, err)
		return
	}
	if !s.authorizeWorkspacePromotion(w, r, "promote.approve", meta.SourceNamespace, meta.TargetNamespace) {
		return
	}
	receipt, err := s.WorkspacePromotionStore.Approve(r.Context(), workspacepromotion.ApproveInput(req))
	if err != nil {
		writeWorkspacePromotionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, receipt)
}

func (s *Server) handleWorkspacePromoteApply(w http.ResponseWriter, r *http.Request) {
	if s.WorkspacePromotionStore == nil {
		writeError(w, http.StatusServiceUnavailable, "domain_unavailable", "workspace promotion store is not wired", nil)
		return
	}
	if !requireScope(w, r, "promote.apply") {
		return
	}
	var req workspacePromotionApplyHTTP
	if !decodeRequestBody(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.RequestID) == "" || strings.TrimSpace(req.Actor) == "" {
		writeError(w, http.StatusBadRequest, "validation_error", "request_id and actor are required", nil)
		return
	}
	meta, err := s.WorkspacePromotionStore.LookupMetadata(r.Context(), req.RequestID)
	if err != nil {
		writeWorkspacePromotionError(w, err)
		return
	}
	if !s.authorizeWorkspacePromotion(w, r, "promote.apply", meta.SourceNamespace, meta.TargetNamespace) {
		return
	}
	receipt, err := s.WorkspacePromotionStore.Apply(r.Context(), workspacepromotion.ApplyInput(req))
	if err != nil {
		writeWorkspacePromotionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, receipt)
}

func (s *Server) authorizeWorkspacePromotion(w http.ResponseWriter, r *http.Request, op, source, target string) bool {
	if source == "" || target == "" {
		writeError(w, http.StatusBadRequest, "validation_error", "source and target namespaces are required", nil)
		return false
	}
	for _, namespace := range []string{source, target} {
		if !promotionNamespaceAccess(w, r, namespace) {
			return false
		}
		if err := s.Policy.ValidateTierPolicy(namespace, op, 0, nil); err != nil {
			writeError(w, http.StatusForbidden, "policy_violation", "promotion namespace policy denies "+op, nil)
			return false
		}
	}
	return true
}

func promotionNamespaceAccess(w http.ResponseWriter, r *http.Request, namespace string) bool {
	claims, ok := getTokenClaims(r)
	if !ok {
		if authModeConfigured(r) {
			writeError(w, http.StatusForbidden, "namespace_not_permitted", "token is not permitted to access a promotion namespace", nil)
			return false
		}
		return true
	}
	if namespacePermitted(claims.NamespaceGlobs, namespace) {
		return true
	}
	writeError(w, http.StatusForbidden, "namespace_not_permitted", "token is not permitted to access a promotion namespace", nil)
	return false
}

func writeWorkspacePromotionError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, workspacepromotion.ErrInvalidInput), errors.Is(err, memory.ErrInvalidInput):
		writeError(w, http.StatusBadRequest, "validation_error", err.Error(), nil)
	case errors.Is(err, workspacepromotion.ErrNotFound), errors.Is(err, workspacepromotion.ErrSourceNotFound), errors.Is(err, memory.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", err.Error(), nil)
	case errors.Is(err, workspacepromotion.ErrSourceDeleted):
		writeError(w, http.StatusGone, "source_deleted", err.Error(), nil)
	case errors.Is(err, workspacepromotion.ErrSourceStale):
		writeError(w, http.StatusConflict, "source_conflict", err.Error(), nil)
	case errors.Is(err, workspacepromotion.ErrTargetStale), errors.Is(err, workspacepromotion.ErrTargetKeyOccupied):
		writeError(w, http.StatusConflict, "target_conflict", err.Error(), nil)
	case errors.Is(err, workspacepromotion.ErrNotApproved):
		writeError(w, http.StatusConflict, "not_approved", err.Error(), nil)
	default:
		writeError(w, http.StatusInternalServerError, "promotion_failed", err.Error(), nil)
	}
}
