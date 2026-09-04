package memory_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/hollis-labs/tesseract/internal/contextstore"
	"github.com/hollis-labs/tesseract/internal/knowledge"
	"github.com/hollis-labs/tesseract/internal/memory"
)

// --- fixture ------------------------------------------------------------

const healthNamespace = "user/tester/knowledge/pointers"

func newHealthFixture(t *testing.T) (*memory.Store, *knowledge.Store) {
	t.Helper()
	dir := t.TempDir()
	cs, err := contextstore.Open(context.Background(), contextstore.Config{RootDir: dir})
	if err != nil {
		t.Fatalf("contextstore.Open: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	ms := memory.NewStore(cs.DB(), nil, "", 0, memory.NoopQueue{})
	return ms, knowledge.New(ms)
}

// seedKnowledge writes one knowledge entry with the given pointer and returns
// its revision. `kind` must be canonical — the write path enforces the closed
// vocabulary (CW-20260825-0014).
func seedKnowledge(t *testing.T, ks *knowledge.Store, key, scheme, locator string) memory.Revision {
	t.Helper()
	rev, err := ks.Write(context.Background(), knowledge.WriteInput{
		Namespace: healthNamespace,
		Key:       key,
		Kind:      "note",
		Source:    "manual",
		Pointer:   memory.Pointer{Scheme: scheme, Locator: locator},
		Summary:   "fixture entry " + key,
		Body:      "the body is the durable half",
		Author:    memory.Author{AgentID: "test", AgentVersion: "1"},
		SessionID: "test:pointer-health",
	})
	if err != nil {
		t.Fatalf("knowledge write %s: %v", key, err)
	}
	return rev
}

func recordObservation(t *testing.T, db *sql.DB, revisionID, scheme, locator string, outcome memory.PointerOutcome, detail string, at time.Time) {
	t.Helper()
	plan := memory.VerificationPlan{Rows: []memory.PointerObservation{{
		RevisionID: revisionID, Scheme: scheme, Locator: locator,
		Outcome: outcome, Detail: detail, CheckedAt: at,
	}}}
	if _, err := memory.ApplyVerificationPlan(context.Background(), db, plan); err != nil {
		t.Fatalf("apply observation: %v", err)
	}
}

// --- the five states ----------------------------------------------------

// TestPointerHealth_FiveStatesAreDistinct is the core of the ticket's
// "absence is information" requirement. Every state must be reachable AND
// distinguishable from the others through the public read surface.
func TestPointerHealth_FiveStatesAreDistinct(t *testing.T) {
	ms, ks := newHealthFixture(t)
	db := ms.DB()
	ctx := context.Background()
	now := time.Now().UTC()

	resolved := seedKnowledge(t, ks, "e.resolved", "file", "/tmp/whatever")
	dead := seedKnowledge(t, ks, "e.dead", "file", "/tmp/gone")
	murky := seedKnowledge(t, ks, "e.murky", "https", "https://example.test/thing")
	never := seedKnowledge(t, ks, "e.never", "file", "/tmp/unlooked")
	selfContained := seedKnowledge(t, ks, "e.self", memory.SchemeNil, "no-external-source")

	recordObservation(t, db, resolved.RevisionID, "file", "/tmp/whatever", memory.OutcomeResolved, "stat_ok", now)
	recordObservation(t, db, dead.RevisionID, "file", "/tmp/gone", memory.OutcomeUnresolvable, "not_found", now)
	recordObservation(t, db, murky.RevisionID, "https", "https://example.test/thing", memory.OutcomeUnverifiable, "timeout", now)
	// `never` deliberately gets no row.
	// `selfContained` deliberately gets no row — and must not read as `never`.

	results, err := ms.Recall(ctx, memory.RecallInput{
		Namespaces: []string{healthNamespace}, Ranking: memory.RankingChronological, Limit: 50,
	})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}

	byID := map[string]*memory.PointerHealth{}
	for _, r := range results {
		byID[r.Revision.RevisionID] = r.PointerHealth
	}

	want := map[string]memory.PointerHealthStatus{
		resolved.RevisionID:      memory.PointerHealthResolved,
		dead.RevisionID:          memory.PointerHealthUnresolvable,
		murky.RevisionID:         memory.PointerHealthUnverifiable,
		never.RevisionID:         memory.PointerHealthUnchecked,
		selfContained.RevisionID: memory.PointerHealthNotApplicable,
	}
	for id, wantStatus := range want {
		h := byID[id]
		if h == nil {
			t.Errorf("revision %s: pointer_health absent, want %q", id, wantStatus)
			continue
		}
		if h.Status != wantStatus {
			t.Errorf("revision %s: status %q, want %q", id, h.Status, wantStatus)
		}
	}

	// The distinctions that carry the design.
	if byID[never.RevisionID].Status == byID[dead.RevisionID].Status {
		t.Error("never-checked and verified-missing collapsed to one status — absence must stay expressible")
	}
	if byID[never.RevisionID].Status == byID[selfContained.RevisionID].Status {
		t.Error("a nil-scheme entry reads as never-checked; the recommended pattern must not sit in the suspect pile")
	}
	if byID[murky.RevisionID].Status == byID[dead.RevisionID].Status {
		t.Error("a timeout collapsed into dead — this is the failure that makes the field untrustworthy")
	}

	// checked_at rides only on real observations.
	if byID[never.RevisionID].CheckedAt != nil {
		t.Error("unchecked carries a checked_at; it is not an observation")
	}
	if byID[selfContained.RevisionID].CheckedAt != nil {
		t.Error("not_applicable carries a checked_at; it is not an observation")
	}
	if byID[dead.RevisionID].CheckedAt == nil {
		t.Error("a recorded observation lost its checked_at")
	}
}

// TestPointerHealth_AbsentFieldMeansNoPointer separates "this record has no
// pointer" from every health state. A memory-domain revision has no pointer
// facet at all and must not acquire a status.
func TestPointerHealth_AbsentFieldMeansNoPointer(t *testing.T) {
	ms, _ := newHealthFixture(t)
	ctx := context.Background()

	if _, err := ms.WriteRevision(ctx, memory.WriteInput{
		Namespace:  "user/tester/memory/notes",
		MemoryKey:  "plain.note",
		Author:     memory.Author{AgentID: "test", AgentVersion: "1"},
		Trigger:    memory.TriggerExplicit,
		SessionID:  "test:pointer-health",
		Origin:     memory.OriginUser,
		Confidence: 0.5,
		Status:     memory.StatusCanonical,
		Payload:    memory.Payload{Summary: "no pointer here"},
	}); err != nil {
		t.Fatalf("WriteRevision: %v", err)
	}

	results, err := ms.Recall(ctx, memory.RecallInput{
		Namespaces: []string{"user/tester/memory/notes"}, Ranking: memory.RankingChronological, Limit: 10,
	})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	if results[0].PointerHealth != nil {
		t.Errorf("memory revision carries pointer_health %+v; absent must mean \"no pointer\", "+
			"not a health verdict on something that does not exist", results[0].PointerHealth)
	}
}

// TestPointerHealth_LatestObservationWinsAndHistorySurvives proves the
// append-only claim from the read side: a later observation changes what a
// reader sees, and the earlier one is still on disk.
func TestPointerHealth_LatestObservationWinsAndHistorySurvives(t *testing.T) {
	ms, ks := newHealthFixture(t)
	db := ms.DB()
	ctx := context.Background()

	rev := seedKnowledge(t, ks, "e.flapping", "https", "https://example.test/flappy")
	yesterday := time.Now().UTC().Add(-24 * time.Hour)
	recordObservation(t, db, rev.RevisionID, "https", "https://example.test/flappy", memory.OutcomeResolved, "http_200", yesterday)
	recordObservation(t, db, rev.RevisionID, "https", "https://example.test/flappy", memory.OutcomeUnverifiable, "timeout", time.Now().UTC())

	results, err := ms.Recall(ctx, memory.RecallInput{
		Namespaces: []string{healthNamespace}, Ranking: memory.RankingChronological, Limit: 10,
	})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	h := results[0].PointerHealth
	if h.Status != memory.PointerHealthUnverifiable {
		t.Fatalf("status %q, want %q (latest observation wins)", h.Status, memory.PointerHealthUnverifiable)
	}
	// The transient discriminator: it resolved yesterday, so this is a blip
	// and not a pointer that has never worked.
	if h.LastResolvedAt == nil {
		t.Fatal("last_resolved_at is absent; without it a blip is indistinguishable from a pointer that never resolved")
	}
	if h.LastResolvedAt.Before(yesterday.Add(-time.Minute)) || h.LastResolvedAt.After(yesterday.Add(time.Minute)) {
		t.Errorf("last_resolved_at = %v, want ~%v", h.LastResolvedAt, yesterday)
	}

	// And the earlier row was not overwritten.
	var n int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM pointer_verifications WHERE revision_id = ?`, rev.RevisionID).Scan(&n); err != nil {
		t.Fatalf("count observations: %v", err)
	}
	if n != 2 {
		t.Errorf("observation log holds %d row(s) for the revision, want 2 — verification must append, never overwrite", n)
	}
}

func TestPointerHealth_LastResolvedAtComparesRFC3339NanoChronologically(t *testing.T) {
	ms, ks := newHealthFixture(t)
	ctx := context.Background()
	rev := seedKnowledge(t, ks, "e.resolved-prefix", "https", "https://example.test/prefix")

	shorter := time.Date(2026, 9, 4, 13, 34, 55, 92_340_000, time.UTC)
	later := time.Date(2026, 9, 4, 13, 34, 55, 92_342_000, time.UTC)
	recordObservation(t, ms.DB(), rev.RevisionID, "https", "https://example.test/prefix", memory.OutcomeResolved, "first", shorter)
	recordObservation(t, ms.DB(), rev.RevisionID, "https", "https://example.test/prefix", memory.OutcomeResolved, "second", later)

	results, err := ms.Recall(ctx, memory.RecallInput{
		Namespaces: []string{healthNamespace}, Ranking: memory.RankingChronological, Limit: 10,
	})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if len(results) != 1 || results[0].PointerHealth == nil {
		t.Fatalf("pointer health result = %+v, want one observed pointer", results)
	}
	if got := results[0].PointerHealth.LastResolvedAt; got == nil || !got.Equal(later) {
		t.Fatalf("last_resolved_at = %v, want later instant %s", got, later.Format(time.RFC3339Nano))
	}
}

// TestPointerHealth_VerificationNeverTouchesTheRevision is the append-only
// guarantee from the write side. Recording an observation must leave
// memory_revisions byte-for-byte alone, including the write-time assertion in
// facet_pointer_resolved_at, which stays what it honestly is.
func TestPointerHealth_VerificationNeverTouchesTheRevision(t *testing.T) {
	ms, ks := newHealthFixture(t)
	db := ms.DB()
	ctx := context.Background()

	rev := seedKnowledge(t, ks, "e.immutable", "file", "/tmp/nope")

	snapshot := func() string {
		var s string
		if err := db.QueryRowContext(ctx, `
SELECT COALESCE(facet_pointer_scheme,'')||'|'||COALESCE(facet_pointer_locator,'')||'|'||
       COALESCE(facet_pointer_resolved_at,'')||'|'||COALESCE(status,'')||'|'||COALESCE(supersedes,'')
FROM memory_revisions WHERE revision_id = ?`, rev.RevisionID).Scan(&s); err != nil {
			t.Fatalf("snapshot: %v", err)
		}
		return s
	}
	var revisionCount = func() int {
		var n int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM memory_revisions`).Scan(&n); err != nil {
			t.Fatalf("count revisions: %v", err)
		}
		return n
	}

	before, beforeN := snapshot(), revisionCount()
	recordObservation(t, db, rev.RevisionID, "file", "/tmp/nope", memory.OutcomeUnresolvable, "not_found", time.Now().UTC())
	after, afterN := snapshot(), revisionCount()

	if before != after {
		t.Errorf("recording a verification mutated the revision row:\n before %q\n after  %q", before, after)
	}
	if beforeN != afterN {
		t.Errorf("recording a verification changed the revision count %d -> %d; "+
			"an observation must not mint a revision", beforeN, afterN)
	}
}

