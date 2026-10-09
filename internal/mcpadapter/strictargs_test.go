package mcpadapter

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"reflect"
	"sort"
	"strings"
	"testing"

	gomcpserver "github.com/hollis-labs/libs/plugin-mcp/go-mcp/server"
	"github.com/hollis-labs/tesseract/internal/event"
	"github.com/hollis-labs/tesseract/internal/knowledge"
	"github.com/hollis-labs/tesseract/internal/memory"
	"github.com/hollis-labs/tesseract/internal/workspace"
	"go.opentelemetry.io/otel/trace"
)

// A traceparent in the shape go-otel's InjectMCP writes: version-trace-span-flags,
// sampled. Stated as a literal rather than built from the propagator, so the
// test still fails if the parsing changes to agree with itself.
const sampledTraceparent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"

func TestTetherProvenanceIsNormalizedBeforeArgumentEarlyReturns(t *testing.T) {
	for _, args := range []any{nil, map[string]any{}, map[string]any{"_traceparent": sampledTraceparent}} {
		var req map[string]any
		if m, ok := args.(map[string]any); ok {
			req = m
		}
		ctx := gomcpserver.WithMeta(context.Background(), map[string]any{
			"unrelated":         "preserved",
			tetherProvenanceKey: map[string]any{"schema_version": float64(1), "session_id": "tether-session", "workstream_id": "ws-12"},
		})
		handler := gatewayMetadataMiddleware(slog.Default(), func(ctx context.Context, got map[string]any) (any, error) {
			wc := memory.WriteContextFromContext(ctx)
			if wc == nil || wc.Issuer != "tether" || wc.Verification != "unverified" || wc.SessionID != "tether-session" || wc.WorkstreamID != "ws-12" {
				t.Fatalf("normalized context = %#v", wc)
			}
			if gomcpserver.MetaFromContext(ctx)["unrelated"] != "preserved" {
				t.Fatalf("unrelated _meta changed: %#v", gomcpserver.MetaFromContext(ctx))
			}
			return "{}", nil
		})
		if _, err := handler(ctx, req); err != nil {
			t.Fatal(err)
		}
	}
}

func TestMalformedTetherProvenanceIsDiscardedWithBoundedDiagnostic(t *testing.T) {
	secret := strings.Repeat("sensitive-", 200)
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	ctx := gomcpserver.WithMeta(context.Background(), map[string]any{
		tetherProvenanceKey: map[string]any{"schema_version": float64(1), "session_id": secret},
	})
	handler := gatewayMetadataMiddleware(logger, func(ctx context.Context, _ map[string]any) (any, error) {
		if wc := memory.WriteContextFromContext(ctx); wc != nil {
			t.Fatalf("malformed context reached handler: %#v", wc)
		}
		return "{}", nil
	})
	if _, err := handler(ctx, map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if got := logs.String(); !strings.Contains(got, "reason=oversized_or_unencodable") || strings.Contains(got, secret) || len(got) > 512 {
		t.Fatalf("diagnostic was not bounded and content-free: %q", got)
	}
}

func TestTetherProvenanceParserIsClosedAndOptional(t *testing.T) {
	tests := []struct {
		name     string
		envelope any
		reason   string
	}{
		{"not object", "x", "not_object"},
		{"unknown version", map[string]any{"schema_version": float64(2), "session_id": "s"}, "unsupported_schema_version"},
		{"unknown field", map[string]any{"schema_version": float64(1), "session_id": "s", "claimed_verified": true}, "unknown_field"},
		{"missing session", map[string]any{"schema_version": float64(1)}, "invalid_session_id"},
		{"padded workstream", map[string]any{"schema_version": float64(1), "session_id": "s", "workstream_id": " ws"}, "invalid_workstream_id"},
		{"null workstream", map[string]any{"schema_version": float64(1), "session_id": "s", "workstream_id": nil}, "invalid_workstream_id"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, reason := parseTetherProvenance(map[string]any{tetherProvenanceKey: tc.envelope})
			if reason != tc.reason || got.SessionID != "" {
				t.Fatalf("got context=%#v reason=%q, want discarded reason=%q", got, reason, tc.reason)
			}
		})
	}
}

