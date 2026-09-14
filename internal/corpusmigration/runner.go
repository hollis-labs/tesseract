package corpusmigration

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/hollis-labs/tesseract/domains"
	"github.com/hollis-labs/tesseract/internal/memory"
	"github.com/hollis-labs/tesseract/internal/memorylinks"
	"github.com/hollis-labs/tesseract/internal/promotion"
	"github.com/hollis-labs/tesseract/internal/workspace"
)

// NamespaceStat represents migration details for one namespace.
type NamespaceStat struct {
	OldNamespace  string         `json:"old_namespace"`
	NewNamespace  string         `json:"new_namespace"`
	Domain        domains.Domain `json:"domain"`
	Category      RuleCategory   `json:"category"`
	Action        Action         `json:"action"`
	StateRows     int            `json:"state_rows"`
	RevisionRows  int            `json:"revision_rows"`
	RecordRows    int            `json:"record_rows"`
	HeadRows      int            `json:"head_rows"`
	WorkspaceRows int            `json:"workspace_rows"`
	TotalRows     int            `json:"total_rows"`
	Reason        string         `json:"reason"`
	IsAmbiguous   bool           `json:"is_ambiguous,omitempty"`
	Confidence    string         `json:"confidence,omitempty"`
}

// ReclassificationRecord specifies one record to promote across domains.
type ReclassificationRecord struct {
	MemoryID        string         `json:"memory_id"`
	RevisionID      string         `json:"revision_id"`
	SourceDomain    domains.Domain `json:"source_domain"`
	TargetDomain    domains.Domain `json:"target_domain"`
	SourceNamespace string         `json:"source_namespace"`
	TargetNamespace string         `json:"target_namespace"`
	Key             string         `json:"key"`
	TargetKey       string         `json:"target_key"`
	Summary         string         `json:"summary"`
	Tags            []string       `json:"tags"`
	TargetTags      []string       `json:"target_tags"`
	Category        RuleCategory   `json:"category"`
	Reason          string         `json:"reason"`
}

// MigrationCollision marks two or more items that would collide on the same target.
type MigrationCollision struct {
	TargetNamespace string   `json:"target_namespace"`
	Key             string   `json:"key"`
	ItemIDs         []string `json:"item_ids"`
	Source          string   `json:"source"`
}

// MigrationPlan represents the full migration execution plan.
type MigrationPlan struct {
	Namespaces        []NamespaceStat          `json:"namespaces"`
	Reclassifications []ReclassificationRecord `json:"reclassifications"`
	CategoryCounts    map[RuleCategory]int     `json:"category_counts"`
	CategoryRows      map[RuleCategory]int     `json:"category_rows"`
	Collisions        []MigrationCollision     `json:"collisions"`
	SkippedNamespaces []string                 `json:"skipped_namespaces"`
	TotalNamespaces   int                      `json:"total_namespaces"`
	TotalRowsAffected int                      `json:"total_rows_affected"`
}

// MigrationReceipt reports execution results after applying the plan.
type MigrationReceipt struct {
	RenamedNamespaces    int      `json:"renamed_namespaces"`
	UpdatedStateRows     int      `json:"updated_state_rows"`
	UpdatedRevisionRows  int      `json:"updated_revision_rows"`
	UpdatedRecordRows    int      `json:"updated_record_rows"`
	UpdatedHeadRows      int      `json:"updated_head_rows"`
	UpdatedWorkspaceRows int      `json:"updated_workspace_rows"`
	PromotedRecords      int      `json:"promoted_records"`
	Errors               []string `json:"errors,omitempty"`
}

