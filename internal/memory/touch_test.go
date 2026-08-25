package memory_test

// TouchRevisions semantics: what one report of "these memories shaped my turn"
// is worth, and what it costs to get it wrong.
//
// The rules under test all exist to make over-reporting unprofitable rather than
// merely discouraged. Naming a thing twice is worth what naming it once is worth;
// naming two revisions of one memory is worth one reinforcement; and a stale ID
// is reported back rather than raised, so a caller holding a partly-stale set has
// no reason to widen or narrow what it reports to avoid an error.

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"github.com/hollis-labs/tesseract/domains"
	"github.com/hollis-labs/tesseract/internal/memory"
)

func TestTouchRevisions_ReinforcesNamedMemory(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	rev, err := ms.WriteRevision(ctx, sampleInput("touch.basic"))
	if err != nil {
		t.Fatal(err)
	}
	setActivation(t, ms, rev.MemoryID, 0.05)

	res, err := ms.TouchRevisions(ctx, []string{rev.RevisionID})
	if err != nil {
		t.Fatalf("TouchRevisions: %v", err)
	}
	if res.Touched != 1 {
		t.Errorf("Touched = %d, want 1", res.Touched)
	}
	if len(res.NotFound) != 0 {
		t.Errorf("NotFound = %v, want empty", res.NotFound)
	}

	st, err := ms.GetState(ctx, rev.MemoryID)
	if err != nil {
		t.Fatal(err)
	}
	if st.AccessCount != 1 {
		t.Errorf("access_count = %d, want 1", st.AccessCount)
	}
	if st.LastAccessedAt == nil {
		t.Error("last_accessed_at not set")
	}
	if st.Activation != 0.24 {
		t.Errorf("activation = %v, want 0.24", st.Activation)
	}
}

// TestTouchRevisions_DuplicateIDsCountOnce is the anti-gaming rule at the level
// of the argument: repeating an ID in one call must not be worth more than
// stating it once.
func TestTouchRevisions_DuplicateIDsCountOnce(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	rev, err := ms.WriteRevision(ctx, sampleInput("touch.dupe_id"))
	if err != nil {
		t.Fatal(err)
	}
	setActivation(t, ms, rev.MemoryID, 0.05)

	res, err := ms.TouchRevisions(ctx,
		[]string{rev.RevisionID, rev.RevisionID, rev.RevisionID})
	if err != nil {
		t.Fatal(err)
	}
	if res.Touched != 1 {
		t.Errorf("Touched = %d for three copies of one ID, want 1", res.Touched)
	}

	st, _ := ms.GetState(ctx, rev.MemoryID)
	if st.AccessCount != 1 {
		t.Errorf("access_count = %d, want 1", st.AccessCount)
	}
	// One reinforcement from the floor, not three. Three would give 0.5136.
	if st.Activation != 0.24 {
		t.Errorf("activation = %v, want 0.24 (one reinforcement, not three)", st.Activation)
	}
}

