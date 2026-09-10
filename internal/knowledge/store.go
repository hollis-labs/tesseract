// Package knowledge implements the Knowledge domain on top of the shared
// memory revision store.
//
// A knowledge entry is content addressed by key, written to be gone back to: a
// project's canonical, a handoff, a playbook, an investigation dossier, a doc
// or package reference. It carries a summary, an optional body and structured
// kind/source/pointer facets for search.
//
// The pointer names an external source WHERE THERE IS ONE, and scheme `nil`
// declares there is none — the common case, since most of the corpus is
// agent-authored. The domain was described as "pointer-first references to
// external content" until 2026-09-10, which was false for `investigation`,
// `session_close` and `project_canonical`. See
// docs/knowledge-memory-boundary.md.
package knowledge

import (
	"context"
	"encoding/json"
	"time"

	"github.com/hollis-labs/tesseract/domains"
	"github.com/hollis-labs/tesseract/internal/memory"
)

// Store is a thin wrapper over *memory.Store that supplies knowledge defaults,
// fixes Domain=Knowledge, and provides a narrower write API than raw
// memory.WriteInput. The underlying memory.Store is authoritative for required
// facets and the closed kind vocabulary.
type Store struct {
	mem *memory.Store
}

// New returns a knowledge Store backed by ms.
func New(ms *memory.Store) *Store {
	return &Store{mem: ms}
}

// WriteInput is the knowledge-write payload. All facet fields are required.
//
// Kind is validated against the closed vocabulary in
// memory.KnowledgeKindVocabulary — an unknown kind is rejected here rather
// than left to caller discipline. Source remains a free-form-but-controlled
// string; upstream callers are expected to pick from a conventional set.
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

	// ConsumerState is the writer's operational JSON bag for this entry
	// (CW-20260909-0036). Optional and usually absent — a knowledge entry is a
	// thing you come back for by name, and most of them have no lifecycle
	// beyond the epistemic one `status` already carries.
	ConsumerState json.RawMessage
}

// Write applies knowledge-specific defaults and forwards to the underlying
// memory.Store with Domain=domains.Knowledge. Returns the created revision
// (Domain will equal domains.Knowledge, Facets populated).
func (s *Store) Write(ctx context.Context, in WriteInput) (memory.Revision, error) {
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
		ConsumerState: in.ConsumerState,
	}
	return s.mem.WriteRevision(ctx, memIn)
}

// GetCurrent returns the current revision for the knowledge entry keyed by
// (namespace, key). Returns memory.ErrNotFound if the revision exists but
// is not in the knowledge domain — callers should not see cross-domain reads.
//
// The filter lives in memory.Store rather than here so that the memory domain
// gets the identical rule from the identical code. It did not, for a while, and
// the asymmetry was a defect: a memory-domain read of a knowledge namespace
// returned the knowledge revision and reinforced it. Consolidating removes the
// drift between the two; it does not by itself make the shared rule right, so
// both domains are asserted separately against hand-stated expectations in
// internal/mcpadapter/crossdomain_parity_test.go.
func (s *Store) GetCurrent(ctx context.Context, namespace, key string) (memory.Revision, error) {
	return s.mem.GetCurrentInDomain(ctx, domains.Knowledge, namespace, key)
}

// GetCurrentReinforced is GetCurrent plus the deliberate-read activation bump,
// mirroring memory.Store.GetCurrentReinforced. It is the entry point behind
// tesseract_get's knowledge arm and GET /v1/knowledge/current.
//
// The pair exists for the same reason memory's does: the call site knows
// whether a read is a deliberate agent consultation or internal bookkeeping,
// and only the former is a use signal. What the call site does NOT decide is
// whether knowledge reinforces at all — reinforceMemoryIDs gates that on the
// domain policy, in SQL. Calling the reinforcing variant from a domain that
// opted out bumps nothing.
//
// That split is the CW-20260910-0021 repair. Both agent-facing knowledge reads
// called plain GetCurrent while their memory twins called the reinforcing
// getter, so knowledge decayed with no path to lift it. Adding the variant
// alone would have re-created a call-site choice; the policy gate is what makes
// it a domain property.
func (s *Store) GetCurrentReinforced(ctx context.Context, namespace, key string) (memory.Revision, error) {
	return s.mem.GetCurrentInDomainReinforced(ctx, domains.Knowledge, namespace, key)
}

// GetHistory returns the revision history for the knowledge entry keyed by
// (namespace, key), newest-first. Non-knowledge revisions are filtered out;
// returns memory.ErrNotFound if the entry exists but has no knowledge revisions.
func (s *Store) GetHistory(ctx context.Context, namespace, key string) ([]memory.Revision, error) {
	return s.mem.GetHistoryInDomain(ctx, domains.Knowledge, namespace, key)
}

// RevisionStore returns the shared revision store this knowledge store is
// built on.
//
// Memory and knowledge revisions live in one table (memory_revisions, keyed by
// a domain column), so revision-level operations — fetch by revision_id,
// deprecate by revision_id — resolve either domain without a domain filter.
// This accessor is what lets a deployment that wires only a knowledge store
// still perform those operations, rather than having them gated behind a
// separate memory-store field that happens to be nil.
func (s *Store) RevisionStore() *memory.Store { return s.mem }
