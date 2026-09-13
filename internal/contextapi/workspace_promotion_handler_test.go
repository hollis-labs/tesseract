package contextapi

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/hollis-labs/tesseract/internal/memory"
	"github.com/hollis-labs/tesseract/internal/workspace"
)

func TestWorkspacePromotionHTTPStagesAndWireShape(t *testing.T) {
	srv := newWorkspaceTestServer(t)
	source, err := srv.WorkspaceStore.Create(context.Background(), workspace.CreateInput{
		Namespace: httpWorkspaceNS, Key: "promotion-source", Summary: "promoted over HTTP", Data: []byte(`{"large":90071992547409931234}`),
		Author: memory.Author{AgentID: "draft"}, SessionID: "draft-session",
	})
	if err != nil {
		t.Fatal(err)
	}
	requested := performJSON(t, srv, http.MethodPost, "/v1/workspace/promote/request", map[string]any{
		"source_item_id": source.ItemID, "source_version_token": source.VersionToken, "actor": "requester", "reason": "reviewed",
		"target": map[string]any{"domain": "memory", "namespace": "user/chrispian/memory/notes", "key": "http.promoted",
			"author": map[string]any{"agent_id": "reviewer"}, "session_id": "review-session", "trigger": "promotion", "derived_from": "project"},
	})
	if requested.Code != http.StatusOK {
		t.Fatalf("request status=%d body=%s", requested.Code, requested.Body.String())
	}
	requestID, _ := decodeHTTPJSON(t, requested.Body.Bytes())["request_id"].(string)
	approved := performJSON(t, srv, http.MethodPost, "/v1/workspace/promote/approve", map[string]any{"request_id": requestID, "actor": "approver", "notes": "ok"})
	if approved.Code != http.StatusOK {
		t.Fatalf("approve status=%d body=%s", approved.Code, approved.Body.String())
	}
	applied := performJSON(t, srv, http.MethodPost, "/v1/workspace/promote/apply", map[string]any{"request_id": requestID, "actor": "applier"})
	if applied.Code != http.StatusOK {
		t.Fatalf("apply status=%d body=%s", applied.Code, applied.Body.String())
	}
	body := decodeHTTPJSON(t, applied.Body.Bytes())
	if body["target_item_id"] == "" || body["target_revision_id"] == "" || body["status"] != "applied" || body["source_item_id"] != source.ItemID || body["source_version_token"] != source.VersionToken {
		t.Fatalf("apply receipt=%v", body)
	}
	replayed := performJSON(t, srv, http.MethodPost, "/v1/workspace/promote/apply", map[string]any{"request_id": requestID, "actor": "retry"})
	if replayed.Code != http.StatusOK || decodeHTTPJSON(t, replayed.Body.Bytes())["target_revision_id"] != body["target_revision_id"] {
		t.Fatalf("replay=%s", replayed.Body.String())
	}
}

