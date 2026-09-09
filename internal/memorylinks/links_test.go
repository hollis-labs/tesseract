package memorylinks_test

import (
	"strings"
	"testing"

	"github.com/hollis-labs/tesseract/internal/memorylinks"
)

// The graph behavior is covered end to end in internal/memory (resolution,
// late binding, the recall expansion) and in internal/contextstore (the
// backfill). What is left here is the package's own small contract: the
// vocabulary it publishes, and the seam it joins summary to body on.

func TestVocabulary(t *testing.T) {
	got := memorylinks.Vocabulary()
	want := []string{"references", "supersedes"}
	if len(got) != len(want) {
		t.Fatalf("Vocabulary() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Vocabulary() = %v, want %v", got, want)
		}
	}
}

func TestRelationValid(t *testing.T) {
	for _, tc := range []struct {
		in   memorylinks.Relation
		want bool
	}{
		{memorylinks.RelationReferences, true},
		{memorylinks.RelationSupersedes, true},
		{"mentions", false},
		{"", false},
		{"References", false}, // the CHECK constraint is case-sensitive too
	} {
		if got := tc.in.Valid(); got != tc.want {
			t.Errorf("Relation(%q).Valid() = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// Every member of the published vocabulary must satisfy the CHECK constraint
// the schema declares, or a surface can advertise a value storage rejects.
func TestVocabularyMatchesTheSchemaConstraint(t *testing.T) {
	var ddl string
	for _, stmt := range memorylinks.Schema {
		if strings.Contains(stmt, "CREATE TABLE IF NOT EXISTS memory_links") {
			ddl = stmt
			break
		}
	}
	if ddl == "" {
		t.Fatal("memory_links DDL not found in Schema")
	}
	for _, rel := range memorylinks.Vocabulary() {
		if !strings.Contains(ddl, "'"+rel+"'") {
			t.Errorf("relation %q is in the vocabulary but not in the CHECK constraint", rel)
		}
	}
}

// The seam exists so a `[[` ending the summary cannot pair with a `]]`
// starting the body. Parse rejects a span containing a newline, so the join
// character is what enforces it.
func TestLinkTextSeparatesSummaryFromBody(t *testing.T) {
	got := memorylinks.LinkText("trailing [[", "spliced]] body")
	if !strings.Contains(got, "\n") {
		t.Fatal("LinkText joined without a newline; a link can now form across the seam")
	}
	if got != "trailing [[\nspliced]] body" {
		t.Errorf("LinkText = %q", got)
	}
}
