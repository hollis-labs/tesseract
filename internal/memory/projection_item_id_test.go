package memory_test

import (
	"testing"
	"time"

	"github.com/hollis-labs/tesseract/domains"
	"github.com/hollis-labs/tesseract/internal/memory"
)

func TestProjectedResponsesCarryItemIDAliasInEveryProjectedMode(t *testing.T) {
	revision := memory.Revision{
		RevisionID: "rev-1",
		ItemID:     "item-1",
		MemoryID:   "item-1",
		Domain:     domains.Memory,
		Namespace:  "user/chrispian/memory/notes",
		CreatedAt:  time.Now(),
		Payload:    memory.Payload{Summary: "summary"},
	}
	for _, mode := range []memory.PayloadMode{memory.PayloadModeKeys, memory.PayloadModeSummary} {
		results := memory.ProjectResults([]memory.RecallResult{{Revision: revision}}, mode).([]memory.ProjectedResult)
		if got := results[0].Revision; got.ItemID != got.MemoryID || got.ItemID != revision.ItemID {
			t.Errorf("ProjectResults(%s) aliases = %q/%q, want %q", mode, got.ItemID, got.MemoryID, revision.ItemID)
		}
		revisions := memory.ProjectRevisions([]memory.Revision{revision}, mode).([]memory.ProjectedRevision)
		if got := revisions[0]; got.ItemID != got.MemoryID || got.ItemID != revision.ItemID {
			t.Errorf("ProjectRevisions(%s) aliases = %q/%q, want %q", mode, got.ItemID, got.MemoryID, revision.ItemID)
		}
	}
}
