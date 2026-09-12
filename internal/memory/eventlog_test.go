package memory_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/tesseract/domains"
	"github.com/hollis-labs/tesseract/internal/memory"
)

const eventNS = "user/chrispian/event/reasoning"

func eventInput(summary string) memory.WriteInput {
	return memory.WriteInput{
		Domain:      domains.Event,
		Namespace:   eventNS,
		Author:      memory.Author{AgentID: "test-agent", AgentVersion: "1.0"},
		Trigger:     memory.TriggerManual,
		SessionID:   "manual:01HXXXXX",
		DerivedFrom: memory.DerivedFromObservation,
		Confidence:  0.9,
		Status:      memory.StatusCanonical,
		Payload:     memory.Payload{Summary: summary, Body: "reasoning: " + summary},
	}
}

// seedEvents writes n keyless events in order and returns their revision IDs,
// oldest first.
//
// Every write here gets a distinct created_at — each one runs a transaction, so
// nanosecond collisions do not happen in practice. That means nothing in this
// file exercises the keyset predicate's revision_id tiebreaker. Proving it
// needs a timestamp tie the write path cannot produce, so it lives in
// eventlog_internal_test.go, which can manufacture one.
func seedEvents(t *testing.T, ms *memory.Store, n int) []string {
	t.Helper()
	ctx := context.Background()
	ids := make([]string, 0, n)
	for i := 0; i < n; i++ {
		rev, err := ms.WriteRevision(ctx, eventInput(fmt.Sprintf("entry %02d", i)))
		if err != nil {
			t.Fatalf("seed event %d: %v", i, err)
		}
		ids = append(ids, rev.RevisionID)
	}
	return ids
}

func TestReadEventLog_NewestFirstIsTheDefault(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()

	ids := seedEvents(t, ms, 5)

	page, err := ms.ReadEventLog(context.Background(), memory.EventLogInput{
		Namespaces: []string{eventNS},
	})
	if err != nil {
		t.Fatalf("ReadEventLog: %v", err)
	}
	if page.Manifest.Direction != memory.LogNewestFirst {
		t.Errorf("direction = %q, want %q", page.Manifest.Direction, memory.LogNewestFirst)
	}
	if len(page.Entries) != 5 {
		t.Fatalf("entries = %d, want 5", len(page.Entries))
	}
	for i, rev := range page.Entries {
		want := ids[len(ids)-1-i]
		if rev.RevisionID != want {
			t.Errorf("entry[%d] = %s, want %s (newest first)", i, rev.RevisionID, want)
		}
	}
	if page.Manifest.HasMore {
		t.Error("has_more = true with every entry returned")
	}
	if page.Manifest.NextCursor != nil {
		t.Errorf("next_cursor = %q on a complete page, want null", *page.Manifest.NextCursor)
	}
}

func TestReadEventLog_OldestFirstIsTheReplayOrder(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()

	ids := seedEvents(t, ms, 5)

	page, err := ms.ReadEventLog(context.Background(), memory.EventLogInput{
		Namespaces: []string{eventNS},
		Direction:  memory.LogOldestFirst,
	})
	if err != nil {
		t.Fatalf("ReadEventLog: %v", err)
	}
	for i, rev := range page.Entries {
		if rev.RevisionID != ids[i] {
			t.Errorf("entry[%d] = %s, want %s (oldest first)", i, rev.RevisionID, ids[i])
		}
	}
}

// TestReadEventLog_PagingIsExactAcrossEveryPage walks the whole log one entry
// at a time and asserts it saw each revision exactly once, in order.
//
// A page size of 1 is the adversarial size for a keyset predicate: every page
// boundary falls between two rows, so an off-by-one in the comparison shows up
// as a skipped or repeated row rather than being masked by a wide page.
func TestReadEventLog_PagingIsExactAcrossEveryPage(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()

	ids := seedEvents(t, ms, 7)

	for _, dir := range []memory.LogDirection{memory.LogNewestFirst, memory.LogOldestFirst} {
		t.Run(string(dir), func(t *testing.T) {
			want := make([]string, len(ids))
			copy(want, ids)
			if dir == memory.LogNewestFirst {
				for i, j := 0, len(want)-1; i < j; i, j = i+1, j-1 {
					want[i], want[j] = want[j], want[i]
				}
			}

			var seen []string
			cursor := ""
			for page := 0; page < 20; page++ {
				got, err := ms.ReadEventLog(context.Background(), memory.EventLogInput{
					Namespaces: []string{eventNS},
					Direction:  dir,
					Limit:      1,
					Cursor:     cursor,
				})
				if err != nil {
					t.Fatalf("page %d: %v", page, err)
				}
				for _, rev := range got.Entries {
					seen = append(seen, rev.RevisionID)
				}
				if got.Manifest.NextCursor == nil {
					break
				}
				cursor = *got.Manifest.NextCursor
			}

			if len(seen) != len(want) {
				t.Fatalf("saw %d entries paging one at a time, want %d", len(seen), len(want))
			}
			for i := range want {
				if seen[i] != want[i] {
					t.Errorf("seen[%d] = %s, want %s", i, seen[i], want[i])
				}
			}
		})
	}
}

