package corpusmigration

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hollis-labs/tesseract/internal/memorylinks"
)

// BacklogApplyOutcome represents the disposition of a backlog record during apply.
type BacklogApplyOutcome string

const (
	OutcomeAppliedTier123      BacklogApplyOutcome = "applied-via-tier1/2/3"
	OutcomeAppliedTier4System  BacklogApplyOutcome = "applied-via-tier4-system-default"
	OutcomeSkippedConflict     BacklogApplyOutcome = "skipped-conflict"
	OutcomeSkippedNonCanonical BacklogApplyOutcome = "skipped-non-canonical"
	OutcomeSkippedAgridd       BacklogApplyOutcome = "skipped-agridd"
)

// ExcludedNonCanonicalProjects defines the set of non-canonical project slugs parked pending CW-20260914-0048.
var ExcludedNonCanonicalProjects = map[string]struct{}{
	"clockwork-manifold":     {},
	"go-providers":           {},
	"go-agent-sessions":      {},
	"vanta-conduit":          {},
	"go-envelopes":           {},
	"portfolio":              {},
	"agent-mux":              {},
	"go-runner":              {},
	"folio":                  {},
	"hollis-labs":            {},
	"go-mcp-sanitize":        {},
	"go-apppaths":            {},
	"go-sandbox":             {},
	"go-toolbroker":          {},
	"torque-messaging":       {},
	"shared-materialization": {},
}

// MatchesAgriddPattern returns true if memory_key or any tag matches "agridd" (case-insensitive).
func MatchesAgriddPattern(rec BacklogRecordInput) bool {
	if strings.Contains(strings.ToLower(rec.MemoryKey), "agridd") {
		return true
	}
	for _, tag := range rec.Tags {
		if strings.Contains(strings.ToLower(tag), "agridd") {
			return true
		}
	}
	return false
}

// BacklogRecordDisposition details the planned action and rationale for a backlog record.
type BacklogRecordDisposition struct {
	MemoryID        string              `json:"memory_id"`
	OldNamespace    string              `json:"old_namespace"`
	TargetNamespace string              `json:"target_namespace"`
	Domain          string              `json:"domain"`
	MemoryKey       string              `json:"memory_key"`
	Summary         string              `json:"summary"`
	SummarySnippet  string              `json:"summary_snippet"`
	Tags            []string            `json:"tags"`
	MatchedProject  string              `json:"matched_project,omitempty"`
	SignalTier      SignalTier          `json:"signal_tier"`
	Outcome         BacklogApplyOutcome `json:"outcome"`
	SkipReason      string              `json:"skip_reason,omitempty"`
}

// DetermineDisposition evaluates a classified backlog record to determine if it should be applied or skipped.
func DetermineDisposition(rec BacklogRecordInput, res ClassificationResult) BacklogRecordDisposition {
	d := BacklogRecordDisposition{
		MemoryID:        rec.MemoryID,
		OldNamespace:    rec.Namespace,
		TargetNamespace: res.TargetNamespace,
		Domain:          rec.Domain,
		MemoryKey:       rec.MemoryKey,
		Summary:         rec.Summary,
		SummarySnippet:  res.SummarySnippet,
		Tags:            rec.Tags,
		MatchedProject:  res.MatchedProject,
		SignalTier:      res.SignalTier,
	}

	// 1. Conflict: Multiple candidate projects matched
	if res.SignalTier == TierConflict {
		d.Outcome = OutcomeSkippedConflict
		d.SkipReason = res.ConflictReason
		d.TargetNamespace = rec.Namespace // unchanged
		return d
	}

	// 2. Tier 4: System default
	if res.SignalTier == Tier4SystemDefault {
		if MatchesAgriddPattern(rec) {
			d.Outcome = OutcomeSkippedAgridd
			d.SkipReason = "agridd exclusion: experimental Nanite fork deferred pending CW-20260914-0048"
			d.TargetNamespace = rec.Namespace // unchanged
			return d
		}
		d.Outcome = OutcomeAppliedTier4System
		return d
	}

	// 3. Tier 1/2/3: Canonical project match
	if _, isCanonical := CanonicalPortfolioProjects[res.MatchedProject]; isCanonical {
		d.Outcome = OutcomeAppliedTier123
		return d
	}

	// 4. Non-canonical project match
	d.Outcome = OutcomeSkippedNonCanonical
	if _, isKnownExcluded := ExcludedNonCanonicalProjects[res.MatchedProject]; isKnownExcluded {
		d.SkipReason = fmt.Sprintf("non-canonical project %q parked pending CW-20260914-0048", res.MatchedProject)
	} else {
		d.SkipReason = fmt.Sprintf("non-canonical project %q not in CanonicalPortfolioProjects", res.MatchedProject)
	}
	d.TargetNamespace = rec.Namespace // unchanged
	return d
}

