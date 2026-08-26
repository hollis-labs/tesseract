package mcpadapter

// MCP <-> HTTP argument parity for CW-20260825-0006's search_mode knob.
//
// tests/parity/parity_test.go pairs memory_recall with POST /v1/memory/recall
// and tesseract_lookup with POST /v1/tesseract/lookup, but it asserts only
// that the ROUTES exist. Argument parity has no structural guard, and its
// absence has shipped a defect before: CW-20260825-0003 put payload_mode on an
// MCP tool whose HTTP peer's non-strict decoder discarded the field, so the
// same call returned a different projection depending on which door it came
// in, and an invalid value was a validation error on one surface and a 200 on
// the other.
//
// So this file drives all FOUR doors over the SAME store: any difference it
// reports belongs to the surface, not to the data.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hollis-labs/tesseract/internal/contextapi"
	"github.com/hollis-labs/tesseract/internal/memory"
	"github.com/mark3labs/mcp-go/mcp"
)

const parityTicketID = "CW-20260519-0032"

// seedSearchModeCorpus writes one revision carrying the ticket ID verbatim and
// three that scatter its tokens, so lexical and hybrid must disagree.
func seedSearchModeCorpus(t *testing.T, ms *memory.Store) {
	t.Helper()
	ctx := context.Background()
	rows := []struct {
		key, summary, body string
		status             memory.Status
		confidence         float64
	}{
		{"parity.target", "Decision recorded under " + parityTicketID,
			"the lane that shipped it " + strings.Repeat("padding ", 40),
			memory.StatusDraft, 0.2},
		{"parity.decoy.a", "CW status board", "20260519 was busy; 0032 unrelated",
			memory.StatusCanonical, 1.0},
		{"parity.decoy.b", "0032 retro", "CW planning for 20260519",
			memory.StatusCanonical, 1.0},
		{"parity.decoy.c", "20260519 notes", "CW 0032",
			memory.StatusCanonical, 1.0},
	}
	for _, r := range rows {
		if _, err := ms.WriteRevision(ctx, memory.WriteInput{
			Namespace:  "user/chrispian/memory/notes",
			MemoryKey:  r.key,
			Author:     memory.Author{AgentID: "test", AgentVersion: "1.0"},
			Trigger:    memory.TriggerExplicit,
			SessionID:  "sess-searchmode",
			Origin:     memory.OriginUser,
			Confidence: r.confidence,
			Status:     r.status,
			Payload:    memory.Payload{Summary: r.summary, Body: r.body},
		}); err != nil {
			t.Fatalf("seed %s: %v", r.key, err)
		}
	}
}

