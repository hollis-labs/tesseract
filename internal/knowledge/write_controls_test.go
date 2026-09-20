package knowledge_test

import (
	"context"
	"errors"
	"testing"

	"github.com/hollis-labs/tesseract/internal/knowledge"
	"github.com/hollis-labs/tesseract/internal/memory"
)

// A writer that says nothing about status or derived_from gets exactly what
// every knowledge write was stamped before CW-20260912-0012. This is what keeps
// the existing corpus, and every existing writer, ranking as it did.
func TestWrite_StatusAndDerivedFromDefaultWhenOmitted(t *testing.T) {
	s := newTestStore(t)
	rev, err := s.Write(context.Background(), validInput())
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if rev.Status != memory.StatusCanonical {
		t.Errorf("Status = %q, want %q", rev.Status, memory.StatusCanonical)
	}
	if rev.DerivedFrom != memory.DerivedFromReference {
		t.Errorf("DerivedFrom = %q, want %q", rev.DerivedFrom, memory.DerivedFromReference)
	}
	if rev.Trigger != memory.TriggerManual {
		t.Errorf("Trigger = %q, want %q: trigger stays a constant", rev.Trigger, memory.TriggerManual)
	}
}

func TestWrite_StatusAndDerivedFromAreCallerControlled(t *testing.T) {
	for _, tc := range []struct {
		status memory.Status
		from   memory.DerivedFrom
	}{
		{memory.StatusDraft, memory.DerivedFromObservation},
		{memory.StatusReviewed, memory.DerivedFromProject},
		{memory.StatusCanonical, memory.DerivedFromUser},
		{memory.StatusDraft, memory.DerivedFromFeedback},
	} {
		t.Run(string(tc.status)+"/"+string(tc.from), func(t *testing.T) {
			s := newTestStore(t)
			in := validInput()
			in.Status, in.DerivedFrom = tc.status, tc.from
			rev, err := s.Write(context.Background(), in)
			if err != nil {
				t.Fatalf("Write: %v", err)
			}
			if rev.Status != tc.status || rev.DerivedFrom != tc.from {
				t.Errorf("returned status=%q derived_from=%q, want %q and %q", rev.Status, rev.DerivedFrom, tc.status, tc.from)
			}
			// Stored, not merely echoed.
			got, err := s.GetCurrent(context.Background(), in.Namespace, in.Key)
			if err != nil {
				t.Fatalf("GetCurrent: %v", err)
			}
			if got.Status != tc.status || got.DerivedFrom != tc.from {
				t.Errorf("stored status=%q derived_from=%q, want %q and %q", got.Status, got.DerivedFrom, tc.status, tc.from)
			}
		})
	}
}

func TestWrite_UnknownStatusOrDerivedFromIsRejected(t *testing.T) {
	s := newTestStore(t)
	for name, tweak := range map[string]func(*knowledge.WriteInput){
		"status":       func(in *knowledge.WriteInput) { in.Status = "bogus" },
		"derived_from": func(in *knowledge.WriteInput) { in.DerivedFrom = "bogus" },
	} {
		t.Run(name, func(t *testing.T) {
			in := validInput()
			tweak(&in)
			if _, err := s.Write(context.Background(), in); !errors.Is(err, memory.ErrInvalidInput) {
				t.Fatalf("err = %v, want ErrInvalidInput", err)
			}
		})
	}
}

// The two fields are recall ranking multipliers, so this is the test that they
// are more than labels: values a caller chooses must move the entry in what a
// later session reads. Feedback (1.3) outranks the reference default (0.9), and
// a draft (0.6) ranks below the canonical default.
func TestWrite_CallerChosenValuesReachRanking(t *testing.T) {
	s, mem := newTestStoreWithMemory(t)
	ctx := context.Background()

	write := func(key string, tweak func(*knowledge.WriteInput)) {
		in := validInput()
		in.Key = key
		in.Summary = "identical content for " + key
		tweak(&in)
		if _, err := s.Write(ctx, in); err != nil {
			t.Fatalf("Write(%s): %v", key, err)
		}
	}
	write("rank.default", func(*knowledge.WriteInput) {})
	write("rank.draft", func(in *knowledge.WriteInput) { in.Status = memory.StatusDraft })
	write("rank.feedback", func(in *knowledge.WriteInput) { in.DerivedFrom = memory.DerivedFromFeedback })

	results, err := mem.Recall(ctx, memory.RecallInput{
		Namespaces: []string{validInput().Namespace},
		Ranking:    memory.RankingActivation,
	})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	var got []string
	for _, r := range results {
		got = append(got, r.Revision.MemoryKey)
	}
	want := []string{"rank.feedback", "rank.default", "rank.draft"}
	if len(got) != len(want) {
		t.Fatalf("recall order = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("recall order = %v, want %v", got, want)
		}
	}
}

func TestWrite_GuardsAreOptInAndCarryThroughKnowledge(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	create := validInput()
	create.CreateOnly = true
	first, err := s.Write(ctx, create)
	if err != nil {
		t.Fatalf("create_only on a new key: %v", err)
	}
	if first.WriteOutcome != memory.WriteOutcomeCreated {
		t.Errorf("outcome = %q, want created", first.WriteOutcome)
	}

	// create_only refuses the collision that used to succeed silently (G1).
	collide := validInput()
	collide.Summary = "a different writer, same key"
	collide.CreateOnly = true
	_, err = s.Write(ctx, collide)
	if !errors.Is(err, memory.ErrKeyConflict) {
		t.Fatalf("err = %v, want ErrKeyConflict", err)
	}

	// expected_revision_id refuses the stale base that used to succeed (G2).
	edit := validInput()
	edit.Summary = "edit against the head"
	edit.ExpectedRevisionID = first.RevisionID
	edit.Supersedes = first.RevisionID
	next, err := s.Write(ctx, edit)
	if err != nil {
		t.Fatalf("a write against the current head: %v", err)
	}
	if next.WriteOutcome != memory.WriteOutcomeAppended || next.PreviousRevisionID != first.RevisionID {
		t.Errorf("outcome=%q previous=%q, want appended and previous=%s", next.WriteOutcome, next.PreviousRevisionID, first.RevisionID)
	}
	stale := validInput()
	stale.Summary = "edit from the base someone already replaced"
	stale.ExpectedRevisionID = first.RevisionID
	_, err = s.Write(ctx, stale)
	if !errors.Is(err, memory.ErrRevisionConflict) {
		t.Fatalf("err = %v, want ErrRevisionConflict", err)
	}

	// Off by default: the same stale write with no guard still succeeds, so no
	// existing writer changed behavior.
	unguarded := validInput()
	unguarded.Summary = "no guard asked for"
	if _, err = s.Write(ctx, unguarded); err != nil {
		t.Fatalf("an unguarded duplicate must still succeed: %v", err)
	}
}
