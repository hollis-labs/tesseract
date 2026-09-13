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
