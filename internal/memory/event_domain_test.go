package memory_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/hollis-labs/tesseract/domains"
	"github.com/hollis-labs/tesseract/internal/memory"
)

// The Event domain's storage policy, asserted against hand-stated expectations
// (CW-20260909-0035).
//
// Every claim below is one the domain's definition makes. They are tested
// separately from the log read because they are what makes Event a DOMAIN
// rather than a type in the registry: each one is a rule the storage engine
// applies, which is the surface a type declaration was rejected for reaching
// into.

// ── The default recall corpus ────────────────────────────────────────────────

// recallCorpusAnswers is keyed by domain and deliberately not derived from any
// policy. A domain added to the registry without an entry fails
// TestEveryDomainStatesItsRecallCorpusAnswer.
//
// This mirrors activationFixtures, and for the same reason: the compiler forces
// a new domain to IMPLEMENT InDefaultRecallCorpus, but nothing would force
// anyone to have thought about the answer, and the zero value (false) compiles
// fine. A domain that meant to be searchable and returned false would be
// invisible to every unqualified recall — quiet, and wrong in the direction
// nobody checks.
func recallCorpusAnswers() map[domains.Domain]bool {
	return map[domains.Domain]bool{
		domains.Memory:    true,
		domains.Knowledge: true,
		// Out. Volume, not preference: a reasoning log runs 10-100x a curated
		// corpus, so a default that included it would make every unqualified
		// recall a log search. The log has its own read path.
		domains.Event: false,
	}
}

func TestEveryDomainStatesItsRecallCorpusAnswer(t *testing.T) {
	answers := recallCorpusAnswers()
	for _, d := range domains.All() {
		if _, ok := answers[d]; !ok {
			t.Errorf("domain %q is registered but has no entry in recallCorpusAnswers(). "+
				"State whether an unqualified tesseract_recall should search %q, so the "+
				"behavior below is proven rather than inherited.", d, d)
		}
	}
}

// TestUnqualifiedRecallExcludesTheEventLog is the isolation property in its
// observable form: identical corpora, identical query, and the event entry is
// absent until it is asked for by name.
func TestUnqualifiedRecallExcludesTheEventLog(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	memRev, err := ms.WriteRevision(ctx, sampleInput("isolation.memory"))
	if err != nil {
		t.Fatal(err)
	}
	evRev, err := ms.WriteRevision(ctx, eventInput("an event nobody asked for"))
	if err != nil {
		t.Fatal(err)
	}

	namespaces := []string{"user/chrispian/memory/notes", eventNS}

	// No `domains` filter: the curated corpus only.
	got, err := ms.Recall(ctx, memory.RecallInput{
		Namespaces: namespaces,
		Ranking:    memory.RankingChronological,
	})
	if err != nil {
		t.Fatalf("unqualified recall: %v", err)
	}
	for _, r := range got {
		if r.Revision.RevisionID == evRev.RevisionID {
			t.Error("an unqualified recall returned an event revision; the log is supposed to be opt-in")
		}
	}
	if !containsRevision(got, memRev.RevisionID) {
		t.Error("an unqualified recall dropped the memory revision, so this test proves nothing")
	}

	// Named explicitly: fully recallable, which is the other half of the
	// property. Isolation is a default, not a wall.
	got, err = ms.Recall(ctx, memory.RecallInput{
		Namespaces: namespaces,
		Ranking:    memory.RankingChronological,
		Filters:    memory.RecallFilters{Domains: []domains.Domain{domains.Event}},
	})
	if err != nil {
		t.Fatalf("recall with domains=[event]: %v", err)
	}
	if !containsRevision(got, evRev.RevisionID) {
		t.Error("recall with domains=[event] did not return the event revision")
	}
	if containsRevision(got, memRev.RevisionID) {
		t.Error("recall with domains=[event] returned a memory revision")
	}
}