func TestWriteToolsRefuseForgedProvenanceArguments(t *testing.T) {
	srv := registeredSurface(t)
	for _, name := range []string{"memory_write", "knowledge_write", "event_write", "workspace_write"} {
		for _, argument := range []string{"provenance", "verification"} {
			res, err := srv.CallTool(context.Background(), name, map[string]any{argument: "verified"})
			if err != nil || !strings.Contains(mustJSONText(t, res), argument) {
				t.Fatalf("%s accepted %s: result=%#v err=%v", name, argument, res, err)
			}
		}
	}
}

// registeredSurface returns a server carrying every tool this adapter exposes,
// wired against a throwaway store.
func registeredSurface(t *testing.T) *gomcpserver.Server {
	t.Helper()
	cs := newTestStore(t)
	ms := memory.NewStore(cs.DB(), nil, "", 0, memory.NoopQueue{})
	a := New(cs, "")
	a.MemoryStore = ms
	a.KnowledgeStore = knowledge.New(ms)
	a.EventStore = event.New(ms)
	a.WorkspaceStore = workspace.NewStore(cs.DB())

	srv := gomcpserver.NewServer("strictargs", "0.0.0")
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
	defs := srv.ToolDefinitions()
	if len(defs) == 0 {
		t.Fatal("registered zero tools — a clean result here would be meaningless")
	}

	names := make([]string, 0, len(defs))
	for _, def := range defs {
		names = append(names, def.Name)
	}
	sort.Strings(names)

	const undeclared = "definitely_not_a_declared_argument"
	for _, name := range names {
		st, _ := toolDef(srv, name)
		if _, declared := toolSchemaProperties(st.InputSchema)[undeclared]; declared {
			t.Fatalf("tool %q declares the probe name; pick another probe", name)
		}

		res, err := srv.CallTool(context.Background(), name, map[string]any{undeclared: "x"})
		if err != nil {
			t.Errorf("tool %q: handler returned a transport error for an undeclared argument: %v", name, err)
			continue
		}
		text := mustJSONText(t, res)
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
	if _, ok := toolDef(srv, "tesseract_skills"); !ok {
		t.Fatal("tesseract_skills is not registered")
	}

	res, err := srv.CallTool(context.Background(), "tesseract_skills", map[string]any{
		"_traceparent": sampledTraceparent,
		"_tracestate":  "hollis=abc",
	})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	text := mustJSONText(t, res)
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

	res, err := srv.CallTool(context.Background(), "tesseract_skills", map[string]any{"_not_a_gateway_key": "x"})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if text := mustJSONText(t, res); !strings.Contains(text, "_not_a_gateway_key") {
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
	handler := gatewayMetadataMiddleware(slog.Default(), func(ctx context.Context, _ map[string]any) (any, error) {
		seen = gatewayMetadataFromContext(ctx)
		remote = trace.SpanContextFromContext(ctx)
		return "{}", nil
	})

	req := map[string]any{
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
	handler := gatewayMetadataMiddleware(slog.Default(), func(_ context.Context, r map[string]any) (any, error) {
		got = r
		return "{}", nil
	})

	if _, err := handler(context.Background(), want); err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("arguments were rewritten on a call carrying no gateway metadata.\n  got:  %v\n  want: %v", got, want)
	}
}

// TestStripDoesNotMutateTheCallersMap: go-mcp hands one map down the chain, so
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

// TestRetiredPayloadDataIsRefusedWithASuggestion is the motivating case, end to
// end: the retired `payload_data` argument must fail with its new name,
// including when the caller also supplies `data`.
//
// The assertion is on the guidance, not only on the refusal. "unexpected key"
// leaves the caller to guess, and guessing is what produced the key.
func TestRetiredPayloadDataIsRefusedWithASuggestion(t *testing.T) {
	srv := registeredSurface(t)
	if _, ok := toolDef(srv, "memory_write"); !ok {
		t.Fatal("memory_write is not registered")
	}

	res, err := srv.CallTool(context.Background(), "memory_write", map[string]any{
		"namespace":       "user/chrispian/memory/notes",
		"memory_key":      "test.data_typo",
		"author_agent_id": "claude",
		"trigger":         "explicit",
		"session_id":      "sess-typo",
		"derived_from":    "user",
		"payload_summary": "a summary",
		"payload_data":    map[string]any{"severity": "high"},
		"data":            map[string]any{"severity": "low"},
	})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	text := mustJSONText(t, res)
	for _, want := range []string{"`payload_data`", "`data`", "memory_write", "now named"} {
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
		{"memory_write", "payload_data", "now named `data`"},
		{"memory_write", "payload_data_schema_hash", "now named `data_schema_hash`"},
		{"knowledge_write", "payload_data", "now named `data`"},
		{"knowledge_write", "payload_data_schema_hash", "now named `data_schema_hash`"},
		{"event_write", "payload_data", "now named `data`"},
		{"event_write", "payload_data_schema_hash", "now named `data_schema_hash`"},
		{"memory_write", "origin", "derived_from"},
		{"tesseract_recall", "origins", "derived_from"},
		{"context_status_set", "to_status", "`status`"},
		{"context_plan", "budget_tokens", "max_tokens_estimate"},
		{"context_plan", "budget_items", "max_items"},
		{"context_pack", "max_tokens", "max_tokens_estimate"},
		{"context_registry_list", "namespace", "`name`"},
		{"context_pack", "payload_mode", "payload_max_bytes=512"},
	} {
		if _, ok := toolDef(srv, tc.tool); !ok {
			t.Errorf("tool %q is not registered", tc.tool)
			continue
		}
		res, err := srv.CallTool(context.Background(), tc.tool, map[string]any{tc.arg: "x"})
		if err != nil {
			t.Errorf("%s(%s): handler error: %v", tc.tool, tc.arg, err)
			continue
		}
		text := mustJSONText(t, res)
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

	for toolName, retired := range retiredArgGuidance {
		st, ok := toolDef(srv, toolName)
		if !ok {
			t.Errorf("retiredArgGuidance names tool %q, which is not registered", toolName)
			continue
		}
		declared, err := declaredArgNames(gomcpserver.Tool{Name: st.Name, InputSchema: st.InputSchema})
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
		"data", "data_schema_hash", "payload_summary", "tags",
	}
	for _, tc := range []struct {
		unknown string
		want    string
		why     string
	}{
		{"summary", "payload_summary", "the short name of a prefixed argument"},
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

// TestDeclaredArgNamesRejectsASchemaShapeItCannotIntrospect covers go-mcp's
// hand-written-schema escape hatch (Tool.InputSchema is `any`; anything that
// JSON-marshals to valid JSON Schema is accepted on the wire). Nothing in this
// repo builds a schema that way today — every tool goes through schema.go's
// inputSchema, a map[string]any — but declaredArgNames must refuse to guess at
// a tool built some other way rather than silently declaring zero properties
// and refusing every argument it has.
func TestDeclaredArgNamesRejectsASchemaShapeItCannotIntrospect(t *testing.T) {
	tool := gomcpserver.Tool{
		Name:        "raw_schema_tool",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"alpha":{"type":"string"}}}`),
	}
	if _, err := declaredArgNames(tool); err == nil {
		t.Fatal("a tool whose InputSchema is not the map[string]any shape this package builds was accepted; " +
			"a tool whose accepted set is unknown cannot be protected, so registering it must fail")
	}
}
