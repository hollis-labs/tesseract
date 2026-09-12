package mcpadapter

import (
	"context"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/hollis-labs/tesseract/internal/knowledge"
	"github.com/hollis-labs/tesseract/internal/memory"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"go.opentelemetry.io/otel/trace"
)

// A traceparent in the shape go-otel's InjectMCP writes: version-trace-span-flags,
// sampled. Stated as a literal rather than built from the propagator, so the
// test still fails if the parsing changes to agree with itself.
const sampledTraceparent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"

// registeredSurface returns a server carrying every tool this adapter exposes,
// wired against a throwaway store.
func registeredSurface(t *testing.T) *server.MCPServer {
	t.Helper()
	cs := newTestStore(t)
	ms := memory.NewStore(cs.DB(), nil, "", 0, memory.NoopQueue{})
	a := New(cs, "")
	a.MemoryStore = ms
	a.KnowledgeStore = knowledge.New(ms)

	srv := server.NewMCPServer("strictargs", "0.0.0", server.WithToolCapabilities(true))
	a.RegisterAllTools(srv)
	return srv
}

// TestEveryRegisteredToolRefusesAnUndeclaredArgument is the gate.
//
// The compile-time half of the protection — registerXxx helpers hold a
// toolRegistrar, not the server — answers "did someone write a bypass". It
// cannot answer "is every tool the server actually exposes protected", because
// a tool could reach the server by a path that type does not own.
//
// So this checks the effect rather than the syntax: it enumerates what the
// server ENDED UP WITH and calls each tool through its registered handler with
// a name no tool declares. A tool that answers anything other than a refusal
// has escaped the middleware, however it got there.
//
// Calling every handler is safe by construction here: the strict check runs
// ahead of the handler, so a protected tool never reaches its body. A tool that
// is NOT protected does reach its body — which is the failure being reported,
// and it runs against a throwaway store with no token.
func TestEveryRegisteredToolRefusesAnUndeclaredArgument(t *testing.T) {
	srv := registeredSurface(t)
	tools := srv.ListTools()
	if len(tools) == 0 {
		t.Fatal("registered zero tools — a clean result here would be meaningless")
	}

	names := make([]string, 0, len(tools))
	for name := range tools {
		names = append(names, name)
	}
	sort.Strings(names)

	const undeclared = "definitely_not_a_declared_argument"
	for _, name := range names {
		st := tools[name]
		if _, declared := st.Tool.InputSchema.Properties[undeclared]; declared {
			t.Fatalf("tool %q declares the probe name; pick another probe", name)
		}

		req := mcp.CallToolRequest{}
		req.Params.Name = name
		req.Params.Arguments = map[string]any{undeclared: "x"}

		res, err := st.Handler(context.Background(), req)
		if err != nil {
			t.Errorf("tool %q: handler returned a transport error for an undeclared argument: %v", name, err)
			continue
		}
		text := resultText(t, res)
		if !strings.Contains(text, undeclared) || !strings.Contains(text, string(codeValidationError)) {
			t.Errorf("tool %q ACCEPTED an argument it does not declare.\n"+
				"    Every tool must be registered through Adapter.addTool, which installs\n"+
				"    strictArgsMiddleware. A tool reaching the server another way opts out of it,\n"+
				"    and an argument nothing reads produces a successful call with the value missing.\n"+
				"    got: %s", name, text)
		}
	}
}

// TestGatewayMetadataIsStrippedAheadOfStrictness is the other half of the same
// contract: the keys mux injects must NOT be treated as caller arguments.
//
// Without the strip, going strict breaks every call an older mux forwards —
// which is the failure Tangent hit (CW-20260907-0022) and the reason this
// landed with the strip rather than after it.
func TestGatewayMetadataIsStrippedAheadOfStrictness(t *testing.T) {
	srv := registeredSurface(t)
	st, ok := srv.ListTools()["tesseract_skills"]
	if !ok {
		t.Fatal("tesseract_skills is not registered")
	}

	req := mcp.CallToolRequest{}
	req.Params.Name = "tesseract_skills"
	req.Params.Arguments = map[string]any{
		"_traceparent": sampledTraceparent,
		"_tracestate":  "hollis=abc",
	}

	res, err := st.Handler(context.Background(), req)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	text := resultText(t, res)
	if strings.Contains(text, "_traceparent") || strings.Contains(text, "_tracestate") {
		t.Fatalf("gateway metadata reached the strict check instead of being stripped: %s", text)
	}
}

