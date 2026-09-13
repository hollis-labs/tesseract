package contextapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/hollis-labs/tesseract/domains"
	"github.com/hollis-labs/tesseract/internal/memory"
	"github.com/hollis-labs/tesseract/internal/workspace"
)

func TestWorkspaceRecallHTTPAcceptsBroadAndDeepPrefixes(t *testing.T) {
	srv := newWorkspaceTestServer(t)
	item, err := srv.WorkspaceStore.Create(context.Background(), workspace.CreateInput{
		Namespace: httpWorkspaceNS + "/child", Key: "prefix-target", Summary: "prefix target",
		Author: memory.Author{AgentID: "review"}, SessionID: "review",
	})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		call func() string
	}{
		{
			name: "typed lookup broad",
			call: func() string {
				return performJSON(t, srv, http.MethodPost, "/v1/tesseract/lookup", map[string]any{
					"namespaces": []string{"project/tesseract/*"}, "domains": []string{"workspace"},
					"ranking": "chronological", "payload_mode": "summary",
				}).Body.String()
			},
		},
		{
			name: "memory recall deep",
			call: func() string {
				return performJSON(t, srv, http.MethodPost, "/v1/memory/recall", map[string]any{
					"namespaces": []string{httpWorkspaceNS + "/*"}, "ranking": "chronological",
					"filters": map[string]any{"domains": []string{"workspace"}}, "payload_mode": "summary",
				}).Body.String()
			},
		},
		{
			name: "legacy get broad",
			call: func() string {
				return performJSON(t, srv, http.MethodGet, "/v1/recall?namespace=project/*&domains=workspace&format=brief", nil).Body.String()
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if body := tc.call(); !strings.Contains(body, item.ItemID) {
				t.Fatalf("prefix response omitted %s: %s", item.ItemID, body)
			}
		})
	}
}

func TestWorkspaceRecallHTTPRejectsSelectorsWiderThanToken(t *testing.T) {
	srv := newWorkspaceTestServer(t)
	srv.ManagedAuth = true
	token := issueTokenWithScopes(t, srv, "workspace-limited", []string{"memory:read"}, []string{"project/tesseract/workspace/*"})
	headers := map[string]string{"Authorization": "Bearer " + token}
	allowed := performJSONWithHeaders(t, srv, http.MethodPost, "/v1/tesseract/lookup", map[string]any{
		"namespaces": []string{"project/tesseract/*"}, "domains": []string{"workspace"},
		"ranking": "chronological", "payload_mode": "keys",
	}, headers)
	if allowed.Code != http.StatusOK {
		t.Fatalf("domain-narrowed authorized prefix status=%d body=%s", allowed.Code, allowed.Body.String())
	}
	tests := []struct {
		name   string
		method string
		path   string
		body   any
	}{
		{"lookup prefix", http.MethodPost, "/v1/tesseract/lookup", map[string]any{"namespaces": []string{"project/*"}, "domains": []string{"workspace"}}},
		{"memory recall exact", http.MethodPost, "/v1/memory/recall", map[string]any{"namespaces": []string{"project/other/workspace/private"}, "filters": map[string]any{"domains": []string{"workspace"}}, "estimate_only": true}},
		{"get prefix", http.MethodGet, "/v1/recall?namespace=project/*&domains=workspace", nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res := performJSONWithHeaders(t, srv, tc.method, tc.path, tc.body, headers)
			body := decodeHTTPJSON(t, res.Body.Bytes())
			if res.Code != http.StatusForbidden || body["code"] != "namespace_not_permitted" {
				t.Fatalf("status=%d body=%v", res.Code, body)
			}
		})
	}
}

