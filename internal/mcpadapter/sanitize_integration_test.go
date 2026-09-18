package mcpadapter

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	mcpsanitize "github.com/hollis-labs/go-mcp/sanitize"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// runThroughSanitize composes go-mcp/sanitize's Middleware exactly the way
// Adapter.Run installs it — once, on the raw SDK server, ahead of tool
// dispatch, operating on mcpsdk.MethodHandler/mcpsdk.Request rather than on
// a per-tool gomcpserver.ToolHandler (see Adapter.addTool's doc comment for
// why sanitize moved out of the per-tool chain).
//
// This drives the middleware directly rather than standing up a live
// client/server session over go-mcp's in-memory transport: the behavior
// under test is "sanitize cleans the arguments before the tool handler sees
// them", which this reaches without the protocol round trip. next captures
// the decoded, sanitized arguments and calls the real handler with them, the
// same way adaptHandler's JSON-decode-then-dispatch does.
func runThroughSanitize(t *testing.T, logger *slog.Logger, toolName string, args map[string]any, h func(context.Context, map[string]any) (any, error)) (any, error) {
	t.Helper()
	var result any
	var handlerErr error
	next := func(ctx context.Context, method string, req mcpsdk.Request) (mcpsdk.Result, error) {
		call, ok := req.(*mcpsdk.CallToolRequest)
		if !ok {
			t.Fatalf("expected *mcpsdk.CallToolRequest, got %T", req)
		}
		var decoded map[string]any
		if err := json.Unmarshal(call.Params.Arguments, &decoded); err != nil {
			t.Fatalf("decode sanitized arguments: %v", err)
		}
		result, handlerErr = h(ctx, decoded)
		return &mcpsdk.CallToolResult{}, nil
	}
	wrapped := mcpsanitize.Middleware(logger)(next)

	argsJSON, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	call := &mcpsdk.CallToolRequest{Params: &mcpsdk.CallToolParamsRaw{Name: toolName, Arguments: argsJSON}}
	if _, err := wrapped(context.Background(), "tools/call", call); err != nil {
		t.Fatalf("sanitize middleware error: %v", err)
	}
	return result, handlerErr
}

