package workspace_test

import (
	"context"
	"errors"
	"testing"

	"github.com/hollis-labs/tesseract/internal/memory"
	"github.com/hollis-labs/tesseract/internal/workspace"
)

func workspaceReceiverContext(sessionID, workstreamID string) context.Context {
	return memory.ContextWithWriteContext(context.Background(), memory.WriteContext{Issuer: "tether", Verification: "unverified", SessionID: sessionID, WorkstreamID: workstreamID})
}

func TestWorkspaceAssociationReceiptAndRetrySemantics(t *testing.T) {
	_, store, _ := newWorkspaceStore(t)
	req := workspace.CreateRequest{CreateInput: createInput("workstream/retry"), IdempotencyKey: "retry-1"}
	created, err := store.CreateWithReceipt(workspaceReceiverContext("tether-a", "ws-a"), req)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := store.CreateWithReceipt(workspaceReceiverContext("tether-b", "ws-b"), req)
	if err != nil || replayed.Status != "replayed" || replayed.ItemID != created.ItemID {
		t.Fatalf("retry = %#v, err=%v", replayed, err)
	}
	item, err := store.GetCurrent(context.Background(), created.ItemID)
	if err != nil {
		t.Fatal(err)
	}
	if item.WorkstreamID != "ws-a" || item.Provenance.WriteContext.SessionID != "tether-a" {
		t.Fatalf("retry rewrote first receipt: %#v", item)
	}
	different := req
	changed := "ws-explicit"
	different.WorkstreamID = &changed
	if _, retryErr := store.CreateWithReceipt(context.Background(), different); !errors.Is(retryErr, workspace.ErrIdempotencyConflict) {
		t.Fatalf("explicit association was omitted from retry digest: %v", retryErr)
	}

	summary := item.Summary
	edited, err := store.Edit(workspaceReceiverContext("tether-c", "ws-c"), workspace.EditInput{
		ItemID: item.ItemID, VersionToken: item.VersionToken, Summary: &summary,
		Author: memory.Author{AgentID: "writer"}, SessionID: "session-c",
	})
	if err != nil {
		t.Fatal(err)
	}
	if edited.WorkstreamID != "ws-a" || edited.Provenance.WriteContext.WorkstreamID != "ws-c" {
		t.Fatalf("edit association/receipt = %#v", edited)
	}

	body := "unstamped"
	unstamped, err := store.Edit(context.Background(), workspace.EditInput{
		ItemID: edited.ItemID, VersionToken: edited.VersionToken, Body: &body,
		Author: memory.Author{AgentID: "writer"}, SessionID: "session-d",
	})
	if err != nil {
		t.Fatal(err)
	}
	if unstamped.Provenance != nil || unstamped.WorkstreamID != "ws-a" {
		t.Fatalf("unstamped edit retained stale receipt or changed association: %#v", unstamped)
	}

	value := "ws-new"
	_, err = store.Edit(context.Background(), workspace.EditInput{
		ItemID: unstamped.ItemID, VersionToken: unstamped.VersionToken, WorkstreamID: &value,
		ClearFields: []workspace.ClearField{workspace.ClearWorkstreamID},
		Author:      memory.Author{AgentID: "writer"}, SessionID: "session-e",
	})
	if !errors.Is(err, workspace.ErrInvalidInput) {
		t.Fatalf("set+clear error = %v", err)
	}
}

func TestWorkspaceWorkstreamFilterPrecedesLimit(t *testing.T) {
	_, store, _ := newWorkspaceStore(t)
	for i, ws := range []string{"other", "selected"} {
		in := createInput("filter/" + ws)
		in.WorkstreamID = &ws
		if _, err := store.Create(context.Background(), in); err != nil {
			t.Fatal(i, err)
		}
	}
	hits, err := store.Recall(context.Background(), workspace.RecallInput{Namespaces: []string{testNamespace}, WorkstreamID: "selected", Limit: 1})
	if err != nil || len(hits) != 1 || hits[0].Item.WorkstreamID != "selected" {
		t.Fatalf("filtered recall = %#v, err=%v", hits, err)
	}
}