// TestPointerHealth_UnreadableLogRendersUncheckedNotAbsent covers the
// best-effort degradation path, which had no test.
//
// When the verification log cannot be read, recall must still return its
// results — but it must not OMIT pointer_health. Absent is contractually "this
// revision has no pointer", a claim about the record; an unreadable log is a
// claim about us. Omitting it makes a whole knowledge namespace read as
// pointer-free, which is false and, from the caller's side, unfalsifiable.
//
// `unchecked` is the honest rendering: nobody has looked, and on this path
// nobody could. It is the value this package created for exactly that.
func TestPointerHealth_UnreadableLogRendersUncheckedNotAbsent(t *testing.T) {
	ms, ks := newHealthFixture(t)
	db := ms.DB()
	ctx := context.Background()
	now := time.Now().UTC()

	dead := seedKnowledge(t, ks, "deg.dead", "file", "/tmp/deg-dead")
	live := seedKnowledge(t, ks, "deg.live", "file", "/tmp/deg-live")
	selfContained := seedKnowledge(t, ks, "deg.self", memory.SchemeNil, "none")
	recordObservation(t, db, dead.RevisionID, "file", "/tmp/deg-dead", memory.OutcomeUnresolvable, "not_found", now)
	recordObservation(t, db, live.RevisionID, "file", "/tmp/deg-live", memory.OutcomeResolved, "stat_ok", now)

	// Sanity: with the log readable, the two file pointers disagree. If they
	// did not, the assertion after the break would prove nothing.
	before, err := ms.Recall(ctx, memory.RecallInput{
		Namespaces: []string{healthNamespace}, Ranking: memory.RankingChronological, Limit: 10,
	})
	if err != nil {
		t.Fatalf("Recall (before): %v", err)
	}
	seen := map[memory.PointerHealthStatus]bool{}
	for _, r := range before {
		if r.PointerHealth != nil {
			seen[r.PointerHealth.Status] = true
		}
	}
	if !seen[memory.PointerHealthUnresolvable] || !seen[memory.PointerHealthResolved] {
		t.Fatalf("fixture did not produce distinct statuses: %v", seen)
	}

	// Break the log the way an unmigrated or damaged store would.
	if _, err = db.ExecContext(ctx, `ALTER TABLE pointer_verifications RENAME TO pointer_verifications_gone`); err != nil {
		t.Fatalf("rename log away: %v", err)
	}

	after, err := ms.Recall(ctx, memory.RecallInput{
		Namespaces: []string{healthNamespace}, Ranking: memory.RankingChronological, Limit: 10,
	})
	if err != nil {
		t.Fatalf("recall must survive an unreadable verification log, got: %v", err)
	}
	if len(after) != 3 {
		t.Fatalf("got %d results, want 3 — recall must still return its rows", len(after))
	}

	byID := map[string]*memory.PointerHealth{}
	for _, r := range after {
		byID[r.Revision.RevisionID] = r.PointerHealth
	}

	for _, id := range []string{dead.RevisionID, live.RevisionID} {
		h := byID[id]
		if h == nil {
			t.Errorf("revision %s: pointer_health omitted when the log was unreadable.\n"+
				"    Absent means \"this revision has no pointer\" — an entire knowledge namespace\n"+
				"    would read as pointer-free. Want %q.", id, memory.PointerHealthUnchecked)
			continue
		}
		if h.Status != memory.PointerHealthUnchecked {
			t.Errorf("revision %s: status %q, want %q", id, h.Status, memory.PointerHealthUnchecked)
		}
		if h.CheckedAt != nil {
			t.Errorf("revision %s: unchecked carries a checked_at", id)
		}
	}

	// The scheme-derived state needs no log at all and must be unaffected.
	if h := byID[selfContained.RevisionID]; h == nil || h.Status != memory.PointerHealthNotApplicable {
		t.Errorf("nil-scheme revision: got %+v, want %q — this state is derived from the scheme "+
			"and does not depend on the log", h, memory.PointerHealthNotApplicable)
	}
}

