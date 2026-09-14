package workspace

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/hollis-labs/tesseract/internal/contextpolicy"
	"github.com/hollis-labs/tesseract/internal/memorytime"
)

const (
	InitialActivation  = 1.0
	ActivationCeiling  = 2.0
	ReinforcementRate  = 0.1
	ActivationFloor    = 0.05
	ActivationHalfLife = 14 * 24 * time.Hour
	MinimumPurgeIdle   = 30 * 24 * time.Hour

	defaultRetentionLimit = 100
	maxRetentionLimit     = 1000
)

// RetentionSettings are the operator-controlled defaults used by reports and
// purge. Namespace policy may make these settings more protective, never less.
type RetentionSettings struct {
	PurgeEnabled bool
	MinimumIdle  time.Duration
}

func (s RetentionSettings) normalized() (RetentionSettings, error) {
	if s.MinimumIdle == 0 {
		s.MinimumIdle = MinimumPurgeIdle
	}
	if s.MinimumIdle < MinimumPurgeIdle {
		return RetentionSettings{}, fmt.Errorf("%w: workspace retention minimum_idle must be at least %s", ErrInvalidInput, MinimumPurgeIdle)
	}
	return s, nil
}

// RetentionReportInput pages over workspace identity in stable item_id order.
// Cursor is the last scanned item_id from a previous page, not a result offset.
type RetentionReportInput struct {
	Settings RetentionSettings
	Cursor   string
	Limit    int
}

type RetentionCandidate struct {
	ItemID              string    `json:"item_id"`
	Domain              string    `json:"domain"`
	Namespace           string    `json:"namespace"`
	LastUsedAt          time.Time `json:"last_used_at"`
	StoredActivation    float64   `json:"stored_activation"`
	EffectiveActivation float64   `json:"effective_activation"`
	EvaluatedAt         time.Time `json:"evaluated_at"`
	MinimumIdle         string    `json:"minimum_idle"`
	Eligible            bool      `json:"eligible"`
	PolicyReason        string    `json:"policy_reason"`
}

type RetentionReport struct {
	Candidates  []RetentionCandidate `json:"candidates"`
	EvaluatedAt time.Time            `json:"evaluated_at"`
	Scanned     int                  `json:"scanned"`
	Truncated   bool                 `json:"truncated"`
	NextCursor  string               `json:"next_cursor,omitempty"`
}

type RetentionApplyInput struct {
	Settings RetentionSettings
	ItemIDs  []string
}

type RetentionApplyResult struct {
	ItemID    string     `json:"item_id"`
	Status    string     `json:"status"`
	Reason    string     `json:"reason,omitempty"`
	DeletedAt *time.Time `json:"deleted_at,omitempty"`
}

type RetentionApplyReport struct {
	EvaluatedAt time.Time              `json:"evaluated_at"`
	Results     []RetentionApplyResult `json:"results"`
	Purged      int                    `json:"purged"`
	Skipped     int                    `json:"skipped"`
}

type retentionState struct {
	itemID        string
	namespace     string
	activation    float64
	lastUsedAt    time.Time
	lastDecayedAt time.Time
}

func effectiveActivation(stored float64, lastDecayedAt, now time.Time) float64 {
	if stored < ActivationFloor {
		stored = ActivationFloor
	}
	if !now.After(lastDecayedAt) {
		return stored
	}
	elapsed := now.Sub(lastDecayedAt)
	effective := stored * math.Pow(2, -float64(elapsed)/float64(ActivationHalfLife))
	return math.Max(ActivationFloor, effective)
}

func reinforcedActivation(stored float64, lastDecayedAt, now time.Time) float64 {
	effective := effectiveActivation(stored, lastDecayedAt, now)
	return effective + ReinforcementRate*(ActivationCeiling-effective)
}

func readRetentionState(row scanner) (retentionState, error) {
	var state retentionState
	var usedRaw, decayedRaw string
	if err := row.Scan(&state.itemID, &state.namespace, &state.activation, &usedRaw, &decayedRaw); err != nil {
		return retentionState{}, err
	}
	var err error
	if state.lastUsedAt, err = memorytime.Parse(usedRaw); err != nil {
		return retentionState{}, fmt.Errorf("parse workspace last_used_at: %w", err)
	}
	if state.lastDecayedAt, err = memorytime.Parse(decayedRaw); err != nil {
		return retentionState{}, fmt.Errorf("parse workspace last_decayed_at: %w", err)
	}
	return state, nil
}