// TestGatewayMetadataKeysArePinned states the accepted set by hand.
//
// The set is the whole safety argument for the strip: it is an allowlist of
// keys a specific injector is known to write, not a rule about underscores.
// Deriving the expectation from the map would make this agree with any set.
func TestGatewayMetadataKeysArePinned(t *testing.T) {
	want := []string{"_tracestate", "_traceparent"}
	sort.Strings(want)
	if got := GatewayMetadataKeys(); !reflect.DeepEqual(got, want) {
		t.Fatalf("accepted gateway metadata changed.\n  got:  %v\n  want: %v\n"+
			"Each key needs the injector's source as evidence before it is added; "+
			"an underscore prefix is not on its own a reason.", got, want)
	}
}

// TestUnknownUnderscoreKeyIsStillRefused pins the line between "transport
// metadata" and "escape hatch". Accepting any `_foo` would trade a silent drop
// for a silent bypass.
func TestUnknownUnderscoreKeyIsStillRefused(t *testing.T) {
	srv := registeredSurface(t)
	st := srv.ListTools()["tesseract_skills"]

	req := mcp.CallToolRequest{}
	req.Params.Name = "tesseract_skills"
	req.Params.Arguments = map[string]any{"_not_a_gateway_key": "x"}

	res, err := st.Handler(context.Background(), req)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if text := resultText(t, res); !strings.Contains(text, "_not_a_gateway_key") {
		t.Fatalf("an unknown underscore-prefixed key was accepted: %s", text)
	}
}

// TestGatewayMetadataCarriesTheUpstreamTraceForward checks that the traceparent
// is RECORDED rather than discarded, so correlation survives the strip — and
// that the channel a handler reads it from is the one a future stamp will use
// (CW-20260912-0024).
func TestGatewayMetadataCarriesTheUpstreamTraceForward(t *testing.T) {
	var seen gatewayMetadata
	var remote trace.SpanContext
	handler := gatewayMetadataMiddleware(func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		seen = gatewayMetadataFromContext(ctx)
		remote = trace.SpanContextFromContext(ctx)
		return mcp.NewToolResultText("{}"), nil
	})

	req := mcp.CallToolRequest{}
	req.Params.Name = "anything"
	req.Params.Arguments = map[string]any{
		"_traceparent": sampledTraceparent,
		"namespace":    "user/chrispian/memory/notes",
	}
	if _, err := handler(context.Background(), req); err != nil {
		t.Fatalf("handler error: %v", err)
	}

	if !seen.UpstreamTrace.IsValid() {
		t.Fatal("the upstream trace was dropped rather than recorded")
	}
	if got := seen.UpstreamTrace.TraceID().String(); got != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Errorf("upstream trace id = %q, want 4bf92f3577b34da6a3ce929d0e0e4736", got)
	}
	if !seen.UpstreamTrace.IsSampled() {
		t.Error("the sampled flag was lost, so downstream spans would be dropped")
	}
	if !remote.IsValid() {
		t.Error("the parsed context was not attached as the remote span context, " +
			"so a span started while serving this call would not link to the caller")
	}
}

// TestGatewayMetadataLeavesOrdinaryCallsAlone: a call carrying none of the
// accepted keys must reach the handler exactly as it arrived. Most calls take
// this path — HTTP, the CLI and a directly-spawned MCP child all bypass the
// proxy.
func TestGatewayMetadataLeavesOrdinaryCallsAlone(t *testing.T) {
	want := map[string]any{"namespace": "user/chrispian/memory/notes", "limit": 5}
	var got map[string]any
	handler := gatewayMetadataMiddleware(func(_ context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		got = r.GetArguments()
		return mcp.NewToolResultText("{}"), nil
	})

	req := mcp.CallToolRequest{}
	req.Params.Arguments = want
	if _, err := handler(context.Background(), req); err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("arguments were rewritten on a call carrying no gateway metadata.\n  got:  %v\n  want: %v", got, want)
	}
}

