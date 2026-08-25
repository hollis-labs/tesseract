package memory_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/tesseract/internal/memory"
)

// TestPointerVerificationOutcomeCheckMatchesVocabulary binds the SQL CHECK on
// pointer_verifications.outcome to memory.PointerOutcomeVocabulary().
//
// The two are separate renderings of one rule: the DDL lives in
// internal/contextstore, which cannot import this package without inverting
// the dependency. So this drives real INSERTs through a real store and
// asserts acceptance matches PointerOutcome.Valid() in BOTH directions — a
// member that the database rejects, or a non-member it accepts, fails here.
//
// The non-members are not arbitrary. `not_applicable` is a real status this
// package defines but derives from the pointer scheme and never stores;
// `none` is the SQL-side sentinel for "no pointer". Both would render verbatim
// on every read surface while being rejected as filter arguments — visible in
// results, unreachable by query. They are the exact values that made the
// constraint necessary.
func TestPointerVerificationOutcomeCheckMatchesVocabulary(t *testing.T) {
	ms, ks := newHealthFixture(t)
	db := ms.DB()
	ctx := context.Background()

	rev := seedKnowledge(t, ks, "chk.entry", "file", "/tmp/chk")

	insert := func(outcome string) error {
		_, err := db.ExecContext(ctx, `
INSERT INTO pointer_verifications (revision_id, scheme, locator, outcome, checked_at, detail)
VALUES (?, 'file', '/tmp/chk', ?, ?, 'probe')`,
			rev.RevisionID, outcome, time.Now().UTC().Format(time.RFC3339Nano))
		return err
	}

	// Every member of the Go vocabulary must be storable.
	for _, outcome := range memory.PointerOutcomeVocabulary() {
		if !memory.PointerOutcome(outcome).Valid() {
			t.Fatalf("vocabulary member %q fails its own Valid()", outcome)
		}
		if err := insert(outcome); err != nil {
			t.Errorf("the database rejected %q, which PointerOutcomeVocabulary() lists as storable: %v.\n"+
				"    The CHECK in contextstore migration 13 and the Go vocabulary have drifted.", outcome, err)
		}
	}

	// And nothing else may be.
	for _, bad := range []string{
		string(memory.PointerHealthNotApplicable), // derived at read time, never observed
		"none",          // the SQL sentinel for "revision has no pointer"
		"unchecked",     // a surface state the table structurally cannot hold
		"probably_fine", // a plausible-looking invention
		"RESOLVED",      // case matters; the read path compares exact strings
		"",
	} {
		if memory.PointerOutcome(bad).Valid() {
			t.Fatalf("test premise is wrong: %q is a valid outcome", bad)
		}
		err := insert(bad)
		if err == nil {
			t.Errorf("the database accepted outcome %q, which is outside the vocabulary.\n"+
				"    It would surface verbatim on every read surface while being rejected as a\n"+
				"    filter argument — visible in results, unreachable by query.", bad)
			continue
		}
		if !strings.Contains(strings.ToLower(err.Error()), "constraint") {
			t.Errorf("outcome %q was rejected, but not by a constraint: %v", bad, err)
		}
	}
}

// TestApplyVerificationPlanRefusalIsBackedByTheDatabase checks the two layers
// agree. ApplyVerificationPlan validates outcomes in Go before opening a
// transaction; the CHECK is the backstop for every other writer. Neither is
// redundant: the Go check gives a good error message, the constraint holds
// when someone bypasses it.
func TestApplyVerificationPlanRefusalIsBackedByTheDatabase(t *testing.T) {
	ms, ks := newHealthFixture(t)
	db := ms.DB()
	ctx := context.Background()
	rev := seedKnowledge(t, ks, "chk.layers", "file", "/tmp/chk2")

	bad := memory.VerificationPlan{Rows: []memory.PointerObservation{{
		RevisionID: rev.RevisionID, Scheme: "file", Locator: "/tmp/chk2",
		Outcome: memory.PointerOutcome("not_applicable"), CheckedAt: time.Now().UTC(),
	}}}
	if _, err := memory.ApplyVerificationPlan(ctx, db, bad); err == nil {
		t.Fatal("ApplyVerificationPlan accepted an out-of-vocabulary outcome")
	}

	// The same value must also be refused when the Go guard is bypassed.
	_, err := db.ExecContext(ctx, `
INSERT INTO pointer_verifications (revision_id, scheme, locator, outcome, checked_at, detail)
VALUES (?, 'file', '/tmp/chk2', 'not_applicable', ?, '')`,
		rev.RevisionID, time.Now().UTC().Format(time.RFC3339Nano))
	if err == nil {
		t.Error("a direct INSERT bypassed the vocabulary; the Go guard is the only enforcement")
	}

	var n int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pointer_verifications`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Errorf("log holds %d row(s) after two refused writes, want 0", n)
	}
}