func resolveRetentionPolicy(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, namespace string, settings RetentionSettings) (RetentionSettings, string, error) {
	settings, err := settings.normalized()
	if err != nil {
		return RetentionSettings{}, "", err
	}
	var raw sql.NullString
	err = q.QueryRowContext(ctx, `SELECT policy_json FROM namespace_policies WHERE namespace = ?`, namespace).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return settings, "operator_default", nil
	}
	if err != nil {
		return RetentionSettings{}, "", fmt.Errorf("read namespace retention policy: %w", err)
	}
	if !raw.Valid || strings.TrimSpace(raw.String) == "" {
		return settings, "operator_default", nil
	}
	var policy map[string]any
	if decodeErr := json.Unmarshal([]byte(raw.String), &policy); decodeErr != nil {
		return RetentionSettings{}, "", fmt.Errorf("decode namespace retention policy: %w", decodeErr)
	}
	namespacePolicy, present, err := contextpolicy.ParseWorkspaceRetentionPolicy(policy)
	if err != nil {
		return RetentionSettings{}, "", fmt.Errorf("namespace %s: %w", namespace, err)
	}
	if !present {
		return settings, "operator_default", nil
	}
	reason := "namespace_policy"
	if namespacePolicy.MinimumIdle > settings.MinimumIdle {
		settings.MinimumIdle = namespacePolicy.MinimumIdle
	}
	if namespacePolicy.PurgeEnabled != nil && !*namespacePolicy.PurgeEnabled {
		settings.PurgeEnabled = false
		reason = "namespace_purge_disabled"
	}
	return settings, reason, nil
}

func evaluateRetention(state retentionState, now time.Time, settings RetentionSettings, policySource string) RetentionCandidate {
	effective := effectiveActivation(state.activation, state.lastDecayedAt, now)
	idle := time.Duration(0)
	if now.After(state.lastUsedAt) {
		idle = now.Sub(state.lastUsedAt)
	}
	candidate := RetentionCandidate{
		ItemID: state.itemID, Domain: Domain, Namespace: state.namespace,
		LastUsedAt: state.lastUsedAt, StoredActivation: state.activation,
		EffectiveActivation: effective, EvaluatedAt: now,
		MinimumIdle: settings.MinimumIdle.String(),
	}
	switch {
	case effective > ActivationFloor:
		candidate.PolicyReason = "activation_above_floor"
	case idle < settings.MinimumIdle:
		candidate.PolicyReason = "minimum_idle_not_reached"
	case !settings.PurgeEnabled:
		if policySource == "namespace_purge_disabled" {
			candidate.PolicyReason = policySource
		} else {
			candidate.PolicyReason = "operator_purge_disabled"
		}
	default:
		candidate.Eligible = true
		candidate.PolicyReason = "activation_floor_and_minimum_idle_reached"
	}
	return candidate
}

// ReportRetention scans a stable page without hydrating content or recording use.
// Every scanned item is returned with its eligibility reason so disabled policy
// remains visible during review rather than looking like an empty corpus.
func (s *Store) ReportRetention(ctx context.Context, in RetentionReportInput) (RetentionReport, error) {
	return s.reportRetentionAt(ctx, in, s.now().UTC())
}