// TestTouchRevisions_TwoRevisionsOfOneMemoryCountOnce is the same rule one level
// up. A caller that recalled two revisions of an evolving memory and used both
// consulted one memory, and reporting it honestly must not pay double.
func TestTouchRevisions_TwoRevisionsOfOneMemoryCountOnce(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	first, err := ms.WriteRevision(ctx, sampleInput("touch.same_memory"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := ms.WriteRevision(ctx, sampleInput("touch.same_memory"))
	if err != nil {
		t.Fatal(err)
	}
	if first.MemoryID != second.MemoryID {
		t.Fatalf("test premise broken: two writes to one key gave memory_ids %s and %s",
			first.MemoryID, second.MemoryID)
	}
	if first.RevisionID == second.RevisionID {
		t.Fatal("test premise broken: two writes gave one revision_id")
	}
	setActivation(t, ms, first.MemoryID, 0.05)

	res, err := ms.TouchRevisions(ctx, []string{first.RevisionID, second.RevisionID})
	if err != nil {
		t.Fatal(err)
	}
	if res.Touched != 1 {
		t.Errorf("Touched = %d for two revisions of one memory, want 1", res.Touched)
	}
	st, _ := ms.GetState(ctx, first.MemoryID)
	if st.Activation != 0.24 {
		t.Errorf("activation = %v, want 0.24 (one reinforcement)", st.Activation)
	}
}

// TestTouchRevisions_UnknownIDsReportedNotRaised checks the partial-success
// contract, and does it against a mixed batch so the valid half is shown to
// still land — a call that reported the unknown ID and silently dropped the
// known one would pass a not-found-only test.
func TestTouchRevisions_UnknownIDsReportedNotRaised(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	rev, err := ms.WriteRevision(ctx, sampleInput("touch.mixed"))
	if err != nil {
		t.Fatal(err)
	}
	setActivation(t, ms, rev.MemoryID, 0.05)

	res, err := ms.TouchRevisions(ctx, []string{"01NOPE-not-a-revision", rev.RevisionID})
	if err != nil {
		t.Fatalf("a stale ID must not fail the call: %v", err)
	}
	if res.Touched != 1 {
		t.Errorf("Touched = %d, want 1 — the valid half of the batch must still land", res.Touched)
	}
	if len(res.NotFound) != 1 || res.NotFound[0] != "01NOPE-not-a-revision" {
		t.Errorf("NotFound = %v, want [01NOPE-not-a-revision]", res.NotFound)
	}
	st, _ := ms.GetState(ctx, rev.MemoryID)
	if st.Activation != 0.24 {
		t.Errorf("activation = %v, want 0.24", st.Activation)
	}
}

// TestTouchRevisions_RepeatedUnknownIDReportedOnce is the guard the two dedup
// tests above cannot provide.
//
// TouchRevisions dedups twice — once on revision ID, once on the memory each
// resolves to — and for KNOWN IDs the second pass subsumes the first, so
// removing the revision-level pass changes nothing observable there. It is the
// UNKNOWN ID that separates them: without the revision-level dedup a stale ID
// sent twice is reported twice, and a caller diffing not_found against what it
// sent would see a phantom.
func TestTouchRevisions_RepeatedUnknownIDReportedOnce(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	res, err := ms.TouchRevisions(ctx, []string{"01STALE", "01STALE", "01STALE"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.NotFound) != 1 || res.NotFound[0] != "01STALE" {
		t.Errorf("NotFound = %v for three copies of one stale ID, want [01STALE]", res.NotFound)
	}
}

// TestTouchRevisions_EmptyIsANoop pairs with the case above as its positive
// control: an empty request reports zero because there was nothing to do, and
// the same shape from a non-empty request would be a bug.
func TestTouchRevisions_EmptyIsANoop(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	res, err := ms.TouchRevisions(ctx, nil)
	if err != nil {
		t.Fatalf("TouchRevisions(nil): %v", err)
	}
	if res.Touched != 0 {
		t.Errorf("Touched = %d for an empty request, want 0", res.Touched)
	}
	if res.NotFound == nil {
		t.Error("NotFound is nil; it must always be a slice so JSON never carries null")
	}

	// The positive control: one real ID through the same call site does move.
	rev, err := ms.WriteRevision(ctx, sampleInput("touch.noop_control"))
	if err != nil {
		t.Fatal(err)
	}
	res, err = ms.TouchRevisions(ctx, []string{rev.RevisionID})
	if err != nil {
		t.Fatal(err)
	}
	if res.Touched != 1 {
		t.Errorf("control: Touched = %d, want 1 — zero above must mean 'nothing asked', not 'nothing works'",
			res.Touched)
	}
}

func TestTouchRevisions_RejectsOversizedBatch(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	ids := make([]string, memory.MaxTouchRevisions+1)
	for i := range ids {
		ids[i] = "id-" + strconv.Itoa(i)
	}
	_, err := ms.TouchRevisions(ctx, ids)
	if !errors.Is(err, memory.ErrInvalidInput) {
		t.Errorf("err = %v, want ErrInvalidInput for %d ids", err, len(ids))
	}

	// The boundary is inclusive: exactly MaxTouchRevisions is accepted. Without
	// this the cap could be off by one in the safe direction and nothing would
	// notice.
	_, err = ms.TouchRevisions(ctx, ids[:memory.MaxTouchRevisions])
	if err != nil {
		t.Errorf("exactly %d ids must be accepted, got %v", memory.MaxTouchRevisions, err)
	}
}

// TestTouchRevisions_SpansDomains is why the tool is tesseract_touch and not
// memory_touch: a revision ID names a row whether it was written as memory or as
// knowledge, and tesseract_lookup returns both.
func TestTouchRevisions_SpansDomains(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	memRev, err := ms.WriteRevision(ctx, sampleInput("touch.domain_memory"))
	if err != nil {
		t.Fatal(err)
	}
	knowIn := sampleInput("touch.domain_knowledge")
	knowIn.Domain = domains.Knowledge
	knowIn.Namespace = "user/chrispian/knowledge/portfolio"
	knowRev, err := ms.WriteRevision(ctx, knowIn)
	if err != nil {
		t.Fatalf("write knowledge revision: %v", err)
	}
	if knowRev.Domain != domains.Knowledge {
		t.Fatalf("test premise broken: knowledge write landed in domain %q", knowRev.Domain)
	}
	setActivation(t, ms, memRev.MemoryID, 0.05)
	setActivation(t, ms, knowRev.MemoryID, 0.05)

	res, err := ms.TouchRevisions(ctx, []string{memRev.RevisionID, knowRev.RevisionID})
	if err != nil {
		t.Fatal(err)
	}
	if res.Touched != 2 {
		t.Fatalf("Touched = %d across two domains, want 2 (NotFound=%v)", res.Touched, res.NotFound)
	}
	for _, id := range []string{memRev.MemoryID, knowRev.MemoryID} {
		st, _ := ms.GetState(ctx, id)
		if st.Activation != 0.24 {
			t.Errorf("memory %s: activation = %v, want 0.24", id, st.Activation)
		}
	}
}

// TestTouchRevisions_DoesNotReturnContent guards the shape rather than the
// behavior. Touch is a write from a read context; if it ever grows a content
// field it becomes a second read path, and callers will reach for it to hydrate
// — which would reinforce on retrieval, the thing recall refuses to do.
func TestTouchRevisions_DoesNotReturnContent(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	rev, err := ms.WriteRevision(ctx, sampleInput("touch.shape"))
	if err != nil {
		t.Fatal(err)
	}
	res, err := ms.TouchRevisions(ctx, []string{rev.RevisionID})
	if err != nil {
		t.Fatal(err)
	}
	// TouchResult has exactly two fields. Reflection would restate the struct;
	// this states the contract as prose the compiler checks: adding a field
	// breaks nothing here, but adding a CONTENT field cannot be done without
	// changing this literal.
	want := memory.TouchResult{Touched: 1, NotFound: []string{}}
	if res.Touched != want.Touched || len(res.NotFound) != len(want.NotFound) {
		t.Errorf("TouchResult = %+v, want %+v", res, want)
	}
}
