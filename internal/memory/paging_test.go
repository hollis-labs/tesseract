package memory_test

// Store-level tests for CW-20260825-0004's budget/cursor primitives. The
// surface-level contract (argument names, validation, MCP<->HTTP parity) is
// tested in internal/mcpadapter and internal/contextapi; this file covers the
// paging semantics those surfaces share.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/tesseract/domains"
	"github.com/hollis-labs/tesseract/internal/contextstore"
	"github.com/hollis-labs/tesseract/internal/memory"
)

// newPagingStore is newTestStore plus the raw handle, for the tie tests below
// that must forge a collision SQL would otherwise never produce.
func newPagingStore(t *testing.T) (*memory.Store, *sql.DB, func()) {
	t.Helper()
	cs, err := contextstore.Open(context.Background(), contextstore.Config{RootDir: t.TempDir()})
	if err != nil {
		t.Fatalf("contextstore.Open: %v", err)
	}
	ms := memory.NewStore(cs.DB(), nil, "", 0, memory.NoopQueue{})
	return ms, cs.DB(), func() { _ = cs.Close() }
}

func recallInput(ns string) memory.RecallInput {
	return memory.RecallInput{
		Namespaces: []string{ns},
		Ranking:    memory.RankingChronological,
	}
}

// seedN writes n keyed memories and returns them in write order.
func seedN(t *testing.T, ms *memory.Store, n int) []memory.Revision {
	t.Helper()
	out := make([]memory.Revision, 0, n)
	for i := 0; i < n; i++ {
		in := sampleInput("page.key" + string(rune('a'+i)))
		in.Payload.Summary = "seeded row " + string(rune('a'+i))
		rev, err := ms.WriteRevision(context.Background(), in)
		if err != nil {
			t.Fatalf("WriteRevision(%d): %v", i, err)
		}
		out = append(out, rev)
	}
	return out
}

// ── Cursor: the invalid-across-a-changed-sort requirement ────────────────────

// A cursor names a position in an ordering. Resuming it into a DIFFERENT
// ordering returns rows that look plausible and are wrong, with nothing in the
// response to say so. This is the acceptance criterion the ticket calls the
// sharp one, and these are the cases that must error.
func TestCursor_InvalidAcrossAChangedSort(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()
	seedN(t, ms, 6)

	base := recallInput("user/chrispian/memory/notes")
	first, err := ms.RecallPaged(context.Background(), base,
		memory.PageRequest{Limit: 2, PayloadMode: memory.PayloadModeSummary})
	if err != nil {
		t.Fatalf("first page: %v", err)
	}
	if first.Manifest.NextCursor == nil {
		t.Fatalf("first page issued no cursor; manifest = %+v", first.Manifest)
	}
	cursor := *first.Manifest.NextCursor

	changed := []struct {
		name string
		in   memory.RecallInput
	}{
		{"ranking", func() memory.RecallInput {
			c := base
			c.Ranking = memory.RankingActivation
			return c
		}()},
		{"revision_scope", func() memory.RecallInput {
			c := base
			c.RevisionScope = memory.RevisionScopeTimeline
			return c
		}()},
		{"namespaces", func() memory.RecallInput {
			c := base
			c.Namespaces = []string{"user/chrispian/memory/decisions"}
			return c
		}()},
		{"status filter", func() memory.RecallInput {
			c := base
			c.Filters.Statuses = []memory.Status{memory.StatusCanonical}
			return c
		}()},
		{"tag filter", func() memory.RecallInput {
			c := base
			c.Filters.Tags = []string{"whatever"}
			return c
		}()},
		{"confidence filter", func() memory.RecallInput {
			c := base
			c.Filters.ConfidenceMin = 0.5
			return c
		}()},
		{"since filter", func() memory.RecallInput {
			c := base
			ts := time.Now().Add(-time.Hour)
			c.Filters.Since = &ts
			return c
		}()},
		{"domain filter", func() memory.RecallInput {
			c := base
			c.Filters.Domains = []domains.Domain{domains.Knowledge}
			return c
		}()},
		{"reranker", func() memory.RecallInput {
			c := base
			c.Reranker = "some-reranker"
			return c
		}()},
	}
	for _, tc := range changed {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ms.RecallPaged(context.Background(), tc.in,
				memory.PageRequest{Cursor: cursor, Limit: 2, PayloadMode: memory.PayloadModeSummary})
			if !errors.Is(err, memory.ErrInvalidCursor) {
				t.Fatalf("resuming after a changed %s returned err=%v, want ErrInvalidCursor", tc.name, err)
			}
			if !strings.Contains(err.Error(), "different query") {
				t.Errorf("error does not tell the caller what happened: %v", err)
			}
		})
	}
}