// --- the filter ---------------------------------------------------------

// TestPointerHealth_FilterEnumeratesRatherThanSamples is the acceptance
// criterion "discoverable by query rather than by failure", tested at the one
// place it can silently fail: the interaction with limit.
//
// The ORDER the fixture seeds in is the whole test. Ranking is chronological
// (newest first), so the dead entries are written FIRST and the live ones
// after, which puts every dead revision outside the top-`limit` window. A
// post-LIMIT filter therefore returns zero here, while the shipped SQL filter
// returns all of them.
//
// Seeding the other way round is the trap: the dead entries are then the
// newest, they all fall inside the window, and a post-LIMIT filter returns
// exactly the right answer for the wrong reason. This test previously did
// that and could not detect the failure it exists to detect. The
// precondition below pins the ordering so it cannot silently regress again.
func TestPointerHealth_FilterEnumeratesRatherThanSamples(t *testing.T) {
	ms, ks := newHealthFixture(t)
	db := ms.DB()
	ctx := context.Background()
	now := time.Now().UTC()

	const deadCount = 7
	const liveCount = 20
	const limit = 10

	// Dead first => oldest => outside the newest-first limit window.
	var deadIDs []string
	for i := 0; i < deadCount; i++ {
		r := seedKnowledge(t, ks, fmt.Sprintf("dead.%02d", i), "file", fmt.Sprintf("/tmp/dead-%02d", i))
		recordObservation(t, db, r.RevisionID, "file", fmt.Sprintf("/tmp/dead-%02d", i), memory.OutcomeUnresolvable, "not_found", now)
		deadIDs = append(deadIDs, r.RevisionID)
	}
	for i := 0; i < liveCount; i++ {
		r := seedKnowledge(t, ks, fmt.Sprintf("live.%02d", i), "file", fmt.Sprintf("/tmp/live-%02d", i))
		recordObservation(t, db, r.RevisionID, "file", fmt.Sprintf("/tmp/live-%02d", i), memory.OutcomeResolved, "stat_ok", now)
	}

	// Precondition: with no filter, the top `limit` must contain NONE of the
	// dead revisions. If this ever stops holding, the assertion below is
	// satisfiable by a post-LIMIT filter and proves nothing.
	unfiltered, err := ms.Recall(ctx, memory.RecallInput{
		Namespaces: []string{healthNamespace},
		Ranking:    memory.RankingChronological,
		Limit:      limit,
	})
	if err != nil {
		t.Fatalf("Recall (precondition): %v", err)
	}
	inWindow := map[string]bool{}
	for _, r := range unfiltered {
		inWindow[r.Revision.RevisionID] = true
	}
	for _, id := range deadIDs {
		if inWindow[id] {
			t.Fatalf("fixture no longer isolates the filter: dead revision %s is inside the top-%d window, "+
				"so a post-LIMIT filter would pass this test", id, limit)
		}
	}

	results, err := ms.Recall(ctx, memory.RecallInput{
		Namespaces: []string{healthNamespace},
		Ranking:    memory.RankingChronological,
		Limit:      limit,
		Filters:    memory.RecallFilters{PointerHealth: []string{string(memory.PointerHealthUnresolvable)}},
	})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if len(results) != deadCount {
		t.Fatalf("filter returned %d dead pointer(s), want %d — the filter must apply before limit, "+
			"or \"show me the dead ones\" silently becomes \"show me the dead ones that ranked well\"",
			len(results), deadCount)
	}
	got := map[string]bool{}
	for _, r := range results {
		got[r.Revision.RevisionID] = true
		if r.PointerHealth == nil || r.PointerHealth.Status != memory.PointerHealthUnresolvable {
			t.Errorf("revision %s came back from an unresolvable filter with health %+v", r.Revision.RevisionID, r.PointerHealth)
		}
	}
	for _, id := range deadIDs {
		if !got[id] {
			t.Errorf("dead revision %s missing from the filtered result set", id)
		}
	}
}