// BacklogCollision marks an apply operation that would collide on a target key.
type BacklogCollision struct {
	TargetNamespace string   `json:"target_namespace"`
	Key             string   `json:"key"`
	ItemIDs         []string `json:"item_ids"`
	Reason          string   `json:"reason"`
}

// BacklogApplyPlan represents the complete planned apply operations and statistics.
type BacklogApplyPlan struct {
	TotalRecords             int                        `json:"total_records"`
	TotalApplied             int                        `json:"total_applied"`
	TotalSkipped             int                        `json:"total_skipped"`
	AppliedTier123Count      int                        `json:"applied_tier123_count"`
	AppliedTier4SystemCount  int                        `json:"applied_tier4_system_count"`
	SkippedConflictCount     int                        `json:"skipped_conflict_count"`
	SkippedNonCanonicalCount int                        `json:"skipped_non_canonical_count"`
	SkippedAgriddCount       int                        `json:"skipped_agridd_count"`
	AppliedCountsByProject   map[string]int             `json:"applied_counts_by_project"`
	AppliedCountsByTargetNS  map[string]int             `json:"applied_counts_by_target_ns"`
	SkippedCountsByReason    map[string]int             `json:"skipped_counts_by_reason"`
	AppliedRecords           []BacklogRecordDisposition `json:"applied_records"`
	SkippedRecords           []BacklogRecordDisposition `json:"skipped_records"`
	Collisions               []BacklogCollision         `json:"collisions,omitempty"`
}

// BacklogApplyReceipt reports execution results after applying the backlog plan.
type BacklogApplyReceipt struct {
	TotalApplied         int            `json:"total_applied"`
	TotalSkipped         int            `json:"total_skipped"`
	UpdatedStateRows     int            `json:"updated_state_rows"`
	UpdatedRevisionRows  int            `json:"updated_revision_rows"`
	UpdatedRecordRows    int            `json:"updated_record_rows"`
	UpdatedHeadRows      int            `json:"updated_head_rows"`
	RegisteredNamespaces int            `json:"registered_namespaces"`
	WikilinkDiff         *WikilinkDiff  `json:"wikilink_diff,omitempty"`
	LineageReport        *LineageReport `json:"lineage_report,omitempty"`
	Errors               []string       `json:"errors,omitempty"`
}

// BuildBacklogApplyPlan inspects backlog records in memory_state and determines the full apply plan.
func BuildBacklogApplyPlan(ctx context.Context, db *sql.DB) (*BacklogApplyPlan, error) {
	query := `
SELECT 
    s.memory_id,
    s.namespace,
    s.domain,
    COALESCE(s.memory_key, ''),
    COALESCE(r.payload_summary, ''),
    COALESCE(r.tags, '[]')
FROM memory_state s
LEFT JOIN memory_revisions r ON r.revision_id = s.current_revision
WHERE (s.namespace LIKE 'user/chrispian/memory/%' OR s.namespace = 'user/chrispian/event/reasoning')
  AND s.current_revision IS NOT NULL AND (r.status IS NULL OR r.status <> 'deprecated')
ORDER BY s.namespace, s.memory_key, s.memory_id`

	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("query backlog records: %w", err)
	}
	defer func() { _ = rows.Close() }()

	plan := &BacklogApplyPlan{
		AppliedCountsByProject:  make(map[string]int),
		AppliedCountsByTargetNS: make(map[string]int),
		SkippedCountsByReason:   make(map[string]int),
	}

	for rows.Next() {
		var rec BacklogRecordInput
		var tagsJSON string
		if scanErr := rows.Scan(&rec.MemoryID, &rec.Namespace, &rec.Domain, &rec.MemoryKey, &rec.Summary, &tagsJSON); scanErr != nil {
			return nil, fmt.Errorf("scan backlog record: %w", scanErr)
		}
		if tagsJSON != "" {
			_ = json.Unmarshal([]byte(tagsJSON), &rec.Tags)
		}

		classRes := ClassifyRecord(rec)
		disp := DetermineDisposition(rec, classRes)
		plan.TotalRecords++

		switch disp.Outcome {
		case OutcomeAppliedTier123:
			plan.TotalApplied++
			plan.AppliedTier123Count++
			plan.AppliedCountsByProject[disp.MatchedProject]++
			plan.AppliedCountsByTargetNS[disp.TargetNamespace]++
			plan.AppliedRecords = append(plan.AppliedRecords, disp)
		case OutcomeAppliedTier4System:
			plan.TotalApplied++
			plan.AppliedTier4SystemCount++
			plan.AppliedCountsByProject["system"]++
			plan.AppliedCountsByTargetNS[disp.TargetNamespace]++
			plan.AppliedRecords = append(plan.AppliedRecords, disp)
		case OutcomeSkippedConflict:
			plan.TotalSkipped++
			plan.SkippedConflictCount++
			plan.SkippedCountsByReason[string(OutcomeSkippedConflict)]++
			plan.SkippedRecords = append(plan.SkippedRecords, disp)
		case OutcomeSkippedNonCanonical:
			plan.TotalSkipped++
			plan.SkippedNonCanonicalCount++
			plan.SkippedCountsByReason[string(OutcomeSkippedNonCanonical)]++
			plan.SkippedRecords = append(plan.SkippedRecords, disp)
		case OutcomeSkippedAgridd:
			plan.TotalSkipped++
			plan.SkippedAgriddCount++
			plan.SkippedCountsByReason[string(OutcomeSkippedAgridd)]++
			plan.SkippedRecords = append(plan.SkippedRecords, disp)
		}
	}

	if rErr := rows.Err(); rErr != nil {
		return nil, fmt.Errorf("iterate backlog records: %w", rErr)
	}

	collisions, err := detectBacklogCollisions(ctx, db, plan)
	if err != nil {
		return nil, fmt.Errorf("detect backlog collisions: %w", err)
	}
	plan.Collisions = collisions

	return plan, nil
}

