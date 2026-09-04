// Package domains defines the in-tree domain discriminator and per-domain
// policy interface for Tesseract revisions. A domain selects policy
// (namespace shape, facet requirements, default status, decay rules) while
// reusing the shared memory_state + memory_revisions storage.
//
// S1 ships two built-ins: MemoryDomain and KnowledgeDomain. Plugin-extensible
// domains are deferred until plugin-sdk v2 GA.
package domains

import (
	"fmt"
	"strings"
)

// Domain identifies a revision's policy bucket. The zero value is invalid;
// callers must pass one of the built-in constants.
type Domain string

const (
	// Memory is the default domain for agent memory revisions (D-core).
	Memory Domain = "memory"

	// Knowledge is the pointer-first external reference domain (S1).
	Knowledge Domain = "knowledge"
)

// Valid reports whether d is a recognized domain.
func (d Domain) Valid() bool {
	_, ok := registry[d]
	return ok
}

// Policy returns the DomainPolicy for d, or an error if unknown.
func (d Domain) Policy() (DomainPolicy, error) {
	p, ok := registry[d]
	if !ok {
		return nil, fmt.Errorf("unknown domain %q", d)
	}
	return p, nil
}

// DomainPolicy is the in-tree interface each domain implements. Impls are
// kept small and deterministic — no hidden mutation, no I/O.
type DomainPolicy interface {
	// Name returns the canonical domain identifier.
	Name() Domain

	// ValidateNamespace reports whether ns is allowed under this domain.
	// The memory-domain policy accepts the legacy namespace shapes; the
	// knowledge-domain policy requires the "/knowledge" segment.
	ValidateNamespace(ns string) error
}

// memoryPolicy accepts any non-empty namespace (validation happens upstream
// in internal/memory/namespaces.go). S1 keeps this permissive to preserve
// existing behavior.
type memoryPolicy struct{}

func (memoryPolicy) Name() Domain { return Memory }

func (memoryPolicy) ValidateNamespace(ns string) error {
	if ns == "" {
		return fmt.Errorf("namespace is required")
	}
	return nil
}

// knowledgePolicy requires a namespace segment of exactly "knowledge". Shape:
// user/{user}/knowledge/{...} or app/{app}/knowledge/{...}. Facet validation
// lives at the shared revision persistence boundary, not in namespace policy.
type knowledgePolicy struct{}

func (knowledgePolicy) Name() Domain { return Knowledge }

func (knowledgePolicy) ValidateNamespace(ns string) error {
	if ns == "" {
		return fmt.Errorf("namespace is required")
	}
	if strings.HasSuffix(ns, "/") {
		return fmt.Errorf("knowledge namespace must not have a trailing slash: %q", ns)
	}
	segs := strings.Split(ns, "/")
	if len(segs) < 3 {
		return fmt.Errorf("knowledge namespace must have shape {user|app}/{id}/knowledge[/...], got %q", ns)
	}
	for _, s := range segs {
		if s == "" {
			return fmt.Errorf("knowledge namespace must not contain empty segments: %q", ns)
		}
	}
	if segs[0] != "user" && segs[0] != "app" {
		return fmt.Errorf("knowledge namespace must begin with 'user/' or 'app/', got %q", segs[0])
	}
	if segs[2] != "knowledge" {
		return fmt.Errorf("knowledge namespace third segment must be 'knowledge', got %q in %q", segs[2], ns)
	}
	return nil
}

var registry = map[Domain]DomainPolicy{
	Memory:    memoryPolicy{},
	Knowledge: knowledgePolicy{},
}

// All returns the set of registered domains in a stable order.
func All() []Domain {
	return []Domain{Memory, Knowledge}
}
