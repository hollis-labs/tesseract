package memory

import (
	"encoding/json"
	"time"

	"github.com/hollis-labs/tesseract/domains"
)

// Origin categorizes why a memory exists (closed vocabulary, D6/D9).
type Origin string

const (
	OriginUser        Origin = "user"
	OriginFeedback    Origin = "feedback"
	OriginProject     Origin = "project"
	OriginReference   Origin = "reference"
	OriginObservation Origin = "observation"
)

// Valid reports whether o is one of the five canonical origin values.
func (o Origin) Valid() bool {
	switch o {
	case OriginUser, OriginFeedback, OriginProject, OriginReference, OriginObservation:
		return true
	}
	return false
}

// Status is the revision lifecycle state (D9).
type Status string

const (
	StatusDraft      Status = "draft"
	StatusReviewed   Status = "reviewed"
	StatusCanonical  Status = "canonical"
	StatusDeprecated Status = "deprecated"
)

// Valid reports whether s is a recognized revision lifecycle status.
func (s Status) Valid() bool {
	switch s {
	case StatusDraft, StatusReviewed, StatusCanonical, StatusDeprecated:
		return true
	}
	return false
}

// Trigger identifies the signal that caused a memory to be authored (D9).
type Trigger string

const (
	TriggerExplicit    Trigger = "explicit"
	TriggerPostCompact Trigger = "post_compact"
	TriggerPerTurn     Trigger = "per_turn"
	TriggerPromotion   Trigger = "promotion"
	TriggerManual      Trigger = "manual"
)

// Valid reports whether t is one of the five canonical trigger values.
func (t Trigger) Valid() bool {
	switch t {
	case TriggerExplicit, TriggerPostCompact, TriggerPerTurn, TriggerPromotion, TriggerManual:
		return true
	}
	return false
}

// Author identifies who wrote a memory revision.
type Author struct {
	AgentID      string `json:"agent_id"`
	AgentVersion string `json:"agent_version"`
}

// Payload is the structured-by-convention memory content (D9).
type Payload struct {
	Summary string `json:"summary"`
	Body    string `json:"body,omitempty"`
}

// Pointer identifies an external reference for knowledge revisions. Scheme
// names the locator scheme (file, http, https, obsidian, nil, ...) and
// Locator is the scheme-specific address. ResolvedAt records the last time
// the pointer was verified against the external source.
type Pointer struct {
	Scheme     string     `json:"scheme"`
	Locator    string     `json:"locator"`
	ResolvedAt *time.Time `json:"resolved_at,omitempty"`
}

// Facets are structured knowledge-domain attributes. WriteRevision requires
// all fields for knowledge revisions and rejects every non-zero field for
// memory revisions.
type Facets struct {
	Kind    string   `json:"kind,omitempty"`
	Source  string   `json:"source,omitempty"`
	Pointer *Pointer `json:"pointer,omitempty"`
}

// IsZero reports whether f carries no facet data.
func (f Facets) IsZero() bool {
	return f.Kind == "" && f.Source == "" && f.Pointer == nil
}

// Revision is an immutable memory revision. The only field that may be
// mutated after write is Status, and only via the deprecation code path.
type Revision struct {
	RevisionID string         `json:"revision_id"`
	MemoryID   string         `json:"memory_id"`
	Domain     domains.Domain `json:"domain"`
	Namespace  string         `json:"namespace"`
	MemoryKey  string         `json:"memory_key,omitempty"`
	Status     Status         `json:"status"`
	Supersedes string         `json:"supersedes,omitempty"`
	CreatedAt  time.Time      `json:"created_at"`
	Author     Author         `json:"author"`
	Trigger    Trigger        `json:"trigger"`
	SessionID  string         `json:"session_id"`
	Origin     Origin         `json:"origin"`
	Confidence float64        `json:"confidence"`
	Tags       []string       `json:"tags"`
	TTLSeconds int64          `json:"ttl_seconds,omitempty"`
	ExpiresAt  *time.Time     `json:"expires_at,omitempty"`
	Payload    Payload        `json:"payload"`
	Facets     Facets         `json:"facets,omitempty"`

	// ConsumerState is the consumer's own operational bag, stored as JSON in
	// memory_revisions.consumer_state (CW-20260909-0036).
	//
	// It is NOT the State struct below, and the distance between the two is
	// the whole reason this field is not called State. State is Tesseract's
	// mutable bookkeeping about an ENTRY — activation, access_count,
	// current_revision — and it moves on every read. This is an immutable
	// property of ONE REVISION, written by whoever wrote the revision, about
	// their workflow rather than about ours.
	//
	// Tesseract validates that it is well-formed JSON and an object, plus the
	// declaring type's required_fields, and never reads a value out of it.
	// See consumerstate.go for the discipline line and the test that holds it.
	ConsumerState   json.RawMessage `json:"consumer_state,omitempty"`
	EmbeddingModel  string          `json:"embedding_model,omitempty"`
	EmbeddingVector []float32       `json:"-"` // never serialized — BLG-20260416-037
	DedupMatch      string          `json:"dedup_match,omitempty"`
}

// State is the mutable per-memory state (D9). Lives in memory_state table.
//
// Not to be confused with Revision.ConsumerState, which was added later and
// deliberately not called State. This one is Tesseract's: it is scoped to a
// logical memory rather than to a revision, it is mutable, and activation and
// decay write to it. Revision.ConsumerState is the consumer's, is scoped to
// one revision, and Tesseract never reads a value out of it.
type State struct {
	MemoryID        string         `json:"memory_id"`
	Domain          domains.Domain `json:"domain"`
	Namespace       string         `json:"namespace"`
	MemoryKey       string         `json:"memory_key,omitempty"`
	CurrentRevision string         `json:"current_revision"`
	Activation      float64        `json:"activation"`
	AccessCount     int64          `json:"access_count"`
	LastAccessedAt  *time.Time     `json:"last_accessed_at,omitempty"`
	CreatedAt       time.Time      `json:"created_at"`
}