func TestWorkspacePromotionHTTPChecksOnlyNamedStageScopeAndBothNamespaceGrants(t *testing.T) {
	srv := newWorkspaceTestServer(t)
	source, err := srv.WorkspaceStore.Create(context.Background(), workspace.CreateInput{Namespace: httpWorkspaceNS, Key: "authorized-source", Summary: "content", Author: memory.Author{AgentID: "draft"}, SessionID: "draft"})
	if err != nil {
		t.Fatal(err)
	}
	requestBody := map[string]any{"source_item_id": source.ItemID, "source_version_token": source.VersionToken, "actor": "requester", "target": map[string]any{
		"domain": "memory", "namespace": "user/chrispian/memory/notes", "key": "authorized.target", "author": map[string]any{"agent_id": "reviewer"}, "session_id": "review", "trigger": "promotion", "derived_from": "project"}}
	requestOnly := issueTokenWithScopes(t, srv, "request-only", []string{"promote.request"}, []string{"project/tesseract/workspace/*", "user/chrispian/memory/*"})
	wrongScope := issueTokenWithScopes(t, srv, "apply-only", []string{"promote.apply"}, []string{"*"})
	sourceOnly := issueTokenWithScopes(t, srv, "source-only", []string{"promote.request"}, []string{"project/tesseract/workspace/*"})
	srv.ManagedAuth = true
	deniedScope := performJSONWithHeaders(t, srv, http.MethodPost, "/v1/workspace/promote/request", requestBody, map[string]string{"Authorization": "Bearer " + wrongScope})
	if deniedScope.Code != http.StatusForbidden || decodeHTTPJSON(t, deniedScope.Body.Bytes())["code"] != "insufficient_scope" {
		t.Fatalf("scope response=%s", deniedScope.Body.String())
	}
	deniedTarget := performJSONWithHeaders(t, srv, http.MethodPost, "/v1/workspace/promote/request", requestBody, map[string]string{"Authorization": "Bearer " + sourceOnly})
	if deniedTarget.Code != http.StatusForbidden || decodeHTTPJSON(t, deniedTarget.Body.Bytes())["code"] != "namespace_not_permitted" {
		t.Fatalf("grant response=%s", deniedTarget.Body.String())
	}
	if strings.Contains(deniedTarget.Body.String(), "user/chrispian") {
		t.Fatalf("authorization failure disclosed retained target namespace: %s", deniedTarget.Body.String())
	}
	allowed := performJSONWithHeaders(t, srv, http.MethodPost, "/v1/workspace/promote/request", requestBody, map[string]string{"Authorization": "Bearer " + requestOnly})
	if allowed.Code != http.StatusOK {
		t.Fatalf("allowed status=%d body=%s", allowed.Code, allowed.Body.String())
	}
	boundaryBody := map[string]any{"source_item_id": source.ItemID, "source_version_token": source.VersionToken, "actor": "requester", "target": map[string]any{
		"domain": "memory", "namespace": "user/chrispian/memoryevil/notes", "key": "boundary.target", "author": map[string]any{"agent_id": "reviewer"}, "session_id": "review", "trigger": "promotion", "derived_from": "project"}}
	boundary := performJSONWithHeaders(t, srv, http.MethodPost, "/v1/workspace/promote/request", boundaryBody, map[string]string{"Authorization": "Bearer " + requestOnly})
	if boundary.Code != http.StatusForbidden || decodeHTTPJSON(t, boundary.Body.Bytes())["code"] != "namespace_not_permitted" {
		t.Fatalf("literal namespace boundary response=%s", boundary.Body.String())
	}
}

func TestWorkspacePromotionHTTPApproveApplyScopeAndReceiptAuthorization(t *testing.T) {
	srv := newWorkspaceTestServer(t)
	source, err := srv.WorkspaceStore.Create(context.Background(), workspace.CreateInput{Namespace: httpWorkspaceNS, Key: "receipt-auth-source", Summary: "content", Author: memory.Author{AgentID: "draft"}, SessionID: "draft"})
	if err != nil {
		t.Fatal(err)
	}
	request := performJSON(t, srv, http.MethodPost, "/v1/workspace/promote/request", map[string]any{"source_item_id": source.ItemID, "source_version_token": source.VersionToken, "actor": "requester", "target": map[string]any{
		"domain": "memory", "namespace": "user/chrispian/memory/notes", "key": "receipt.authorization", "author": map[string]any{"agent_id": "reviewer"}, "session_id": "review", "trigger": "promotion", "derived_from": "project"}})
	if request.Code != http.StatusOK {
		t.Fatalf("request=%s", request.Body.String())
	}
	requestID := decodeHTTPJSON(t, request.Body.Bytes())["request_id"].(string)
	approveToken := issueTokenWithScopes(t, srv, "approve", []string{"promote.approve"}, []string{"project/tesseract/workspace/*", "user/chrispian/memory/*"})
	applyToken := issueTokenWithScopes(t, srv, "apply", []string{"promote.apply"}, []string{"project/tesseract/workspace/*", "user/chrispian/memory/*"})
	deniedNamespaceToken := issueTokenWithScopes(t, srv, "apply-denied-namespace", []string{"promote.apply"}, []string{"project/other/*"})
	srv.ManagedAuth = true
	approved := performJSONWithHeaders(t, srv, http.MethodPost, "/v1/workspace/promote/approve", map[string]any{"request_id": requestID, "actor": "approver"}, map[string]string{"Authorization": "Bearer " + approveToken})
	if approved.Code != http.StatusOK {
		t.Fatalf("approve=%s", approved.Body.String())
	}
	wrongStage := performJSONWithHeaders(t, srv, http.MethodPost, "/v1/workspace/promote/apply", map[string]any{"request_id": requestID, "actor": "applier"}, map[string]string{"Authorization": "Bearer " + approveToken})
	if wrongStage.Code != http.StatusForbidden || decodeHTTPJSON(t, wrongStage.Body.Bytes())["code"] != "insufficient_scope" {
		t.Fatalf("wrong stage scope=%s", wrongStage.Body.String())
	}
	deniedReceipt := performJSONWithHeaders(t, srv, http.MethodPost, "/v1/workspace/promote/apply", map[string]any{"request_id": requestID, "actor": "applier"}, map[string]string{"Authorization": "Bearer " + deniedNamespaceToken})
	if deniedReceipt.Code != http.StatusForbidden || decodeHTTPJSON(t, deniedReceipt.Body.Bytes())["code"] != "namespace_not_permitted" {
		t.Fatalf("receipt authorization=%s", deniedReceipt.Body.String())
	}
	for _, secret := range []string{httpWorkspaceNS, "user/chrispian/memory/notes", requestID} {
		if strings.Contains(deniedReceipt.Body.String(), secret) {
			t.Fatalf("denied receipt disclosed %q: %s", secret, deniedReceipt.Body.String())
		}
	}
	applied := performJSONWithHeaders(t, srv, http.MethodPost, "/v1/workspace/promote/apply", map[string]any{"request_id": requestID, "actor": "applier"}, map[string]string{"Authorization": "Bearer " + applyToken})
	if applied.Code != http.StatusOK {
		t.Fatalf("apply=%s", applied.Body.String())
	}
}