// BuildMigrationPlan inspects the database without mutating anything and returns
// the complete migration plan.
func BuildMigrationPlan(ctx context.Context, db *sql.DB) (*MigrationPlan, error) {
	seen := make(map[string]domains.Domain)

	// 1. From namespace_policies
	npRows, err := db.QueryContext(ctx, `SELECT namespace FROM namespace_policies`)
	if err == nil {
		defer func() { _ = npRows.Close() }()
		for npRows.Next() {
			var ns string
			if scanErr := npRows.Scan(&ns); scanErr == nil && ns != "" {
				seen[ns] = ""
			}
		}
	}

	// 2. From memory_state
	msRows, err := db.QueryContext(ctx, `SELECT DISTINCT namespace, domain FROM memory_state WHERE namespace IS NOT NULL AND namespace <> ''`)
	if err != nil {
		return nil, fmt.Errorf("query memory_state namespaces: %w", err)
	}
	defer func() { _ = msRows.Close() }()
	for msRows.Next() {
		var ns, dom string
		if scanErr := msRows.Scan(&ns, &dom); scanErr == nil && ns != "" {
			seen[ns] = domains.Domain(dom)
		}
	}

	// 3. From records (context)
	rcRows, err := db.QueryContext(ctx, `SELECT DISTINCT namespace FROM records WHERE namespace IS NOT NULL AND namespace <> ''`)
	if err == nil {
		defer func() { _ = rcRows.Close() }()
		for rcRows.Next() {
			var ns string
			if scanErr := rcRows.Scan(&ns); scanErr == nil && ns != "" {
				if _, ok := seen[ns]; !ok || seen[ns] == "" {
					seen[ns] = domains.Domain("context")
				}
			}
		}
	}

	// 4. From workspace_items
	wsRows, err := db.QueryContext(ctx, `SELECT DISTINCT namespace FROM workspace_items WHERE namespace IS NOT NULL AND namespace <> ''`)
	if err == nil {
		defer func() { _ = wsRows.Close() }()
		for wsRows.Next() {
			var ns string
			if scanErr := wsRows.Scan(&ns); scanErr == nil && ns != "" {
				if _, ok := seen[ns]; !ok || seen[ns] == "" {
					seen[ns] = domains.Domain("workspace")
				}
			}
		}
	}

	// Sort namespaces deterministically
	allNS := make([]string, 0, len(seen))
	for ns := range seen {
		allNS = append(allNS, ns)
	}
	sort.Strings(allNS)

	plan := &MigrationPlan{
		CategoryCounts: make(map[RuleCategory]int),
		CategoryRows:   make(map[RuleCategory]int),
	}

	// Pre-load counts by namespace for performance
	stateCounts := countByNamespace(ctx, db, `SELECT namespace, COUNT(*) FROM memory_state GROUP BY namespace`)
	revCounts := countByNamespace(ctx, db, `SELECT namespace, COUNT(*) FROM memory_revisions GROUP BY namespace`)
	recCounts := countByNamespace(ctx, db, `SELECT namespace, COUNT(*) FROM records GROUP BY namespace`)
	headCounts := countByNamespace(ctx, db, `SELECT namespace, COUNT(*) FROM heads GROUP BY namespace`)
	wsCounts := countByNamespace(ctx, db, `SELECT namespace, COUNT(*) FROM workspace_items GROUP BY namespace`)

	for _, ns := range allNS {
		dom := seen[ns]
		if dom == "" {
			// Infer domain from namespace structure if not in state
			if strings.Contains(ns, "/knowledge/") || strings.HasSuffix(ns, "/knowledge") {
				dom = domains.Knowledge
			} else if strings.Contains(ns, "/memory/") || strings.HasSuffix(ns, "/memory") {
				dom = domains.Memory
			} else if strings.Contains(ns, "/event/") || strings.HasSuffix(ns, "/event") {
				dom = domains.Event
			} else if strings.Contains(ns, "/workspace/") || strings.HasSuffix(ns, "/workspace") {
				dom = domains.Domain("workspace")
			} else {
				dom = domains.Domain("context")
			}
		}

		dec := ClassifyNamespace(ns, dom)
		sRows := stateCounts[ns]
		rRows := revCounts[ns]
		recRows := recCounts[ns]
		hRows := headCounts[ns]
		wRows := wsCounts[ns]
		totRows := sRows + rRows + recRows + hRows + wRows

		stat := NamespaceStat{
			OldNamespace:  ns,
			NewNamespace:  dec.NewNamespace,
			Domain:        dom,
			Category:      dec.Category,
			Action:        dec.Action,
			StateRows:     sRows,
			RevisionRows:  rRows,
			RecordRows:    recRows,
			HeadRows:      hRows,
			WorkspaceRows: wRows,
			TotalRows:     totRows,
			Reason:        dec.Reason,
			IsAmbiguous:   dec.IsAmbiguous,
			Confidence:    dec.Confidence,
		}

		plan.Namespaces = append(plan.Namespaces, stat)
		plan.CategoryCounts[dec.Category]++
		plan.CategoryRows[dec.Category] += totRows
		plan.TotalNamespaces++

		if dec.Action == ActionSkip || dec.Action == ActionDeferred {
			plan.SkippedNamespaces = append(plan.SkippedNamespaces, ns)
			continue
		}

		if dec.Action == ActionRename {
			plan.TotalRowsAffected += totRows
		}

		if dec.Action == ActionReclassify {
			// Enumerate individual records to reclassify
			records, loadErr := loadRecordsForReclassification(ctx, db, ns, dec)
			if loadErr != nil {
				return nil, fmt.Errorf("load reclassification records for %s: %w", ns, loadErr)
			}
			plan.Reclassifications = append(plan.Reclassifications, records...)
			plan.TotalRowsAffected += len(records)
		}
	}

	// Collision detection
	collisions, err := detectCollisions(ctx, db, plan)
	if err != nil {
		return nil, fmt.Errorf("collision detection: %w", err)
	}
	plan.Collisions = collisions

	return plan, nil
}

