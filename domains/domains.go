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
// S1 ships two built-ins: Memory and Knowledge. Plugin-extensible domains are
// deferred until plugin-sdk v2 GA.
package domains

// Domain identifies a revision's policy bucket. The zero value is invalid;
// callers must pass one of the built-in constants.
type Domain string

const (
	// Memory is the default domain for agent memory revisions (D-core).
	Memory Domain = "memory"

	// Knowledge is the pointer-first external reference domain (S1).
	Knowledge Domain = "knowledge"
)

// registered is the single list of built-in domains. Both Valid and All read
// it, so a domain cannot be enumerable without being valid or vice versa.
// internal/memory's policy registry is held to this list by
// TestEveryDomainHasAPolicy, which is what makes a domain added here fail
// loudly rather than fall through a switch default.
var registered = []Domain{Memory, Knowledge}

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
