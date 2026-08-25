package memory

// The recall comparators must be TOTAL orders. Offset paging (CW-20260825-0004)
// depends on it: an offset into a partial order skips one tied row and repeats
// another, with nothing in the response to say so.
//
// This test is internal because it must feed sortRecallResults a SHUFFLED
// input. Driving it through the store cannot: fetchCandidates issues no ORDER
// BY, so rows arrive in rowid order, and revision_ids are ULIDs that increase
// with insertion — SQLite's natural order already equals the tiebreak order,
// so a store-level test passes whether the tiebreaker is there or not. That was
// verified by deleting the tiebreaker and watching the store-level tests stay
// green.

import (
	"fmt"
	"sort"
	"testing"
	"time"
)

// permute deterministically reorders in place using a small xorshift sequence.
//
// A seeded math/rand would do the same job, but a test that must be
// reproducible on failure is better served by a generator with no package
// state at all — and it keeps the file free of a weak-RNG lint waiver.
func permute(n int, seed uint32, swap func(i, j int)) {
	x := seed | 1
	for i := n - 1; i > 0; i-- {
		x ^= x << 13
		x ^= x >> 17
		x ^= x << 5
		// x is masked to 31 bits before the conversion so the int cast can
		// never be negative on any platform.
		swap(i, int(x&0x7fffffff)%(i+1))
	}
}

// tiedResults builds n results that tie on BOTH sort keys — same timestamp,
// same score — in an order deliberately reversed from revision_id.
func tiedResults(n int) []RecallResult {
	ts := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	score := 0.5
	out := make([]RecallResult, 0, n)
	for i := n - 1; i >= 0; i-- {
		out = append(out, RecallResult{
			Revision: Revision{
				// Fixed width so lexical order matches numeric order.
				RevisionID: fmt.Sprintf("01M%04d", i),
				CreatedAt:  ts,
			},
			Score: &score,
		})
	}
	return out
}

func idsOf(results []RecallResult) []string {
	ids := make([]string, len(results))
	for i, r := range results {
		ids[i] = r.Revision.RevisionID
	}
	return ids
}

func isAscending(ids []string) bool {
	return sort.SliceIsSorted(ids, func(i, j int) bool { return ids[i] < ids[j] })
}

// Fully tied rows must come out ordered by revision_id regardless of the order
// they went in. Without the tiebreaker the input order survives, so the
// reversed input below comes out reversed.
func TestSortRecallResults_TotalOrder(t *testing.T) {
	for _, ranking := range []Ranking{
		RankingChronological, RankingActivation, RankingSimilarity,
	} {
		t.Run(string(ranking), func(t *testing.T) {
			results := tiedResults(12)
			if isAscending(idsOf(results)) {
				t.Fatal("test premise broken: input is already in tiebreak order")
			}
			sortRecallResults(results, ranking)
			got := idsOf(results)
			if !isAscending(got) {
				t.Errorf("tied rows not ordered by revision_id: %v", got)
			}
		})
	}
}

// Any input permutation must produce the same output permutation. This is the
// property an offset cursor actually relies on.
func TestSortRecallResults_ShuffleInvariant(t *testing.T) {
	for _, ranking := range []Ranking{RankingChronological, RankingActivation} {
		t.Run(string(ranking), func(t *testing.T) {
			base := tiedResults(16)
			sortRecallResults(base, ranking)
			want := idsOf(base)

			for pass := 0; pass < 25; pass++ {
				shuffled := tiedResults(16)
				permute(len(shuffled), uint32(pass)+1, func(i, j int) {
					shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
				})
				sortRecallResults(shuffled, ranking)
				got := idsOf(shuffled)
				for i := range got {
					if got[i] != want[i] {
						t.Fatalf("pass %d: shuffled input produced a different order\n got: %v\nwant: %v",
							pass, got, want)
					}
				}
			}
		})
	}
}

// The tiebreaker must not override the primary key. Distinct scores and
// distinct timestamps still sort by score/recency, best first.
func TestSortRecallResults_PrimaryKeyStillWins(t *testing.T) {
	ts := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	mk := func(id string, score float64, ageMinutes int) RecallResult {
		s := score
		return RecallResult{
			Revision: Revision{
				RevisionID: id,
				CreatedAt:  ts.Add(-time.Duration(ageMinutes) * time.Minute),
			},
			Score: &s,
		}
	}

	// "zzz" has the best score but the worst revision_id; it must still lead.
	scored := []RecallResult{mk("aaa", 0.1, 3), mk("zzz", 0.9, 1), mk("mmm", 0.5, 2)}
	sortRecallResults(scored, RankingActivation)
	if got := idsOf(scored); got[0] != "zzz" || got[2] != "aaa" {
		t.Errorf("score ordering broken by the tiebreaker: %v", got)
	}

	// Newest first for chronological, again against revision_id order.
	chrono := []RecallResult{mk("aaa", 0, 3), mk("zzz", 0, 1), mk("mmm", 0, 2)}
	sortRecallResults(chrono, RankingChronological)
	if got := idsOf(chrono); got[0] != "zzz" || got[2] != "aaa" {
		t.Errorf("chronological ordering broken by the tiebreaker: %v", got)
	}
}