func detectBacklogCollisions(ctx context.Context, db *sql.DB, plan *BacklogApplyPlan) ([]BacklogCollision, error) {
	var collisions []BacklogCollision

	// 1. Intra-plan collisions: two records being moved to the same target namespace with the same key
	seen := make(map[string]string) // "targetNS/key" -> memory_id
	for _, rec := range plan.AppliedRecords {
		if rec.MemoryKey == "" {
			continue
		}
		composite := fmt.Sprintf("%s/%s", rec.TargetNamespace, rec.MemoryKey)
		if prevID, exists := seen[composite]; exists {
			collisions = append(collisions, BacklogCollision{
				TargetNamespace: rec.TargetNamespace,
				Key:             rec.MemoryKey,
				ItemIDs:         []string{prevID, rec.MemoryID},
				Reason:          "intra-plan target key collision in memory_state",
			})
		} else {
			seen[composite] = rec.MemoryID
		}
	}

	// 2. Collisions with existing records in target namespace in memory_state
	checkStmt, err := db.PrepareContext(ctx, `
SELECT memory_id 
FROM memory_state 
WHERE namespace = ? AND memory_key = ? AND memory_id <> ?
LIMIT 1`)
	if err != nil {
		return nil, fmt.Errorf("prepare collision check: %w", err)
	}
	defer func() { _ = checkStmt.Close() }()

	for _, rec := range plan.AppliedRecords {
		if rec.MemoryKey == "" {
			continue
		}
		if rec.TargetNamespace == rec.OldNamespace {
			continue
		}
		var occupiedID string
		err := checkStmt.QueryRowContext(ctx, rec.TargetNamespace, rec.MemoryKey, rec.MemoryID).Scan(&occupiedID)
		if err == nil && occupiedID != "" {
			collisions = append(collisions, BacklogCollision{
				TargetNamespace: rec.TargetNamespace,
				Key:             rec.MemoryKey,
				ItemIDs:         []string{occupiedID, rec.MemoryID},
				Reason:          "target namespace already occupied by existing record",
			})
		} else if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("query existing target key: %w", err)
		}
	}

	return collisions, nil
}

