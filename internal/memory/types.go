package memory

import (
	"encoding/json"
	"time"

	"github.com/hollis-labs/tesseract/domains"
)

// DerivedFrom records WHAT KIND OF INPUT CAUSED A REVISION TO EXIST (closed
// vocabulary, D6/D9).
//
// It was called Origin until 2026-09-12. The word read as *who originated
// this*, so `origin: "user"` got filled in as an authorship or approval claim
// by agents who had merely been talking to a person. `derived_from` states the
// relationship instead — derived from an observation, from the user, from
// feedback, from a reference. Chrispian chose it knowing `derived_from:
// project` is the awkward one of the five. The VALUES did not change.
//
// The rename was worth a breaking change because this field is not inert
// bookkeeping: it is a direct multiplier on the recall score in both ranking
// modes that weight — see derivedFromWeights in ranking.go, applied in
// activationScore and in the relevance path. A value chosen by vibe does not
// merely mislabel a revision, it moves it up or down the results a later
// session reads.
//
// The spread is 1.3 to 0.8, and the ratio is what matters: a record stamped
// `user` outranks the same record stamped `observation` by 1.375x with
// everything else held equal.
//
//	feedback     1.3   a correction, or a standing instruction about how to work
//	user         1.1   a person ruled it
//	project      1.0   a property of a codebase or project
//	reference    0.9   see the note below
//	observation  0.8   the author noticed or measured it
//
// ON THE AUTHORITY OF THOSE ONE-LINERS. The values have been a closed
// vocabulary with no per-value definition anywhere in this repo since they were
// declared, which is most of why they get chosen by name alone. Two are pinned
// by code — knowledge.Store.Write stamps DerivedFromReference unconditionally,
// and event defaults to DerivedFromObservation — and the rest are DESCRIBED
// FROM SETTLED USE rather than specified: measured 2026-09-12 over 2022
// memory-domain revisions, `feedback` carries corrections and working
// instructions ("subagents given a read-only prompt implement anyway"), and
// `project` carries facts about a thing ("this repo has one unpushed commit").
// Those readings are consistent across the corpus, and they are still a
// reading.
//
// DerivedFromReference is the one to be careful with, and on the KNOWLEDGE
// domain it is not a choice at all: knowledge.Store.Write hard-codes it for
// every write, so all 200 current knowledge entries carry it across all 11
// kinds. The field is a constant there — it carries no information, and every
// knowledge entry takes the uniform 0.9 weight regardless of content, which
// nobody chose. That is CW-20260912-0012's subject, deliberately not fixed
// here. On the memory surface it is merely unsettled: 22 revisions carry it, 4
// under a namespace type retired in 2026-09. Treat a memory revision stamped
// `reference` as unclassified rather than as meaning something specific.
type DerivedFrom string

const (
	DerivedFromUser        DerivedFrom = "user"
	DerivedFromFeedback    DerivedFrom = "feedback"
	DerivedFromProject     DerivedFrom = "project"
	DerivedFromReference   DerivedFrom = "reference"
	DerivedFromObservation DerivedFrom = "observation"
)

// Valid reports whether o is one of the five canonical derived_from values.
func (o DerivedFrom) Valid() bool {
	switch o {
	case DerivedFromUser, DerivedFromFeedback, DerivedFromProject, DerivedFromReference, DerivedFromObservation:
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
//
// Summary and Body are PROSE and always have been: they are what recall reads,
// what the embedder embeds, and what FTS indexes. Data is the third member and
// is none of those things — see payloaddata.go for the contract, which is the
// part of this that is easy to erode.
type Payload struct {
	Summary string `json:"summary"`
	Body    string `json:"body,omitempty"`

	// Data is the consumer's own object: an ADR's fields, a contact's fields,
	// a bug report's fields. Shaped for THEM, stored verbatim, never
	// interpreted. Tesseract checks that it parses and that it is an object,
	// and nothing else — no typing, no schema enforcement, no required keys,
	// no indexing, and no code path reads a value out of it.
	//
	// json.RawMessage rather than map[string]any deliberately: a map round
	// trip reorders keys and pushes every number through float64, so a
	// consumer's 64-bit id would come back changed. "Stored verbatim" has to
	// mean the bytes.
	Data json.RawMessage `json:"data,omitempty"`

	// DataSchemaHash is the caller's CLAIM about which schema Data was written
	// against — a hex sha256 matching a typeregistry.SchemaRef.SchemaHash.
	//
	// Tesseract never opens the schema, never parses it and never validates
	// Data against it. It records what the writer said so that drift is
	// detectable later: without the hash on the revision, a record written
	// under an older schema is indistinguishable from one that drifted.
	//
	// Absent means the caller made no claim, and it is NEVER defaulted from
	// the type's current SchemaRef. Stamping the current hash onto a write
	// that claimed nothing would manufacture exactly the evidence this field
	// exists to provide — and per AGENTS.md, "a default is indistinguishable
	// from a choice in the audit log."
	DataSchemaHash string `json:"data_schema_hash,omitempty"`
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
	RevisionID  string         `json:"revision_id"`
	MemoryID    string         `json:"memory_id"`
	Domain      domains.Domain `json:"domain"`
	Namespace   string         `json:"namespace"`
	MemoryKey   string         `json:"memory_key,omitempty"`
	Status      Status         `json:"status"`
	Supersedes  string         `json:"supersedes,omitempty"`
	CreatedAt   time.Time      `json:"created_at"`
	Author      Author         `json:"author"`
	Trigger     Trigger        `json:"trigger"`
	SessionID   string         `json:"session_id"`
	DerivedFrom DerivedFrom    `json:"derived_from"`
	Confidence  float64        `json:"confidence"`
	Tags        []string       `json:"tags"`
	TTLSeconds  int64          `json:"ttl_seconds,omitempty"`
	ExpiresAt   *time.Time     `json:"expires_at,omitempty"`
	Payload     Payload        `json:"payload"`
	Facets      Facets         `json:"facets,omitempty"`

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
