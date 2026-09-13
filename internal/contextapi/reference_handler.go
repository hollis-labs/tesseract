package contextapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/hollis-labs/tesseract/domains"
	"github.com/hollis-labs/tesseract/internal/itemservice"
)

type referenceResolveRequest struct {
	ItemID     json.RawMessage `json:"item_id,omitempty"`
	RevisionID json.RawMessage `json:"revision_id,omitempty"`
	Domain     json.RawMessage `json:"domain,omitempty"`
	Namespace  json.RawMessage `json:"namespace,omitempty"`
	Key        json.RawMessage `json:"key,omitempty"`
	URI        json.RawMessage `json:"uri,omitempty"`
}

func (req referenceResolveRequest) selector() (itemservice.ReferenceSelector, error) {
	itemID, hasItemID, err := referenceString(req.ItemID)
	if err != nil {
		return itemservice.ReferenceSelector{}, err
	}
	revisionID, hasRevisionID, err := referenceString(req.RevisionID)
	if err != nil {
		return itemservice.ReferenceSelector{}, err
	}
	domain, hasDomain, err := referenceString(req.Domain)
	if err != nil {
		return itemservice.ReferenceSelector{}, err
	}
	namespace, hasNamespace, err := referenceString(req.Namespace)
	if err != nil {
		return itemservice.ReferenceSelector{}, err
	}
	key, hasKey, err := referenceString(req.Key)
	if err != nil {
		return itemservice.ReferenceSelector{}, err
	}
	uri, hasURI, err := referenceString(req.URI)
	if err != nil {
		return itemservice.ReferenceSelector{}, err
	}

	selectors := 0
	if hasItemID {
		selectors++
	}
	if hasRevisionID {
		selectors++
	}
	if hasURI {
		selectors++
	}
	keyParts := 0
	for _, present := range []bool{hasDomain, hasNamespace, hasKey} {
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
	return itemservice.ReferenceSelector{
		ItemID: itemID, RevisionID: revisionID, Domain: domain,
		Namespace: namespace, Key: key, URI: uri,
	}, nil
}

// referenceString keeps wire presence distinct from value. In particular,
// JSON null is a supplied selector field and must not collapse into absence.
// Values are not trimmed because keys and namespaces are exact locators.
func referenceString(raw json.RawMessage) (string, bool, error) {
	if len(raw) == 0 {
		return "", false, nil
	}
	var value *string
	if err := json.Unmarshal(raw, &value); err != nil || value == nil || *value == "" {
		return "", true, itemservice.ErrInvalidReference
	}
	return *value, true, nil
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
	if !requireScope(w, r, "memory:read") {
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
	if selector.Domain != "" && !s.itemDomainAvailable(domains.Domain(selector.Domain)) {
		writeError(w, http.StatusServiceUnavailable, "domain_unavailable",
			"the selected reference domain is not available on this deployment", nil)
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
	return namespacePermitted(claims.NamespaceGlobs, namespace)
}
