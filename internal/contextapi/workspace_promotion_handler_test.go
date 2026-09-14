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

func TestWorkspacePromotionHTTPRejectsNoncanonicalFieldNamesAtEveryLevel(t *testing.T) {
	srv := newWorkspaceTestServer(t)
	ctx := context.Background()
	source, err := srv.WorkspaceStore.Create(ctx, workspace.CreateInput{Namespace: httpWorkspaceNS, Key: "canonical-fields-source", Summary: "content", Author: memory.Author{AgentID: "draft"}, SessionID: "draft"})
	if err != nil {
		t.Fatal(err)
	}
	target, err := srv.MemoryStore.WriteRevision(ctx, memory.WriteInput{Domain: "memory", Namespace: "user/chrispian/memory/notes", MemoryKey: "canonical.fields.target", Author: memory.Author{AgentID: "old"}, SessionID: "old", Summary: "old", Trigger: memory.TriggerExplicit, DerivedFrom: memory.DerivedFromProject})
	if err != nil {
		t.Fatal(err)
	}
	canonicalTarget := func() map[string]any {
		return map[string]any{"item_id": target.ItemID, "expected_revision_id": target.RevisionID, "author": map[string]any{"agent_id": "codex"}, "session_id": "review", "trigger": "promotion", "derived_from": "project"}
	}
	tests := []struct {
		name string
		body map[string]any
	}{
		{"root", map[string]any{"source_item_id": source.ItemID, "source_version_token": source.VersionToken, "Actor": "agent", "target": canonicalTarget()}},
		{"target", func() map[string]any {
			targetSpec := canonicalTarget()
			targetSpec["Domain"] = ""
			return map[string]any{"source_item_id": source.ItemID, "source_version_token": source.VersionToken, "actor": "agent", "target": targetSpec}
		}()},
		{"author_object", func() map[string]any {
			targetSpec := canonicalTarget()
			delete(targetSpec, "author")
			targetSpec["Author"] = map[string]any{"agent_id": "codex", "agent_version": nil}
			return map[string]any{"source_item_id": source.ItemID, "source_version_token": source.VersionToken, "actor": "agent", "target": targetSpec}
		}()},
		{"author_field", func() map[string]any {
			targetSpec := canonicalTarget()
			targetSpec["author"] = map[string]any{"agent_id": "codex", "Agent_Version": "1"}
			return map[string]any{"source_item_id": source.ItemID, "source_version_token": source.VersionToken, "actor": "agent", "target": targetSpec}
		}()},
		{"author_null", func() map[string]any {
			targetSpec := canonicalTarget()
			targetSpec["author"] = map[string]any{"agent_id": "codex", "agent_version": nil}
			return map[string]any{"source_item_id": source.ItemID, "source_version_token": source.VersionToken, "actor": "agent", "target": targetSpec}
		}()},
		{"pointer_field", map[string]any{"source_item_id": source.ItemID, "source_version_token": source.VersionToken, "actor": "agent", "target": map[string]any{"domain": "knowledge", "namespace": "project/tesseract/knowledge/contracts", "author": map[string]any{"agent_id": "codex"}, "session_id": "review", "kind": "doc", "source": "manual", "pointer": map[string]any{"Scheme": "nil", "locator": "x"}}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := performJSON(t, srv, http.MethodPost, "/v1/workspace/promote/request", test.body)
			if response.Code != http.StatusBadRequest || decodeHTTPJSON(t, response.Body.Bytes())["code"] != "validation_error" {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
	for _, stage := range []struct {
		path string
		body map[string]any
	}{
		{"/v1/workspace/promote/approve", map[string]any{"request_id": "request", "actor": "agent", "Notes": "reviewed"}},
		{"/v1/workspace/promote/apply", map[string]any{"request_id": "request", "Actor": "agent"}},
	} {
		response := performJSON(t, srv, http.MethodPost, stage.path, stage.body)
		if response.Code != http.StatusBadRequest || decodeHTTPJSON(t, response.Body.Bytes())["code"] != "validation_error" {
			t.Fatalf("path=%s status=%d body=%s", stage.path, response.Code, response.Body.String())
		}
	}
}

func TestWorkspacePromotionHTTPAuthorizesExistingTargetBeforeDomainCheck(t *testing.T) {
	srv := newWorkspaceTestServer(t)
	ctx := context.Background()
	source, err := srv.WorkspaceStore.Create(ctx, workspace.CreateInput{Namespace: httpWorkspaceNS, Key: "target-auth-source", Summary: "source", Author: memory.Author{AgentID: "draft"}, SessionID: "draft"})
	if err != nil {
		t.Fatal(err)
	}
	forbiddenTarget, err := srv.WorkspaceStore.Create(ctx, workspace.CreateInput{Namespace: "project/other/workspace/private", Key: "secret", Summary: "secret", Author: memory.Author{AgentID: "other"}, SessionID: "other"})
	if err != nil {
		t.Fatal(err)
	}
	token := issueTokenWithScopes(t, srv, "target-domain-denial", []string{"promote.request"}, []string{"project/tesseract/workspace/*"})
	srv.ManagedAuth = true
	response := performJSONWithHeaders(t, srv, http.MethodPost, "/v1/workspace/promote/request", map[string]any{
		"source_item_id": source.ItemID, "source_version_token": source.VersionToken, "actor": "agent",
		"target": map[string]any{"item_id": forbiddenTarget.ItemID, "expected_revision_id": "irrelevant", "author": map[string]any{"agent_id": "codex"}, "session_id": "review"},
	}, map[string]string{"Authorization": "Bearer " + token})
	if response.Code != http.StatusForbidden || decodeHTTPJSON(t, response.Body.Bytes())["code"] != "namespace_not_permitted" {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), forbiddenTarget.Namespace) || strings.Contains(response.Body.String(), "revisioned") {
		t.Fatalf("authorization denial disclosed target facts: %s", response.Body.String())
	}
	authorized := performJSONWithHeaders(t, srv, http.MethodPost, "/v1/workspace/promote/request", map[string]any{
		"source_item_id": source.ItemID, "source_version_token": source.VersionToken, "actor": "agent",
		"target": map[string]any{"item_id": source.ItemID, "expected_revision_id": "irrelevant", "author": map[string]any{"agent_id": "codex"}, "session_id": "review"},
	}, map[string]string{"Authorization": "Bearer " + token})
	if authorized.Code != http.StatusBadRequest || decodeHTTPJSON(t, authorized.Body.Bytes())["code"] != "validation_error" {
		t.Fatalf("authorized ineligible target status=%d body=%s", authorized.Code, authorized.Body.String())
	}
}