// The mirror of the test above: a cursor must NOT be rejected for a change
// that cannot reorder anything. ProjectResults is a per-element map and limit
// only sets where pages break, so binding either would reject legitimate
// paging — browsing under keys then continuing under summary, or paging the
// same query with a different page size.
func TestCursor_SurvivesPayloadModeAndLimitChanges(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()
	seedN(t, ms, 6)

	base := recallInput("user/chrispian/memory/notes")
	first, err := ms.RecallPaged(context.Background(), base,
		memory.PageRequest{Limit: 2, PayloadMode: memory.PayloadModeKeys})
	if err != nil {
		t.Fatalf("first page: %v", err)
	}
	if first.Manifest.NextCursor == nil {
		t.Fatal("first page issued no cursor")
	}

	second, err := ms.RecallPaged(context.Background(), base, memory.PageRequest{
		Cursor:      *first.Manifest.NextCursor,
		Limit:       3, // different page size
		PayloadMode: memory.PayloadModeSummary,
	})
	if err != nil {
		t.Fatalf("resuming under a different payload_mode and limit must not error: %v", err)
	}
	if second.Manifest.ResultsReturned != 3 {
		t.Errorf("results_returned = %d, want 3", second.Manifest.ResultsReturned)
	}
	// And it must resume at the right place, not restart.
	if got := second.Kept[0].Revision.RevisionID; got == first.Kept[0].Revision.RevisionID {
		t.Errorf("second page restarted at the first row (%s)", got)
	}
}

// An omitted default and its explicit value are the same query. Fingerprinting
// the raw input rather than the resolved one would break a caller who spelled
// its defaults out on page two.
func TestCursor_ResolvedDefaultsFingerprintIdentically(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()
	seedN(t, ms, 4)

	implicit := memory.RecallInput{Namespaces: []string{"user/chrispian/memory/notes"}}
	first, err := ms.RecallPaged(context.Background(), implicit,
		memory.PageRequest{Limit: 2, PayloadMode: memory.PayloadModeSummary})
	if err != nil {
		t.Fatalf("first page: %v", err)
	}
	if first.Manifest.NextCursor == nil {
		t.Fatal("no cursor issued")
	}

	// Same query, defaults written out: no query means activation ranking,
	// current scope, and the three-status default.
	explicit := memory.RecallInput{
		Namespaces:    []string{"user/chrispian/memory/notes"},
		Ranking:       memory.RankingActivation,
		RevisionScope: memory.RevisionScopeCurrent,
		Filters: memory.RecallFilters{
			Statuses: []memory.Status{memory.StatusCanonical, memory.StatusReviewed, memory.StatusDraft},
		},
	}
	if _, err := ms.RecallPaged(context.Background(), explicit, memory.PageRequest{
		Cursor: *first.Manifest.NextCursor, Limit: 2, PayloadMode: memory.PayloadModeSummary,
	}); err != nil {
		t.Fatalf("spelling out the defaults must not invalidate the cursor: %v", err)
	}
}

// Filter ORDER is not a query change: namespaces and the set-semantics filters
// are canonicalized before hashing.
func TestCursor_FilterOrderIsNotAQueryChange(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()
	seedN(t, ms, 4)

	a := memory.RecallInput{
		Namespaces: []string{"user/chrispian/memory/notes", "user/chrispian/memory/decisions"},
		Ranking:    memory.RankingChronological,
	}
	b := a
	b.Namespaces = []string{"user/chrispian/memory/decisions", "user/chrispian/memory/notes"}

	if memory.RecallOrderingFingerprint(a) != memory.RecallOrderingFingerprint(b) {
		t.Error("reordering the namespace list changed the fingerprint")
	}
}

func TestCursor_MalformedTokensAreErrors(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()
	seedN(t, ms, 2)

	for _, tc := range []struct{ name, cursor string }{
		{"not base64", "!!!not-a-cursor!!!"},
		{"base64 but not JSON", "aGVsbG8gd29ybGQ"},
		{"JSON but not a cursor", "eyJoZWxsbyI6IndvcmxkIn0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ms.RecallPaged(context.Background(),
				recallInput("user/chrispian/memory/notes"),
				memory.PageRequest{Cursor: tc.cursor, PayloadMode: memory.PayloadModeSummary})
			if !errors.Is(err, memory.ErrInvalidCursor) {
				t.Fatalf("err = %v, want ErrInvalidCursor", err)
			}
		})
	}
}

// A cursor is opaque, and an offset that has run past the end is not an
// error — it is an empty final page. Erroring there would make the natural
// "page until next_cursor is null" loop fail on a corpus that shrank.
func TestCursor_OffsetPastTheEndIsAnEmptyPage(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()
	seedN(t, ms, 3)

	in := recallInput("user/chrispian/memory/notes")
	cursor := memory.EncodeCursor(99, memory.RecallOrderingFingerprint(in))
	page, err := ms.RecallPaged(context.Background(), in,
		memory.PageRequest{Cursor: cursor, PayloadMode: memory.PayloadModeSummary})
	if err != nil {
		t.Fatalf("RecallPaged: %v", err)
	}
	if page.Manifest.ResultsReturned != 0 {
		t.Errorf("results_returned = %d, want 0", page.Manifest.ResultsReturned)
	}
	if page.Manifest.Truncated {
		t.Error("an exhausted page must not claim truncation")
	}
	if page.Manifest.NextCursor != nil {
		t.Errorf("next_cursor = %q, want null", *page.Manifest.NextCursor)
	}
}

// ── Paging end to end ────────────────────────────────────────────────────────