func TestWorkspaceRecallHTTPRejectsLegacyNestedHeadOutsideGrant(t *testing.T) {
	srv := newWorkspaceTestServer(t)
	srv.ManagedAuth = true
	legacy, err := srv.WorkspaceStore.Create(context.Background(), workspace.CreateInput{
		Namespace: "user/chrispian/project/secret/workspace/private", Key: "legacy-private", Summary: "legacy private",
		Author: memory.Author{AgentID: "review"}, SessionID: "review",
	})
	if err != nil {
		t.Fatal(err)
	}
	narrowToken := issueTokenWithScopes(t, srv, "legacy-narrow", []string{"memory:read"}, []string{"user/chrispian/workspace/*"})
	narrowHeaders := map[string]string{"Authorization": "Bearer " + narrowToken}
	for _, tc := range []struct {
		name, method, path string
		body               any
	}{
		{"lookup full", http.MethodPost, "/v1/tesseract/lookup", map[string]any{"namespaces": []string{"user/chrispian/*"}, "domains": []string{"workspace"}, "payload_mode": "full"}},
		{"memory summary", http.MethodPost, "/v1/memory/recall", map[string]any{"namespaces": []string{"user/chrispian/*"}, "filters": map[string]any{"domains": []string{"workspace"}}, "payload_mode": "summary"}},
		{"legacy get", http.MethodGet, "/v1/recall?namespace=user/chrispian/*&domains=workspace&format=full", nil},
		{"estimate", http.MethodPost, "/v1/tesseract/lookup", map[string]any{"namespaces": []string{"user/chrispian/*"}, "domains": []string{"workspace"}, "estimate_only": true}},
		{"mixed domains", http.MethodPost, "/v1/tesseract/lookup", map[string]any{"namespaces": []string{"user/chrispian/*"}, "domains": []string{"memory", "workspace"}, "payload_mode": "keys"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := performJSONWithHeaders(t, srv, tc.method, tc.path, tc.body, narrowHeaders)
			body := decodeHTTPJSON(t, res.Body.Bytes())
			if res.Code != http.StatusForbidden || body["code"] != "namespace_not_permitted" {
				t.Fatalf("status=%d body=%v", res.Code, body)
			}
		})
	}

	broadToken := issueTokenWithScopes(t, srv, "legacy-broad", []string{"memory:read"}, []string{"user/chrispian/*"})
	broad := performJSONWithHeaders(t, srv, http.MethodPost, "/v1/tesseract/lookup", map[string]any{
		"namespaces": []string{"user/chrispian/*"}, "domains": []string{"workspace"}, "payload_mode": "keys",
	}, map[string]string{"Authorization": "Bearer " + broadToken})
	if broad.Code != http.StatusOK || !strings.Contains(broad.Body.String(), legacy.ItemID) {
		t.Fatalf("broader user grant did not retain legacy nested recall: status=%d body=%s", broad.Code, broad.Body.String())
	}
}

func TestWorkspaceRecallHTTPPrefixQueryIsCaseSensitiveAcrossRoutes(t *testing.T) {
	srv := newWorkspaceTestServer(t)
	srv.ManagedAuth = true
	allowed, err := srv.WorkspaceStore.Create(context.Background(), workspace.CreateInput{
		Namespace: "project/tesseract/workspace/private/allowed", Key: "allowed", Summary: "allowed",
		Author: memory.Author{AgentID: "review"}, SessionID: "review",
	})
	if err != nil {
		t.Fatal(err)
	}
	forbidden, err := srv.WorkspaceStore.Create(context.Background(), workspace.CreateInput{
		Namespace: "project/tesseract/workspace/Private/secret", Key: "forbidden", Summary: "forbidden",
		Author: memory.Author{AgentID: "review"}, SessionID: "review",
	})
	if err != nil {
		t.Fatal(err)
	}
	token := issueTokenWithScopes(t, srv, "case-sensitive", []string{"memory:read"}, []string{"project/tesseract/workspace/private/*"})
	headers := map[string]string{"Authorization": "Bearer " + token}
	for _, tc := range []struct {
		name, method, path string
		body               any
	}{
		{"lookup keys", http.MethodPost, "/v1/tesseract/lookup", map[string]any{"namespaces": []string{"project/tesseract/workspace/private/*"}, "domains": []string{"workspace"}, "payload_mode": "keys"}},
		{"memory summary", http.MethodPost, "/v1/memory/recall", map[string]any{"namespaces": []string{"project/tesseract/workspace/private/*"}, "filters": map[string]any{"domains": []string{"workspace"}}, "payload_mode": "summary"}},
		{"legacy full", http.MethodGet, "/v1/recall?namespace=project/tesseract/workspace/private/*&domains=workspace&format=full", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := performJSONWithHeaders(t, srv, tc.method, tc.path, tc.body, headers)
			if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), allowed.ItemID) || strings.Contains(res.Body.String(), forbidden.ItemID) {
				t.Fatalf("status=%d returned the wrong case-sensitive set: %s", res.Code, res.Body.String())
			}
		})
	}

	estimate := performJSONWithHeaders(t, srv, http.MethodPost, "/v1/tesseract/lookup", map[string]any{
		"namespaces": []string{"project/tesseract/workspace/private/*"}, "domains": []string{"workspace"}, "estimate_only": true,
	}, headers)
	body := decodeHTTPJSON(t, estimate.Body.Bytes())
	manifest, _ := body["manifest"].(map[string]any)
	if estimate.Code != http.StatusOK || int(manifest["results_total"].(float64)) != 1 {
		t.Fatalf("case-sensitive estimate status=%d body=%v", estimate.Code, body)
	}
}