func countByNamespace(ctx context.Context, db *sql.DB, query string) map[string]int {
	m := make(map[string]int)
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return m
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var ns string
		var cnt int
		if scanErr := rows.Scan(&ns, &cnt); scanErr == nil {
			m[ns] = cnt
		}
	}
	return m
}

// NormalizeKeyForTarget normalizes a key for the target domain if required.
// Memory and Event domains require valid dot-notation/underscore keys.
// Workspace and Knowledge domains allow arbitrary string keys.
func NormalizeKeyForTarget(targetDomain domains.Domain, key string) string {
	if targetDomain == domains.Domain("workspace") || targetDomain == domains.Knowledge {
		return key
	}
	if key == "" {
		return ""
	}
	if err := memory.ValidateKey(key); err == nil {
		return key
	}
	if sugg, ok := memory.SuggestKey(key); ok && memory.ValidateKey(sugg) == nil {
		return sugg
	}
	// Direct normalization: replace '-' with '_'
	normalized := strings.ReplaceAll(strings.ToLower(key), "-", "_")
	if memory.ValidateKey(normalized) == nil {
		return normalized
	}
	return ""
}

func loadRecordsForReclassification(ctx context.Context, db *sql.DB, ns string, dec Decision) ([]ReclassificationRecord, error) {
	query := `
SELECT 
    s.memory_id,
    COALESCE(s.current_revision, ''),
    COALESCE(s.memory_key, ''),
    COALESCE(r.payload_summary, ''),
    COALESCE(r.tags, '[]')
FROM memory_state s
LEFT JOIN memory_revisions r ON r.revision_id = s.current_revision
WHERE s.namespace = ? AND s.current_revision IS NOT NULL AND r.status <> 'deprecated'`

	rows, err := db.QueryContext(ctx, query, ns)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []ReclassificationRecord
	for rows.Next() {
		var memID, revID, key, summary, tagsJSON string
		if err := rows.Scan(&memID, &revID, &key, &summary, &tagsJSON); err != nil {
			return nil, err
		}
		var tags []string
		if tagsJSON != "" {
			_ = json.Unmarshal([]byte(tagsJSON), &tags)
		}
		targetKey := NormalizeKeyForTarget(dec.TargetDomain, key)
		out = append(out, ReclassificationRecord{
			MemoryID:        memID,
			RevisionID:      revID,
			SourceDomain:    dec.SourceDomain,
			TargetDomain:    dec.TargetDomain,
			SourceNamespace: ns,
			TargetNamespace: dec.NewNamespace,
			Key:             key,
			TargetKey:       targetKey,
			Summary:         summary,
			Tags:            tags,
			TargetTags:      dec.TargetTags,
			Category:        dec.Category,
			Reason:          dec.Reason,
		})
	}
	return out, rows.Err()
}

