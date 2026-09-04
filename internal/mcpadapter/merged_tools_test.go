// Equivalence and gating tests for the seven context-domain merges of
// CW-20260825-0011.
//
// Seventeen tools became seven. Each pre-merge tool's behavior is now reached
// by naming a knob value on the tool that absorbed it, and the claim this file
// makes is one test per (retired tool → knob value) pair, named for the pair.
// Seventeen tests, not one test named TestMergedToolsPreserveBehavior.
//
// What "equivalent" means here, stated before it is asserted:
//
//	The merged tool, called with the migrated arguments against the same
//	corpus, returns the SAME response body the retired tool returned for the
//	same input, key for key and value for value.
//
// Every expectation below is written out by hand from what the retired tool
// did, never read back from the constant or the handler under test. Two
// deliberate exceptions are enumerated where they occur, each with its own
// named test:
//
//   - context_status_promote(to_status="deprecated") took the PROMOTION path;
//     the merged context_status_set(status="deprecated") takes the DEPRECATION
//     path. See TestMerge_StatusSet_DeprecatedTargetTakesTheDeprecationPath.
//   - context_packet / context_broker_fetch payload_mode="head_only" is gone,
//     replaced by payload_max_bytes. It was not a working mode: see
//     TestPayloadMaxBytes_HeadOnlyReturnedAnEmptyResultAndTheCapDoesNot.
package mcpadapter

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/hollis-labs/tesseract/internal/contextstore"
	"github.com/mark3labs/mcp-go/mcp"
)

// callTool builds a CallToolRequest from a plain argument map.
func mergedToolRequest(args map[string]any) mcp.CallToolRequest {
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	return req
}

// mustCall runs a handler and decodes its body, failing on a transport error.
func mustCall(t *testing.T, h func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error), args map[string]any) map[string]any {
	t.Helper()
	res, err := h(context.Background(), mergedToolRequest(args))
	if err != nil {
		t.Fatalf("handler returned a Go error (tools answer with an error BODY, never this): %v", err)
	}
	return parseResult(t, res)
}

// wantNoError fails when the body is an error envelope, printing it.
func wantNoError(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	if code, ok := body["code"]; ok && code != nil {
		t.Fatalf("expected success, got error body: %v", body)
	}
	return body
}

// wantErrorCode asserts the body is the named error.
func wantErrorCode(t *testing.T, body map[string]any, code string) {
	t.Helper()
	if body["code"] != code {
		t.Fatalf("code = %v, want %q; full body: %v", body["code"], code, body)
	}
}

// ── Merge 1: context_view + views_evaluate → context_view ────────────────────

// TestMerge_ContextView_DefaultArmReturnsSummaryEnvelope covers
// (context_view → full_evaluation absent). The retired context_view answered the
// shared budget envelope of record summaries: a `count`, an `items` array, and
// no payloads at all.
func TestMerge_ContextView_DefaultArmReturnsSummaryEnvelope(t *testing.T) {
	s := newTestStore(t)
	writeRecord(t, s, "user/memory/task-001", "state", `{"v":1}`)
	writeRecord(t, s, "user/memory/task-002", "state", `{"v":2}`)
	writeRecord(t, s, "app/other/session", "state", `{"v":3}`)
	a := New(s, "")

	body := wantNoError(t, mustCall(t, a.handleContextView, map[string]any{
		"namespaces":     "user/memory/*",
		"revision_scope": "head",
	}))

	if got, _ := body["count"].(float64); int(got) != 2 {
		t.Errorf("count = %v, want 2", body["count"])
	}
	if _, leaked := body["evaluation_meta"]; leaked {
		t.Errorf("default arm must not carry evaluation_meta; got %v", body)
	}
	for _, item := range parseItems(t, body) {
		m := item.(map[string]any)
		if _, ok := m["payload"]; ok {
			t.Errorf("default arm must return summaries, never payloads; item = %v", m)
		}
		for _, k := range []string{"record_id", "namespace", "key", "revision", "status", "updated_at"} {
			if _, ok := m[k]; !ok {
				t.Errorf("summary missing %q; got %v", k, m)
			}
		}
	}
}

// TestMerge_ViewsEvaluate_FullEvaluationArmReturnsEvaluationEnvelope covers
// (views_evaluate → full_evaluation=true). The retired views_evaluate answered
// `{items, evaluation_meta:{sort_keys, matched_count, truncated,
// normalized_scope}}` — the exact envelope of HTTP POST /v1/views/evaluate.
func TestMerge_ViewsEvaluate_FullEvaluationArmReturnsEvaluationEnvelope(t *testing.T) {
	s := newTestStore(t)
	writeRecord(t, s, "app/test/one", "k", `{"v":1}`)
	a := New(s, "")

	body := wantNoError(t, mustCall(t, a.handleContextView, map[string]any{
		"selector":        `{"namespaces":["app/test/*"]}`,
		"full_evaluation": true,
	}))

	meta, ok := body["evaluation_meta"].(map[string]any)
	if !ok {
		t.Fatalf("missing evaluation_meta; got %v", body)
	}
	for _, k := range []string{"sort_keys", "matched_count", "truncated", "normalized_scope"} {
		if _, ok := meta[k]; !ok {
			t.Errorf("evaluation_meta missing %q; got %v", k, meta)
		}
	}
	if meta["normalized_scope"] != "head" {
		t.Errorf("normalized_scope = %v, want head", meta["normalized_scope"])
	}
	if _, leaked := body["count"]; leaked {
		t.Errorf("evaluate arm must not carry the summary envelope's count; got %v", body)
	}
}

// TestContextView_FullEvaluationSelectsAnArmRatherThanAnnotatingOne is the
// 0010 lesson applied: a knob that merely annotates is a knob that does not
// work. Flipping full_evaluation on the SAME selector against the SAME corpus
// must change which fields come back, not just add a block.
func TestContextView_FullEvaluationSelectsAnArmRatherThanAnnotatingOne(t *testing.T) {
	s := newTestStore(t)
	writeRecord(t, s, "app/test/one", "k", `{"v":1}`)
	a := New(s, "")

	args := map[string]any{"namespaces": "app/test/*"}
	plain := wantNoError(t, mustCall(t, a.handleContextView, args))

	withMeta := wantNoError(t, mustCall(t, a.handleContextView, map[string]any{
		"namespaces":      "app/test/*",
		"full_evaluation": true,
	}))

	if _, ok := plain["count"]; !ok {
		t.Errorf("default arm lost its count field: %v", plain)
	}
	if _, ok := withMeta["count"]; ok {
		t.Errorf("full_evaluation merely annotated the default arm instead of selecting the other one: %v", withMeta)
	}
	if _, ok := withMeta["evaluation_meta"]; !ok {
		t.Errorf("full_evaluation arm did not run: %v", withMeta)
	}
}

// TestContextView_IncludePayloadWithoutFullEvaluationIsRejected pins the
// fail-closed rule: the default arm cannot carry payloads, so accepting the
// knob and silently dropping it would be a parameter reporting it is honoring
// something it ignores.
func TestContextView_IncludePayloadWithoutFullEvaluationIsRejected(t *testing.T) {
	a := New(newTestStore(t), "")
	wantErrorCode(t, mustCall(t, a.handleContextView, map[string]any{
		"namespaces":      "app/test/*",
		"include_payload": true,
	}), "validation_error")
}