// TestPointerHealth_FilterCoversEveryState walks the whole vocabulary through
// the SQL filter and asserts each one selects exactly the revisions whose
// surfaced status matches.
//
// This is the binding test between the two renderings of the derivation rule
// — DerivePointerHealth in Go and pointerHealthStatusExpr in SQL. They are
// separate code, and this is what stops them drifting.
func TestPointerHealth_FilterCoversEveryState(t *testing.T) {
	ms, ks := newHealthFixture(t)
	db := ms.DB()
	ctx := context.Background()
	now := time.Now().UTC()

	resolved := seedKnowledge(t, ks, "s.resolved", "file", "/tmp/a")
	dead := seedKnowledge(t, ks, "s.dead", "file", "/tmp/b")
	murky := seedKnowledge(t, ks, "s.murky", "https", "https://example.test/c")
	never := seedKnowledge(t, ks, "s.never", "file", "/tmp/d")
	selfContained := seedKnowledge(t, ks, "s.self", memory.SchemeNil, "none")

	recordObservation(t, db, resolved.RevisionID, "file", "/tmp/a", memory.OutcomeResolved, "stat_ok", now)
	recordObservation(t, db, dead.RevisionID, "file", "/tmp/b", memory.OutcomeUnresolvable, "not_found", now)
	recordObservation(t, db, murky.RevisionID, "https", "https://example.test/c", memory.OutcomeUnverifiable, "http_403", now)

	// Ground truth: what the Go derivation says, read off an unfiltered recall.
	all, err := ms.Recall(ctx, memory.RecallInput{
		Namespaces: []string{healthNamespace}, Ranking: memory.RankingChronological, Limit: 100,
	})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	goStatus := map[string]memory.PointerHealthStatus{}
	for _, r := range all {
		if r.PointerHealth != nil {
			goStatus[r.Revision.RevisionID] = r.PointerHealth.Status
		}
	}
	expectSeeded := map[string]memory.PointerHealthStatus{
		resolved.RevisionID:      memory.PointerHealthResolved,
		dead.RevisionID:          memory.PointerHealthUnresolvable,
		murky.RevisionID:         memory.PointerHealthUnverifiable,
		never.RevisionID:         memory.PointerHealthUnchecked,
		selfContained.RevisionID: memory.PointerHealthNotApplicable,
	}
	for id, want := range expectSeeded {
		if goStatus[id] != want {
			t.Fatalf("fixture is wrong: revision %s derived %q, want %q", id, goStatus[id], want)
		}
	}

	for _, status := range memory.PointerHealthStatusVocabulary() {
		filtered, ferr := ms.Recall(ctx, memory.RecallInput{
			Namespaces: []string{healthNamespace}, Ranking: memory.RankingChronological, Limit: 100,
			Filters: memory.RecallFilters{PointerHealth: []string{status}},
		})
		if ferr != nil {
			t.Fatalf("Recall(%s): %v", status, ferr)
		}
		var sqlSelected, goExpected []string
		for _, r := range filtered {
			sqlSelected = append(sqlSelected, r.Revision.RevisionID)
		}
		for id, s := range goStatus {
			if string(s) == status {
				goExpected = append(goExpected, id)
			}
		}
		sort.Strings(sqlSelected)
		sort.Strings(goExpected)
		if fmt.Sprint(sqlSelected) != fmt.Sprint(goExpected) {
			t.Errorf("status %q: SQL filter selected %v, Go derivation says %v — the two renderings of the rule have drifted",
				status, sqlSelected, goExpected)
		}
	}
}