// TestRecallOrderingFingerprintIgnoresTheResolvedCorpusDefault: an omitted
// `domains` and an explicit list of the same domains must page alike, or a
// caller who spelled out its defaults on page 2 would be told the query
// changed. This is the property resolveRecallDefaults exists to give, and the
// reason the corpus default is resolved into the filter rather than rendered as
// a hidden SQL fragment below the fingerprint.
func TestRecallOrderingFingerprintIgnoresTheResolvedCorpusDefault(t *testing.T) {
	base := memory.RecallInput{
		Namespaces: []string{"user/chrispian/memory/notes"},
		Ranking:    memory.RankingChronological,
	}
	explicit := base
	explicit.Filters.Domains = []domains.Domain{domains.Memory, domains.Knowledge}

	if memory.RecallOrderingFingerprint(base) != memory.RecallOrderingFingerprint(explicit) {
		t.Error("omitting `domains` and naming the default set fingerprint differently, " +
			"so a cursor issued on page 1 would be refused on page 2")
	}

	// Naming a different set genuinely changes the sequence and must not.
	withEvent := base
	withEvent.Filters.Domains = []domains.Domain{domains.Event}
	if memory.RecallOrderingFingerprint(base) == memory.RecallOrderingFingerprint(withEvent) {
		t.Error("the default corpus and domains=[event] fingerprint alike; a cursor could " +
			"be resumed across a change that selects entirely different rows")
	}
}

// ── Activation ───────────────────────────────────────────────────────────────

// TestActivationRankingIsRefusedOverEvent covers the failure the refusal
// prevents: an event row's activation is memory_state's insert default forever,
// which sits ABOVE almost the whole curated corpus. Answering would rank the
// log first, everywhere, permanently — an ordering by a constant that looks
// like it works.
func TestActivationRankingIsRefusedOverEvent(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	if _, err := ms.WriteRevision(ctx, eventInput("ranked by nothing")); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name string
		doms []domains.Domain
	}{
		{"event alone", []domains.Domain{domains.Event}},
		// The mixed case is the drowning case, and the one worth refusing
		// loudest: it is the call that would silently put the whole log above
		// the whole curated corpus.
		{"event mixed with memory", []domains.Domain{domains.Memory, domains.Event}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ms.Recall(ctx, memory.RecallInput{
				Namespaces: []string{eventNS, "user/chrispian/memory/notes"},
				Ranking:    memory.RankingActivation,
				Filters:    memory.RecallFilters{Domains: tc.doms},
			})
			if !errors.Is(err, memory.ErrInvalidInput) {
				t.Fatalf("err = %v, want ErrInvalidInput", err)
			}
			// The message has to name the way forward, or a caller reads it as
			// "events are not searchable" and stops.
			if !strings.Contains(err.Error(), "chronological") {
				t.Errorf("the refusal does not point at ranking=chronological: %v", err)
			}
		})
	}
}