func TestWorkspacePromotionHTTPRejectsNullAndRedundantExistingSelectorFields(t *testing.T) {
	srv := newWorkspaceTestServer(t)
	null := performJSON(t, srv, http.MethodPost, "/v1/workspace/promote/request", map[string]any{"source_item_id": nil})
	if null.Code != http.StatusBadRequest || decodeHTTPJSON(t, null.Body.Bytes())["code"] != "validation_error" {
		t.Fatalf("null=%s", null.Body.String())
	}
	source, err := srv.WorkspaceStore.Create(context.Background(), workspace.CreateInput{Namespace: httpWorkspaceNS, Key: "redundant-source", Summary: "content", Author: memory.Author{AgentID: "draft"}, SessionID: "draft"})
	if err != nil {
		t.Fatal(err)
	}
	target, err := srv.MemoryStore.WriteRevision(context.Background(), memory.WriteInput{Domain: "memory", Namespace: "user/chrispian/memory/notes", MemoryKey: "existing.http", Status: memory.StatusDraft, Author: memory.Author{AgentID: "old"}, Trigger: memory.TriggerExplicit, SessionID: "old", DerivedFrom: memory.DerivedFromProject, Summary: "old"})
	if err != nil {
		t.Fatal(err)
	}
	redundant := performJSON(t, srv, http.MethodPost, "/v1/workspace/promote/request", map[string]any{"source_item_id": source.ItemID, "source_version_token": source.VersionToken, "actor": "requester", "target": map[string]any{
		"item_id": target.ItemID, "expected_revision_id": target.RevisionID, "domain": "", "author": map[string]any{"agent_id": "reviewer"}, "session_id": "review", "trigger": "promotion", "derived_from": "project"}})
	if redundant.Code != http.StatusBadRequest || decodeHTTPJSON(t, redundant.Body.Bytes())["code"] != "validation_error" {
		t.Fatalf("redundant=%s", redundant.Body.String())
	}
	unknown := performJSON(t, srv, http.MethodPost, "/v1/workspace/promote/request", map[string]any{"source_item_id": source.ItemID, "source_version_token": source.VersionToken, "actor": "requester", "target": map[string]any{
		"domain": "memory", "namespace": "user/chrispian/memory/notes", "author": map[string]any{"agent_id": "reviewer"}, "session_id": "review", "trigger": "promotion", "derived_from": "project", "content_override": "forbidden"}})
	if unknown.Code != http.StatusBadRequest || decodeHTTPJSON(t, unknown.Body.Bytes())["code"] != "validation_error" {
		t.Fatalf("unknown nested field=%s", unknown.Body.String())
	}
}
