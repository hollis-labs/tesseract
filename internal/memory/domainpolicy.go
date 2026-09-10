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
	"sort"
	"strings"

	"github.com/hollis-labs/tesseract/domains"
)

// DomainPolicy is the in-tree behavior contract each domain implements. Impls
// are kept small and deterministic — no hidden mutation, no I/O — which is
// also why store selection stays at the adapter layer: picking MemoryStore or
// KnowledgeStore is dependency wiring, not policy, and does not belong here.
//
// Every method is a rule that genuinely differs per domain, or is on an axis
// where domains are known to differ. Rules uniform across every domain with no
// prospect of divergence stay out: the default status is an unconditional
// StatusDraft at write.go and gets no method here.
//
// The interface carries two kinds of rule, and that is deliberate rather than
// drift. Three are validation; ParticipatesInActivation is storage policy.
// CW-20260910-0021 weighed giving activation its own seam and rejected it: a
// second registry, a second lookup and a second completeness test, all to hold
// one bool. The failure mode this file exists to prevent — a domain added
// without an answer, silently taking another domain's — is exactly the failure
// mode on the activation axis, and one registry a new domain must satisfy
// completely is a stronger guard than two it must satisfy separately. The file
// comment above scopes this as "what does this domain do", which is the wider
// claim; three-validation-methods was what the CW-20260909-0033 measurement
// happened to find, not a designed boundary.
type DomainPolicy interface {
	// Name returns the canonical domain identifier.
	Name() domains.Domain

	// ParticipatesInActivation reports whether this domain's rows take part in
	// the activation system: decayed by the sweep AND reinforced by deliberate
	// reads. It is one predicate rather than two on purpose.
	//
	// CW-20260910-0021 is the reason. Knowledge decayed and never reinforced —
	// a countdown with no input — because the two halves were reachable
	// independently, decay through a domain-blind SELECT and reinforcement
	// through a call-site choice. Splitting participation into DecaysActivation
	// and ReinforcesOnRead would make that incoherent pair a supported
	// configuration instead of the defect it was. A domain is in or out.
	//
	// Callers must not consult this to decide whether to reinforce. Both halves
	// read it themselves — collectDecayUpdates and reinforceMemoryIDs, in SQL —
	// so participation cannot be re-decided at a call site. That is the whole
	// point: the defect was a call site holding this choice.
	ParticipatesInActivation() bool

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

// ParticipatesInActivation: memory is the domain activation was built for.
func (memoryPolicy) ParticipatesInActivation() bool { return true }

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

// ParticipatesInActivation: yes, per Chrispian 2026-09-09 — "Knowledge should
// participate in activation… I think knowledge would benefit from it, we can
// measure how much."
//
// Knowledge already decayed; this is what finally makes the other half true.
// Before CW-20260910-0021 its activation was a pure function of age: of the 39
// knowledge rows above the floor at the time of measurement, 38 had
// access_count 0 and no last_accessed_at, so they ranked purely by how recently
// they were written. Activation-ranked recall over knowledge was a
// chronological ordering wearing an activation label.
func (knowledgePolicy) ParticipatesInActivation() bool { return true }

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

// activationDomains returns the domains whose policy opts into activation,
// sorted so the statements built from it are stable across processes.
//
// It reads domains.All() rather than ranging over domainPolicies, so a domain
// registered without a policy is a missing-policy panic here rather than a
// silent omission from the activation set. Being quietly left out of decay is
// precisely the failure this axis just spent a ticket recovering from.
func activationDomains() []domains.Domain {
	var participating []domains.Domain
	for _, d := range domains.All() {
		p, err := policyFor(d)
		if err != nil {
			// Unreachable while TestEveryDomainHasAPolicy passes. Skipping
			// would hide a registry gap; excluding the domain from activation
			// entirely is the safer of the two wrong answers, and the test is
			// what keeps it from being reached.
			continue
		}
		if p.ParticipatesInActivation() {
			participating = append(participating, d)
		}
	}
	sort.Slice(participating, func(i, j int) bool {
		return participating[i] < participating[j]
	})
	return participating
}

// activationDomainPredicate renders a SQL fragment and its args restricting a
// memory_state query to domains that participate in activation.
//
// Both halves of activation call this — collectDecayUpdates for the sweep,
// reinforceMemoryIDs for the bump — which is what makes participation a
// property of the domain rather than of whoever wrote the call site. The
// column is always memory_state.domain: activation state lives on
// memory_state, so the domain that owns it is the one stamped there.
//
// The zero case returns a false literal rather than `IN ()`, which is a syntax
// error in SQLite. It cannot happen today and must fail closed if it ever can:
// a predicate that matched everything would silently decay a domain that had
// opted out.
func activationDomainPredicate(column string) (string, []any) {
	participating := activationDomains()
	if len(participating) == 0 {
		return "0", nil
	}
	placeholders := make([]string, len(participating))
	args := make([]any, len(participating))
	for i, d := range participating {
		placeholders[i] = "?"
		args[i] = string(d)
	}
	return column + " IN (" + strings.Join(placeholders, ", ") + ")", args
}
