package memory_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/hollis-labs/tesseract/domains"
	"github.com/hollis-labs/tesseract/internal/memory"
	"github.com/hollis-labs/tesseract/internal/typeregistry"
)

// The vocabularies moved to config. The write boundary did not.
//
// CW-20260909-0034 folded memory's {type} allowlist and the knowledge
// facet_kind map into the type registry, which reads types.yaml. That moves
// enforcement AUTHORITY — which values are allowed — from a Go literal to an
// operator's file, per [[config_is_policy_code_is_engine]]. It does not move
// enforcement. `Store.WriteRevision` is still the persistence boundary, still
// the only thing that has to be right, and it now consults a declaration
// instead of a hardcoded set.
//
// These tests are the pair that proves both halves. A vocabulary the operator
// NARROWS must start rejecting; one they WIDEN must start accepting, without a
// release. Either half alone would be satisfiable by a store that ignored the
// registry entirely or by one that had stopped checking.

// TestNarrowedKnowledgeVocabularyIsEnforcedAtTheWriteBoundary is the half that
// matters most, and the direct non-regression for CW-20260825-0022.
//
// `memory.Store.WriteRevision` is reachable from the exported Go facade, not
// just from the agent surfaces. It historically took an arbitrary facet_kind
// for domain=knowledge; CW-20260909-0033 closed that by routing facet
// validation through DomainPolicy. Moving the vocabulary to config is exactly
// the moment that could be reopened by accident — a registry lookup that
// silently returns "allowed" when it cannot find the vocabulary would look
// like a working refactor and be an open door.
func TestNarrowedKnowledgeVocabularyIsEnforcedAtTheWriteBoundary(t *testing.T) {
	// `note` is canonical by default. Take it away.
	narrowed := typeregistry.NewRegistry()
	if err := narrowed.LoadFromBytes([]byte(`
vocabularies:
  - vocabulary_id: knowledge.facet_kind
    closed: true
    types:
      - type_id: doc
`)); err != nil {
		t.Fatalf("LoadFromBytes: %v", err)
	}
	defer typeregistry.Install(narrowed)()

	ms, cleanup := newTestStore(t)
	defer cleanup()

	in := sampleInput("vocab.narrowed")
	in.Domain = domains.Knowledge
	in.Namespace = "user/chrispian/knowledge/framework"
	in.Facets = validKnowledgeFacets()
	in.Facets.Kind = "note"

	_, err := ms.WriteRevision(context.Background(), in)
	if err == nil {
		t.Fatal("a kind the operator removed from the vocabulary was written")
	}
	if !errors.Is(err, memory.ErrInvalidInput) {
		t.Fatalf("error = %v, want ErrInvalidInput", err)
	}
	// The rejection still names what IS allowed — a closed vocabulary that
	// only refuses is unusable, which is why the error renders the set.
	if !strings.Contains(err.Error(), "doc") {
		t.Errorf("rejection %q does not name the allowed set", err)
	}

	// And the surviving kind still writes: this narrowed, it did not close.
	in.Facets.Kind = "doc"
	if _, err := ms.WriteRevision(context.Background(), in); err != nil {
		t.Fatalf("the one declared kind was rejected: %v", err)
	}
}

// TestWidenedKnowledgeVocabularyNeedsNoRelease is the other half — the reason
// the trade was accepted. Adding a kind used to require a compile.
func TestWidenedKnowledgeVocabularyNeedsNoRelease(t *testing.T) {
	widened := typeregistry.NewRegistry()
	if err := widened.LoadFromBytes([]byte(`
vocabularies:
  - vocabulary_id: knowledge.facet_kind
    closed: true
    types:
      - type_id: doc
      - type_id: note
      - type_id: house_style
`)); err != nil {
		t.Fatalf("LoadFromBytes: %v", err)
	}
	defer typeregistry.Install(widened)()

	ms, cleanup := newTestStore(t)
	defer cleanup()

	in := sampleInput("vocab.widened")
	in.Domain = domains.Knowledge
	in.Namespace = "user/chrispian/knowledge/framework"
	in.Facets = validKnowledgeFacets()
	in.Facets.Kind = "house_style"

	rev, err := ms.WriteRevision(context.Background(), in)
	if err != nil {
		t.Fatalf("an operator-declared kind was rejected: %v", err)
	}
	if rev.Facets.Kind != "house_style" {
		t.Errorf("stored kind = %q, want house_style", rev.Facets.Kind)
	}
}

// TestMemoryTypeVocabularyFollowsTheRegistry is the same pair for the other
// folded vocabulary, at its own boundary: the namespace parser.
func TestMemoryTypeVocabularyFollowsTheRegistry(t *testing.T) {
	restore := memory.SetTypeAllowlist([]string{"decisions", "field_notes"})
	defer restore()

	if err := memory.ValidateNamespace("user/chrispian/memory/field_notes"); err != nil {
		t.Errorf("an operator-declared memory type was rejected: %v", err)
	}
	err := memory.ValidateNamespace("user/chrispian/memory/notes")
	if err == nil {
		t.Fatal("a memory type the operator removed still parsed")
	}
	if !errors.Is(err, memory.ErrInvalidNamespace) {
		t.Fatalf("error = %v, want ErrInvalidNamespace", err)
	}
	if !strings.Contains(err.Error(), "field_notes") {
		t.Errorf("rejection %q does not name the allowed set", err)
	}
}

// TestWriteBoundaryFailsClosedWhenTheVocabularyIsMissing pins the fail-closed
// reading, because the failure it prevents is silent.
//
// If a registry with no knowledge.facet_kind vocabulary answered "allowed" for
// everything, an operator who deleted or misnamed that block would get an OPEN
// vocabulary and no signal at all — writes would succeed and the corpus would
// fill with values governance never approved. The engine has to refuse instead.
func TestWriteBoundaryFailsClosedWhenTheVocabularyIsMissing(t *testing.T) {
	empty := typeregistry.NewRegistry()
	if err := empty.LoadVocabulary(typeregistry.Vocabulary{
		VocabularyID: typeregistry.VocabKnowledgeFacetKind,
		Closed:       true,
	}); err != nil {
		t.Fatalf("LoadVocabulary: %v", err)
	}
	defer typeregistry.Install(empty)()

	ms, cleanup := newTestStore(t)
	defer cleanup()

	in := sampleInput("vocab.empty")
	in.Domain = domains.Knowledge
	in.Namespace = "user/chrispian/knowledge/framework"
	in.Facets = validKnowledgeFacets()
	in.Facets.Kind = "note"

	if _, err := ms.WriteRevision(context.Background(), in); err == nil {
		t.Fatal("an empty vocabulary accepted a write; it must refuse everything")
	}
}

// TestSetTypeAllowlistPanicsOnAnInvalidList. The helper builds a registry and
// installs it; if the build fails and the error is swallowed, what gets
// installed carries the DEFAULT vocabulary. The override then silently does
// not apply and the calling test passes for the wrong reason — invisible in
// exactly the tests this helper exists to serve.
func TestSetTypeAllowlistPanicsOnAnInvalidList(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("SetTypeAllowlist accepted a list with an empty entry")
		}
		if !strings.Contains(fmt.Sprint(r), "SetTypeAllowlist") {
			t.Errorf("panic %v does not name the helper that rejected the input", r)
		}
	}()
	restore := memory.SetTypeAllowlist([]string{"decisions", ""})
	restore()
}