// TestStripDoesNotMutateTheCallersMap: mcp-go hands one map down the chain, so
// editing it in place would change what every other middleware sees.
func TestStripDoesNotMutateTheCallersMap(t *testing.T) {
	args := map[string]any{"_traceparent": sampledTraceparent, "namespace": "ns"}
	stripped, _, changed := stripGatewayMetadata(args)
	if !changed {
		t.Fatal("expected the traceparent to be stripped")
	}
	if _, still := args["_traceparent"]; !still {
		t.Error("the caller's map was mutated in place")
	}
	if _, gone := stripped["_traceparent"]; gone {
		t.Error("the traceparent survived into the stripped arguments")
	}
}

// TestPayloadDataTypoIsRefusedWithASuggestion is the motivating case, end to
// end: `data` instead of `payload_data` used to produce a SUCCESSFUL WRITE with
// the object missing.
//
// The assertion is on the guidance, not only on the refusal. "unexpected key"
// leaves the caller to guess, and guessing is what produced the key.
func TestPayloadDataTypoIsRefusedWithASuggestion(t *testing.T) {
	srv := registeredSurface(t)
	st, ok := srv.ListTools()["memory_write"]
	if !ok {
		t.Fatal("memory_write is not registered")
	}

	req := mcp.CallToolRequest{}
	req.Params.Name = "memory_write"
	req.Params.Arguments = map[string]any{
		"namespace":       "user/chrispian/memory/notes",
		"memory_key":      "test.payload_data_typo",
		"author_agent_id": "claude",
		"trigger":         "explicit",
		"session_id":      "sess-typo",
		"derived_from":    "user",
		"payload_summary": "a summary",
		"data":            map[string]any{"severity": "high"},
	}

	res, err := st.Handler(context.Background(), req)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	text := resultText(t, res)
	for _, want := range []string{"`data`", "payload_data", "memory_write"} {
		if !strings.Contains(text, want) {
			t.Errorf("the refusal does not mention %s.\n  got: %s", want, text)
		}
	}
	if !strings.Contains(text, "This tool accepts:") {
		t.Errorf("the refusal does not name the declared alternatives.\n  got: %s", text)
	}
}

// TestRetiredArgKeepsItsGuidance: wholesale rejection subsumes DETECTING a
// retired name, but not the migration knowledge. "`origin` is now
// `derived_from`" is worth far more than "unexpected key `origin`", which is
// the whole reason retiredArgGuidance exists rather than nothing.
func TestRetiredArgKeepsItsGuidance(t *testing.T) {
	srv := registeredSurface(t)

	for _, tc := range []struct {
		tool string
		arg  string
		want string
	}{
		{"memory_write", "origin", "derived_from"},
		{"tesseract_recall", "origins", "derived_from"},
		{"context_status_set", "to_status", "`status`"},
		{"context_plan", "budget_tokens", "max_tokens_estimate"},
		{"context_plan", "budget_items", "max_items"},
		{"context_pack", "max_tokens", "max_tokens_estimate"},
		{"context_registry_list", "namespace", "`name`"},
		{"context_pack", "payload_mode", "payload_max_bytes=512"},
	} {
		st, ok := srv.ListTools()[tc.tool]
		if !ok {
			t.Errorf("tool %q is not registered", tc.tool)
			continue
		}
		req := mcp.CallToolRequest{}
		req.Params.Name = tc.tool
		req.Params.Arguments = map[string]any{tc.arg: "x"}

		res, err := st.Handler(context.Background(), req)
		if err != nil {
			t.Errorf("%s(%s): handler error: %v", tc.tool, tc.arg, err)
			continue
		}
		text := resultText(t, res)
		if !strings.Contains(text, tc.want) {
			t.Errorf("%s(%s) was refused without its migration guidance.\n  want the message to name %q\n  got: %s",
				tc.tool, tc.arg, tc.want, text)
		}
	}
}

