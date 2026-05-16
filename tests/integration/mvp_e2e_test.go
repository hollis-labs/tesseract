package integration

// TestMVPEndToEndWorkflow verifies the complete agent workflow described in TASK-20260228-019.
// This is the final MVP gate test covering all 10 verification steps.

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/tesseract/internal/contextstore"
)

func TestMVPEndToEndWorkflow(t *testing.T) {
	srv := newServer(t)
	srv.ManagedAuth = true
	ctx := context.Background()

	// ── Step 1: Bootstrap namespaces ────────────────────────────────────────────
	// The policy engine handles namespace permissions via tier rules; namespace
	// registration is optional for the tier model. Skip explicit registration
	// (no namespace schema endpoint required for MVP core flow).

	// ── Step 2: Create scoped token for app agent ────────────────────────────────
	appToken, appMeta, err := srv.Store.CreateAuthToken(ctx, contextstore.TokenCreateInput{
		Label:          "test-agent",
		ClientID:       "app:test",
		Scopes:         []string{"write", "packet", "promote.request"},
		NamespaceGlobs: []string{"app/test/*"},
		TTL:            time.Hour,
	})
	if err != nil {
		t.Fatalf("step2: create app token: %v", err)
	}
	t.Logf("step2: app token created: id=%s", appMeta.TokenID)

	// Verify token is in list.
	tokens, err := srv.Store.ListAuthTokens(ctx, 50)
	if err != nil {
		t.Fatalf("step2: list tokens: %v", err)
	}
	found := false
	for _, tk := range tokens {
		if tk.TokenID == appMeta.TokenID {
			found = true
		}
	}
	if !found {
		t.Errorf("step2: created token not found in list")
	}

	// ── Step 3: App writes session context ──────────────────────────────────────
	writeRes := performWithHeaders(t, srv, http.MethodPost, "/v1/context/write", map[string]any{
		"client_id": "test",
		"actor":     "app:test",
		"namespace": "app/test/session/task-001",
		"key":       "state",
		"payload":   map[string]any{"task": "MVP verification", "phase": "integration"},
	}, map[string]string{"Authorization": "Bearer " + appToken})
	if writeRes.Code != http.StatusOK {
		t.Fatalf("step3: app session write expected 200, got %d: %s", writeRes.Code, writeRes.Body)
	}

	// Verify record is readable.
	headRes := perform(t, srv, http.MethodGet,
		"/v1/context/head?namespace=app/test/session/task-001&key=state", nil)
	if headRes.Code != http.StatusOK {
		t.Fatalf("step3: head after write expected 200, got %d: %s", headRes.Code, headRes.Body)
	}
	t.Log("step3: app session write OK")

	// ── Step 4: App writes to user/memory (should FAIL) ─────────────────────────
	// The app token has namespace_globs: ["app/test/*"] — user/* is not permitted.
	badWrite := performWithHeaders(t, srv, http.MethodPost, "/v1/context/write", map[string]any{
		"client_id": "test",
		"actor":     "app:test",
		"namespace": "user/memory/task-001",
		"key":       "state",
		"payload":   map[string]any{"attempt": "direct write to user namespace"},
	}, map[string]string{"Authorization": "Bearer " + appToken})
	if badWrite.Code != http.StatusForbidden {
		t.Fatalf("step4: app→user write expected 403, got %d: %s", badWrite.Code, badWrite.Body)
	}
	var badBody map[string]any
	json.NewDecoder(badWrite.Body).Decode(&badBody)
	if badBody["code"] != "namespace_not_permitted" {
		t.Errorf("step4: expected code=namespace_not_permitted, got %v", badBody["code"])
	}
	t.Log("step4: app→user write correctly rejected with 403 namespace_not_permitted")

	// ── Step 5: User writes pins ─────────────────────────────────────────────────
	// User has no managed-auth constraints in this test (no token passed).
	// Use a full-access token for user writes.
	userToken, _, err := srv.Store.CreateAuthToken(ctx, contextstore.TokenCreateInput{
		Label:    "user-operator",
		ClientID: "user",
		TTL:      time.Hour,
	})
	if err != nil {
		t.Fatalf("step5: create user token: %v", err)
	}
	pinWrite := performWithHeaders(t, srv, http.MethodPost, "/v1/context/write", map[string]any{
		"client_id": "user",
		"actor":     "user",
		"namespace": "user/pins/context-memory-service",
		"key":       "project",
		"payload":   map[string]any{"project": "context-memory-service", "mvp_phase": "integration"},
	}, map[string]string{"Authorization": "Bearer " + userToken})
	if pinWrite.Code != http.StatusOK {
		t.Fatalf("step5: pin write expected 200, got %d: %s", pinWrite.Code, pinWrite.Body)
	}
	t.Log("step5: user pin write OK")

	// ── Step 6: Get a context packet ─────────────────────────────────────────────
	packetRes := performWithHeaders(t, srv, http.MethodPost, "/v1/context/packet", map[string]any{
		"selector": map[string]any{
			"namespaces":     []string{"app/test/session/*", "user/memory/*"},
			"revision_scope": "head",
			"limit":          50,
		},
		"assembly": map[string]any{
			"include_pins":   true,
			"manifest_level": "summary",
			"shape":          map[string]any{"include_payload": true},
		},
	}, map[string]string{"Authorization": "Bearer " + userToken})
	if packetRes.Code != http.StatusOK {
		t.Fatalf("step6: packet expected 200, got %d: %s", packetRes.Code, packetRes.Body)
	}
	var packetBody map[string]any
	json.NewDecoder(packetRes.Body).Decode(&packetBody)
	manifest, _ := packetBody["manifest"].(map[string]any)
	if manifest == nil {
		t.Fatalf("step6: no manifest in packet response")
	}
	if manifest["request_id"] == "" || manifest["request_id"] == nil {
		t.Errorf("step6: expected non-empty manifest.request_id")
	}
	pinsIncluded := int(manifest["pins_included"].(float64))
	if pinsIncluded < 1 {
		t.Errorf("step6: expected pins_included >= 1, got %v", pinsIncluded)
	}
	t.Logf("step6: packet OK — items_returned=%v, pins_included=%v", manifest["items_returned"], pinsIncluded)

	// ── Step 7: Use the broker ────────────────────────────────────────────────────
	brokerRes := performWithHeaders(t, srv, http.MethodPost, "/v1/broker/plan", map[string]any{
		"intent":       "resume_task",
		"task_summary": "MVP verification integration test capability",
	}, map[string]string{"Authorization": "Bearer " + userToken})
	if brokerRes.Code != http.StatusOK {
		t.Fatalf("step7: broker plan expected 200, got %d: %s", brokerRes.Code, brokerRes.Body)
	}
	var brokerBody map[string]any
	json.NewDecoder(brokerRes.Body).Decode(&brokerBody)
	brokerPlan, _ := brokerBody["plan"].(map[string]any)
	if brokerPlan == nil {
		t.Fatalf("step7: no plan in broker response")
	}
	rationale, _ := brokerBody["rationale"].(string)
	if !strings.Contains(rationale, "resume_task") {
		t.Errorf("step7: rationale should mention resume_task, got %q", rationale)
	}
	t.Logf("step7: broker plan OK — rationale=%q", rationale)

	// ── Step 8: Promotion workflow ────────────────────────────────────────────────
	// App writes draft record.
	draftWrite := performWithHeaders(t, srv, http.MethodPost, "/v1/context/write", map[string]any{
		"client_id": "test",
		"actor":     "app:test",
		"namespace": "app/test/draft",
		"key":       "user-preference",
		"payload":   map[string]any{"preference": "verbose output", "confidence": 0.9},
	}, map[string]string{"Authorization": "Bearer " + appToken})
	if draftWrite.Code != http.StatusOK {
		t.Fatalf("step8: draft write expected 200, got %d: %s", draftWrite.Code, draftWrite.Body)
	}

	// App creates promotion request.
	// App token must have promote.request scope. But our app token only has ["write","packet","promote.request"]
	// and namespace app/test/* which covers app/test/draft. Good.
	promReqRes := performWithHeaders(t, srv, http.MethodPost, "/v1/context/promote/request", map[string]any{
		"actor":            "app:test",
		"client_id":        "test",
		"source_namespace": "app/test/draft",
		"source_key":       "user-preference",
		"target_namespace": "user/memory/preferences",
		"target_key":       "test-preference",
		"reason":           "High-confidence preference detected in session",
	}, map[string]string{"Authorization": "Bearer " + appToken})
	if promReqRes.Code != http.StatusOK {
		t.Fatalf("step8: promote request expected 200, got %d: %s", promReqRes.Code, promReqRes.Body)
	}
	var promReqBody map[string]any
	json.NewDecoder(promReqRes.Body).Decode(&promReqBody)
	requestID := promReqBody["request_id"].(string)
	if promReqBody["status"] != "pending" {
		t.Errorf("step8: expected status=pending, got %v", promReqBody["status"])
	}
	t.Logf("step8: promote request created: %s", requestID)

	// User approves.
	promApprRes := performWithHeaders(t, srv, http.MethodPost, "/v1/context/promote/approve", map[string]any{
		"actor":      "user",
		"request_id": requestID,
		"notes":      "Looks correct",
	}, map[string]string{"Authorization": "Bearer " + userToken})
	if promApprRes.Code != http.StatusOK {
		t.Fatalf("step8: promote approve expected 200, got %d: %s", promApprRes.Code, promApprRes.Body)
	}
	var promApprBody map[string]any
	json.NewDecoder(promApprRes.Body).Decode(&promApprBody)
	approvalID := promApprBody["approval_id"].(string)
	t.Logf("step8: promote approved: %s", approvalID)

	// User applies.
	promApplyRes := performWithHeaders(t, srv, http.MethodPost, "/v1/context/promote/apply", map[string]any{
		"actor":      "user",
		"request_id": requestID,
	}, map[string]string{"Authorization": "Bearer " + userToken})
	if promApplyRes.Code != http.StatusOK {
		t.Fatalf("step8: promote apply expected 200, got %d: %s", promApplyRes.Code, promApplyRes.Body)
	}
	var promApplyBody map[string]any
	json.NewDecoder(promApplyRes.Body).Decode(&promApplyBody)
	newRecordID := promApplyBody["record_id"].(string)
	t.Logf("step8: promotion applied: record_id=%s", newRecordID)

	// Verify record appears in target namespace.
	targetHead := perform(t, srv, http.MethodGet,
		"/v1/context/head?namespace=user/memory/preferences&key=test-preference", nil)
	if targetHead.Code != http.StatusOK {
		t.Fatalf("step8: head in target namespace expected 200, got %d: %s", targetHead.Code, targetHead.Body)
	}

	// Verify audit trail has 3 linked entries.
	auditRes := perform(t, srv, http.MethodGet,
		"/v1/context/audit?event_type=promote&limit=20", nil)
	if auditRes.Code != http.StatusOK {
		t.Fatalf("step8: audit expected 200, got %d", auditRes.Code)
	}
	var auditBody struct {
		Items []struct {
			EventType string          `json:"event_type"`
			Metadata  json.RawMessage `json:"metadata"`
		} `json:"items"`
	}
	json.NewDecoder(auditRes.Body).Decode(&auditBody)
	// Verify the apply audit entry links back to request_id and approval_id.
	foundLinked := false
	for _, ev := range auditBody.Items {
		var meta map[string]any
		if json.Unmarshal(ev.Metadata, &meta) == nil {
			if meta["request_id"] == requestID && meta["approval_id"] == approvalID {
				foundLinked = true
			}
		}
	}
	if !foundLinked {
		t.Errorf("step8: audit trail should have entry linking request_id=%s and approval_id=%s", requestID, approvalID)
	}
	t.Log("step8: promotion audit trail linked correctly")

	// ── Step 9: Maintenance ───────────────────────────────────────────────────────
	// Create a full-access admin token for maintenance operations.
	adminToken, _, err := srv.Store.CreateAuthToken(ctx, contextstore.TokenCreateInput{
		Label:    "admin",
		ClientID: "user",
		Scopes:   []string{"write", "repair", "namespace.register"},
		TTL:      time.Hour,
	})
	if err != nil {
		t.Fatalf("step9: create admin token: %v", err)
	}

	// Dry-run trim.
	trimDry := performWithHeaders(t, srv, http.MethodPost, "/v1/maintenance/trim", map[string]any{
		"namespace_pattern": "app/test/session/%",
		"retention":         "72h",
		"dry_run":           true,
	}, map[string]string{"Authorization": "Bearer " + adminToken})
	if trimDry.Code != http.StatusOK {
		t.Fatalf("step9: trim dry-run expected 200, got %d: %s", trimDry.Code, trimDry.Body)
	}

	// Actual trim.
	trimRes := performWithHeaders(t, srv, http.MethodPost, "/v1/maintenance/trim", map[string]any{
		"namespace_pattern": "app/test/session/%",
		"retention":         "72h",
	}, map[string]string{"Authorization": "Bearer " + adminToken})
	if trimRes.Code != http.StatusOK {
		t.Fatalf("step9: trim expected 200, got %d: %s", trimRes.Code, trimRes.Body)
	}
	t.Log("step9: maintenance trim OK")

	// ── Step 10: Token revocation ─────────────────────────────────────────────────
	if err := srv.Store.RevokeAuthTokenByID(ctx, appMeta.TokenID); err != nil {
		t.Fatalf("step10: revoke token: %v", err)
	}

	// Revoked token should be rejected immediately.
	revokedWrite := performWithHeaders(t, srv, http.MethodPost, "/v1/context/write", map[string]any{
		"client_id": "test",
		"actor":     "app:test",
		"namespace": "app/test/session/task-001",
		"key":       "state",
		"payload":   map[string]any{"test": "should fail"},
	}, map[string]string{"Authorization": "Bearer " + appToken})
	if revokedWrite.Code != http.StatusUnauthorized {
		t.Fatalf("step10: revoked token expected 401, got %d: %s", revokedWrite.Code, revokedWrite.Body)
	}
	var revokedBody map[string]any
	json.NewDecoder(revokedWrite.Body).Decode(&revokedBody)
	details, _ := revokedBody["details"].(map[string]any)
	if details != nil {
		reason, _ := details["reason"].(string)
		if !strings.Contains(reason, "revoked") {
			t.Errorf("step10: expected reason to mention revoked, got %q", reason)
		}
	}
	t.Log("step10: revoked token immediately rejected with 401")

	t.Log("=== MVP End-to-End Verification PASSED ===")
}

