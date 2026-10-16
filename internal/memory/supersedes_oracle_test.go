package memory_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/hollis-labs/tesseract/internal/memory"
)

// A write that supersedes a revision living in another namespace must fail
// exactly like one that supersedes a missing revision: the caller is
// authorized for the namespace it writes to, and any other answer would
// confirm that a revision id exists elsewhere.
func TestSupersedesOutsideTheWrittenNamespaceLooksMissing(t *testing.T) {
	ms, cleanup := newTestStoreNoEmbedder(t)
	defer cleanup()
	ctx := context.Background()

	other := sampleInput("other.key")
	other.Namespace = "project/other/memory/notes"
	elsewhere, err := ms.WriteRevision(ctx, other)
	if err != nil {
		t.Fatal(err)
	}

	attempt := func(supersedes string) error {
		in := sampleInput("mine.key")
		in.Namespace = "project/mine/memory/notes"
		in.Supersedes = supersedes
		_, err := ms.WriteRevision(ctx, in)
		return err
	}
	const missingID = "01ZZZZZZZZZZZZZZZZZZZZZZZZ"
	denied := attempt(elsewhere.RevisionID)
	missing := attempt(missingID)
	if denied == nil || missing == nil {
		t.Fatalf("both writes must fail: denied=%v missing=%v", denied, missing)
	}
	if !errors.Is(denied, memory.ErrInvalidInput) || !errors.Is(missing, memory.ErrInvalidInput) {
		t.Fatalf("both must be ErrInvalidInput: %v / %v", denied, missing)
	}
	norm := func(err error, id string) string { return strings.ReplaceAll(err.Error(), id, "<id>") }
	if norm(denied, elsewhere.RevisionID) != norm(missing, missingID) {
		t.Fatalf("an out-of-namespace target is distinguishable from a missing one:\n denied:  %v\n missing: %v", denied, missing)
	}
	if strings.Contains(denied.Error(), "different memory") || strings.Contains(denied.Error(), "other") {
		t.Fatalf("denial leaks that the revision exists elsewhere: %v", denied)
	}
}

// Inside the written namespace the more helpful diagnosis stays.
func TestSupersedesAnotherMemoryInTheSameNamespaceKeepsItsMessage(t *testing.T) {
	ms, cleanup := newTestStoreNoEmbedder(t)
	defer cleanup()
	ctx := context.Background()
	first, err := ms.WriteRevision(ctx, sampleInput("first.key"))
	if err != nil {
		t.Fatal(err)
	}
	in := sampleInput("second.key")
	in.Supersedes = first.RevisionID
	_, err = ms.WriteRevision(ctx, in)
	if err == nil || !strings.Contains(err.Error(), "different memory") {
		t.Fatalf("same-namespace different-memory supersede: %v", err)
	}
}
