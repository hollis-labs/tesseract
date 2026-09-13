package contextapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/hollis-labs/tesseract/domains"
	"github.com/hollis-labs/tesseract/internal/memory"
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
	store := s.itemRevisionStore()
	if store == nil {
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

	state, err := store.GetState(r.Context(), itemID)
	if err != nil {
		if errors.Is(err, memory.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "item_id not found: "+itemID, nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "read_failed", err.Error(), nil)
		return
	}
	if !requireNamespaceAccess(w, r, state.Namespace) {
		return
	}
	if !s.itemDomainAvailable(state.Domain) {
		writeError(w, http.StatusServiceUnavailable, "domain_unavailable",
			"no store is wired for resolved item domain "+string(state.Domain),
			map[string]any{"domain": state.Domain})
		return
	}

	if history {
		pr, ok := s.historyPageRequest(w, r)
		if !ok {
			return
		}
		revs, readErr := store.GetHistoryByItemID(r.Context(), itemID)
		if readErr != nil {
			writeItemReadError(w, readErr)
			return
		}
		writeHistoryPage(w, revs, pr, memory.ItemHistoryOrderingFingerprint(itemID))
		return
	}

	var rev memory.Revision
	if state.Domain == domains.Event {
		rev, err = store.GetCurrentByItemID(r.Context(), itemID)
	} else {
		rev, err = store.GetCurrentByItemIDReinforced(r.Context(), itemID)
	}
	if err != nil {
		writeItemReadError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rev)
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
