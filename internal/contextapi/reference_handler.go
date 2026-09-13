package contextapi

import (
	"errors"
	"net/http"

	"github.com/hollis-labs/tesseract/domains"
	"github.com/hollis-labs/tesseract/internal/itemservice"
)

type referenceResolveRequest struct {
	ItemID     *string `json:"item_id,omitempty"`
	RevisionID *string `json:"revision_id,omitempty"`
	Domain     *string `json:"domain,omitempty"`
	Namespace  *string `json:"namespace,omitempty"`
	Key        *string `json:"key,omitempty"`
	URI        *string `json:"uri,omitempty"`
}

func (req referenceResolveRequest) selector() (itemservice.ReferenceSelector, error) {
	selectors := 0
	if req.ItemID != nil {
		selectors++
	}
	if req.RevisionID != nil {
		selectors++
	}
	if req.URI != nil {
		selectors++
	}
	keyParts := 0
	for _, present := range []bool{req.Domain != nil, req.Namespace != nil, req.Key != nil} {
		if present {
			keyParts++
		}
	}
	if keyParts > 0 {
		selectors++
	}
	if selectors != 1 || (keyParts > 0 && keyParts != 3) {
		return itemservice.ReferenceSelector{}, itemservice.ErrInvalidReference
	}
	selector := itemservice.ReferenceSelector{}
	if req.ItemID != nil {
		selector.ItemID = *req.ItemID
	}
	if req.RevisionID != nil {
		selector.RevisionID = *req.RevisionID
	}
	if req.URI != nil {
		selector.URI = *req.URI
	}
	if keyParts == 3 {
		selector.Domain, selector.Namespace, selector.Key = *req.Domain, *req.Namespace, *req.Key
	}
	return selector, nil
}

func (s *Server) handleReferenceResolve(w http.ResponseWriter, r *http.Request) {
	var req referenceResolveRequest
	if !decodeRequestBody(w, r, &req) {
		return
	}
	selector, err := req.selector()
	if err != nil {
		writeError(w, http.StatusBadRequest, "validation_error",
			"choose exactly one of item_id, revision_id, domain + namespace + key, or uri", nil)
		return
	}
	result, err := s.itemService().ResolveReference(r.Context(), selector)
	if err != nil {
		switch {
		case errors.Is(err, itemservice.ErrInvalidReference):
			writeError(w, http.StatusBadRequest, "validation_error", err.Error(), nil)
		case errors.Is(err, itemservice.ErrBackendUnavailable):
			writeError(w, http.StatusServiceUnavailable, "domain_unavailable",
				"the required reference backend is not available on this deployment", nil)
		default:
			writeError(w, http.StatusInternalServerError, "read_failed", err.Error(), nil)
		}
		return
	}
	if result.Status == itemservice.ResolutionResolved || result.Status == itemservice.ResolutionDeleted {
		if !referenceNamespacePermitted(r, result.Namespace) {
			writeError(w, http.StatusForbidden, "namespace_not_permitted",
				"token namespace globs do not permit resolving this reference", nil)
			return
		}
		if !s.itemDomainAvailable(domains.Domain(result.Domain)) {
			writeError(w, http.StatusServiceUnavailable, "domain_unavailable",
				"the resolved reference domain is not available on this deployment", nil)
			return
		}
	}
	writeJSON(w, http.StatusOK, result)
}

func referenceNamespacePermitted(r *http.Request, namespace string) bool {
	claims, ok := getTokenClaims(r)
	if !ok {
		return !authModeConfigured(r)
	}
	return namespaceSelectorPermitted(claims.NamespaceGlobs, namespace, nil)
}