// TestReadEventLog_AppendsDoNotShiftAResumedRead is the property an offset
// cursor cannot give a log, and the reason this read exists rather than
// ranking=chronological.
//
// A log grows at the head. Under an offset cursor, writing one entry between
// page 1 and page 2 pushes every row down by one, so the reader sees the last
// row of page 1 again at the top of page 2. A keyset cursor names the position
// it stopped at, so the append is simply not in the sequence being walked.
func TestReadEventLog_AppendsDoNotShiftAResumedRead(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	seedEvents(t, ms, 4)

	first, err := ms.ReadEventLog(ctx, memory.EventLogInput{
		Namespaces: []string{eventNS},
		Limit:      2,
	})
	if err != nil {
		t.Fatalf("first page: %v", err)
	}
	if first.Manifest.NextCursor == nil {
		t.Fatal("first page issued no cursor with 4 entries and limit 2")
	}

	// The append that would corrupt an offset-paged read.
	if _, writeErr := ms.WriteRevision(ctx, eventInput("written mid-page")); writeErr != nil {
		t.Fatalf("append during paging: %v", writeErr)
	}

	second, err := ms.ReadEventLog(ctx, memory.EventLogInput{
		Namespaces: []string{eventNS},
		Limit:      2,
		Cursor:     *first.Manifest.NextCursor,
	})
	if err != nil {
		t.Fatalf("second page: %v", err)
	}

	seen := map[string]int{}
	for _, rev := range append(append([]memory.Revision{}, first.Entries...), second.Entries...) {
		seen[rev.RevisionID]++
	}
	for id, n := range seen {
		if n != 1 {
			t.Errorf("revision %s appeared %d times across two pages, want 1", id, n)
		}
	}
	if len(seen) != 4 {
		t.Errorf("saw %d distinct revisions across two pages of the pre-append log, want 4", len(seen))
	}
	for _, rev := range second.Entries {
		if rev.Payload.Summary == "written mid-page" {
			t.Error("an entry appended after the cursor was issued appeared in the resumed page; " +
				"the cursor is behaving like an offset, not a position")
		}
	}
}

func TestReadEventLog_CursorIsBoundToItsOrdering(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()

	seedEvents(t, ms, 4)

	page, err := ms.ReadEventLog(context.Background(), memory.EventLogInput{
		Namespaces: []string{eventNS},
		Limit:      2,
	})
	if err != nil {
		t.Fatalf("first page: %v", err)
	}
	if page.Manifest.NextCursor == nil {
		t.Fatal("no cursor issued")
	}

	// Same log, opposite direction: the position means something different in
	// a reversed sequence, so resuming it must be an error rather than a
	// plausible page.
	_, err = ms.ReadEventLog(context.Background(), memory.EventLogInput{
		Namespaces: []string{eventNS},
		Direction:  memory.LogOldestFirst,
		Limit:      2,
		Cursor:     *page.Manifest.NextCursor,
	})
	if !errors.Is(err, memory.ErrInvalidCursor) {
		t.Errorf("resuming a newest_first cursor under oldest_first: err = %v, want ErrInvalidCursor", err)
	}

	// A different namespace set selects different rows.
	_, err = ms.ReadEventLog(context.Background(), memory.EventLogInput{
		Namespaces: []string{"user/chrispian/event/journal"},
		Limit:      2,
		Cursor:     *page.Manifest.NextCursor,
	})
	if !errors.Is(err, memory.ErrInvalidCursor) {
		t.Errorf("resuming across a namespace change: err = %v, want ErrInvalidCursor", err)
	}

	// A garbage token is rejected rather than treated as the first page.
	_, err = ms.ReadEventLog(context.Background(), memory.EventLogInput{
		Namespaces: []string{eventNS},
		Cursor:     "not-a-cursor",
	})
	if !errors.Is(err, memory.ErrInvalidCursor) {
		t.Errorf("garbage cursor: err = %v, want ErrInvalidCursor", err)
	}
}

