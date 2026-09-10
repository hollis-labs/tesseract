package memory

import (
	"errors"
	"strings"
	"testing"

	"github.com/hollis-labs/tesseract/domains"
)

// TestEveryDomainHasAPolicy is the guard this whole refactor exists to install
// (CW-20260909-0033).
//
// Domain behavior used to be if/switch sites keyed on domains.Memory, and a
// domain added to the registry without a matching arm did not fail — it took
// the default branch and silently got another domain's rules. There is no
// default branch left to take, but the map could still be missing an entry, so
// the failure has to be moved somewhere loud. This is that place: add a domain
// to domains.All() without registering a policy and this test names it.
func TestEveryDomainHasAPolicy(t *testing.T) {
	for _, d := range domains.All() {
		p, err := policyFor(d)
		if err != nil {
			t.Errorf("domain %q is registered in domains.All() but has no DomainPolicy: %v", d, err)
			continue
		}
		if p.Name() != d {
			t.Errorf("policyFor(%q).Name() = %q; the registry maps a domain to another domain's policy", d, p.Name())
		}
	}
}

func TestPolicyForUnknown(t *testing.T) {
	if _, err := policyFor(domains.Domain("made-up")); err == nil {
		t.Error("policyFor() on unknown domain returned nil error")
	}
}

func TestMemoryPolicyValidateNamespace(t *testing.T) {
	p := memoryPolicy{}
	if err := p.ValidateNamespace("user/alice/memory/decisions"); err != nil {
		t.Errorf("memory namespace rejected: %v", err)
	}
	if err := p.ValidateNamespace(""); err == nil {
		t.Error("empty namespace accepted; want error")
	}
	// "user/alice/memory" is the shape the old domains-package test asserted
	// this policy ACCEPTS, back when it only checked for non-empty and the
	// legacy parser ran separately in write.go. It was never accepted on
	// write — the parser wants 4 or 6 segments — so the policy rejecting it
	// now is the fold working, not a behavior change at the boundary.
	if err := p.ValidateNamespace("user/alice/memory"); err == nil {
		t.Error("memory namespace with a bare type segment accepted; want the legacy parser's rejection")
	}
}

// TestMemoryPolicyValidateNamespaceAppliesLegacyShape proves the fold that
// removed write.go's second namespace check: the legacy parser used to run
// behind its own `if in.Domain == domains.Memory`, one branch below the policy
// call. If the policy did not absorb it, a namespace that only the legacy
// parser rejects would now be accepted on write.
func TestMemoryPolicyValidateNamespaceAppliesLegacyShape(t *testing.T) {
	p := memoryPolicy{}
	for _, ns := range []string{
		"user/alice/knowledge",
		"nonsense",
		"user/alice/notes",
	} {
		policyErr := p.ValidateNamespace(ns)
		legacyErr := ValidateNamespace(ns)
		if legacyErr == nil {
			continue // not a case that distinguishes the two
		}
		if policyErr == nil {
			t.Errorf("memoryPolicy.ValidateNamespace(%q) = nil, but the legacy parser rejects it: %v", ns, legacyErr)
		}
	}
}

func TestKnowledgePolicyValidateNamespace(t *testing.T) {
	p := knowledgePolicy{}
	ok := []string{
		"user/alice/knowledge",
		"user/alice/knowledge/framework",
		"app/ingester/knowledge/obsidian/work",
	}
	for _, ns := range ok {
		if err := p.ValidateNamespace(ns); err != nil {
			t.Errorf("knowledge namespace %q rejected: %v", ns, err)
		}
	}
	bad := []string{
		"",
		"user/alice/memory",
		"user/alice/notes",
		"user/alice/memory/knowledge", // knowledge must be 3rd segment
		"knowledge/user/alice",        // missing user|app prefix
		"user/alice/knowledge/",       // trailing slash
		"user//knowledge",             // empty segment
		"user/alice",                  // too short
		"org/acme/knowledge",          // wrong prefix
	}
	for _, ns := range bad {
		if err := p.ValidateNamespace(ns); err == nil {
			t.Errorf("knowledge namespace %q accepted; want error", ns)
		}
	}
}

