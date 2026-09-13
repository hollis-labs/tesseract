package contextapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/hollis-labs/tesseract/internal/itemservice"
	"github.com/hollis-labs/tesseract/internal/memory"
	"github.com/hollis-labs/tesseract/internal/workspace"
)

func (s *Server) itemService() *itemservice.Service {
	return &itemservice.Service{Revisions: s.itemRevisionStore(), Workspace: s.WorkspaceStore}
}

type workspaceWriteRequest struct {
	Namespace      *string                `json:"namespace,omitempty"`
	ItemID         *string                `json:"item_id,omitempty"`
	VersionToken   *string                `json:"version_token,omitempty"`
	IdempotencyKey *string                `json:"idempotency_key,omitempty"`
	Key            *string                `json:"key,omitempty"`
	Summary        *string                `json:"summary,omitempty"`
	Body           *string                `json:"body,omitempty"`
	Data           *json.RawMessage       `json:"data,omitempty"`
	DataSchemaHash *string                `json:"data_schema_hash,omitempty"`
	Tags           *[]string              `json:"tags,omitempty"`
	ConsumerState  *json.RawMessage       `json:"consumer_state,omitempty"`
	ClearFields    []workspace.ClearField `json:"clear_fields,omitempty"`
	Author         memory.Author          `json:"author"`
	SessionID      string                 `json:"session_id"`
}

func (s *Server) handleWorkspaceWrite(w http.ResponseWriter, r *http.Request) {
	if s.WorkspaceStore == nil {
		writeError(w, http.StatusServiceUnavailable, "domain_unavailable", "workspace store is not wired", nil)
		return
	}
	if !requireScope(w, r, "write") {
		return
	}
	var req workspaceWriteRequest
	if !decodeRequestBody(w, r, &req) {
		return
	}
	if req.Namespace != nil {
		if req.ItemID != nil || req.VersionToken != nil {
			writeError(w, http.StatusBadRequest, "validation_error", "choose create by namespace or edit by item_id + version_token; do not mix selectors", nil)
			return
		}
		if !requireNamespaceAccess(w, r, *req.Namespace) {
			return
		}
		var data, consumerState json.RawMessage
		if req.Data != nil {
			data = wholeRawMessage(req.Data)
		}
		if req.ConsumerState != nil {
			consumerState = wholeRawMessage(req.ConsumerState)
		}
		create := workspace.CreateRequest{CreateInput: workspace.CreateInput{Namespace: *req.Namespace, Key: stringValue(req.Key), Summary: stringValue(req.Summary), Body: stringValue(req.Body), Data: data, DataSchemaHash: stringValue(req.DataSchemaHash), Tags: sliceValue(req.Tags), ConsumerState: consumerState, Author: req.Author, SessionID: req.SessionID}, IdempotencyKey: stringValue(req.IdempotencyKey)}
		receipt, err := s.WorkspaceStore.CreateWithReceipt(r.Context(), create)
		if err != nil {
			writeWorkspaceError(w, err, "workspace_write_failed")
			return
		}
		writeJSON(w, http.StatusOK, receipt)
		return
	}
	if req.ItemID == nil || req.VersionToken == nil {
		writeError(w, http.StatusBadRequest, "validation_error", "choose create by namespace or edit by item_id + version_token", nil)
		return
	}
	if req.IdempotencyKey != nil {
		writeError(w, http.StatusBadRequest, "validation_error", "idempotency_key is valid only for create", nil)
		return
	}
	meta, err := s.itemService().LookupMetadata(r.Context(), *req.ItemID)
	if err != nil {
		writeWorkspaceError(w, err, "workspace_write_failed")
		return
	}
	if meta.Domain != workspace.Domain {
		writeError(w, http.StatusBadRequest, "validation_error", "item_id does not identify a workspace item", nil)
		return
	}
	if !requireNamespaceAccess(w, r, meta.Namespace) {
		return
	}
	edit := workspace.EditInput{ItemID: *req.ItemID, VersionToken: *req.VersionToken, Key: req.Key, Summary: req.Summary, Body: req.Body, Data: req.Data, DataSchemaHash: req.DataSchemaHash, Tags: req.Tags, ConsumerState: req.ConsumerState, ClearFields: req.ClearFields, Author: req.Author, SessionID: req.SessionID}
	item, err := s.WorkspaceStore.Edit(r.Context(), edit)
	if err != nil {
		writeWorkspaceError(w, err, "workspace_write_failed")
		return
	}
	writeJSON(w, http.StatusOK, workspace.MutationReceipt{Status: "updated", ItemID: item.ItemID, VersionToken: item.VersionToken})
}

