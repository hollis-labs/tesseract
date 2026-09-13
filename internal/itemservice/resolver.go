package itemservice

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hollis-labs/tesseract/domains"
	"github.com/hollis-labs/tesseract/internal/memory"
	"github.com/hollis-labs/tesseract/internal/workspace"
)

var (
	ErrInvalidReference   = errors.New("invalid reference selector")
	ErrBackendUnavailable = errors.New("reference backend unavailable")
)

type ResolutionStatus string

const (
	ResolutionResolved             ResolutionStatus = "resolved"
	ResolutionDeleted              ResolutionStatus = "deleted"
	ResolutionNotFound             ResolutionStatus = "not_found"
	ResolutionAmbiguous            ResolutionStatus = "ambiguous"
	ResolutionUnsupportedReference ResolutionStatus = "unsupported_reference"
)

type ReferenceKind string

const (
	ReferenceKindItem     ReferenceKind = "tesseract_item"
	ReferenceKindRevision ReferenceKind = "tesseract_revision"
)

// ReferenceSelector accepts exactly one typed ID, one complete legacy key, or
// one canonical Tesseract URI. Key is a string because empty keys are not
// addressable; callers that need wire-level presence validation do that while
// decoding and then pass the normalized value here.
type ReferenceSelector struct {
	ItemID     string `json:"item_id,omitempty"`
	RevisionID string `json:"revision_id,omitempty"`
	Domain     string `json:"domain,omitempty"`
	Namespace  string `json:"namespace,omitempty"`
	Key        string `json:"key,omitempty"`
	URI        string `json:"uri,omitempty"`
}

type CanonicalReference struct {
	Kind  ReferenceKind `json:"kind"`
	RefID string        `json:"ref_id"`
	URI   string        `json:"uri"`
}

// ReferenceResolution is an identity-only result. Namespace is retained for
// adapter authorization and is never serialized on the wire.
type ReferenceResolution struct {
	Status     ResolutionStatus    `json:"status"`
	Ref        *CanonicalReference `json:"ref,omitempty"`
	ItemID     string              `json:"item_id,omitempty"`
	Domain     string              `json:"domain,omitempty"`
	ResolvedAt *time.Time          `json:"resolved_at,omitempty"`
	DeletedAt  *time.Time          `json:"deleted_at,omitempty"`
	Namespace  string              `json:"-"`
}

type referenceSelectorKind int

const (
	selectorInvalid referenceSelectorKind = iota
	selectorItem
	selectorRevision
	selectorKey
	selectorURI
)

func (s ReferenceSelector) kind() (referenceSelectorKind, error) {
	hasItem := s.ItemID != ""
	hasRevision := s.RevisionID != ""
	hasURI := s.URI != ""
	keyParts := 0
	for _, value := range []string{s.Domain, s.Namespace, s.Key} {
		if value != "" {
			keyParts++
		}
	}
	selectors := 0
	if hasItem {
		selectors++
	}
	if hasRevision {
		selectors++
	}
	if hasURI {
		selectors++
	}
	if keyParts > 0 {
		selectors++
	}
	if selectors != 1 || (keyParts > 0 && keyParts != 3) {
		return selectorInvalid, fmt.Errorf("%w: choose exactly one of item_id, revision_id, domain + namespace + key, or uri", ErrInvalidReference)
	}
	switch {
	case hasItem:
		return selectorItem, nil
	case hasRevision:
		return selectorRevision, nil
	case hasURI:
		return selectorURI, nil
	default:
		return selectorKey, nil
	}
}

func canonicalReference(kind ReferenceKind, id string) CanonicalReference {
	subject := "item"
	if kind == ReferenceKindRevision {
		subject = "revision"
	}
	return CanonicalReference{Kind: kind, RefID: id, URI: "tesseract://" + subject + "/" + id}
}

// ResolveReference normalizes one supported selector to Tesseract-owned
// identity. It performs metadata-only reads and never loads content, reinforces
// access, rotates a workspace token, or writes a revision.
func (s *Service) ResolveReference(ctx context.Context, selector ReferenceSelector) (ReferenceResolution, error) {
	kind, err := selector.kind()
	if err != nil {
		return ReferenceResolution{}, err
	}
	if kind == selectorURI {
		parsed, ok, parseErr := parseCanonicalURI(selector.URI)
		if parseErr != nil {
			return ReferenceResolution{}, parseErr
		}
		if !ok {
			return ReferenceResolution{Status: ResolutionUnsupportedReference}, nil
		}
		return s.ResolveReference(ctx, parsed)
	}

	now := time.Now().UTC()
	switch kind {
	case selectorItem:
		meta, lookupErr := s.lookupItemReference(ctx, selector.ItemID)
		if errors.Is(lookupErr, memory.ErrNotFound) {
			return ReferenceResolution{Status: ResolutionNotFound}, nil
		}
		if lookupErr != nil {
			return ReferenceResolution{}, lookupErr
		}
		ref := canonicalReference(ReferenceKindItem, meta.ItemID)
		status := ResolutionResolved
		if meta.Deleted {
			status = ResolutionDeleted
		}
		return ReferenceResolution{Status: status, Ref: &ref, ItemID: meta.ItemID, Domain: meta.Domain,
			ResolvedAt: &now, DeletedAt: meta.DeletedAt, Namespace: meta.Namespace}, nil

	case selectorRevision:
		if s.Revisions == nil {
			return ReferenceResolution{}, fmt.Errorf("%w: revision store", ErrBackendUnavailable)
		}
		meta, lookupErr := s.Revisions.LookupRevisionMetadata(ctx, selector.RevisionID)
		if errors.Is(lookupErr, memory.ErrNotFound) {
			return ReferenceResolution{Status: ResolutionNotFound}, nil
		}
		if lookupErr != nil {
			return ReferenceResolution{}, lookupErr
		}
		ref := canonicalReference(ReferenceKindRevision, meta.RevisionID)
		return ReferenceResolution{Status: ResolutionResolved, Ref: &ref, ItemID: meta.ItemID,
			Domain: string(meta.Domain), ResolvedAt: &now, Namespace: meta.Namespace}, nil

	case selectorKey:
		return s.resolveKeyReference(ctx, selector, now)
	default:
		return ReferenceResolution{}, fmt.Errorf("%w: unrecognized selector", ErrInvalidReference)
	}
}