// The ticket's second acceptance criterion: paging a namespace chronologically
// works end to end WITHOUT raising limit. Every row exactly once, no gaps, no
// repeats, and the loop terminates on next_cursor == nil.
func TestCursor_PagesEveryRowExactlyOnce(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()
	seeded := seedN(t, ms, 11)

	in := recallInput("user/chrispian/memory/notes")
	seen := []string{}
	cursor := ""
	for pages := 0; ; pages++ {
		if pages > 20 {
			t.Fatal("paging did not terminate")
		}
		page, err := ms.RecallPaged(context.Background(), in, memory.PageRequest{
			Cursor: cursor, Limit: 3, PayloadMode: memory.PayloadModeSummary,
		})
		if err != nil {
			t.Fatalf("page %d: %v", pages, err)
		}
		if page.Manifest.ResultsTotal != len(seeded) {
			t.Errorf("page %d: results_total = %d, want %d",
				pages, page.Manifest.ResultsTotal, len(seeded))
		}
		for _, r := range page.Kept {
			seen = append(seen, r.Revision.RevisionID)
		}
		// truncated and next_cursor are two readings of one fact.
		if page.Manifest.Truncated != (page.Manifest.NextCursor != nil) {
			t.Fatalf("page %d: truncated=%v but next_cursor=%v",
				pages, page.Manifest.Truncated, page.Manifest.NextCursor)
		}
		if page.Manifest.NextCursor == nil {
			break
		}
		cursor = *page.Manifest.NextCursor
	}

	if len(seen) != len(seeded) {
		t.Fatalf("paged %d rows, seeded %d", len(seen), len(seeded))
	}
	uniq := map[string]int{}
	for _, id := range seen {
		uniq[id]++
	}
	if len(uniq) != len(seeded) {
		t.Errorf("saw %d distinct rows across %d returned — a row repeated", len(uniq), len(seen))
	}
	for _, rev := range seeded {
		if uniq[rev.RevisionID] != 1 {
			t.Errorf("revision %s seen %d times, want exactly 1", rev.RevisionID, uniq[rev.RevisionID])
		}
	}
}

// The ordering must be a TOTAL order or offset paging is unsound even on a
// frozen corpus: tied rows would come back in whatever order fetchCandidates'
// unordered SQL produced, and an offset would then skip one row and repeat
// another with nothing in the response to indicate it.
//
// Asserting only that two identical calls agree is NOT enough to prove this —
// SQLite returns rows in a stable physical order for an unchanged database and
// sort.Slice is deterministic for a given input permutation, so a comparator
// with no tiebreaker passes that test while still being a partial order. What
// makes the assertion real is forcing genuine ties and then requiring the
// SPECIFIC order the tiebreaker imposes.
//
// Under activation, rows written from the same template tie exactly: the score
// is activation x status x confidence x origin x recency, and every factor is
// equal across them. The tiebreaker orders ties by ascending revision_id.
func TestRecall_TiedRowsOrderByRevisionID(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()
	seeded := seedN(t, ms, 12)

	results, err := ms.Recall(context.Background(), memory.RecallInput{
		Namespaces: []string{"user/chrispian/memory/notes"},
		Ranking:    memory.RankingActivation,
		Limit:      100,
	})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if len(results) != len(seeded) {
		t.Fatalf("recalled %d rows, seeded %d", len(results), len(seeded))
	}

	// Confirm the premise: these rows really do tie, so the assertion below
	// is about the tiebreaker and not about the scores.
	for i := 1; i < len(results); i++ {
		if results[i].Score == nil || results[0].Score == nil {
			t.Fatalf("activation ranking returned a nil score at %d", i)
		}
		if *results[i].Score != *results[0].Score {
			t.Skipf("rows did not tie (%v vs %v); this test needs ties to say anything",
				*results[i].Score, *results[0].Score)
		}
	}

	for i := 1; i < len(results); i++ {
		if results[i-1].Revision.RevisionID >= results[i].Revision.RevisionID {
			t.Fatalf("tied rows are not ordered by revision_id at %d: %s then %s",
				i, results[i-1].Revision.RevisionID, results[i].Revision.RevisionID)
		}
	}
}

// The chronological comparator needs the same property. created_at is
// nanosecond-precision so sequential writes never collide naturally; forcing
// the collision in SQL is the only way to exercise the tiebreaker at all.
func TestRecall_TiedTimestampsOrderByRevisionID(t *testing.T) {
	ms, db, cleanup := newPagingStore(t)
	defer cleanup()
	seeded := seedN(t, ms, 8)

	// Collapse every row onto one timestamp.
	var stamp string
	if err := db.QueryRowContext(context.Background(),
		`SELECT created_at FROM memory_revisions LIMIT 1`).Scan(&stamp); err != nil {
		t.Fatalf("read a timestamp: %v", err)
	}
	if _, err := db.ExecContext(context.Background(),
		`UPDATE memory_revisions SET created_at = ?`, stamp); err != nil {
		t.Fatalf("collapse timestamps: %v", err)
	}

	results, err := ms.Recall(context.Background(), memory.RecallInput{
		Namespaces: []string{"user/chrispian/memory/notes"},
		Ranking:    memory.RankingChronological,
		Limit:      100,
	})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if len(results) != len(seeded) {
		t.Fatalf("recalled %d rows, seeded %d", len(results), len(seeded))
	}
	for i := 1; i < len(results); i++ {
		if !results[i-1].Revision.CreatedAt.Equal(results[i].Revision.CreatedAt) {
			t.Fatalf("timestamps did not collapse; test premise broken")
		}
		if results[i-1].Revision.RevisionID >= results[i].Revision.RevisionID {
			t.Fatalf("tied rows are not ordered by revision_id at %d: %s then %s",
				i, results[i-1].Revision.RevisionID, results[i].Revision.RevisionID)
		}
	}
}

