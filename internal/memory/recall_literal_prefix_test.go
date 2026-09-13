package memory_test

import (
	"context"
	"testing"

	"github.com/hollis-labs/tesseract/domains"
	"github.com/hollis-labs/tesseract/internal/memory"
)

func TestRecallPrefixMatchesCaseAndLiteralSegments(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	write := func(namespace, key string) memory.Revision {
		t.Helper()
		rev, err := ms.WriteRevision(ctx, memory.WriteInput{
			Domain: domains.Knowledge, Namespace: namespace, MemoryKey: key,
			Author: memory.Author{AgentID: "prefix-test"}, Trigger: memory.TriggerManual,
			SessionID: "prefix-test", DerivedFrom: memory.DerivedFromProject,
			Confidence: 0.9, Summary: key,
			Facets: memory.Facets{
				Kind: "note", Source: "manual",
				Pointer: &memory.Pointer{Scheme: "nil", Locator: key},
			},
		})
		if err != nil {
			t.Fatalf("WriteRevision(%q): %v", namespace, err)
		}
		return rev
	}

	lower := write("project/tesseract/knowledge/private/allowed", "prefix.case.allowed")
	write("project/tesseract/knowledge/Private/secret", "prefix.case.forbidden")
	literal := write(`project/tesseract/knowledge/literal_%\part/child`, "prefix.literal.allowed")
	write(`project/tesseract/knowledge/literal_AX\part/child`, "prefix.literal.forbidden")

	for _, tc := range []struct {
		name      string
		selector  string
		wantRevID string
	}{
		{"case sensitive", "project/tesseract/knowledge/private/*", lower.RevisionID},
		{"literal metacharacters", `project/tesseract/knowledge/literal_%\part/*`, literal.RevisionID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			results, err := ms.Recall(ctx, memory.RecallInput{
				Namespaces: []string{tc.selector},
				Ranking:    memory.RankingChronological,
			})
			if err != nil {
				t.Fatalf("Recall(%q): %v", tc.selector, err)
			}
			if len(results) != 1 || results[0].Revision.RevisionID != tc.wantRevID {
				t.Fatalf("Recall(%q) revisions=%v, want only %s", tc.selector, revisionIDs(results), tc.wantRevID)
			}
		})
	}
}

func revisionIDs(results []memory.RecallResult) []string {
	ids := make([]string, 0, len(results))
	for _, result := range results {
		ids = append(ids, result.Revision.RevisionID)
	}
	return ids
}