func (s *Service) lookupItemReference(ctx context.Context, itemID string) (Metadata, error) {
	meta, err := s.LookupMetadata(ctx, itemID)
	if !errors.Is(err, memory.ErrNotFound) {
		return meta, err
	}
	// A negative identity lookup is conclusive only when both identity stores
	// participated. A hit from either one is conclusive on its own.
	if s.Revisions == nil || s.Workspace == nil {
		return Metadata{}, fmt.Errorf("%w: complete item identity lookup", ErrBackendUnavailable)
	}
	return Metadata{}, err
}

func (s *Service) resolveKeyReference(ctx context.Context, selector ReferenceSelector, now time.Time) (ReferenceResolution, error) {
	domain := domains.Domain(selector.Domain)
	if selector.Domain == workspace.Domain {
		if err := memory.ValidateWorkspaceNamespace(selector.Namespace); err != nil {
			return ReferenceResolution{}, fmt.Errorf("%w: %w", ErrInvalidReference, err)
		}
		if s.Workspace == nil {
			return ReferenceResolution{}, fmt.Errorf("%w: workspace store", ErrBackendUnavailable)
		}
		meta, err := s.Workspace.LookupMetadataByKey(ctx, selector.Namespace, selector.Key)
		if errors.Is(err, workspace.ErrNotFound) {
			return ReferenceResolution{Status: ResolutionNotFound}, nil
		}
		if err != nil {
			return ReferenceResolution{}, err
		}
		ref := canonicalReference(ReferenceKindItem, meta.ItemID)
		return ReferenceResolution{Status: ResolutionResolved, Ref: &ref, ItemID: meta.ItemID,
			Domain: meta.Domain, ResolvedAt: &now, Namespace: meta.Namespace}, nil
	}
	if !domain.Valid() {
		return ReferenceResolution{}, fmt.Errorf("%w: unsupported domain %q", ErrInvalidReference, selector.Domain)
	}
	var validationErr error
	switch domain {
	case domains.Memory:
		validationErr = memory.ValidateNamespace(selector.Namespace)
	case domains.Knowledge:
		validationErr = memory.ValidateKnowledgeNamespace(selector.Namespace)
	case domains.Event:
		validationErr = memory.ValidateEventNamespace(selector.Namespace)
	}
	if validationErr == nil && (domain == domains.Memory || domain == domains.Event) {
		validationErr = memory.ValidateKey(selector.Key)
	}
	if validationErr != nil {
		return ReferenceResolution{}, fmt.Errorf("%w: %w", ErrInvalidReference, validationErr)
	}
	if s.Revisions == nil {
		return ReferenceResolution{}, fmt.Errorf("%w: revision store", ErrBackendUnavailable)
	}
	meta, err := s.Revisions.LookupCurrentMetadataByKey(ctx, domain, selector.Namespace, selector.Key)
	if errors.Is(err, memory.ErrNotFound) {
		return ReferenceResolution{Status: ResolutionNotFound}, nil
	}
	if err != nil {
		return ReferenceResolution{}, err
	}
	ref := canonicalReference(ReferenceKindItem, meta.ItemID)
	return ReferenceResolution{Status: ResolutionResolved, Ref: &ref, ItemID: meta.ItemID,
		Domain: string(meta.Domain), ResolvedAt: &now, Namespace: meta.Namespace}, nil
}

func parseCanonicalURI(raw string) (ReferenceSelector, bool, error) {
	for _, candidate := range []struct {
		prefix string
		field  func(string) ReferenceSelector
	}{
		{"tesseract://item/", func(id string) ReferenceSelector { return ReferenceSelector{ItemID: id} }},
		{"tesseract://revision/", func(id string) ReferenceSelector { return ReferenceSelector{RevisionID: id} }},
	} {
		if !strings.HasPrefix(raw, candidate.prefix) {
			continue
		}
		id := strings.TrimPrefix(raw, candidate.prefix)
		if id == "" || strings.TrimSpace(id) != id || strings.ContainsAny(id, "/?#") {
			return ReferenceSelector{}, false, fmt.Errorf("%w: malformed canonical Tesseract URI", ErrInvalidReference)
		}
		return candidate.field(id), true, nil
	}
	return ReferenceSelector{}, false, nil
}
