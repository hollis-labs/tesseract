package workspace_test

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/tesseract/internal/contextstore"
	"github.com/hollis-labs/tesseract/internal/memory"
	"github.com/hollis-labs/tesseract/internal/workspace"
)

func setRetentionState(t *testing.T, cs *contextstore.Store, itemID string, activation float64, used, decayed time.Time) {
	t.Helper()
	_, err := cs.DB().Exec(`UPDATE workspace_items SET activation = ?, last_used_at = ?, last_decayed_at = ? WHERE item_id = ?`,
		activation, used.UTC().Format(time.RFC3339Nano), decayed.UTC().Format(time.RFC3339Nano), itemID)
	if err != nil {
		t.Fatal(err)
	}
}

func closeEnough(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestWorkspaceUseAppliesOwedDecayBeforeReinforcement(t *testing.T) {
	_, store, now := newWorkspaceStore(t)
	ctx := context.Background()
	item, err := store.Create(ctx, createInput("decay-use"))
	if err != nil {
		t.Fatal(err)
	}
	*now = now.Add(workspace.ActivationHalfLife)
	used, err := store.GetCurrentAndUse(ctx, item.ItemID)
	if err != nil {
		t.Fatal(err)
	}
	if !closeEnough(used.Activation, 0.65) {
		t.Fatalf("activation after one half-life use = %.12f, want .65", used.Activation)
	}
	if used.AccessCount != 1 || !used.LastUsedAt.Equal(*now) || !used.LastDecayedAt.Equal(*now) {
		t.Fatalf("use bookkeeping = %#v", used)
	}
	if used.VersionToken != item.VersionToken || used.UpdatedAt != item.UpdatedAt || used.Author != item.Author || used.SessionID != item.SessionID {
		t.Fatalf("use changed authored metadata: before=%#v after=%#v", item, used)
	}

	again, err := store.GetCurrentAndUse(ctx, item.ItemID)
	if err != nil {
		t.Fatal(err)
	}
	if !closeEnough(again.Activation, 0.785) {
		t.Fatalf("same-instant second reinforcement = %.12f, want .785", again.Activation)
	}

	*now = now.Add(365 * 24 * time.Hour)
	floored, err := store.GetCurrentAndUse(ctx, item.ItemID)
	if err != nil {
		t.Fatal(err)
	}
	if !closeEnough(floored.Activation, 0.245) {
		t.Fatalf("long-idle reinforcement = %.12f, want .245", floored.Activation)
	}

	previousUsed, previousDecay := floored.LastUsedAt, floored.LastDecayedAt
	*now = now.Add(-48 * time.Hour)
	backward, err := store.GetCurrentAndUse(ctx, item.ItemID)
	if err != nil {
		t.Fatal(err)
	}
	if backward.LastUsedAt.Before(previousUsed) || backward.LastDecayedAt.Before(previousDecay) {
		t.Fatalf("backward clock moved use baselines backward: %#v", backward)
	}
}

func TestActivationRecallRanksEffectiveRatherThanStoredValues(t *testing.T) {
	cs, store, now := newWorkspaceStore(t)
	ctx := context.Background()
	stale, err := store.Create(ctx, createInput("stored-high-decayed"))
	if err != nil {
		t.Fatal(err)
	}
	current, err := store.Create(ctx, createInput("stored-lower-current"))
	if err != nil {
		t.Fatal(err)
	}
	setRetentionState(t, cs, stale.ItemID, 2.0, now.Add(-100*24*time.Hour), now.Add(-100*24*time.Hour))
	setRetentionState(t, cs, current.ItemID, 0.5, *now, *now)
	results, err := store.Recall(ctx, workspace.RecallInput{Namespaces: []string{testNamespace}, Ranking: memory.RankingActivation, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Item.ItemID != current.ItemID || results[0].Score == nil || !closeEnough(*results[0].Score, 0.5) {
		t.Fatalf("activation recall = %#v", results)
	}
}

func TestRetentionReportUsesEffectiveFloorPolicyAndStablePages(t *testing.T) {
	cs, store, now := newWorkspaceStore(t)
	ctx := context.Background()
	var ids []string
	for _, key := range []string{"retention-a", "retention-b", "retention-c"} {
		item, err := store.Create(ctx, createInput(key))
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, item.ItemID)
		setRetentionState(t, cs, item.ItemID, 0.0505, now.Add(-100*24*time.Hour), now.Add(-100*24*time.Hour))
	}
	settings := workspace.RetentionSettings{PurgeEnabled: true, MinimumIdle: workspace.MinimumPurgeIdle}
	seen := map[string]bool{}
	cursor := ""
	for {
		report, err := store.ReportRetention(ctx, workspace.RetentionReportInput{Settings: settings, Cursor: cursor, Limit: 1})
		if err != nil {
			t.Fatal(err)
		}
		if report.Scanned != 1 || len(report.Candidates) != 1 {
			t.Fatalf("page = %#v", report)
		}
		candidate := report.Candidates[0]
		seen[candidate.ItemID] = true
		if !candidate.Eligible || candidate.EffectiveActivation != workspace.ActivationFloor {
			t.Fatalf("near-floor candidate = %#v", candidate)
		}
		if !report.Truncated {
			break
		}
		if report.NextCursor == "" {
			t.Fatal("truncated page omitted next_cursor")
		}
		cursor = report.NextCursor
	}
	if len(seen) != len(ids) {
		t.Fatalf("stable paging saw %d/%d items", len(seen), len(ids))
	}

	if policyErr := cs.UpsertNamespacePolicy(ctx, contextstore.NamespacePolicyEntry{
		Namespace: testNamespace, OwnerType: "user", OwnerID: "test",
		Policy: map[string]any{"workspace_retention": map[string]any{"minimum_idle": "2880h"}},
	}); policyErr != nil {
		t.Fatal(policyErr)
	}
	report, err := store.ReportRetention(ctx, workspace.RetentionReportInput{Settings: settings, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if report.Candidates[0].Eligible || report.Candidates[0].PolicyReason != "minimum_idle_not_reached" {
		t.Fatalf("longer namespace minimum ignored: %#v", report.Candidates[0])
	}

	if policyErr := cs.UpsertNamespacePolicy(ctx, contextstore.NamespacePolicyEntry{
		Namespace: testNamespace, OwnerType: "user", OwnerID: "test",
		Policy: map[string]any{"workspace_retention": map[string]any{"purge_enabled": false}},
	}); policyErr != nil {
		t.Fatal(policyErr)
	}
	report, err = store.ReportRetention(ctx, workspace.RetentionReportInput{Settings: settings, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if report.Candidates[0].Eligible || report.Candidates[0].PolicyReason != "namespace_purge_disabled" {
		t.Fatalf("namespace purge disable ignored: %#v", report.Candidates[0])
	}

	if policyErr := cs.UpsertNamespacePolicy(ctx, contextstore.NamespacePolicyEntry{
		Namespace: testNamespace, OwnerType: "user", OwnerID: "test",
		Policy: map[string]any{"workspace_retention": map[string]any{"surprise": true}},
	}); policyErr != nil {
		t.Fatal(policyErr)
	}
	if _, reportErr := store.ReportRetention(ctx, workspace.RetentionReportInput{Settings: settings, Limit: 10}); reportErr == nil {
		t.Fatal("unknown namespace retention setting was silently accepted")
	}
}

func TestRetentionApplyRechecksUseAndPreservesIdentityReceipts(t *testing.T) {
	cs, store, now := newWorkspaceStore(t)
	ctx := context.Background()
	receipt, err := store.CreateWithReceipt(ctx, workspace.CreateRequest{CreateInput: createInput("purge-key"), IdempotencyKey: "purge-create"})
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.GetCurrent(ctx, receipt.ItemID)
	if err != nil {
		t.Fatal(err)
	}
	setRetentionState(t, cs, item.ItemID, workspace.ActivationFloor, now.Add(-100*24*time.Hour), now.Add(-100*24*time.Hour))
	settings := workspace.RetentionSettings{PurgeEnabled: true, MinimumIdle: workspace.MinimumPurgeIdle}
	report, err := store.ReportRetention(ctx, workspace.RetentionReportInput{Settings: settings, Limit: 10})
	if err != nil || !report.Candidates[0].Eligible {
		t.Fatalf("report=%#v err=%v", report, err)
	}
	originalToken := item.VersionToken
	if useErr := store.MarkUsed(ctx, item.ItemID); useErr != nil {
		t.Fatal(useErr)
	}
	afterUse, err := store.GetCurrent(ctx, item.ItemID)
	if err != nil {
		t.Fatal(err)
	}
	if afterUse.VersionToken != originalToken {
		t.Fatal("touch changed authored token")
	}
	applied, err := store.ApplyRetention(ctx, workspace.RetentionApplyInput{Settings: settings, ItemIDs: []string{item.ItemID}})
	if err != nil {
		t.Fatal(err)
	}
	if applied.Purged != 0 || applied.Skipped != 1 {
		t.Fatalf("stale apply = %#v", applied)
	}

	setRetentionState(t, cs, item.ItemID, workspace.ActivationFloor, now.Add(-100*24*time.Hour), now.Add(-100*24*time.Hour))
	if policyErr := cs.UpsertNamespacePolicy(ctx, contextstore.NamespacePolicyEntry{
		Namespace: testNamespace, OwnerType: "user", OwnerID: "test",
		Policy: map[string]any{"workspace_retention": map[string]any{"purge_enabled": false}},
	}); policyErr != nil {
		t.Fatal(policyErr)
	}
	applied, err = store.ApplyRetention(ctx, workspace.RetentionApplyInput{Settings: settings, ItemIDs: []string{item.ItemID}})
	if err != nil || applied.Purged != 0 || applied.Results[0].Reason != "namespace_purge_disabled" {
		t.Fatalf("policy change after report was not honored: %#v err=%v", applied, err)
	}
	if policyErr := cs.UpsertNamespacePolicy(ctx, contextstore.NamespacePolicyEntry{
		Namespace: testNamespace, OwnerType: "user", OwnerID: "test", Policy: nil,
	}); policyErr != nil {
		t.Fatal(policyErr)
	}
	applied, err = store.ApplyRetention(ctx, workspace.RetentionApplyInput{Settings: settings, ItemIDs: []string{item.ItemID}})
	if err != nil || applied.Purged != 1 {
		t.Fatalf("apply=%#v err=%v", applied, err)
	}
	if _, readErr := store.GetCurrent(ctx, item.ItemID); !errors.Is(readErr, workspace.ErrDeleted) {
		t.Fatalf("read after purge: %v", readErr)
	}
	meta, err := store.LookupMetadata(ctx, item.ItemID)
	if err != nil || !meta.Deleted || meta.Namespace != testNamespace {
		t.Fatalf("tombstone=%#v err=%v", meta, err)
	}
	var domain, namespace string
	var deletedAt string
	if scanErr := cs.DB().QueryRow(`SELECT domain, namespace, deleted_at FROM workspace_tombstones WHERE item_id = ?`, item.ItemID).Scan(&domain, &namespace, &deletedAt); scanErr != nil {
		t.Fatal(scanErr)
	}
	if domain != workspace.Domain || namespace != testNamespace || deletedAt == "" {
		t.Fatalf("minimal tombstone values: %q %q %q", domain, namespace, deletedAt)
	}
	replayed, err := store.CreateWithReceipt(ctx, workspace.CreateRequest{CreateInput: createInput("purge-key"), IdempotencyKey: "purge-create"})
	if err != nil || replayed.Status != "replayed" || replayed.Availability != "deleted" || replayed.ItemID != item.ItemID {
		t.Fatalf("create replay after purge = %#v err=%v", replayed, err)
	}
	recreated, err := store.Create(ctx, createInput("purge-key"))
	if err != nil {
		t.Fatal(err)
	}
	if recreated.ItemID == item.ItemID {
		t.Fatal("reused key revived purged identity")
	}
}

func TestCanceledRetentionApplyLeavesLiveContentWithoutTombstone(t *testing.T) {
	cs, store, now := newWorkspaceStore(t)
	item, err := store.Create(context.Background(), createInput("cancel-purge"))
	if err != nil {
		t.Fatal(err)
	}
	setRetentionState(t, cs, item.ItemID, workspace.ActivationFloor, now.Add(-100*24*time.Hour), now.Add(-100*24*time.Hour))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = store.ApplyRetention(ctx, workspace.RetentionApplyInput{
		Settings: workspace.RetentionSettings{PurgeEnabled: true, MinimumIdle: workspace.MinimumPurgeIdle},
		ItemIDs:  []string{item.ItemID},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled apply error = %v", err)
	}
	if _, err := store.GetCurrent(context.Background(), item.ItemID); err != nil {
		t.Fatalf("canceled apply removed live content: %v", err)
	}
	var tombstones int
	if err := cs.DB().QueryRow(`SELECT COUNT(*) FROM workspace_tombstones WHERE item_id = ?`, item.ItemID).Scan(&tombstones); err != nil {
		t.Fatal(err)
	}
	if tombstones != 0 {
		t.Fatalf("canceled apply left %d tombstones", tombstones)
	}
}

func TestRetentionPassReportsCommittedPrefixAcrossPages(t *testing.T) {
	cs, store, now := newWorkspaceStore(t)
	ctx := context.Background()
	var itemIDs []string
	for _, key := range []string{"partial-a", "partial-b", "partial-c", "partial-d"} {
		item, err := store.Create(ctx, createInput(key))
		if err != nil {
			t.Fatal(err)
		}
		itemIDs = append(itemIDs, item.ItemID)
		setRetentionState(t, cs, item.ItemID, workspace.ActivationFloor, now.Add(-365*24*time.Hour), *now)
	}
	sort.Strings(itemIDs)
	failedItemID := itemIDs[len(itemIDs)-1]
	if _, err := cs.DB().Exec(fmt.Sprintf(`CREATE TRIGGER abort_last_retention_delete BEFORE DELETE ON workspace_items WHEN OLD.item_id = '%s' BEGIN SELECT RAISE(ABORT, 'injected purge failure'); END`, failedItemID)); err != nil {
		t.Fatal(err)
	}

	applied, err := store.RunRetentionPass(ctx, workspace.RetentionSettings{PurgeEnabled: true}, 2)
	if err == nil || !strings.Contains(err.Error(), "injected purge failure") {
		t.Fatalf("RunRetentionPass error = %v", err)
	}
	if applied.Purged != 3 || applied.Skipped != 0 || len(applied.Results) != 3 {
		t.Fatalf("partial pass report = %#v", applied)
	}
	for i, result := range applied.Results {
		if result.ItemID != itemIDs[i] || result.Status != "purged" {
			t.Fatalf("result[%d] = %#v, want committed item %s", i, result, itemIDs[i])
		}
	}
	if _, err := store.GetCurrent(ctx, failedItemID); err != nil {
		t.Fatalf("failed item did not remain live: %v", err)
	}
}

func TestConcurrentRetentionAndUseSerialize(t *testing.T) {
	cs, store, now := newWorkspaceStore(t)
	ctx := context.Background()
	item, err := store.Create(ctx, createInput("retention-race"))
	if err != nil {
		t.Fatal(err)
	}
	setRetentionState(t, cs, item.ItemID, workspace.ActivationFloor, now.Add(-100*24*time.Hour), now.Add(-100*24*time.Hour))
	settings := workspace.RetentionSettings{PurgeEnabled: true, MinimumIdle: workspace.MinimumPurgeIdle}
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	var useErr, purgeErr error
	var applied workspace.RetentionApplyReport
	go func() { defer wg.Done(); <-start; useErr = store.MarkUsed(ctx, item.ItemID) }()
	go func() {
		defer wg.Done()
		<-start
		applied, purgeErr = store.ApplyRetention(ctx, workspace.RetentionApplyInput{Settings: settings, ItemIDs: []string{item.ItemID}})
	}()
	close(start)
	wg.Wait()
	if purgeErr != nil {
		t.Fatalf("purge error: %v", purgeErr)
	}
	meta, err := store.LookupMetadata(ctx, item.ItemID)
	if err != nil {
		t.Fatal(err)
	}
	if meta.Deleted {
		if !errors.Is(useErr, workspace.ErrDeleted) || applied.Purged != 1 {
			t.Fatalf("purge-first: use=%v applied=%#v", useErr, applied)
		}
	} else {
		if useErr != nil || applied.Skipped != 1 {
			t.Fatalf("use-first: use=%v applied=%#v", useErr, applied)
		}
	}
}

func TestRetentionJobRequiresExplicitPositiveInterval(t *testing.T) {
	cs, store, now := newWorkspaceStore(t)
	item, err := store.Create(context.Background(), createInput("job-gate"))
	if err != nil {
		t.Fatal(err)
	}
	setRetentionState(t, cs, item.ItemID, workspace.ActivationFloor, now.Add(-100*24*time.Hour), now.Add(-100*24*time.Hour))
	settings := workspace.RetentionSettings{PurgeEnabled: true, MinimumIdle: workspace.MinimumPurgeIdle}

	done := make(chan struct{})
	go func() {
		defer close(done)
		(&workspace.RetentionJob{Store: store, Settings: settings}).Run(context.Background())
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("zero-interval retention job did not stay disabled")
	}
	if _, err := store.GetCurrent(context.Background(), item.ItemID); err != nil {
		t.Fatalf("disabled job changed item: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done = make(chan struct{})
	go func() {
		defer close(done)
		(&workspace.RetentionJob{Store: store, Settings: settings, Interval: time.Millisecond, BatchSize: 1}).Run(ctx)
	}()
	deadline := time.After(time.Second)
	for {
		_, err := store.GetCurrent(context.Background(), item.ItemID)
		if errors.Is(err, workspace.ErrDeleted) {
			break
		}
		select {
		case <-deadline:
			t.Fatal("configured retention job did not purge eligible item")
		case <-time.After(time.Millisecond):
		}
	}
	cancel()
	<-done
}

func TestRetentionJobLogsCommittedPrefixOnFailure(t *testing.T) {
	cs, store, now := newWorkspaceStore(t)
	ctx := context.Background()
	var itemIDs []string
	for _, key := range []string{"job-partial-a", "job-partial-b"} {
		item, err := store.Create(ctx, createInput(key))
		if err != nil {
			t.Fatal(err)
		}
		itemIDs = append(itemIDs, item.ItemID)
		setRetentionState(t, cs, item.ItemID, workspace.ActivationFloor, now.Add(-365*24*time.Hour), *now)
	}
	sort.Strings(itemIDs)
	if _, err := cs.DB().Exec(fmt.Sprintf(`CREATE TRIGGER abort_job_retention_delete BEFORE DELETE ON workspace_items WHEN OLD.item_id = '%s' BEGIN SELECT RAISE(ABORT, 'injected job purge failure'); END`, itemIDs[1])); err != nil {
		t.Fatal(err)
	}

	jobCtx, cancel := context.WithCancel(ctx)
	logs := make(chan string, 4)
	done := make(chan struct{})
	go func() {
		defer close(done)
		(&workspace.RetentionJob{
			Store: store, Settings: workspace.RetentionSettings{PurgeEnabled: true}, Interval: time.Millisecond, BatchSize: 10,
			Logger: func(format string, args ...any) {
				select {
				case logs <- fmt.Sprintf(format, args...):
				default:
				}
			},
		}).Run(jobCtx)
	}()
	var logged string
	select {
	case logged = <-logs:
		cancel()
	case <-time.After(time.Second):
		cancel()
		t.Fatal("retention job did not report its failed pass")
	}
	<-done
	if !strings.Contains(logged, "failed: purged=1 skipped=0") || !strings.Contains(logged, "injected job purge failure") {
		t.Fatalf("failure log omitted committed outcomes: %q", logged)
	}
}
