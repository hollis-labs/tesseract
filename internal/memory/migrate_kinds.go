package memory

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
)

// --- Knowledge kind taxonomy normalization -------------------------------
//
// This file is the kind-migration counterpart to the namespace migration in
// migrate.go. It is deliberately a PARALLEL plan/apply pair rather than an
// extension of MigrationRow: a namespace migration derives a corpus project
// set and rewrites identity (namespace / memory_key / tags), while a kind
// migration is a lookup table against a fixed vocabulary and rewrites one
// non-identity column. The two share a shape (plan-then-apply, mutation-free
// planning, single transaction, refusal before apply, counts for evidence)
// but not a row struct.
//
// Scope note: `facet_kind` lives on `memory_revisions` only — `memory_state`
// carries no facet columns — so the apply is a SINGLE statement, unlike the
// namespace migration's state+revisions pair.

// knowledgeKindVocabulary is the target `facet_kind` vocabulary for the
// knowledge domain that this migration normalizes toward.
//
// It is the taxonomy locked 2026-05-14 (nine kinds) plus two kinds promoted
// to canonical because a shipped producer emits them systematically:
// `mcp_server` (slug-normalized from `mcp-server` to match enum style) and
// `investigation`.
//
// IMPORTANT: this set is the MIGRATION TARGET, not a write-path enum. It says
// which values this migration considers already-conformant; it does not
// validate or reject anything on write. Which subset the knowledge write path
// enforces is a separate decision made at the enforcement step — in
// particular `playbook` is carried in the vocabulary here despite having zero
// producers in the corpus, and whether it belongs in an enforced set is not
// decided by this file.
var knowledgeKindVocabulary = map[string]struct{}{
	"session_close":     {},
	"project_canonical": {},
	"playbook":          {},
	"doc":               {},
	"package":           {},
	"learning":          {},
	"handoff":           {},
	"note":              {},
	"pointer":           {},
	"mcp_server":        {},
	"investigation":     {},
}

// kindMapping is the replacement for one off-vocabulary kind.
type kindMapping struct {
	NewKind string
	Reason  string
}

// kindMigrations maps each off-vocabulary `facet_kind` observed in the corpus
// to its canonical replacement. A kind that is off-vocabulary and absent from
// this map is reported as unmapped and BLOCKS the apply — leaving an unknown
// value in place would strand those rows once the write path enforces the
// vocabulary.
var kindMigrations = map[string]kindMapping{
	// Promoted to canonical (a shipped producer emits it systematically); the
	// slug is normalized from hyphen to underscore to match enum style.
	"mcp-server": {NewKind: "mcp_server", Reason: "promoted-to-canonical-slug-normalized-to-underscore"},
	// Retired: issues are Torque-domain entities, not a Tesseract kind. The
	// record itself stays as a generic knowledge note.
	"issue/bug": {NewKind: "note", Reason: "retired-kind-torque-owns-issues-remapped-to-note"},
}

// KindMigrationRow is one row in the kind migration plan. It is keyed on
// revision_id, not memory_id: `facet_kind` is a per-revision column, so
// normalizing by memory_id would rewrite every revision of that memory
// including ones that already hold a conformant value.
type KindMigrationRow struct {
	RevisionID string `json:"revision_id"`
	MemoryID   string `json:"memory_id"`
	Namespace  string `json:"namespace"`
	MemoryKey  string `json:"memory_key"`
	OldKind    string `json:"old_kind"`
	NewKind    string `json:"new_kind"`
	Reason     string `json:"reason"`
	// IsHead reports whether this revision is the current head of its memory
	// (memory_state.current_revision). Non-head rows are historical revisions.
	IsHead bool `json:"is_head"`
}

// KindMigrationUnmapped is an off-vocabulary kind with no entry in
// kindMigrations. Its presence in a plan refuses the apply — it is the
// kind-migration analogue of the namespace migration's collision check.
type KindMigrationUnmapped struct {
	Kind        string   `json:"kind"`
	Count       int      `json:"count"`
	RevisionIDs []string `json:"revision_ids"`
}

// KindMigrationPlan is the full set of `facet_kind` rewrites plus any
// off-vocabulary kinds the planner could not map.
type KindMigrationPlan struct {
	Rows       []KindMigrationRow      `json:"rows"`
	Unmapped   []KindMigrationUnmapped `json:"unmapped"`
	Vocabulary []string                `json:"target_vocabulary"`
	// HeadRows is how many of Rows are current heads; the remainder are
	// historical revisions carrying the same off-vocabulary value.
	HeadRows     int    `json:"head_rows"`
	SourceFilter string `json:"source_filter"`
}

// nullKindLabel is how a NULL/empty facet_kind is rendered in a plan. Such a
// row is off-vocabulary and unmappable, so it surfaces under Unmapped.
const nullKindLabel = "(null)"

