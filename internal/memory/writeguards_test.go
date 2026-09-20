package memory_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/hollis-labs/tesseract/domains"
	"github.com/hollis-labs/tesseract/internal/memory"
)

const guardNamespace = "user/chrispian/memory/notes"

func historyLen(t *testing.T, ms *memory.Store, key string) int {
	t.Helper()
	hist, err := ms.GetHistoryInDomain(context.Background(), domains.Memory, guardNamespace, key)
	if err != nil {
		t.Fatalf("GetHistoryInDomain(%s): %v", key, err)
	}
	return len(hist)
}

// The default is unchanged: with no guard set a duplicate key still appends. The
// response now says so, and names the head it replaced.
func TestWriteGuards_OutcomeAndPreviousHeadOnUnguardedWrites(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	first, err := ms.WriteRevision(ctx, sampleInput("guards.outcome"))
	if err != nil {
		t.Fatalf("first write: %v", err)
	}
	if first.WriteOutcome != memory.WriteOutcomeCreated || first.PreviousRevisionID != "" {
		t.Errorf("first write: outcome=%q previous=%q, want created and no previous head",
			first.WriteOutcome, first.PreviousRevisionID)
	}

	dup := sampleInput("guards.outcome")
	dup.Summary = "written again, no guard"
	second, err := ms.WriteRevision(ctx, dup)
	if err != nil {
		t.Fatalf("an unguarded duplicate key must still succeed: %v", err)
	}
	if second.ItemID != first.ItemID {
		t.Errorf("duplicate key minted a new item: %s vs %s", second.ItemID, first.ItemID)
	}
	if second.WriteOutcome != memory.WriteOutcomeAppended || second.PreviousRevisionID != first.RevisionID {
		t.Errorf("duplicate write: outcome=%q previous=%q, want appended and previous=%s",
			second.WriteOutcome, second.PreviousRevisionID, first.RevisionID)
	}

	keyless, err := ms.WriteRevision(ctx, sampleInput(""))
	if err != nil {
		t.Fatalf("keyless write: %v", err)
	}
	if keyless.WriteOutcome != memory.WriteOutcomeCreated {
		t.Errorf("a keyless write always mints an item; outcome=%q", keyless.WriteOutcome)
	}

	// The two fields describe a write, not a revision: a read never carries them.
	read, err := ms.GetRevisionByID(ctx, second.RevisionID)
	if err != nil {
		t.Fatalf("GetRevisionByID: %v", err)
	}
	if read.WriteOutcome != "" || read.PreviousRevisionID != "" {
		t.Errorf("a read carries write-only fields: outcome=%q previous=%q", read.WriteOutcome, read.PreviousRevisionID)
	}
}

func TestWriteGuards_CreateOnlyRefusesAnExistingKey(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	fresh := sampleInput("guards.create")
	fresh.CreateOnly = true
	first, err := ms.WriteRevision(ctx, fresh)
	if err != nil {
		t.Fatalf("create_only on a new key must succeed: %v", err)
	}
	if first.WriteOutcome != memory.WriteOutcomeCreated {
		t.Errorf("outcome = %q, want created", first.WriteOutcome)
	}

	collide := sampleInput("guards.create")
	collide.Summary = "a different writer picked the same key"
	collide.CreateOnly = true
	_, err = ms.WriteRevision(ctx, collide)
	if !errors.Is(err, memory.ErrKeyConflict) {
		t.Fatalf("err = %v, want ErrKeyConflict", err)
	}
	var conflict *memory.WriteConflict
	if !errors.As(err, &conflict) {
		t.Fatalf("err does not carry a *WriteConflict: %v", err)
	}
	if conflict.ItemID != first.ItemID || conflict.CurrentRevisionID != first.RevisionID {
		t.Errorf("conflict names item=%q head=%q, want item=%q head=%q",
			conflict.ItemID, conflict.CurrentRevisionID, first.ItemID, first.RevisionID)
	}
	// The message is for a caller with one turn to recover.
	for _, needle := range []string{"create_only", first.RevisionID, "expected_revision_id"} {
		if !strings.Contains(err.Error(), needle) {
			t.Errorf("conflict message omits %q: %v", needle, err)
		}
	}

	// A refused write leaves nothing behind.
	if n := historyLen(t, ms, "guards.create"); n != 1 {
		t.Errorf("history length = %d after a refused write, want 1", n)
	}
	head, err := ms.GetCurrentInDomain(ctx, domains.Memory, guardNamespace, "guards.create")
	if err != nil {
		t.Fatalf("GetCurrentInDomain: %v", err)
	}
	if head.RevisionID != first.RevisionID {
		t.Errorf("head moved to %s despite the refusal", head.RevisionID)
	}
}