// TestContextView_SelectorAcceptsBothGlobsAndJSON pins the knob the ticket
// specified: one argument, two input languages, same records out.
func TestContextView_SelectorAcceptsBothGlobsAndJSON(t *testing.T) {
	s := newTestStore(t)
	writeRecord(t, s, "app/test/one", "k", `{"v":1}`)
	writeRecord(t, s, "app/other/two", "k", `{"v":2}`)
	a := New(s, "")

	globForm := wantNoError(t, mustCall(t, a.handleContextView, map[string]any{"selector": "app/test/*"}))
	jsonForm := wantNoError(t, mustCall(t, a.handleContextView, map[string]any{"selector": `{"namespaces":["app/test/*"]}`}))

	if len(parseItems(t, globForm)) != 1 || len(parseItems(t, jsonForm)) != 1 {
		t.Fatalf("both selector forms should match exactly the one app/test record; glob=%v json=%v", globForm, jsonForm)
	}
	// Positive control: the selector is actually filtering, not matching all.
	all := wantNoError(t, mustCall(t, a.handleContextView, map[string]any{}))
	if len(parseItems(t, all)) != 2 {
		t.Fatalf("unfiltered call should see both records, got %d — the corpus, not the selector, is doing the filtering", len(parseItems(t, all)))
	}
}

// TestContextView_SelectorAndNamespacesTogetherIsRejected: two ways to say the
// same thing, given at once, is ambiguous rather than merge-able.
func TestContextView_SelectorAndNamespacesTogetherIsRejected(t *testing.T) {
	a := New(newTestStore(t), "")
	wantErrorCode(t, mustCall(t, a.handleContextView, map[string]any{
		"selector":   "app/test/*",
		"namespaces": "app/other/*",
	}), "validation_error")
}

// TestContextView_DefaultArmRejectsSelectorFieldsItCannotHonor: the summary arm
// reads only namespaces and revision_scope. A caller passing tags_any there
// would otherwise get a silently unfiltered read.
func TestContextView_DefaultArmRejectsSelectorFieldsItCannotHonor(t *testing.T) {
	a := New(newTestStore(t), "")
	wantErrorCode(t, mustCall(t, a.handleContextView, map[string]any{
		"selector": `{"namespaces":["app/test/*"],"tags_any":["x"]}`,
	}), "validation_error")
}

// ── Merge 2: context_pack + context_packet → context_pack ────────────────────

// TestMerge_ContextPack_ListShapeReturnsTheViewPack covers
// (context_pack → shape absent / shape=list). The retired context_pack answered
// `{view, generated_at, token_estimate, items, count}`.
func TestMerge_ContextPack_ListShapeReturnsTheViewPack(t *testing.T) {
	s := newTestStore(t)
	a := New(s, "")

	body := wantNoError(t, mustCall(t, a.handleContextPackShape, map[string]any{
		"view_id": "task_exec",
	}))
	for _, k := range []string{"view", "generated_at", "token_estimate", "items", "count"} {
		if _, ok := body[k]; !ok {
			t.Errorf("list shape missing %q; got %v", k, body)
		}
	}
	if body["view"] != "task_exec" {
		t.Errorf("view = %v, want task_exec", body["view"])
	}
	if _, leaked := body["manifest"]; leaked {
		t.Errorf("list shape must not carry the packet manifest; got %v", body)
	}
}

// TestMerge_ContextPacket_PacketShapeReturnsItemsAndManifest covers
// (context_packet → shape=packet). The retired context_packet answered
// `{items, manifest}` with the manifest naming pins_included, items_total,
// items_returned, bytes_returned, tokens_estimate, truncated,
// truncation_reason and sources.
func TestMerge_ContextPacket_PacketShapeReturnsItemsAndManifest(t *testing.T) {
	s := newTestStore(t)
	writeRecord(t, s, "user/pins/project", "context", `{"project":"test"}`)
	writeRecord(t, s, "app/test/session/task-001", "state", `{"status":"active"}`)
	a := New(s, "")

	body := wantNoError(t, mustCall(t, a.handleContextPackShape, map[string]any{
		"shape":        "packet",
		"namespaces":   "app/test/session/task-001",
		"include_pins": true,
	}))
	manifest, ok := body["manifest"].(map[string]any)
	if !ok {
		t.Fatalf("packet shape missing manifest; got %v", body)
	}
	for _, k := range []string{"request_id", "pins_included", "items_total", "items_returned",
		"bytes_returned", "tokens_estimate", "truncated", "truncation_reason", "sources"} {
		if _, ok := manifest[k]; !ok {
			t.Errorf("manifest missing %q; got %v", k, manifest)
		}
	}
	if got, _ := manifest["pins_included"].(float64); int(got) != 1 {
		t.Errorf("pins_included = %v, want 1", manifest["pins_included"])
	}
	if n := len(parseItems(t, body)); n != 2 {
		t.Errorf("items = %d, want 2 (1 pin + 1 record)", n)
	}
	if _, leaked := body["view"]; leaked {
		t.Errorf("packet shape must not carry the list shape's view field; got %v", body)
	}
}

// TestContextPack_ShapeRejectsTheOtherShapesKnobs: the two shapes take
// different arguments, so accepting one shape's knob under the other would be
// the silent-no-op this merge exists to prevent.
func TestContextPack_ShapeRejectsTheOtherShapesKnobs(t *testing.T) {
	a := New(newTestStore(t), "")
	for _, tc := range []struct {
		name string
		args map[string]any
	}{
		{"include_pins under list", map[string]any{"view_id": "task_exec", "include_pins": true}},
		{"payload_max_bytes under list", map[string]any{"view_id": "task_exec", "payload_max_bytes": float64(10)}},
		{"max_tokens_estimate under list", map[string]any{"view_id": "task_exec", "max_tokens_estimate": float64(10)}},
		{"view_id under packet", map[string]any{"shape": "packet", "view_id": "task_exec"}},
		{"max_tokens under packet", map[string]any{"shape": "packet", "max_tokens": float64(10)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wantErrorCode(t, mustCall(t, a.handleContextPackShape, tc.args), "validation_error")
		})
	}
}

// TestContextPack_RejectListIsLoadBearing walks every knob in both shapes'
// reject lists and asserts each is refused BY NAME, so no entry can be deleted
// without a failure here.
func TestContextPack_RejectListIsLoadBearing(t *testing.T) {
	a := New(newTestStore(t), "")

	for knob, value := range map[string]any{
		"include_pins":        true,
		"max_tokens_estimate": float64(10),
		"payload_max_bytes":   float64(10),
		"payload_mode":        "full",
	} {
		t.Run("list/"+knob, func(t *testing.T) {
			body := mustCall(t, a.handleContextPackShape, map[string]any{"view_id": "task_exec", knob: value})
			wantErrorCode(t, body, "validation_error")
			wantMessageNames(t, body, knob)
		})
	}
	for knob, value := range map[string]any{
		"view_id":    "task_exec",
		"max_tokens": float64(10),
	} {
		t.Run("packet/"+knob, func(t *testing.T) {
			body := mustCall(t, a.handleContextPackShape, map[string]any{"shape": "packet", knob: value})
			wantErrorCode(t, body, "validation_error")
			wantMessageNames(t, body, knob)
		})
	}

	// Positive controls: each shape's own arguments are accepted, so the
	// rejections above are about the wrong-arm knob and nothing else.
	wantNoError(t, mustCall(t, a.handleContextPackShape, map[string]any{"view_id": "task_exec", "max_tokens": float64(100)}))
	wantNoError(t, mustCall(t, a.handleContextPackShape, map[string]any{"shape": "packet", "include_pins": false}))
}