func detectCollisions(ctx context.Context, db *sql.DB, plan *MigrationPlan) ([]MigrationCollision, error) {
	var collisions []MigrationCollision

	// Check renames: target namespace collision
	for _, ns := range plan.Namespaces {
		if ns.Action != ActionRename || ns.OldNamespace == ns.NewNamespace {
			continue
		}
		// Check if any key in OldNamespace already exists in NewNamespace in memory_state
		query := `
SELECT a.memory_key, a.memory_id, b.memory_id
FROM memory_state a
JOIN memory_state b ON a.memory_key = b.memory_key
WHERE a.namespace = ? AND b.namespace = ? AND a.memory_key IS NOT NULL AND a.memory_key <> '' AND a.memory_id <> b.memory_id`
		rows, err := db.QueryContext(ctx, query, ns.OldNamespace, ns.NewNamespace)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var key, idA, idB string
			if scanErr := rows.Scan(&key, &idA, &idB); scanErr == nil {
				collisions = append(collisions, MigrationCollision{
					TargetNamespace: ns.NewNamespace,
					Key:             key,
					ItemIDs:         []string{idA, idB},
					Source:          "rename collision in memory_state",
				})
			}
		}
		_ = rows.Close()
	}

	// Check reclassifications: target already occupied
	seenReclass := make(map[string]string) // composite target -> itemID
	for _, rec := range plan.Reclassifications {
		if rec.TargetKey == "" {
			continue
		}
		composite := fmt.Sprintf("%s/%s/%s", rec.TargetDomain, rec.TargetNamespace, rec.TargetKey)
		if prevID, exists := seenReclass[composite]; exists {
			collisions = append(collisions, MigrationCollision{
				TargetNamespace: rec.TargetNamespace,
				Key:             rec.TargetKey,
				ItemIDs:         []string{prevID, rec.MemoryID},
				Source:          "intra-plan reclassification key collision",
			})
		} else {
			seenReclass[composite] = rec.MemoryID
		}

		if rec.TargetDomain == domains.Domain("workspace") {
			var exists int
			err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM workspace_items WHERE namespace = ? AND key_name = ?)`, rec.TargetNamespace, rec.TargetKey).Scan(&exists)
			if err == nil && exists != 0 {
				collisions = append(collisions, MigrationCollision{
					TargetNamespace: rec.TargetNamespace,
					Key:             rec.TargetKey,
					ItemIDs:         []string{rec.MemoryID},
					Source:          "workspace target key already occupied",
				})
			}
		} else if rec.TargetDomain == domains.Event || rec.TargetDomain == domains.Memory {
			var exists int
			err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM memory_state WHERE namespace = ? AND memory_key = ?)`, rec.TargetNamespace, rec.TargetKey).Scan(&exists)
			if err == nil && exists != 0 {
				collisions = append(collisions, MigrationCollision{
					TargetNamespace: rec.TargetNamespace,
					Key:             rec.TargetKey,
					ItemIDs:         []string{rec.MemoryID},
					Source:          fmt.Sprintf("%s target key already occupied", rec.TargetDomain),
				})
			}
		}
	}

	return collisions, nil
}

