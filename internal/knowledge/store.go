// Package knowledge implements the Knowledge domain on top of the shared
// memory revision store. Knowledge entries are pointer-first references to
// external content (packages, docs, notes) — the original source remains
// authoritative; Conduit holds a summary + optional body and structured
// facets for search.
package knowledge

import (
	"context"
	"fmt"
	"time"

	"github.com/hollis-labs/vanta-conduit/domains"
	"github.com/hollis-labs/vanta-conduit/internal/memory"
)

// Store is a thin wrapper over *memory.Store that enforces knowledge-domain
// invariants (required facets, Domain=Knowledge) and provides a narrower
// write API than the raw memory.WriteInput.
type Store struct {
	mem *memory.Store
}

// New returns a knowledge Store backed by ms.
func New(ms *memory.Store) *Store {
	return &Store{mem: ms}
}

// WriteInput is the knowledge-write payload. All facet fields are required.
// Kind/Source are free-form-but-controlled strings at this layer; upstream
// callers (indexers, MCP tool) are expected to pick from a closed vocabulary.
type WriteInput struct {
	Namespace string
	Key       string

	// Facets are required on every knowledge write.
	Kind    string
	Source  string
	Pointer memory.Pointer

	// Summary is required; Body optional. Both feed embeddings.
	Summary string
	Body    string

	// Authorship + trace metadata.
	Author    memory.Author
	SessionID string

	// Optional knobs.
	Tags       []string
	TTL        time.Duration
	Confidence float64
	Supersedes string
}

// Write validates the knowledge-specific invariants and forwards to the
// underlying memory.Store with Domain=domains.Knowledge. Returns the created
// revision (Domain will equal domains.Knowledge, Facets populated).
func (s *Store) Write(ctx context.Context, in WriteInput) (memory.Revision, error) {
	if in.Kind == "" {
		return memory.Revision{}, fmt.Errorf("%w: facet.kind is required", memory.ErrInvalidInput)
	}
	if in.Source == "" {
		return memory.Revision{}, fmt.Errorf("%w: facet.source is required", memory.ErrInvalidInput)
	}
	if in.Pointer.Scheme == "" || in.Pointer.Locator == "" {
		return memory.Revision{}, fmt.Errorf("%w: facet.pointer.scheme and facet.pointer.locator are required", memory.ErrInvalidInput)
	}
	if in.Summary == "" {
		return memory.Revision{}, fmt.Errorf("%w: payload.summary is required", memory.ErrInvalidInput)
	}

	confidence := in.Confidence
	if confidence == 0 {
		// Ingested references default to high confidence; downstream callers
		// may override. Validation still enforces [0, 1.0].
		confidence = 0.9
	}

	pointer := in.Pointer
	if pointer.ResolvedAt == nil {
		now := time.Now().UTC()
		pointer.ResolvedAt = &now
	}

	memIn := memory.WriteInput{
		Domain:     domains.Knowledge,
		Namespace:  in.Namespace,
		MemoryKey:  in.Key,
		Supersedes: in.Supersedes,
		Status:     memory.StatusCanonical,
		Author:     in.Author,
		// Knowledge writes originate from indexers or manual capture; use
		// `reference` as the closest origin bucket and `manual` as the
		// generic trigger. Indexer plugins will refine this later.
		Trigger:    memory.TriggerManual,
		SessionID:  in.SessionID,
		Origin:     memory.OriginReference,
		Confidence: confidence,
		Tags:       in.Tags,
		TTL:        in.TTL,
		Payload: memory.Payload{
			Summary: in.Summary,
			Body:    in.Body,
		},
		Facets: memory.Facets{
			Kind:    in.Kind,
			Source:  in.Source,
			Pointer: &pointer,
		},
	}
	return s.mem.WriteRevision(ctx, memIn)
}

// GetCurrent returns the current revision for the knowledge entry keyed by
// (namespace, key). Returns memory.ErrNotFound if the revision exists but
// is not in the knowledge domain — callers should not see cross-domain reads.
func (s *Store) GetCurrent(ctx context.Context, namespace, key string) (memory.Revision, error) {
	rev, err := s.mem.GetCurrent(ctx, namespace, key)
	if err != nil {
		return memory.Revision{}, err
	}
	if rev.Domain != domains.Knowledge {
		return memory.Revision{}, fmt.Errorf("%w: revision at %s/%s is not a knowledge entry", memory.ErrNotFound, namespace, key)
	}
	return rev, nil
}

// GetHistory returns the revision history for the knowledge entry keyed by
// (namespace, key), newest-first. Non-knowledge revisions are filtered out;
// returns memory.ErrNotFound if the entry exists but has no knowledge revisions.
func (s *Store) GetHistory(ctx context.Context, namespace, key string) ([]memory.Revision, error) {
	revs, err := s.mem.GetHistory(ctx, namespace, key)
	if err != nil {
		return nil, err
	}
	out := make([]memory.Revision, 0, len(revs))
	for _, rev := range revs {
		if rev.Domain == domains.Knowledge {
			out = append(out, rev)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: no knowledge revisions for %s/%s", memory.ErrNotFound, namespace, key)
	}
	return out, nil
}