func TestRecallSelectorGrantBoundaryMatrixHTTP(t *testing.T) {
	for _, tc := range []struct {
		name      string
		globs     []string
		selector  string
		domains   []string
		permitted bool
	}{
		{"exact selector exact grant", []string{"project/tesseract/knowledge/archive"}, "project/tesseract/knowledge/archive", []string{"knowledge"}, true},
		{"explicit descendants exact grant", []string{"project/tesseract/workspace/private"}, "project/tesseract/workspace/private/*", []string{"workspace"}, false},
		{"explicit descendants descendant grant", []string{"project/tesseract/workspace/private/*"}, "project/tesseract/workspace/private/*", []string{"workspace"}, true},
		{"legacy memory exact grant", []string{"project/tesseract/memory"}, "project/tesseract/memory", []string{"memory"}, false},
		{"legacy memory descendant grant", []string{"project/tesseract/memory/*"}, "project/tesseract/memory", []string{"memory"}, true},
		{"legacy event exact grant", []string{"project/tesseract/event"}, "project/tesseract/event", []string{"event"}, false},
		{"legacy event descendant grant", []string{"project/tesseract/event/*"}, "project/tesseract/event", []string{"event"}, true},
		{"project domain narrowing", []string{"project/tesseract/workspace/*"}, "project/tesseract/*", []string{"workspace"}, true},
		{"other domain memory tail exact", []string{"project/tesseract/knowledge/archive/memory"}, "project/tesseract/knowledge/archive/memory", []string{"knowledge"}, true},
		{"mixed domains complete", []string{"project/tesseract/memory/*", "project/tesseract/workspace/*"}, "project/tesseract/*", []string{"memory", "workspace"}, true},
		{"mixed domains incomplete", []string{"project/tesseract/workspace/*"}, "project/tesseract/*", []string{"memory", "workspace"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := true
			for _, selector := range recallAuthorizationSelectorsHTTP(tc.selector, tc.domains) {
				if !namespaceSelectorPermitted(tc.globs, selector, tc.domains) {
					got = false
					break
				}
			}
			if got != tc.permitted {
				t.Fatalf("namespaceSelectorPermitted(%v, %q, %v)=%v, want %v", tc.globs, tc.selector, tc.domains, got, tc.permitted)
			}
		})
	}
}