// resultKeys pulls memory_key out of a recall/lookup response in any payload
// mode. Comparing the ORDERED keys is the point: two doors can agree on a
// manifest while returning different rows in a different order.
func resultKeys(t *testing.T, raw []byte) []string {
	t.Helper()
	var env struct {
		Results []struct {
			Revision struct {
				MemoryKey string `json:"memory_key"`
			} `json:"revision"`
		} `json:"results"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("decode results: %v\nraw=%s", err, raw)
	}
	out := make([]string, len(env.Results))
	for i, r := range env.Results {
		out[i] = r.Revision.MemoryKey
	}
	return out
}

// searchModeDoors bundles the four call sites under one signature so a case
// table can run every one of them without the table knowing which is which.
type searchModeDoors struct {
	// mcp returns (raw body, ok) — ok is false for a tool error result.
	mcpRecall func(t *testing.T, args map[string]any) (string, bool)
	mcpLookup func(t *testing.T, args map[string]any) (string, bool)
	// http returns (status, raw body).
	httpRecall func(t *testing.T, body string) (int, string)
	httpLookup func(t *testing.T, body string) (int, string)
}

func newSearchModeDoors(a *Adapter, srv *contextapi.Server) searchModeDoors {
	callMCP := func(h func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error)) func(*testing.T, map[string]any) (string, bool) {
		return func(t *testing.T, args map[string]any) (string, bool) {
			t.Helper()
			req := mcp.CallToolRequest{}
			req.Params.Arguments = args
			res, err := h(context.Background(), req)
			if err != nil {
				t.Fatalf("MCP call: %v", err)
			}
			raw := res.Content[0].(mcp.TextContent).Text
			return raw, !isToolError(raw)
		}
	}
	callHTTP := func(path string) func(*testing.T, string) (int, string) {
		return func(t *testing.T, body string) (int, string) {
			t.Helper()
			req := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
			rr := httptest.NewRecorder()
			srv.ServeHTTP(rr, req)
			return rr.Code, rr.Body.String()
		}
	}
	return searchModeDoors{
		mcpRecall:  callMCP(a.handleTesseractRecall),
		mcpLookup:  callMCP(a.handleTesseractRecall),
		httpRecall: callHTTP("/v1/memory/recall"),
		httpLookup: callHTTP("/v1/tesseract/lookup"),
	}
}

// All four doors must return the SAME ordered rows for the same search_mode,
// and lexical must differ from hybrid on all four — otherwise the comparison
// is between two copies of the same ignored argument.
func TestSearchModeParity_AllFourDoorsAgree(t *testing.T) {
	a, srv := bothSurfaces(t, 0)
	seedSearchModeCorpus(t, a.MemoryStore)
	d := newSearchModeDoors(a, srv)
	const ns = `["user/chrispian/memory/notes"]`

	for _, mode := range []string{"", "hybrid", "lexical"} {
		t.Run("mode="+mode, func(t *testing.T) {
			args := map[string]any{"namespaces": ns, "query": parityTicketID, "payload_mode": "keys"}
			body := `{"namespaces":` + ns + `,"query":"` + parityTicketID + `","payload_mode":"keys"`
			if mode != "" {
				args["search_mode"] = mode
				body += `,"search_mode":"` + mode + `"`
			}
			body += `}`

			mcpR, ok := d.mcpRecall(t, args)
			if !ok {
				t.Fatalf("MCP memory_recall errored: %s", mcpR)
			}
			mcpL, ok := d.mcpLookup(t, args)
			if !ok {
				t.Fatalf("MCP tesseract_lookup errored: %s", mcpL)
			}
			codeR, httpR := d.httpRecall(t, body)
			if codeR != http.StatusOK {
				t.Fatalf("HTTP /v1/memory/recall %d: %s", codeR, httpR)
			}
			codeL, httpL := d.httpLookup(t, body)
			if codeL != http.StatusOK {
				t.Fatalf("HTTP /v1/tesseract/lookup %d: %s", codeL, httpL)
			}

			want := resultKeys(t, []byte(mcpR))
			if len(want) == 0 {
				t.Fatalf("MCP memory_recall returned no rows for mode=%q; nothing to compare", mode)
			}
			for _, peer := range []struct {
				name string
				raw  string
			}{
				{"MCP tesseract_lookup", mcpL},
				{"HTTP /v1/memory/recall", httpR},
				{"HTTP /v1/tesseract/lookup", httpL},
			} {
				got := resultKeys(t, []byte(peer.raw))
				if strings.Join(got, ",") != strings.Join(want, ",") {
					t.Errorf("%s returned %v; MCP memory_recall returned %v — the same "+
						"search_mode must mean the same thing on every door", peer.name, got, want)
				}
			}
			t.Logf("mode=%q -> %v", mode, want)
		})
	}

	// The knob must actually bind on every door. Without this, a surface that
	// silently drops search_mode passes every comparison above.
	args := map[string]any{"namespaces": ns, "query": parityTicketID, "payload_mode": "keys"}
	body := `{"namespaces":` + ns + `,"query":"` + parityTicketID + `","payload_mode":"keys"`
	lexArgs := map[string]any{"namespaces": ns, "query": parityTicketID,
		"payload_mode": "keys", "search_mode": "lexical"}
	lexBody := body + `,"search_mode":"lexical"}`
	body += `}`

	type pair struct {
		name              string
		hybridRaw, lexRaw string
	}
	mcpRH, _ := d.mcpRecall(t, args)
	mcpRL, _ := d.mcpRecall(t, lexArgs)
	mcpLH, _ := d.mcpLookup(t, args)
	mcpLL, _ := d.mcpLookup(t, lexArgs)
	_, httpRH := d.httpRecall(t, body)
	_, httpRL := d.httpRecall(t, lexBody)
	_, httpLH := d.httpLookup(t, body)
	_, httpLL := d.httpLookup(t, lexBody)

	for _, p := range []pair{
		{"MCP memory_recall", mcpRH, mcpRL},
		{"MCP tesseract_lookup", mcpLH, mcpLL},
		{"HTTP /v1/memory/recall", httpRH, httpRL},
		{"HTTP /v1/tesseract/lookup", httpLH, httpLL},
	} {
		hyb := resultKeys(t, []byte(p.hybridRaw))
		lex := resultKeys(t, []byte(p.lexRaw))
		if strings.Join(hyb, ",") == strings.Join(lex, ",") {
			t.Errorf("%s: hybrid and lexical returned the same rows (%v) — this door is "+
				"ignoring search_mode, and every parity comparison above is vacuous for it",
				p.name, hyb)
		}
		if len(lex) == 0 || lex[0] != "parity.target" {
			t.Errorf("%s under lexical: top hit %v, want parity.target first", p.name, lex)
		}
		if len(hyb) > 0 && hyb[0] == "parity.target" {
			t.Errorf("%s under hybrid already ranks the exact match first (%v) — "+
				"the fixture no longer demonstrates the dilution lexical removes", p.name, hyb)
		}
	}
}

// An invalid or unsupported search_mode must be an error on every door, not a
// 200 on one and a 400 on another. This is the exact shape of the
// CW-20260825-0003 defect, applied to the new knob.
func TestSearchModeParity_InvalidValuesRejectedOnEveryDoor(t *testing.T) {
	a, srv := bothSurfaces(t, 0)
	seedSearchModeCorpus(t, a.MemoryStore)
	d := newSearchModeDoors(a, srv)
	const ns = `["user/chrispian/memory/notes"]`

	for _, tc := range []struct {
		name string
		args map[string]any
		body string
	}{
		{"unknown value",
			map[string]any{"namespaces": ns, "query": "x", "search_mode": "keyword"},
			`{"namespaces":` + ns + `,"query":"x","search_mode":"keyword"}`},
		{"empty-but-present is not a wildcard",
			map[string]any{"namespaces": ns, "query": "x", "search_mode": "HYBRID"},
			`{"namespaces":` + ns + `,"query":"x","search_mode":"HYBRID"}`},
		{"lexical under a non-relevance ranking",
			map[string]any{"namespaces": ns, "query": "x", "ranking": "activation", "search_mode": "lexical"},
			`{"namespaces":` + ns + `,"query":"x","ranking":"activation","search_mode":"lexical"}`},
		{"semantic under a non-relevance ranking",
			map[string]any{"namespaces": ns, "query": "x", "ranking": "chronological", "search_mode": "semantic"},
			`{"namespaces":` + ns + `,"query":"x","ranking":"chronological","search_mode":"semantic"}`},
		{"lexical with an untokenizable query",
			map[string]any{"namespaces": ns, "query": "--*^:()", "search_mode": "lexical"},
			`{"namespaces":` + ns + `,"query":"--*^:()","search_mode":"lexical"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for name, call := range map[string]func(*testing.T, map[string]any) (string, bool){
				"MCP memory_recall":    d.mcpRecall,
				"MCP tesseract_lookup": d.mcpLookup,
			} {
				raw, ok := call(t, tc.args)
				if ok || !strings.Contains(raw, "validation_error") {
					t.Errorf("%s accepted %s: %s", name, tc.name, raw)
				}
			}
			for name, call := range map[string]func(*testing.T, string) (int, string){
				"HTTP /v1/memory/recall":    d.httpRecall,
				"HTTP /v1/tesseract/lookup": d.httpLookup,
			} {
				code, body := call(t, tc.body)
				if code != http.StatusBadRequest {
					t.Errorf("%s accepted %s: status=%d body=%s", name, tc.name, code, body)
				}
			}
		})
	}
}