// ApplyBacklogPlan applies the planned backlog renames to the database atomically.
func ApplyBacklogPlan(ctx context.Context, db *sql.DB, plan *BacklogApplyPlan) (*BacklogApplyReceipt, error) {
	if len(plan.Collisions) > 0 {
		return nil, fmt.Errorf("refusing to apply backlog migration: %d collision(s) detected", len(plan.Collisions))
	}

	receipt := &BacklogApplyReceipt{
		TotalApplied: len(plan.AppliedRecords),
		TotalSkipped: len(plan.SkippedRecords),
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin apply tx: %w", err)
	}
	defer func() {
		if tx != nil {
			_ = tx.Rollback()
		}
	}()

	stateStmt, err := tx.PrepareContext(ctx, `UPDATE memory_state SET namespace = ? WHERE memory_id = ?`)
	if err != nil {
		return nil, fmt.Errorf("prepare memory_state update: %w", err)
	}
	defer func() { _ = stateStmt.Close() }()

	revStmt, err := tx.PrepareContext(ctx, `UPDATE memory_revisions SET namespace = ? WHERE memory_id = ?`)
	if err != nil {
		return nil, fmt.Errorf("prepare memory_revisions update: %w", err)
	}
	defer func() { _ = revStmt.Close() }()

	recStmt, err := tx.PrepareContext(ctx, `UPDATE records SET namespace = ? WHERE namespace = ? AND key_name = ?`)
	if err != nil {
		return nil, fmt.Errorf("prepare records update: %w", err)
	}
	defer func() { _ = recStmt.Close() }()

	headStmt, err := tx.PrepareContext(ctx, `UPDATE heads SET namespace = ? WHERE namespace = ? AND key_name = ?`)
	if err != nil {
		return nil, fmt.Errorf("prepare heads update: %w", err)
	}
	defer func() { _ = headStmt.Close() }()

	npInsertStmt, err := tx.PrepareContext(ctx, `INSERT OR IGNORE INTO namespace_policies (namespace, owner_type, owner_id, policy_json, updated_at) VALUES (?, 'system', 'system', '{}', ?)`)
	if err != nil {
		return nil, fmt.Errorf("prepare namespace_policies insert: %w", err)
	}
	defer func() { _ = npInsertStmt.Close() }()

	now := time.Now().UTC().Format(time.RFC3339)
	registeredNamespaces := make(map[string]struct{})

	for _, rec := range plan.AppliedRecords {
		// 1. Update memory_state
		res, exErr := stateStmt.ExecContext(ctx, rec.TargetNamespace, rec.MemoryID)
		if exErr != nil {
			return nil, fmt.Errorf("update memory_state for %s (%s -> %s): %w", rec.MemoryID, rec.OldNamespace, rec.TargetNamespace, exErr)
		}
		if n, _ := res.RowsAffected(); n > 0 {
			receipt.UpdatedStateRows += int(n)
		}

		// 2. Update memory_revisions
		res, exErr = revStmt.ExecContext(ctx, rec.TargetNamespace, rec.MemoryID)
		if exErr != nil {
			return nil, fmt.Errorf("update memory_revisions for %s (%s -> %s): %w", rec.MemoryID, rec.OldNamespace, rec.TargetNamespace, exErr)
		}
		if n, _ := res.RowsAffected(); n > 0 {
			receipt.UpdatedRevisionRows += int(n)
		}

		// 3. Update records and heads if key is present
		if rec.MemoryKey != "" {
			res, exErr = recStmt.ExecContext(ctx, rec.TargetNamespace, rec.OldNamespace, rec.MemoryKey)
			if exErr == nil {
				if n, _ := res.RowsAffected(); n > 0 {
					receipt.UpdatedRecordRows += int(n)
				}
			}

			res, exErr = headStmt.ExecContext(ctx, rec.TargetNamespace, rec.OldNamespace, rec.MemoryKey)
			if exErr == nil {
				if n, _ := res.RowsAffected(); n > 0 {
					receipt.UpdatedHeadRows += int(n)
				}
			}
		}

		// 4. Ensure target namespace is registered in namespace_policies
		if _, seen := registeredNamespaces[rec.TargetNamespace]; !seen {
			if _, npErr := npInsertStmt.ExecContext(ctx, rec.TargetNamespace, now); npErr == nil {
				registeredNamespaces[rec.TargetNamespace] = struct{}{}
			}
		}
	}

	receipt.RegisteredNamespaces = len(registeredNamespaces)

	if commitErr := tx.Commit(); commitErr != nil {
		return nil, fmt.Errorf("commit apply tx: %w", commitErr)
	}
	tx = nil // committed successfully

	// Re-resolve pending wikilinks for applied keys
	resolveTx, rErr := db.BeginTx(ctx, nil)
	if rErr == nil {
		resolver := memorylinks.TxResolver{Tx: resolveTx}
		keySet := make(map[string]struct{})
		for _, rec := range plan.AppliedRecords {
			if rec.MemoryKey != "" {
				keySet[rec.MemoryKey] = struct{}{}
			}
		}
		for k := range keySet {
			_ = memorylinks.ResolvePending(ctx, resolveTx, resolver, k)
		}
		_ = resolveTx.Commit()
	}

	return receipt, nil
}