func TestWorkspaceRecallHTTPDescendantGrantExcludesParent(t *testing.T) {
	srv := newWorkspaceTestServer(t)
	srv.ManagedAuth = true
	const parentNamespace = "project/tesseract/workspace/boundary-http"
	parent, err := srv.WorkspaceStore.Create(context.Background(), workspace.CreateInput{
		Namespace: parentNamespace, Key: "parent", Summary: "parent",
		Author: memory.Author{AgentID: "review"}, SessionID: "review",
	})
	if err != nil {
		t.Fatal(err)
	}
	child, err := srv.WorkspaceStore.Create(context.Background(), workspace.CreateInput{
		Namespace: parentNamespace + "/child", Key: "child", Summary: "child",
		Author: memory.Author{AgentID: "review"}, SessionID: "review",
	})
	if err != nil {
		t.Fatal(err)
	}
	token := issueTokenWithScopes(t, srv, "workspace-descendants", []string{"memory:read"}, []string{parentNamespace + "/*"})
	headers := map[string]string{"Authorization": "Bearer " + token}
	if res := getItemRoute(t, srv, "/v1/items/"+parent.ItemID, headers); res.Code != http.StatusForbidden {
		t.Fatalf("parent current read status=%d body=%s", res.Code, res.Body.String())
	}
	if res := getItemRoute(t, srv, "/v1/items/"+child.ItemID, headers); res.Code != http.StatusOK {
		t.Fatalf("child current read status=%d body=%s", res.Code, res.Body.String())
	}
	for _, tc := range []struct {
		name, method, path string
		body               any
	}{
		{"lookup full", http.MethodPost, "/v1/tesseract/lookup", map[string]any{"namespaces": []string{parentNamespace + "/*"}, "domains": []string{"workspace"}, "payload_mode": "full"}},
		{"memory summary", http.MethodPost, "/v1/memory/recall", map[string]any{"namespaces": []string{parentNamespace + "/*"}, "filters": map[string]any{"domains": []string{"workspace"}}, "payload_mode": "summary"}},
		{"legacy keys", http.MethodGet, "/v1/recall?namespace=" + parentNamespace + "/*&domains=workspace&format=brief", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := performJSONWithHeaders(t, srv, tc.method, tc.path, tc.body, headers)
			if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), child.ItemID) || strings.Contains(res.Body.String(), parent.ItemID) {
				t.Fatalf("status=%d returned the wrong boundary set: %s", res.Code, res.Body.String())
			}
		})
	}
	estimate := performJSONWithHeaders(t, srv, http.MethodPost, "/v1/tesseract/lookup", map[string]any{
		"namespaces": []string{parentNamespace + "/*"}, "domains": []string{"workspace"}, "estimate_only": true,
	}, headers)
	manifest := decodeHTTPJSON(t, estimate.Body.Bytes())["manifest"].(map[string]any)
	if estimate.Code != http.StatusOK || int(manifest["results_total"].(float64)) != 1 {
		t.Fatalf("estimate status=%d manifest=%v", estimate.Code, manifest)
	}
}

func TestMemoryRecallHTTPLegacyPrefixRequiresDescendantGrant(t *testing.T) {
	srv := newWorkspaceTestServer(t)
	srv.ManagedAuth = true
	const root = "project/tesseract/memory"
	child, err := srv.MemoryStore.WriteRevision(context.Background(), memory.WriteInput{
		Domain: domains.Memory, Namespace: root + "/notes", MemoryKey: "boundary.http.memory",
		Summary: "memory child", Author: memory.Author{AgentID: "review"}, SessionID: "review",
		Trigger: memory.TriggerManual, DerivedFrom: memory.DerivedFromProject,
	})
	if err != nil {
		t.Fatal(err)
	}
	exactToken := issueTokenWithScopes(t, srv, "memory-root", []string{"memory:read"}, []string{root})
	exactHeaders := map[string]string{"Authorization": "Bearer " + exactToken}
	if res := getItemRoute(t, srv, "/v1/items/"+child.ItemID, exactHeaders); res.Code != http.StatusForbidden {
		t.Fatalf("child current read status=%d body=%s", res.Code, res.Body.String())
	}
	for _, tc := range []struct {
		name, method, path string
		body               any
	}{
		{"lookup", http.MethodPost, "/v1/tesseract/lookup", map[string]any{"namespaces": []string{root}, "domains": []string{"memory"}, "payload_mode": "full"}},
		{"memory recall", http.MethodPost, "/v1/memory/recall", map[string]any{"namespaces": []string{root}, "filters": map[string]any{"domains": []string{"memory"}}, "payload_mode": "summary"}},
		{"legacy get", http.MethodGet, "/v1/recall?namespace=" + root + "&domains=memory&format=full", nil},
	} {
		t.Run("exact grant "+tc.name, func(t *testing.T) {
			res := performJSONWithHeaders(t, srv, tc.method, tc.path, tc.body, exactHeaders)
			body := decodeHTTPJSON(t, res.Body.Bytes())
			if res.Code != http.StatusForbidden || body["code"] != "namespace_not_permitted" {
				t.Fatalf("status=%d body=%v", res.Code, body)
			}
		})
	}

	descendantToken := issueTokenWithScopes(t, srv, "memory-children", []string{"memory:read"}, []string{root + "/*"})
	allowed := performJSONWithHeaders(t, srv, http.MethodPost, "/v1/tesseract/lookup", map[string]any{
		"namespaces": []string{root}, "domains": []string{"memory"}, "payload_mode": "full",
	}, map[string]string{"Authorization": "Bearer " + descendantToken})
	if allowed.Code != http.StatusOK || !strings.Contains(allowed.Body.String(), child.ItemID) {
		t.Fatalf("descendant grant did not preserve legacy bare recall: status=%d body=%s", allowed.Code, allowed.Body.String())
	}
}

