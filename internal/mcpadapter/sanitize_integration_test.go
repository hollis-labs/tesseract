package mcpadapter

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	mcpsanitize "github.com/hollis-labs/go-mcp-sanitize"
	"github.com/mark3labs/mcp-go/mcp"
)

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
		"memory_key":      "decisions.test.cw_sanitize_integration",
		"author_agent_id": "claude",
		"trigger":         "explicit",
		"session_id":      "sess-sanitize-int",
		"origin":          "user",
		"confidence":      0.9,
		"payload_summary": pollutedSummary,
		"payload_body":    cleanBody,
		"tags":            `["decision","captured_during_session","test"]`,
	}

	// Wrap the handler with the same middleware addTool installs. This
	// mirrors the production wiring exactly.
	wrapped := mcpsanitize.Middleware(a.Logger)(a.handleMemoryWrite)

	req := mcp.CallToolRequest{}
	req.Params.Name = "memory_write"
	req.Params.Arguments = args

	res, err := wrapped(context.Background(), req)
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
		"memory_key":      "test.clean_call",
		"author_agent_id": "claude",
		"trigger":         "explicit",
		"session_id":      "sess-clean",
		"origin":          "user",
		"confidence":      0.9,
		"payload_summary": "Clean summary, no markup at all.",
		"payload_body":    "Clean body, no markup at all.",
	}

	wrapped := mcpsanitize.Middleware(a.Logger)(a.handleMemoryWrite)

	req := mcp.CallToolRequest{}
	req.Params.Name = "memory_write"
	req.Params.Arguments = args

	res, err := wrapped(context.Background(), req)
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