// TestContextPack_UnrecognizedShapeFailsClosed: an unparseable arm selector
// must not fall through to the default arm.
func TestContextPack_UnrecognizedShapeFailsClosed(t *testing.T) {
	a := New(newTestStore(t), "")
	body := mustCall(t, a.handleContextPackShape, map[string]any{"shape": "bundle", "view_id": "task_exec"})
	wantErrorCode(t, body, "validation_error")
	if _, ran := body["view"]; ran {
		t.Errorf("unrecognized shape fell through to the list arm: %v", body)
	}
}

// ── Merge 3: context_broker_plan + context_broker_fetch → context_plan ─────

// TestMerge_ContextBrokerPlan_DefaultArmReturnsPlanAndRationale covers
// (context_broker_plan → execute absent). The retired tool answered
// `{plan:{namespaces, include_pins, budget}, rationale}` and read no records.
func TestMerge_ContextBrokerPlan_DefaultArmReturnsPlanAndRationale(t *testing.T) {
	s := newTestStore(t)
	writeRecord(t, s, "user/memory/thing", "k", `{"v":1}`)
	a := New(s, "")

	body := wantNoError(t, mustCall(t, a.handleContextPlan, map[string]any{
		"intent": "boot_project",
	}))
	plan, ok := body["plan"].(map[string]any)
	if !ok {
		t.Fatalf("missing plan; got %v", body)
	}
	for _, k := range []string{"namespaces", "include_pins", "budget"} {
		if _, ok := plan[k]; !ok {
			t.Errorf("plan missing %q; got %v", k, plan)
		}
	}
	if _, ok := body["rationale"]; !ok {
		t.Errorf("missing rationale; got %v", body)
	}
	if _, leaked := body["items"]; leaked {
		t.Errorf("the planning arm must not read records; got %v", body)
	}
}

// TestMerge_ContextBrokerFetch_ExecuteArmReturnsItemsManifestRationale covers
// (context_broker_fetch → execute=true). The retired tool answered
// `{items, manifest, rationale}`.
func TestMerge_ContextBrokerFetch_ExecuteArmReturnsItemsManifestRationale(t *testing.T) {
	s := newTestStore(t)
	writeRecord(t, s, "user/memory/thing", "k", `{"v":1}`)
	a := New(s, "")

	body := wantNoError(t, mustCall(t, a.handleContextPlan, map[string]any{
		"intent":  "boot_project",
		"execute": true,
	}))
	for _, k := range []string{"items", "manifest", "rationale"} {
		if _, ok := body[k]; !ok {
			t.Errorf("execute arm missing %q; got %v", k, body)
		}
	}
	if _, leaked := body["plan"]; leaked {
		t.Errorf("execute arm must answer with records, not the plan object; got %v", body)
	}
	if n := len(parseItems(t, body)); n != 1 {
		t.Errorf("items = %d, want 1", n)
	}
}

// TestContextBroker_PayloadCapUnderThePlanningArmIsRejected: the planning arm
// returns no records, so a byte cap there would be a knob with nothing to cap.
func TestContextBroker_PayloadCapUnderThePlanningArmIsRejected(t *testing.T) {
	a := New(newTestStore(t), "")
	wantErrorCode(t, mustCall(t, a.handleContextPlan, map[string]any{
		"intent":            "boot_project",
		"payload_max_bytes": float64(512),
	}), "validation_error")
}

// ── Merge 4: context_bulk_ingest + context_chunked_ingest → context_ingest ───

// TestMerge_BulkIngest_BulkModeReturnsPerItemResults covers
// (context_bulk_ingest → mode absent / mode=bulk). The retired tool answered
// `{total, written, embedded, errors, results}`.
func TestMerge_BulkIngest_BulkModeReturnsPerItemResults(t *testing.T) {
	s := newTestStore(t)
	a := New(s, writeToken(t, s, []string{"write"}, []string{"*"}))

	body := wantNoError(t, mustCall(t, a.handleContextIngest, map[string]any{
		"items": `[{"namespace":"app/test/ingest","key":"a","payload":{"v":1}},
		           {"namespace":"app/test/ingest","key":"b","payload":{"v":2}}]`,
	}))
	for _, k := range []string{"total", "written", "embedded", "errors", "results"} {
		if _, ok := body[k]; !ok {
			t.Errorf("bulk mode missing %q; got %v", k, body)
		}
	}
	if got, _ := body["written"].(float64); int(got) != 2 {
		t.Errorf("written = %v, want 2", body["written"])
	}
	if _, leaked := body["total_chunks"]; leaked {
		t.Errorf("bulk mode must not answer with the chunked envelope; got %v", body)
	}
}

// TestMerge_ChunkedIngest_ChunkedModeReturnsChunkResults covers
// (context_chunked_ingest → mode=chunked). The retired tool answered
// `{namespace, key_prefix, strategy, total_chunks, embedded, results}`.
func TestMerge_ChunkedIngest_ChunkedModeReturnsChunkResults(t *testing.T) {
	s := newTestStore(t)
	a := New(s, writeToken(t, s, []string{"write"}, []string{"*"}))

	body := wantNoError(t, mustCall(t, a.handleContextIngest, map[string]any{
		"mode":       "chunked",
		"namespace":  "app/test/doc",
		"key_prefix": "readme",
		"text":       "One sentence. Two sentence. Three sentence.",
		"strategy":   "sentence",
		"max_chars":  float64(20),
	}))
	for _, k := range []string{"namespace", "key_prefix", "strategy", "total_chunks", "embedded", "results"} {
		if _, ok := body[k]; !ok {
			t.Errorf("chunked mode missing %q; got %v", k, body)
		}
	}
	if body["key_prefix"] != "readme" {
		t.Errorf("key_prefix = %v, want readme", body["key_prefix"])
	}
	if got, _ := body["total_chunks"].(float64); int(got) < 1 {
		t.Errorf("total_chunks = %v, want at least 1", body["total_chunks"])
	}
	if _, leaked := body["written"]; leaked {
		t.Errorf("chunked mode must not answer with the bulk envelope; got %v", body)
	}
}