// TestRetiredArgsAreNotDeclared stops retiredArgGuidance rotting into a second
// hand-maintained list.
//
// An entry naming an argument the tool DOES declare is unreachable — the
// generic check accepts the name before the guidance is consulted — and it
// would sit there reading like coverage. The accepted set comes from the
// schema; this map only explains refusals the schema already produces.
func TestRetiredArgsAreNotDeclared(t *testing.T) {
	srv := registeredSurface(t)
	tools := srv.ListTools()

	for toolName, retired := range retiredArgGuidance {
		st, ok := tools[toolName]
		if !ok {
			t.Errorf("retiredArgGuidance names tool %q, which is not registered", toolName)
			continue
		}
		declared, err := declaredArgNames(st.Tool)
		if err != nil {
			t.Fatalf("declaredArgNames(%s): %v", toolName, err)
		}
		for arg := range retired {
			if _, isDeclared := declared[arg]; isDeclared {
				t.Errorf("%s declares %q AND lists it as retired. "+
					"The declared set wins, so the guidance is unreachable — "+
					"drop the entry or undeclare the argument.", toolName, arg)
			}
		}
	}
}

// TestSuggestArgNames states the two shapes the ranking exists for, by hand.
func TestSuggestArgNames(t *testing.T) {
	accepted := []string{
		"consumer_state", "memory_key", "namespace", "payload_body",
		"payload_data", "payload_data_schema_hash", "payload_summary", "tags",
	}
	for _, tc := range []struct {
		unknown string
		want    string
		why     string
	}{
		{"data", "payload_data", "the short name of a prefixed argument, not a typo — and the SHORTEST containing name wins"},
		{"namespcae", "namespace", "a transposition, which only edit distance catches"},
		{"payload", "payload_body", "a prefix matching several: the shortest is offered first"},
		{"tag", "tags", "a singular/plural slip"},
	} {
		got := suggestArgNames(tc.unknown, accepted)
		if len(got) == 0 || got[0] != tc.want {
			t.Errorf("suggestArgNames(%q) = %v, want %q first (%s)", tc.unknown, got, tc.want, tc.why)
		}
	}

	// A name resembling nothing suggests nothing: a wrong guess is worse than
	// none, because the caller will act on it.
	if got := suggestArgNames("zzzqqq_unrelated", accepted); len(got) != 0 {
		t.Errorf("suggestArgNames on an unrelated name = %v, want no suggestions", got)
	}
}

// TestDeclaredArgNamesReadsRawInputSchema covers mcp-go's hand-written-schema
// escape hatch. Nothing in this repo uses it today; a tool that started to
// would otherwise declare zero properties here and refuse every argument it has.
func TestDeclaredArgNamesReadsRawInputSchema(t *testing.T) {
	tool := mcp.NewTool("raw_schema_tool")
	tool.RawInputSchema = []byte(`{"type":"object","properties":{"alpha":{"type":"string"}}}`)

	got, err := declaredArgNames(tool)
	if err != nil {
		t.Fatalf("declaredArgNames: %v", err)
	}
	if _, ok := got["alpha"]; !ok || len(got) != 1 {
		t.Fatalf("declaredArgNames read %v, want exactly {alpha}", sortedNames(got))
	}

	tool.RawInputSchema = []byte(`{ this is not json`)
	if _, err := declaredArgNames(tool); err == nil {
		t.Fatal("an unparseable RawInputSchema was accepted; a tool whose accepted set " +
			"is unknown cannot be protected, so registering it must fail")
	}
}

// resultText pulls the text payload out of a tool result.
func resultText(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	if res == nil {
		t.Fatal("nil tool result")
	}
	var b strings.Builder
	for _, c := range res.Content {
		if text, ok := c.(mcp.TextContent); ok {
			b.WriteString(text.Text)
		}
	}
	return b.String()
}