// BuildKindMigrationPlan scans the knowledge domain for `facet_kind` values
// outside knowledgeKindVocabulary and returns the rewrite plan. It does NOT
// mutate the database — pass the plan to ApplyKindMigration to do that.
//
// Every revision carrying an off-vocabulary value is planned, not just current
// heads: the value is invalid as a value, so a historical revision holding it
// would otherwise still surface an off-vocabulary kind through a
// revision-scoped read.
func BuildKindMigrationPlan(ctx context.Context, db *sql.DB) (KindMigrationPlan, error) {
	vocab := sortedKeys(knowledgeKindVocabulary)

	// Guard against a mapping table that points outside the vocabulary — that
	// would migrate rows to a value the next step would reject.
	for old, m := range kindMigrations {
		if _, ok := knowledgeKindVocabulary[m.NewKind]; !ok {
			return KindMigrationPlan{}, fmt.Errorf("mapping %q -> %q targets a kind outside the vocabulary", old, m.NewKind)
		}
	}

	// NULL is not matched by NOT IN, so it is called out explicitly; the empty
	// string is caught by NOT IN since '' is not a vocabulary member.
	filter := `r.domain = 'knowledge' AND (r.facet_kind IS NULL OR r.facet_kind NOT IN (` +
		placeholders(len(vocab)) + `))`
	args := make([]any, 0, len(vocab))
	for _, k := range vocab {
		args = append(args, k)
	}

	rows, err := db.QueryContext(ctx, `
		SELECT r.revision_id, r.memory_id, r.namespace, COALESCE(r.memory_key, ''),
		       COALESCE(r.facet_kind, ''), (s.memory_id IS NOT NULL)
		FROM memory_revisions r
		LEFT JOIN memory_state s ON s.current_revision = r.revision_id
		WHERE `+filter+`
		ORDER BY r.facet_kind, r.namespace, r.memory_key`, args...) // nolint:gosec // placeholders only
	if err != nil {
		return KindMigrationPlan{}, fmt.Errorf("scan off-vocabulary rows: %w", err)
	}
	defer rows.Close()

	plan := KindMigrationPlan{
		Vocabulary:   vocab,
		SourceFilter: strings.Join(strings.Fields(filter), " "),
	}
	unmapped := map[string][]string{}

	for rows.Next() {
		var r KindMigrationRow
		var isHead int
		if err := rows.Scan(&r.RevisionID, &r.MemoryID, &r.Namespace, &r.MemoryKey, &r.OldKind, &isHead); err != nil {
			return KindMigrationPlan{}, fmt.Errorf("scan row: %w", err)
		}
		r.IsHead = isHead != 0

		mapping, ok := kindMigrations[r.OldKind]
		if !ok {
			label := r.OldKind
			if label == "" {
				label = nullKindLabel
			}
			unmapped[label] = append(unmapped[label], r.RevisionID)
			continue
		}
		r.NewKind = mapping.NewKind
		r.Reason = mapping.Reason
		if r.IsHead {
			plan.HeadRows++
		}
		plan.Rows = append(plan.Rows, r)
	}
	if rows.Err() != nil {
		return KindMigrationPlan{}, fmt.Errorf("iterate rows: %w", rows.Err())
	}

	for kind, ids := range unmapped {
		plan.Unmapped = append(plan.Unmapped, KindMigrationUnmapped{
			Kind:        kind,
			Count:       len(ids),
			RevisionIDs: ids,
		})
	}
	sort.Slice(plan.Unmapped, func(i, j int) bool { return plan.Unmapped[i].Kind < plan.Unmapped[j].Kind })

	return plan, nil
}

// ApplyKindMigration runs the plan against the database in a single
// transaction, updating `memory_revisions.facet_kind` in place. Revision IDs
// and supersedes lineage are preserved — a taxonomy conformance change is the
// allowed value set changing, not a new revision superseding an old one.
//
// Refusals (both leave the database untouched):
//   - the plan carries unmapped off-vocabulary kinds;
//   - a row's target kind is outside the vocabulary.
//
// The UPDATE is additionally guarded on the old kind, so a plan built against
// a database that has since changed fails loudly as stale instead of writing
// a mapping the caller never reviewed. Re-running after a successful apply is
// a no-op: the rescan finds nothing off-vocabulary and the plan is empty.
func ApplyKindMigration(ctx context.Context, db *sql.DB, plan KindMigrationPlan) (revisionUpdates int, err error) {
	if len(plan.Unmapped) > 0 {
		return 0, fmt.Errorf("plan has %d unmapped off-vocabulary kind(s); resolve before applying", len(plan.Unmapped))
	}
	for _, row := range plan.Rows {
		if _, ok := knowledgeKindVocabulary[row.NewKind]; !ok {
			return 0, fmt.Errorf("row %s targets kind %q outside the vocabulary", row.RevisionID, row.NewKind)
		}
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin tx: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	stmt, err := tx.PrepareContext(ctx,
		`UPDATE memory_revisions SET facet_kind = ? WHERE revision_id = ? AND facet_kind = ?`)
	if err != nil {
		return 0, fmt.Errorf("prepare kind update: %w", err)
	}
	defer stmt.Close()

	var stale []string
	for _, row := range plan.Rows {
		res, exErr := stmt.ExecContext(ctx, row.NewKind, row.RevisionID, row.OldKind)
		if exErr != nil {
			return revisionUpdates, fmt.Errorf("update revision %s: %w", row.RevisionID, exErr)
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			stale = append(stale, row.RevisionID)
			continue
		}
		revisionUpdates += int(n)
	}
	if len(stale) > 0 {
		return revisionUpdates, fmt.Errorf("plan is stale: %d row(s) no longer hold the planned old kind (%s); rebuild the plan",
			len(stale), strings.Join(stale, ", "))
	}

	if cerr := tx.Commit(); cerr != nil {
		return revisionUpdates, fmt.Errorf("commit: %w", cerr)
	}
	return revisionUpdates, nil
}

// KnowledgeKindVocabulary returns the sorted target kind vocabulary. Exported
// for the CLI summary and for tests; see the note on
// knowledgeKindVocabulary — this is a migration target, not a write-path enum.
func KnowledgeKindVocabulary() []string { return sortedKeys(knowledgeKindVocabulary) }
