package mcpadapter

// CW-20260825-0008. tesseract_touch gets an HTTP peer at POST /v1/memory/touch.
//
// tests/parity/parity_test.go pairs the two but asserts only that the route
// exists — it never sends an argument. Argument parity has no structural guard,
// and its absence has already shipped a defect once (payload_mode,
// CW-20260825-0003). This file is that guard for touch: it drives BOTH doors
// over stores seeded identically, so any difference it reports belongs to the
// surface rather than to the data.
//
// Touch is not idempotent — that is the point of it — so the two doors are never
// pointed at the same memory. Each gets its own identically-seeded row, and the
// assertion is that the two rows end up in the same state.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/hollis-labs/tesseract/internal/contextapi"
	"github.com/hollis-labs/tesseract/internal/contextpolicy"
	"github.com/hollis-labs/tesseract/internal/contextstore"
	"github.com/hollis-labs/tesseract/internal/knowledge"
	"github.com/hollis-labs/tesseract/internal/memory"
	"github.com/mark3labs/mcp-go/mcp"
)

const touchNS = "user/chrispian/memory/notes"

// touchSurfaces wires one store to both doors.
func touchSurfaces(t *testing.T) (*Adapter, *contextapi.Server, *memory.Store) {
	t.Helper()
	cs := newTestStore(t)
	ms := memory.NewStore(cs.DB(), nil, "", 0, memory.NoopQueue{})
	ks := knowledge.New(ms)

	tok, _, err := cs.CreateAuthToken(context.Background(), contextstore.TokenCreateInput{
		Label:  "touch",
		Scopes: []string{"memory:read", "memory:write"},
	})
	if err != nil {
		t.Fatalf("create token: %v", err)
	}

	a := New(cs, tok)
	a.MemoryStore = ms
	a.KnowledgeStore = ks

	srv := contextapi.NewServer(cs, contextpolicy.New())
	srv.MemoryStore = ms
	srv.KnowledgeStore = ks

	return a, srv, ms
}

// seedTouchable writes a memory and parks it at the floor, so one reinforcement
// produces a value distinguishable from both no reinforcement and two.
func seedTouchable(t *testing.T, ms *memory.Store, key string) memory.Revision {
	t.Helper()
	ctx := context.Background()
	rev, err := ms.WriteRevision(ctx, memory.WriteInput{
		Namespace:  touchNS,
		MemoryKey:  key,
		Author:     memory.Author{AgentID: "test", AgentVersion: "1.0"},
		Trigger:    memory.TriggerExplicit,
		SessionID:  "sess-touch",
		Origin:     memory.OriginUser,
		Confidence: 0.9,
		Status:     memory.StatusCanonical,
		Payload:    memory.Payload{Summary: "touchable " + key, Body: "body " + key},
	})
	if err != nil {
		t.Fatalf("seed %s: %v", key, err)
	}
	if _, err := ms.DB().ExecContext(ctx,
		`UPDATE memory_state SET activation = 0.05 WHERE memory_id = ?`, rev.MemoryID); err != nil {
		t.Fatalf("park %s at floor: %v", key, err)
	}
	return rev
}

// touchWire is the response shape both doors must answer with.
type touchWire struct {
	Touched  int      `json:"touched"`
	NotFound []string `json:"not_found"`
}

func touchViaMCP(t *testing.T, a *Adapter, args map[string]any) touchWire {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	res, err := a.handleTesseractTouch(context.Background(), req)
	if err != nil {
		t.Fatalf("tesseract_touch: %v", err)
	}
	raw := res.Content[0].(mcp.TextContent).Text
	var out touchWire
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("decode MCP touch response: %v; raw=%s", err, raw)
	}
	return out
}

func touchViaHTTP(t *testing.T, srv *contextapi.Server, body string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/memory/touch", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	return rr.Code, rr.Body.String()
}

func activationFor(t *testing.T, ms *memory.Store, memoryID string) (float64, int64) {
	t.Helper()
	st, err := ms.GetState(context.Background(), memoryID)
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	return st.Activation, st.AccessCount
}