// And with ties present, paging must still cover every row exactly once —
// the property the tiebreaker exists to protect.
func TestCursor_PagesTiedRowsExactlyOnce(t *testing.T) {
	ms, db, cleanup := newPagingStore(t)
	defer cleanup()
	seeded := seedN(t, ms, 9)

	var stamp string
	if err := db.QueryRowContext(context.Background(),
		`SELECT created_at FROM memory_revisions LIMIT 1`).Scan(&stamp); err != nil {
		t.Fatalf("read a timestamp: %v", err)
	}
	if _, err := db.ExecContext(context.Background(),
		`UPDATE memory_revisions SET created_at = ?`, stamp); err != nil {
		t.Fatalf("collapse timestamps: %v", err)
	}

	in := recallInput("user/chrispian/memory/notes")
	seen := map[string]int{}
	cursor := ""
	for pages := 0; pages < 20; pages++ {
		page, err := ms.RecallPaged(context.Background(), in, memory.PageRequest{
			Cursor: cursor, Limit: 2, PayloadMode: memory.PayloadModeKeys,
		})
		if err != nil {
			t.Fatalf("page %d: %v", pages, err)
		}
		for _, r := range page.Kept {
			seen[r.Revision.RevisionID]++
		}
		if page.Manifest.NextCursor == nil {
			break
		}
		cursor = *page.Manifest.NextCursor
	}
	if len(seen) != len(seeded) {
		t.Errorf("paged %d distinct rows over a tied ordering, seeded %d", len(seen), len(seeded))
	}
	for _, rev := range seeded {
		if seen[rev.RevisionID] != 1 {
			t.Errorf("revision %s seen %d times, want exactly 1", rev.RevisionID, seen[rev.RevisionID])
		}
	}
}

// Repeated identical calls must agree. Weaker than the tie tests above, but it
// covers the whole pipeline rather than one comparator.
func TestRecall_OrderingIsDeterministic(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()
	seedN(t, ms, 12)

	for _, ranking := range []memory.Ranking{
		memory.RankingChronological, memory.RankingActivation,
	} {
		t.Run(string(ranking), func(t *testing.T) {
			in := memory.RecallInput{
				Namespaces: []string{"user/chrispian/memory/notes"},
				Ranking:    ranking,
				Limit:      100,
			}
			var first []string
			for pass := 0; pass < 5; pass++ {
				results, err := ms.Recall(context.Background(), in)
				if err != nil {
					t.Fatalf("Recall: %v", err)
				}
				ids := make([]string, len(results))
				for i, r := range results {
					ids[i] = r.Revision.RevisionID
				}
				if pass == 0 {
					first = ids
					continue
				}
				if strings.Join(ids, ",") != strings.Join(first, ",") {
					t.Fatalf("pass %d ordering differs from pass 0\n got: %v\nwant: %v", pass, ids, first)
				}
			}
		})
	}
}

// ── Budget ───────────────────────────────────────────────────────────────────

// The ticket's first acceptance criterion.
func TestBudget_TruncatesWithReasonAndCursor(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()
	seedN(t, ms, 10)

	in := recallInput("user/chrispian/memory/notes")
	unbounded, err := ms.RecallPaged(context.Background(), in,
		memory.PageRequest{Limit: 10, PayloadMode: memory.PayloadModeSummary})
	if err != nil {
		t.Fatalf("unbounded: %v", err)
	}
	if unbounded.Manifest.Truncated {
		t.Fatalf("baseline should not be truncated: %+v", unbounded.Manifest)
	}
	if unbounded.Manifest.NextCursor != nil {
		t.Fatal("baseline should carry no next_cursor")
	}

	// Half the bytes the full page needed.
	half := unbounded.Manifest.BytesReturned / 2
	bounded, err := ms.RecallPaged(context.Background(), in, memory.PageRequest{
		Limit:       10,
		PayloadMode: memory.PayloadModeSummary,
		Budget:      memory.Budget{Bytes: half},
	})
	if err != nil {
		t.Fatalf("bounded: %v", err)
	}
	if !bounded.Manifest.Truncated {
		t.Fatalf("a budget that cannot fit the page must report truncation: %+v", bounded.Manifest)
	}
	if bounded.Manifest.TruncationReason != memory.TruncationBudgetBytes {
		t.Errorf("truncation_reason = %q, want %q",
			bounded.Manifest.TruncationReason, memory.TruncationBudgetBytes)
	}
	if bounded.Manifest.NextCursor == nil {
		t.Error("a truncated page must carry a next_cursor")
	}
	if bounded.Manifest.BytesReturned > half {
		t.Errorf("bytes_returned %d exceeds the budget %d", bounded.Manifest.BytesReturned, half)
	}
	if bounded.Manifest.ResultsReturned >= unbounded.Manifest.ResultsReturned {
		t.Errorf("budget returned %d results, unbounded returned %d — nothing was withheld",
			bounded.Manifest.ResultsReturned, unbounded.Manifest.ResultsReturned)
	}
	if bounded.Manifest.ResultsTotal != unbounded.Manifest.ResultsTotal {
		t.Errorf("results_total moved under a budget: %d vs %d",
			bounded.Manifest.ResultsTotal, unbounded.Manifest.ResultsTotal)
	}
}

