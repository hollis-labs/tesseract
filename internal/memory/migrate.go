package memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// typeNormalize maps legacy / singular type prefixes to their canonical form
// in the new {type} allowlist. Anything not in this map and not directly a
// member of the allowlist is treated as "no type prefix" and the row lands
// in the default `notes` bucket with the residual key preserved.
var typeNormalize = map[string]string{
	"decision":    "decisions",
	"decisions":   "decisions",
	"followup":    "followups",
	"followups":   "followups",
	"limitation":  "limitations",
	"limitations": "limitations",
	"feedback":    "feedback",
	"outcome":     "outcomes",
	"outcomes":    "outcomes",
	"reference":   "references",
	"references":  "references",
	"learning":    "learnings",
	"learnings":   "learnings",
	"note":        "notes",
	"notes":       "notes",
}

// metaPrefixDenylist is the set of leading-segment words that look project-like
// in the corpus but are actually meta-markers (type-like words, audit events,
// probes). Excluded from corpus-derived project detection AND never lifted as
// project: tags.
var metaPrefixDenylist = map[string]struct{}{
	"project":   {}, // project.<X>.<rest> pattern handled specially
	"projects":  {},
	"meta":      {},
	"audit":     {},
	"probe":     {},
	"note":      {},
	"notes":     {},
	"decision":  {},
	"decisions": {},
	"followup":  {},
	"followups": {},
}

// ticketPattern matches the cw_YYYYMMDD_NNNN canonical ticket segment
// (lowercase variant of CW-YYYYMMDD-NNNN used in memory keys).
var ticketPattern = regexp.MustCompile(`^cw_[0-9]{8}_[0-9]{4}$`)