// TestPolicyValidateKeyMatchesPreviousBehavior pins the key rule each domain
// carried before it moved onto the policy: write.go applied ValidateKey only
// under `in.Domain == domains.Memory`, so knowledge keys were never checked.
func TestPolicyValidateKeyMatchesPreviousBehavior(t *testing.T) {
	keys := []string{
		"notes.today",
		"user-preferences", // hyphens: invalid as a memory key
		"pkg/react",        // external slug: invalid as a memory key
		"MixedCase",
	}
	for _, k := range keys {
		wantMemory := ValidateKey(k)
		gotMemory := memoryPolicy{}.ValidateKey(k)
		if (gotMemory == nil) != (wantMemory == nil) {
			t.Errorf("memoryPolicy.ValidateKey(%q) = %v, want parity with ValidateKey = %v", k, gotMemory, wantMemory)
		}
		if err := (knowledgePolicy{}).ValidateKey(k); err != nil {
			t.Errorf("knowledgePolicy.ValidateKey(%q) = %v, want nil (knowledge keys carry external slugs)", k, err)
		}
	}
}

// TestPolicyValidateFacetsMatchesPreviousBehavior pins the arms of the
// validateFacets switch this replaced, including the wrapping: these errors
// are returned to the caller unwrapped by write.go, so they must already carry
// ErrInvalidInput.
func TestPolicyValidateFacetsMatchesPreviousBehavior(t *testing.T) {
	goodKnowledge := Facets{
		Kind:    canonicalKnowledgeKindForTest(t),
		Source:  "filesystem",
		Pointer: &Pointer{Scheme: "file", Locator: "/tmp/x.md"},
	}

	if err := (memoryPolicy{}).ValidateFacets(Facets{}); err != nil {
		t.Errorf("memory domain rejected zero facets: %v", err)
	}
	err := memoryPolicy{}.ValidateFacets(goodKnowledge)
	if err == nil {
		t.Error("memory domain accepted knowledge facets; want error")
	} else if !errors.Is(err, ErrInvalidInput) {
		t.Errorf("memory facet error does not wrap ErrInvalidInput: %v", err)
	}

	if err := (knowledgePolicy{}).ValidateFacets(goodKnowledge); err != nil {
		t.Errorf("knowledge domain rejected valid facets: %v", err)
	}
	for name, f := range map[string]Facets{
		"missing kind":    {Source: "filesystem", Pointer: &Pointer{Scheme: "file", Locator: "/tmp/x.md"}},
		"missing source":  {Kind: goodKnowledge.Kind, Pointer: &Pointer{Scheme: "file", Locator: "/tmp/x.md"}},
		"missing pointer": {Kind: goodKnowledge.Kind, Source: "filesystem"},
		"bad kind":        {Kind: "not-a-kind", Source: "filesystem", Pointer: &Pointer{Scheme: "file", Locator: "/tmp/x.md"}},
	} {
		err := knowledgePolicy{}.ValidateFacets(f)
		if err == nil {
			t.Errorf("knowledge domain accepted facets with %s; want error", name)
			continue
		}
		if !errors.Is(err, ErrInvalidInput) {
			t.Errorf("knowledge facet error for %s does not wrap ErrInvalidInput: %v", name, err)
		}
	}
}

// canonicalKnowledgeKindForTest picks a real kind off the canonical list so
// this file does not hardcode a vocabulary that lives elsewhere.
func canonicalKnowledgeKindForTest(t *testing.T) string {
	t.Helper()
	kinds := strings.Split(KnowledgeKindList(), ", ")
	if len(kinds) == 0 || kinds[0] == "" {
		t.Fatal("KnowledgeKindList() returned no kinds")
	}
	return strings.TrimSpace(kinds[0])
}

// TestExplainDomainKeyMissRoutesByPolicy covers the two read-path sites that
// used to test `domain == domains.Memory` inline: a memory miss on an
// unspellable key carries the diagnosis, a knowledge miss on the same key does
// not, and an unregistered domain returns the error untouched rather than
// complaining about the domain.
func TestExplainDomainKeyMissRoutesByPolicy(t *testing.T) {
	base := errors.New("wrapped")
	notFound := errors.Join(ErrNotFound, base)
	const badKey = "user-preferences"

	got := explainDomainKeyMiss(domains.Memory, badKey, notFound)
	if !errors.Is(got, ErrInvalidKey) {
		t.Errorf("memory miss on %q lost the key diagnosis: %v", badKey, got)
	}
	if !errors.Is(got, ErrNotFound) {
		t.Errorf("memory miss stopped wrapping ErrNotFound: %v", got)
	}

	got = explainDomainKeyMiss(domains.Knowledge, badKey, notFound)
	if errors.Is(got, ErrInvalidKey) {
		t.Errorf("knowledge miss on %q gained a memory-key diagnosis: %v", badKey, got)
	}

	got = explainDomainKeyMiss(domains.Domain("made-up"), badKey, notFound)
	if !errors.Is(got, ErrNotFound) || errors.Is(got, ErrInvalidKey) {
		t.Errorf("unregistered domain should return the miss untouched, got: %v", got)
	}
}
