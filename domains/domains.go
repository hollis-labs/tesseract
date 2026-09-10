// Package domains defines the in-tree domain discriminator for Tesseract
// revisions. A domain selects policy — namespace shape, key rules, facet
// requirements — while reusing the shared memory_state + memory_revisions
// storage.
//
// This package holds identity and nothing else: the constants, the membership
// test, and the enumeration. The DomainPolicy interface that carries each
// domain's behavior lives in internal/memory, next to the types it validates
// (namespaces, keys, facets) and next to its only consumer, the write path.
//
// The split is deliberate (CW-20260909-0033). Everything can import this
// package precisely because it knows nothing: contextapi, mcpadapter,
// knowledge and the public facade all need to name a Domain, and none of them
// should inherit memory's domain model in order to do it. Hoisting Facets and
// the namespace parser up here to keep the policy interface company would
// spend exactly the property that makes this package cheap to depend on.
//
// S1 ships three built-ins: Memory, Knowledge and Event. Plugin-extensible
// domains are deferred until plugin-sdk v2 GA.
package domains

// Domain identifies a revision's policy bucket. The zero value is invalid;
// callers must pass one of the built-in constants.
type Domain string

const (
	// Memory is the default domain for agent memory revisions (D-core).
	Memory Domain = "memory"

	// Knowledge is the reference domain (S1) — content addressed by key,
	// carrying kind/source/pointer facets. "Pointer-first" describes the facet
	// shape, not a requirement that the content live elsewhere: scheme `nil`
	// declares no external source and is the common case.
	Knowledge Domain = "knowledge"

	// Event is the append-only narrative log: an agent's reasoning about what
	// it is doing, and Chrispian's personal log and journal (CW-20260909-0035).
	//
	// Not telemetry. The distinguishing property is that an Event carries
	// reasoning in PROSE, which is exactly what a span or a metric discards —
	// so it wants embeddings and a linear read path, not aggregation.
	//
	// It is a domain rather than a registry type because every property it
	// implies is STORAGE policy: no activation decay, out of the default
	// ranked corpus, long retention, chronological read primary. Expressing
	// those as a type would hand the type registry authority over the storage
	// engine. Domain selects storage policy; type classifies within it.
	Event Domain = "event"
)

// registered is the single list of built-in domains. Both Valid and All read
// it, so a domain cannot be enumerable without being valid or vice versa.
// internal/memory's policy registry is held to this list by
// TestEveryDomainHasAPolicy, which is what makes a domain added here fail
// loudly rather than fall through a switch default.
var registered = []Domain{Memory, Knowledge, Event}

// Valid reports whether d is a recognized domain.
func (d Domain) Valid() bool {
	for _, c := range registered {
		if c == d {
			return true
		}
	}
	return false
}

// All returns the set of registered domains in a stable order. The slice is a
// copy; callers cannot reorder the registry by sorting what they are handed.
func All() []Domain {
	out := make([]Domain, len(registered))
	copy(out, registered)
	return out
}