func (s *Store) reportRetentionAt(ctx context.Context, in RetentionReportInput, now time.Time) (RetentionReport, error) {
	settings, err := in.Settings.normalized()
	if err != nil {
		return RetentionReport{}, err
	}
	limit := in.Limit
	if limit == 0 {
		limit = defaultRetentionLimit
	}
	if limit < 1 || limit > maxRetentionLimit {
		return RetentionReport{}, fmt.Errorf("%w: retention limit must be between 1 and %d", ErrInvalidInput, maxRetentionLimit)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT item_id, namespace, activation, last_used_at, last_decayed_at
		FROM workspace_items WHERE item_id > ? ORDER BY item_id LIMIT ?`, in.Cursor, limit+1)
	if err != nil {
		return RetentionReport{}, fmt.Errorf("query workspace retention page: %w", err)
	}
	defer func() { _ = rows.Close() }()
	states := make([]retentionState, 0, limit+1)
	for rows.Next() {
		state, scanErr := readRetentionState(rows)
		if scanErr != nil {
			return RetentionReport{}, scanErr
		}
		states = append(states, state)
	}
	if err := rows.Err(); err != nil {
		return RetentionReport{}, err
	}
	if err := rows.Close(); err != nil {
		return RetentionReport{}, fmt.Errorf("close workspace retention page: %w", err)
	}
	report := RetentionReport{Candidates: []RetentionCandidate{}, EvaluatedAt: now}
	if len(states) > limit {
		report.Truncated = true
		states = states[:limit]
	}
	report.Scanned = len(states)
	for _, state := range states {
		resolved, source, policyErr := resolveRetentionPolicy(ctx, s.db, state.namespace, settings)
		if policyErr != nil {
			return RetentionReport{}, policyErr
		}
		report.Candidates = append(report.Candidates, evaluateRetention(state, now, resolved, source))
	}
	if report.Truncated && len(states) > 0 {
		report.NextCursor = states[len(states)-1].itemID
	}
	return report, nil
}

// ApplyRetention re-evaluates every named item and its current namespace policy
// in the same transaction that creates the tombstone and removes content.
func (s *Store) ApplyRetention(ctx context.Context, in RetentionApplyInput) (RetentionApplyReport, error) {
	return s.applyRetentionAt(ctx, in, s.now().UTC())
}

func (s *Store) applyRetentionAt(ctx context.Context, in RetentionApplyInput, now time.Time) (RetentionApplyReport, error) {
	settings, err := in.Settings.normalized()
	if err != nil {
		return RetentionApplyReport{}, err
	}
	if len(in.ItemIDs) == 0 || len(in.ItemIDs) > maxRetentionLimit {
		return RetentionApplyReport{}, fmt.Errorf("%w: item_ids must contain between 1 and %d values", ErrInvalidInput, maxRetentionLimit)
	}
	if !settings.PurgeEnabled {
		return RetentionApplyReport{}, fmt.Errorf("%w: workspace purge is disabled by operator configuration", ErrInvalidInput)
	}
	report := RetentionApplyReport{EvaluatedAt: now, Results: make([]RetentionApplyResult, 0, len(in.ItemIDs))}
	seen := map[string]bool{}
	for _, itemID := range in.ItemIDs {
		itemID = strings.TrimSpace(itemID)
		if itemID == "" || seen[itemID] {
			continue
		}
		seen[itemID] = true
		result, applyErr := retryMutation(ctx, "retention purge", func() (RetentionApplyResult, error) {
			return s.applyRetentionOne(ctx, itemID, now, settings)
		})
		if applyErr != nil {
			return report, applyErr
		}
		report.Results = append(report.Results, result)
		if result.Status == "purged" {
			report.Purged++
		} else {
			report.Skipped++
		}
	}
	return report, nil
}

// RunRetentionPass scans the complete workspace in bounded pages and applies
// eligible purges using one captured evaluation instant for the whole pass.
func (s *Store) RunRetentionPass(ctx context.Context, settings RetentionSettings, batchSize int) (RetentionApplyReport, error) {
	if batchSize == 0 {
		batchSize = defaultRetentionLimit
	}
	now := s.now().UTC()
	total := RetentionApplyReport{EvaluatedAt: now, Results: []RetentionApplyResult{}}
	cursor := ""
	for {
		report, err := s.reportRetentionAt(ctx, RetentionReportInput{Settings: settings, Cursor: cursor, Limit: batchSize}, now)
		if err != nil {
			return total, err
		}
		var itemIDs []string
		for _, candidate := range report.Candidates {
			if candidate.Eligible {
				itemIDs = append(itemIDs, candidate.ItemID)
			}
		}
		if len(itemIDs) > 0 {
			applied, err := s.applyRetentionAt(ctx, RetentionApplyInput{Settings: settings, ItemIDs: itemIDs}, now)
			if err != nil {
				return total, err
			}
			total.Results = append(total.Results, applied.Results...)
			total.Purged += applied.Purged
			total.Skipped += applied.Skipped
		}
		if !report.Truncated {
			return total, nil
		}
		cursor = report.NextCursor
	}
}

func (s *Store) applyRetentionOne(ctx context.Context, itemID string, now time.Time, settings RetentionSettings) (RetentionApplyResult, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RetentionApplyResult{}, fmt.Errorf("begin workspace retention purge: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	state, err := readRetentionState(tx.QueryRowContext(ctx, `SELECT item_id, namespace, activation, last_used_at, last_decayed_at FROM workspace_items WHERE item_id = ?`, itemID))
	if errors.Is(err, sql.ErrNoRows) {
		return RetentionApplyResult{ItemID: itemID, Status: "skipped", Reason: "not_live"}, nil
	}
	if err != nil {
		return RetentionApplyResult{}, err
	}
	resolved, source, err := resolveRetentionPolicy(ctx, tx, state.namespace, settings)
	if err != nil {
		return RetentionApplyResult{}, err
	}
	candidate := evaluateRetention(state, now, resolved, source)
	if !candidate.Eligible {
		return RetentionApplyResult{ItemID: itemID, Status: "skipped", Reason: candidate.PolicyReason}, nil
	}
	if _, err := deleteLiveInTx(ctx, tx, itemID, "", now); err != nil {
		return RetentionApplyResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return RetentionApplyResult{}, fmt.Errorf("commit workspace retention purge: %w", err)
	}
	return RetentionApplyResult{ItemID: itemID, Status: "purged", DeletedAt: &now}, nil
}