// ApplyMigration applies the migration plan atomically.
func ApplyMigration(ctx context.Context, db *sql.DB, plan *MigrationPlan) (*MigrationReceipt, error) {
	if len(plan.Collisions) > 0 {
		return nil, fmt.Errorf("refusing to apply migration: %d collision(s) detected", len(plan.Collisions))
	}

	receipt := &MigrationReceipt{}

	// Phase A: Same-domain namespace renames in a single transaction
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin rename tx: %w", err)
	}
	defer func() {
		if tx != nil {
			_ = tx.Rollback()
		}
	}()

	stateStmt, err := tx.PrepareContext(ctx, `UPDATE memory_state SET namespace = ? WHERE namespace = ?`)
	if err != nil {
		return nil, fmt.Errorf("prepare state update: %w", err)
	}
	defer func() { _ = stateStmt.Close() }()

	revStmt, err := tx.PrepareContext(ctx, `UPDATE memory_revisions SET namespace = ? WHERE namespace = ?`)
	if err != nil {
		return nil, fmt.Errorf("prepare rev update: %w", err)
	}
	defer func() { _ = revStmt.Close() }()

	recStmt, err := tx.PrepareContext(ctx, `UPDATE records SET namespace = ? WHERE namespace = ?`)
	if err != nil {
		return nil, fmt.Errorf("prepare records update: %w", err)
	}
	defer func() { _ = recStmt.Close() }()

	headStmt, err := tx.PrepareContext(ctx, `UPDATE heads SET namespace = ? WHERE namespace = ?`)
	if err != nil {
		return nil, fmt.Errorf("prepare heads update: %w", err)
	}
	defer func() { _ = headStmt.Close() }()

	wsStmt, err := tx.PrepareContext(ctx, `UPDATE workspace_items SET namespace = ? WHERE namespace = ?`)
	if err != nil {
		return nil, fmt.Errorf("prepare ws update: %w", err)
	}
	defer func() { _ = wsStmt.Close() }()

	npInsertStmt, err := tx.PrepareContext(ctx, `INSERT OR IGNORE INTO namespace_policies (namespace, owner_type, owner_id, policy_json, updated_at) VALUES (?, 'system', 'system', '{}', ?)`)
	if err != nil {
		return nil, fmt.Errorf("prepare np insert: %w", err)
	}
	defer func() { _ = npInsertStmt.Close() }()

	now := time.Now().UTC().Format(time.RFC3339)

	for _, ns := range plan.Namespaces {
		if ns.Action != ActionRename || ns.OldNamespace == ns.NewNamespace {
			continue
		}

		res, exErr := stateStmt.ExecContext(ctx, ns.NewNamespace, ns.OldNamespace)
		if exErr != nil {
			return nil, fmt.Errorf("update memory_state for %s -> %s: %w", ns.OldNamespace, ns.NewNamespace, exErr)
		}
		if n, _ := res.RowsAffected(); n > 0 {
			receipt.UpdatedStateRows += int(n)
		}

		res, exErr = revStmt.ExecContext(ctx, ns.NewNamespace, ns.OldNamespace)
		if exErr != nil {
			return nil, fmt.Errorf("update memory_revisions for %s -> %s: %w", ns.OldNamespace, ns.NewNamespace, exErr)
		}
		if n, _ := res.RowsAffected(); n > 0 {
			receipt.UpdatedRevisionRows += int(n)
		}

		res, exErr = recStmt.ExecContext(ctx, ns.NewNamespace, ns.OldNamespace)
		if exErr == nil {
			if n, _ := res.RowsAffected(); n > 0 {
				receipt.UpdatedRecordRows += int(n)
			}
		}

		res, exErr = headStmt.ExecContext(ctx, ns.NewNamespace, ns.OldNamespace)
		if exErr == nil {
			if n, _ := res.RowsAffected(); n > 0 {
				receipt.UpdatedHeadRows += int(n)
			}
		}

		res, exErr = wsStmt.ExecContext(ctx, ns.NewNamespace, ns.OldNamespace)
		if exErr == nil {
			if n, _ := res.RowsAffected(); n > 0 {
				receipt.UpdatedWorkspaceRows += int(n)
			}
		}

		// Ensure new namespace in registry
		_, _ = npInsertStmt.ExecContext(ctx, ns.NewNamespace, now)
		receipt.RenamedNamespaces++
	}

	if commitErr := tx.Commit(); commitErr != nil {
		return nil, fmt.Errorf("commit rename tx: %w", commitErr)
	}
	tx = nil // committed successfully

	// Phase B: Cross-domain reclassifications using promotion.Store
	memStore := memory.NewStore(db, nil, "", 0, memory.NoopQueue{})
	wsStore := workspace.NewStore(db)
	promStore := promotion.NewStoreWithDB(db, memStore, wsStore)

	for _, rec := range plan.Reclassifications {
		mergedTags := append([]string(nil), rec.Tags...)
		for _, t := range rec.TargetTags {
			found := false
			for _, existing := range mergedTags {
				if existing == t {
					found = true
					break
				}
			}
			if !found {
				mergedTags = append(mergedTags, t)
			}
		}

		reqIn := promotion.RequestInput{
			SourceDomain:       rec.SourceDomain,
			SourceItemID:       rec.MemoryID,
			SourceVersionToken: rec.RevisionID,
			Actor:              "user",
			Reason:             fmt.Sprintf("n6 corpus migration: %s", rec.Reason),
			Target: promotion.Target{
				Domain:      rec.TargetDomain,
				Namespace:   rec.TargetNamespace,
				Key:         rec.TargetKey,
				Tags:        mergedTags,
				Status:      memory.StatusCanonical,
				Trigger:     memory.TriggerPromotion,
				DerivedFrom: memory.DerivedFromProject,
				Author: memory.Author{
					AgentID: "corpus-migration",
				},
				SessionID: "session-n6-phase1",
			},
		}

		reqReceipt, reqErr := promStore.Request(ctx, reqIn)
		if reqErr != nil {
			errMsg := fmt.Sprintf("request promotion %s (%s -> %s): %v", rec.MemoryID, rec.Key, rec.TargetKey, reqErr)
			receipt.Errors = append(receipt.Errors, errMsg)
			return receipt, errors.New(errMsg)
		}

		_, appErr := promStore.Approve(ctx, promotion.ApproveInput{
			RequestID: reqReceipt.RequestID,
			Actor:     "user",
			Notes:     "n6 corpus migration auto-approval",
		})
		if appErr != nil {
			errMsg := fmt.Sprintf("approve promotion %s (%s): %v", rec.MemoryID, rec.TargetKey, appErr)
			receipt.Errors = append(receipt.Errors, errMsg)
			return receipt, errors.New(errMsg)
		}

		_, applyErr := promStore.Apply(ctx, promotion.ApplyInput{
			RequestID: reqReceipt.RequestID,
			Actor:     "user",
		})
		if applyErr != nil {
			errMsg := fmt.Sprintf("apply promotion %s (%s): %v", rec.MemoryID, rec.TargetKey, applyErr)
			receipt.Errors = append(receipt.Errors, errMsg)
			return receipt, errors.New(errMsg)
		}

		receipt.PromotedRecords++
	}

	// Phase C: Re-resolve any pending wikilinks
	resolveTx, rErr := db.BeginTx(ctx, nil)
	if rErr == nil {
		resolver := memorylinks.TxResolver{Tx: resolveTx}
		// Gather all unique keys touched
		keySet := make(map[string]struct{})
		for _, rec := range plan.Reclassifications {
			if rec.TargetKey != "" {
				keySet[rec.TargetKey] = struct{}{}
			}
			if rec.Key != "" {
				keySet[rec.Key] = struct{}{}
			}
		}
		for k := range keySet {
			_ = memorylinks.ResolvePending(ctx, resolveTx, resolver, k)
		}
		_ = resolveTx.Commit()
	}

	return receipt, nil
}
