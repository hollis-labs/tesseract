package promotion_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/tesseract/domains"
	"github.com/hollis-labs/tesseract/internal/contextstore"
	"github.com/hollis-labs/tesseract/internal/memory"
	"github.com/hollis-labs/tesseract/internal/promotion"
	"github.com/hollis-labs/tesseract/internal/workspace"
)

func promotionStores(t *testing.T) (*contextstore.Store, *memory.Store, *workspace.Store, *promotion.Store) {
	t.Helper()
	cs, err := contextstore.Open(context.Background(), contextstore.Config{RootDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	mem := memory.NewStore(cs.DB(), nil, "", 0, memory.NoopQueue{})
	return cs, mem, workspace.NewStore(cs.DB()), promotion.NewStore(cs, mem)
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

func baseTarget(domain domains.Domain, key string) promotion.Target {
	t := promotion.Target{Domain: domain, Namespace: "user/chrispian/" + string(domain) + "/notes", Key: key,
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

func runPromotion(t *testing.T, store *promotion.Store, source workspace.Item, target promotion.Target) promotion.ApplyReceipt {
	t.Helper()
	ctx := context.Background()
	requested, err := store.Request(ctx, promotion.RequestInput{SourceItemID: source.ItemID, SourceVersionToken: source.VersionToken, Actor: "requester", Reason: "reviewed", Target: target})
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	approved, err := store.Approve(ctx, promotion.ApproveInput{RequestID: requested.RequestID, Actor: "approver", Notes: "ok"})
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if approved.ApprovalID == "" {
		t.Fatal("empty approval id")
	}
	applied, err := store.Apply(ctx, promotion.ApplyInput{RequestID: requested.RequestID, Actor: "applier"})
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
			if err := cs.DB().QueryRow(`SELECT target_spec_json FROM promotion_requests WHERE request_id=?`, receipt.RequestID).Scan(&stored); err != nil {
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
			req, err := promotions.Request(ctx, promotion.RequestInput{SourceItemID: source.ItemID, SourceVersionToken: source.VersionToken, Actor: "requester", Target: baseTarget(domains.Memory, targetKey)})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = promotions.Approve(ctx, promotion.ApproveInput{RequestID: req.RequestID, Actor: "approver"}); err != nil {
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
			_, err = promotions.Apply(ctx, promotion.ApplyInput{RequestID: req.RequestID, Actor: "applier"})
			if deleted && !errors.Is(err, promotion.ErrSourceDeleted) {
				t.Fatalf("got %v", err)
			}
			if !deleted && !errors.Is(err, promotion.ErrSourceStale) {
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
	target, err := mem.WriteRevision(ctx, memory.WriteInput{Domain: domains.Memory, Actor: "user", Namespace: "user/chrispian/memory/notes", MemoryKey: "existing.target", WorkstreamID: &originalWS, Status: memory.StatusReviewed, Author: memory.Author{AgentID: "old"}, Trigger: memory.TriggerExplicit, SessionID: "old-session", DerivedFrom: memory.DerivedFromProject, Confidence: .8, Summary: "old"})
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
		Domain: domains.Memory, Actor: "user", Namespace: current.Namespace, MemoryKey: current.MemoryKey,
		WorkstreamID: &originalWS, Supersedes: current.RevisionID, Status: memory.StatusReviewed,
		Author: memory.Author{AgentID: "later"}, Trigger: memory.TriggerExplicit, SessionID: "later-session",
		DerivedFrom: memory.DerivedFromProject, Confidence: .8, Summary: "later target revision",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = ws.Delete(ctx, workspace.DeleteInput{ItemID: source.ItemID, VersionToken: source.VersionToken}); err != nil {
		t.Fatal(err)
	}
	replay, err := promotions.Apply(ctx, promotion.ApplyInput{RequestID: receipt.RequestID, Actor: "different-retry"})
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
	req, err := promotions.Request(ctx, promotion.RequestInput{SourceItemID: source.ItemID, SourceVersionToken: source.VersionToken, Actor: "requester", Target: baseTarget(domains.Memory, "concurrent.target")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = promotions.Approve(ctx, promotion.ApproveInput{RequestID: req.RequestID, Actor: "approver"}); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Add(2)
	receipts := make([]promotion.ApplyReceipt, 2)
	errs := make([]error, 2)
	for i := range 2 {
		go func(i int) {
			defer wg.Done()
			receipts[i], errs[i] = promotions.Apply(ctx, promotion.ApplyInput{RequestID: req.RequestID, Actor: "applier"})
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
	req, err := promotions.Request(ctx, promotion.RequestInput{SourceItemID: source.ItemID, SourceVersionToken: source.VersionToken, Actor: "requester", Target: baseTarget(domains.Memory, "concurrent.approval")})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Add(2)
	receipts := make([]promotion.ApprovalReceipt, 2)
	errs := make([]error, 2)
	for i := range 2 {
		go func(i int) {
			defer wg.Done()
			receipts[i], errs[i] = promotions.Approve(ctx, promotion.ApproveInput{RequestID: req.RequestID, Actor: "approver"})
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
	requested, err := promotions.Request(ctx, promotion.RequestInput{SourceItemID: source.ItemID, SourceVersionToken: source.VersionToken, Actor: "requester", Target: target})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = promotions.Approve(ctx, promotion.ApproveInput{RequestID: requested.RequestID, Actor: "approver"}); err != nil {
		t.Fatal(err)
	}
	applyCtx := memory.ContextWithWriteContext(ctx, memory.WriteContext{Issuer: "tether", Verification: "unverified", SessionID: "apply-session", WorkstreamID: "ws-applying"})
	receipt, err := promotions.Apply(applyCtx, promotion.ApplyInput{RequestID: requested.RequestID, Actor: "applier"})
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
	replayed, err := promotions.Apply(replayCtx, promotion.ApplyInput{RequestID: requested.RequestID, Actor: "retry"})
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
		return memory.WriteInput{Domain: domains.Memory, Actor: "user", Namespace: "user/chrispian/memory/notes", MemoryKey: key, Status: memory.StatusReviewed, Author: memory.Author{AgentID: "old"}, Trigger: memory.TriggerExplicit, SessionID: "old", DerivedFrom: memory.DerivedFromProject, Summary: "old"}
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
	if _, err = promotions.Request(ctx, promotion.RequestInput{SourceItemID: source.ItemID, SourceVersionToken: source.VersionToken, Actor: "requester", Target: wrong}); !errors.Is(err, promotion.ErrTargetStale) {
		t.Fatalf("wrong target parent/revision error=%v", err)
	}

	key := "occupied.after.review"
	requested, err := promotions.Request(ctx, promotion.RequestInput{SourceItemID: source.ItemID, SourceVersionToken: source.VersionToken, Actor: "requester", Target: baseTarget(domains.Memory, key)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = promotions.Approve(ctx, promotion.ApproveInput{RequestID: requested.RequestID, Actor: "approver"}); err != nil {
		t.Fatal(err)
	}
	if _, err = mem.WriteRevision(ctx, input(key)); err != nil {
		t.Fatal(err)
	}
	if _, err = promotions.Apply(ctx, promotion.ApplyInput{RequestID: requested.RequestID, Actor: "applier"}); !errors.Is(err, promotion.ErrTargetKeyOccupied) {
		t.Fatalf("newly occupied target error=%v", err)
	}
}

func TestApplyRejectsAdvancedExistingTarget(t *testing.T) {
	_, mem, ws, promotions := promotionStores(t)
	ctx := context.Background()
	targetInput := memory.WriteInput{Domain: domains.Memory, Actor: "user", Namespace: "user/chrispian/memory/notes", MemoryKey: "advanced.target", Status: memory.StatusReviewed, Author: memory.Author{AgentID: "old"}, Trigger: memory.TriggerExplicit, SessionID: "old", DerivedFrom: memory.DerivedFromProject, Summary: "old"}
	target, err := mem.WriteRevision(ctx, targetInput)
	if err != nil {
		t.Fatal(err)
	}
	source := sourceItem(t, ws, "advanced-source", nil)
	spec := baseTarget(domains.Memory, "")
	spec.Domain, spec.Namespace, spec.Key, spec.ItemID, spec.ExpectedRevisionID = "", "", "", target.ItemID, target.RevisionID
	requested, err := promotions.Request(ctx, promotion.RequestInput{SourceItemID: source.ItemID, SourceVersionToken: source.VersionToken, Actor: "requester", Target: spec})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = promotions.Approve(ctx, promotion.ApproveInput{RequestID: requested.RequestID, Actor: "approver"}); err != nil {
		t.Fatal(err)
	}
	targetInput.Supersedes = target.RevisionID
	targetInput.Summary = "advanced"
	if _, err = mem.WriteRevision(ctx, targetInput); err != nil {
		t.Fatal(err)
	}
	if _, err = promotions.Apply(ctx, promotion.ApplyInput{RequestID: requested.RequestID, Actor: "applier"}); !errors.Is(err, promotion.ErrTargetStale) {
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
	requested, err := promotions.Request(ctx, promotion.RequestInput{SourceItemID: source.ItemID, SourceVersionToken: source.VersionToken, Actor: "requester", Target: target})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = promotions.Approve(ctx, promotion.ApproveInput{RequestID: requested.RequestID, Actor: "approver"}); err != nil {
		t.Fatal(err)
	}
	if _, err = cs.DB().Exec(`CREATE TRIGGER reject_promotion_receipt BEFORE UPDATE OF status ON promotion_requests WHEN new.status='applied' BEGIN SELECT RAISE(ABORT,'receipt failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err = promotions.Apply(ctx, promotion.ApplyInput{RequestID: requested.RequestID, Actor: "applier"}); err == nil {
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

func TestPromotionKnowledgeToWorkspace(t *testing.T) {
	ctx := context.Background()
	cs, mem, ws, promotions := promotionStores(t)

	tx, err := cs.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()

	now := time.Now().UTC()
	kRev, err := mem.WriteRevisionInTx(ctx, tx, memory.WriteInput{
		Domain:      domains.Knowledge,
		Namespace:   "user/chrispian/knowledge/notes",
		MemoryKey:   "doc.spec",
		Summary:     "specification summary",
		Body:        "full specification body",
		Data:        json.RawMessage(`{"version":1}`),
		Status:      memory.StatusCanonical,
		Trigger:     memory.TriggerManual,
		DerivedFrom: memory.DerivedFromReference,
		Actor:       "user",
		Author:      memory.Author{AgentID: "author"},
		SessionID:   "session-1",
		Facets: memory.Facets{
			Kind:    "doc",
			Source:  "manual",
			Pointer: &memory.Pointer{Scheme: "nil", Locator: "none", ResolvedAt: &now},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}

	target := promotion.Target{
		Domain:    domains.Domain("workspace"),
		Namespace: "user/chrispian/workspace/notes",
		Key:       "doc.spec.draft",
		Author:    memory.Author{AgentID: "agent-promote", AgentVersion: "1"},
		SessionID: "sess-promote",
	}

	reqReceipt, err := promotions.Request(ctx, promotion.RequestInput{
		SourceDomain:       domains.Knowledge,
		SourceItemID:       kRev.ItemID,
		SourceVersionToken: kRev.RevisionID,
		Actor:              "requester",
		Reason:             "moving to workspace for drafting",
		Target:             target,
	})
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	if reqReceipt.SourceDomain != domains.Knowledge {
		t.Fatalf("expected SourceDomain knowledge, got %s", reqReceipt.SourceDomain)
	}

	appReceipt, err := promotions.Approve(ctx, promotion.ApproveInput{
		RequestID: reqReceipt.RequestID,
		Actor:     "approver",
	})
	if err != nil {
		t.Fatalf("Approve failed: %v", err)
	}
	if appReceipt.Status != "approved" {
		t.Fatalf("expected approved status, got %s", appReceipt.Status)
	}

	applyReceipt, err := promotions.Apply(ctx, promotion.ApplyInput{
		RequestID: reqReceipt.RequestID,
		Actor:     "applier",
	})
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}
	if applyReceipt.SourceDomain != domains.Knowledge {
		t.Fatalf("expected SourceDomain knowledge, got %s", applyReceipt.SourceDomain)
	}
	if applyReceipt.TargetDomain != domains.Domain("workspace") {
		t.Fatalf("expected TargetDomain workspace, got %s", applyReceipt.TargetDomain)
	}
	if applyReceipt.TargetVersionToken == "" {
		t.Fatal("expected non-empty TargetVersionToken")
	}

	// Verify target in workspace
	wsItem, err := ws.GetCurrent(ctx, applyReceipt.TargetItemID)
	if err != nil {
		t.Fatalf("workspace item not found: %v", err)
	}
	if wsItem.Summary != "specification summary" || wsItem.Body != "full specification body" {
		t.Fatalf("workspace item content mismatch: summary=%q body=%q", wsItem.Summary, wsItem.Body)
	}
	if wsItem.Key != "doc.spec.draft" {
		t.Fatalf("workspace item key mismatch: %q", wsItem.Key)
	}

	// Verify source knowledge revision is deprecated
	sourceRev, err := mem.GetRevisionByID(ctx, kRev.RevisionID)
	if err != nil {
		t.Fatalf("get source revision: %v", err)
	}
	if sourceRev.Status != memory.StatusDeprecated {
		t.Fatalf("expected source revision status deprecated, got %s", sourceRev.Status)
	}

	// Re-apply is idempotent
	reapply, err := promotions.Apply(ctx, promotion.ApplyInput{
		RequestID: reqReceipt.RequestID,
		Actor:     "applier",
	})
	if err != nil {
		t.Fatalf("Re-apply failed: %v", err)
	}
	if reapply.TargetItemID != applyReceipt.TargetItemID || reapply.TargetVersionToken != applyReceipt.TargetVersionToken {
		t.Fatalf("Re-apply receipt mismatch: %#v vs %#v", reapply, applyReceipt)
	}
}

func TestPromotionMemoryToWorkspacePreservesNonCurrentRevisions(t *testing.T) {
	ctx := context.Background()
	cs, mem, ws, promotions := promotionStores(t)

	tx, err := cs.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()

	rev1, err := mem.WriteRevisionInTx(ctx, tx, memory.WriteInput{
		Domain:      domains.Memory,
		Namespace:   "user/chrispian/memory/notes",
		MemoryKey:   "multi.rev",
		Summary:     "revision 1 summary",
		Body:        "revision 1 body",
		Status:      memory.StatusCanonical,
		Trigger:     memory.TriggerPromotion,
		DerivedFrom: memory.DerivedFromProject,
		Actor:       "user",
		Author:      memory.Author{AgentID: "author"},
		SessionID:   "session-1",
	})
	if err != nil {
		t.Fatal(err)
	}

	rev2, err := mem.WriteRevisionInTx(ctx, tx, memory.WriteInput{
		Domain:      domains.Memory,
		Namespace:   "user/chrispian/memory/notes",
		MemoryKey:   "multi.rev",
		Supersedes:  rev1.RevisionID,
		Summary:     "revision 2 summary",
		Body:        "revision 2 body",
		Status:      memory.StatusCanonical,
		Trigger:     memory.TriggerPromotion,
		DerivedFrom: memory.DerivedFromProject,
		Actor:       "user",
		Author:      memory.Author{AgentID: "author"},
		SessionID:   "session-2",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}

	// Verify rev2 is current
	stateBefore, err := mem.GetState(ctx, rev2.ItemID)
	if err != nil {
		t.Fatal(err)
	}
	if stateBefore.CurrentRevision != rev2.RevisionID {
		t.Fatalf("expected current revision %s, got %s", rev2.RevisionID, stateBefore.CurrentRevision)
	}

	// Promote rev2 to workspace
	target := promotion.Target{
		Domain:    domains.Domain("workspace"),
		Namespace: "user/chrispian/workspace/notes",
		Key:       "multi.rev.workspace",
		Author:    memory.Author{AgentID: "agent-promote"},
		SessionID: "sess-promote",
	}

	reqReceipt, err := promotions.Request(ctx, promotion.RequestInput{
		SourceDomain:       domains.Memory,
		SourceItemID:       rev2.ItemID,
		SourceVersionToken: rev2.RevisionID,
		Actor:              "requester",
		Target:             target,
	})
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}

	_, err = promotions.Approve(ctx, promotion.ApproveInput{
		RequestID: reqReceipt.RequestID,
		Actor:     "approver",
	})
	if err != nil {
		t.Fatalf("Approve failed: %v", err)
	}

	applyReceipt, err := promotions.Apply(ctx, promotion.ApplyInput{
		RequestID: reqReceipt.RequestID,
		Actor:     "applier",
	})
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}

	// Verify rev2 is deprecated
	r2, err := mem.GetRevisionByID(ctx, rev2.RevisionID)
	if err != nil {
		t.Fatal(err)
	}
	if r2.Status != memory.StatusDeprecated {
		t.Fatalf("expected rev2 deprecated, got %s", r2.Status)
	}

	// Verify rev1 is still readable in memory
	r1, err := mem.GetRevisionByID(ctx, rev1.RevisionID)
	if err != nil {
		t.Fatalf("expected rev1 to remain readable: %v", err)
	}
	if r1.Payload.Summary != "revision 1 summary" {
		t.Fatalf("expected rev1 summary: %q", r1.Payload.Summary)
	}

	// Verify history still has both revisions
	history, err := mem.GetHistoryByItemID(ctx, rev2.ItemID)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 2 {
		t.Fatalf("expected 2 revisions in history, got %d", len(history))
	}

	// Verify workspace target created
	wsItem, err := ws.GetCurrent(ctx, applyReceipt.TargetItemID)
	if err != nil {
		t.Fatal(err)
	}
	if wsItem.Summary != "revision 2 summary" {
		t.Fatalf("expected summary 'revision 2 summary', got %q", wsItem.Summary)
	}
}

func TestPromotionKnowledgeToMemory(t *testing.T) {
	ctx := context.Background()
	cs, mem, _, promotions := promotionStores(t)

	tx, err := cs.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()

	now := time.Now().UTC()
	kRev, err := mem.WriteRevisionInTx(ctx, tx, memory.WriteInput{
		Domain:      domains.Knowledge,
		Namespace:   "user/chrispian/knowledge/concepts",
		MemoryKey:   "architecture",
		Summary:     "system architecture",
		Body:        "system details",
		Status:      memory.StatusCanonical,
		Trigger:     memory.TriggerManual,
		DerivedFrom: memory.DerivedFromReference,
		Actor:       "user",
		Author:      memory.Author{AgentID: "author"},
		SessionID:   "sess-1",
		Facets: memory.Facets{
			Kind:    "doc",
			Source:  "manual",
			Pointer: &memory.Pointer{Scheme: "nil", Locator: "none", ResolvedAt: &now},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}

	target := promotion.Target{
		Domain:      domains.Memory,
		Namespace:   "user/chrispian/memory/notes",
		Key:         "architecture.mem",
		Trigger:     memory.TriggerPromotion,
		DerivedFrom: memory.DerivedFromProject,
		Status:      memory.StatusCanonical,
		Author:      memory.Author{AgentID: "promoter"},
		SessionID:   "sess-promoter",
	}

	reqReceipt, err := promotions.Request(ctx, promotion.RequestInput{
		SourceDomain:       domains.Knowledge,
		SourceItemID:       kRev.ItemID,
		SourceVersionToken: kRev.RevisionID,
		Actor:              "requester",
		Target:             target,
	})
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}

	_, err = promotions.Approve(ctx, promotion.ApproveInput{
		RequestID: reqReceipt.RequestID,
		Actor:     "approver",
	})
	if err != nil {
		t.Fatalf("Approve failed: %v", err)
	}

	applyReceipt, err := promotions.Apply(ctx, promotion.ApplyInput{
		RequestID: reqReceipt.RequestID,
		Actor:     "applier",
	})
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}
	if applyReceipt.SourceDomain != domains.Knowledge || applyReceipt.TargetDomain != domains.Memory {
		t.Fatalf("domain pair mismatch: %#v", applyReceipt)
	}

	// Verify target exists in memory
	targetRev, err := mem.GetCurrentInDomain(ctx, domains.Memory, target.Namespace, target.Key)
	if err != nil {
		t.Fatalf("get memory target: %v", err)
	}
	if targetRev.Payload.Summary != "system architecture" {
		t.Fatalf("target summary mismatch: %q", targetRev.Payload.Summary)
	}

	// Verify source knowledge revision deprecated
	kSource, err := mem.GetRevisionByID(ctx, kRev.RevisionID)
	if err != nil {
		t.Fatal(err)
	}
	if kSource.Status != memory.StatusDeprecated {
		t.Fatalf("expected knowledge source deprecated, got %s", kSource.Status)
	}
}

func TestPromotionRejectsOccupiedWorkspaceKey(t *testing.T) {
	ctx := context.Background()
	_, _, ws, promotions := promotionStores(t)

	// Pre-populate an item with key "existing.key" in workspace
	_, err := ws.Create(ctx, workspace.CreateInput{
		Namespace: "user/chrispian/workspace/notes",
		Key:       "existing.key",
		Summary:   "already here",
		Author:    memory.Author{AgentID: "author"},
		SessionID: "sess-init",
	})
	if err != nil {
		t.Fatal(err)
	}

	source := sourceItem(t, ws, "some-source-key", nil)

	// Attempt to promote to same namespace and occupied key
	target := promotion.Target{
		Domain:    domains.Domain("workspace"),
		Namespace: "user/chrispian/workspace/notes",
		Key:       "existing.key",
		Author:    memory.Author{AgentID: "promoter"},
		SessionID: "sess-promoter",
	}

	_, err = promotions.Request(ctx, promotion.RequestInput{
		SourceDomain:       domains.Domain("workspace"),
		SourceItemID:       source.ItemID,
		SourceVersionToken: source.VersionToken,
		Actor:              "requester",
		Target:             target,
	})
	if !errors.Is(err, promotion.ErrTargetKeyOccupied) {
		t.Fatalf("expected ErrTargetKeyOccupied, got %v", err)
	}
}