// bytes_returned must describe the array it is attached to, or the budget
// bounds nothing. This is the assertion that stops the envelope from being
// decorative.
func TestBudget_BytesReturnedMatchesTheSerializedArray(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()
	seedN(t, ms, 7)

	for _, mode := range []memory.PayloadMode{
		memory.PayloadModeKeys, memory.PayloadModeSummary, memory.PayloadModeFull,
	} {
		t.Run(string(mode), func(t *testing.T) {
			page, err := ms.RecallPaged(context.Background(),
				recallInput("user/chrispian/memory/notes"),
				memory.PageRequest{Limit: 7, PayloadMode: mode})
			if err != nil {
				t.Fatalf("RecallPaged: %v", err)
			}
			raw, err := json.Marshal(page.Results)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if len(raw) != page.Manifest.BytesReturned {
				t.Errorf("bytes_returned = %d, marshaled array = %d",
					page.Manifest.BytesReturned, len(raw))
			}
			if page.Manifest.TokensEstimate != memory.EstimateTokens(len(raw)) {
				t.Errorf("tokens_estimate = %d, want %d",
					page.Manifest.TokensEstimate, memory.EstimateTokens(len(raw)))
			}
		})
	}
}

// A budget too small for even one row must still return that row. Returning
// zero results plus a cursor at the same offset is an infinite loop that never
// makes progress.
func TestBudget_AlwaysReturnsAtLeastOneRow(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()
	seedN(t, ms, 5)

	page, err := ms.RecallPaged(context.Background(),
		recallInput("user/chrispian/memory/notes"),
		memory.PageRequest{
			Limit:       5,
			PayloadMode: memory.PayloadModeFull,
			Budget:      memory.Budget{Bytes: 1},
		})
	if err != nil {
		t.Fatalf("RecallPaged: %v", err)
	}
	if page.Manifest.ResultsReturned != 1 {
		t.Fatalf("results_returned = %d, want 1", page.Manifest.ResultsReturned)
	}
	if !page.Manifest.Truncated || page.Manifest.TruncationReason != memory.TruncationBudgetBytes {
		t.Errorf("an over-budget row must still be reported as truncated: %+v", page.Manifest)
	}
	if page.Manifest.NextCursor == nil {
		t.Fatal("no next_cursor — paging cannot make progress")
	}

	// And the loop must actually advance.
	next, err := ms.RecallPaged(context.Background(),
		recallInput("user/chrispian/memory/notes"),
		memory.PageRequest{
			Cursor:      *page.Manifest.NextCursor,
			Limit:       5,
			PayloadMode: memory.PayloadModeFull,
			Budget:      memory.Budget{Bytes: 1},
		})
	if err != nil {
		t.Fatalf("second page: %v", err)
	}
	if next.Kept[0].Revision.RevisionID == page.Kept[0].Revision.RevisionID {
		t.Error("paging under a tiny budget did not advance")
	}
}

// When both budgets are set the tighter one binds, and the reason names it.
func TestBudget_TokensBindsAndIsNamed(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()
	seedN(t, ms, 8)

	in := recallInput("user/chrispian/memory/notes")
	base, err := ms.RecallPaged(context.Background(), in,
		memory.PageRequest{Limit: 8, PayloadMode: memory.PayloadModeSummary})
	if err != nil {
		t.Fatalf("baseline: %v", err)
	}

	page, err := ms.RecallPaged(context.Background(), in, memory.PageRequest{
		Limit:       8,
		PayloadMode: memory.PayloadModeSummary,
		Budget: memory.Budget{
			Bytes:  base.Manifest.BytesReturned,      // would not bind
			Tokens: base.Manifest.TokensEstimate / 3, // binds
		},
	})
	if err != nil {
		t.Fatalf("RecallPaged: %v", err)
	}
	if page.Manifest.TruncationReason != memory.TruncationBudgetTokens {
		t.Errorf("truncation_reason = %q, want %q",
			page.Manifest.TruncationReason, memory.TruncationBudgetTokens)
	}
	if page.Manifest.TokensEstimate > base.Manifest.TokensEstimate/3 {
		t.Errorf("tokens_estimate %d exceeds the budget %d",
			page.Manifest.TokensEstimate, base.Manifest.TokensEstimate/3)
	}
}

// ── The limit cap ────────────────────────────────────────────────────────────