type workspaceDeleteRequest struct {
	ItemID       string `json:"item_id"`
	VersionToken string `json:"version_token"`
}

func (s *Server) handleWorkspaceDelete(w http.ResponseWriter, r *http.Request) {
	if s.WorkspaceStore == nil {
		writeError(w, http.StatusServiceUnavailable, "domain_unavailable", "workspace store is not wired", nil)
		return
	}
	if !requireScope(w, r, "write") {
		return
	}
	var req workspaceDeleteRequest
	if !decodeRequestBody(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.ItemID) == "" || req.VersionToken == "" {
		writeError(w, http.StatusBadRequest, "validation_error", "item_id and version_token are required", nil)
		return
	}
	meta, err := s.itemService().LookupMetadata(r.Context(), req.ItemID)
	if err != nil {
		writeWorkspaceError(w, err, "workspace_delete_failed")
		return
	}
	if meta.Domain != workspace.Domain {
		writeError(w, http.StatusBadRequest, "validation_error", "item_id does not identify a workspace item", nil)
		return
	}
	if !requireNamespaceAccess(w, r, meta.Namespace) {
		return
	}
	receipt, err := s.WorkspaceStore.DeleteWithReceipt(r.Context(), workspace.DeleteInput{ItemID: req.ItemID, VersionToken: req.VersionToken})
	if err != nil {
		writeWorkspaceError(w, err, "workspace_delete_failed")
		return
	}
	writeJSON(w, http.StatusOK, receipt)
}

func (s *Server) handleWorkspaceCurrent(w http.ResponseWriter, r *http.Request) {
	if s.WorkspaceStore == nil {
		writeError(w, http.StatusServiceUnavailable, "domain_unavailable", "workspace store is not wired", nil)
		return
	}
	ns := r.URL.Query().Get("namespace")
	key := r.URL.Query().Get("key")
	if ns == "" || key == "" {
		writeError(w, http.StatusBadRequest, "validation_error", "namespace and key are required", nil)
		return
	}
	if !requireNamespaceAccess(w, r, ns) {
		return
	}
	item, err := s.itemService().ReadWorkspaceByKey(r.Context(), ns, key)
	if err != nil {
		writeWorkspaceError(w, err, "read_failed")
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func writeWorkspaceError(w http.ResponseWriter, err error, fallback string) {
	switch {
	case errors.Is(err, workspace.ErrInvalidInput):
		writeError(w, http.StatusBadRequest, "validation_error", err.Error(), nil)
	case errors.Is(err, workspace.ErrNotFound), errors.Is(err, memory.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", err.Error(), nil)
	case errors.Is(err, workspace.ErrDeleted):
		writeError(w, http.StatusGone, "deleted", err.Error(), nil)
	case errors.Is(err, workspace.ErrKeyConflict):
		writeError(w, http.StatusConflict, "key_conflict", err.Error(), nil)
	case errors.Is(err, workspace.ErrVersionConflict):
		writeError(w, http.StatusConflict, "version_conflict", err.Error(), nil)
	case errors.Is(err, workspace.ErrIdempotencyConflict):
		writeError(w, http.StatusConflict, "idempotency_conflict", err.Error(), nil)
	case errors.Is(err, itemservice.ErrHistoryUnavailable):
		writeError(w, http.StatusBadRequest, "history_unavailable", err.Error(), nil)
	default:
		writeError(w, http.StatusInternalServerError, fallback, err.Error(), nil)
	}
}

func stringValue(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}
func sliceValue(v *[]string) []string {
	if v == nil {
		return nil
	}
	return *v
}

// wholeRawMessage copies one complete document already validated by the HTTP
// decoder while preserving omission separately through the pointer.
func wholeRawMessage(v *json.RawMessage) json.RawMessage {
	if v == nil {
		return nil
	}
	return bytes.Clone(*v)
}