// search_mode=semantic with no embedder must fail the same way on every door.
// bothSurfaces wires a store with no embedder, which is the condition the
// daemon reports as "embedding disabled, falling back to BM25-only recall" —
// the case where a silent fallback would be easiest to ship and hardest to see.
func TestSearchModeParity_SemanticWithoutEmbedder(t *testing.T) {
	a, srv := bothSurfaces(t, 0)
	seedSearchModeCorpus(t, a.MemoryStore)
	d := newSearchModeDoors(a, srv)
	const ns = `["user/chrispian/memory/notes"]`

	args := map[string]any{"namespaces": ns, "query": parityTicketID, "search_mode": "semantic"}
	body := `{"namespaces":` + ns + `,"query":"` + parityTicketID + `","search_mode":"semantic"}`

	for name, call := range map[string]func(*testing.T, map[string]any) (string, bool){
		"MCP memory_recall":    d.mcpRecall,
		"MCP tesseract_lookup": d.mcpLookup,
	} {
		raw, ok := call(t, args)
		if ok {
			t.Errorf("%s answered search_mode=semantic with no embedder instead of erroring: %s", name, raw)
		}
		if !strings.Contains(raw, "similarity_unavailable") {
			t.Errorf("%s did not report similarity_unavailable: %s", name, raw)
		}
	}
	for name, call := range map[string]func(*testing.T, string) (int, string){
		"HTTP /v1/memory/recall":    d.httpRecall,
		"HTTP /v1/tesseract/lookup": d.httpLookup,
	} {
		code, raw := call(t, body)
		if code != http.StatusServiceUnavailable {
			t.Errorf("%s returned %d for semantic with no embedder, want 503: %s", name, code, raw)
		}
		if !strings.Contains(raw, "similarity_unavailable") {
			t.Errorf("%s did not report similarity_unavailable: %s", name, raw)
		}
	}

	// Control: the same store answers hybrid fine, so the errors above are a
	// refusal of this mode rather than a broken fixture.
	if raw, ok := d.mcpRecall(t, map[string]any{"namespaces": ns, "query": parityTicketID}); !ok {
		t.Fatalf("hybrid must still answer with no embedder: %s", raw)
	}
}