// TestPointerHealth_FilterIsNotANoOp guards against the filter silently
// matching everything, which would make every assertion above vacuous.
func TestPointerHealth_FilterIsNotANoOp(t *testing.T) {
	ms, ks := newHealthFixture(t)
	db := ms.DB()
	ctx := context.Background()

	live := seedKnowledge(t, ks, "n.live", "file", "/tmp/x")
	recordObservation(t, db, live.RevisionID, "file", "/tmp/x", memory.OutcomeResolved, "stat_ok", time.Now().UTC())
	seedKnowledge(t, ks, "n.unchecked", "file", "/tmp/y")

	unfiltered, err := ms.Recall(ctx, memory.RecallInput{
		Namespaces: []string{healthNamespace}, Ranking: memory.RankingChronological, Limit: 100,
	})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if len(unfiltered) != 2 {
		t.Fatalf("fixture: got %d results, want 2", len(unfiltered))
	}
	dead, err := ms.Recall(ctx, memory.RecallInput{
		Namespaces: []string{healthNamespace}, Ranking: memory.RankingChronological, Limit: 100,
		Filters: memory.RecallFilters{PointerHealth: []string{string(memory.PointerHealthUnresolvable)}},
	})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if len(dead) != 0 {
		t.Errorf("filtering for unresolvable in a corpus with none returned %d row(s) — the filter is not filtering", len(dead))
	}
}

