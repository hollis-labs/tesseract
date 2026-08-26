package mcpadapter

import (
	"context"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

// TestViewsEvaluate_MatchesHTTPEnvelope locks in the response shape parity
// with /v1/views/evaluate: the body is `{ items, evaluation_meta: {...} }`,
// not the flat EvaluateResult fields. Agents should be able to share a
// single parser across both surfaces.
func TestViewsEvaluate_MatchesHTTPEnvelope(t *testing.T) {
	a := newMemoryAdapter(t)
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"selector":        `{"namespaces":["app/test/*"]}`,
		"full_evaluation": true,
	}
	res, err := a.handleContextView(context.Background(), req)
	if err != nil {
		t.Fatalf("handleViewsEvaluate: %v", err)
	}
	body := parseResult(t, res)
	if _, ok := body["items"]; !ok {
		t.Errorf("missing top-level `items`; got %v", body)
	}
	meta, ok := body["evaluation_meta"].(map[string]any)
	if !ok {
		t.Fatalf("missing or non-object `evaluation_meta`; got %v", body)
	}
	for _, k := range []string{"sort_keys", "matched_count", "truncated", "normalized_scope"} {
		if _, ok := meta[k]; !ok {
			t.Errorf("evaluation_meta missing %q; got %v", k, meta)
		}
	}
	// Flat EvaluateResult fields must NOT leak to top level — that's the
	// regression we're guarding against.
	for _, leaked := range []string{"sort_keys", "matched_count", "truncated", "normalized_scope"} {
		if _, ok := body[leaked]; ok {
			t.Errorf("top-level %q leaked from EvaluateResult; should only live under evaluation_meta", leaked)
		}
	}
}

// TestViewsEvaluate_RejectsFractionalLimit verifies the limit param refuses
// non-integer values (matches the HTTP peer which decodes into Go's int).
func TestViewsEvaluate_RejectsFractionalLimit(t *testing.T) {
	a := newMemoryAdapter(t)
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"selector":        `{"namespaces":["app/test/*"]}`,
		"full_evaluation": true,
		"limit":           2.5,
	}
	res, err := a.handleContextView(context.Background(), req)
	if err != nil {
		t.Fatalf("handleViewsEvaluate: %v", err)
	}
	body := parseResult(t, res)
	if body["code"] != "validation_error" {
		t.Errorf("code = %v, want validation_error; got %v", body["code"], body)
	}
}