func TestClampRecallLimit(t *testing.T) {
	for _, tc := range []struct {
		name       string
		limit      int
		mode       memory.PayloadMode
		wantLimit  int
		wantCapped bool
	}{
		{"unspecified is the default, never capped", 0, memory.PayloadModeFull,
			memory.DefaultRecallLimit, false},
		{"negative is the default, never capped", -5, memory.PayloadModeFull,
			memory.DefaultRecallLimit, false},
		{"under the cap passes through", 50, memory.PayloadModeFull, 50, false},
		{"full caps at MaxRecallLimitFull", 500, memory.PayloadModeFull,
			memory.MaxRecallLimitFull, true},
		{"summary keeps the full ceiling", 500, memory.PayloadModeSummary, 500, false},
		{"keys keeps the full ceiling", 500, memory.PayloadModeKeys, 500, false},
		{"above the ceiling caps in every mode", 5000, memory.PayloadModeKeys,
			memory.MaxRecallLimit, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, capped := memory.ClampRecallLimit(tc.limit, tc.mode)
			if got != tc.wantLimit || capped != tc.wantCapped {
				t.Errorf("ClampRecallLimit(%d, %s) = (%d, %v), want (%d, %v)",
					tc.limit, tc.mode, got, capped, tc.wantLimit, tc.wantCapped)
			}
		})
	}
}

// The cap must never be silent: a caller who asked for 500 full results and
// got MaxRecallLimitFull is told which knob overrode them, and the rows past
// the cap stay reachable by paging.
//
// Seeds one row past the cap so the clamp actually withholds something —
// asserting the reason on a corpus smaller than the cap would pass while
// testing nothing.
func TestLimitCap_PayloadModeCapIsReportedAndPageable(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()

	const rows = memory.MaxRecallLimitFull + 3
	for i := 0; i < rows; i++ {
		in := sampleInput("cap.key")
		in.MemoryKey = "" // unkeyed: each write is its own memory
		in.Payload.Summary = "cap probe"
		if _, err := ms.WriteRevision(context.Background(), in); err != nil {
			t.Fatalf("WriteRevision(%d): %v", i, err)
		}
	}

	in := recallInput("user/chrispian/memory/notes")
	page, err := ms.RecallPaged(context.Background(), in,
		memory.PageRequest{Limit: 500, PayloadMode: memory.PayloadModeFull})
	if err != nil {
		t.Fatalf("RecallPaged: %v", err)
	}
	if page.Manifest.ResultsReturned != memory.MaxRecallLimitFull {
		t.Fatalf("results_returned = %d, want the cap %d",
			page.Manifest.ResultsReturned, memory.MaxRecallLimitFull)
	}
	if page.Manifest.ResultsTotal != rows {
		t.Errorf("results_total = %d, want %d — the cap must not hide the match count",
			page.Manifest.ResultsTotal, rows)
	}
	if page.Manifest.TruncationReason != memory.TruncationPayloadModeLimitCap {
		t.Errorf("truncation_reason = %q, want %q",
			page.Manifest.TruncationReason, memory.TruncationPayloadModeLimitCap)
	}
	if page.Manifest.NextCursor == nil {
		t.Fatal("the rows past the cap must stay reachable by paging")
	}

	// The same limit under a projected mode is not capped at all.
	projected, err := ms.RecallPaged(context.Background(), in,
		memory.PageRequest{Limit: 500, PayloadMode: memory.PayloadModeSummary})
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	if projected.Manifest.ResultsReturned != rows {
		t.Errorf("summary returned %d of %d — the full-mode cap leaked into a projected mode",
			projected.Manifest.ResultsReturned, rows)
	}

	// And the withheld rows are actually retrievable.
	rest, err := ms.RecallPaged(context.Background(), in, memory.PageRequest{
		Cursor: *page.Manifest.NextCursor, Limit: 500, PayloadMode: memory.PayloadModeFull,
	})
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if rest.Manifest.ResultsReturned != rows-memory.MaxRecallLimitFull {
		t.Errorf("second page returned %d, want %d",
			rest.Manifest.ResultsReturned, rows-memory.MaxRecallLimitFull)
	}
}

// A limit the caller chose, binding normally, reports `limit` — not the
// payload_mode cap. The two reasons must stay distinguishable.
func TestLimitCap_PlainLimitReportsLimit(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()
	seedN(t, ms, 6)

	page, err := ms.RecallPaged(context.Background(),
		recallInput("user/chrispian/memory/notes"),
		memory.PageRequest{Limit: 2, PayloadMode: memory.PayloadModeFull})
	if err != nil {
		t.Fatalf("RecallPaged: %v", err)
	}
	if !page.Manifest.Truncated {
		t.Fatalf("2 of 6 rows is truncated: %+v", page.Manifest)
	}
	if page.Manifest.TruncationReason != memory.TruncationLimit {
		t.Errorf("truncation_reason = %q, want %q (the cap did not bind here)",
			page.Manifest.TruncationReason, memory.TruncationLimit)
	}
}

// A cap that withholds nothing is not a truncation. Asking for 500 full
// results when only 6 exist loses no rows, so the manifest must stay clean.
func TestLimitCap_NotReportedWhenNothingIsWithheld(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()
	seedN(t, ms, 6)

	page, err := ms.RecallPaged(context.Background(),
		recallInput("user/chrispian/memory/notes"),
		memory.PageRequest{Limit: 500, PayloadMode: memory.PayloadModeFull})
	if err != nil {
		t.Fatalf("RecallPaged: %v", err)
	}
	if page.Manifest.Truncated {
		t.Errorf("nothing was withheld but truncated=true: %+v", page.Manifest)
	}
	if page.Manifest.TruncationReason != "" {
		t.Errorf("truncation_reason = %q, want empty", page.Manifest.TruncationReason)
	}
	if page.Manifest.NextCursor != nil {
		t.Errorf("next_cursor = %q, want null", *page.Manifest.NextCursor)
	}
}

