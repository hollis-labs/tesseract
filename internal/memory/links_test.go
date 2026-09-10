package memory_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/hollis-labs/tesseract/domains"
	"github.com/hollis-labs/tesseract/internal/contextstore"
	"github.com/hollis-labs/tesseract/internal/memory"
)

// newLinkStore returns a memory store plus the raw handle, because the edge
// table is an index with no read API of its own yet — `related` is how callers
// reach it, and these tests need to see the rows that expansion walks.
func newLinkStore(t *testing.T) (*memory.Store, *sql.DB, func()) {
	t.Helper()
	dir := t.TempDir()
	cs, err := contextstore.Open(context.Background(), contextstore.Config{RootDir: dir})
	if err != nil {
		t.Fatalf("contextstore.Open: %v", err)
	}
	ms := memory.NewStore(cs.DB(), nil, "", 0, memory.NoopQueue{})
	return ms, cs.DB(), func() { _ = cs.Close() }
}

func linkInput(ns, key, body string) memory.WriteInput {
	return memory.WriteInput{
		Namespace:  ns,
		MemoryKey:  key,
		Author:     memory.Author{AgentID: "test-agent", AgentVersion: "1.0"},
		Trigger:    memory.TriggerExplicit,
		SessionID:  "manual:01HXXXXX",
		Origin:     memory.OriginUser,
		Confidence: 0.9,
		Status:     memory.StatusCanonical,
		Payload:    memory.Payload{Summary: "s", Body: body},
	}
}

const notesNS = "user/chrispian/memory/notes"

// edgeTarget reads the resolved end of a revision's single reference edge.
// A helper rather than an inline QueryRow at each site: the tests below
// interleave it with writes that carry their own err, and an inline
// `if err := ...` shadows the outer one.
func edgeTarget(t *testing.T, db *sql.DB, revisionID string) sql.NullString {
	t.Helper()
	var to sql.NullString
	err := db.QueryRow(
		`SELECT to_memory_id FROM memory_links WHERE from_revision_id = ?`,
		revisionID).Scan(&to)
	if err != nil {
		t.Fatalf("read edge for %s: %v", revisionID, err)
	}
	return to
}

