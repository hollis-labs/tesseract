// Package memory re-exports the Tesseract memory subsystem types for use by
// external consumers. The implementation lives in internal/memory; this
// package provides the public API surface.
//
// External consumers should use the exported types, constants, errors, and
// helpers in this package as the public memory API surface. The Tesseract
// facade (tesseract.Open()) returns *internal/memory.Store types; those are
// assignable to memory.Store via the Store alias below.
package memory

import (
	"github.com/hollis-labs/tesseract/domains"
	internal "github.com/hollis-labs/tesseract/internal/memory"
)

// ---- Type aliases (zero-cost re-exports) ----

// Store is the memory subsystem's storage handle.
type Store = internal.Store

// Revision is an immutable memory revision.
type Revision = internal.Revision

// State is the mutable per-memory state.
type State = internal.State

// Author identifies who wrote a memory revision.
type Author = internal.Author

// Payload is the structured memory content.
type Payload = internal.Payload

// Domain identifies a revision's policy bucket (memory, knowledge, ...).
type Domain = domains.Domain

// Pointer is an external reference stored on a knowledge revision.
type Pointer = internal.Pointer

// Facets are structured knowledge-domain attributes (kind, source, pointer).
type Facets = internal.Facets

// Origin categorizes why a memory exists.
type Origin = internal.Origin

// Status is the revision lifecycle state.
type Status = internal.Status

// Trigger identifies the signal that caused a memory to be authored.
type Trigger = internal.Trigger

// WriteInput carries all fields for a new revision write.
type WriteInput = internal.WriteInput

// RecallInput carries parameters for a recall query.
type RecallInput = internal.RecallInput

// RecallFilters constrains which revisions are returned.
type RecallFilters = internal.RecallFilters

// RecallResult pairs a revision with its ranking score and the parent state.
// It serializes as {revision, score, state}.
//
// Score is ranking-relative: comparable only against other results in the
// same response, never across responses or across ranking modes. Its units
// differ per mode —
//
//	activation     activation strength (recency x reinforcement x confidence)
//	similarity     cosine similarity between query and revision embeddings
//	relevance      RRF-fused BM25 + cosine, weighted by status/origin/activation
//	chronological  nil — no score
//
// Under chronological ranking the field is absent rather than set to a sort
// key: ordering is already carried by slice order plus Revision.CreatedAt.
// Score is a pointer so that "no score" stays distinguishable from a real
// zero — cosine similarity is legitimately 0 (orthogonal) or negative
// (opposite), so callers must nil-check before dereferencing.
type RecallResult = internal.RecallResult

// PayloadMode controls how much of each recall result is serialized:
// keys (identity only), summary (identity + payload.summary), or full.
type PayloadMode = internal.PayloadMode

// ProjectedResult is the wire shape of a recall hit under keys or summary
// projection. It always carries PayloadMode, so an absent body is
// distinguishable from a body that is genuinely empty.
type ProjectedResult = internal.ProjectedResult

// ProjectedRevision is the identity-and-triage subset of a Revision that
// survives keys and summary projection.
type ProjectedRevision = internal.ProjectedRevision

// Ranking determines how recall results are ordered.
type Ranking = internal.Ranking

// RevisionScope controls whether recall returns only the current revision
// per memory or all revisions.
type RevisionScope = internal.RevisionScope

// PromoteInput carries parameters for promoting a memory.
type PromoteInput = internal.PromoteInput

// JobQueue is the interface for background job processing.
type JobQueue = internal.JobQueue

// Job is a background job payload.
type Job = internal.Job

// NoopQueue is a JobQueue that silently discards all jobs.
type NoopQueue = internal.NoopQueue

// Scope is the memory namespace scope.
type Scope = internal.Scope

// Namespace is a parsed memory namespace.
type Namespace = internal.Namespace

// ---- Re-exported constants ----

const (
	OriginUser        = internal.OriginUser
	OriginFeedback    = internal.OriginFeedback
	OriginProject     = internal.OriginProject
	OriginReference   = internal.OriginReference
	OriginObservation = internal.OriginObservation

	StatusDraft      = internal.StatusDraft
	StatusReviewed   = internal.StatusReviewed
	StatusCanonical  = internal.StatusCanonical
	StatusDeprecated = internal.StatusDeprecated

	TriggerExplicit    = internal.TriggerExplicit
	TriggerPostCompact = internal.TriggerPostCompact
	TriggerPerTurn     = internal.TriggerPerTurn
	TriggerPromotion   = internal.TriggerPromotion
	TriggerManual      = internal.TriggerManual

	RankingActivation    = internal.RankingActivation
	RankingChronological = internal.RankingChronological
	RankingSimilarity    = internal.RankingSimilarity

	RevisionScopeCurrent  = internal.RevisionScopeCurrent
	RevisionScopeTimeline = internal.RevisionScopeTimeline

	PayloadModeKeys    = internal.PayloadModeKeys
	PayloadModeSummary = internal.PayloadModeSummary
	PayloadModeFull    = internal.PayloadModeFull
	DefaultPayloadMode = internal.DefaultPayloadMode

	ScopeUnknown = internal.ScopeUnknown
	ScopeUser    = internal.ScopeUser
	ScopeProject = internal.ScopeProject
	ScopeSession = internal.ScopeSession

	DomainMemory    = domains.Memory
	DomainKnowledge = domains.Knowledge
)

// ---- Re-exported sentinel errors ----

var (
	ErrInvalidInput          = internal.ErrInvalidInput
	ErrNotFound              = internal.ErrNotFound
	ErrSimilarityUnavailable = internal.ErrSimilarityUnavailable
	ErrInvalidNamespace      = internal.ErrInvalidNamespace
	ErrInvalidKey            = internal.ErrInvalidKey
)

// ---- Re-exported functions ----

// ValidateNamespace checks a namespace string.
var ValidateNamespace = internal.ValidateNamespace

// ValidateKey checks a memory key.
var ValidateKey = internal.ValidateKey

// ParseNamespace parses a memory namespace string.
var ParseNamespace = internal.ParseNamespace

// ProjectResults renders recall results under a payload mode for
// serialization. Full mode returns the results unchanged.
var ProjectResults = internal.ProjectResults