// TestNoQueryRecallOverEventDefaultsToChronological is the other arm of the
// same rule. An UNSTATED default that resolves to a ranking the domain has no
// values for should resolve honestly instead of erroring — the caller did not
// ask for activation, the resolver did.
func TestNoQueryRecallOverEventDefaultsToChronological(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	ids := seedEvents(t, ms, 3)

	got, err := ms.Recall(ctx, memory.RecallInput{
		Namespaces: []string{eventNS},
		Filters:    memory.RecallFilters{Domains: []domains.Domain{domains.Event}},
	})
	if err != nil {
		t.Fatalf("no-query recall over event: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("returned %d results, want 3", len(got))
	}
	// Chronological ranking carries no score, and newest first.
	if got[0].Score != nil {
		t.Errorf("result carries a score %v; chronological ranking has none", *got[0].Score)
	}
	if got[0].Revision.RevisionID != ids[len(ids)-1] {
		t.Error("results are not newest-first, so the ranking did not resolve to chronological")
	}

	// The curated corpus keeps activation as its no-query default.
	if _, writeErr := ms.WriteRevision(ctx, sampleInput("still.activation")); writeErr != nil {
		t.Fatal(writeErr)
	}
	got, err = ms.Recall(ctx, memory.RecallInput{
		Namespaces: []string{"user/chrispian/memory/notes"},
	})
	if err != nil {
		t.Fatalf("no-query recall over memory: %v", err)
	}
	if len(got) == 0 || got[0].Score == nil {
		t.Error("no-query recall over memory lost its activation score; the honest-default arm " +
			"is meant to fire only where no domain in scope has activation")
	}
}

// TestTouchReportsAnEventAsNotReinforced is the counter fix. Before
// CW-20260909-0035 TouchRevisions counted what it ASKED to reinforce, which was
// exact only for as long as every domain participated in activation.
func TestTouchReportsAnEventAsNotReinforced(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	ev, err := ms.WriteRevision(ctx, eventInput("touched but unmoved"))
	if err != nil {
		t.Fatal(err)
	}
	mem, err := ms.WriteRevision(ctx, sampleInput("touch.mixed"))
	if err != nil {
		t.Fatal(err)
	}

	res, err := ms.TouchRevisions(ctx, []string{ev.RevisionID, mem.RevisionID, "01HXNOSUCHREVISION"})
	if err != nil {
		t.Fatalf("TouchRevisions: %v", err)
	}

	if res.Touched != 1 {
		t.Errorf("touched = %d, want 1 — only the memory revision moved", res.Touched)
	}
	if len(res.NotReinforced) != 1 || res.NotReinforced[0] != ev.RevisionID {
		t.Errorf("not_reinforced = %v, want [%s]", res.NotReinforced, ev.RevisionID)
	}
	if len(res.NotFound) != 1 || res.NotFound[0] != "01HXNOSUCHREVISION" {
		t.Errorf("not_found = %v, want the unknown id only", res.NotFound)
	}

	// The state is genuinely untouched, not merely reported so.
	st, err := ms.GetState(ctx, ev.MemoryID)
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if st.AccessCount != 0 || st.LastAccessedAt != nil {
		t.Errorf("event state moved under touch: access_count=%d last_accessed_at=%v",
			st.AccessCount, st.LastAccessedAt)
	}
}

// TestEventsCarryEmbeddings. "Recovering the why is a semantic query" is the
// reason an earlier guess — that a log wants no embeddings — was corrected
// before it reached a design. Nothing in the embed path branches on domain
// today, so this passes for free; it is written down because free-today is not
// the same as decided.
func TestEventsCarryEmbeddings(t *testing.T) {
	ms, cleanup := newTestStoreWithEmbedder(t)
	defer cleanup()
	ctx := context.Background()

	rev, err := ms.WriteRevision(ctx, eventInput("why we chose keyset paging"))
	if err != nil {
		t.Fatal(err)
	}
	if embedErr := ms.EmbedRevision(ctx, rev.RevisionID, "test-model"); embedErr != nil {
		t.Fatalf("EmbedRevision on an event: %v", embedErr)
	}
	got, err := ms.GetRevisionByID(ctx, rev.RevisionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.EmbeddingVector) == 0 {
		t.Error("an event revision has no embedding vector; recovering the why is a semantic query")
	}
}

// ── The namespace grammar ────────────────────────────────────────────────────

func TestEventNamespaceGrammar(t *testing.T) {
	cases := []struct {
		ns   string
		want bool
		why  string
	}{
		{"user/chrispian/event/journal", true, "user scope, the journal"},
		{"user/chrispian/event/reasoning", true, "user scope, agent reasoning"},
		{"user/chrispian/project/tesseract/event/reasoning", true, "project scope"},
		{"user/chrispian/session/session-20260910-85916030/event/reasoning", true, "session scope"},

		{"user/chrispian/event", false, "no {type} segment"},
		{"user/chrispian/event/", false, "trailing slash"},
		{"user/chrispian/event/telemetry", false, "type outside the closed vocabulary"},
		{"user/chrispian/memory/notes", false, "memory namespace, wrong domain segment"},
		{"user/chrispian/knowledge/framework", false, "knowledge namespace"},
		{"app/indexer/event/reasoning", false, "must begin with user/"},
		// The trap the grammar deliberately declines: created_at is indexed and
		// the log read filters on it, so a date partition buys nothing and
		// turns every window read into a multi-namespace query.
		{"user/chrispian/event/journal/2026/09", false, "dates do not belong in the path"},
		{"", false, "empty"},
	}
	for _, tc := range cases {
		t.Run(tc.ns, func(t *testing.T) {
			err := memory.ValidateEventNamespace(tc.ns)
			if tc.want && err != nil {
				t.Errorf("ValidateEventNamespace(%q) = %v, want nil (%s)", tc.ns, err, tc.why)
			}
			if !tc.want && err == nil {
				t.Errorf("ValidateEventNamespace(%q) = nil, want an error (%s)", tc.ns, tc.why)
			}
		})
	}
}

// TestEventGrammarIsEnforcedOnWrite closes the loop: the parser above is the
// one the write path consults, through eventPolicy.
func TestEventGrammarIsEnforcedOnWrite(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	bad := eventInput("wrong door")
	bad.Namespace = "user/chrispian/memory/notes"
	if _, err := ms.WriteRevision(ctx, bad); !errors.Is(err, memory.ErrInvalidInput) {
		t.Errorf("writing an event into a memory namespace: err = %v, want ErrInvalidInput", err)
	}

	// And the mirror: a memory write into an event namespace.
	crossed := sampleInput("cross.domain")
	crossed.Namespace = eventNS
	if _, err := ms.WriteRevision(ctx, crossed); !errors.Is(err, memory.ErrInvalidInput) {
		t.Errorf("writing a memory into an event namespace: err = %v, want ErrInvalidInput", err)
	}

	// Events carry no knowledge facets.
	faceted := eventInput("faceted")
	faceted.Facets = memory.Facets{
		Kind:    "note",
		Source:  "manual",
		Pointer: &memory.Pointer{Scheme: "nil", Locator: "x"},
	}
	if _, err := ms.WriteRevision(ctx, faceted); !errors.Is(err, memory.ErrInvalidInput) {
		t.Errorf("event write with knowledge facets: err = %v, want ErrInvalidInput", err)
	}
}

// TestKeylessEventsAreDistinctEntries. A log entry records that something
// happened; two of them are two things, not two revisions of one.
func TestKeylessEventsAreDistinctEntries(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	first, err := ms.WriteRevision(ctx, eventInput("first"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := ms.WriteRevision(ctx, eventInput("second"))
	if err != nil {
		t.Fatal(err)
	}
	if first.MemoryID == second.MemoryID {
		t.Error("two keyless event writes share a memory_id; the second would be a revision " +
			"of the first rather than a separate entry")
	}
}

// TestSemanticDedupWorksInsideTheEventDomain guards a hole the recall corpus
// default opened on the WRITE path.
//
// findSemanticMatch used to leave `domains` unset and let recall cover
// everything. Once an unqualified recall stopped covering the event log, a
// dedup check on an event write searched a corpus that by definition holds no
// event rows and answered "no duplicate" every time. Nothing failed; the
// feature just silently stopped working for one domain — which is the exact
// shape of defect the domain-policy work exists to prevent.
func TestSemanticDedupWorksInsideTheEventDomain(t *testing.T) {
	ms, cleanup := newTestStoreWithEmbedder(t)
	defer cleanup()
	ctx := context.Background()

	// The mock embedder returns one fixed vector, so any two embedded revisions
	// are a perfect cosine match. That is what makes this a test of
	// REACHABILITY — whether the dedup recall can see the earlier row at all —
	// rather than of the similarity threshold.
	//
	// The first revision is embedded explicitly: similarity ranking drops
	// candidates with no stored vector, and the test store's queue is a no-op,
	// so without this there is nothing to match against and the test would pass
	// for the wrong reason.
	firstRev, err := ms.WriteRevision(ctx, eventInput("the same thought twice"))
	if err != nil {
		t.Fatal(err)
	}
	if embedErr := ms.EmbedRevision(ctx, firstRev.RevisionID, "test-model"); embedErr != nil {
		t.Fatalf("embed the first event: %v", embedErr)
	}

	second := eventInput("the same thought twice")
	second.Dedup = "semantic"
	secondRev, err := ms.WriteRevision(ctx, second)
	if err != nil {
		t.Fatal(err)
	}
	if secondRev.DedupMatch != firstRev.RevisionID {
		t.Errorf("dedup_match = %q, want %q — the dedup recall cannot see event rows",
			secondRev.DedupMatch, firstRev.RevisionID)
	}

	// And it stays scoped: a memory write must not match an event row.
	memIn := sampleInput("dedup.scope")
	memIn.Payload = memory.Payload{Summary: "the same thought twice"}
	memIn.Dedup = "semantic"
	memRev, err := ms.WriteRevision(ctx, memIn)
	if err != nil {
		t.Fatal(err)
	}
	if memRev.DedupMatch != "" {
		t.Errorf("a memory write matched %q; dedup must not reach across domains", memRev.DedupMatch)
	}
}

func containsRevision(results []memory.RecallResult, revisionID string) bool {
	for _, r := range results {
		if r.Revision.RevisionID == revisionID {
			return true
		}
	}
	return false
}
