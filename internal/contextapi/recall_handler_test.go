package contextapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hollis-labs/vanta-conduit/internal/knowledge"
	"github.com/hollis-labs/vanta-conduit/internal/memory"
)

// newRecallServer builds a test server with both memory and knowledge stores
// wired, matching production wiring in cmd/contextd.
func newRecallServer(t *testing.T) *Server {
	t.Helper()
	srv := newMemoryTestServer(t)
	srv.KnowledgeStore = knowledge.New(srv.MemoryStore)
	return srv
}

func seedMemoryWithTags(t *testing.T, srv *Server, ns, key, summary string, tags []string) {
	t.Helper()
	_, err := srv.MemoryStore.WriteRevision(context.Background(), memory.WriteInput{
		Namespace:  ns,
		MemoryKey:  key,
		Author:     memory.Author{AgentID: "test", AgentVersion: "1.0"},
		Trigger:    memory.TriggerExplicit,
		SessionID:  "manual:01HX",
		Origin:     memory.OriginUser,
		Confidence: 0.9,
		Status:     memory.StatusCanonical,
		Tags:       tags,
		Payload:    memory.Payload{Summary: summary},
	})
	if err != nil {
		t.Fatalf("seedMemoryWithTags: %v", err)
	}
}

func seedKnowledgeWithTags(t *testing.T, srv *Server, ns, key, summary string, tags []string) {
	t.Helper()
	_, err := srv.KnowledgeStore.Write(context.Background(), knowledge.WriteInput{
		Namespace: ns,
		Key:       key,
		Kind:      "session-close",
		Source:    "agent",
		Pointer:   memory.Pointer{Scheme: "nil", Locator: "session-close"},
		Summary:   summary,
		Author:    memory.Author{AgentID: "nanite", AgentVersion: "1.0"},
		SessionID: "session:01HX",
		Tags:      tags,
	})
	if err != nil {
		t.Fatalf("seedKnowledgeWithTags: %v", err)
	}
}

// TestRecall_MissingNamespaceReturns400 validates the required-param guard.
func TestRecall_MissingNamespaceReturns400(t *testing.T) {
	srv := newRecallServer(t)
	req := httptest.NewRequest(http.MethodGet, "/v1/recall", nil)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
}

// TestRecall_NoStoreReturns503 verifies that missing MemoryStore gives 503.
func TestRecall_NoStoreReturns503(t *testing.T) {
	srv := newRecallServer(t)
	srv.MemoryStore = nil
	req := httptest.NewRequest(http.MethodGet, "/v1/recall?namespace=user/chrispian/memory", nil)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body=%s", rr.Code, rr.Body.String())
	}
}

// TestRecall_InvalidLimitReturns400 ensures non-integer limit is rejected.
func TestRecall_InvalidLimitReturns400(t *testing.T) {
	srv := newRecallServer(t)
	req := httptest.NewRequest(http.MethodGet, "/v1/recall?namespace=user/chrispian/memory&limit=abc", nil)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
}

// TestRecall_BriefFormat verifies format=brief returns condensed items.
func TestRecall_BriefFormat(t *testing.T) {
	srv := newRecallServer(t)
	seedMemoryWithTags(t, srv, "user/chrispian/memory", "prefs.terse", "terse output preferred",
		[]string{"scope:nanite.backend.main"})

	req := httptest.NewRequest(http.MethodGet,
		"/v1/recall?namespace=user/chrispian/memory&format=brief&limit=15", nil)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}

	var resp recallResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Meta.Format != "brief" {
		t.Errorf("meta.format = %q, want brief", resp.Meta.Format)
	}
	if resp.Meta.Namespace != "user/chrispian/memory" {
		t.Errorf("meta.namespace = %q", resp.Meta.Namespace)
	}
	if resp.Meta.Returned != 1 {
		t.Fatalf("meta.returned = %d, want 1", resp.Meta.Returned)
	}

	// Results are []recallBriefItem when format=brief; confirm shape via
	// round-trip through JSON since the field is typed as any.
	raw, _ := json.Marshal(resp.Results)
	var items []recallBriefItem
	if err := json.Unmarshal(raw, &items); err != nil {
		t.Fatalf("decode items: %v", err)
	}
	if items[0].Summary != "terse output preferred" {
		t.Errorf("summary = %q, want 'terse output preferred'", items[0].Summary)
	}
	if items[0].Domain != "memory" {
		t.Errorf("domain = %q, want memory", items[0].Domain)
	}
}

