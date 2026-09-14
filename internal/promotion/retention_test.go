package promotion_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/hollis-labs/tesseract/domains"
	"github.com/hollis-labs/tesseract/internal/promotion"
	"github.com/hollis-labs/tesseract/internal/workspace"
)

func makeRetentionEligible(t *testing.T, csDB *sql.DB, itemID string) {
	t.Helper()
	old := time.Now().UTC().Add(-365 * 24 * time.Hour).Format(time.RFC3339Nano)
	if _, err := csDB.Exec(`UPDATE workspace_items SET activation = ?, last_used_at = ?, last_decayed_at = ? WHERE item_id = ?`, workspace.ActivationFloor, old, old, itemID); err != nil {
		t.Fatal(err)
	}
}

func TestRetentionPreservesAppliedReplayAndBlocksPendingSource(t *testing.T) {
	cs, _, ws, promotions := promotionStores(t)
	ctx := context.Background()
	settings := workspace.RetentionSettings{PurgeEnabled: true, MinimumIdle: workspace.MinimumPurgeIdle}

	appliedSource := sourceItem(t, ws, "retention-applied", nil)
	applied := runPromotion(t, promotions, appliedSource, baseTarget(domains.Memory, "retention.applied"))
	makeRetentionEligible(t, cs.DB(), appliedSource.ItemID)
	if report, err := ws.ApplyRetention(ctx, workspace.RetentionApplyInput{Settings: settings, ItemIDs: []string{appliedSource.ItemID}}); err != nil || report.Purged != 1 {
		t.Fatalf("purge applied source: %#v err=%v", report, err)
	}
	replayed, err := promotions.Apply(ctx, promotion.ApplyInput{RequestID: applied.RequestID, Actor: "retry"})
	if err != nil || replayed.TargetRevisionID != applied.TargetRevisionID {
		t.Fatalf("applied promotion did not replay after purge: %#v err=%v", replayed, err)
	}

	pendingSource := sourceItem(t, ws, "retention-pending", nil)
	requested, err := promotions.Request(ctx, promotion.RequestInput{
		SourceItemID: pendingSource.ItemID, SourceVersionToken: pendingSource.VersionToken,
		Actor: "requester", Target: baseTarget(domains.Memory, "retention.pending"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := promotions.Approve(ctx, promotion.ApproveInput{RequestID: requested.RequestID, Actor: "approver"}); err != nil {
		t.Fatal(err)
	}
	makeRetentionEligible(t, cs.DB(), pendingSource.ItemID)
	if report, err := ws.ApplyRetention(ctx, workspace.RetentionApplyInput{Settings: settings, ItemIDs: []string{pendingSource.ItemID}}); err != nil || report.Purged != 1 {
		t.Fatalf("purge pending source: %#v err=%v", report, err)
	}
	if _, err := promotions.Apply(ctx, promotion.ApplyInput{RequestID: requested.RequestID, Actor: "applier"}); !errors.Is(err, promotion.ErrSourceDeleted) {
		t.Fatalf("pending promotion after purge error = %v, want source deleted", err)
	}
}