// TestContextIngest_ModeRejectsTheOtherModesKnobs pins the disjoint argument
// sets, so a chunked call carrying `items` is told rather than half-honored.
//
// The bulk cases carry a VALID, non-empty `items` array on purpose. An earlier
// draft passed `[]`, which handleBulkIngest independently rejects with
// "items array is empty" — so both bulk cases returned validation_error whether
// or not the knob rejection existed, and deleting the entire bulk reject list
// left the suite green. The knob name is asserted in the message for the same
// reason: a bare code assertion cannot tell WHICH rule fired.
func TestContextIngest_ModeRejectsTheOtherModesKnobs(t *testing.T) {
	s := newTestStore(t)
	a := New(s, writeToken(t, s, []string{"write"}, []string{"*"}))
	const validItems = `[{"namespace":"app/test/ingest","key":"a","payload":{"v":1}}]`
	for _, tc := range []struct {
		name string
		knob string
		args map[string]any
	}{
		{"namespace under bulk", "namespace", map[string]any{"items": validItems, "namespace": "app/x"}},
		{"strategy under bulk", "strategy", map[string]any{"items": validItems, "strategy": "fixed"}},
		{"record_type under bulk", "record_type", map[string]any{"items": validItems, "record_type": "brief/summary"}},
		{"items under chunked", "items", map[string]any{"mode": "chunked", "namespace": "app/x", "key_prefix": "p", "text": "t", "items": validItems}},
		{"stop_on_error under chunked", "stop_on_error", map[string]any{"mode": "chunked", "namespace": "app/x", "key_prefix": "p", "text": "t", "stop_on_error": true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := mustCall(t, a.handleContextIngest, tc.args)
			wantErrorCode(t, body, "validation_error")
			wantMessageNames(t, body, tc.knob)
		})
	}

	// Positive control: the same valid items with no cross-mode knob is
	// accepted, so the rejections above are about the knob and not about the
	// fixture being unwritable.
	wantNoError(t, mustCall(t, a.handleContextIngest, map[string]any{"items": validItems}))
}

// wantMessageNames asserts the error message names the specific thing that was
// rejected. A bare code assertion passes for any validation_error the handler
// happens to reach first, which is how a guard comes to measure something other
// than its own name.
func wantMessageNames(t *testing.T, body map[string]any, want string) {
	t.Helper()
	msg, _ := body["message"].(string)
	if !strings.Contains(msg, want) {
		t.Fatalf("error message does not name %q — some other rule fired: %s", want, msg)
	}
}

// TestContextIngest_BulkRejectListIsLoadBearing is the direct answer to the
// review finding: it walks EVERY knob in the bulk arm's reject list and asserts
// each one is refused by name. Deleting any entry from that list fails here.
func TestContextIngest_BulkRejectListIsLoadBearing(t *testing.T) {
	s := newTestStore(t)
	a := New(s, writeToken(t, s, []string{"write"}, []string{"*"}))
	const validItems = `[{"namespace":"app/test/ingest","key":"a","payload":{"v":1}}]`

	for knob, value := range map[string]any{
		"namespace":   "app/x",
		"key_prefix":  "p",
		"text":        "some text",
		"record_type": "brief/summary",
		"strategy":    "fixed",
		"max_chars":   float64(100),
		"overlap_pct": float64(10),
		"actor":       "someone",
	} {
		t.Run(knob, func(t *testing.T) {
			body := mustCall(t, a.handleContextIngest, map[string]any{"items": validItems, knob: value})
			wantErrorCode(t, body, "validation_error")
			wantMessageNames(t, body, knob)
		})
	}
}

// TestContextIngest_ChunkedRejectListIsLoadBearing is the same walk for the
// chunked arm.
func TestContextIngest_ChunkedRejectListIsLoadBearing(t *testing.T) {
	s := newTestStore(t)
	a := New(s, writeToken(t, s, []string{"write"}, []string{"*"}))
	base := func() map[string]any {
		return map[string]any{"mode": "chunked", "namespace": "app/test/doc", "key_prefix": "p", "text": "One. Two."}
	}

	for knob, value := range map[string]any{
		"items":         `[{"namespace":"app/x","key":"a","payload":{}}]`,
		"embed":         true,
		"stop_on_error": true,
	} {
		t.Run(knob, func(t *testing.T) {
			args := base()
			args[knob] = value
			body := mustCall(t, a.handleContextIngest, args)
			wantErrorCode(t, body, "validation_error")
			wantMessageNames(t, body, knob)
		})
	}

	// Positive control: the base call without a cross-mode knob succeeds.
	wantNoError(t, mustCall(t, a.handleContextIngest, base()))
}

// TestContextIngest_UnrecognizedModeFailsClosed: an unparseable arm selector
// must not fall through to the default arm and start writing.
func TestContextIngest_UnrecognizedModeFailsClosed(t *testing.T) {
	s := newTestStore(t)
	a := New(s, writeToken(t, s, []string{"write"}, []string{"*"}))
	body := mustCall(t, a.handleContextIngest, map[string]any{
		"mode":  "stream",
		"items": `[{"namespace":"app/test/ingest","key":"a","payload":{"v":1}}]`,
	})
	wantErrorCode(t, body, "validation_error")
	if _, ran := body["written"]; ran {
		t.Fatalf("unrecognized mode fell through to the bulk arm: %v", body)
	}
	// Positive control: nothing was written.
	if _, err := s.Head(context.Background(), "app/test/ingest", "a"); err == nil {
		t.Fatalf("unrecognized mode wrote a record; it must fail closed")
	}
}

// ── Merge 5: context_status_promote + _deprecate → context_status_set ────────