func TestWriteGuards_CreateOnlyOnAKeylessWriteIsTriviallySatisfied(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()

	in := sampleInput("")
	in.CreateOnly = true
	for i := 0; i < 2; i++ {
		rev, err := ms.WriteRevision(context.Background(), in)
		if err != nil {
			t.Fatalf("keyless create_only write %d: %v", i, err)
		}
		if rev.WriteOutcome != memory.WriteOutcomeCreated {
			t.Errorf("write %d outcome = %q, want created", i, rev.WriteOutcome)
		}
	}
}

func TestWriteGuards_ExpectedRevisionAcceptsTheHeadAndRefusesAStaleOne(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	base, err := ms.WriteRevision(ctx, sampleInput("guards.expected"))
	if err != nil {
		t.Fatalf("base write: %v", err)
	}

	// Writer A read `base` and edits it. The guard is independent of supersedes;
	// passing the same revision to both is the edit idiom.
	edit := sampleInput("guards.expected")
	edit.Summary = "writer A"
	edit.ExpectedRevisionID = base.RevisionID
	edit.Supersedes = base.RevisionID
	next, err := ms.WriteRevision(ctx, edit)
	if err != nil {
		t.Fatalf("a write against the current head must succeed: %v", err)
	}
	if next.WriteOutcome != memory.WriteOutcomeAppended || next.PreviousRevisionID != base.RevisionID {
		t.Errorf("outcome=%q previous=%q, want appended and previous=%s", next.WriteOutcome, next.PreviousRevisionID, base.RevisionID)
	}
	deprecated, err := ms.GetRevisionByID(ctx, base.RevisionID)
	if err != nil {
		t.Fatalf("GetRevisionByID: %v", err)
	}
	if deprecated.Status != memory.StatusDeprecated {
		t.Errorf("superseded base status = %q, want deprecated", deprecated.Status)
	}

	// Writer B also read `base`, and is now stale: this is G2, which used to succeed.
	stale := sampleInput("guards.expected")
	stale.Summary = "writer B, from the same base"
	stale.ExpectedRevisionID = base.RevisionID
	_, err = ms.WriteRevision(ctx, stale)
	if !errors.Is(err, memory.ErrRevisionConflict) {
		t.Fatalf("err = %v, want ErrRevisionConflict", err)
	}
	var conflict *memory.WriteConflict
	if !errors.As(err, &conflict) {
		t.Fatalf("err does not carry a *WriteConflict: %v", err)
	}
	if conflict.ItemID != next.ItemID || conflict.CurrentRevisionID != next.RevisionID || conflict.ExpectedRevisionID != base.RevisionID {
		t.Errorf("conflict = %+v, want item=%s head=%s expected=%s", conflict, next.ItemID, next.RevisionID, base.RevisionID)
	}
	if n := historyLen(t, ms, "guards.expected"); n != 2 {
		t.Errorf("history length = %d, want 2: the stale write must leave nothing behind", n)
	}

	// Unguarded, the same stale write still goes through — the default is unchanged.
	unguarded := sampleInput("guards.expected")
	unguarded.Summary = "no guard"
	if _, err := ms.WriteRevision(ctx, unguarded); err != nil {
		t.Errorf("an unguarded write must still succeed: %v", err)
	}
}

