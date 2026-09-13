package workspace_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/hollis-labs/tesseract/internal/workspace"
)

func createRequest(key, idempotencyKey string) workspace.CreateRequest {
	return workspace.CreateRequest{CreateInput: createInput(key), IdempotencyKey: idempotencyKey}
}

func TestCreateReceiptReplaysIdentityWithoutOldTokenOrUse(t *testing.T) {
	_, store, _ := newWorkspaceStore(t)
	ctx := context.Background()
	req := createRequest("", "retry-1")
	created, err := store.CreateWithReceipt(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if created.Status != "created" || created.VersionToken == "" {
		t.Fatalf("created receipt = %+v", created)
	}
	edited, err := store.Edit(ctx, workspace.EditInput{ItemID: created.ItemID, VersionToken: created.VersionToken, Summary: ptr("changed"), Author: req.Author, SessionID: req.SessionID})
	if err != nil {
		t.Fatal(err)
	}
	before, err := store.GetCurrent(ctx, created.ItemID)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := store.CreateWithReceipt(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if replayed.Status != "replayed" || replayed.ItemID != created.ItemID || replayed.Availability != "live" || replayed.VersionToken != "" {
		t.Fatalf("replay = %+v", replayed)
	}
	after, err := store.GetCurrent(ctx, created.ItemID)
	if err != nil {
		t.Fatal(err)
	}
	if after.VersionToken != edited.VersionToken || after.AccessCount != before.AccessCount || after.Summary != "changed" {
		t.Fatalf("replay changed current item: before=%+v after=%+v", before, after)
	}
}

func TestCreateReceiptSurvivesDeleteAndRejectsChangedArgumentsLosslessly(t *testing.T) {
	_, store, _ := newWorkspaceStore(t)
	ctx := context.Background()
	req := createRequest("", "retry-delete")
	req.Data = json.RawMessage(`{"n":1}`)
	created, err := store.CreateWithReceipt(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	changed := req
	changed.Data = json.RawMessage(`{"n":1.0}`)
	if _, conflictErr := store.CreateWithReceipt(ctx, changed); !errors.Is(conflictErr, workspace.ErrIdempotencyConflict) {
		t.Fatalf("changed numeric representation error = %v", conflictErr)
	}
	if _, deleteErr := store.Delete(ctx, workspace.DeleteInput{ItemID: created.ItemID, VersionToken: created.VersionToken}); deleteErr != nil {
		t.Fatal(deleteErr)
	}
	replay, err := store.CreateWithReceipt(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if replay.Availability != "deleted" || replay.ItemID != created.ItemID || replay.VersionToken != "" {
		t.Fatalf("deleted replay = %+v", replay)
	}
}

func TestCreateReceiptRequiresKeylessKeyAndRollsBackWithItem(t *testing.T) {
	cs, store, _ := newWorkspaceStore(t)
	ctx := context.Background()
	if _, err := store.CreateWithReceipt(ctx, createRequest("", "")); !errors.Is(err, workspace.ErrInvalidInput) {
		t.Fatalf("keyless create error = %v", err)
	}
	if _, err := cs.DB().ExecContext(ctx, `CREATE TRIGGER reject_workspace_receipt BEFORE INSERT ON workspace_creation_receipts BEGIN SELECT RAISE(ABORT, 'receipt rejected'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateWithReceipt(ctx, createRequest("rollback-key", "rollback-receipt")); err == nil {
		t.Fatal("create succeeded despite receipt failure")
	}
	var count int
	if err := cs.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM workspace_items WHERE namespace=? AND key_name=?`, testNamespace, "rollback-key").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("item count after receipt rollback = %d", count)
	}
}

func TestConcurrentExactCreateReceiptAllocatesOneIdentity(t *testing.T) {
	cs, store, _ := newWorkspaceStore(t)
	ctx := context.Background()
	req := createRequest("", "concurrent-retry")
	const n = 8
	results := make(chan workspace.MutationReceipt, n)
	errs := make(chan error, n)
	var wg sync.WaitGroup
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := store.CreateWithReceipt(ctx, req)
			if err != nil {
				errs <- err
				return
			}
			results <- r
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		t.Errorf("concurrent create: %v", err)
	}
	var itemID string
	seen := 0
	for r := range results {
		seen++
		if itemID == "" {
			itemID = r.ItemID
		}
		if r.ItemID != itemID {
			t.Errorf("item ids differ: %s and %s", itemID, r.ItemID)
		}
	}
	if seen != n {
		t.Fatalf("successful results=%d want %d", seen, n)
	}
	var items, receipts int
	if err := cs.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM workspace_items`).Scan(&items); err != nil {
		t.Fatal(err)
	}
	if err := cs.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM workspace_creation_receipts`).Scan(&receipts); err != nil {
		t.Fatal(err)
	}
	if items != 1 || receipts != 1 {
		t.Fatalf("items=%d receipts=%d", items, receipts)
	}
}