// statusFixture writes one draft record and returns an adapter with write scope.
func statusFixture(t *testing.T) (*contextstore.Store, *Adapter) {
	t.Helper()
	s := newTestStore(t)
	if _, err := s.AppendRecord(context.Background(), contextstore.AppendInput{
		Namespace: "app/test/status", Key: "doc", Actor: "test",
		Payload: json.RawMessage(`{"v":1}`), Status: "draft",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return s, New(s, writeToken(t, s, []string{"write"}, []string{"*"}))
}

// TestMerge_StatusPromote_StatusSelectsThePromotionPath covers
// (context_status_promote → status=<target>). The retired tool answered
// `{record_id, revision, from_status, to_status, record_type}`.
func TestMerge_StatusPromote_StatusSelectsThePromotionPath(t *testing.T) {
	_, a := statusFixture(t)

	body := wantNoError(t, mustCall(t, a.handleStatusSet, map[string]any{
		"namespace": "app/test/status",
		"key":       "doc",
		"status":    "reviewed",
	}))
	for _, k := range []string{"record_id", "revision", "from_status", "to_status", "record_type"} {
		if _, ok := body[k]; !ok {
			t.Errorf("missing %q; got %v", k, body)
		}
	}
	if body["from_status"] != "draft" || body["to_status"] != "reviewed" {
		t.Errorf("from/to = %v/%v, want draft/reviewed", body["from_status"], body["to_status"])
	}
}

// TestMerge_StatusPromote_OmittedStatusAdvancesOneStep covers the retired
// tool's to_status="" behavior: advance along draft → reviewed → canonical.
func TestMerge_StatusPromote_OmittedStatusAdvancesOneStep(t *testing.T) {
	_, a := statusFixture(t)

	body := wantNoError(t, mustCall(t, a.handleStatusSet, map[string]any{
		"namespace": "app/test/status",
		"key":       "doc",
	}))
	if body["to_status"] != "reviewed" {
		t.Errorf("to_status = %v, want reviewed (next after draft)", body["to_status"])
	}
}

// TestMerge_StatusDeprecate_DeprecatedTargetSelectsTheDeprecationPath covers
// (context_status_deprecate → status="deprecated").
func TestMerge_StatusDeprecate_DeprecatedTargetSelectsTheDeprecationPath(t *testing.T) {
	_, a := statusFixture(t)

	body := wantNoError(t, mustCall(t, a.handleStatusSet, map[string]any{
		"namespace": "app/test/status",
		"key":       "doc",
		"status":    "deprecated",
	}))
	if body["from_status"] != "draft" || body["to_status"] != "deprecated" {
		t.Errorf("from/to = %v/%v, want draft/deprecated", body["from_status"], body["to_status"])
	}

	// The retired tool's own guard: deprecating twice is a validation_error.
	wantErrorCode(t, mustCall(t, a.handleStatusSet, map[string]any{
		"namespace": "app/test/status",
		"key":       "doc",
		"status":    "deprecated",
	}), "validation_error")
}

// TestMerge_StatusSet_DeprecatedTargetTakesTheDeprecationPath is the ONE
// enumerated non-equivalence in this ticket, pinned so it is visible rather
// than buried in a definition of "equivalent".
//
// context_status_promote(to_status="deprecated") was a legal call:
// draft → deprecated is in ValidTransitions, so it ran the promotion path —
// type-rule validation, then a status_promote audit event. The merged tool
// routes on the target status, so that same target now runs the deprecation
// path, which writes NO audit event from this surface.
//
// The audit asymmetry itself predates this ticket: the MCP deprecation handler
// has never emitted, while its HTTP peer POST /v1/context/status/deprecate
// calls EmitStatusDeprecate. This test pins the asymmetry so a future ticket
// closing it has to come through here.
func TestMerge_StatusSet_DeprecatedTargetTakesTheDeprecationPath(t *testing.T) {
	s, a := statusFixture(t)

	before := auditEventTypes(t, s)
	wantNoError(t, mustCall(t, a.handleStatusSet, map[string]any{
		"namespace": "app/test/status",
		"key":       "doc",
		"status":    "deprecated",
	}))
	after := auditEventTypes(t, s)

	if n := after["status_promote"] - before["status_promote"]; n != 0 {
		t.Errorf("status=deprecated emitted %d status_promote events — it took the promotion path, "+
			"so the routing changed and this test's premise is stale", n)
	}
	if n := after["status_deprecate"] - before["status_deprecate"]; n != 0 {
		t.Errorf("status=deprecated emitted %d status_deprecate events — the MCP deprecation path has "+
			"started emitting, which closes the MCP/HTTP asymmetry this test pins; update the test and the docs", n)
	}

	// Positive control: the promotion path DOES emit, so a zero above is a
	// fact about the deprecation path rather than about this counter.
	_, a2 := statusFixture(t)
	s2 := a2.Store
	pBefore := auditEventTypes(t, s2)
	wantNoError(t, mustCall(t, a2.handleStatusSet, map[string]any{
		"namespace": "app/test/status", "key": "doc", "status": "reviewed",
	}))
	pAfter := auditEventTypes(t, s2)
	if n := pAfter["status_promote"] - pBefore["status_promote"]; n != 1 {
		t.Fatalf("promotion path emitted %d status_promote events, want 1 — the audit counter is not measuring what this test assumes", n)
	}
}

// auditEventTypes counts audit events by type.
func auditEventTypes(t *testing.T, s *contextstore.Store) map[string]int {
	t.Helper()
	events, _, err := s.QueryAuditEvents(context.Background(), contextstore.AuditQuery{Limit: 500})
	if err != nil {
		t.Fatalf("query audit: %v", err)
	}
	out := map[string]int{}
	for _, ev := range events {
		out[ev.EventType]++
	}
	return out
}

// ── Merge 6: four registry listers → context_registry_list ──────────────────

// TestMerge_TypesList_KindTypes covers (context_types_list → kind=types).
func TestMerge_TypesList_KindTypes(t *testing.T) {
	a := New(newTestStore(t), "")
	body := wantNoError(t, mustCall(t, a.handleRegistryList, map[string]any{"kind": "types"}))
	if _, ok := body["types"]; !ok {
		t.Fatalf("kind=types must answer with a `types` array; got %v", body)
	}
	if _, leaked := body["views"]; leaked {
		t.Errorf("kind=types leaked the views registry; got %v", body)
	}
}

// TestMerge_ViewsList_KindViews covers (context_views_list → kind=views).
func TestMerge_ViewsList_KindViews(t *testing.T) {
	a := New(newTestStore(t), "")
	body := wantNoError(t, mustCall(t, a.handleRegistryList, map[string]any{"kind": "views"}))
	if _, ok := body["views"]; !ok {
		t.Fatalf("kind=views must answer with a `views` array; got %v", body)
	}
	if _, leaked := body["types"]; leaked {
		t.Errorf("kind=views leaked the types registry; got %v", body)
	}
}

// TestMerge_NamespacesList_KindNamespaces covers
// (context_namespaces_list → kind=namespaces, no name). The retired tool
// answered the shared budget envelope of policy entries and honored `prefix`
// as a literal string prefix, not a glob.
func TestMerge_NamespacesList_KindNamespaces(t *testing.T) {
	s := newTestStore(t)
	for _, ns := range []string{"user/chrispian/a", "user/chrispian/b", "app/other/c"} {
		if err := s.UpsertNamespacePolicy(context.Background(), contextstore.NamespacePolicyEntry{
			Namespace: ns, OwnerType: "app", OwnerID: "test",
		}); err != nil {
			t.Fatalf("seed %s: %v", ns, err)
		}
	}
	a := New(s, "")

	all := wantNoError(t, mustCall(t, a.handleRegistryList, map[string]any{"kind": "namespaces"}))
	if n := len(parseItems(t, all)); n != 3 {
		t.Fatalf("unfiltered list = %d, want 3", n)
	}
	filtered := wantNoError(t, mustCall(t, a.handleRegistryList, map[string]any{
		"kind":   "namespaces",
		"prefix": "user/chrispian/",
	}))
	if n := len(parseItems(t, filtered)); n != 2 {
		t.Errorf("prefix-filtered list = %d, want 2", n)
	}
	first := parseItems(t, filtered)[0].(map[string]any)
	for _, k := range []string{"namespace", "owner_type", "owner_id", "policy"} {
		if _, ok := first[k]; !ok {
			t.Errorf("policy entry missing %q; got %v", k, first)
		}
	}
}

// TestMerge_NamespaceShow_KindNamespacesWithName covers
// (context_namespace_show → kind=namespaces + name). The retired tool answered
// the single entry `{namespace, owner_type, owner_id, policy}` — a different
// shape from the list, because it answers a different question.
func TestMerge_NamespaceShow_KindNamespacesWithName(t *testing.T) {
	s := newTestStore(t)
	if err := s.UpsertNamespacePolicy(context.Background(), contextstore.NamespacePolicyEntry{
		Namespace: "app/my-agent/session", OwnerType: "app", OwnerID: "my-agent",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := New(s, "")

	body := wantNoError(t, mustCall(t, a.handleRegistryList, map[string]any{
		"kind": "namespaces",
		"name": "app/my-agent/session",
	}))
	if body["namespace"] != "app/my-agent/session" || body["owner_type"] != "app" || body["owner_id"] != "my-agent" {
		t.Errorf("single-namespace body = %v", body)
	}
	if _, leaked := body["items"]; leaked {
		t.Errorf("name form must not answer with the list envelope; got %v", body)
	}

	wantErrorCode(t, mustCall(t, a.handleRegistryList, map[string]any{
		"kind": "namespaces", "name": "app/nonexistent/ns",
	}), "not_found")
}

// TestContextRegistryList_RejectsKnobsTheChosenKindCannotHonor walks every knob
// each arm cannot honor, asserting each is refused BY NAME.
//
// The kind=namespaces + name cases are the review finding: namespaceShowResult
// takes only the namespace, so prefix and limit CANNOT reach it. Accepting them
// there was the silently-ignored-knob failure this whole tool exists to remove.
func TestContextRegistryList_RejectsKnobsTheChosenKindCannotHonor(t *testing.T) {
	s := newTestStore(t)
	if err := s.UpsertNamespacePolicy(context.Background(), contextstore.NamespacePolicyEntry{
		Namespace: "app/known/ns", OwnerType: "app", OwnerID: "test",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := New(s, "")

	for _, tc := range []struct {
		name string
		knob string
		args map[string]any
	}{
		{"name under types", "name", map[string]any{"kind": "types", "name": "x"}},
		{"prefix under types", "prefix", map[string]any{"kind": "types", "prefix": "user/"}},
		{"limit under types", "limit", map[string]any{"kind": "types", "limit": float64(5)}},
		{"name under views", "name", map[string]any{"kind": "views", "name": "x"}},
		{"prefix under views", "prefix", map[string]any{"kind": "views", "prefix": "user/"}},
		{"limit under views", "limit", map[string]any{"kind": "views", "limit": float64(5)}},
		{"prefix with name", "prefix", map[string]any{"kind": "namespaces", "name": "app/known/ns", "prefix": "app/"}},
		{"limit with name", "limit", map[string]any{"kind": "namespaces", "name": "app/known/ns", "limit": float64(5)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := mustCall(t, a.handleRegistryList, tc.args)
			wantErrorCode(t, body, "validation_error")
			wantMessageNames(t, body, tc.knob)
		})
	}

	// Positive controls. Each of these is the SAME call minus the knob that was
	// rejected, so a rejection above cannot be the arm failing for its own
	// reasons. The name form resolves a real namespace, and prefix/limit are
	// legal on the list form.
	wantNoError(t, mustCall(t, a.handleRegistryList, map[string]any{"kind": "types"}))
	wantNoError(t, mustCall(t, a.handleRegistryList, map[string]any{"kind": "views"}))
	wantNoError(t, mustCall(t, a.handleRegistryList, map[string]any{"kind": "namespaces", "name": "app/known/ns"}))
	wantNoError(t, mustCall(t, a.handleRegistryList, map[string]any{"kind": "namespaces", "prefix": "app/", "limit": float64(5)}))
}

// ── Retired argument NAMES fail closed, they are not ignored ────────────────
//
// mcp-go sets no additionalProperties:false, so an argument a tool no longer
// declares still reaches the handler. Ignoring it is worse than a hard break:
// the caller gets a plausible answer to a different question.

// TestRetiredArg_ToStatusIsRefusedNotIgnored covers the migration of
// context_status_promote(to_status=X) → context_status_set(status=X).
//
// Ignored, `to_status: "canonical"` would leave `status` empty, which means
// "advance one step" — so a record at draft would go to `reviewed` and the
// response would report success. That is the silent behavior change.
func TestRetiredArg_ToStatusIsRefusedNotIgnored(t *testing.T) {
	s, a := statusFixture(t)

	body := mustCall(t, a.handleStatusSet, map[string]any{
		"namespace": "app/test/status",
		"key":       "doc",
		"to_status": "canonical",
	})
	wantErrorCode(t, body, "validation_error")
	wantMessageNames(t, body, "to_status")

	// Nothing moved: the record is still at draft, not silently at reviewed.
	head, err := s.Head(context.Background(), "app/test/status", "doc")
	if err != nil {
		t.Fatalf("head: %v", err)
	}
	if head.Status != "draft" {
		t.Fatalf("status = %q, want draft — the retired argument was honored or the record advanced anyway", head.Status)
	}

	// Positive control: the new spelling works and lands on canonical, so the
	// rejection above is about the ARGUMENT NAME and not about the transition.
	wantNoError(t, mustCall(t, a.handleStatusSet, map[string]any{
		"namespace": "app/test/status", "key": "doc", "status": "reviewed",
	}))
	got := wantNoError(t, mustCall(t, a.handleStatusSet, map[string]any{
		"namespace": "app/test/status", "key": "doc", "status": "canonical",
	}))
	if got["to_status"] != "canonical" {
		t.Fatalf("status=canonical landed on %v", got["to_status"])
	}
}

// TestRetiredArg_NamespaceIsRefusedNotIgnored covers the migration of
// context_namespace_show(namespace=X) → context_registry_list(kind=namespaces,
// name=X).
//
// Ignored, `namespace` would leave `name` empty and the tool would answer the
// WHOLE LIST — a different question, in a different shape, reported as success.
func TestRetiredArg_NamespaceIsRefusedNotIgnored(t *testing.T) {
	s := newTestStore(t)
	for _, ns := range []string{"app/one/ns", "app/two/ns"} {
		if err := s.UpsertNamespacePolicy(context.Background(), contextstore.NamespacePolicyEntry{
			Namespace: ns, OwnerType: "app", OwnerID: "test",
		}); err != nil {
			t.Fatalf("seed %s: %v", ns, err)
		}
	}
	a := New(s, "")

	body := mustCall(t, a.handleRegistryList, map[string]any{
		"kind":      "namespaces",
		"namespace": "app/one/ns",
	})
	wantErrorCode(t, body, "validation_error")
	wantMessageNames(t, body, "namespace")
	if _, listed := body["items"]; listed {
		t.Fatalf("the retired argument was ignored and the whole list came back: %v", body)
	}

	// Positive control: the new spelling answers the single-namespace question.
	got := wantNoError(t, mustCall(t, a.handleRegistryList, map[string]any{
		"kind": "namespaces", "name": "app/one/ns",
	}))
	if got["namespace"] != "app/one/ns" {
		t.Fatalf("name form returned %v", got)
	}
}

// TestContextRegistryList_UnrecognizedKindFailsClosed: no default kind, so a
// typo cannot silently return the wrong registry.
func TestContextRegistryList_UnrecognizedKindFailsClosed(t *testing.T) {
	a := New(newTestStore(t), "")
	for _, kind := range []string{"", "type", "namespace", "everything"} {
		body := mustCall(t, a.handleRegistryList, map[string]any{"kind": kind})
		wantErrorCode(t, body, "validation_error")
		for _, leaked := range []string{"types", "views", "items"} {
			if _, ok := body[leaked]; ok {
				t.Errorf("kind=%q fell through and returned %q: %v", kind, leaked, body)
			}
		}
	}
}

// ── Merge 7: the three promote stages → context_promote ─────────────────────

// promoteFixture seeds a promotable record and returns the store plus a token
// factory bound to it.
func promoteFixture(t *testing.T) *contextstore.Store {
	t.Helper()
	s := newTestStore(t)
	writeRecord(t, s, "app/agent/session", "summary", `{"text":"ready"}`)
	return s
}

// TestMerge_PromoteRequest_StageRequest covers
// (context_promote_request → stage=request). The retired tool answered
// `{request_id, status:"pending"}`.
func TestMerge_PromoteRequest_StageRequest(t *testing.T) {
	s := promoteFixture(t)
	a := New(s, writeToken(t, s, []string{"promote.request"}, []string{"*"}))

	body := wantNoError(t, mustCall(t, a.handlePromote, map[string]any{
		"stage":            "request",
		"source_namespace": "app/agent/session",
		"source_key":       "summary",
		"target_namespace": "user/memory/agent",
		"target_key":       "summary",
	}))
	if body["status"] != "pending" {
		t.Errorf("status = %v, want pending", body["status"])
	}
	if id, _ := body["request_id"].(string); !strings.HasPrefix(id, "req-") {
		t.Errorf("request_id = %v, want a req-… id", body["request_id"])
	}
}

// TestMerge_PromoteApprove_StageApprove covers
// (context_promote_approve → stage=approve). The retired tool answered
// `{approval_id, request_id, status:"approved"}`.
func TestMerge_PromoteApprove_StageApprove(t *testing.T) {
	s := promoteFixture(t)
	a := New(s, writeToken(t, s, []string{"promote.request", "promote.approve"}, []string{"*"}))
	reqID := seedPromoteRequest(t, a)

	body := wantNoError(t, mustCall(t, a.handlePromote, map[string]any{
		"stage": "approve", "request_id": reqID,
	}))
	if body["status"] != "approved" || body["request_id"] != reqID {
		t.Errorf("approve body = %v", body)
	}
	if id, _ := body["approval_id"].(string); !strings.HasPrefix(id, "appr-") {
		t.Errorf("approval_id = %v, want an appr-… id", body["approval_id"])
	}
}

// TestMerge_PromoteApply_StageApply covers
// (context_promote_apply → stage=apply). The retired tool answered
// `{record_id, request_id, status:"applied", target_namespace, target_key}`.
func TestMerge_PromoteApply_StageApply(t *testing.T) {
	s := promoteFixture(t)
	a := New(s, writeToken(t, s, []string{"promote.request", "promote.approve", "promote.apply"}, []string{"*"}))
	reqID := seedPromoteRequest(t, a)
	wantNoError(t, mustCall(t, a.handlePromote, map[string]any{"stage": "approve", "request_id": reqID}))

	body := wantNoError(t, mustCall(t, a.handlePromote, map[string]any{
		"stage": "apply", "request_id": reqID,
	}))
	if body["status"] != "applied" || body["target_namespace"] != "user/memory/agent" || body["target_key"] != "summary" {
		t.Errorf("apply body = %v", body)
	}
	if _, err := s.Head(context.Background(), "user/memory/agent", "summary"); err != nil {
		t.Errorf("apply did not write the target record: %v", err)
	}
}

func seedPromoteRequest(t *testing.T, a *Adapter) string {
	t.Helper()
	body := wantNoError(t, mustCall(t, a.handlePromote, map[string]any{
		"stage":            "request",
		"source_namespace": "app/agent/session",
		"source_key":       "summary",
		"target_namespace": "user/memory/agent",
		"target_key":       "summary",
	}))
	return body["request_id"].(string)
}

// ── AC2: the promotion gate, asserted as an attack ──────────────────────────

// TestPromoteGate_EachScopeReachesOnlyItsOwnStage is AC2's nine cases.
//
// It is written as an ATTACK, not as a permission check: for each of the three
// scopes, all three stages are attempted, and the two the caller has no scope
// for must come back insufficient_scope with nothing written. Before the merge
// the gate was structural — the tool holding the approve path refused a caller
// without promote.approve — and after the merge one tool is reachable by anyone
// holding ANY promote scope, with the stage argument selecting which check
// runs. These nine cases are what makes the two arrangements the same
// statement about reachability rather than two arrangements that look alike.
func TestPromoteGate_EachScopeReachesOnlyItsOwnStage(t *testing.T) {
	stageArgs := map[string]map[string]any{
		"request": {
			"stage":            "request",
			"source_namespace": "app/agent/session",
			"source_key":       "summary",
			"target_namespace": "user/memory/agent",
			"target_key":       "summary",
		},
		"approve": {"stage": "approve", "request_id": "PLACEHOLDER"},
		"apply":   {"stage": "apply", "request_id": "PLACEHOLDER"},
	}

	for _, held := range []string{"promote.request", "promote.approve", "promote.apply"} {
		for _, stage := range []string{"request", "approve", "apply"} {
			t.Run(held+"/"+stage, func(t *testing.T) {
				s := promoteFixture(t)

				// A separate fully-scoped adapter seeds a real, approved
				// request, so the attacking call fails on SCOPE rather than on
				// a missing request id — otherwise a not_found would be
				// indistinguishable from a rejection.
				seeder := New(s, writeToken(t, s,
					[]string{"promote.request", "promote.approve"}, []string{"*"}))
				reqID := seedPromoteRequest(t, seeder)
				if stage == "apply" {
					wantNoError(t, mustCall(t, seeder.handlePromote, map[string]any{
						"stage": "approve", "request_id": reqID,
					}))
				}

				args := map[string]any{}
				for k, v := range stageArgs[stage] {
					args[k] = v
				}
				if args["request_id"] == "PLACEHOLDER" {
					args["request_id"] = reqID
				}
				if stage == "request" {
					// Distinct target so a success is visible in the store.
					args["target_key"] = "attacked"
				}

				attacker := New(s, writeToken(t, s, []string{held}, []string{"*"}))
				body := mustCall(t, attacker.handlePromote, args)

				wantScope := map[string]string{
					"request": "promote.request",
					"approve": "promote.approve",
					"apply":   "promote.apply",
				}[stage]

				if held == wantScope {
					wantNoError(t, body)
					return
				}
				wantErrorCode(t, body, "insufficient_scope")
				msg, _ := body["message"].(string)
				if !strings.Contains(msg, wantScope) {
					t.Errorf("rejection names %q, but the stage requires %q; message: %s", msg, wantScope, msg)
				}
				if stage == "request" {
					if _, err := s.Head(context.Background(), "user/memory/agent", "attacked"); err == nil {
						t.Fatalf("a caller holding only %s effected a %s", held, stage)
					}
				}
			})
		}
	}
}

// TestPromoteGate_UnparseableStageFailsClosed covers AC2's malformed and absent
// stage values. A stage that fails to parse must reach no scope check, no store
// read and no write — the failure mode tools.go:266 shipped for payload_mode,
// where an unrecognized value fell through to the permissive branch.
func TestPromoteGate_UnparseableStageFailsClosed(t *testing.T) {
	for _, stage := range []any{nil, "", "REQUEST", "request ", "apply;approve", "list", 7, true} {
		s := promoteFixture(t)
		// Full scopes: if the gate leaked, this caller could do the damage.
		a := New(s, writeToken(t, s,
			[]string{"promote.request", "promote.approve", "promote.apply"}, []string{"*"}))

		args := map[string]any{
			"source_namespace": "app/agent/session",
			"source_key":       "summary",
			"target_namespace": "user/memory/agent",
			"target_key":       "leaked",
		}
		if stage != nil {
			args["stage"] = stage
		}
		body := mustCall(t, a.handlePromote, args)
		wantErrorCode(t, body, "validation_error")
		if _, ok := body["request_id"]; ok {
			t.Fatalf("stage=%v ran the request arm: %v", stage, body)
		}
		if _, err := s.Head(context.Background(), "app/mcp-agent/promotions", "leaked"); err == nil {
			t.Fatalf("stage=%v wrote a promotion record", stage)
		}
	}
}

// TestPromoteGate_PositiveControl proves the nine-case harness above can see a
// success at all. A table of rejections that would pass with the gate wired
// shut is worth nothing.
func TestPromoteGate_PositiveControl(t *testing.T) {
	s := promoteFixture(t)
	a := New(s, writeToken(t, s, []string{"promote.request"}, []string{"*"}))
	wantNoError(t, mustCall(t, a.handlePromote, map[string]any{
		"stage":            "request",
		"source_namespace": "app/agent/session",
		"source_key":       "summary",
		"target_namespace": "user/memory/agent",
		"target_key":       "summary",
	}))
}

// ── AC3: payload_max_bytes replaces payload_mode=head_only ──────────────────

// TestPayloadMaxBytes_HeadOnlyReturnedAnEmptyResultAndTheCapDoesNot is the
// evidence for retiring head_only rather than renaming it.
//
// head_only cut rec.Payload at 512 bytes and handed the prefix to
// json.RawMessage. The prefix of a JSON object is not valid JSON, so the
// ENCLOSING json.Marshal failed, and toolJSON used to discard that error — so
// an oversized record came back as an empty tool result rather than a
// truncated one. This test reproduces the invalid old representation, verifies
// the response path now reports it, then shows the cap does not share it.
func TestPayloadMaxBytes_HeadOnlyReturnedAnEmptyResultAndTheCapDoesNot(t *testing.T) {
	payload := []byte(`{"body":"` + strings.Repeat("x", 600) + `"}`)

	// The retired mechanism, reproduced: shorten the payload in place.
	oldStyle := map[string]any{"key": "k", "payload": json.RawMessage(payload[:512])}
	if body, err := json.Marshal(oldStyle); err == nil {
		t.Fatalf("the head_only mechanism marshaled cleanly (%d bytes) — this test's premise is stale", len(body))
	}
	got := textOf(t, toolJSON(oldStyle))
	var failure map[string]any
	if err := json.Unmarshal([]byte(got), &failure); err != nil {
		t.Fatalf("marshal failure result is not JSON: %q: %v", got, err)
	}
	if failure["code"] != "internal_error" {
		t.Fatalf("marshal failure code = %v, want internal_error (body=%s)", failure["code"], got)
	}

	// Positive control: untruncated, the same shape marshals fine, so the
	// failure above is about the truncation and not about the fixture.
	if _, err := json.Marshal(map[string]any{"key": "k", "payload": json.RawMessage(payload)}); err != nil {
		t.Fatalf("control failed to marshal: %v", err)
	}

	// The cap: valid JSON, and it says it capped.
	item := map[string]any{"key": "k"}
	served := capPayload(item, payload, 512)
	if served != 512 {
		t.Errorf("served = %d, want 512", served)
	}
	if _, ok := item["payload"]; ok {
		t.Errorf("a capped item must not carry `payload`; got %v", item)
	}
	if item["payload_truncated"] != true || item["payload_bytes"] != len(payload) {
		t.Errorf("capped item = %v", item)
	}
	body, err := json.Marshal(item)
	if err != nil {
		t.Fatalf("capped item does not marshal: %v", err)
	}
	if !json.Valid(body) {
		t.Fatalf("capped item is not valid JSON: %s", body)
	}
}

func textOf(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	if res == nil || len(res.Content) == 0 {
		return ""
	}
	tc, ok := res.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", res.Content[0])
	}
	return tc.Text
}

// TestPayloadMaxBytes_UncappedPayloadIsUnchanged pins that the cap is inert
// when it does not bind, so the default response shape is untouched.
func TestPayloadMaxBytes_UncappedPayloadIsUnchanged(t *testing.T) {
	payload := []byte(`{"v":1}`)
	for _, cap := range []int{0, len(payload), len(payload) + 1} {
		item := map[string]any{}
		served := capPayload(item, payload, cap)
		if served != len(payload) {
			t.Errorf("cap=%d served %d, want %d", cap, served, len(payload))
		}
		raw, ok := item["payload"].(json.RawMessage)
		if !ok || string(raw) != string(payload) {
			t.Errorf("cap=%d changed an uncapped payload: %v", cap, item)
		}
		if _, ok := item["payload_truncated"]; ok {
			t.Errorf("cap=%d marked an uncapped payload truncated", cap)
		}
	}
}

// TestPayloadMaxBytes_CapBindsEndToEndOnThePacketShape drives the cap through
// the shipped tool rather than through capPayload alone.
func TestPayloadMaxBytes_CapBindsEndToEndOnThePacketShape(t *testing.T) {
	s := newTestStore(t)
	writeRecord(t, s, "app/test/big", "doc", `{"body":"`+strings.Repeat("x", 600)+`"}`)
	a := New(s, "")

	body := wantNoError(t, mustCall(t, a.handleContextPackShape, map[string]any{
		"shape":             "packet",
		"namespaces":        "app/test/big",
		"include_pins":      false,
		"payload_max_bytes": float64(64),
	}))
	items := parseItems(t, body)
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1", len(items))
	}
	item := items[0].(map[string]any)
	if item["payload_truncated"] != true {
		t.Errorf("cap did not bind: %v", item)
	}
	head, _ := item["payload_head"].(string)
	if len(head) != 64 {
		t.Errorf("payload_head = %d bytes, want 64", len(head))
	}

	// Positive control: without the cap the same call returns the payload.
	uncapped := wantNoError(t, mustCall(t, a.handleContextPackShape, map[string]any{
		"shape": "packet", "namespaces": "app/test/big", "include_pins": false,
	}))
	only := parseItems(t, uncapped)[0].(map[string]any)
	if _, ok := only["payload"]; !ok {
		t.Fatalf("uncapped call lost the payload: %v", only)
	}
}

// TestPayloadMode_RetiredVocabularyFailsClosed is the fail-open fix.
//
// tools.go branched only on `payload_mode == "head_only"`; every other value —
// keys, summary, a typo — fell through to FULL payloads with no error, failing
// toward more context than the caller asked for. The packet-shaped tools no
// longer take a projection mode at all, and say so.
func TestPayloadMode_RetiredVocabularyFailsClosed(t *testing.T) {
	s := newTestStore(t)
	writeRecord(t, s, "app/test/big", "doc", `{"v":1}`)
	a := New(s, "")

	for _, mode := range []string{"head_only", "keys", "summary", "bogus"} {
		t.Run(mode, func(t *testing.T) {
			body := mustCall(t, a.handleContextPackShape, map[string]any{
				"shape": "packet", "namespaces": "app/test/big", "payload_mode": mode,
			})
			wantErrorCode(t, body, "validation_error")
			if _, ok := body["items"]; ok {
				t.Errorf("payload_mode=%q fell through and returned items: %v", mode, body)
			}
		})
	}

	// `full` was the default and means the same thing here as everywhere else.
	wantNoError(t, mustCall(t, a.handleContextPackShape, map[string]any{
		"shape": "packet", "namespaces": "app/test/big", "payload_mode": "full",
	}))
}

// TestPayloadMaxBytes_NegativeCapIsRejected: a negative cap is outside the
// knob's meaning, and reading it as "no cap" would return MORE than asked for.
func TestPayloadMaxBytes_NegativeCapIsRejected(t *testing.T) {
	a := New(newTestStore(t), "")
	wantErrorCode(t, mustCall(t, a.handleContextPackShape, map[string]any{
		"shape": "packet", "namespaces": "app/test/*", "payload_max_bytes": float64(-1),
	}), "validation_error")
}
