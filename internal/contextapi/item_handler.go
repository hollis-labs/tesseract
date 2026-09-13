package contextapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/hollis-labs/tesseract/domains"
	"github.com/hollis-labs/tesseract/internal/itemservice"
	"github.com/hollis-labs/tesseract/internal/memory"
	"github.com/hollis-labs/tesseract/internal/workspace"
)

// itemRevisionStore returns the shared revision store through whichever
// curated-domain adapter is wired. The resolved item's domain is checked
// separately before content is returned, so a knowledge-only deployment does
// not accidentally expose memory rows merely because both use one database.
func (s *Server) itemRevisionStore() *memory.Store {
	if s.MemoryStore != nil {
		return s.MemoryStore
	}
	if s.KnowledgeStore != nil {
		return s.KnowledgeStore.RevisionStore()
	}
	if s.EventStore != nil {
		return s.EventStore.RevisionStore()
	}
	return nil
}

func (s *Server) itemDomainAvailable(domain domains.Domain) bool {
	switch domain {
	case domains.Memory:
		return s.MemoryStore != nil
	case domains.Knowledge:
		return s.KnowledgeStore != nil
	case domains.Event:
		return s.EventStore != nil
	case domains.Domain(workspace.Domain):
		return s.WorkspaceStore != nil
	default:
		return false
	}
}

// handleItemRead serves both domain-neutral item routes:
//
//	GET /v1/items/{item_id}
//	GET /v1/items/{item_id}/history
//
// Resolution reads only memory_state first. Namespace authorization then runs
// before revision content is loaded or activation is reinforced.
func (s *Server) handleItemRead(w http.ResponseWriter, r *http.Request) {
	if s.itemRevisionStore() == nil && s.WorkspaceStore == nil {
		writeError(w, http.StatusServiceUnavailable, "domain_unavailable",
			"no item-backed domain store is wired into this server", nil)
		return
	}

	relative := strings.TrimPrefix(r.URL.Path, "/v1/items/")
	history := strings.HasSuffix(relative, "/history")
	if history {
		relative = strings.TrimSuffix(relative, "/history")
	}
	itemID := strings.TrimSpace(relative)
	if itemID == "" || strings.Contains(itemID, "/") {
		writeError(w, http.StatusBadRequest, "validation_error", "item_id is required", nil)
		return
	}

	meta, err := s.itemService().LookupMetadata(r.Context(), itemID)
	if err != nil {
		if errors.Is(err, memory.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "item_id not found: "+itemID, nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "read_failed", err.Error(), nil)
		return
	}
	if !requireNamespaceAccess(w, r, meta.Namespace) {
		return
	}
	if !s.itemDomainAvailable(domains.Domain(meta.Domain)) {
		writeError(w, http.StatusServiceUnavailable, "domain_unavailable",
			"no store is wired for resolved item domain "+meta.Domain,
			map[string]any{"domain": meta.Domain})
		return
	}

	if history {
		pr, ok := s.historyPageRequest(w, r)
		if !ok {
			return
		}
		revs, readErr := s.itemService().History(r.Context(), meta)
		if readErr != nil {
			if errors.Is(readErr, itemservice.ErrHistoryUnavailable) {
				writeWorkspaceError(w, readErr, "read_failed")
				return
			}
			writeItemReadError(w, readErr)
			return
		}
		writeHistoryPage(w, revs, pr, memory.ItemHistoryOrderingFingerprint(itemID))
		return
	}

	read, err := s.itemService().ReadCurrent(r.Context(), meta)
	if err != nil {
		writeWorkspaceError(w, err, "read_failed")
		return
	}
	if read.Item != nil {
		writeJSON(w, http.StatusOK, *read.Item)
		return
	}
	writeJSON(w, http.StatusOK, *read.Revision)
}

func writeItemReadError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, memory.ErrInvalidInput):
		writeError(w, http.StatusBadRequest, "validation_error", err.Error(), nil)
	case errors.Is(err, memory.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", err.Error(), nil)
	default:
		writeError(w, http.StatusInternalServerError, "read_failed", err.Error(), nil)
	}
}
