// Package event implements the Event domain on top of the shared memory
// revision store. An event is a narrative log entry: an agent's reasoning
// about what it is doing, or Chrispian's personal log and journal
// (CW-20260909-0035).
//
// It is NOT telemetry. Spans and metrics exist and are someone else's job; the
// distinguishing property of an Event is that it carries reasoning in prose,
// which is exactly what a trace discards. If a caller reaching for this
// package is trying to record a duration, a count or a structured span, it
// wants a metrics pipeline, not this.
//
// The domain's storage policy is what makes it a domain rather than a registry
// type, and none of it lives here — it lives in memory.eventPolicy: no
// activation decay, out of the default recall corpus, memory's namespace
// grammar with `event` in the domain-segment position. This package is the
// narrow write API and the log read, in the shape internal/knowledge already
// established.
package event

import (
	"context"
	"encoding/json"
	"time"

	"github.com/hollis-labs/tesseract/domains"
	"github.com/hollis-labs/tesseract/internal/memory"
)

// Store is a thin wrapper over *memory.Store that supplies event defaults,
// fixes Domain=Event, and provides a narrower write API than raw
// memory.WriteInput. The underlying memory.Store stays authoritative for the
// namespace grammar and every other invariant.
type Store struct {
	mem *memory.Store
}

// New returns an event Store backed by ms.
func New(ms *memory.Store) *Store {
	return &Store{mem: ms}
}

// WriteInput is the event-write payload.
//
// Key is optional and usually absent, which is the shape that distinguishes
// this write from every other one in the tree. A memory or a knowledge entry
// is a thing you revise: it has an identity, and a later write supersedes an
// earlier one at that identity. A log entry is a thing that HAPPENED. It has a
// timestamp, not a current value, and superseding it would be rewriting the
// record of what an agent was thinking — which is the one thing a reasoning
// log must not permit casually. Keyless writes get a fresh memory_id per
// entry, so the append-only store's grain matches the domain's.
//
// A key is still accepted for the case where an entry genuinely does have a
// stable identity worth revising — a running journal page for a day, say — and
// it is held to memory's dot-notation vocabulary rather than waved through the
// way knowledge's externally-sourced slugs are.
type WriteInput struct {
	Namespace string
	Key       string

	// Summary is required; Body optional. Both feed embeddings.
	//
	// Embeddings are ON for this domain, deliberately and against an earlier
	// guess that a log wants none. Recovering the WHY of a past decision is a
	// semantic question — "what was I thinking when I chose this" has no
	// keyword — and that recovery is the entire reason the narrative is stored
	// instead of discarded with the trace.
	Summary string
	Body    string

	// Data is the consumer's own object for this record, stored verbatim and
	// never interpreted. DataSchemaHash is the caller's claim about which
	// schema it follows, recorded and never checked. See
	// internal/memory/payloaddata.go for the contract.
	Data           json.RawMessage
	DataSchemaHash string

	// Authorship + trace metadata.
	Author    memory.Author
	SessionID string

	// Optional knobs.
	Tags        []string
	TTL         time.Duration
	Confidence  float64
	DerivedFrom memory.DerivedFrom
	Trigger     memory.Trigger
	Supersedes  string

	// ConsumerState is the writer's operational JSON bag for this entry
	// (CW-20260909-0036). Optional and usually absent.
	//
	// Wired here rather than left to the memory domain because the Event
	// definition asked for it by name: "an event has consumer-meaningful state
	// distinct from the epistemic draft|reviewed|canonical|deprecated ladder"
	// ([[tesseract_event_domain_definition]]). A friction log marking an entry
	// resolved, a journal page marking a day closed — neither is a claim about
	// how settled the entry is, which is all `status` can say.
	ConsumerState json.RawMessage
}

// Write applies event defaults and forwards to the underlying memory.Store
// with Domain=domains.Event.
func (s *Store) Write(ctx context.Context, in WriteInput) (memory.Revision, error) {
	confidence := in.Confidence
	if confidence == 0 {
		// A log entry is a report of what happened, not a claim about the
		// world, so the default is high and means "this is what I observed"
		// rather than "I am sure this is true". Validation still enforces
		// [0, 1.0] and callers recording a hedge can lower it.
		confidence = 0.9
	}

	derivedFrom := in.DerivedFrom
	if derivedFrom == "" {
		// `observation` is the closest bucket in the existing vocabulary: an
		// event is something the author noticed or did, reported first-hand.
		derivedFrom = memory.DerivedFromObservation
	}

	trigger := in.Trigger
	if trigger == "" {
		trigger = memory.TriggerManual
	}

	memIn := memory.WriteInput{
		Domain:     domains.Event,
		Namespace:  in.Namespace,
		MemoryKey:  in.Key,
		Supersedes: in.Supersedes,
		// StatusCanonical, not the StatusDraft every other write path defaults
		// to. The draft → reviewed → canonical ladder is an EPISTEMIC one: it
		// tracks how settled a claim is. A log entry makes no claim to settle —
		// it records that something happened, and it was as true the moment it
		// was written as it will ever be. Landing events in draft would put the
		// whole log permanently on the bottom rung of a ladder it is not
		// climbing.
		Status:      memory.StatusCanonical,
		Author:      in.Author,
		Trigger:     trigger,
		SessionID:   in.SessionID,
		DerivedFrom: derivedFrom,
		Confidence:  confidence,
		Tags:        in.Tags,
		TTL:         in.TTL,
		Payload: memory.Payload{
			Summary:        in.Summary,
			Body:           in.Body,
			Data:           in.Data,
			DataSchemaHash: in.DataSchemaHash,
		},
		ConsumerState: in.ConsumerState,
	}
	return s.mem.WriteRevision(ctx, memIn)
}

// ReadLog returns one window of the event log in chronological order. See
// memory.Store.ReadEventLog for why this is a dedicated read path rather than
// recall with ranking=chronological.
func (s *Store) ReadLog(ctx context.Context, in memory.EventLogInput) (memory.EventLogPage, error) {
	return s.mem.ReadEventLog(ctx, in)
}

// GetCurrent returns the current revision for the event entry keyed by
// (namespace, key). Returns memory.ErrNotFound if the entry exists but is not
// in the event domain — callers must not see cross-domain reads.
//
// Most events are keyless and unreachable this way by design; this serves the
// keyed minority and tesseract_get's event arm.
func (s *Store) GetCurrent(ctx context.Context, namespace, key string) (memory.Revision, error) {
	return s.mem.GetCurrentInDomain(ctx, domains.Event, namespace, key)
}

// GetHistory returns the revision history for the event entry keyed by
// (namespace, key), newest-first.
//
// There is deliberately no GetCurrentReinforced twin, and its absence is the
// point rather than an omission. Memory and knowledge have one because a
// deliberate read is a use signal that lifts activation; Event opts out of
// activation entirely, so a reinforcing variant here would be a method that
// reads identically and bumps nothing — a call site holding a choice the
// domain policy has already made, which is exactly the shape CW-20260910-0021
// removed. Calling memory's reinforcing getter on an event row is already a
// no-op because reinforceMemoryIDs gates on the domain in SQL; not offering a
// second door to it keeps the reason visible.
func (s *Store) GetHistory(ctx context.Context, namespace, key string) ([]memory.Revision, error) {
	return s.mem.GetHistoryInDomain(ctx, domains.Event, namespace, key)
}

// RevisionStore returns the shared revision store this event store is built
// on. Same rationale as knowledge.Store.RevisionStore: memory, knowledge and
// event revisions live in one table, so revision-level operations resolve any
// of them without a domain filter.
func (s *Store) RevisionStore() *memory.Store { return s.mem }