// TestReadEventLog_LimitDoesNotInvalidateACursor mirrors the rule DecodeCursor
// states for recall: limit sets where pages break, not what the sequence is.
func TestReadEventLog_LimitDoesNotInvalidateACursor(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()

	seedEvents(t, ms, 6)

	page, err := ms.ReadEventLog(context.Background(), memory.EventLogInput{
		Namespaces: []string{eventNS},
		Limit:      2,
	})
	if err != nil {
		t.Fatalf("first page: %v", err)
	}
	if page.Manifest.NextCursor == nil {
		t.Fatal("no cursor issued")
	}
	if _, err := ms.ReadEventLog(context.Background(), memory.EventLogInput{
		Namespaces: []string{eventNS},
		Limit:      4,
		Cursor:     *page.Manifest.NextCursor,
	}); err != nil {
		t.Errorf("resuming with a different limit: %v", err)
	}
}

func TestReadEventLog_TimeWindowBounds(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	var stamps []time.Time
	for i := 0; i < 3; i++ {
		rev, err := ms.WriteRevision(ctx, eventInput(fmt.Sprintf("windowed %d", i)))
		if err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
		stamps = append(stamps, rev.CreatedAt)
		time.Sleep(2 * time.Millisecond)
	}

	// Both bounds are inclusive, so a window pinned exactly to the middle
	// entry's timestamp selects it.
	page, err := ms.ReadEventLog(ctx, memory.EventLogInput{
		Namespaces: []string{eventNS},
		Since:      &stamps[1],
		Until:      &stamps[1],
	})
	if err != nil {
		t.Fatalf("ReadEventLog: %v", err)
	}
	if len(page.Entries) != 1 || page.Entries[0].Payload.Summary != "windowed 1" {
		t.Fatalf("inclusive window on one timestamp returned %d entries: %+v",
			len(page.Entries), page.Entries)
	}

	// An inverted window selects nothing and says so rather than answering an
	// empty page that reads like an empty log.
	_, err = ms.ReadEventLog(ctx, memory.EventLogInput{
		Namespaces: []string{eventNS},
		Since:      &stamps[2],
		Until:      &stamps[0],
	})
	if !errors.Is(err, memory.ErrInvalidInput) {
		t.Errorf("inverted window: err = %v, want ErrInvalidInput", err)
	}
}

// TestReadEventLog_DeprecatedEntriesAreRetracted is the domain's retraction
// story: event_list stops showing the entry, history still has it.
func TestReadEventLog_DeprecatedEntriesAreRetracted(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	keep, err := ms.WriteRevision(ctx, eventInput("kept"))
	if err != nil {
		t.Fatal(err)
	}
	retract, err := ms.WriteRevision(ctx, eventInput("retracted"))
	if err != nil {
		t.Fatal(err)
	}
	if depErr := ms.Deprecate(ctx, retract.RevisionID); depErr != nil {
		t.Fatalf("deprecate: %v", depErr)
	}

	page, err := ms.ReadEventLog(ctx, memory.EventLogInput{Namespaces: []string{eventNS}})
	if err != nil {
		t.Fatalf("ReadEventLog: %v", err)
	}
	if len(page.Entries) != 1 || page.Entries[0].RevisionID != keep.RevisionID {
		t.Fatalf("log returned %d entries after one was deprecated: %+v", len(page.Entries), page.Entries)
	}

	// Still retrievable by id — the store is append-only and deprecation is a
	// status, not a delete.
	if _, err := ms.GetRevisionByID(ctx, retract.RevisionID); err != nil {
		t.Errorf("a deprecated event revision should still be fetchable by id: %v", err)
	}
}

// TestReadEventLog_PrefixNamespaceReadsEveryStream exercises the shorthand the
// event grammar inherits from memory's.
func TestReadEventLog_PrefixNamespaceReadsEveryStream(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	reasoning := eventInput("in reasoning")
	if _, err := ms.WriteRevision(ctx, reasoning); err != nil {
		t.Fatal(err)
	}
	journal := eventInput("in journal")
	journal.Namespace = "user/chrispian/event/journal"
	if _, err := ms.WriteRevision(ctx, journal); err != nil {
		t.Fatal(err)
	}

	page, err := ms.ReadEventLog(ctx, memory.EventLogInput{
		Namespaces: []string{"user/chrispian/event"},
	})
	if err != nil {
		t.Fatalf("ReadEventLog: %v", err)
	}
	if len(page.Entries) != 2 {
		t.Errorf("prefix read returned %d entries, want both streams: %+v", len(page.Entries), page.Entries)
	}
}

// TestReadEventLog_ReadsOnlyTheEventDomain guards the domain bind. A memory
// revision in a namespace that happened to match must not appear in the log.
func TestReadEventLog_ReadsOnlyTheEventDomain(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	if _, err := ms.WriteRevision(ctx, eventInput("an event")); err != nil {
		t.Fatal(err)
	}
	if _, err := ms.WriteRevision(ctx, sampleInput("memory.entry")); err != nil {
		t.Fatal(err)
	}

	// A namespace list naming both. The memory row is excluded by the domain
	// bind, not by the namespace filter.
	page, err := ms.ReadEventLog(ctx, memory.EventLogInput{
		Namespaces: []string{eventNS, "user/chrispian/memory/notes"},
	})
	if err != nil {
		t.Fatalf("ReadEventLog: %v", err)
	}
	if len(page.Entries) != 1 {
		t.Fatalf("log returned %d entries, want only the event one: %+v", len(page.Entries), page.Entries)
	}
	if page.Entries[0].Domain != domains.Event {
		t.Errorf("entry domain = %q, want %q", page.Entries[0].Domain, domains.Event)
	}
}