// ── Manifest field presence ──────────────────────────────────────────────────

// Every manifest field is emitted even at its zero value. truncated:false is
// how a caller learns its result set is COMPLETE; next_cursor:null is how it
// learns there is nothing left. omitempty on either collapses the case the
// caller most needs — the defect class that has already been fixed three times
// in this domain.
func TestManifest_EmitsEveryFieldAtItsZeroValue(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()
	// Deliberately empty: no rows at all is the strongest zero case.

	page, err := ms.RecallPaged(context.Background(),
		recallInput("user/chrispian/memory/notes"),
		memory.PageRequest{PayloadMode: memory.PayloadModeSummary})
	if err != nil {
		t.Fatalf("RecallPaged: %v", err)
	}
	raw, err := json.Marshal(page.Manifest)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, want := range []string{
		`"results_total":0`,
		`"results_returned":0`,
		`"bytes_returned":`,
		`"tokens_estimate":`,
		`"truncated":false`,
		`"truncation_reason":""`,
		`"next_cursor":null`,
	} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("manifest does not carry %s; raw = %s", want, raw)
		}
	}
}

// truncated, truncation_reason and next_cursor are three readings of one
// fact. They must never disagree.
func TestManifest_TruncationSignalsAgree(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()
	seedN(t, ms, 9)

	for _, pr := range []memory.PageRequest{
		{PayloadMode: memory.PayloadModeSummary},
		{Limit: 2, PayloadMode: memory.PayloadModeSummary},
		{Limit: 9, PayloadMode: memory.PayloadModeSummary},
		{Limit: 9, PayloadMode: memory.PayloadModeFull, Budget: memory.Budget{Bytes: 400}},
		{Limit: 9, PayloadMode: memory.PayloadModeKeys, Budget: memory.Budget{Tokens: 20}},
		{Limit: 500, PayloadMode: memory.PayloadModeFull},
	} {
		page, err := ms.RecallPaged(context.Background(),
			recallInput("user/chrispian/memory/notes"), pr)
		if err != nil {
			t.Fatalf("RecallPaged(%+v): %v", pr, err)
		}
		m := page.Manifest
		if m.Truncated != (m.NextCursor != nil) {
			t.Errorf("%+v: truncated=%v but next_cursor=%v", pr, m.Truncated, m.NextCursor)
		}
		if m.Truncated != (m.TruncationReason != "") {
			t.Errorf("%+v: truncated=%v but reason=%q", pr, m.Truncated, m.TruncationReason)
		}
		if m.ResultsReturned != len(page.Kept) {
			t.Errorf("%+v: results_returned=%d but %d rows returned", pr, m.ResultsReturned, len(page.Kept))
		}
		if m.ResultsReturned > m.ResultsTotal {
			t.Errorf("%+v: returned %d of a total of %d", pr, m.ResultsReturned, m.ResultsTotal)
		}
	}
}

// ── History paging ───────────────────────────────────────────────────────────

func TestPageRevisions_LimitAndCursor(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()

	// Five revisions of one key.
	var last string
	for i := 0; i < 5; i++ {
		in := sampleInput("hist.key")
		in.Supersedes = last
		in.Payload.Summary = "revision " + string(rune('0'+i))
		rev, err := ms.WriteRevision(context.Background(), in)
		if err != nil {
			t.Fatalf("WriteRevision(%d): %v", i, err)
		}
		last = rev.RevisionID
	}
	revs, err := ms.GetHistory(context.Background(), "user/chrispian/memory/notes", "hist.key")
	if err != nil {
		t.Fatalf("GetHistory: %v", err)
	}
	if len(revs) != 5 {
		t.Fatalf("want 5 revisions, got %d", len(revs))
	}

	fp := memory.HistoryOrderingFingerprint(string(domains.Memory),
		"user/chrispian/memory/notes", "hist.key")

	seen := []string{}
	cursor := ""
	for pages := 0; ; pages++ {
		if pages > 10 {
			t.Fatal("history paging did not terminate")
		}
		page, err := memory.PageRevisions(revs, memory.PageRequest{Cursor: cursor, Limit: 2}, fp)
		if err != nil {
			t.Fatalf("page %d: %v", pages, err)
		}
		if page.Manifest.ResultsTotal != 5 {
			t.Errorf("results_total = %d, want 5", page.Manifest.ResultsTotal)
		}
		for _, r := range page.Results {
			seen = append(seen, r.RevisionID)
		}
		if page.Manifest.NextCursor == nil {
			break
		}
		cursor = *page.Manifest.NextCursor
	}
	if len(seen) != 5 {
		t.Errorf("paged %d revisions, want 5", len(seen))
	}
}

