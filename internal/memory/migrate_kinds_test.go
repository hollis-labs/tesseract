package memory_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"sort"
	"strings"
	"testing"

	"github.com/hollis-labs/tesseract/domains"
	"github.com/hollis-labs/tesseract/internal/contextstore"
	"github.com/hollis-labs/tesseract/internal/memory"
)

// newKindFixture returns a memory store plus the raw *sql.DB behind it, both
// backed by an isolated temp-dir database. The kind migration operates on the
// DB directly, so tests need both handles.
func newKindFixture(t *testing.T) (*memory.Store, *sql.DB) {
	t.Helper()
	dir := t.TempDir()
	cs, err := contextstore.Open(context.Background(), contextstore.Config{RootDir: dir})
	if err != nil {
		t.Fatalf("contextstore.Open: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return memory.NewStore(cs.DB(), nil, "", 0, memory.NoopQueue{}), cs.DB()
}

// writeKnowledge writes one knowledge fixture and returns its revision ID.
// Off-vocabulary kinds are intentionally legacy/corrupt migration inputs: the
// production persistence boundary must reject them, so this test-only helper
// first writes a conformant row and then uses explicit raw SQL to recreate the
// pre-enforcement database state the migration is responsible for repairing.
func writeKnowledge(t *testing.T, ms *memory.Store, key, kind string) string {
	t.Helper()
	in := sampleInput(key)
	in.Domain = domains.Knowledge
	in.Namespace = "user/chrispian/knowledge/tools/mcp"
	writeKind := kind
	if !memory.IsCanonicalKnowledgeKind(writeKind) {
		writeKind = "note"
	}
	in.Facets = memory.Facets{
		Kind:    writeKind,
		Source:  "agent",
		Pointer: &memory.Pointer{Scheme: "nil", Locator: "inline"},
	}
	rev, err := ms.WriteRevision(context.Background(), in)
	if err != nil {
		t.Fatalf("WriteRevision(%s, kind=%s): %v", key, kind, err)
	}
	if writeKind != kind {
		if _, err := ms.DB().Exec(
			`UPDATE memory_revisions SET facet_kind = ? WHERE revision_id = ?`,
			kind, rev.RevisionID,
		); err != nil {
			t.Fatalf("raw legacy facet fixture (%s, kind=%s): %v", key, kind, err)
		}
	}
	return rev.RevisionID
}

// kindOf reads facet_kind straight from the row, bypassing the read path.
func kindOf(t *testing.T, db *sql.DB, revisionID string) string {
	t.Helper()
	var kind sql.NullString
	err := db.QueryRow(`SELECT facet_kind FROM memory_revisions WHERE revision_id = ?`, revisionID).Scan(&kind)
	if err != nil {
		t.Fatalf("read facet_kind for %s: %v", revisionID, err)
	}
	return kind.String
}

func TestKnowledgeKindVocabulary_ContainsPromotedKinds(t *testing.T) {
	vocab := memory.KnowledgeKindVocabulary()
	if len(vocab) != 11 {
		t.Fatalf("vocabulary size = %d, want 11; got %v", len(vocab), vocab)
	}
	want := []string{
		"doc", "handoff", "investigation", "learning", "mcp_server", "note",
		"package", "playbook", "pointer", "project_canonical", "session_close",
	}
	if !sort.SliceIsSorted(vocab, func(i, j int) bool { return vocab[i] < vocab[j] }) {
		t.Errorf("vocabulary is not sorted: %v", vocab)
	}
	for i, w := range want {
		if vocab[i] != w {
			t.Errorf("vocabulary[%d] = %q, want %q (full: %v)", i, vocab[i], w, vocab)
		}
	}
}

func TestBuildKindMigrationPlan_MapsOffVocabularyKinds(t *testing.T) {
	ms, db := newKindFixture(t)
	ctx := context.Background()

	mcpRev := writeKnowledge(t, ms, "mcp.server.cerberus", "mcp-server")
	issueRev := writeKnowledge(t, ms, "issues.manual-field-dropped", "issue/bug")
	// Conformant rows must not appear in the plan — including the two kinds
	// promoted to canonical by this migration.
	docRev := writeKnowledge(t, ms, "docs.some-doc", "doc")
	invRev := writeKnowledge(t, ms, "investigations.some-dossier", "investigation")
	playRev := writeKnowledge(t, ms, "playbooks.some-runbook", "playbook")

	plan, err := memory.BuildKindMigrationPlan(ctx, db)
	if err != nil {
		t.Fatalf("BuildKindMigrationPlan: %v", err)
	}
	if len(plan.Unmapped) != 0 {
		t.Fatalf("Unmapped = %v, want none", plan.Unmapped)
	}
	if len(plan.Rows) != 2 {
		t.Fatalf("plan rows = %d, want 2; got %+v", len(plan.Rows), plan.Rows)
	}
	if plan.HeadRows != 2 {
		t.Errorf("HeadRows = %d, want 2", plan.HeadRows)
	}

	byRev := map[string]memory.KindMigrationRow{}
	for _, r := range plan.Rows {
		byRev[r.RevisionID] = r
	}
	if got := byRev[mcpRev]; got.OldKind != "mcp-server" || got.NewKind != "mcp_server" {
		t.Errorf("mcp row = %q -> %q, want mcp-server -> mcp_server", got.OldKind, got.NewKind)
	}
	if got := byRev[issueRev]; got.OldKind != "issue/bug" || got.NewKind != "note" {
		t.Errorf("issue row = %q -> %q, want issue/bug -> note", got.OldKind, got.NewKind)
	}
	for _, conformant := range []string{docRev, invRev, playRev} {
		if _, planned := byRev[conformant]; planned {
			t.Errorf("conformant revision %s (kind=%s) must not be planned", conformant, kindOf(t, db, conformant))
		}
	}

	// Planning must not mutate anything.
	if got := kindOf(t, db, mcpRev); got != "mcp-server" {
		t.Errorf("planning mutated the row: facet_kind = %q, want mcp-server", got)
	}
}

func TestApplyKindMigration_RewritesInPlacePreservingIdentity(t *testing.T) {
	ms, db := newKindFixture(t)
	ctx := context.Background()

	mcpRev := writeKnowledge(t, ms, "mcp.server.cerberus", "mcp-server")
	issueRev := writeKnowledge(t, ms, "issues.manual-field-dropped", "issue/bug")

	var memIDBefore, nsBefore, keyBefore string
	if err := db.QueryRow(
		`SELECT memory_id, namespace, memory_key FROM memory_revisions WHERE revision_id = ?`, mcpRev,
	).Scan(&memIDBefore, &nsBefore, &keyBefore); err != nil {
		t.Fatalf("read identity: %v", err)
	}

	plan, err := memory.BuildKindMigrationPlan(ctx, db)
	if err != nil {
		t.Fatalf("BuildKindMigrationPlan: %v", err)
	}
	n, err := memory.ApplyKindMigration(ctx, db, plan)
	if err != nil {
		t.Fatalf("ApplyKindMigration: %v", err)
	}
	if n != 2 {
		t.Errorf("updated rows = %d, want 2", n)
	}

	if got := kindOf(t, db, mcpRev); got != "mcp_server" {
		t.Errorf("mcp facet_kind = %q, want mcp_server", got)
	}
	if got := kindOf(t, db, issueRev); got != "note" {
		t.Errorf("issue facet_kind = %q, want note", got)
	}

	// Identity columns must be untouched — this is a metadata conformance
	// change, not a superseding write.
	var memIDAfter, nsAfter, keyAfter string
	if readErr := db.QueryRow(
		`SELECT memory_id, namespace, memory_key FROM memory_revisions WHERE revision_id = ?`, mcpRev,
	).Scan(&memIDAfter, &nsAfter, &keyAfter); readErr != nil {
		t.Fatalf("read identity after: %v", readErr)
	}
	if memIDAfter != memIDBefore || nsAfter != nsBefore || keyAfter != keyBefore {
		t.Errorf("identity changed: (%s,%s,%s) -> (%s,%s,%s)",
			memIDBefore, nsBefore, keyBefore, memIDAfter, nsAfter, keyAfter)
	}

	// No new revisions were minted.
	var revCount int
	if countErr := db.QueryRow(`SELECT COUNT(*) FROM memory_revisions WHERE domain = 'knowledge'`).Scan(&revCount); countErr != nil {
		t.Fatalf("count revisions: %v", countErr)
	}
	if revCount != 2 {
		t.Errorf("knowledge revision count = %d, want 2 (no new revisions minted)", revCount)
	}

	// Re-planning after a successful apply finds nothing.
	plan2, err := memory.BuildKindMigrationPlan(ctx, db)
	if err != nil {
		t.Fatalf("BuildKindMigrationPlan (second): %v", err)
	}
	if len(plan2.Rows) != 0 || len(plan2.Unmapped) != 0 {
		t.Errorf("second plan not empty: %d rows, %d unmapped", len(plan2.Rows), len(plan2.Unmapped))
	}
}

func TestBuildKindMigrationPlan_UnmappedKindBlocksApply(t *testing.T) {
	ms, db := newKindFixture(t)
	ctx := context.Background()

	mcpRev := writeKnowledge(t, ms, "mcp.server.cerberus", "mcp-server")
	bogusRev := writeKnowledge(t, ms, "weird.thing", "totally_unknown_kind")

	plan, err := memory.BuildKindMigrationPlan(ctx, db)
	if err != nil {
		t.Fatalf("BuildKindMigrationPlan: %v", err)
	}
	if len(plan.Unmapped) != 1 {
		t.Fatalf("Unmapped = %+v, want exactly 1", plan.Unmapped)
	}
	if plan.Unmapped[0].Kind != "totally_unknown_kind" || plan.Unmapped[0].Count != 1 {
		t.Errorf("Unmapped[0] = %+v, want totally_unknown_kind count 1", plan.Unmapped[0])
	}
	if len(plan.Unmapped[0].RevisionIDs) != 1 || plan.Unmapped[0].RevisionIDs[0] != bogusRev {
		t.Errorf("Unmapped[0].RevisionIDs = %v, want [%s]", plan.Unmapped[0].RevisionIDs, bogusRev)
	}

	if _, err := memory.ApplyKindMigration(ctx, db, plan); err == nil {
		t.Fatal("expected apply to refuse a plan carrying unmapped kinds")
	} else if !strings.Contains(err.Error(), "unmapped") {
		t.Errorf("error = %v, want it to mention unmapped kinds", err)
	}

	// Refusal must leave the database untouched — including the mappable row.
	if got := kindOf(t, db, mcpRev); got != "mcp-server" {
		t.Errorf("refused apply mutated a row: facet_kind = %q, want mcp-server", got)
	}
	if got := kindOf(t, db, bogusRev); got != "totally_unknown_kind" {
		t.Errorf("refused apply mutated the unmapped row: facet_kind = %q", got)
	}
}

func TestBuildKindMigrationPlan_PlansHistoricalRevisionsToo(t *testing.T) {
	ms, db := newKindFixture(t)
	ctx := context.Background()

	// Two revisions of the same memory, both carrying the off-vocabulary kind.
	first := writeKnowledge(t, ms, "mcp.server.cerberus", "mcp-server")
	second := writeKnowledge(t, ms, "mcp.server.cerberus", "mcp-server")
	if first == second {
		t.Fatal("expected the second write to mint a distinct revision")
	}

	plan, err := memory.BuildKindMigrationPlan(ctx, db)
	if err != nil {
		t.Fatalf("BuildKindMigrationPlan: %v", err)
	}
	if len(plan.Rows) != 2 {
		t.Fatalf("plan rows = %d, want 2 (head + historical); got %+v", len(plan.Rows), plan.Rows)
	}
	if plan.HeadRows != 1 {
		t.Errorf("HeadRows = %d, want 1", plan.HeadRows)
	}

	heads := map[string]bool{}
	for _, r := range plan.Rows {
		heads[r.RevisionID] = r.IsHead
	}
	if !heads[second] {
		t.Errorf("revision %s should be the head", second)
	}
	if heads[first] {
		t.Errorf("revision %s should be historical", first)
	}

	if _, err := memory.ApplyKindMigration(ctx, db, plan); err != nil {
		t.Fatalf("ApplyKindMigration: %v", err)
	}
	for _, rev := range []string{first, second} {
		if got := kindOf(t, db, rev); got != "mcp_server" {
			t.Errorf("revision %s facet_kind = %q, want mcp_server", rev, got)
		}
	}
}

func TestApplyKindMigration_RefusesStalePlan(t *testing.T) {
	ms, db := newKindFixture(t)
	ctx := context.Background()

	mcpRev := writeKnowledge(t, ms, "mcp.server.cerberus", "mcp-server")
	issueRev := writeKnowledge(t, ms, "issues.manual-field-dropped", "issue/bug")

	plan, err := memory.BuildKindMigrationPlan(ctx, db)
	if err != nil {
		t.Fatalf("BuildKindMigrationPlan: %v", err)
	}

	// Someone else changes the row out from under the plan.
	if _, err := db.Exec(`UPDATE memory_revisions SET facet_kind = 'note' WHERE revision_id = ?`, mcpRev); err != nil {
		t.Fatalf("out-of-band update: %v", err)
	}

	if _, err := memory.ApplyKindMigration(ctx, db, plan); err == nil {
		t.Fatal("expected apply to refuse a stale plan")
	} else if !strings.Contains(err.Error(), "stale") {
		t.Errorf("error = %v, want it to mention a stale plan", err)
	}

	// The transaction rolled back, so the other planned row is unchanged.
	if got := kindOf(t, db, issueRev); got != "issue/bug" {
		t.Errorf("stale-plan refusal was not rolled back: issue facet_kind = %q, want issue/bug", got)
	}
}

func TestBuildKindMigrationPlan_IgnoresMemoryDomain(t *testing.T) {
	ms, db := newKindFixture(t)
	ctx := context.Background()

	// A memory-domain row is out of scope even if it somehow carries a kind.
	writeKnowledge(t, ms, "mcp.server.cerberus", "mcp-server")
	in := sampleInput("some.memory.note")
	memRev, err := ms.WriteRevision(ctx, in)
	if err != nil {
		t.Fatalf("WriteRevision (memory): %v", err)
	}
	// WriteRevision correctly refuses this combination. Raw SQL is explicit
	// here because the migration planner must still scope itself safely when
	// inspecting a legacy/corrupt database.
	if _, fixtureErr := db.Exec(`UPDATE memory_revisions SET facet_kind = 'mcp-server' WHERE revision_id = ?`, memRev.RevisionID); fixtureErr != nil {
		t.Fatalf("raw invalid memory facet fixture: %v", fixtureErr)
	}

	plan, err := memory.BuildKindMigrationPlan(ctx, db)
	if err != nil {
		t.Fatalf("BuildKindMigrationPlan: %v", err)
	}
	if len(plan.Rows) != 1 {
		t.Errorf("plan rows = %d, want 1 (memory-domain rows excluded); got %+v", len(plan.Rows), plan.Rows)
	}
	if len(plan.Unmapped) != 0 {
		t.Errorf("Unmapped = %+v, want none (memory-domain NULL kinds excluded)", plan.Unmapped)
	}
	if !strings.Contains(plan.SourceFilter, "domain = 'knowledge'") {
		t.Errorf("SourceFilter = %q, want it scoped to the knowledge domain", plan.SourceFilter)
	}
}

func TestKindMigrationPlan_DigestBindsPlanContent(t *testing.T) {
	ms, db := newKindFixture(t)
	ctx := context.Background()

	writeKnowledge(t, ms, "mcp.server.cerberus", "mcp-server")

	first, err := memory.BuildKindMigrationPlan(ctx, db)
	if err != nil {
		t.Fatalf("BuildKindMigrationPlan: %v", err)
	}
	second, err := memory.BuildKindMigrationPlan(ctx, db)
	if err != nil {
		t.Fatalf("BuildKindMigrationPlan (second): %v", err)
	}
	if first.Digest() == "" {
		t.Fatal("digest is empty")
	}
	if first.Digest() != second.Digest() {
		t.Errorf("digest not stable across rebuilds: %s vs %s", first.Digest(), second.Digest())
	}

	// A row arriving after the plan was reviewed must change the digest.
	writeKnowledge(t, ms, "mcp.server.nanite", "mcp-server")
	third, err := memory.BuildKindMigrationPlan(ctx, db)
	if err != nil {
		t.Fatalf("BuildKindMigrationPlan (third): %v", err)
	}
	if third.Digest() == first.Digest() {
		t.Error("digest unchanged after a new off-vocabulary row landed")
	}

	// An unmapped kind is part of the fingerprint too: it changes what the
	// plan describes while leaving the mapped row count untouched, so a bare
	// --expect-rows check would not notice it.
	writeKnowledge(t, ms, "weird.thing", "totally_unknown_kind")
	fourth, err := memory.BuildKindMigrationPlan(ctx, db)
	if err != nil {
		t.Fatalf("BuildKindMigrationPlan (fourth): %v", err)
	}
	if len(fourth.Rows) != len(third.Rows) {
		t.Fatalf("row count = %d, want %d (unmapped rows are not plan rows)", len(fourth.Rows), len(third.Rows))
	}
	if fourth.Digest() == third.Digest() {
		t.Error("digest unchanged after an unmapped kind appeared at equal row count")
	}
}

func TestCountNonConformantKinds_AgreesWithPlanAndReachesZero(t *testing.T) {
	ms, db := newKindFixture(t)
	ctx := context.Background()

	writeKnowledge(t, ms, "mcp.server.cerberus", "mcp-server")
	writeKnowledge(t, ms, "mcp.server.nanite", "mcp-server")
	writeKnowledge(t, ms, "issues.dropped-field", "issue/bug")
	writeKnowledge(t, ms, "docs.some-doc", "doc")

	before, err := memory.CountNonConformantKinds(ctx, db)
	if err != nil {
		t.Fatalf("CountNonConformantKinds: %v", err)
	}
	if before != 3 {
		t.Errorf("non-conformant before = %d, want 3", before)
	}

	plan, err := memory.BuildKindMigrationPlan(ctx, db)
	if err != nil {
		t.Fatalf("BuildKindMigrationPlan: %v", err)
	}
	// The aggregate count and the per-row planner are separate code paths; on
	// a fully mappable corpus they must agree.
	if before != len(plan.Rows) {
		t.Errorf("aggregate count %d disagrees with planner row count %d", before, len(plan.Rows))
	}

	if _, applyErr := memory.ApplyKindMigration(ctx, db, plan); applyErr != nil {
		t.Fatalf("ApplyKindMigration: %v", applyErr)
	}

	after, err := memory.CountNonConformantKinds(ctx, db)
	if err != nil {
		t.Fatalf("CountNonConformantKinds (after): %v", err)
	}
	if after != 0 {
		t.Errorf("non-conformant after apply = %d, want 0", after)
	}
}

func TestCountNonConformantKinds_CountsUnmappableKinds(t *testing.T) {
	ms, db := newKindFixture(t)
	ctx := context.Background()

	writeKnowledge(t, ms, "weird.thing", "totally_unknown_kind")
	writeKnowledge(t, ms, "docs.some-doc", "doc")

	// An unmapped kind is non-conformant even though it is never a plan row —
	// otherwise the post-apply check could read zero with rows still stranded.
	n, err := memory.CountNonConformantKinds(ctx, db)
	if err != nil {
		t.Fatalf("CountNonConformantKinds: %v", err)
	}
	if n != 1 {
		t.Errorf("non-conformant = %d, want 1", n)
	}
}

func TestBuildKindMigrationPlan_EmptyPlanHasNonNilSlices(t *testing.T) {
	ms, db := newKindFixture(t)
	ctx := context.Background()

	writeKnowledge(t, ms, "docs.some-doc", "doc")

	plan, err := memory.BuildKindMigrationPlan(ctx, db)
	if err != nil {
		t.Fatalf("BuildKindMigrationPlan: %v", err)
	}
	if plan.Rows == nil {
		t.Error("Rows is nil; JSON consumers expect a list")
	}
	if plan.Unmapped == nil {
		t.Error("Unmapped is nil; JSON consumers expect a list")
	}

	blob, err := json.Marshal(plan)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, want := range []string{`"rows":[]`, `"unmapped":[]`} {
		if !strings.Contains(string(blob), want) {
			t.Errorf("JSON missing %s; got %s", want, blob)
		}
	}
}
