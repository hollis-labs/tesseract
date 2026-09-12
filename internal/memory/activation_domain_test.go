package memory_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/tesseract/domains"
	"github.com/hollis-labs/tesseract/internal/memory"
)

// activationFixture is a minimal valid write for one domain, plus the
// activation answer that domain's policy is expected to give.
//
// The expectation is stated here by hand rather than read back off the policy.
// A test that asks the policy what it does and then checks it did that is a
// tautology — it passes for any implementation, including the broken one this
// file exists to prevent. See TestActivationParticipationIsCoherentPerDomain.
type activationFixture struct {
	input   memory.WriteInput
	wantsIn bool
}

// activationFixtures is keyed by domain and deliberately not derived from
// domains.All(). A domain added to the registry without an entry here fails
// TestEveryDomainHasAnActivationFixture, which is the loud version of the
// silence that let knowledge decay unreinforced for four months.
func activationFixtures() map[domains.Domain]activationFixture {
	base := func(ns, key string) memory.WriteInput {
		return memory.WriteInput{
			Domain:      domains.Memory,
			Namespace:   ns,
			MemoryKey:   key,
			Author:      memory.Author{AgentID: "test-agent", AgentVersion: "1.0"},
			Trigger:     memory.TriggerExplicit,
			SessionID:   "manual:01HXXXXX",
			DerivedFrom: memory.DerivedFromUser,
			Confidence:  0.9,
			Status:      memory.StatusDraft,
			Payload:     memory.Payload{Summary: "s", Body: "b"},
		}
	}

	memIn := base("user/chrispian/memory/notes", "activation.probe")
	memIn.Domain = domains.Memory

	knowIn := base("user/chrispian/knowledge/probe", "activation-probe")
	knowIn.Domain = domains.Knowledge
	knowIn.Facets = memory.Facets{
		Kind:    knowledgeKindForTest(),
		Source:  "filesystem",
		Pointer: &memory.Pointer{Scheme: "file", Locator: "/tmp/probe.md"},
	}

	eventIn := base("user/chrispian/event/reasoning", "activation.probe")
	eventIn.Domain = domains.Event

	return map[domains.Domain]activationFixture{
		domains.Memory: {input: memIn, wantsIn: true},
		// Chrispian, 2026-09-09: "Knowledge should participate in activation."
		domains.Knowledge: {input: knowIn, wantsIn: true},
		// Event is OUT, and it is the first domain that is. Per
		// tesseract_event_domain_definition: "long retention, no activation
		// decay. A journal that fades because nobody touched it is a broken
		// journal." The coherence test below therefore proves the mirror image
		// for this row — nothing decays it AND nothing reinforces it — which is
		// the half of the invariant no domain could exercise until now.
		domains.Event: {input: eventIn, wantsIn: false},
	}
}

// knowledgeKindForTest takes the first canonical kind off the real list so the
// fixture does not hardcode a vocabulary that lives elsewhere.
func knowledgeKindForTest() string {
	list := memory.KnowledgeKindList()
	if i := strings.Index(list, ","); i >= 0 {
		return strings.TrimSpace(list[:i])
	}
	return strings.TrimSpace(list)
}

// TestEveryDomainHasAnActivationFixture forces a newly registered domain to
// state its activation answer here before it can ship.
//
// This is the companion to TestEveryDomainHasAPolicy. The compiler already
// requires a new policy to implement ParticipatesInActivation, so no domain can
// silently lack the method — but nothing would require anyone to have *thought*
// about the answer, and returning the zero value (false) compiles fine. A
// domain that means to participate and returns false would decay nothing and
// reinforce nothing, which is quiet and wrong in the opposite direction from
// the CW-20260910-0021 defect.
func TestEveryDomainHasAnActivationFixture(t *testing.T) {
	fixtures := activationFixtures()
	for _, d := range domains.All() {
		if _, ok := fixtures[d]; !ok {
			t.Errorf("domain %q is registered but has no activation fixture. "+
				"Add one to activationFixtures() stating whether %q participates "+
				"in activation, so the coherence test below can prove both halves agree.", d, d)
		}
	}
}

