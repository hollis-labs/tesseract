package mcpadapter

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// Strict MCP arguments (CW-20260912-0055).
//
// mcp-go does not set `additionalProperties: false` on a tool's input schema
// and does not validate arguments unless a server opts in, so an argument name
// Tesseract does not declare USED TO ARRIVE at the handler where nothing read
// it. A caller who sent `data` instead of `payload_data` got a successful write
// with the object missing, where the HTTP peer answered 400 for the same
// mistake. This file closes that asymmetry from the MCP side.
//
// The check is server-side, in a middleware every registered tool routes
// through. It does NOT depend on publishing `additionalProperties: false`: the
// published schema is advisory, and enforcement is ours. See
// the note below on advertised-versus-enforced strictness for why the two are
// not the same decision and why only one of them has been taken.
//
// Two things run ahead of it, in this order:
//
//  1. stripGatewayMetadata removes the gateway's transport keys, because they
//     are not caller arguments and refusing them would break every call made
//     through mux (see acceptedGatewayMetadata).
//  2. The go-mcp-sanitize middleware, because it can WRITE an argument slot —
//     a leaked `<parameter name="OTHER">…</parameter>` fragment inside a
//     free-text value is moved into args["OTHER"]. Validating before that ran
//     would guard the call the client sent rather than the call the handler
//     receives, and the recovered name is exactly the kind that is misspelled.

// WHY THE PUBLISHED SCHEMAS STAY PERMISSIVE WHILE THE SERVER ENFORCES.
//
// Advertising `additionalProperties: false` and enforcing it are two separate
// decisions, and the risk in each is different. The one that could have bitten
// is a gateway validating our OUTBOUND calls against our advertised schema:
// that would reject every proxied call at mux, before any middleware of ours
// ran, and the strip below would be dead code.
//
// Measured, 2026-09-12, against mux commit 268340c — the installed binary:
// mux's ProxyRouter (tether internal/mcpadapter/proxy.go) forwards a tool call
// with no validation of any kind, and mux does not even compile a JSON Schema
// validator (`go mod why github.com/google/jsonschema-go` reports the main
// module does not need the package). The failure Tangent hit,
//
//	validating "arguments": validating root: unexpected additional properties ["_traceparent"]
//
// came from Tangent's OWN process: the text is google/jsonschema-go's
// (jsonschema/validate.go), wrapped by modelcontextprotocol/go-sdk's server
// (mcp/server.go, `validating "arguments"`), both direct dependencies of
// Tangent and neither of them linked into mux. Tangent's server-side strip
// fixing it is the same evidence from the other end.
//
// So advertising strictness would be safe today, and it is still not done,
// for two reasons that outlive the measurement.
//
// It would buy no enforcement. mcp-go honors `additionalProperties: false`
// only through its own opt-in validator, and that validator's message names
// the offending key and nothing else. This middleware's names the key, what it
// should have been, and the tool's whole declared set — which is the
// deliverable. Publishing the flag would hand some clients the poorer error
// first and change nothing about what is accepted.
//
// And while mux injects into `params.arguments`, `additionalProperties: false`
// is a contract THE GATEWAY ITSELF BREAKS on every call it forwards. Publishing
// a rule that the transport violates invites the next validator anywhere in
// this chain — a stricter client, a future mux, a different gateway — to reject
// calls that are in fact fine. Tighten it in the same change that deletes the
// strip, once the `_meta` move is deployed: at that point the advertised schema
// and the wire agree, and the flag is documentation rather than a trap.

// acceptedGatewayMetadata is the whole set of argument keys Tesseract treats as
// gateway transport metadata rather than as caller arguments.
//
//   - `_traceparent`: the W3C trace context mux writes into the arguments map
//     (github.com/hollis-labs/go-otel/propagation.InjectMCP). Parsed and
//     recorded as the upstream trace link so correlation survives the strip.
//   - `_tracestate`: injected by the same code only when the upstream trace
//     carries vendor state. Stripped and parsed alongside the traceparent;
//     never stored and never treated as caller input.
//
// Adding a key here is a deliberate act that needs the injector's source as
// evidence, the way both of these do. An underscore prefix is NOT a general
// escape hatch: an unknown `_foo` is refused exactly like an unknown `foo`,
// which is what stops this becoming a silent bypass of the strictness above.
//
// WHEN THIS COMES OUT. CW-20260907-0026 moves the injection to `params._meta`,
// where the MCP spec puts request metadata. The strip may be deleted once
// EVERY DEPLOYED mux is past that change — not once the task is merged. The
// difference is not pedantry: a merged-but-undeployed fix is precisely what
// broke eight sessions on the `derived_from` rename (CW-20260912-0047), and a
// premature deletion here fails every Tesseract call an older mux forwards.
// mcp.CallToolParams.Meta already exists in mcp-go v1.0.0, so reading trace
// context from `_meta` needs no library bump when that day comes — only this
// map emptied and the read moved.
var acceptedGatewayMetadata = map[string]bool{
	"_traceparent": true,
	"_tracestate":  true,
}