// TestPointerHealth_FilterAppliesUnderRelevanceRanking covers the second
// recall path. Relevance recall runs its own SQL through the BM25 arm; a
// filter wired into only one path would pass every test above and fail in the
// default ranking mode for a query.
func TestPointerHealth_FilterAppliesUnderRelevanceRanking(t *testing.T) {
	ms, ks := newHealthFixture(t)
	db := ms.DB()
	ctx := context.Background()
	now := time.Now().UTC()

	dead := seedKnowledge(t, ks, "r.dead", "file", "/tmp/dead-relevance")
	live := seedKnowledge(t, ks, "r.live", "file", "/tmp/live-relevance")
	recordObservation(t, db, dead.RevisionID, "file", "/tmp/dead-relevance", memory.OutcomeUnresolvable, "not_found", now)
	recordObservation(t, db, live.RevisionID, "file", "/tmp/live-relevance", memory.OutcomeResolved, "stat_ok", now)

	results, err := ms.Recall(ctx, memory.RecallInput{
		Namespaces: []string{healthNamespace},
		Ranking:    memory.RankingRelevance,
		Query:      "fixture entry",
		Limit:      100,
		Filters:    memory.RecallFilters{PointerHealth: []string{string(memory.PointerHealthUnresolvable)}},
	})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if len(results) != 1 || results[0].Revision.RevisionID != dead.RevisionID {
		t.Fatalf("relevance path returned %d result(s); want exactly the dead one (%s)", len(results), dead.RevisionID)
	}
	if results[0].PointerHealth == nil || results[0].PointerHealth.Status != memory.PointerHealthUnresolvable {
		t.Errorf("relevance path lost pointer_health: %+v", results[0].PointerHealth)
	}
}