// ── The argument name is load-bearing on both doors ──────────────────────────

// TestTouchParity_SameArgumentSameEffect is the core parity assertion: the same
// argument name carries the same meaning through both doors, and leaves the two
// stores in the same state.
func TestTouchParity_SameArgumentSameEffect(t *testing.T) {
	a, srv, ms := touchSurfaces(t)

	viaMCP := seedTouchable(t, ms, "parity.mcp")
	viaHTTP := seedTouchable(t, ms, "parity.http")

	mcpRes := touchViaMCP(t, a, map[string]any{
		"revision_ids": []any{viaMCP.RevisionID},
	})
	code, body := touchViaHTTP(t, srv,
		`{"revision_ids":["`+viaHTTP.RevisionID+`"]}`)
	if code != http.StatusOK {
		t.Fatalf("HTTP touch status = %d, body = %s", code, body)
	}
	var httpRes touchWire
	if err := json.Unmarshal([]byte(body), &httpRes); err != nil {
		t.Fatalf("decode HTTP touch response: %v; raw=%s", err, body)
	}

	if mcpRes.Touched != 1 || httpRes.Touched != 1 {
		t.Errorf("touched: MCP=%d HTTP=%d, want 1 from each", mcpRes.Touched, httpRes.Touched)
	}
	if len(mcpRes.NotFound) != 0 || len(httpRes.NotFound) != 0 {
		t.Errorf("not_found: MCP=%v HTTP=%v, want empty from each", mcpRes.NotFound, httpRes.NotFound)
	}

	// not_found must serialize as [] on both doors, never null: a caller that
	// has to distinguish an empty list from a missing key on one door and not
	// the other is exactly the drift this file exists to catch.
	if !strings.Contains(body, `"not_found":[]`) {
		t.Errorf("HTTP body does not carry not_found as an empty array: %s", body)
	}

	mcpAct, mcpCount := activationFor(t, ms, viaMCP.MemoryID)
	httpAct, httpCount := activationFor(t, ms, viaHTTP.MemoryID)
	if mcpAct != httpAct {
		t.Errorf("the two doors left different activations: MCP=%v HTTP=%v", mcpAct, httpAct)
	}
	if mcpCount != httpCount {
		t.Errorf("the two doors left different access_counts: MCP=%d HTTP=%d", mcpCount, httpCount)
	}
	// Stated independently of the constants: one reinforcement from 0.05.
	if mcpAct != 0.245 {
		t.Errorf("activation after one touch = %v, want 0.245", mcpAct)
	}
}

// TestTouchParity_WrongArgumentNameIsInertOnBothDoors is the negative control
// for the test above. Without it, a door that reinforced on ANY body — ignoring
// the argument entirely — would pass the positive case.
func TestTouchParity_WrongArgumentNameIsInertOnBothDoors(t *testing.T) {
	a, srv, ms := touchSurfaces(t)

	viaMCP := seedTouchable(t, ms, "wrongname.mcp")
	viaHTTP := seedTouchable(t, ms, "wrongname.http")

	// MCP: the argument is declared required, so a body without it is refused
	// rather than silently treated as an empty touch.
	res := touchViaMCP(t, a, map[string]any{
		"revisions": []any{viaMCP.RevisionID},
	})
	if res.Touched != 0 {
		t.Errorf("MCP touched %d under a misspelled argument, want 0", res.Touched)
	}

	// HTTP refuses the misspelling outright now that the memory handlers decode
	// strictly (CW-20260514-0023). This used to answer 200 with touched=0, which
	// satisfied "inert" only in the narrow sense that nothing was reinforced —
	// the caller still got a success for a body the server had silently ignored.
	// A 400 is the stronger form of the same guarantee, and it closes the gap
	// rather than widening it: the MCP door already refused this body, because
	// revision_ids is declared required, so the two doors now agree on refusal
	// instead of one of them quietly accepting.
	code, body := touchViaHTTP(t, srv,
		`{"revisions":["`+viaHTTP.RevisionID+`"]}`)
	if code != http.StatusBadRequest {
		t.Fatalf("HTTP status = %d, want 400; body = %s", code, body)
	}
	if !strings.Contains(body, `"revisions"`) {
		t.Errorf("HTTP rejection does not name the offending field: %s", body)
	}

	for _, c := range []struct {
		door string
		id   string
	}{{"MCP", viaMCP.MemoryID}, {"HTTP", viaHTTP.MemoryID}} {
		act, count := activationFor(t, ms, c.id)
		if act != 0.05 || count != 0 {
			t.Errorf("%s door reinforced despite a misspelled argument: activation=%v access_count=%d",
				c.door, act, count)
		}
	}
}