// TestSanitizeMiddleware_PollutedMemoryWrite exercises the go-mcp-sanitize
// middleware against the smoking-gun shape captured in revision
// 01KR74F29P9Y80C384JDJ3QYQG: payload_summary contains a leaked
// </payload_summary>\n<parameter name="payload_body">...</parameter> trailing
// fragment, while a separate clean payload_body field also lands in the call.
//
// After the wrapped handler runs, the persisted memory revision must have:
//   - payload_summary cleaned (no XML-ish trailer, no payload_body markup)
//   - payload_body equal to the agent's clean copy (NOT overwritten by the
//     leaked fragment)
//
// This locks in the integration as wired by Adapter.addTool.
func TestSanitizeMiddleware_PollutedMemoryWrite(t *testing.T) {
	a := newMemoryAdapter(t, "memory:write", "memory:read")
	a.Logger = slog.New(slog.NewTextHandler(testWriter{t}, nil))

	cleanBody := "## Decision\n\nFor CW-test, we chose option B.\n\n## Why\n\nOption A would silently break two consumers."
	cleanSummary := "CW-test: chose option B (keep adapters in go-providers)."

	// The smoking-gun shape: payload_summary has a leaked closing tag plus a
	// duplicated <parameter name="payload_body">...</parameter> XML chunk
	// inlined into the summary string.
	pollutedSummary := cleanSummary +
		"</payload_summary>\n" +
		"<parameter name=\"payload_body\">" + cleanBody + "</parameter>"

	args := map[string]any{
		"namespace":       "user/chrispian/memory/notes",
		"actor":           "user",
		"memory_key":      "decisions.test.cw_sanitize_integration",
		"author_agent_id": "claude",
		"trigger":         "explicit",
		"session_id":      "sess-sanitize-int",
		"derived_from":    "user",
		"confidence":      0.9,
		"payload_summary": pollutedSummary,
		"payload_body":    cleanBody,
		"tags":            `["decision","captured_during_session","test"]`,
	}

	// Run through the middleware the way Adapter.Run installs it. This
	// mirrors the production wiring exactly.
	res, err := runThroughSanitize(t, a.Logger, "memory_write", args, a.handleMemoryWrite)
	if err != nil {
		t.Fatalf("wrapped handler error: %v", err)
	}
	body := parseResult(t, res)
	revisionID, _ := body["revision_id"].(string)
	if revisionID == "" {
		t.Fatalf("expected revision_id in response, got %v", body)
	}

	rev, err := a.MemoryStore.GetRevisionByID(context.Background(), revisionID)
	if err != nil {
		t.Fatalf("GetRevisionByID(%q): %v", revisionID, err)
	}

	// Assertion 1 — payload_summary is cleaned: no closing tag, no leaked
	// payload_body markup.
	if strings.Contains(rev.Payload.Summary, "</payload_summary>") {
		t.Errorf("payload_summary still contains </payload_summary>: %q", rev.Payload.Summary)
	}
	if strings.Contains(rev.Payload.Summary, "<parameter") {
		t.Errorf("payload_summary still contains <parameter ...> markup: %q", rev.Payload.Summary)
	}
	if !strings.HasPrefix(rev.Payload.Summary, "CW-test: chose option B") {
		t.Errorf("payload_summary lost its real content; got: %q", rev.Payload.Summary)
	}

	// Assertion 2 — payload_body is preserved as the agent's clean copy.
	// The middleware must NOT overwrite the explicit clean payload_body
	// field with anything it recovered from the polluted summary.
	if rev.Payload.Body != cleanBody {
		t.Errorf("payload_body was overwritten or lost.\n  want: %q\n  got:  %q", cleanBody, rev.Payload.Body)
	}
}

// TestSanitizeMiddleware_CleanCallPassesThrough confirms the middleware is a
// near-no-op on already-clean calls: no rewrite, no log noise, identical
// persisted shape.
func TestSanitizeMiddleware_CleanCallPassesThrough(t *testing.T) {
	a := newMemoryAdapter(t, "memory:write", "memory:read")
	a.Logger = slog.New(slog.NewTextHandler(testWriter{t}, nil))

	args := map[string]any{
		"namespace":       "user/chrispian/memory/notes",
		"actor":           "user",
		"memory_key":      "test.clean_call",
		"author_agent_id": "claude",
		"trigger":         "explicit",
		"session_id":      "sess-clean",
		"derived_from":    "user",
		"confidence":      0.9,
		"payload_summary": "Clean summary, no markup at all.",
		"payload_body":    "Clean body, no markup at all.",
	}

	res, err := runThroughSanitize(t, a.Logger, "memory_write", args, a.handleMemoryWrite)
	if err != nil {
		t.Fatalf("wrapped handler error: %v", err)
	}
	body := parseResult(t, res)
	revisionID, _ := body["revision_id"].(string)
	if revisionID == "" {
		t.Fatalf("expected revision_id in response, got %v", body)
	}

	rev, err := a.MemoryStore.GetRevisionByID(context.Background(), revisionID)
	if err != nil {
		t.Fatalf("GetRevisionByID: %v", err)
	}
	if rev.Payload.Summary != "Clean summary, no markup at all." {
		t.Errorf("clean payload_summary mutated: %q", rev.Payload.Summary)
	}
	if rev.Payload.Body != "Clean body, no markup at all." {
		t.Errorf("clean payload_body mutated: %q", rev.Payload.Body)
	}
}

// testWriter routes slog output to t.Log so cleaned-call telemetry is visible
// in -v test runs without polluting stderr.
type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) {
	w.t.Helper()
	w.t.Log(strings.TrimRight(string(p), "\n"))
	return len(p), nil
}