// --- projection ---------------------------------------------------------

// TestPointerHealth_ProjectionCarriesUnderDefaultButNotKeys pins the decision
// about which modes carry the signal. Summary is DefaultPayloadMode, so this
// is what makes it discoverable without opting in.
func TestPointerHealth_ProjectionCarriesUnderDefaultButNotKeys(t *testing.T) {
	ms, ks := newHealthFixture(t)
	db := ms.DB()
	ctx := context.Background()

	rev := seedKnowledge(t, ks, "p.dead", "file", "/tmp/projected")
	recordObservation(t, db, rev.RevisionID, "file", "/tmp/projected", memory.OutcomeUnresolvable, "not_found", time.Now().UTC())

	results, err := ms.Recall(ctx, memory.RecallInput{
		Namespaces: []string{healthNamespace}, Ranking: memory.RankingChronological, Limit: 10,
	})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}

	hasHealth := func(mode memory.PayloadMode) bool {
		raw, mErr := json.Marshal(memory.ProjectResults(results, mode))
		if mErr != nil {
			t.Fatalf("marshal %s: %v", mode, mErr)
		}
		var docs []map[string]json.RawMessage
		if uErr := json.Unmarshal(raw, &docs); uErr != nil {
			t.Fatalf("unmarshal %s: %v", mode, uErr)
		}
		if len(docs) != 1 {
			t.Fatalf("mode %s: got %d docs, want 1", mode, len(docs))
		}
		_, ok := docs[0]["pointer_health"]
		return ok
	}

	if hasHealth(memory.PayloadModeKeys) {
		t.Error("keys mode carries pointer_health; keys is documented as identity-only")
	}
	if !hasHealth(memory.PayloadModeSummary) {
		t.Error("summary mode drops pointer_health — summary is the default projection, " +
			"and a staleness signal nobody sees by default is not discoverable")
	}
	if !hasHealth(memory.PayloadModeFull) {
		t.Error("full mode drops pointer_health")
	}
	if memory.DefaultPayloadMode != memory.PayloadModeSummary {
		t.Fatalf("DefaultPayloadMode is %q; this test's premise (that summary is the default) no longer holds",
			memory.DefaultPayloadMode)
	}
}