func TestWorkspaceHTTPCreateRejectsClearFieldsBeforeWriting(t *testing.T) {
	srv := newWorkspaceTestServer(t)
	for i, clearFields := range []any{[]string{}, []string{"body"}, []string{"unknown"}, nil, map[string]any{"bad": true}} {
		key := fmt.Sprintf("unexpected-clear-%d", i)
		res := performJSON(t, srv, http.MethodPost, "/v1/workspace/write", map[string]any{
			"namespace": httpWorkspaceNS, "key": key, "summary": "create must reject edit fields",
			"author": map[string]any{"agent_id": "review"}, "session_id": "review", "clear_fields": clearFields,
		})
		body := decodeHTTPJSON(t, res.Body.Bytes())
		if res.Code != http.StatusBadRequest || body["code"] != "validation_error" {
			t.Fatalf("case %d status=%d body=%v", i, res.Code, body)
		}
		if _, err := srv.WorkspaceStore.GetCurrentByKey(context.Background(), httpWorkspaceNS, key); !errors.Is(err, workspace.ErrNotFound) {
			t.Fatalf("case %d wrote an item despite rejection: %v", i, err)
		}
	}
}

func TestLegacyGETRecallKeepsBriefFullAndFiveHundredCeiling(t *testing.T) {
	srv := newWorkspaceTestServer(t)
	ctx := context.Background()
	const namespace = "project/tesseract/memory/notes"
	for i := 0; i < 501; i++ {
		_, err := srv.MemoryStore.WriteRevision(ctx, memory.WriteInput{
			Domain: domains.Memory, Namespace: namespace, MemoryKey: fmt.Sprintf("legacy.%03d", i),
			Summary: "legacy target", Author: memory.Author{AgentID: "review"}, SessionID: "review",
			Trigger: memory.TriggerManual, DerivedFrom: memory.DerivedFromProject,
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, format := range []string{"brief", "full"} {
		t.Run(format+" requested limit", func(t *testing.T) {
			res := performJSON(t, srv, http.MethodGet, "/v1/recall?namespace="+namespace+"&limit=121&format="+format, nil)
			body := decodeHTTPJSON(t, res.Body.Bytes())
			results, _ := body["results"].([]any)
			if res.Code != http.StatusOK || len(results) != 121 || int(body["meta"].(map[string]any)["returned"].(float64)) != 121 {
				t.Fatalf("status=%d results=%d body=%v", res.Code, len(results), body["meta"])
			}
		})
		t.Run(format+" store ceiling", func(t *testing.T) {
			res := performJSON(t, srv, http.MethodGet, "/v1/recall?namespace="+namespace+"&limit=600&format="+format, nil)
			body := decodeHTTPJSON(t, res.Body.Bytes())
			results, _ := body["results"].([]any)
			if res.Code != http.StatusOK || len(results) != memory.MaxRecallLimit {
				t.Fatalf("status=%d results=%d want=%d meta=%v", res.Code, len(results), memory.MaxRecallLimit, body["meta"])
			}
		})
	}
}