// A cursor issued under one search_mode must be rejected under another, on
// both doors — the ordering fingerprint has to reach the wire, not just the
// store.
func TestSearchModeParity_CursorIsBoundToTheMode(t *testing.T) {
	a, srv := bothSurfaces(t, 0)
	seedSearchModeCorpus(t, a.MemoryStore)
	d := newSearchModeDoors(a, srv)
	const ns = `["user/chrispian/memory/notes"]`

	raw, ok := d.mcpRecall(t, map[string]any{
		"namespaces": ns, "query": parityTicketID, "payload_mode": "keys", "limit": float64(1),
	})
	if !ok {
		t.Fatalf("hybrid page 1: %s", raw)
	}
	m := decodeManifest(t, []byte(raw))
	if m.NextCursor == nil {
		t.Fatalf("hybrid page 1 issued no cursor (returned %d of %d); nothing to test",
			m.ResultsReturned, m.ResultsTotal)
	}
	cursorJSON, err := json.Marshal(*m.NextCursor)
	if err != nil {
		t.Fatalf("marshal cursor: %v", err)
	}

	resumed, ok := d.mcpRecall(t, map[string]any{
		"namespaces": ns, "query": parityTicketID, "payload_mode": "keys",
		"limit": float64(1), "search_mode": "lexical", "cursor": *m.NextCursor,
	})
	if ok || !strings.Contains(resumed, "validation_error") {
		t.Errorf("MCP accepted a hybrid cursor under search_mode=lexical: %s", resumed)
	}
	code, body := d.httpRecall(t, `{"namespaces":`+ns+`,"query":"`+parityTicketID+
		`","payload_mode":"keys","limit":1,"search_mode":"lexical","cursor":`+string(cursorJSON)+`}`)
	if code != http.StatusBadRequest {
		t.Errorf("HTTP accepted a hybrid cursor under search_mode=lexical: %d %s", code, body)
	}
}
