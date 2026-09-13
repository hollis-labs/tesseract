package contextapi

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/tesseract/domains"
	"github.com/hollis-labs/tesseract/internal/contextstore"
	"github.com/hollis-labs/tesseract/internal/event"
	"github.com/hollis-labs/tesseract/internal/knowledge"
	"github.com/hollis-labs/tesseract/internal/memory"
	"github.com/hollis-labs/tesseract/internal/workspace"
)

func referenceHTTPServer(t *testing.T) *Server {
	t.Helper()
	srv := newTestServer(t)
	ms := memory.NewStore(srv.Store.DB(), nil, "", 0, memory.NoopQueue{})
	srv.MemoryStore = ms
	srv.KnowledgeStore = knowledge.New(ms)
	srv.EventStore = event.New(ms)
	srv.WorkspaceStore = workspace.NewStore(srv.Store.DB())
	return srv
}

func TestReferenceResolveHTTPContract(t *testing.T) {
	srv := referenceHTTPServer(t)
	ctx := context.Background()
	rev, err := srv.MemoryStore.WriteRevision(ctx, memory.WriteInput{
		Domain: domains.Memory, Namespace: "project/tesseract/memory/notes", MemoryKey: "resolver.http",
		Summary: "must not leave the store", Author: memory.Author{AgentID: "test"}, SessionID: "resolver",
		Trigger: memory.TriggerManual, DerivedFrom: memory.DerivedFromProject,
	})
	if err != nil {
		t.Fatal(err)
	}
	item, err := srv.WorkspaceStore.Create(ctx, workspace.CreateInput{
		Namespace: "project/tesseract/workspace/http-resolver", Key: "path/with slash", Summary: "workspace secret",
		Author: memory.Author{AgentID: "test"}, SessionID: "resolver",
	})
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name             string
		body             map[string]any
		wantKind, wantID string
	}{
		{"item", map[string]any{"item_id": rev.ItemID}, "tesseract_item", rev.ItemID},
		{"revision", map[string]any{"revision_id": rev.RevisionID}, "tesseract_revision", rev.RevisionID},
		{"legacy", map[string]any{"domain": "workspace", "namespace": item.Namespace, "key": item.Key}, "tesseract_item", item.ItemID},
		{"uri", map[string]any{"uri": "tesseract://revision/" + rev.RevisionID}, "tesseract_revision", rev.RevisionID},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rr := performJSON(t, srv, http.MethodPost, "/v1/refs/resolve", tc.body)
			body := decodeHTTPJSON(t, rr.Body.Bytes())
			if rr.Code != http.StatusOK || body["status"] != "resolved" {
				t.Fatalf("status=%d body=%v", rr.Code, body)
			}
			ref, _ := body["ref"].(map[string]any)
			if ref["kind"] != tc.wantKind || ref["ref_id"] != tc.wantID {
				t.Fatalf("ref=%v", ref)
			}
			for _, forbidden := range []string{"payload", "summary", "body", "version_token", "access_count", "namespace", "memory_key"} {
				if _, found := body[forbidden]; found {
					t.Fatalf("identity response leaked %s: %v", forbidden, body)
				}
			}
		})
	}

	unsupported := performJSON(t, srv, http.MethodPost, "/v1/refs/resolve", map[string]any{"uri": "https://example.test/object/1"})
	if unsupported.Code != http.StatusOK || decodeHTTPJSON(t, unsupported.Body.Bytes())["status"] != "unsupported_reference" {
		t.Fatalf("unsupported=%s", unsupported.Body.String())
	}
	notFound := performJSON(t, srv, http.MethodPost, "/v1/refs/resolve", map[string]any{"item_id": "01ARZ3NDEKTSV4RRFFQ69G5FAV"})
	if notFound.Code != http.StatusOK || decodeHTTPJSON(t, notFound.Body.Bytes())["status"] != "not_found" {
		t.Fatalf("not found=%s", notFound.Body.String())
	}

	deleted, err := srv.WorkspaceStore.Delete(ctx, workspace.DeleteInput{ItemID: item.ItemID, VersionToken: item.VersionToken})
	if err != nil {
		t.Fatal(err)
	}
	tombstone := performJSON(t, srv, http.MethodPost, "/v1/refs/resolve", map[string]any{"item_id": item.ItemID})
	tombstoneBody := decodeHTTPJSON(t, tombstone.Body.Bytes())
	if tombstone.Code != http.StatusOK || tombstoneBody["status"] != "deleted" || tombstoneBody["deleted_at"] == nil || tombstoneBody["item_id"] != deleted.ItemID {
		t.Fatalf("tombstone=%v", tombstoneBody)
	}
	if strings.Contains(tombstone.Body.String(), item.Key) || strings.Contains(tombstone.Body.String(), "workspace secret") {
		t.Fatalf("tombstone leaked deleted content: %s", tombstone.Body.String())
	}
}