// TestRecall_FullFormat verifies format=full returns complete RecallResult.
func TestRecall_FullFormat(t *testing.T) {
	srv := newRecallServer(t)
	seedMemoryWithTags(t, srv, "user/chrispian/memory", "prefs.full", "full output",
		[]string{"scope:nanite.backend.main"})

	req := httptest.NewRequest(http.MethodGet,
		"/v1/recall?namespace=user/chrispian/memory&format=full&limit=5", nil)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}

	var resp recallResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Meta.Format != "full" {
		t.Errorf("meta.format = %q, want full", resp.Meta.Format)
	}
	if resp.Meta.Returned < 1 {
		t.Fatalf("meta.returned = %d, want >= 1", resp.Meta.Returned)
	}
}

// TestRecall_TagFilter verifies that tags param narrows results to matching
// records only (OR semantics, consistent with memory.RecallFilters.Tags).
func TestRecall_TagFilter(t *testing.T) {
	srv := newRecallServer(t)
	seedMemoryWithTags(t, srv, "user/chrispian/memory", "prefs.a", "tagged item",
		[]string{"scope:nanite.backend.main"})
	seedMemoryWithTags(t, srv, "user/chrispian/memory", "prefs.b", "untagged item", nil)

	req := httptest.NewRequest(http.MethodGet,
		"/v1/recall?namespace=user/chrispian/memory&tags=scope:nanite.backend.main&limit=10", nil)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}

	var resp recallResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Meta.Returned != 1 {
		t.Fatalf("meta.returned = %d, want 1 (tag filter should exclude untagged)", resp.Meta.Returned)
	}
	raw, _ := json.Marshal(resp.Results)
	var items []recallBriefItem
	_ = json.Unmarshal(raw, &items)
	if items[0].Summary != "tagged item" {
		t.Errorf("summary = %q, want 'tagged item'", items[0].Summary)
	}
}

// TestRecall_KnowledgeDomain verifies session-close records in the knowledge
// domain are accessible via GET /v1/recall.
func TestRecall_KnowledgeDomain(t *testing.T) {
	srv := newRecallServer(t)
	seedKnowledgeWithTags(t, srv,
		"user/chrispian/knowledge/session-close/nanite",
		"session-close.2026-04-21.01",
		"nanite session closed — backend main stable",
		[]string{"scope:nanite.backend.main"},
	)

	req := httptest.NewRequest(http.MethodGet,
		"/v1/recall?namespace=user/chrispian/knowledge/session-close/nanite&tags=scope:nanite.backend.main&limit=3&format=brief",
		nil)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}

	var resp recallResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Meta.Returned < 1 {
		t.Fatalf("meta.returned = %d, want >= 1 for knowledge namespace", resp.Meta.Returned)
	}
	if resp.Facets.Domains["knowledge"] < 1 {
		t.Errorf("facets.domains.knowledge = %d, want >= 1", resp.Facets.Domains["knowledge"])
	}
}

// TestRecall_LimitApplied confirms the limit param caps the returned set.
func TestRecall_LimitApplied(t *testing.T) {
	srv := newRecallServer(t)
	// Seed 5 items.
	keys := []string{"k1", "k2", "k3", "k4", "k5"}
	for _, k := range keys {
		seedMemoryWithTags(t, srv, "user/chrispian/memory", "prefs."+k, "item "+k, nil)
	}

	req := httptest.NewRequest(http.MethodGet,
		"/v1/recall?namespace=user/chrispian/memory&limit=3", nil)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}

	var resp recallResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Meta.Returned > 3 {
		t.Errorf("meta.returned = %d, want <= 3 (limit=3)", resp.Meta.Returned)
	}
}