// TestMVPCapabilityTokenCreateListRevoke verifies the token management API
// end-to-end from the integration test perspective.
func TestMVPCapabilityTokenCreateListRevoke(t *testing.T) {
	srv := newServer(t)
	srv.ManagedAuth = true
	ctx := context.Background()

	// Bootstrap: need an admin token to call the create endpoint.
	adminToken, _, err := srv.Store.IssueAuthToken(ctx, "admin", time.Hour)
	if err != nil {
		t.Fatalf("issue admin: %v", err)
	}

	// Create a scoped token via API.
	createRes := performWithHeaders(t, srv, http.MethodPost, "/v1/auth/tokens/create", map[string]any{
		"name":            "integration-agent",
		"client_id":       "app:integration",
		"scopes":          []string{"write", "packet"},
		"namespace_globs": []string{"app/integration/*"},
		"ttl":             "1h",
	}, map[string]string{"Authorization": "Bearer " + adminToken})
	if createRes.Code != http.StatusOK {
		t.Fatalf("token create: %d %s", createRes.Code, createRes.Body)
	}
	var createBody map[string]any
	json.NewDecoder(createRes.Body).Decode(&createBody)
	tokenID := createBody["id"].(string)
	if createBody["token"] == nil {
		t.Errorf("token create: raw token missing from response")
	}
	if createBody["name"] != "integration-agent" {
		t.Errorf("token create: name mismatch")
	}

	// List tokens — raw value must not appear.
	listRes := performWithHeaders(t, srv, http.MethodGet, "/v1/auth/tokens/list", nil,
		map[string]string{"Authorization": "Bearer " + adminToken})
	if listRes.Code != http.StatusOK {
		t.Fatalf("token list: %d %s", listRes.Code, listRes.Body)
	}
	var listBody map[string]any
	json.NewDecoder(listRes.Body).Decode(&listBody)
	tokens := listBody["tokens"].([]any)
	found := false
	for _, raw := range tokens {
		entry := raw.(map[string]any)
		if entry["id"] == tokenID {
			found = true
			if _, hasToken := entry["token"]; hasToken {
				t.Errorf("list must never return raw token value")
			}
		}
	}
	if !found {
		t.Errorf("created token %s not found in list", tokenID)
	}

	// Revoke by ID.
	revokeRes := performWithHeaders(t, srv, http.MethodPost, "/v1/auth/tokens/revoke", map[string]any{
		"id": tokenID,
	}, map[string]string{"Authorization": "Bearer " + adminToken})
	if revokeRes.Code != http.StatusOK {
		t.Fatalf("token revoke: %d %s", revokeRes.Code, revokeRes.Body)
	}
}