// GatewayMetadataKeys returns the accepted key set, sorted, for documentation
// and for the test that asserts the set has not grown by accident.
func GatewayMetadataKeys() []string {
	keys := make([]string, 0, len(acceptedGatewayMetadata))
	for key := range acceptedGatewayMetadata {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// gatewayTraceContext parses the stripped keys with an EXPLICIT W3C propagator
// rather than otel.GetTextMapPropagator().
//
// go-otel's ExtractMCP reads the global propagator, which is nil-safe but
// silently returns nothing when no process has configured one — and
// `tesseract mcp` is a stdio child that may run without OTel initialized at
// all. The injector builds the header by hand as W3C traceparent, so parsing
// it as W3C is exact, and doing it explicitly means the upstream link is
// recorded whether or not this process exports traces.
var gatewayTraceContext = propagation.TraceContext{}

// gatewayMetadata is what one call's transport keys amounted to once parsed.
//
// It is a struct with one field rather than a bare trace.SpanContext because
// the strip is a CHANNEL, not just a filter, and the difference is about to
// matter. `_traceparent` is read, recorded and otherwise dropped — nothing
// downstream needs it. The next key on this transport will not be like that:
// CW-20260912-0024 wants mux to stamp provenance an agent cannot forge —
// verified client identity, session id — and a stamp is only worth having if it
// reaches WriteInput. Returning "the args, minus some keys" would have made
// remove-and-continue the only expressible outcome and forced that work to
// start by undoing this.
//
// So a parsed key lands here, and gatewayMetadataFromContext is how a handler
// reaches it. Nothing is accepted today that was not accepted before: the
// allowlist is still exactly the two trace keys.
//
// A CONSTRAINT ON WHOEVER FILLS THIS IN: absent must never imply forged. The
// HTTP surface, the CLI and a directly-spawned MCP child all bypass the proxy,
// and standalone-with-no-daemon is a supported configuration — so a stamp can
// be trusted when present and can never be required.
type gatewayMetadata struct {
	// UpstreamTrace is the caller's span context, invalid when the call
	// carried no `_traceparent` or one that did not parse.
	UpstreamTrace trace.SpanContext
}

// gatewayMetadataContextKey is unexported and uniquely typed, so nothing
// outside this package can write the channel by guessing a key. That is the
// property a provenance stamp will depend on: what arrives here arrived from
// the middleware, never from a caller's arguments.
type gatewayMetadataContextKey struct{}

// contextWithGatewayMetadata attaches one call's parsed transport metadata.
func contextWithGatewayMetadata(ctx context.Context, meta gatewayMetadata) context.Context {
	return context.WithValue(ctx, gatewayMetadataContextKey{}, meta)
}

// gatewayMetadataFromContext returns what the gateway sent with this call, and
// the zero value when the call came through a surface that sends none — which
// is most of them, and is not an error.
func gatewayMetadataFromContext(ctx context.Context) gatewayMetadata {
	meta, _ := ctx.Value(gatewayMetadataContextKey{}).(gatewayMetadata)
	return meta
}

// stripGatewayMetadata removes the accepted keys from a call's arguments and
// returns the remaining arguments, what those keys parsed to, and whether
// anything was removed.
//
// The input map is never mutated: mcp-go hands the same map to every
// middleware in the chain, and a handler that reads arguments after this ran
// should see the caller's own call, not a map something else edited underneath
// it. A call carrying none of the accepted keys is returned unchanged and
// costs one map lookup per key.
func stripGatewayMetadata(args map[string]any) (map[string]any, gatewayMetadata, bool) {
	present := false
	for key := range acceptedGatewayMetadata {
		if _, ok := args[key]; ok {
			present = true
			break
		}
	}
	if !present {
		return args, gatewayMetadata{}, false
	}

	carrier := propagation.MapCarrier{}
	stripped := make(map[string]any, len(args))
	for key, value := range args {
		if !acceptedGatewayMetadata[key] {
			stripped[key] = value
			continue
		}
		// A non-string value here is the gateway's bug, not the caller's.
		// Strip it either way and record no upstream link rather than a
		// fabricated one.
		if header, ok := value.(string); ok {
			carrier.Set(strings.TrimPrefix(key, "_"), header)
		}
	}
	meta := gatewayMetadata{
		UpstreamTrace: trace.SpanContextFromContext(
			gatewayTraceContext.Extract(context.Background(), carrier)),
	}
	return stripped, meta, true
}

// retiredArgGuidance maps a tool's RETIRED argument names to the sentence that
// tells a caller what to send instead.
//
// This replaces nine scattered rejectRetiredArg call sites. The scattering was
// the problem, not the knowledge: wholesale rejection subsumes the DETECTION of
// a retired name — an undeclared name is now refused wherever it appears — but
// it cannot produce "`origin` is now `derived_from`", and that sentence is the
// entire value of those sites. So the guidance moved here and the detection
// went away, rather than the other way round.
//
// KEYED BY TOOL, NOT GLOBALLY, and that is not a stylistic choice. `namespace`
// is declared by fourteen tools and retired on exactly one. `budget_tokens` is
// live on tesseract_recall, tesseract_history and tesseract_get — where it is
// the response serialization ceiling — and retired on context_plan, where it
// was an assembly budget at a different default. A global list would refuse a
// live argument on the strength of a name that means something else elsewhere,
// which is a worse defect than the one being fixed.
//
// This is NOT an accept-list, and the difference is what keeps it from
// reproducing the hand-maintained-list defect: nothing here decides what is
// ACCEPTED. The accepted set is derived from each tool's own schema at
// registration time (declaredArgNames). An entry that goes stale — because the
// name was re-declared, say — degrades to guidance nobody reaches, and
// TestRetiredArgsAreNotDeclared fails rather than letting it rot.
var retiredArgGuidance = map[string]map[string]string{
	"memory_write": {
		// `origin` until 2026-09-12. An ignored `origin` leaves `derived_from`
		// empty, so a caller who supplied a value is told a REQUIRED FIELD IS
		// MISSING — a confusing error about the wrong field. Had the field
		// carried a default, the write would have succeeded at the wrong
		// ranking weight instead, which is worse.
		"origin": "this field is now named `derived_from`. The five values are unchanged " +
			"(user, feedback, project, reference, observation) — only the name moved, " +
			"because `origin` read as *who originated this* and was being filled in as " +
			"an authorship claim. It is a recall ranking multiplier, so the value matters " +
			"beyond labeling; see `tesseract_skills memory`.",
	},
	"tesseract_recall": {
		// `origins` until 2026-09-12, and the sharpest case on this list: an
		// ignored recall FILTER does not fail, it silently widens the result
		// set. The caller gets back the rows it asked to exclude, ranked and
		// plausible, with no error anywhere.
		"origins": "this filter is now named `derived_from` and still takes a JSON array of the " +
			"same five values (user, feedback, project, reference, observation). " +
			"Only the name moved.",
	},
	"context_pack": {
		// The list arm's old spelling of the token budget. Ignoring it
		// silently restores the 8000 default under a caller who asked for less.
		"max_tokens":   "the token budget is named `max_tokens_estimate` on both shapes.",
		"payload_mode": retiredPayloadModeGuidance,
	},
	"context_status_set": {
		// The retired context_status_promote spelled the target `to_status`.
		// An ignored `to_status` leaves `status` empty, which means "advance
		// one step" — so the caller gets `reviewed` where they asked for
		// `canonical`, and a success envelope saying so.
		"to_status": "the target status is now named `status`; omit it to advance one step.",
	},
	"context_plan": {
		// This tool spelled the assembly budget `budget_items` /
		// `budget_tokens` at a different token default (4000, against 8000
		// everywhere else), so an ignored old name does not merely drop a
		// knob — it silently doubles the budget the caller asked for.
		"budget_items": "the item budget is now named `max_items`, matching context_pack and POST /v1/context/packet.",
		"budget_tokens": "the token budget is now named `max_tokens_estimate` and defaults to 8000 (was 4000), " +
			"matching context_pack and POST /v1/context/packet. " +
			"`budget_tokens` still exists on tesseract_recall / tesseract_history / tesseract_get, " +
			"where it is a different knob — the response serialization ceiling, not an assembly budget.",
		"payload_mode": retiredPayloadModeGuidance,
	},
	"context_registry_list": {
		// The retired context_namespace_show spelled this `namespace`.
		// Ignoring it answers the whole-list question for a caller who asked
		// about one namespace.
		"namespace": "kind=namespaces names a single namespace with `name`, not `namespace`.",
	},
}

// retiredPayloadModeGuidance is shared by the two packet-shaped tools, which
// retired the same vocabulary on the same day.
//
// It replaces rejectRetiredPayloadMode, which those two handlers called. That
// function accepted `payload_mode: "full"` as a no-op — it was the default, and
// meant what it means everywhere else — and refused every other value. Neither
// tool DECLARES payload_mode, so the generic check now refuses the name
// outright and the no-op is gone with it.
//
// That is a real contract change, in the direction this task exists to move:
// "accepted, read by nothing, reported as success" is the defect, and `full`
// was the last instance of it on these tools. The projection knob itself is
// unaffected — tesseract_recall and event_list declare payload_mode and keep it.
const retiredPayloadModeGuidance = "payload_mode is not a knob on this tool. It was never read here — " +
	"only \"full\" was accepted, and only as a no-op. The former `payload_mode=head_only`, " +
	"which cut a payload mid-JSON, is now `payload_max_bytes=512`. " +
	"payload_mode survives as the keys|summary|full projection on the recall and lookup tools."

// declaredArgNames reads the set of argument names a tool declares off the
// tool's OWN schema.
//
// There is exactly one path from a registered tool to the names it accepts, and
// this is it. A hand-maintained accept-list is what retiredArgGuidance's
// predecessor was — a record of past mistakes rather than the current contract —
// and building a second one keyed by the same hands would reproduce the defect
// in a new place. A tool that gains an argument gains it here on the same line
// that declares it.
//
// RawInputSchema is mcp-go's escape hatch for a hand-written schema; nothing in
// this repo uses it, and a tool that started to would otherwise declare zero
// properties here and refuse every argument it has. It is parsed rather than
// assumed away, and a schema that cannot be parsed is a registration failure:
// registering a tool whose accepted set is unknown is the silent-drop defect
// with extra steps.
func declaredArgNames(t mcp.Tool) (map[string]struct{}, error) {
	if len(t.RawInputSchema) > 0 {
		var raw struct {
			Properties map[string]json.RawMessage `json:"properties"`
		}
		if err := json.Unmarshal(t.RawInputSchema, &raw); err != nil {
			return nil, fmt.Errorf("tool %q: RawInputSchema is not valid JSON: %w", t.Name, err)
		}
		out := make(map[string]struct{}, len(raw.Properties))
		for name := range raw.Properties {
			out[name] = struct{}{}
		}
		return out, nil
	}
	out := make(map[string]struct{}, len(t.InputSchema.Properties))
	for name := range t.InputSchema.Properties {
		out[name] = struct{}{}
	}
	return out, nil
}

// sortedNames returns a set's members in a stable order, so an error message
// reads the same on every call and a test can assert its text.
func sortedNames(set map[string]struct{}) []string {
	out := make([]string, 0, len(set))
	for name := range set {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// strictArgsMiddleware refuses any argument the tool does not declare.
//
// The declared set is read once at registration time, not per call: the schema
// cannot change after the tool is registered, and deriving it here keeps the
// per-call cost to one map lookup per argument.
func strictArgsMiddleware(t mcp.Tool) (func(server.ToolHandlerFunc) server.ToolHandlerFunc, error) {
	declared, err := declaredArgNames(t)
	if err != nil {
		return nil, err
	}
	accepted := sortedNames(declared)
	retired := retiredArgGuidance[t.Name]

	return func(next server.ToolHandlerFunc) server.ToolHandlerFunc {
		return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			args := req.GetArguments()
			// Arguments that are not a JSON object carry no names to check.
			// mcp-go answers that shape for itself; adding a second opinion
			// here would only change which message a caller sees.
			if len(args) == 0 {
				return next(ctx, req)
			}
			unknown := make([]string, 0, 2)
			for name := range args {
				if _, ok := declared[name]; !ok {
					unknown = append(unknown, name)
				}
			}
			if len(unknown) == 0 {
				return next(ctx, req)
			}
			sort.Strings(unknown)
			return unknownArgError(t.Name, unknown, accepted, retired), nil
		}
	}, nil
}

// unknownArgError builds the refusal.
//
// A rejection that only says "unexpected key" leaves the caller to guess, and
// guessing is what produced the key. So the message answers all three
// questions a caller actually has: what was refused, what it probably should
// have been, and what this tool does accept.
func unknownArgError(toolName string, unknown, accepted []string, retired map[string]string) *mcp.CallToolResult {
	var b strings.Builder
	for i, name := range unknown {
		if i > 0 {
			b.WriteString(" ")
		}
		if guidance, ok := retired[name]; ok {
			fmt.Fprintf(&b, "`%s` is not an argument of %s. %s", name, toolName, guidance)
			continue
		}
		fmt.Fprintf(&b, "`%s` is not an argument of %s.", name, toolName)
		if suggestions := suggestArgNames(name, accepted); len(suggestions) > 0 {
			fmt.Fprintf(&b, " Did you mean %s?", quotedList(suggestions))
		}
	}
	fmt.Fprintf(&b, " This tool accepts: %s.", strings.Join(accepted, ", "))
	b.WriteString(" Arguments a tool does not declare are refused rather than dropped, " +
		"so that a misspelled name cannot produce a successful call with the value missing.")
	return toolError(codeValidationError, b.String())
}

// quotedList renders suggestions as `a` or `b`.
func quotedList(names []string) string {
	quoted := make([]string, 0, len(names))
	for _, name := range names {
		quoted = append(quoted, "`"+name+"`")
	}
	if len(quoted) == 1 {
		return quoted[0]
	}
	return strings.Join(quoted[:len(quoted)-1], ", ") + " or " + quoted[len(quoted)-1]
}

// maxArgSuggestions caps how many alternatives a refusal offers. Three is
// enough to cover a genuine near-miss and few enough that the answer still
// reads as an answer; the full accepted set follows it either way.
const maxArgSuggestions = 3

// suggestArgNames ranks a tool's declared names by how likely each is to be
// what the caller meant.
//
// Two signals, because the motivating mistakes have two shapes. `data` for
// `payload_data` is a caller reaching for the short name of a prefixed
// argument — no edit distance catches that, but containment does. `namespcae`
// for `namespace` is a typo — containment misses it and edit distance is
// exactly right. Both are ranked before length so the shortest containing name
// wins: `data` suggests `payload_data`, not `payload_data_schema_hash`.
func suggestArgNames(unknown string, accepted []string) []string {
	type scored struct {
		name string
		rank int
		size int
	}
	lower := strings.ToLower(unknown)
	var matches []scored
	for _, name := range accepted {
		candidate := strings.ToLower(name)
		switch {
		case candidate == lower:
			// A case-only difference: the caller has the right name.
			matches = append(matches, scored{name, 0, len(name)})
		case strings.Contains(candidate, lower) || strings.Contains(lower, candidate):
			matches = append(matches, scored{name, 1, len(name)})
		default:
			distance := editDistance(lower, candidate)
			if distance <= argTypoTolerance(lower) {
				matches = append(matches, scored{name, 2 + distance, len(name)})
			}
		}
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].rank != matches[j].rank {
			return matches[i].rank < matches[j].rank
		}
		if matches[i].size != matches[j].size {
			return matches[i].size < matches[j].size
		}
		return matches[i].name < matches[j].name
	})
	out := make([]string, 0, maxArgSuggestions)
	for _, match := range matches {
		if len(out) == maxArgSuggestions {
			break
		}
		out = append(out, match.name)
	}
	return out
}

// argTypoTolerance scales the accepted edit distance with the length of the
// name, so `id` does not suggest every two-letter argument while `payload_body`
// still tolerates a slip or two.
func argTypoTolerance(name string) int {
	switch {
	case len(name) <= 4:
		return 1
	case len(name) <= 8:
		return 2
	default:
		return 3
	}
}

// editDistance is the Levenshtein distance between two strings, over bytes.
// Argument names are ASCII by construction — they are Go identifiers in a
// schema — so bytes and characters are the same thing here.
func editDistance(a, b string) int {
	if a == b {
		return 0
	}
	if len(a) == 0 {
		return len(b)
	}
	if len(b) == 0 {
		return len(a)
	}
	prev := make([]int, len(b)+1)
	curr := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		curr[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			curr[j] = min(prev[j]+1, min(curr[j-1]+1, prev[j-1]+cost))
		}
		prev, curr = curr, prev
	}
	return prev[len(b)]
}