// A history cursor is bound to its series, so resuming one key's cursor
// against another key is an error rather than a wrong answer.
func TestPageRevisions_CursorIsBoundToItsSeries(t *testing.T) {
	fpA := memory.HistoryOrderingFingerprint(string(domains.Memory), "ns", "key.a")
	fpB := memory.HistoryOrderingFingerprint(string(domains.Memory), "ns", "key.b")
	fpKnowledge := memory.HistoryOrderingFingerprint(string(domains.Knowledge), "ns", "key.a")

	cursor := memory.EncodeCursor(1, fpA)
	revs := []memory.Revision{{RevisionID: "a"}, {RevisionID: "b"}, {RevisionID: "c"}}

	if _, err := memory.PageRevisions(revs, memory.PageRequest{Cursor: cursor, Limit: 1}, fpA); err != nil {
		t.Fatalf("same series must resume: %v", err)
	}
	if _, err := memory.PageRevisions(revs, memory.PageRequest{Cursor: cursor, Limit: 1}, fpB); !errors.Is(err, memory.ErrInvalidCursor) {
		t.Errorf("a different key must reject the cursor, got %v", err)
	}
	if _, err := memory.PageRevisions(revs, memory.PageRequest{Cursor: cursor, Limit: 1}, fpKnowledge); !errors.Is(err, memory.ErrInvalidCursor) {
		t.Errorf("a different domain must reject the cursor, got %v", err)
	}
}

func TestPageRequest_EngagedOnlyWhenAKnobIsPassed(t *testing.T) {
	for _, tc := range []struct {
		name string
		pr   memory.PageRequest
		want bool
	}{
		{"nothing passed", memory.PageRequest{}, false},
		{"payload_mode alone is not a paging knob",
			memory.PageRequest{PayloadMode: memory.PayloadModeFull}, false},
		{"limit", memory.PageRequest{Limit: 5}, true},
		{"cursor", memory.PageRequest{Cursor: "x"}, true},
		{"budget bytes", memory.PageRequest{Budget: memory.Budget{Bytes: 10}}, true},
		{"budget tokens", memory.PageRequest{Budget: memory.Budget{Tokens: 10}}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.pr.Engaged(); got != tc.want {
				t.Errorf("Engaged() = %v, want %v", got, tc.want)
			}
		})
	}
}

// ── The composition claim ────────────────────────────────────────────────────

// The ticket asserts that ranking=chronological + payload_mode=summary +
// cursor IS the linear history/log the episodic domain lacks, so no separate
// log tool is needed. A log has to be strictly ordered newest-first, cover
// every entry once, and stream to exhaustion. This checks all three against
// the composition rather than taking the claim on trust.
func TestChronologicalLogComposition(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()
	seedN(t, ms, 13)

	in := memory.RecallInput{
		Namespaces: []string{"user/chrispian/memory/notes"},
		Ranking:    memory.RankingChronological,
	}

	var stamps []time.Time
	var ids []string
	cursor := ""
	for pages := 0; ; pages++ {
		if pages > 30 {
			t.Fatal("log stream did not terminate")
		}
		page, err := ms.RecallPaged(context.Background(), in, memory.PageRequest{
			Cursor: cursor, Limit: 4, PayloadMode: memory.PayloadModeSummary,
		})
		if err != nil {
			t.Fatalf("page %d: %v", pages, err)
		}
		for _, r := range page.Kept {
			stamps = append(stamps, r.Revision.CreatedAt)
			ids = append(ids, r.Revision.RevisionID)
			// summary mode must carry the summary and withhold the body —
			// a log line, not a log payload.
			if r.Revision.Payload.Summary == "" {
				t.Errorf("row %s carried no summary", r.Revision.RevisionID)
			}
		}
		if page.Manifest.NextCursor == nil {
			break
		}
		cursor = *page.Manifest.NextCursor
	}

	if len(ids) != 13 {
		t.Fatalf("log streamed %d entries, want 13", len(ids))
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			t.Errorf("log repeated entry %s", id)
		}
		seen[id] = true
	}
	// Newest first, monotonically, ACROSS page boundaries — the property a
	// log has and a paged ranked search does not automatically inherit.
	for i := 1; i < len(stamps); i++ {
		if stamps[i].After(stamps[i-1]) {
			t.Errorf("entry %d (%s) is newer than entry %d (%s): stream is not ordered",
				i, stamps[i], i-1, stamps[i-1])
		}
	}
}

// The projected log line must actually be small — the composition is only a
// log if streaming it is cheaper than streaming the corpus.
func TestChronologicalLogComposition_SummaryIsCheaperThanFull(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()
	seedN(t, ms, 10)

	in := memory.RecallInput{
		Namespaces: []string{"user/chrispian/memory/notes"},
		Ranking:    memory.RankingChronological,
	}
	summary, err := ms.RecallPaged(context.Background(), in,
		memory.PageRequest{Limit: 10, PayloadMode: memory.PayloadModeSummary})
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	full, err := ms.RecallPaged(context.Background(), in,
		memory.PageRequest{Limit: 10, PayloadMode: memory.PayloadModeFull})
	if err != nil {
		t.Fatalf("full: %v", err)
	}
	if summary.Manifest.BytesReturned >= full.Manifest.BytesReturned {
		t.Errorf("summary (%d B) is not cheaper than full (%d B)",
			summary.Manifest.BytesReturned, full.Manifest.BytesReturned)
	}
	t.Logf("log line cost: summary=%d B full=%d B over %d rows",
		summary.Manifest.BytesReturned, full.Manifest.BytesReturned,
		summary.Manifest.ResultsReturned)
}