// TestTouchParity_AcceptsTheFormTheSchemaAdvertises closes a gap between what
// the tool declares and what the tests exercise.
//
// revision_ids is declared with mcp.WithString, so a client generating a call
// from the input schema sends a JSON-encoded STRING: "[\"01HX...\"]", not a JSON
// array. parseStringArrayArg accepts both, but every other test here passes
// []any — the native form — so the form the schema actually advertises was the
// one form never driven through the door advertising it.
//
// Both forms must reinforce identically. If they ever diverge, the schema-
// conformant client is the one that breaks, and it is the one least likely to be
// holding the test suite.
func TestTouchParity_AcceptsTheFormTheSchemaAdvertises(t *testing.T) {
	a, _, ms := touchSurfaces(t)

	viaNative := seedTouchable(t, ms, "schemaform.native")
	viaString := seedTouchable(t, ms, "schemaform.string")

	native := touchViaMCP(t, a, map[string]any{
		"revision_ids": []any{viaNative.RevisionID},
	})
	// The stringified form, exactly as a schema-driven client would build it.
	stringified := touchViaMCP(t, a, map[string]any{
		"revision_ids": `["` + viaString.RevisionID + `"]`,
	})

	if native.Touched != 1 || stringified.Touched != 1 {
		t.Errorf("touched: native=%d stringified=%d, want 1 from each "+
			"(not_found native=%v stringified=%v)",
			native.Touched, stringified.Touched, native.NotFound, stringified.NotFound)
	}

	nativeAct, nativeCount := activationFor(t, ms, viaNative.MemoryID)
	stringAct, stringCount := activationFor(t, ms, viaString.MemoryID)
	if nativeAct != stringAct || nativeCount != stringCount {
		t.Errorf("the two argument encodings left different state: native=(%v,%d) stringified=(%v,%d)",
			nativeAct, nativeCount, stringAct, stringCount)
	}
	if stringAct != 0.245 {
		t.Errorf("activation after one stringified touch = %v, want 0.245", stringAct)
	}
}

// TestTouchParity_MCPRequiresTheArgument checks the declaration, not just the
// behavior: revision_ids is marked Required on the MCP tool, so an agent's
// client refuses the call before it reaches the handler. The HTTP peer has no
// equivalent declaration and treats a missing field as an empty list, which the
// test above pins.
func TestTouchParity_MCPRequiresTheArgument(t *testing.T) {
	a, _, _ := touchSurfaces(t)
	res := touchViaMCP(t, a, map[string]any{})
	if res.Touched != 0 {
		t.Errorf("touched = %d with no arguments at all, want 0", res.Touched)
	}
}

// ── Both doors apply the same rules ──────────────────────────────────────────

func TestTouchParity_DedupIdenticalOnBothDoors(t *testing.T) {
	a, srv, ms := touchSurfaces(t)

	viaMCP := seedTouchable(t, ms, "dedup.mcp")
	viaHTTP := seedTouchable(t, ms, "dedup.http")

	touchViaMCP(t, a, map[string]any{
		"revision_ids": []any{viaMCP.RevisionID, viaMCP.RevisionID, viaMCP.RevisionID},
	})
	code, body := touchViaHTTP(t, srv,
		`{"revision_ids":["`+viaHTTP.RevisionID+`","`+viaHTTP.RevisionID+`","`+viaHTTP.RevisionID+`"]}`)
	if code != http.StatusOK {
		t.Fatalf("HTTP status = %d, body = %s", code, body)
	}

	mcpAct, mcpCount := activationFor(t, ms, viaMCP.MemoryID)
	httpAct, httpCount := activationFor(t, ms, viaHTTP.MemoryID)
	if mcpAct != httpAct || mcpCount != httpCount {
		t.Errorf("dedup differs across doors: MCP=(%v,%d) HTTP=(%v,%d)",
			mcpAct, mcpCount, httpAct, httpCount)
	}
	if mcpCount != int64(1) {
		t.Errorf("access_count = %d after three copies of one ID, want 1", mcpCount)
	}
	if mcpAct != 0.245 {
		t.Errorf("activation = %v, want 0.245 (one reinforcement, not three)", mcpAct)
	}
}

