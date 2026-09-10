// Package memory — per-domain policy.
//
// This file is the answer to "what does this domain do", in one place. Before
// CW-20260909-0033 the answer was spread across if/switch sites in write.go
// and read.go, each testing `domain == domains.Memory` inline. That shape had
// one failure mode worth removing: a domain added to the registry without a
// matching arm did not fail, it took the default branch — silently getting
// another domain's rules.
//
// The interface lives here rather than in the domains package because every
// method names a type that lives here: namespaces, keys and facets are
// memory's domain model. The domains package stays a leaf holding identity
// alone, so the layers that only need to name a Domain can keep importing it
// for free. See the package comment on domains for that trade.
package memory

import (
	"fmt"
	"strings"

	"github.com/hollis-labs/tesseract/domains"
)

// DomainPolicy is the in-tree behavior contract each domain implements. Impls
// are kept small and deterministic — no hidden mutation, no I/O — which is
// also why store selection stays at the adapter layer: picking MemoryStore or
// KnowledgeStore is dependency wiring, not policy, and does not belong here.
//
// Every method is a rule that genuinely differs per domain. Rules that are
// uniform across domains are deliberately absent: activation decay,
// reinforcement on read and the default status are the same for every domain
// today, and adding methods for them would be inventing behavior rather than
// relocating it. See CW-20260910-0021, which settles per-domain activation
// with a concrete second domain in hand.
type DomainPolicy interface {
	// Name returns the canonical domain identifier.
	Name() domains.Domain

	// ValidateNamespace reports whether ns is allowed under this domain.
	ValidateNamespace(ns string) error

	// ValidateKey reports whether key is a well-formed memory_key under this
	// domain. Domains that accept externally-sourced keys return nil for
	// everything. Both the write path and the read path's not-found diagnosis
	// consult this, so a key rejected on write is the same key explained on
	// read.
	ValidateKey(key string) error

	// ValidateFacets enforces the domain/facet contract. Implementations
	// return errors already wrapped in ErrInvalidInput, because this is the
	// persistence boundary's own check rather than an advisory one.
	ValidateFacets(f Facets) error
}

// memoryPolicy carries the D-core rules: the legacy namespace shape and the
// strict dot-notation key vocabulary, and no facets at all.
type memoryPolicy struct{}

func (memoryPolicy) Name() domains.Domain { return domains.Memory }

// ValidateNamespace applies the user/{id}[/project|session/{id}]/memory shape.
//
// The unqualified ValidateNamespace call below is the package-level function
// in namespaces.go, not a recursive call on this method — a method name is not
// a package-level identifier, so the two do not collide. This fold is the
// point of the refactor: the legacy check used to sit in write.go behind its
// own `if in.Domain == domains.Memory`, one branch below the policy call that
// existed to hold exactly this rule.
func (memoryPolicy) ValidateNamespace(ns string) error {
	if ns == "" {
		return fmt.Errorf("namespace is required")
	}
	return ValidateNamespace(ns)
}

// ValidateKey applies the dot-notation rules. See the note above ValidateKey
// in keys.go for why these reject rather than normalize.
func (memoryPolicy) ValidateKey(key string) error {
	return ValidateKey(key)
}

func (memoryPolicy) ValidateFacets(f Facets) error {
	if !f.IsZero() {
		return fmt.Errorf("%w: memory revisions must not carry knowledge facets", ErrInvalidInput)
	}
	return nil
}

// knowledgePolicy requires a namespace segment of exactly "knowledge". Shape:
// user/{user}/knowledge/{...} or app/{app}/knowledge/{...}.
type knowledgePolicy struct{}

func (knowledgePolicy) Name() domains.Domain { return domains.Knowledge }

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

// ValidateKey accepts anything. Knowledge keys carry slugs from external
// sources — hyphens, slashes, mixed case — and were never held to the memory
// key vocabulary. Returning nil here is the same bypass write.go used to spell
// as `&& in.Domain == domains.Memory` on the call to ValidateKey.
func (knowledgePolicy) ValidateKey(string) error { return nil }

func (knowledgePolicy) ValidateFacets(f Facets) error {
	if f.Kind == "" {
		return fmt.Errorf("%w: facet.kind is required (allowed kinds: %s)",
			ErrInvalidInput, KnowledgeKindList())
	}
	if !IsCanonicalKnowledgeKind(f.Kind) {
		return fmt.Errorf("%w: facet.kind %q is not a canonical knowledge kind (allowed kinds: %s)",
			ErrInvalidInput, f.Kind, KnowledgeKindList())
	}
	if f.Source == "" {
		return fmt.Errorf("%w: facet.source is required", ErrInvalidInput)
	}
	if f.Pointer == nil || f.Pointer.Scheme == "" || f.Pointer.Locator == "" {
		return fmt.Errorf("%w: facet.pointer.scheme and facet.pointer.locator are required", ErrInvalidInput)
	}
	return nil
}

// domainPolicies maps each registered domain to its policy. A domain present
// in domains.All() but missing here is a test failure, not a runtime default —
// TestEveryDomainHasAPolicy is the guard, and it is the reason this refactor
// blocks the Event domain rather than the other way round.
var domainPolicies = map[domains.Domain]DomainPolicy{
	domains.Memory:    memoryPolicy{},
	domains.Knowledge: knowledgePolicy{},
}

// policyFor returns the DomainPolicy for d, or an error if d has none.
//
// The error is never reachable for a domain that passed Domain.Valid(), and
// callers on the write path check that first. Read-path callers take a
// caller-supplied domain that may be junk, and treat the error as "no policy
// to consult" rather than promoting it — see explainDomainKeyMiss.
func policyFor(d domains.Domain) (DomainPolicy, error) {
	p, ok := domainPolicies[d]
	if !ok {
		return nil, fmt.Errorf("unknown domain %q", d)
	}
	return p, nil
}