// TestActivationParticipationIsCoherentPerDomain is the invariant CW-20260910-0021
// exists to install, and the one test that would have caught the defect.
//
// The defect was never that knowledge had the wrong answer. It was that
// knowledge had TWO answers: decay swept it (a domain-blind SELECT) and
// reinforcement skipped it (a call site reaching for the plain getter). Neither
// half was individually surprising, and the test suite asserted each half
// separately and passed. Activation with only the decay half is a countdown
// with no input — every knowledge row walked to the 0.05 floor and stayed.
//
// So this asserts the two halves against each other, per domain: whatever a
// domain's answer is, decay and reinforcement must both give it. A future
// domain that opts out gets the same proof in the mirror — nothing decays it
// and nothing reinforces it.
func TestActivationParticipationIsCoherentPerDomain(t *testing.T) {
	for d, fx := range activationFixtures() {
		t.Run(string(d), func(t *testing.T) {
			ctx := context.Background()

			// --- Half 1: does a deliberate read reinforce? ---
			ms, cleanup := newTestStore(t)
			defer cleanup()

			rev, err := ms.WriteRevision(ctx, fx.input)
			if err != nil {
				t.Fatalf("write %s fixture: %v", d, err)
			}
			before, err := ms.GetState(ctx, rev.MemoryID)
			if err != nil {
				t.Fatalf("state before: %v", err)
			}
			if _, readErr := ms.GetCurrentInDomainReinforced(ctx, d, rev.Namespace, rev.MemoryKey); readErr != nil {
				t.Fatalf("reinforced read: %v", readErr)
			}
			after, err := ms.GetState(ctx, rev.MemoryID)
			if err != nil {
				t.Fatalf("state after: %v", err)
			}
			reinforced := after.AccessCount > before.AccessCount

			// --- Half 2: does the sweep decay? ---
			// A fresh row starts at 1.0. One half-life later a participating
			// domain must have moved it; a non-participating one must not.
			ms2, cleanup2 := newTestStore(t)
			defer cleanup2()

			rev2, err := ms2.WriteRevision(ctx, fx.input)
			if err != nil {
				t.Fatalf("write %s fixture for decay: %v", d, err)
			}
			pre, err := ms2.GetState(ctx, rev2.MemoryID)
			if err != nil {
				t.Fatalf("decay state before: %v", err)
			}
			future := time.Now().UTC().Add(14 * 24 * time.Hour)
			if sweepErr := ms2.ExportApplyActivationDecayAt(ctx, future); sweepErr != nil {
				t.Fatalf("decay sweep: %v", sweepErr)
			}
			post, err := ms2.GetState(ctx, rev2.MemoryID)
			if err != nil {
				t.Fatalf("decay state after: %v", err)
			}
			decayed := post.Activation < pre.Activation

			// --- The invariant ---
			if reinforced != decayed {
				t.Errorf("domain %q is incoherent on activation: reinforced=%v decayed=%v. "+
					"A domain participates in BOTH halves or NEITHER — decay without "+
					"reinforcement is a countdown with no input, which is exactly the "+
					"CW-20260910-0021 defect.", d, reinforced, decayed)
			}
			if reinforced != fx.wantsIn {
				t.Errorf("domain %q: participates=%v, want %v", d, reinforced, fx.wantsIn)
			}
		})
	}
}

// TestKnowledgeDeliberateReadReinforces is the narrow regression test for the
// defect itself, stated at the layer it broke.
//
// The two call sites were internal/mcpadapter/crossdomain_read_tools.go and
// internal/contextapi/knowledge_handler.go, both calling the plain getter where
// their memory twins called the reinforcing one. Their behavior is pinned at
// the adapter layer (crossdomain_parity_test.go); this pins the store primitive
// they now reach for, so the two cannot drift back apart quietly.
func TestKnowledgeDeliberateReadReinforces(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	in := activationFixtures()[domains.Knowledge].input
	rev, err := ms.WriteRevision(ctx, in)
	if err != nil {
		t.Fatalf("write knowledge revision: %v", err)
	}

	before, err := ms.GetState(ctx, rev.MemoryID)
	if err != nil {
		t.Fatalf("state before: %v", err)
	}
	if _, readErr := ms.GetCurrentInDomainReinforced(ctx, domains.Knowledge, rev.Namespace, rev.MemoryKey); readErr != nil {
		t.Fatalf("deliberate knowledge read: %v", readErr)
	}
	after, err := ms.GetState(ctx, rev.MemoryID)
	if err != nil {
		t.Fatalf("state after: %v", err)
	}

	if after.AccessCount != before.AccessCount+1 {
		t.Errorf("knowledge access_count %d -> %d, want +1", before.AccessCount, after.AccessCount)
	}
	if after.Activation <= before.Activation {
		t.Errorf("knowledge activation %v -> %v, want an increase", before.Activation, after.Activation)
	}
	if after.LastAccessedAt == nil {
		t.Error("knowledge last_accessed_at not stamped")
	}
}