// projectSegmentPattern matches a plausible project-slug segment: lowercase
// alphanumeric or underscore, length 2-40, must start with a letter.
var projectSegmentPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{1,39}$`)

// MigrationRow is one row in the migration plan.
type MigrationRow struct {
	MemoryID     string   `json:"memory_id"`
	OldNamespace string   `json:"old_namespace"`
	OldKey       string   `json:"old_key"`
	OldTags      []string `json:"old_tags"`
	NewNamespace string   `json:"new_namespace"`
	NewKey       string   `json:"new_key"`
	NewTags      []string `json:"new_tags"`
	Reason       string   `json:"reason"` // human-readable note about the mapping decision
}

// MigrationCollision marks two or more memory_ids that would resolve to the
// same (new_namespace, new_key) — keyless rows are excluded since keyless
// memories don't have unique key constraints.
type MigrationCollision struct {
	Namespace string   `json:"namespace"`
	Key       string   `json:"key"`
	MemoryIDs []string `json:"memory_ids"`
}

// MigrationPlan is the full mapping from current shape to new shape, plus
// any collisions the planner detected.
type MigrationPlan struct {
	Rows         []MigrationRow       `json:"rows"`
	Collisions   []MigrationCollision `json:"collisions"`
	ProjectSet   []string             `json:"project_set"`   // detected projects (corpus-derived)
	SourceFilter string               `json:"source_filter"` // SQL WHERE used to select source rows
}

// BuildMigrationPlan inspects the DB and returns the full migration plan.
// It does NOT mutate the database — pass the plan to ApplyMigration to do
// that. Caller must pre-validate that the DB is a copy of (or otherwise
// safe to mutate from) the production store.
//
// projectThreshold controls how many times a segment must appear as the
// post-type-strip leading segment before it is treated as a project name to
// be lifted into a tag. A threshold of 2 catches "tesseract", "nanite",
// "agent_ops" while leaving unique long-form keys alone.
func BuildMigrationPlan(ctx context.Context, db *sql.DB, projectThreshold int) (MigrationPlan, error) {
	if projectThreshold < 1 {
		projectThreshold = 2
	}

	// Source filter: memory-domain rows with a legacy flat namespace shape
	// (3-seg user, 5-seg project/session). Already-typed rows are skipped.
	// Filter is applied against `memory_state` (aliased s in the join).
	const filter = `s.domain = 'memory' AND (
		s.namespace GLOB 'user/*/memory'
		OR s.namespace GLOB 'user/*/project/*/memory'
		OR s.namespace GLOB 'user/*/session/*/memory'
	)`

	// Pass 1 — load all source rows (memory_id, namespace, memory_key, tags).
	rows, err := db.QueryContext(ctx, `
		SELECT s.memory_id, s.namespace, COALESCE(s.memory_key, ''), COALESCE(r.tags, '[]')
		FROM memory_state s
		LEFT JOIN memory_revisions r ON r.revision_id = s.current_revision
		WHERE `+filter, // nolint:gosec // constant filter
	)
	if err != nil {
		return MigrationPlan{}, fmt.Errorf("scan source rows: %w", err)
	}
	defer rows.Close()

	type rawRow struct {
		MemoryID  string
		Namespace string
		Key       string
		Tags      []string
	}
	var raws []rawRow
	for rows.Next() {
		var r rawRow
		var tagsJSON string
		if err := rows.Scan(&r.MemoryID, &r.Namespace, &r.Key, &tagsJSON); err != nil {
			return MigrationPlan{}, fmt.Errorf("scan row: %w", err)
		}
		if tagsJSON != "" {
			_ = json.Unmarshal([]byte(tagsJSON), &r.Tags)
		}
		raws = append(raws, r)
	}
	if rows.Err() != nil {
		return MigrationPlan{}, fmt.Errorf("iterate rows: %w", rows.Err())
	}

	// Pass 2 — frequency analysis of post-type-strip leading segments to
	// derive the project allowlist from the actual corpus. Meta-prefixes
	// (project/meta/audit/probe/...) are deny-listed: they're scoping
	// markers, not project names.
	leadingFreq := map[string]int{}
	for _, r := range raws {
		_, residual, _ := stripTypePrefix(r.Key)
		first := firstSegment(residual)
		if first == "" || ticketPattern.MatchString(first) || !projectSegmentPattern.MatchString(first) {
			continue
		}
		if _, denied := metaPrefixDenylist[first]; denied {
			continue
		}
		leadingFreq[first]++
	}
	projectSet := map[string]struct{}{}
	for seg, n := range leadingFreq {
		if n >= projectThreshold {
			projectSet[seg] = struct{}{}
		}
	}

	// Pass 3 — generate the plan.
	plan := MigrationPlan{
		ProjectSet:   sortedKeys(projectSet),
		SourceFilter: strings.Join(strings.Fields(filter), " "),
	}
	for _, r := range raws {
		mapped := mapRow(r.Namespace, r.Key, r.Tags, projectSet)
		mapped.MemoryID = r.MemoryID
		plan.Rows = append(plan.Rows, mapped)
	}

	// Collision detection: same (NewNamespace, NewKey) across multiple
	// memory_ids — keyless rows skipped (each is its own logical memory).
	collKey := func(ns, k string) string { return ns + "|" + k }
	collisions := map[string][]string{}
	for _, row := range plan.Rows {
		if row.NewKey == "" {
			continue
		}
		k := collKey(row.NewNamespace, row.NewKey)
		collisions[k] = append(collisions[k], row.MemoryID)
	}
	for k, ids := range collisions {
		if len(ids) < 2 {
			continue
		}
		parts := strings.SplitN(k, "|", 2)
		plan.Collisions = append(plan.Collisions, MigrationCollision{
			Namespace: parts[0],
			Key:       parts[1],
			MemoryIDs: ids,
		})
	}
	sort.Slice(plan.Collisions, func(i, j int) bool {
		if plan.Collisions[i].Namespace != plan.Collisions[j].Namespace {
			return plan.Collisions[i].Namespace < plan.Collisions[j].Namespace
		}
		return plan.Collisions[i].Key < plan.Collisions[j].Key
	})

	return plan, nil
}

// mapRow computes the (new_namespace, new_key, new_tags, reason) for one row.
// Idempotent on already-typed inputs (no-op).
func mapRow(oldNS, oldKey string, oldTags []string, projectSet map[string]struct{}) MigrationRow {
	newTags := dedupedCopy(oldTags)

	typ, residual, typeNote := stripTypePrefix(oldKey)
	residualSegs := splitDots(residual)

	// Meta-prefix handling: `project.<X>.<rest>` and `projects.<X>.<rest>`
	// encode scoping in the key. Lift the second segment as the project
	// regardless of frequency threshold, then drop the meta segment.
	if len(residualSegs) >= 2 {
		if first := residualSegs[0]; first == "project" || first == "projects" {
			candidate := residualSegs[1]
			if projectSegmentPattern.MatchString(candidate) {
				newTags = addTag(newTags, "project:"+candidate)
				residualSegs = residualSegs[2:]
				if typeNote != "" {
					typeNote += "; "
				}
				typeNote += "lifted-meta-prefix-" + first
			}
		}
	}

	// Opportunistic project lift from the corpus-derived projectSet
	// (only when the leading segment isn't a meta-prefix).
	if len(residualSegs) > 0 {
		first := residualSegs[0]
		if _, denied := metaPrefixDenylist[first]; !denied {
			if _, ok := projectSet[first]; ok {
				newTags = addTag(newTags, "project:"+first)
				residualSegs = residualSegs[1:]
			}
		}
	}
	if len(residualSegs) > 0 && ticketPattern.MatchString(residualSegs[0]) {
		newTags = addTag(newTags, "ticket:"+residualSegs[0])
		residualSegs = residualSegs[1:]
	}
	newKey := strings.Join(residualSegs, ".")

	// Build new namespace by appending /{type} to the existing legacy form.
	newNS := oldNS + "/" + typ

	reason := typeNote
	return MigrationRow{
		OldNamespace: oldNS,
		OldKey:       oldKey,
		OldTags:      oldTags,
		NewNamespace: newNS,
		NewKey:       newKey,
		NewTags:      newTags,
		Reason:       reason,
	}
}

// stripTypePrefix examines the first dot-segment of `key`, returns
// (canonical_type, residual_key, human_reason). When the first segment is
// not a recognized type (or the key has no segments), returns
// ("notes", key, "no-known-type-prefix-bucketed-to-notes").
func stripTypePrefix(key string) (string, string, string) {
	if key == "" {
		return "notes", "", "empty-key-bucketed-to-notes"
	}
	first := firstSegment(key)
	canon, ok := typeNormalize[first]
	if !ok {
		return "notes", key, "no-known-type-prefix-bucketed-to-notes"
	}
	residual := strings.TrimPrefix(key, first)
	residual = strings.TrimPrefix(residual, ".")
	reason := "stripped-type-prefix"
	if first != canon {
		reason = "normalized-type-prefix-" + first + "-to-" + canon
	}
	return canon, residual, reason
}

func firstSegment(s string) string {
	if i := strings.IndexByte(s, '.'); i >= 0 {
		return s[:i]
	}
	return s
}

func splitDots(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, ".")
}

func dedupedCopy(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(in))
	for _, t := range in {
		if _, ok := seen[t]; ok {
			continue
		}
		seen[t] = struct{}{}
		out = append(out, t)
	}
	return out
}

func addTag(tags []string, tag string) []string {
	for _, t := range tags {
		if t == tag {
			return tags
		}
	}
	return append(tags, tag)
}

func sortedKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ApplyMigration runs the plan against the database in a single transaction.
// Both `memory_state.namespace`/`memory_key` and `memory_revisions.namespace`
// /`memory_key`/`tags` are updated in-place, keyed on `memory_id` — the FK
// memory_revisions.memory_id -> memory_state.memory_id stays valid since
// memory_id never changes. Returns the number of state rows + revision rows
// updated, or an error (rolling back) on the first failure.
//
// Behavior on collisions: returns an error WITHOUT mutating anything if the
// plan carries any collisions. Caller must resolve them before re-attempting.
func ApplyMigration(ctx context.Context, db *sql.DB, plan MigrationPlan) (stateUpdates, revisionUpdates int, err error) {
	if len(plan.Collisions) > 0 {
		return 0, 0, fmt.Errorf("plan has %d collisions; resolve before applying", len(plan.Collisions))
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, fmt.Errorf("begin tx: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	stateStmt, err := tx.PrepareContext(ctx, `UPDATE memory_state SET namespace = ?, memory_key = ? WHERE memory_id = ?`)
	if err != nil {
		return 0, 0, fmt.Errorf("prepare state update: %w", err)
	}
	defer stateStmt.Close()

	revStmt, err := tx.PrepareContext(ctx, `UPDATE memory_revisions SET namespace = ?, memory_key = ?, tags = ? WHERE memory_id = ?`)
	if err != nil {
		return 0, 0, fmt.Errorf("prepare revisions update: %w", err)
	}
	defer revStmt.Close()

	for _, row := range plan.Rows {
		// Some keyless rows store '' or NULL for memory_state.memory_key;
		// preserve NULL by binding sql.NullString when empty.
		var keyArg interface{} = row.NewKey
		if row.NewKey == "" {
			keyArg = sql.NullString{}
		}
		res, exErr := stateStmt.ExecContext(ctx, row.NewNamespace, keyArg, row.MemoryID)
		if exErr != nil {
			return stateUpdates, revisionUpdates, fmt.Errorf("update state %s: %w", row.MemoryID, exErr)
		}
		if n, _ := res.RowsAffected(); n > 0 {
			stateUpdates += int(n)
		}

		tagsJSON, jerr := json.Marshal(row.NewTags)
		if jerr != nil {
			return stateUpdates, revisionUpdates, fmt.Errorf("marshal tags for %s: %w", row.MemoryID, jerr)
		}
		res, exErr = revStmt.ExecContext(ctx, row.NewNamespace, keyArg, string(tagsJSON), row.MemoryID)
		if exErr != nil {
			return stateUpdates, revisionUpdates, fmt.Errorf("update revisions %s: %w", row.MemoryID, exErr)
		}
		if n, _ := res.RowsAffected(); n > 0 {
			revisionUpdates += int(n)
		}
	}

	if cerr := tx.Commit(); cerr != nil {
		return stateUpdates, revisionUpdates, fmt.Errorf("commit: %w", cerr)
	}
	return stateUpdates, revisionUpdates, nil
}