// An expectation about a head cannot hold where there is no head, and the
// refusal must not leave the key half-created behind it.
func TestWriteGuards_ExpectedRevisionOnAnAbsentKeyRefusesAndCreatesNothing(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	in := sampleInput("guards.absent")
	in.ExpectedRevisionID = "01HZZZZZZZZZZZZZZZZZZZZZZZ"
	_, err := ms.WriteRevision(ctx, in)
	if !errors.Is(err, memory.ErrRevisionConflict) {
		t.Fatalf("err = %v, want ErrRevisionConflict", err)
	}
	var conflict *memory.WriteConflict
	if !errors.As(err, &conflict) {
		t.Fatalf("err does not carry a *WriteConflict: %v", err)
	}
	if conflict.ItemID != "" || conflict.CurrentRevisionID != "" {
		t.Errorf("no item exists, yet the conflict names item=%q head=%q", conflict.ItemID, conflict.CurrentRevisionID)
	}
	if _, has := conflict.Details()["item_id"]; has {
		t.Error("Details() names an item for a key that has none")
	}

	// If the refused write had left its memory_state row behind, this would be an
	// append. It must be a create.
	again := sampleInput("guards.absent")
	again.CreateOnly = true
	rev, err := ms.WriteRevision(ctx, again)
	if err != nil {
		t.Fatalf("create_only after a refused expected_revision_id write: %v", err)
	}
	if rev.WriteOutcome != memory.WriteOutcomeCreated {
		t.Errorf("outcome = %q, want created: the refused write left a row behind", rev.WriteOutcome)
	}
}

func TestWriteGuards_InputValidation(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()

	tests := []struct {
		name  string
		tweak func(*memory.WriteInput)
		want  string
	}{
		{"both guards", func(in *memory.WriteInput) { in.CreateOnly = true; in.ExpectedRevisionID = "01HX" }, "cannot be combined"},
		{"expected_revision_id without a key", func(in *memory.WriteInput) { in.MemoryKey = ""; in.ExpectedRevisionID = "01HX" }, "requires a key"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			in := sampleInput("guards.validate")
			tc.tweak(&in)
			_, err := ms.WriteRevision(context.Background(), in)
			if !errors.Is(err, memory.ErrInvalidInput) {
				t.Fatalf("err = %v, want ErrInvalidInput", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want it to say %q", err, tc.want)
			}
		})
	}
}

// Several writers who all read the same head and all say "only if it is still
// that head": exactly one may win. This is the property the guard exists for.
//
// Measured over 150 rounds of 8 writers, every loser answered
// ErrRevisionConflict and none was turned away by SQLite. A busy refusal is
// still tolerated here, because it is a refusal and never a second success, and
// exactly-one is the property under test; the tolerance is not a claim that it
// happens.
func TestWriteGuards_ConcurrentWritersFromOneBaseOnlyOneWins(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	base, err := ms.WriteRevision(ctx, sampleInput("guards.race"))
	if err != nil {
		t.Fatalf("base write: %v", err)
	}

	const writers = 8
	errs := make([]error, writers)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			in := sampleInput("guards.race")
			in.Summary = fmt.Sprintf("writer %d", i)
			in.ExpectedRevisionID = base.RevisionID
			in.Supersedes = base.RevisionID
			<-start
			_, errs[i] = ms.WriteRevision(ctx, in)
		}()
	}
	close(start)
	wg.Wait()

	winners := 0
	for i, err := range errs {
		switch {
		case err == nil:
			winners++
		case errors.Is(err, memory.ErrRevisionConflict):
		default:
			msg := strings.ToLower(err.Error())
			if !strings.Contains(msg, "locked") && !strings.Contains(msg, "busy") {
				t.Errorf("writer %d failed with something other than a conflict or a busy refusal: %v", i, err)
			}
		}
	}
	if winners != 1 {
		t.Fatalf("%d writers succeeded from one base, want exactly 1; errs=%v", winners, errs)
	}
	if n := historyLen(t, ms, "guards.race"); n != 2 {
		t.Errorf("history length = %d, want 2 (the base and the one winner)", n)
	}
}