func TestReadEventLog_RequiresANamespace(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()

	_, err := ms.ReadEventLog(context.Background(), memory.EventLogInput{})
	if !errors.Is(err, memory.ErrInvalidInput) {
		t.Errorf("no namespace: err = %v, want ErrInvalidInput", err)
	}

	_, err = ms.ReadEventLog(context.Background(), memory.EventLogInput{
		Namespaces: []string{eventNS},
		Direction:  "sideways",
	})
	if !errors.Is(err, memory.ErrInvalidInput) {
		t.Errorf("bad direction: err = %v, want ErrInvalidInput", err)
	}
}

func TestReadEventLog_LimitIsClamped(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()

	seedEvents(t, ms, 2)

	page, err := ms.ReadEventLog(context.Background(), memory.EventLogInput{
		Namespaces: []string{eventNS},
		Limit:      memory.MaxEventLogLimit + 1000,
	})
	if err != nil {
		t.Fatalf("ReadEventLog: %v", err)
	}
	if page.Manifest.Limit != memory.MaxEventLogLimit {
		t.Errorf("limit = %d, want it clamped to %d", page.Manifest.Limit, memory.MaxEventLogLimit)
	}

	page, err = ms.ReadEventLog(context.Background(), memory.EventLogInput{Namespaces: []string{eventNS}})
	if err != nil {
		t.Fatalf("ReadEventLog: %v", err)
	}
	if page.Manifest.Limit != memory.DefaultEventLogLimit {
		t.Errorf("limit = %d with none passed, want the default %d",
			page.Manifest.Limit, memory.DefaultEventLogLimit)
	}
}

// TestProjectEventLogPage_DeclaresItsProjection covers the one contract the
// envelope carries: an absent body means withheld, and the manifest says so.
func TestProjectEventLogPage_DeclaresItsProjection(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()

	seedEvents(t, ms, 2)
	page, err := ms.ReadEventLog(context.Background(), memory.EventLogInput{Namespaces: []string{eventNS}})
	if err != nil {
		t.Fatalf("ReadEventLog: %v", err)
	}

	full := memory.ProjectEventLogPage(page, memory.PayloadModeFull)
	if full.Manifest.PayloadMode != memory.PayloadModeFull {
		t.Errorf("manifest payload_mode = %q, want full", full.Manifest.PayloadMode)
	}
	revs, ok := full.Entries.([]memory.Revision)
	if !ok {
		t.Fatalf("full mode entries are %T, want []memory.Revision", full.Entries)
	}
	if len(revs) == 0 || revs[0].Payload.Body == "" {
		t.Error("full mode dropped the body, which is the field this domain exists for")
	}

	summary := memory.ProjectEventLogPage(page, memory.PayloadModeSummary)
	if summary.Manifest.PayloadMode != memory.PayloadModeSummary {
		t.Errorf("manifest payload_mode = %q, want summary", summary.Manifest.PayloadMode)
	}
	projected, ok := summary.Entries.([]memory.ProjectedRevision)
	if !ok {
		t.Fatalf("summary mode entries are %T, want []memory.ProjectedRevision", summary.Entries)
	}
	if len(projected) == 0 {
		t.Fatal("summary mode returned no entries")
	}
	if projected[0].Payload == nil || projected[0].Payload.Summary == "" {
		t.Error("summary mode dropped payload.summary")
	}
	if projected[0].Payload != nil && projected[0].Payload.Body != "" {
		t.Error("summary mode carried a body; it must be dropped, not truncated")
	}
}

// TestEventLogManifestHasNoTotal states the omission by hand, because a total
// is exactly the field somebody adds later to be helpful. Counting the rows a
// selection matches is a scan of the whole partition on every page — the work
// keyset paging exists to avoid — and on an append-only log the number would be
// stale before the caller read it.
func TestEventLogManifestHasNoTotal(t *testing.T) {
	want := []string{"Returned", "Limit", "Direction", "HasMore", "NextCursor"}
	ty := reflect.TypeOf(memory.EventLogManifest{})
	var got []string
	for i := 0; i < ty.NumField(); i++ {
		got = append(got, ty.Field(i).Name)
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("EventLogManifest fields = %v, want exactly %v.\n"+
			"In particular there is no Total: see the type's doc comment for why.", got, want)
	}
}
