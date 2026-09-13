package workspacepromotion_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/hollis-labs/tesseract/domains"
	"github.com/hollis-labs/tesseract/internal/contextstore"
	"github.com/hollis-labs/tesseract/internal/memory"
	"github.com/hollis-labs/tesseract/internal/workspace"
	"github.com/hollis-labs/tesseract/internal/workspacepromotion"
)

func promotionStores(t *testing.T) (*contextstore.Store, *memory.Store, *workspace.Store, *workspacepromotion.Store) {
	t.Helper()
	cs, err := contextstore.Open(context.Background(), contextstore.Config{RootDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	mem := memory.NewStore(cs.DB(), nil, "", 0, memory.NoopQueue{})
	return cs, mem, workspace.NewStore(cs.DB()), workspacepromotion.NewStore(cs, mem)
}

func sourceItem(t *testing.T, ws *workspace.Store, key string, workstream *string) workspace.Item {
	t.Helper()
	item, err := ws.Create(context.Background(), workspace.CreateInput{
		Namespace: "project/tesseract/workspace/promotion", Key: key, Summary: "reviewed secret summary", Body: "body [[linked.note]]",
		Data: json.RawMessage(`{"large":90071992547409931234}`), DataSchemaHash: strings.Repeat("a", 64), Tags: []string{"draft"},
		WorkstreamID: workstream, Author: memory.Author{AgentID: "draft-agent"}, SessionID: "draft-session",
	})
	if err != nil {
		t.Fatal(err)
	}
	return item
}

func baseTarget(domain domains.Domain, key string) workspacepromotion.Target {
	t := workspacepromotion.Target{Domain: domain, Namespace: "user/chrispian/" + string(domain) + "/notes", Key: key,
		Author: memory.Author{AgentID: "review-agent", AgentVersion: "1"}, SessionID: "review-session"}
	switch domain {
	case domains.Memory:
		t.Trigger = memory.TriggerPromotion
		t.DerivedFrom = memory.DerivedFromProject
		t.Status = memory.StatusReviewed
	case domains.Knowledge:
		t.Kind = "project_canonical"
		t.Source = "manual"
		t.Pointer = &memory.Pointer{Scheme: "nil", Locator: "none"}
	case domains.Event:
		t.Namespace = "user/chrispian/event/reasoning"
		t.Trigger = memory.TriggerPromotion
		t.DerivedFrom = memory.DerivedFromObservation
	}
	return t
}

func runPromotion(t *testing.T, store *workspacepromotion.Store, source workspace.Item, target workspacepromotion.Target) workspacepromotion.ApplyReceipt {
	t.Helper()
	ctx := context.Background()
	requested, err := store.Request(ctx, workspacepromotion.RequestInput{SourceItemID: source.ItemID, SourceVersionToken: source.VersionToken, Actor: "requester", Reason: "reviewed", Target: target})
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	approved, err := store.Approve(ctx, workspacepromotion.ApproveInput{RequestID: requested.RequestID, Actor: "approver", Notes: "ok"})
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if approved.ApprovalID == "" {
		t.Fatal("empty approval id")
	}
	applied, err := store.Apply(ctx, workspacepromotion.ApplyInput{RequestID: requested.RequestID, Actor: "applier"})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	return applied
}

func TestPromotionCopiesLiveContentAcrossDomainsAndRetainsNoSourcePayload(t *testing.T) {
	cs, mem, ws, promotions := promotionStores(t)
	for _, domain := range []domains.Domain{domains.Memory, domains.Knowledge, domains.Event} {
		t.Run(string(domain), func(t *testing.T) {
			workstream := "ws-reviewed"
			source := sourceItem(t, ws, "draft-"+string(domain), &workstream)
			before, err := ws.GetCurrent(context.Background(), source.ItemID)
			if err != nil {
				t.Fatal(err)
			}
			receipt := runPromotion(t, promotions, source, baseTarget(domain, "promoted."+string(domain)))
			rev, err := mem.GetRevisionByID(context.Background(), receipt.TargetRevisionID)
			if err != nil {
				t.Fatal(err)
			}
			if rev.Domain != domain || rev.Payload.Summary != source.Summary || rev.Payload.Body != source.Body {
				t.Fatalf("wrong promoted revision: %#v", rev)
			}
			if receipt.SourceItemID != source.ItemID || receipt.SourceVersionToken != source.VersionToken {
				t.Fatalf("receipt lost reviewed source identity: %#v", receipt)
			}
			if string(rev.Payload.Data) != string(source.Data) {
				t.Fatalf("data bytes changed: got %s want %s", rev.Payload.Data, source.Data)
			}
			if rev.Payload.DataSchemaHash != "" {
				t.Fatalf("source schema claim was copied: %q", rev.Payload.DataSchemaHash)
			}
			if rev.WorkstreamID != workstream {
				t.Fatalf("workstream=%q want %q", rev.WorkstreamID, workstream)
			}
			if rev.Provenance != nil {
				t.Fatalf("unstamped apply invented provenance: %#v", rev.Provenance)
			}
			switch domain {
			case domains.Memory:
			case domains.Knowledge:
				if rev.Status != memory.StatusCanonical || rev.Trigger != memory.TriggerManual || rev.DerivedFrom != memory.DerivedFromReference || rev.Facets.Kind == "" || rev.Facets.Source == "" || rev.Facets.Pointer == nil {
					t.Fatalf("knowledge defaults/facets were not normalized: %#v", rev)
				}
			case domains.Event:
				if rev.Status != memory.StatusCanonical || rev.Trigger != memory.TriggerPromotion || rev.DerivedFrom != memory.DerivedFromObservation {
					t.Fatalf("event metadata was not normalized: %#v", rev)
				}
			}
			after, err := ws.GetCurrent(context.Background(), source.ItemID)
			if err != nil {
				t.Fatal(err)
			}
			if after.VersionToken != before.VersionToken || after.AccessCount != before.AccessCount {
				t.Fatalf("source mutated: before=%#v after=%#v", before, after)
			}

			var stored string
			if err := cs.DB().QueryRow(`SELECT target_spec_json FROM workspace_promotion_requests WHERE request_id=?`, receipt.RequestID).Scan(&stored); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(stored, source.Summary) || strings.Contains(stored, string(source.Data)) {
				t.Fatalf("workflow row retained source content: %s", stored)
			}
		})
	}
}

func TestKeylessEventPromotion(t *testing.T) {
	_, mem, ws, promotions := promotionStores(t)
	source := sourceItem(t, ws, "keyless-event-source", nil)
	target := baseTarget(domains.Event, "")
	receipt := runPromotion(t, promotions, source, target)
	rev, err := mem.GetRevisionByID(context.Background(), receipt.TargetRevisionID)
	if err != nil {
		t.Fatal(err)
	}
	if rev.MemoryKey != "" || receipt.TargetKey != "" {
		t.Fatalf("keyless target acquired a key: rev=%#v receipt=%#v", rev, receipt)
	}
}

func TestApplyRejectsChangedOrDeletedSourceWithoutTargetWrite(t *testing.T) {
	_, mem, ws, promotions := promotionStores(t)
	ctx := context.Background()
	for _, deleted := range []bool{false, true} {
		t.Run(map[bool]string{false: "edited", true: "deleted"}[deleted], func(t *testing.T) {
			source := sourceItem(t, ws, map[bool]string{false: "edit-me", true: "delete-me"}[deleted], nil)
			targetKey := "stale." + strings.ReplaceAll(source.Key, "-", "_")
			req, err := promotions.Request(ctx, workspacepromotion.RequestInput{SourceItemID: source.ItemID, SourceVersionToken: source.VersionToken, Actor: "requester", Target: baseTarget(domains.Memory, targetKey)})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = promotions.Approve(ctx, workspacepromotion.ApproveInput{RequestID: req.RequestID, Actor: "approver"}); err != nil {
				t.Fatal(err)
			}
			if deleted {
				_, err = ws.Delete(ctx, workspace.DeleteInput{ItemID: source.ItemID, VersionToken: source.VersionToken})
			} else {
				summary := "changed"
				_, err = ws.Edit(ctx, workspace.EditInput{ItemID: source.ItemID, VersionToken: source.VersionToken, Summary: &summary, Author: memory.Author{AgentID: "editor"}, SessionID: "edit"})
			}
			if err != nil {
				t.Fatal(err)
			}
			_, err = promotions.Apply(ctx, workspacepromotion.ApplyInput{RequestID: req.RequestID, Actor: "applier"})
			if deleted && !errors.Is(err, workspacepromotion.ErrSourceDeleted) {
				t.Fatalf("got %v", err)
			}
			if !deleted && !errors.Is(err, workspacepromotion.ErrSourceStale) {
				t.Fatalf("got %v", err)
			}
			if _, readErr := mem.GetCurrentInDomain(ctx, domains.Memory, "user/chrispian/memory/notes", targetKey); !errors.Is(readErr, memory.ErrNotFound) {
				t.Fatalf("target was written: %v", readErr)
			}
		})
	}
}

func TestExistingTargetPreconditionAssociationAndApplyReplay(t *testing.T) {
	_, mem, ws, promotions := promotionStores(t)
	ctx := context.Background()
	originalWS := "target-stream"
	target, err := mem.WriteRevision(ctx, memory.WriteInput{Domain: domains.Memory, Namespace: "user/chrispian/memory/notes", MemoryKey: "existing.target", WorkstreamID: &originalWS, Status: memory.StatusReviewed, Author: memory.Author{AgentID: "old"}, Trigger: memory.TriggerExplicit, SessionID: "old-session", DerivedFrom: memory.DerivedFromProject, Confidence: .8, Summary: "old"})
	if err != nil {
		t.Fatal(err)
	}
	source := sourceItem(t, ws, "existing-source", nil)
	spec := baseTarget(domains.Memory, "")
	spec.Domain = ""
	spec.Namespace = ""
	spec.Key = ""
	spec.ItemID = target.ItemID
	spec.ExpectedRevisionID = target.RevisionID
	receipt := runPromotion(t, promotions, source, spec)
	if receipt.TargetItemID != target.ItemID {
		t.Fatalf("item id changed: %#v", receipt)
	}
	current, err := mem.GetCurrentByItemID(ctx, target.ItemID)
	if err != nil {
		t.Fatal(err)
	}
	if current.WorkstreamID != originalWS || current.Supersedes != target.RevisionID {
		t.Fatalf("existing target fields: %#v", current)
	}
	if _, err = mem.WriteRevision(ctx, memory.WriteInput{
		Domain: domains.Memory, Namespace: current.Namespace, MemoryKey: current.MemoryKey,
		WorkstreamID: &originalWS, Supersedes: current.RevisionID, Status: memory.StatusReviewed,
		Author: memory.Author{AgentID: "later"}, Trigger: memory.TriggerExplicit, SessionID: "later-session",
		DerivedFrom: memory.DerivedFromProject, Confidence: .8, Summary: "later target revision",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = ws.Delete(ctx, workspace.DeleteInput{ItemID: source.ItemID, VersionToken: source.VersionToken}); err != nil {
		t.Fatal(err)
	}
	replay, err := promotions.Apply(ctx, workspacepromotion.ApplyInput{RequestID: receipt.RequestID, Actor: "different-retry"})
	if err != nil {
		t.Fatal(err)
	}
	if replay.TargetRevisionID != receipt.TargetRevisionID || !replay.AppliedAt.Equal(receipt.AppliedAt) {
		t.Fatalf("replay changed receipt: %#v %#v", receipt, replay)
	}
	history, err := mem.GetHistoryByItemID(ctx, target.ItemID)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 3 {
		t.Fatalf("apply replay wrote %d revisions", len(history))
	}
}

func TestConcurrentApplyCommitsOneRevisionAndOneReceipt(t *testing.T) {
	_, mem, ws, promotions := promotionStores(t)
	ctx := context.Background()
	source := sourceItem(t, ws, "concurrent", nil)
	req, err := promotions.Request(ctx, workspacepromotion.RequestInput{SourceItemID: source.ItemID, SourceVersionToken: source.VersionToken, Actor: "requester", Target: baseTarget(domains.Memory, "concurrent.target")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = promotions.Approve(ctx, workspacepromotion.ApproveInput{RequestID: req.RequestID, Actor: "approver"}); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Add(2)
	receipts := make([]workspacepromotion.ApplyReceipt, 2)
	errs := make([]error, 2)
	for i := range 2 {
		go func(i int) {
			defer wg.Done()
			receipts[i], errs[i] = promotions.Apply(ctx, workspacepromotion.ApplyInput{RequestID: req.RequestID, Actor: "applier"})
		}(i)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if receipts[0].TargetRevisionID != receipts[1].TargetRevisionID {
		t.Fatalf("different receipts: %#v", receipts)
	}
	history, err := mem.GetHistoryByItemID(ctx, receipts[0].TargetItemID)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 {
		t.Fatalf("concurrent apply wrote %d revisions", len(history))
	}
}

func TestConcurrentApprovalReturnsOneApproval(t *testing.T) {
	_, _, ws, promotions := promotionStores(t)
	ctx := context.Background()
	source := sourceItem(t, ws, "concurrent-approval", nil)
	req, err := promotions.Request(ctx, workspacepromotion.RequestInput{SourceItemID: source.ItemID, SourceVersionToken: source.VersionToken, Actor: "requester", Target: baseTarget(domains.Memory, "concurrent.approval")})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Add(2)
	receipts := make([]workspacepromotion.ApprovalReceipt, 2)
	errs := make([]error, 2)
	for i := range 2 {
		go func(i int) {
			defer wg.Done()
			receipts[i], errs[i] = promotions.Approve(ctx, workspacepromotion.ApproveInput{RequestID: req.RequestID, Actor: "approver"})
		}(i)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if receipts[0].ApprovalID == "" || receipts[0].ApprovalID != receipts[1].ApprovalID || !receipts[0].ApprovedAt.Equal(receipts[1].ApprovedAt) {
		t.Fatalf("concurrent approvals diverged: %#v", receipts)
	}
}

func TestPromotionAssociationAndApplyingWriteContextAreIndependent(t *testing.T) {
	_, mem, ws, promotions := promotionStores(t)
	ctx := context.Background()
	sourceAssociation := "ws-source"
	source := sourceItem(t, ws, "provenance-source", &sourceAssociation)
	target := baseTarget(domains.Memory, "provenance.target")
	requested, err := promotions.Request(ctx, workspacepromotion.RequestInput{SourceItemID: source.ItemID, SourceVersionToken: source.VersionToken, Actor: "requester", Target: target})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = promotions.Approve(ctx, workspacepromotion.ApproveInput{RequestID: requested.RequestID, Actor: "approver"}); err != nil {
		t.Fatal(err)
	}
	applyCtx := memory.ContextWithWriteContext(ctx, memory.WriteContext{Issuer: "tether", Verification: "unverified", SessionID: "apply-session", WorkstreamID: "ws-applying"})
	receipt, err := promotions.Apply(applyCtx, workspacepromotion.ApplyInput{RequestID: requested.RequestID, Actor: "applier"})
	if err != nil {
		t.Fatal(err)
	}
	rev, err := mem.GetRevisionByID(ctx, receipt.TargetRevisionID)
	if err != nil {
		t.Fatal(err)
	}
	if rev.WorkstreamID != sourceAssociation || rev.Provenance == nil || rev.Provenance.WriteContext == nil || rev.Provenance.WriteContext.WorkstreamID != "ws-applying" {
		t.Fatalf("association and applying context were conflated: %#v", rev)
	}
	replayCtx := memory.ContextWithWriteContext(ctx, memory.WriteContext{Issuer: "tether", Verification: "unverified", SessionID: "retry-session", WorkstreamID: "ws-retry"})
	replayed, err := promotions.Apply(replayCtx, workspacepromotion.ApplyInput{RequestID: requested.RequestID, Actor: "retry"})
	if err != nil || replayed.TargetRevisionID != receipt.TargetRevisionID {
		t.Fatalf("replay=%#v err=%v", replayed, err)
	}
	after, err := mem.GetRevisionByID(ctx, receipt.TargetRevisionID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Provenance.WriteContext.SessionID != "apply-session" || after.WorkstreamID != sourceAssociation {
		t.Fatalf("replay changed immutable association/provenance: %#v", after)
	}
}

func TestPromotionExplicitAssociationClearIsFrozen(t *testing.T) {
	_, mem, ws, promotions := promotionStores(t)
	sourceAssociation := "ws-source"
	source := sourceItem(t, ws, "clear-association-source", &sourceAssociation)
	target := baseTarget(domains.Memory, "clear.association")
	cleared := ""
	target.WorkstreamID = &cleared
	receipt := runPromotion(t, promotions, source, target)
	rev, err := mem.GetRevisionByID(context.Background(), receipt.TargetRevisionID)
	if err != nil {
		t.Fatal(err)
	}
	if rev.WorkstreamID != "" {
		t.Fatalf("explicit clear did not win: %#v", rev)
	}
}

func TestPromotionRejectsWrongExistingRevisionAndNewlyOccupiedKey(t *testing.T) {
	_, mem, ws, promotions := promotionStores(t)
	ctx := context.Background()
	input := func(key string) memory.WriteInput {
		return memory.WriteInput{Domain: domains.Memory, Namespace: "user/chrispian/memory/notes", MemoryKey: key, Status: memory.StatusReviewed, Author: memory.Author{AgentID: "old"}, Trigger: memory.TriggerExplicit, SessionID: "old", DerivedFrom: memory.DerivedFromProject, Summary: "old"}
	}
	first, err := mem.WriteRevision(ctx, input("parent.first"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := mem.WriteRevision(ctx, input("parent.second"))
	if err != nil {
		t.Fatal(err)
	}
	source := sourceItem(t, ws, "wrong-parent-source", nil)
	wrong := baseTarget(domains.Memory, "")
	wrong.Domain, wrong.Namespace, wrong.ItemID, wrong.ExpectedRevisionID = "", "", first.ItemID, second.RevisionID
	if _, err = promotions.Request(ctx, workspacepromotion.RequestInput{SourceItemID: source.ItemID, SourceVersionToken: source.VersionToken, Actor: "requester", Target: wrong}); !errors.Is(err, workspacepromotion.ErrTargetStale) {
		t.Fatalf("wrong target parent/revision error=%v", err)
	}

	key := "occupied.after.review"
	requested, err := promotions.Request(ctx, workspacepromotion.RequestInput{SourceItemID: source.ItemID, SourceVersionToken: source.VersionToken, Actor: "requester", Target: baseTarget(domains.Memory, key)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = promotions.Approve(ctx, workspacepromotion.ApproveInput{RequestID: requested.RequestID, Actor: "approver"}); err != nil {
		t.Fatal(err)
	}
	if _, err = mem.WriteRevision(ctx, input(key)); err != nil {
		t.Fatal(err)
	}
	if _, err = promotions.Apply(ctx, workspacepromotion.ApplyInput{RequestID: requested.RequestID, Actor: "applier"}); !errors.Is(err, workspacepromotion.ErrTargetKeyOccupied) {
		t.Fatalf("newly occupied target error=%v", err)
	}
}

func TestApplyRejectsAdvancedExistingTarget(t *testing.T) {
	_, mem, ws, promotions := promotionStores(t)
	ctx := context.Background()
	targetInput := memory.WriteInput{Domain: domains.Memory, Namespace: "user/chrispian/memory/notes", MemoryKey: "advanced.target", Status: memory.StatusReviewed, Author: memory.Author{AgentID: "old"}, Trigger: memory.TriggerExplicit, SessionID: "old", DerivedFrom: memory.DerivedFromProject, Summary: "old"}
	target, err := mem.WriteRevision(ctx, targetInput)
	if err != nil {
		t.Fatal(err)
	}
	source := sourceItem(t, ws, "advanced-source", nil)
	spec := baseTarget(domains.Memory, "")
	spec.Domain, spec.Namespace, spec.Key, spec.ItemID, spec.ExpectedRevisionID = "", "", "", target.ItemID, target.RevisionID
	requested, err := promotions.Request(ctx, workspacepromotion.RequestInput{SourceItemID: source.ItemID, SourceVersionToken: source.VersionToken, Actor: "requester", Target: spec})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = promotions.Approve(ctx, workspacepromotion.ApproveInput{RequestID: requested.RequestID, Actor: "approver"}); err != nil {
		t.Fatal(err)
	}
	targetInput.Supersedes = target.RevisionID
	targetInput.Summary = "advanced"
	if _, err = mem.WriteRevision(ctx, targetInput); err != nil {
		t.Fatal(err)
	}
	if _, err = promotions.Apply(ctx, workspacepromotion.ApplyInput{RequestID: requested.RequestID, Actor: "applier"}); !errors.Is(err, workspacepromotion.ErrTargetStale) {
		t.Fatalf("got %v", err)
	}
	history, err := mem.GetHistoryByItemID(ctx, target.ItemID)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 2 {
		t.Fatalf("stale apply wrote a revision: %d", len(history))
	}
}

func TestReceiptFailureRollsBackTargetRevisionAndIndexes(t *testing.T) {
	cs, mem, ws, promotions := promotionStores(t)
	ctx := context.Background()
	source := sourceItem(t, ws, "rollback-source", nil)
	target := baseTarget(domains.Memory, "rollback.target")
	requested, err := promotions.Request(ctx, workspacepromotion.RequestInput{SourceItemID: source.ItemID, SourceVersionToken: source.VersionToken, Actor: "requester", Target: target})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = promotions.Approve(ctx, workspacepromotion.ApproveInput{RequestID: requested.RequestID, Actor: "approver"}); err != nil {
		t.Fatal(err)
	}
	if _, err = cs.DB().Exec(`CREATE TRIGGER reject_promotion_receipt BEFORE UPDATE OF status ON workspace_promotion_requests WHEN new.status='applied' BEGIN SELECT RAISE(ABORT,'receipt failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err = promotions.Apply(ctx, workspacepromotion.ApplyInput{RequestID: requested.RequestID, Actor: "applier"}); err == nil {
		t.Fatal("apply succeeded despite receipt failure")
	}
	if _, err = mem.GetCurrentInDomain(ctx, domains.Memory, target.Namespace, target.Key); !errors.Is(err, memory.ErrNotFound) {
		t.Fatalf("target survived rollback: %v", err)
	}
	var links int
	if err := cs.DB().QueryRow(`SELECT COUNT(*) FROM memory_links`).Scan(&links); err != nil {
		t.Fatal(err)
	}
	if links != 0 {
		t.Fatalf("link index survived rollback: %d", links)
	}
	if _, err := cs.GetNamespacePolicy(ctx, target.Namespace); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("namespace registration survived rollback: %v", err)
	}
}
