// Package memory re-exports the Conduit memory subsystem types for use by
// external consumers. The implementation lives in internal/memory; this
// package provides the public API surface.
//
// Create a Store via conduit.Open() and then conduit.MemoryStore().
package memory

import (
	internal "github.com/hollis-labs/vanta-conduit/internal/memory"
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

// RecallResult pairs a revision with its computed score and the parent state.
type RecallResult = internal.RecallResult

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

	ScopeUnknown = internal.ScopeUnknown
	ScopeUser    = internal.ScopeUser
	ScopeProject = internal.ScopeProject
	ScopeSession = internal.ScopeSession
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