func TestReferenceResolveHTTPValidationAuthorizationAndAvailability(t *testing.T) {
	srv := referenceHTTPServer(t)
	rev, err := srv.MemoryStore.WriteRevision(context.Background(), memory.WriteInput{
		Domain: domains.Memory, Namespace: "project/tesseract/memory/notes", MemoryKey: "private.reference",
		Summary: "revision secret", Author: memory.Author{AgentID: "test"}, SessionID: "resolver",
		Trigger: memory.TriggerManual, DerivedFrom: memory.DerivedFromProject,
	})
	if err != nil {
		t.Fatal(err)
	}
	item, err := srv.WorkspaceStore.Create(context.Background(), workspace.CreateInput{
		Namespace: "project/tesseract/workspace/private", Key: "private", Summary: "secret",
		Author: memory.Author{AgentID: "test"}, SessionID: "resolver",
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, body := range []map[string]any{
		{}, {"item_id": item.ItemID, "revision_id": "x"}, {"domain": "workspace", "namespace": item.Namespace},
		{"uri": "tesseract://item/"}, {"item_id": ""}, {"memory_key": "private"},
		{"domain": "unknown", "namespace": item.Namespace, "key": item.Key},
	} {
		rr := performJSON(t, srv, http.MethodPost, "/v1/refs/resolve", body)
		if rr.Code != http.StatusBadRequest || decodeHTTPJSON(t, rr.Body.Bytes())["code"] != "validation_error" {
			t.Fatalf("body=%v status=%d response=%s", body, rr.Code, rr.Body.String())
		}
	}
	for _, field := range []string{"revision_id", "uri", "domain", "namespace", "key"} {
		body := map[string]any{"item_id": item.ItemID, field: nil}
		rr := performJSON(t, srv, http.MethodPost, "/v1/refs/resolve", body)
		if rr.Code != http.StatusBadRequest || decodeHTTPJSON(t, rr.Body.Bytes())["code"] != "validation_error" {
			t.Fatalf("null field=%s status=%d response=%s", field, rr.Code, rr.Body.String())
		}
	}
	for _, field := range []string{"item_id", "revision_id", "uri", "domain", "namespace", "key"} {
		rr := performJSON(t, srv, http.MethodPost, "/v1/refs/resolve", map[string]any{field: 7})
		if rr.Code != http.StatusBadRequest || decodeHTTPJSON(t, rr.Body.Bytes())["code"] != "validation_error" {
			t.Fatalf("non-string field=%s status=%d response=%s", field, rr.Code, rr.Body.String())
		}
	}

	rawToken, _, err := srv.Store.CreateAuthToken(context.Background(), contextstore.TokenCreateInput{
		Label: "resolver-denied", TTL: time.Hour, Scopes: []string{"memory:read"}, NamespaceGlobs: []string{"project/other/*"},
	})
	if err != nil {
		t.Fatal(err)
	}
	srv.ManagedAuth = true
	headers := map[string]string{"Authorization": "Bearer " + rawToken}
	denied := performJSONWithHeaders(t, srv, http.MethodPost, "/v1/refs/resolve", map[string]any{"item_id": item.ItemID}, headers)
	if denied.Code != http.StatusForbidden || decodeHTTPJSON(t, denied.Body.Bytes())["code"] != "namespace_not_permitted" || strings.Contains(denied.Body.String(), item.Namespace) || strings.Contains(denied.Body.String(), item.ItemID) {
		t.Fatalf("denied status=%d body=%s", denied.Code, denied.Body.String())
	}
	deniedRevision := performJSONWithHeaders(t, srv, http.MethodPost, "/v1/refs/resolve", map[string]any{"revision_id": rev.RevisionID}, headers)
	if deniedRevision.Code != http.StatusForbidden || strings.Contains(deniedRevision.Body.String(), rev.Namespace) || strings.Contains(deniedRevision.Body.String(), rev.ItemID) || strings.Contains(deniedRevision.Body.String(), rev.RevisionID) {
		t.Fatalf("denied revision status=%d body=%s", deniedRevision.Code, deniedRevision.Body.String())
	}
	deniedKey := performJSONWithHeaders(t, srv, http.MethodPost, "/v1/refs/resolve", map[string]any{"domain": "workspace", "namespace": item.Namespace, "key": item.Key}, headers)
	if deniedKey.Code != http.StatusForbidden || strings.Contains(deniedKey.Body.String(), item.ItemID) {
		t.Fatalf("denied key status=%d body=%s", deniedKey.Code, deniedKey.Body.String())
	}
	allowedToken, _, err := srv.Store.CreateAuthToken(context.Background(), contextstore.TokenCreateInput{
		Label: "resolver-allowed", TTL: time.Hour, Scopes: []string{"memory:read"}, NamespaceGlobs: []string{"project/tesseract/*"},
	})
	if err != nil {
		t.Fatal(err)
	}
	allowedHeaders := map[string]string{"Authorization": "Bearer " + allowedToken}
	for _, request := range []map[string]any{{"item_id": item.ItemID}, {"revision_id": rev.RevisionID}, {"domain": "workspace", "namespace": item.Namespace, "key": item.Key}} {
		positive := performJSONWithHeaders(t, srv, http.MethodPost, "/v1/refs/resolve", request, allowedHeaders)
		if positive.Code != http.StatusOK || decodeHTTPJSON(t, positive.Body.Bytes())["status"] != "resolved" {
			t.Fatalf("authorized request=%v response=%s", request, positive.Body.String())
		}
	}
	if _, err := srv.WorkspaceStore.Delete(context.Background(), workspace.DeleteInput{ItemID: item.ItemID, VersionToken: item.VersionToken}); err != nil {
		t.Fatal(err)
	}
	deniedTombstone := performJSONWithHeaders(t, srv, http.MethodPost, "/v1/refs/resolve", map[string]any{"item_id": item.ItemID}, headers)
	if deniedTombstone.Code != http.StatusForbidden || strings.Contains(deniedTombstone.Body.String(), "deleted_at") {
		t.Fatalf("denied tombstone=%s", deniedTombstone.Body.String())
	}
	allowedTombstone := performJSONWithHeaders(t, srv, http.MethodPost, "/v1/refs/resolve", map[string]any{"item_id": item.ItemID}, allowedHeaders)
	if allowedTombstone.Code != http.StatusOK || decodeHTTPJSON(t, allowedTombstone.Body.Bytes())["status"] != "deleted" {
		t.Fatalf("allowed tombstone=%s", allowedTombstone.Body.String())
	}

	unwired := newTestServer(t)
	unavailable := performJSON(t, unwired, http.MethodPost, "/v1/refs/resolve", map[string]any{"item_id": "01ARZ3NDEKTSV4RRFFQ69G5FAV"})
	if unavailable.Code != http.StatusServiceUnavailable || decodeHTTPJSON(t, unavailable.Body.Bytes())["code"] != "domain_unavailable" {
		t.Fatalf("unavailable=%s", unavailable.Body.String())
	}
}

func TestReferenceResolveHTTPRequiresReadScopeBeforeEverySelector(t *testing.T) {
	srv := referenceHTTPServer(t)
	ctx := context.Background()
	rev, err := srv.MemoryStore.WriteRevision(ctx, memory.WriteInput{
		Domain: domains.Memory, Namespace: "project/tesseract/memory/notes", MemoryKey: "scope.reference",
		Summary: "secret", Author: memory.Author{AgentID: "test"}, SessionID: "resolver",
		Trigger: memory.TriggerManual, DerivedFrom: memory.DerivedFromProject,
	})
	if err != nil {
		t.Fatal(err)
	}
	current, err := srv.WorkspaceStore.Create(ctx, workspace.CreateInput{
		Namespace: "project/tesseract/workspace/scope", Key: "current", Summary: "secret",
		Author: memory.Author{AgentID: "test"}, SessionID: "resolver",
	})
	if err != nil {
		t.Fatal(err)
	}
	deleted, err := srv.WorkspaceStore.Create(ctx, workspace.CreateInput{
		Namespace: current.Namespace, Key: "deleted", Summary: "secret",
		Author: memory.Author{AgentID: "test"}, SessionID: "resolver",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, deleteErr := srv.WorkspaceStore.Delete(ctx, workspace.DeleteInput{ItemID: deleted.ItemID, VersionToken: deleted.VersionToken}); deleteErr != nil {
		t.Fatal(deleteErr)
	}

	requests := []map[string]any{
		{"item_id": current.ItemID},
		{"revision_id": rev.RevisionID},
		{"domain": "workspace", "namespace": current.Namespace, "key": current.Key},
		{"uri": "tesseract://revision/" + rev.RevisionID},
		{"item_id": deleted.ItemID},
	}
	writeOnly, _, err := srv.Store.CreateAuthToken(ctx, contextstore.TokenCreateInput{
		Label: "resolver-write-only", TTL: time.Hour, Scopes: []string{"memory:write"}, NamespaceGlobs: []string{"*"},
	})
	if err != nil {
		t.Fatal(err)
	}
	readToken, _, err := srv.Store.CreateAuthToken(ctx, contextstore.TokenCreateInput{
		Label: "resolver-reader", TTL: time.Hour, Scopes: []string{"memory:read"}, NamespaceGlobs: []string{"*"},
	})
	if err != nil {
		t.Fatal(err)
	}
	srv.ManagedAuth = true
	for i, request := range requests {
		denied := performJSONWithHeaders(t, srv, http.MethodPost, "/v1/refs/resolve", request,
			map[string]string{"Authorization": "Bearer " + writeOnly})
		if denied.Code != http.StatusForbidden || decodeHTTPJSON(t, denied.Body.Bytes())["code"] != "insufficient_scope" {
			t.Fatalf("request %d escaped read scope: status=%d body=%s", i, denied.Code, denied.Body.String())
		}
		allowed := performJSONWithHeaders(t, srv, http.MethodPost, "/v1/refs/resolve", request,
			map[string]string{"Authorization": "Bearer " + readToken})
		if allowed.Code != http.StatusOK {
			t.Fatalf("request %d with read scope: status=%d body=%s", i, allowed.Code, allowed.Body.String())
		}
	}
}

func TestReferenceResolveHTTPAuthorizesConcreteNamespaceBytes(t *testing.T) {
	srv := referenceHTTPServer(t)
	ctx := context.Background()
	namespace := "project/tesseract/workspace/literal/*"
	item, err := srv.WorkspaceStore.Create(ctx, workspace.CreateInput{
		Namespace: namespace, Key: " ", Summary: "secret",
		Author: memory.Author{AgentID: "test"}, SessionID: "resolver",
	})
	if err != nil {
		t.Fatal(err)
	}
	exact, _, err := srv.Store.CreateAuthToken(ctx, contextstore.TokenCreateInput{
		Label: "resolver-literal", TTL: time.Hour, Scopes: []string{"memory:read"},
		NamespaceGlobs: []string{"project/tesseract/workspace/literal/\\*"},
	})
	if err != nil {
		t.Fatal(err)
	}
	descendants, _, err := srv.Store.CreateAuthToken(ctx, contextstore.TokenCreateInput{
		Label: "resolver-descendants", TTL: time.Hour, Scopes: []string{"memory:read"},
		NamespaceGlobs: []string{"project/tesseract/workspace/literal/\\*/child/*"},
	})
	if err != nil {
		t.Fatal(err)
	}
	srv.ManagedAuth = true
	request := map[string]any{"domain": "workspace", "namespace": namespace, "key": item.Key}
	allowed := performJSONWithHeaders(t, srv, http.MethodPost, "/v1/refs/resolve", request,
		map[string]string{"Authorization": "Bearer " + exact})
	if allowed.Code != http.StatusOK || decodeHTTPJSON(t, allowed.Body.Bytes())["status"] != "resolved" {
		t.Fatalf("literal namespace/key bytes were not resolved: status=%d body=%s", allowed.Code, allowed.Body.String())
	}
	denied := performJSONWithHeaders(t, srv, http.MethodPost, "/v1/refs/resolve", request,
		map[string]string{"Authorization": "Bearer " + descendants})
	if denied.Code != http.StatusForbidden || decodeHTTPJSON(t, denied.Body.Bytes())["code"] != "namespace_not_permitted" ||
		strings.Contains(denied.Body.String(), item.ItemID) || strings.Contains(denied.Body.String(), namespace) {
		t.Fatalf("descendant-only grant response: status=%d body=%s", denied.Code, denied.Body.String())
	}
}

func TestReferenceResolveHTTPUnavailableExplicitDomainBeforeNegativeAnswer(t *testing.T) {
	srv := referenceHTTPServer(t)
	rev, err := srv.KnowledgeStore.Write(context.Background(), knowledge.WriteInput{
		Namespace: "project/tesseract/knowledge/resolver", Key: "present", Summary: "secret",
		Kind: "doc", Source: "test", Pointer: memory.Pointer{Scheme: "nil", Locator: "test"},
		Author: memory.Author{AgentID: "test"}, SessionID: "resolver",
	})
	if err != nil {
		t.Fatal(err)
	}
	srv.KnowledgeStore = nil
	for _, key := range []string{rev.MemoryKey, "absent"} {
		rr := performJSON(t, srv, http.MethodPost, "/v1/refs/resolve", map[string]any{
			"domain": "knowledge", "namespace": rev.Namespace, "key": key,
		})
		if rr.Code != http.StatusServiceUnavailable || decodeHTTPJSON(t, rr.Body.Bytes())["code"] != "domain_unavailable" {
			t.Fatalf("key=%q status=%d body=%s", key, rr.Code, rr.Body.String())
		}
	}
}