func countRows(t *testing.T, db *sql.DB, query string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

func TestWriteRevision_IndexesLinks(t *testing.T) {
	ms, db, cleanup := newLinkStore(t)
	defer cleanup()
	ctx := context.Background()

	if _, err := ms.WriteRevision(ctx, linkInput(notesNS, "target_key", "no links here")); err != nil {
		t.Fatalf("write target: %v", err)
	}
	src, err := ms.WriteRevision(ctx, linkInput(notesNS, "source_key",
		"cites [[target_key]] and [[never_written]]"))
	if err != nil {
		t.Fatalf("write source: %v", err)
	}

	if got := countRows(t, db,
		`SELECT COUNT(*) FROM memory_links WHERE from_revision_id = ?`, src.RevisionID); got != 2 {
		t.Fatalf("expected 2 edges, got %d", got)
	}

	// The resolvable one carries a target; the other is retained unresolved
	// rather than dropped, which is the whole point of the nullable column.
	if got := countRows(t, db,
		`SELECT COUNT(*) FROM memory_links WHERE from_revision_id = ? AND to_memory_id IS NOT NULL`,
		src.RevisionID); got != 1 {
		t.Errorf("expected exactly 1 resolved edge, got %d", got)
	}
	var target string
	if err := db.QueryRow(
		`SELECT target FROM memory_links WHERE from_revision_id = ? AND to_memory_id IS NULL`,
		src.RevisionID).Scan(&target); err != nil {
		t.Fatalf("read unresolved edge: %v", err)
	}
	if target != "never_written" {
		t.Errorf("unresolved edge lost its target text: got %q", target)
	}
}

// A revision with no links must leave no rows — an empty body should not cost
// an edge, and a `[[` that never closes is not a link.
func TestWriteRevision_NoLinksNoRows(t *testing.T) {
	ms, db, cleanup := newLinkStore(t)
	defer cleanup()

	rev, err := ms.WriteRevision(context.Background(),
		linkInput(notesNS, "plain", "prose with [[ an unterminated opener"))
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := countRows(t, db,
		`SELECT COUNT(*) FROM memory_links WHERE from_revision_id = ?`, rev.RevisionID); got != 0 {
		t.Errorf("expected no edges, got %d", got)
	}
}

// Links in the summary count too: some entries put the citation there.
func TestWriteRevision_IndexesLinksFromSummary(t *testing.T) {
	ms, db, cleanup := newLinkStore(t)
	defer cleanup()
	ctx := context.Background()

	in := linkInput(notesNS, "summary_linker", "body has none")
	in.Payload.Summary = "supersedes the approach in [[other_key]]"
	rev, err := ms.WriteRevision(ctx, in)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := countRows(t, db,
		`SELECT COUNT(*) FROM memory_links WHERE from_revision_id = ?`, rev.RevisionID); got != 1 {
		t.Errorf("expected 1 edge from the summary, got %d", got)
	}
}

// A `[[` at the end of the summary must not pair with a `]]` at the start of
// the body: they are unrelated spans that only touch because of concatenation.
func TestWriteRevision_NoLinkAcrossTheSummaryBodySeam(t *testing.T) {
	ms, db, cleanup := newLinkStore(t)
	defer cleanup()

	in := linkInput(notesNS, "seam", "spliced]] rest of body")
	in.Payload.Summary = "trailing [["
	rev, err := ms.WriteRevision(context.Background(), in)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := countRows(t, db,
		`SELECT COUNT(*) FROM memory_links WHERE from_revision_id = ?`, rev.RevisionID); got != 0 {
		t.Errorf("a link formed across the summary/body seam: %d edges", got)
	}
}

// Resolution prefers the caller's own namespace, then falls back to a unique
// match anywhere, and declines to guess between two equal claimants.
func TestLinkResolution_NamespaceRule(t *testing.T) {
	const otherNS = "user/chrispian/memory/decisions"
	const thirdNS = "user/chrispian/memory/followups"

	t.Run("same namespace wins over a foreign namespace", func(t *testing.T) {
		ms, db, cleanup := newLinkStore(t)
		defer cleanup()
		ctx := context.Background()

		foreign, err := ms.WriteRevision(ctx, linkInput(otherNS, "shared_key", "x"))
		if err != nil {
			t.Fatalf("write foreign: %v", err)
		}
		local, err := ms.WriteRevision(ctx, linkInput(notesNS, "shared_key", "x"))
		if err != nil {
			t.Fatalf("write local: %v", err)
		}
		src, err := ms.WriteRevision(ctx, linkInput(notesNS, "src", "see [[shared_key]]"))
		if err != nil {
			t.Fatalf("write src: %v", err)
		}

		var to string
		if err := db.QueryRow(
			`SELECT to_memory_id FROM memory_links WHERE from_revision_id = ?`,
			src.RevisionID).Scan(&to); err != nil {
			t.Fatalf("read edge: %v", err)
		}
		if to != local.MemoryID {
			t.Errorf("resolved to %s, want the same-namespace memory %s (foreign was %s)",
				to, local.MemoryID, foreign.MemoryID)
		}
	})

	t.Run("unique match in another namespace resolves", func(t *testing.T) {
		ms, db, cleanup := newLinkStore(t)
		defer cleanup()
		ctx := context.Background()

		target, err := ms.WriteRevision(ctx, linkInput(otherNS, "only_over_there", "x"))
		if err != nil {
			t.Fatalf("write target: %v", err)
		}
		src, err := ms.WriteRevision(ctx, linkInput(notesNS, "src", "see [[only_over_there]]"))
		if err != nil {
			t.Fatalf("write src: %v", err)
		}

		var to string
		if err := db.QueryRow(
			`SELECT to_memory_id FROM memory_links WHERE from_revision_id = ?`,
			src.RevisionID).Scan(&to); err != nil {
			t.Fatalf("read edge: %v", err)
		}
		if to != target.MemoryID {
			t.Errorf("cross-namespace link did not resolve: got %q want %q", to, target.MemoryID)
		}
	})

	// The resolution query returns only the two rows that decide the answer.
	// That is safe only because the ordering surfaces a same-namespace claimant
	// first — if it did not, a local match sitting behind a crowd of foreign
	// ones would be truncated away and the edge would resolve to nothing, or
	// worse, to a foreign entry.
	t.Run("same namespace wins from behind many foreign claimants", func(t *testing.T) {
		ms, db, cleanup := newLinkStore(t)
		defer cleanup()
		ctx := context.Background()

		crowd := []string{
			"user/chrispian/memory/decisions",
			"user/chrispian/memory/feedback",
			"user/chrispian/memory/followups",
			"user/chrispian/memory/learnings",
			"user/chrispian/memory/limitations",
			"user/chrispian/memory/outcomes",
			"user/chrispian/memory/references",
		}
		for _, ns := range crowd {
			if _, err := ms.WriteRevision(ctx, linkInput(ns, "crowded", "claimant")); err != nil {
				t.Fatalf("write %s: %v", ns, err)
			}
		}
		local, err := ms.WriteRevision(ctx, linkInput(notesNS, "crowded", "the local one"))
		if err != nil {
			t.Fatalf("write local: %v", err)
		}
		src, err := ms.WriteRevision(ctx, linkInput(notesNS, "src", "see [[crowded]]"))
		if err != nil {
			t.Fatalf("write src: %v", err)
		}

		if to := edgeTarget(t, db, src.RevisionID); !to.Valid || to.String != local.MemoryID {
			t.Errorf("resolved to %v, want the same-namespace memory %s among %d claimants",
				to, local.MemoryID, len(crowd)+1)
		}
	})

	// The mirror case: many claimants, none local, stays unresolved rather
	// than binding to whichever two rows the bounded query happened to see.
	t.Run("many foreign claimants and no local match stays unresolved", func(t *testing.T) {
		ms, db, cleanup := newLinkStore(t)
		defer cleanup()
		ctx := context.Background()

		for _, ns := range []string{
			"user/chrispian/memory/decisions",
			"user/chrispian/memory/feedback",
			"user/chrispian/memory/followups",
			"user/chrispian/memory/learnings",
			"user/chrispian/memory/limitations",
		} {
			if _, err := ms.WriteRevision(ctx, linkInput(ns, "crowded", "claimant")); err != nil {
				t.Fatalf("write %s: %v", ns, err)
			}
		}
		src, err := ms.WriteRevision(ctx, linkInput(notesNS, "src", "see [[crowded]]"))
		if err != nil {
			t.Fatalf("write src: %v", err)
		}

		if to := edgeTarget(t, db, src.RevisionID); to.Valid {
			t.Errorf("bound to %q despite five equal foreign claimants", to.String)
		}
	})

	t.Run("ambiguous across two foreign namespaces stays unresolved", func(t *testing.T) {
		ms, db, cleanup := newLinkStore(t)
		defer cleanup()
		ctx := context.Background()

		if _, err := ms.WriteRevision(ctx, linkInput(otherNS, "contested", "x")); err != nil {
			t.Fatalf("write a: %v", err)
		}
		if _, err := ms.WriteRevision(ctx, linkInput(thirdNS, "contested", "x")); err != nil {
			t.Fatalf("write b: %v", err)
		}
		src, err := ms.WriteRevision(ctx, linkInput(notesNS, "src", "see [[contested]]"))
		if err != nil {
			t.Fatalf("write src: %v", err)
		}

		var to sql.NullString
		if err := db.QueryRow(
			`SELECT to_memory_id FROM memory_links WHERE from_revision_id = ?`,
			src.RevisionID).Scan(&to); err != nil {
			t.Fatalf("read edge: %v", err)
		}
		if to.Valid {
			t.Errorf("ambiguous target resolved to %q; it should decline to guess", to.String)
		}
	})
}

// The lineage projection is trigger-maintained, so it must appear without any
// Go code asking for it — and it must agree with the column exactly.
func TestSupersedesProjection_MatchesTheColumn(t *testing.T) {
	ms, db, cleanup := newLinkStore(t)
	defer cleanup()
	ctx := context.Background()

	first, err := ms.WriteRevision(ctx, linkInput(notesNS, "evolving", "v1"))
	if err != nil {
		t.Fatalf("write v1: %v", err)
	}
	in := linkInput(notesNS, "evolving", "v2")
	in.Supersedes = first.RevisionID
	second, err := ms.WriteRevision(ctx, in)
	if err != nil {
		t.Fatalf("write v2: %v", err)
	}

	var toRev, toMem, target string
	var kind, label sql.NullString
	if err := db.QueryRow(`
SELECT to_revision_id, to_memory_id, target, kind, label
FROM memory_links WHERE relation = 'supersedes' AND from_revision_id = ?`,
		second.RevisionID).Scan(&toRev, &toMem, &target, &kind, &label); err != nil {
		t.Fatalf("read lineage edge: %v", err)
	}
	if toRev != first.RevisionID {
		t.Errorf("to_revision_id = %q, want %q", toRev, first.RevisionID)
	}
	if target != first.RevisionID {
		t.Errorf("target = %q, want the superseded revision id %q", target, first.RevisionID)
	}
	if toMem != second.MemoryID {
		t.Errorf("to_memory_id = %q, want %q", toMem, second.MemoryID)
	}
	// A lineage edge was written in no syntax, so it claims none.
	if kind.Valid {
		t.Errorf("kind = %q, want NULL for a structural edge", kind.String)
	}
	if label.Valid {
		t.Errorf("label = %q, want NULL", label.String)
	}

	// The invariant the whole two-grain model rests on.
	if got := countRows(t, db,
		`SELECT COUNT(*) FROM memory_links WHERE relation='supersedes' AND to_memory_id <> from_memory_id`); got != 0 {
		t.Errorf("%d supersedes edges cross memories; WriteRevision is supposed to reject those", got)
	}

	// Projection and column must agree row for row.
	col := countRows(t, db, `SELECT COUNT(*) FROM memory_revisions WHERE supersedes IS NOT NULL`)
	proj := countRows(t, db, `SELECT COUNT(*) FROM memory_links WHERE relation = 'supersedes'`)
	if col != proj {
		t.Errorf("lineage drift: %d rows carry supersedes, %d edges project it", col, proj)
	}
}

// The CHECK constraint is what makes the vocabulary closed in storage rather
// than only in Go. Without it an unknown relation is silently traversed or
// silently skipped depending on the query.
func TestRelationVocabulary_IsClosedInStorage(t *testing.T) {
	_, db, cleanup := newLinkStore(t)
	defer cleanup()

	_, err := db.Exec(`
INSERT INTO memory_links (from_revision_id, from_memory_id, relation, target, position, created_at)
VALUES ('r', 'm', 'mentions', 't', 0, '2026-09-09T00:00:00.000000000Z')`)
	if err == nil {
		t.Fatal("expected the CHECK constraint to reject an unknown relation")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "constraint") {
		t.Errorf("rejected for the wrong reason: %v", err)
	}
}

func TestLinkRelationVocabulary(t *testing.T) {
	got := memory.LinkRelationVocabulary()
	want := []string{"references", "supersedes"}
	if len(got) != len(want) {
		t.Fatalf("vocabulary = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("vocabulary = %v, want %v", got, want)
		}
	}
	if memory.LinkRelation("mentions").Valid() {
		t.Error("'mentions' should not be a valid relation")
	}
	if !memory.LinkRelationReferences.Valid() || !memory.LinkRelationSupersedes.Valid() {
		t.Error("both vocabulary members must validate")
	}
}

// ── The `related` expansion ──────────────────────────────────────────────────

func recallRelated(t *testing.T, ms *memory.Store, anchors, relations []string) []string {
	t.Helper()
	page, err := ms.RecallPaged(context.Background(), memory.RecallInput{
		Namespaces: []string{notesNS, "user/chrispian/memory/decisions"},
		Ranking:    memory.RankingChronological,
		Filters: memory.RecallFilters{
			RelatedTo:        anchors,
			RelatedRelations: relations,
		},
	}, memory.PageRequest{Limit: 50})
	if err != nil {
		t.Fatalf("RecallPaged: %v", err)
	}
	keys := make([]string, 0, len(page.Kept))
	for _, r := range page.Kept {
		keys = append(keys, r.Revision.MemoryKey)
	}
	return keys
}

func hasKey(keys []string, want string) bool {
	for _, k := range keys {
		if k == want {
			return true
		}
	}
	return false
}

// Adjacency is undirected: the anchor's neighbors include what it cites AND
// what cites it. The second half is the one a forward-only walk would lose,
// and in this corpus shape it is the larger half.
func TestRecall_RelatedIsUndirected(t *testing.T) {
	ms, _, cleanup := newLinkStore(t)
	defer cleanup()
	ctx := context.Background()

	for _, w := range []memory.WriteInput{
		linkInput(notesNS, "anchor", "cites [[cited_by_anchor]]"),
		linkInput(notesNS, "cited_by_anchor", "a leaf"),
		linkInput(notesNS, "cites_the_anchor", "refers to [[anchor]]"),
		linkInput(notesNS, "unrelated", "mentions nobody"),
	} {
		if _, err := ms.WriteRevision(ctx, w); err != nil {
			t.Fatalf("write %s: %v", w.MemoryKey, err)
		}
	}

	keys := recallRelated(t, ms, []string{"anchor"}, nil)

	if !hasKey(keys, "cited_by_anchor") {
		t.Errorf("outbound neighbor missing from %v", keys)
	}
	if !hasKey(keys, "cites_the_anchor") {
		t.Errorf("inbound neighbor missing from %v — the expansion is directional", keys)
	}
	if hasKey(keys, "unrelated") {
		t.Errorf("unrelated entry leaked into %v", keys)
	}
}

// An anchor with no edges is a normal state, not an error.
func TestRecall_RelatedToUnlinkedAnchorIsEmpty(t *testing.T) {
	ms, _, cleanup := newLinkStore(t)
	defer cleanup()

	if _, err := ms.WriteRevision(context.Background(),
		linkInput(notesNS, "lonely", "no links")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if keys := recallRelated(t, ms, []string{"lonely"}, nil); len(keys) != 0 {
		t.Errorf("expected no neighbors, got %v", keys)
	}
}

// An anchor naming nothing at all is likewise empty rather than an error.
func TestRecall_RelatedToUnknownAnchorIsEmpty(t *testing.T) {
	ms, _, cleanup := newLinkStore(t)
	defer cleanup()

	if _, err := ms.WriteRevision(context.Background(),
		linkInput(notesNS, "something", "no links")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if keys := recallRelated(t, ms, []string{"no_such_key_anywhere"}, nil); len(keys) != 0 {
		t.Errorf("expected no neighbors, got %v", keys)
	}
}

// An edge that never resolved has no far end, so it cannot be walked — but the
// row still exists, which is what makes a later re-resolution possible.
func TestRecall_UnresolvedEdgeIsNotTraversable(t *testing.T) {
	ms, db, cleanup := newLinkStore(t)
	defer cleanup()

	if _, err := ms.WriteRevision(context.Background(),
		linkInput(notesNS, "dangler", "cites [[a_key_that_does_not_exist]]")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if keys := recallRelated(t, ms, []string{"dangler"}, nil); len(keys) != 0 {
		t.Errorf("an unresolved edge was traversed: %v", keys)
	}
	if got := countRows(t, db,
		`SELECT COUNT(*) FROM memory_links WHERE target = 'a_key_that_does_not_exist'`); got != 1 {
		t.Errorf("the unresolved edge was dropped instead of retained: %d rows", got)
	}
}

// The relation filter narrows adjacency. Under `references` the citation
// neighbor appears; under `supersedes` the anchor's own entry does, because
// lineage is intra-entry by construction.
func TestRecall_RelatedRelationFilter(t *testing.T) {
	ms, _, cleanup := newLinkStore(t)
	defer cleanup()
	ctx := context.Background()

	if _, err := ms.WriteRevision(ctx, linkInput(notesNS, "leaf", "nothing")); err != nil {
		t.Fatalf("write leaf: %v", err)
	}
	first, err := ms.WriteRevision(ctx, linkInput(notesNS, "trunk", "v1 cites [[leaf]]"))
	if err != nil {
		t.Fatalf("write trunk v1: %v", err)
	}
	in := linkInput(notesNS, "trunk", "v2 cites [[leaf]]")
	in.Supersedes = first.RevisionID
	if _, err := ms.WriteRevision(ctx, in); err != nil {
		t.Fatalf("write trunk v2: %v", err)
	}

	refs := recallRelated(t, ms, []string{"trunk"}, []string{"references"})
	if !hasKey(refs, "leaf") {
		t.Errorf("references filter lost the citation: %v", refs)
	}

	lineage := recallRelated(t, ms, []string{"trunk"}, []string{"supersedes"})
	if hasKey(lineage, "leaf") {
		t.Errorf("supersedes filter admitted a citation edge: %v", lineage)
	}
	if !hasKey(lineage, "trunk") {
		t.Errorf("supersedes adjacency should be the anchor's own entry, got %v", lineage)
	}
}

// Both retrieval arms go through buildRecallFilters, so `related` must narrow
// a lexical query the same way it narrows a metadata one. This is the property
// that made buildRecallFilters the right seam.
func TestRecall_RelatedAppliesToTheLexicalArm(t *testing.T) {
	ms, _, cleanup := newLinkStore(t)
	defer cleanup()
	ctx := context.Background()

	for _, w := range []memory.WriteInput{
		linkInput(notesNS, "hub", "cites [[spoke]]"),
		linkInput(notesNS, "spoke", "distinctiveword lives here"),
		linkInput(notesNS, "decoy", "distinctiveword lives here too"),
	} {
		if _, err := ms.WriteRevision(ctx, w); err != nil {
			t.Fatalf("write %s: %v", w.MemoryKey, err)
		}
	}

	page, err := ms.RecallPaged(ctx, memory.RecallInput{
		Namespaces: []string{notesNS},
		Ranking:    memory.RankingRelevance,
		SearchMode: memory.SearchModeLexical,
		Query:      "distinctiveword",
		Filters:    memory.RecallFilters{RelatedTo: []string{"hub"}},
	}, memory.PageRequest{Limit: 50})
	if err != nil {
		t.Fatalf("RecallPaged: %v", err)
	}

	var keys []string
	for _, r := range page.Kept {
		keys = append(keys, r.Revision.MemoryKey)
	}
	if !hasKey(keys, "spoke") {
		t.Errorf("lexical hit inside the neighborhood was dropped: %v", keys)
	}
	if hasKey(keys, "decoy") {
		t.Errorf("lexical hit outside the neighborhood survived the expansion: %v", keys)
	}
}

// Cross-namespace adjacency is the interesting third of the graph; the
// expansion must not quietly stay inside one namespace.
func TestRecall_RelatedCrossesNamespaces(t *testing.T) {
	ms, _, cleanup := newLinkStore(t)
	defer cleanup()
	ctx := context.Background()

	if _, err := ms.WriteRevision(ctx,
		linkInput("user/chrispian/memory/decisions", "the_decision", "a ruling")); err != nil {
		t.Fatalf("write decision: %v", err)
	}
	if _, err := ms.WriteRevision(ctx,
		linkInput(notesNS, "the_followup", "deferred from [[the_decision]]")); err != nil {
		t.Fatalf("write followup: %v", err)
	}

	if keys := recallRelated(t, ms, []string{"the_decision"}, nil); !hasKey(keys, "the_followup") {
		t.Errorf("cross-namespace neighbor missing: %v", keys)
	}
}

func TestRecall_RelatedRelationsValidation(t *testing.T) {
	ms, _, cleanup := newLinkStore(t)
	defer cleanup()

	t.Run("unknown relation is rejected", func(t *testing.T) {
		_, err := ms.RecallPaged(context.Background(), memory.RecallInput{
			Namespaces: []string{notesNS},
			Ranking:    memory.RankingChronological,
			Filters: memory.RecallFilters{
				RelatedTo:        []string{"anchor"},
				RelatedRelations: []string{"mentions"},
			},
		}, memory.PageRequest{Limit: 10})
		if !errors.Is(err, memory.ErrInvalidInput) {
			t.Fatalf("expected ErrInvalidInput, got %v", err)
		}
		if !strings.Contains(err.Error(), "references|supersedes") {
			t.Errorf("error should render the vocabulary, got %q", err)
		}
	})

	t.Run("relations without an anchor is rejected", func(t *testing.T) {
		_, err := ms.RecallPaged(context.Background(), memory.RecallInput{
			Namespaces: []string{notesNS},
			Ranking:    memory.RankingChronological,
			Filters:    memory.RecallFilters{RelatedRelations: []string{"references"}},
		}, memory.PageRequest{Limit: 10})
		if !errors.Is(err, memory.ErrInvalidInput) {
			t.Fatalf("expected ErrInvalidInput, got %v", err)
		}
	})
}

// Knowledge and memory share one table and one graph; a knowledge entry citing
// a memory decision is a real and common shape in the corpus.
func TestRecall_RelatedSpansDomains(t *testing.T) {
	ms, _, cleanup := newLinkStore(t)
	defer cleanup()
	ctx := context.Background()

	if _, err := ms.WriteRevision(ctx,
		linkInput("user/chrispian/memory/decisions", "a_decision", "a ruling")); err != nil {
		t.Fatalf("write decision: %v", err)
	}
	kn := linkInput("user/chrispian/knowledge/portfolio", "a_dossier", "grounded in [[a_decision]]")
	kn.Domain = domains.Knowledge
	kn.Facets = memory.Facets{
		Kind:    "doc",
		Source:  "filesystem",
		Pointer: &memory.Pointer{Scheme: "file", Locator: "/tmp/dossier.md"},
	}
	if _, err := ms.WriteRevision(ctx, kn); err != nil {
		t.Fatalf("write knowledge: %v", err)
	}

	page, err := ms.RecallPaged(ctx, memory.RecallInput{
		Namespaces: []string{"user/chrispian/knowledge/portfolio"},
		Ranking:    memory.RankingChronological,
		Filters:    memory.RecallFilters{RelatedTo: []string{"a_decision"}},
	}, memory.PageRequest{Limit: 10})
	if err != nil {
		t.Fatalf("RecallPaged: %v", err)
	}
	if len(page.Kept) != 1 || page.Kept[0].Revision.MemoryKey != "a_dossier" {
		t.Errorf("knowledge→memory adjacency not found; got %d results", len(page.Kept))
	}
}

// ── Late resolution ──────────────────────────────────────────────────────────

// The ordinary authoring order: the citing record is written first. The edge
// must become traversable when its target finally arrives, or the graph
// silently under-reports forever.
func TestLinkResolution_BindsWhenTheTargetArrivesLater(t *testing.T) {
	ms, db, cleanup := newLinkStore(t)
	defer cleanup()
	ctx := context.Background()

	src, err := ms.WriteRevision(ctx, linkInput(notesNS, "cites_early", "spawns [[written_later]]"))
	if err != nil {
		t.Fatalf("write source: %v", err)
	}

	if to := edgeTarget(t, db, src.RevisionID); to.Valid {
		t.Fatalf("edge resolved before its target existed: %q", to.String)
	}

	target, err := ms.WriteRevision(ctx, linkInput(notesNS, "written_later", "here at last"))
	if err != nil {
		t.Fatalf("write target: %v", err)
	}

	if to := edgeTarget(t, db, src.RevisionID); !to.Valid || to.String != target.MemoryID {
		t.Fatalf("edge did not bind when its target arrived: got %v want %s", to, target.MemoryID)
	}

	// And it is now walkable in both directions.
	if keys := recallRelated(t, ms, []string{"cites_early"}, nil); !hasKey(keys, "written_later") {
		t.Errorf("late-bound edge not traversable outbound: %v", keys)
	}
	if keys := recallRelated(t, ms, []string{"written_later"}, nil); !hasKey(keys, "cites_early") {
		t.Errorf("late-bound edge not traversable inbound: %v", keys)
	}
}

// Late resolution must apply the same rule as first-pass resolution, not a
// blanket bind. An edge that declined to guess between two claimants must not
// silently attach itself to a third.
func TestLinkResolution_LateBindingRespectsAmbiguity(t *testing.T) {
	ms, db, cleanup := newLinkStore(t)
	defer cleanup()
	ctx := context.Background()

	const nsA = "user/chrispian/memory/decisions"
	const nsB = "user/chrispian/memory/followups"
	const nsC = "user/chrispian/memory/learnings"

	if _, err := ms.WriteRevision(ctx, linkInput(nsA, "contested", "claimant a")); err != nil {
		t.Fatalf("write a: %v", err)
	}
	if _, err := ms.WriteRevision(ctx, linkInput(nsB, "contested", "claimant b")); err != nil {
		t.Fatalf("write b: %v", err)
	}
	src, err := ms.WriteRevision(ctx, linkInput(notesNS, "src", "see [[contested]]"))
	if err != nil {
		t.Fatalf("write src: %v", err)
	}

	// A third claimant appears. The edge is still ambiguous from notesNS.
	if _, wErr := ms.WriteRevision(ctx, linkInput(nsC, "contested", "claimant c")); wErr != nil {
		t.Fatalf("write c: %v", wErr)
	}

	if to := edgeTarget(t, db, src.RevisionID); to.Valid {
		t.Errorf("late binding guessed between ambiguous claimants: bound to %q", to.String)
	}

	// But a same-namespace claimant DOES settle it — rule 1 outranks ambiguity.
	local, err := ms.WriteRevision(ctx, linkInput(notesNS, "contested", "the local one"))
	if err != nil {
		t.Fatalf("write local: %v", err)
	}
	if to := edgeTarget(t, db, src.RevisionID); !to.Valid || to.String != local.MemoryID {
		t.Errorf("same-namespace arrival did not settle the edge: got %v want %s", to, local.MemoryID)
	}
}

// Re-parsing a revision replaces its reference edges rather than appending, so
// an idempotent rebuild does not double the graph.
func TestLinkIndexing_IsIdempotentPerRevision(t *testing.T) {
	ms, db, cleanup := newLinkStore(t)
	defer cleanup()
	ctx := context.Background()

	// Two revisions of one entry, each citing the same target: the edges belong
	// to the revisions independently, so both survive.
	if _, err := ms.WriteRevision(ctx, linkInput(notesNS, "leaf", "x")); err != nil {
		t.Fatalf("write leaf: %v", err)
	}
	first, err := ms.WriteRevision(ctx, linkInput(notesNS, "trunk", "v1 cites [[leaf]]"))
	if err != nil {
		t.Fatalf("write v1: %v", err)
	}
	in := linkInput(notesNS, "trunk", "v2 cites [[leaf]]")
	in.Supersedes = first.RevisionID
	second, err := ms.WriteRevision(ctx, in)
	if err != nil {
		t.Fatalf("write v2: %v", err)
	}

	for _, rev := range []string{first.RevisionID, second.RevisionID} {
		if got := countRows(t, db,
			`SELECT COUNT(*) FROM memory_links WHERE from_revision_id = ? AND relation='references'`,
			rev); got != 1 {
			t.Errorf("revision %s has %d reference edges, want 1", rev, got)
		}
	}
}