func TestTouchParity_OversizedBatchRefusedByBothDoors(t *testing.T) {
	a, srv, _ := touchSurfaces(t)

	ids := make([]any, memory.MaxTouchRevisions+1)
	quoted := make([]string, memory.MaxTouchRevisions+1)
	for i := range ids {
		id := "id-" + strconv.Itoa(i)
		ids[i] = id
		quoted[i] = `"` + id + `"`
	}

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"revision_ids": ids}
	res, err := a.handleTesseractTouch(context.Background(), req)
	if err != nil {
		t.Fatalf("tesseract_touch: %v", err)
	}
	mcpRaw := res.Content[0].(mcp.TextContent).Text
	if !strings.Contains(mcpRaw, `"code":"validation_error"`) {
		t.Errorf("MCP door did not refuse an oversized batch: %s", mcpRaw)
	}

	code, body := touchViaHTTP(t, srv,
		`{"revision_ids":[`+strings.Join(quoted, ",")+`]}`)
	if code != http.StatusBadRequest {
		t.Errorf("HTTP status = %d, want 400; body = %s", code, body)
	}
	if !strings.Contains(body, "validation_error") {
		t.Errorf("HTTP door did not refuse an oversized batch with validation_error: %s", body)
	}

	// Same cap, same number, on both doors — the message names it, so a cap
	// changed on one side and not the other is visible rather than silent.
	want := strconv.Itoa(memory.MaxTouchRevisions)
	if !strings.Contains(mcpRaw, want) || !strings.Contains(body, want) {
		t.Errorf("the two doors do not both name the cap %s: MCP=%s HTTP=%s", want, mcpRaw, body)
	}
}

func TestTouchParity_UnknownIDsReportedByBothDoors(t *testing.T) {
	a, srv, ms := touchSurfaces(t)

	viaMCP := seedTouchable(t, ms, "unknown.mcp")
	viaHTTP := seedTouchable(t, ms, "unknown.http")

	mcpRes := touchViaMCP(t, a, map[string]any{
		"revision_ids": []any{"01NOPE", viaMCP.RevisionID},
	})
	code, body := touchViaHTTP(t, srv,
		`{"revision_ids":["01NOPE","`+viaHTTP.RevisionID+`"]}`)
	if code != http.StatusOK {
		t.Fatalf("a stale ID must not fail the HTTP call: status = %d, body = %s", code, body)
	}
	var httpRes touchWire
	if err := json.Unmarshal([]byte(body), &httpRes); err != nil {
		t.Fatalf("decode: %v; raw=%s", err, body)
	}

	if mcpRes.Touched != 1 || httpRes.Touched != 1 {
		t.Errorf("touched: MCP=%d HTTP=%d, want 1 each — the valid half must land",
			mcpRes.Touched, httpRes.Touched)
	}
	if len(mcpRes.NotFound) != 1 || len(httpRes.NotFound) != 1 {
		t.Fatalf("not_found: MCP=%v HTTP=%v, want one entry each", mcpRes.NotFound, httpRes.NotFound)
	}
	if mcpRes.NotFound[0] != httpRes.NotFound[0] || mcpRes.NotFound[0] != "01NOPE" {
		t.Errorf("not_found contents differ: MCP=%v HTTP=%v", mcpRes.NotFound, httpRes.NotFound)
	}
}
