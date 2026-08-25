package mcpadapter

// CW-20260825-0007. Two knobs land on recall and lookup here: estimate_only,
// the pre-flight that sizes a read before it is paid for, and similarity_min,
// a floor on how closely a result must actually resemble the query.
//
// Both are new MCP <-> HTTP seams, and this file drives BOTH doors over the
// SAME stores so any difference it reports belongs to the surface rather than
// to the data. tests/parity/parity_test.go pairs memory_recall with
// POST /v1/memory/recall but asserts only that the route exists; argument
// parity has no structural guard and its absence has already shipped a defect
// (payload_mode, CW-20260825-0003).

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	embedcontracts "github.com/hollis-labs/go-embed-contracts"
	"github.com/hollis-labs/tesseract/internal/contextapi"
	"github.com/hollis-labs/tesseract/internal/contextpolicy"
	"github.com/hollis-labs/tesseract/internal/contextstore"
	"github.com/hollis-labs/tesseract/internal/knowledge"
	"github.com/hollis-labs/tesseract/internal/memory"
	"github.com/mark3labs/mcp-go/mcp"
)

// ── Fixture ──────────────────────────────────────────────────────────────────

// gradedEmbedder places text on a two-dimensional axis so cosine similarity
// SPREADS across the fixture instead of being equal everywhere.
//
// That spread is what makes a similarity floor testable at all: against a
// fixed-vector embedder every revision scores identically, so any floor either
// keeps everything or drops everything, and a broken floor is indistinguishable
// from a working one.
//
// "beta" subtracts from the same axis "alpha" adds to, which is what lets the
// fixture produce NEGATIVE similarity. Without a negative row a floor of 0.0
// and an absent floor select the same set — the floor is inclusive, matching
// confidence_min's `>=` — and the distinction the pointer type exists to
// preserve would be unobservable in a test.
type gradedEmbedder struct{}

func (gradedEmbedder) Embed(_ context.Context, text, _ string) (*embedcontracts.EmbeddingResult, error) {
	var v [2]float32
	lower := strings.ToLower(text)
	v[0] = float32(strings.Count(lower, "alpha") - strings.Count(lower, "beta"))
	v[1] = float32(strings.Count(lower, "omega"))
	if v[0] == 0 && v[1] == 0 {
		v[1] = 1
	}
	return &embedcontracts.EmbeddingResult{Embedding: v[:], TokenCount: 2}, nil
}

func (e gradedEmbedder) EmbedBatch(ctx context.Context, texts []string, model string) ([]embedcontracts.EmbeddingResult, error) {
	out := make([]embedcontracts.EmbeddingResult, len(texts))
	for i, tx := range texts {
		r, err := e.Embed(ctx, tx, model)
		if err != nil {
			return nil, err
		}
		out[i] = *r
	}
	return out, nil
}

func (gradedEmbedder) EmbeddingDimensions(_ string) int { return 2 }

const (
	estNS       = "user/chrispian/memory/notes"
	estKnowNS   = "user/chrispian/knowledge/framework"
	estNSJSON   = `["user/chrispian/memory/notes"]`
	estBothJSON = `["user/chrispian/memory/notes","user/chrispian/knowledge/framework"]`
)

// estimateSurfaces wires one store to both doors and seeds a corpus with a
// deliberate spread of similarity to the query "alpha".
//
// Every seeded row carries a body, so the three payload modes have genuinely
// different byte totals and an identity that held under one mode but not
// another cannot pass unnoticed. Knowledge rows are seeded too, so the facet
// histogram has non-empty kinds and sources rather than only domains.
func estimateSurfaces(t *testing.T) (*Adapter, *contextapi.Server) {
	t.Helper()
	cs := newTestStore(t)
	embedder := gradedEmbedder{}
	ms := memory.NewStore(cs.DB(), embedder, "test-model", 0, memory.NoopQueue{})
	ks := knowledge.New(ms)

	tok, _, err := cs.CreateAuthToken(context.Background(), contextstore.TokenCreateInput{
		Label:  "estimate",
		Scopes: []string{"memory:read", "memory:write"},
	})
	if err != nil {
		t.Fatalf("create token: %v", err)
	}

	a := New(cs, tok)
	a.MemoryStore = ms
	a.KnowledgeStore = ks
	a.EmbeddingProvider = embedder
	a.EmbeddingModel = "test-model"

	srv := contextapi.NewServer(cs, contextpolicy.New())
	srv.MemoryStore = ms
	srv.KnowledgeStore = ks

	ctx := context.Background()
	// Bodies vary in length as well as in marker content, so bytes_returned
	// differs per row and a byte total cannot be right by coincidence.
	for _, row := range []struct {
		key  string
		text string
	}{
		{"est.strong.one", "alpha alpha alpha " + strings.Repeat("x", 120)},
		{"est.strong.two", "alpha alpha " + strings.Repeat("y", 200)},
		{"est.mixed", "alpha omega " + strings.Repeat("z", 80)},
		// Orthogonal to an "alpha" query: cosine exactly 0.
		{"est.orthogonal.one", "omega omega " + strings.Repeat("w", 160)},
		{"est.orthogonal.two", "omega " + strings.Repeat("v", 40)},
		// Opposed to an "alpha" query: cosine strictly negative. The only rows
		// a floor of 0.0 removes, and therefore the only reason that floor is
		// distinguishable from passing nothing.
		{"est.opposed", "beta beta " + strings.Repeat("u", 100)},
	} {
		rev, err := ms.WriteRevision(ctx, memory.WriteInput{
			Namespace:  estNS,
			MemoryKey:  row.key,
			Author:     memory.Author{AgentID: "test", AgentVersion: "1.0"},
			Trigger:    memory.TriggerExplicit,
			SessionID:  "sess-estimate",
			Origin:     memory.OriginUser,
			Confidence: 0.9,
			Status:     memory.StatusCanonical,
			Payload:    memory.Payload{Summary: row.text, Body: row.text},
		})
		if err != nil {
			t.Fatalf("seed %s: %v", row.key, err)
		}
		if err := ms.EmbedRevision(ctx, rev.RevisionID, "test-model"); err != nil {
			t.Fatalf("embed %s: %v", row.key, err)
		}
	}

	for _, k := range []struct{ key, kind, source, summary string }{
		{"framework.go-providers", "package", "filesystem", "alpha provider adapter"},
		{"framework.notes", "doc", "obsidian", "omega design notes"},
	} {
		rev, err := ks.Write(ctx, knowledge.WriteInput{
			Namespace: estKnowNS,
			Key:       k.key,
			Kind:      k.kind,
			Source:    k.source,
			Pointer:   memory.Pointer{Scheme: "file", Locator: "/pkg/" + k.key},
			Summary:   k.summary,
			Author:    memory.Author{AgentID: "indexer", AgentVersion: "1.0"},
			SessionID: "indexer:01HX",
		})
		if err != nil {
			t.Fatalf("seed knowledge %s: %v", k.key, err)
		}
		if err := ms.EmbedRevision(ctx, rev.RevisionID, "test-model"); err != nil {
			t.Fatalf("embed knowledge %s: %v", k.key, err)
		}
	}

	return a, srv
}

// ── Call helpers ─────────────────────────────────────────────────────────────

func estCall(t *testing.T, a *Adapter, tool string, args map[string]any) string {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	var (
		res *mcp.CallToolResult
		err error
	)
	switch tool {
	case "memory_recall":
		res, err = a.handleMemoryRecall(context.Background(), req)
	case "tesseract_lookup":
		res, err = a.handleTesseractLookup(context.Background(), req)
	default:
		t.Fatalf("unknown tool %q", tool)
	}
	if err != nil {
		t.Fatalf("%s: %v", tool, err)
	}
	return res.Content[0].(mcp.TextContent).Text
}

func callRoute(t *testing.T, srv *contextapi.Server, route, body string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, route, bytes.NewBufferString(body))
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	return rr.Code, rr.Body.String()
}

// estimateFacets is the facet histogram as it appears on the wire. Both doors
// serialize the same three families; the MCP tool builds a nested map and the
// HTTP route a struct, so decoding into one shape here is also a check that
// they agree on the wire.
type estimateFacets struct {
	Domains map[string]int `json:"domains"`
	Kinds   map[string]int `json:"kinds"`
	Sources map[string]int `json:"sources"`
}

type estimateEnvelopeWire struct {
	Results      json.RawMessage `json:"results"`
	Facets       *estimateFacets `json:"facets"`
	Manifest     memory.Manifest `json:"manifest"`
	EstimateOnly bool            `json:"estimate_only"`
}

func decodeEnvelope(t *testing.T, raw string) estimateEnvelopeWire {
	t.Helper()
	var env estimateEnvelopeWire
	if err := json.Unmarshal([]byte(raw), &env); err != nil {
		t.Fatalf("decode envelope: %v; raw=%s", err, raw)
	}
	return env
}

// ── AC1: estimate_only is an exact identity ──────────────────────────────────

// For each payload_mode, estimate_only must report the count and byte total
// the identical query WITHOUT estimate_only actually returns under that same
// mode, in the same run against the same corpus. Equality, not tolerance.
//
// The comparison is self-referential by design: both halves are produced from
// the same fixture in the same test, so nothing here can drift against an
// external referent. What it can catch is an estimate computed by a second,
// cheaper code path that disagrees with the real one — which is the failure
// mode a pre-flight has.
//
// NON-VACUITY PRECONDITION: the reference call must return a non-zero count AND
// a non-zero byte total before any comparison runs. Without it `0 == 0` passes
// on a fixture that could not have detected a wrong estimate, and the criterion
// certifies nothing. It is t.Fatalf rather than t.Errorf so the test REFUSES to
// run the comparison rather than reporting a pass beside a warning.
func TestEstimateOnly_ExactIdentityPerPayloadMode(t *testing.T) {
	for _, tool := range []struct {
		name  string
		route string
		ns    string
	}{
		{"memory_recall", "/v1/memory/recall", estNSJSON},
		{"tesseract_lookup", "/v1/tesseract/lookup", estBothJSON},
	} {
		for _, mode := range []string{"keys", "summary", "full"} {
			t.Run(tool.name+"/payload_mode="+mode, func(t *testing.T) {
				a, srv := estimateSurfaces(t)

				mcpArgs := func(estimate bool) map[string]any {
					args := map[string]any{
						"namespaces":   tool.ns,
						"ranking":      "chronological",
						"payload_mode": mode,
					}
					if estimate {
						args["estimate_only"] = true
					}
					return args
				}
				httpBody := func(estimate bool) string {
					b := `{"namespaces":` + tool.ns + `,"ranking":"chronological","payload_mode":"` + mode + `"`
					if estimate {
						b += `,"estimate_only":true`
					}
					return b + `}`
				}

				actual := decodeEnvelope(t, estCall(t, a, tool.name, mcpArgs(false)))

				// ── Non-vacuity precondition ──────────────────────────────
				if actual.Manifest.ResultsReturned == 0 {
					t.Fatalf("fixture returned no rows under payload_mode=%s; a 0 == 0 "+
						"comparison cannot detect a wrong estimate — fix the fixture, "+
						"do not weaken the assertion", mode)
				}
				if actual.Manifest.BytesReturned == 0 {
					t.Fatalf("fixture returned 0 bytes under payload_mode=%s; the byte "+
						"identity below would be vacuous", mode)
				}

				est := decodeEnvelope(t, estCall(t, a, tool.name, mcpArgs(true)))

				// The whole manifest, compared as the wire renders it — count,
				// bytes, token proxy, total, truncation state and cursor at
				// once. Marshaling rather than == because NextCursor is a
				// *string and two equal cursors are two different pointers.
				gotM, wantM := manifestKey(t, est.Manifest), manifestKey(t, actual.Manifest)
				if gotM != wantM {
					t.Errorf("estimate_only manifest is not the identity of the real read "+
						"under payload_mode=%s:\nestimate: %s\n    real: %s", mode, gotM, wantM)
				}

				// The rows must be WITHHELD, not empty. An empty array reads as
				// "nothing matched", which is exactly the question a pre-flight
				// is asked.
				if est.Results != nil {
					t.Errorf("estimate_only emitted a results key (%s); it must be absent, "+
						"because an empty array is indistinguishable from no matches",
						string(est.Results))
				}
				if !est.EstimateOnly {
					t.Error("estimate_only response does not carry estimate_only: true, " +
						"so a caller cannot tell a withheld array from a missing one")
				}

				// ── The facet histogram is subject to the same identity ────
				if actual.Facets != nil {
					if est.Facets == nil {
						t.Fatalf("the real read carries facets but the estimate does not")
					}
					assertFacetIdentity(t, mode, *est.Facets, *actual.Facets)
				} else if est.Facets != nil {
					t.Errorf("estimate reports a facet histogram the real read does not "+
						"return (%+v); an estimate must not advertise a number its "+
						"read cannot produce", *est.Facets)
				}

				// ── Same identity through the HTTP door ───────────────────
				codeReal, bodyReal := callRoute(t, srv, tool.route, httpBody(false))
				if codeReal != http.StatusOK {
					t.Fatalf("HTTP %s real read: %d %s", tool.route, codeReal, bodyReal)
				}
				codeEst, bodyEst := callRoute(t, srv, tool.route, httpBody(true))
				if codeEst != http.StatusOK {
					t.Fatalf("HTTP %s estimate: %d %s", tool.route, codeEst, bodyEst)
				}
				httpReal := decodeEnvelope(t, bodyReal)
				httpEst := decodeEnvelope(t, bodyEst)

				if httpReal.Manifest.ResultsReturned == 0 || httpReal.Manifest.BytesReturned == 0 {
					t.Fatalf("HTTP fixture is vacuous under payload_mode=%s: %+v",
						mode, httpReal.Manifest)
				}
				if got, want := manifestKey(t, httpEst.Manifest), manifestKey(t, httpReal.Manifest); got != want {
					t.Errorf("HTTP estimate_only manifest is not the identity of the real read "+
						"under payload_mode=%s:\nestimate: %s\n    real: %s", mode, got, want)
				}
				if httpEst.Results != nil {
					t.Errorf("HTTP estimate emitted a results key: %s", string(httpEst.Results))
				}
				if !httpEst.EstimateOnly {
					t.Error("HTTP estimate response does not carry estimate_only: true")
				}

				// ── MCP and HTTP must agree with each other ───────────────
				if got, want := manifestKey(t, est.Manifest), manifestKey(t, httpEst.Manifest); got != want {
					t.Errorf("the two doors disagree about the estimate under payload_mode=%s:\n"+
						" MCP: %s\nHTTP: %s", mode, got, want)
				}
			})
		}
	}
}

// assertFacetIdentity compares two histograms family by family and key by key,
// so a report names the facet that moved rather than only that something did.
//
// It also requires the histogram to be non-empty, for the same reason the
// count and byte totals carry a non-vacuity precondition: two empty maps
// compare equal on a fixture that seeds no facets at all.
func assertFacetIdentity(t *testing.T, mode string, est, actual estimateFacets) {
	t.Helper()

	families := []struct {
		name        string
		est, actual map[string]int
		mustBeFull  bool
	}{
		// domains is populated by every fixture row; kinds and sources come
		// from the knowledge rows. All three are required to be non-empty so
		// an all-empty histogram cannot satisfy the identity.
		{"domains", est.Domains, actual.Domains, true},
		{"kinds", est.Kinds, actual.Kinds, true},
		{"sources", est.Sources, actual.Sources, true},
	}

	for _, f := range families {
		if f.mustBeFull && len(f.actual) == 0 {
			t.Fatalf("facet family %q is empty in the real read under payload_mode=%s; "+
				"comparing two empty histograms proves nothing", f.name, mode)
		}
		if len(f.est) != len(f.actual) {
			t.Errorf("facet family %q: estimate has %d keys, real read has %d (%v vs %v)",
				f.name, len(f.est), len(f.actual), f.est, f.actual)
			continue
		}
		for k, want := range f.actual {
			got, ok := f.est[k]
			if !ok {
				t.Errorf("facet %s[%q] is missing from the estimate; the real read counts %d",
					f.name, k, want)
				continue
			}
			if got != want {
				t.Errorf("facet %s[%q]: estimate says %d, real read returns %d",
					f.name, k, got, want)
			}
		}
	}
}

// An estimate must not change what a subsequent real read returns, and the
// cursor it issues must be usable. estimate_only is a serialization knob, and
// the claim that it "changes what is serialized, never which rows match or in
// what order" is in the tool description, so it carries the same evidence
// burden as code.
func TestEstimateOnly_CursorResumesTheRealRead(t *testing.T) {
	a, _ := estimateSurfaces(t)

	est := decodeEnvelope(t, estCall(t, a, "memory_recall", map[string]any{
		"namespaces": estNSJSON, "ranking": "chronological",
		"payload_mode": "summary", "limit": float64(2), "estimate_only": true,
	}))
	if est.Manifest.NextCursor == nil {
		t.Fatalf("limit=2 over a 5-row fixture issued no cursor: %+v", est.Manifest)
	}

	// The first page of the real read, for comparison.
	first := decodeScored(t, estCall(t, a, "memory_recall", map[string]any{
		"namespaces": estNSJSON, "ranking": "chronological",
		"payload_mode": "summary", "limit": float64(2),
	}))
	if len(first) != 2 {
		t.Fatalf("first page returned %d rows, want 2", len(first))
	}

	// The cursor an ESTIMATE issued, spent on a REAL read.
	resumedRaw := estCall(t, a, "memory_recall", map[string]any{
		"namespaces": estNSJSON, "ranking": "chronological",
		"payload_mode": "summary", "limit": float64(2),
		"cursor": *est.Manifest.NextCursor,
	})
	if strings.Contains(resumedRaw, "validation_error") {
		t.Fatalf("an estimate-issued cursor was rejected by a real read: %s", resumedRaw)
	}
	resumed := decodeScored(t, resumedRaw)
	if len(resumed) != 2 {
		t.Fatalf("resumed page returned %d rows, want 2", len(resumed))
	}
	// It must be the SECOND page, not the first again. Manifest carries no
	// offset, so the rows themselves are the evidence that the cursor advanced.
	for _, r := range resumed {
		for _, f := range first {
			if r.RevisionID == f.RevisionID {
				t.Errorf("row %s appears on both the first page and the estimate-resumed "+
					"page; the cursor did not advance", r.RevisionID)
			}
		}
	}
}

// ── The config default <-> wire shape seam ───────────────────────────────────

// A caller that passes NO arguments beyond the required ones must get the
// response it got before these knobs existed, under every configuration of
// them. CW-20260825-0004 shipped the inverse: a configured budget silently
// flipped the envelope for callers who passed nothing.
//
// Neither new knob has a config default — similarity_min is per-call only and
// estimate_only is a plain bool with no config key — and this test is what
// pins that. It permutes the server-side settings that DO exist beside them
// (read.payload_mode, read.budget_bytes) and asserts the no-argument response
// still carries results and does not carry estimate_only.
func TestNewKnobs_NoArgumentCallerKeepsTheOldShape(t *testing.T) {
	for _, mode := range []string{"", "keys", "summary", "full"} {
		for _, budget := range []int{0, 4096} {
			label := "payload_mode=" + mode + ",budget_bytes=" + itoa(budget)
			t.Run(label, func(t *testing.T) {
				a, srv := estimateSurfaces(t)
				a.DefaultPayloadMode = memory.PayloadMode(mode)
				a.DefaultBudget = memory.Budget{Bytes: budget}
				srv.RuntimeConfig.Read.PayloadMode = mode
				srv.RuntimeConfig.Read.BudgetBytes = budget

				raw := estCall(t, a, "memory_recall", map[string]any{"namespaces": estNSJSON})
				env := decodeEnvelope(t, raw)
				if env.Results == nil {
					t.Errorf("a caller passing no arguments got no results key under %s; "+
						"a server-side setting must never turn a read into an estimate", label)
				}
				if env.EstimateOnly {
					t.Errorf("a caller passing no arguments got estimate_only: true under %s", label)
				}
				if strings.Contains(raw, "similarity_min") {
					t.Errorf("a no-argument response mentions similarity_min under %s: %s", label, raw)
				}

				code, body := callRoute(t, srv, "/v1/memory/recall", `{"namespaces":`+estNSJSON+`}`)
				if code != http.StatusOK {
					t.Fatalf("HTTP no-argument recall under %s: %d %s", label, code, body)
				}
				httpEnv := decodeEnvelope(t, body)
				if httpEnv.Results == nil {
					t.Errorf("HTTP no-argument caller got no results key under %s: %s", label, body)
				}
				if httpEnv.EstimateOnly {
					t.Errorf("HTTP no-argument caller got estimate_only: true under %s", label)
				}
			})
		}
	}
}

// ── similarity_min ───────────────────────────────────────────────────────────

// The floor must actually remove rows, and remove exactly the rows scoring
// below it. Asserting only "fewer rows came back" would pass for a floor that
// dropped an arbitrary suffix.
func TestSimilarityMin_DropsExactlyTheRowsBelowTheFloor(t *testing.T) {
	a, _ := estimateSurfaces(t)

	unfiltered := decodeScored(t, estCall(t, a, "memory_recall", map[string]any{
		"namespaces": estNSJSON, "ranking": "similarity", "query": "alpha",
		"payload_mode": "summary", "limit": float64(50),
	}))
	if len(unfiltered) < 3 {
		t.Fatalf("fixture returned %d scored rows; a floor cannot be shown to "+
			"discriminate on fewer than three", len(unfiltered))
	}
	// The floor is only meaningful if the scores actually spread.
	if unfiltered[0].Score == unfiltered[len(unfiltered)-1].Score {
		t.Fatalf("every row scored %v; a floor cannot be distinguished from a no-op "+
			"on a fixture with no spread", unfiltered[0].Score)
	}

	// A floor placed strictly between the best and worst score, so it must
	// keep some rows and drop others — never all or nothing.
	floor := (unfiltered[0].Score + unfiltered[len(unfiltered)-1].Score) / 2
	var wantIDs []string
	for _, r := range unfiltered {
		if r.Score >= floor {
			wantIDs = append(wantIDs, r.RevisionID)
		}
	}
	if len(wantIDs) == 0 || len(wantIDs) == len(unfiltered) {
		t.Fatalf("floor %v keeps %d of %d rows — it discriminates nothing",
			floor, len(wantIDs), len(unfiltered))
	}

	filtered := decodeScored(t, estCall(t, a, "memory_recall", map[string]any{
		"namespaces": estNSJSON, "ranking": "similarity", "query": "alpha",
		"payload_mode": "summary", "limit": float64(50), "similarity_min": floor,
	}))

	var gotIDs []string
	for _, r := range filtered {
		gotIDs = append(gotIDs, r.RevisionID)
		if r.Score < floor {
			t.Errorf("row %s scored %v, below the floor %v it was supposed to clear",
				r.RevisionID, r.Score, floor)
		}
	}
	if strings.Join(gotIDs, ",") != strings.Join(wantIDs, ",") {
		t.Errorf("similarity_min=%v kept %v; the rows at or above that score are %v",
			floor, gotIDs, wantIDs)
	}
}

// The floor must bind on BOTH paths that carry a cosine score, not just the
// one that is easiest to reach.
//
// ranking=similarity and ranking=relevance+search_mode=semantic are two
// different pipelines — recallOrdered/applySimilarityFloor for the first,
// fetchCosineScored for the second — and a floor wired into one and not the
// other is silently accepted on the unwired path rather than refused. That is
// worse than a rejection: the caller is told nothing and gets rows it excluded.
//
// This test exists because it did not, at first. A mutation that disabled the
// floor inside the semantic arm left every other similarity test passing:
// TestSimilarityMin_RefusedWhereThereIsNoSimilarity only asserts that
// relevance+semantic is ACCEPTED, which a no-op floor also satisfies.
func TestSimilarityMin_BindsOnTheSemanticArmToo(t *testing.T) {
	a, _ := estimateSurfaces(t)

	args := func(extra map[string]any) map[string]any {
		out := map[string]any{
			"namespaces": estNSJSON, "ranking": "relevance", "search_mode": "semantic",
			"query": "alpha", "payload_mode": "summary", "limit": float64(50),
		}
		for k, v := range extra {
			out[k] = v
		}
		return out
	}

	unfiltered := decodeScored(t, estCall(t, a, "memory_recall", args(nil)))
	if len(unfiltered) < 3 {
		t.Fatalf("semantic arm returned %d rows; a floor cannot be shown to "+
			"discriminate on fewer than three", len(unfiltered))
	}
	if unfiltered[0].Score == unfiltered[len(unfiltered)-1].Score {
		t.Fatalf("every semantic row scored %v; a floor cannot be distinguished "+
			"from a no-op with no spread", unfiltered[0].Score)
	}

	floor := (unfiltered[0].Score + unfiltered[len(unfiltered)-1].Score) / 2
	var wantIDs []string
	for _, r := range unfiltered {
		if r.Score >= floor {
			wantIDs = append(wantIDs, r.RevisionID)
		}
	}
	if len(wantIDs) == 0 || len(wantIDs) == len(unfiltered) {
		t.Fatalf("floor %v keeps %d of %d semantic rows — it discriminates nothing",
			floor, len(wantIDs), len(unfiltered))
	}

	filtered := decodeScored(t, estCall(t, a, "memory_recall",
		args(map[string]any{"similarity_min": floor})))

	var gotIDs []string
	for _, r := range filtered {
		gotIDs = append(gotIDs, r.RevisionID)
		if r.Score < floor {
			t.Errorf("semantic row %s scored %v, below the floor %v it was supposed to clear",
				r.RevisionID, r.Score, floor)
		}
	}
	if strings.Join(gotIDs, ",") != strings.Join(wantIDs, ",") {
		t.Errorf("similarity_min=%v on the semantic arm kept %v; the rows at or above "+
			"that score are %v", floor, gotIDs, wantIDs)
	}

	// The floor must narrow the candidate set on this arm too, not the page.
	full := decodeEnvelope(t, estCall(t, a, "memory_recall",
		args(map[string]any{"similarity_min": floor})))
	paged := decodeEnvelope(t, estCall(t, a, "memory_recall",
		args(map[string]any{"similarity_min": floor, "limit": float64(1)})))
	if paged.Manifest.ResultsTotal != full.Manifest.ResultsTotal {
		t.Errorf("semantic results_total is %d at limit=1 and %d at limit=50; the floor "+
			"is being applied to the page rather than to the candidate set",
			paged.Manifest.ResultsTotal, full.Manifest.ResultsTotal)
	}
}

// 0.0 is a legal floor and it is not the same as passing nothing.
//
// The floor is INCLUSIVE — a row clears it at score >= floor, matching
// confidence_min's SQL `>=` — so a floor of 0.0 keeps orthogonal rows and
// removes only strictly negative ones. That makes the negative row in the
// fixture load-bearing rather than decorative: without it the two calls select
// the same set and the distinction is unobservable.
//
// This is the wire-level half of the pointer decision; the fingerprint half is
// TestFingerprint_DistinguishesAbsentFromZeroFloor in internal/memory.
func TestSimilarityMin_ZeroIsAFloorNotAnAbsence(t *testing.T) {
	a, _ := estimateSurfaces(t)

	args := func(extra map[string]any) map[string]any {
		out := map[string]any{
			"namespaces": estNSJSON, "ranking": "similarity", "query": "alpha",
			"payload_mode": "summary", "limit": float64(50),
		}
		for k, v := range extra {
			out[k] = v
		}
		return out
	}

	absent := decodeScored(t, estCall(t, a, "memory_recall", args(nil)))
	atZero := decodeScored(t, estCall(t, a, "memory_recall",
		args(map[string]any{"similarity_min": 0.0})))

	// ── Non-vacuity, in both directions ───────────────────────────────────
	// A negative row must exist, or a floor of 0.0 removes nothing and the
	// comparison below passes for the wrong reason.
	var negative, zero []string
	for _, r := range absent {
		switch {
		case r.Score < 0:
			negative = append(negative, r.RevisionID)
		case r.Score == 0:
			zero = append(zero, r.RevisionID)
		}
	}
	if len(negative) == 0 {
		t.Fatalf("no row scored below 0; a floor of 0.0 cannot be distinguished from "+
			"an absent floor on this fixture (%d rows)", len(absent))
	}
	// An orthogonal row must exist too, or the inclusivity of the boundary is
	// untested — a floor implemented as `>` rather than `>=` would pass.
	if len(zero) == 0 {
		t.Fatalf("no row scored exactly 0; whether the floor is inclusive at its own "+
			"boundary is untested (%d rows)", len(absent))
	}

	kept := map[string]bool{}
	for _, r := range atZero {
		kept[r.RevisionID] = true
		if r.Score < 0 {
			t.Errorf("row %s scored %v and survived similarity_min=0.0; a floor of 0.0 "+
				"is being read as no floor at all — the exact conflation the pointer "+
				"type exists to prevent", r.RevisionID, r.Score)
		}
	}
	for _, id := range negative {
		if kept[id] {
			t.Errorf("opposed row %s survived similarity_min=0.0", id)
		}
	}
	for _, id := range zero {
		if !kept[id] {
			t.Errorf("orthogonal row %s scored exactly 0 and was dropped by "+
				"similarity_min=0.0; the floor must be inclusive at its own boundary, "+
				"as confidence_min is", id)
		}
	}
	if len(atZero) != len(absent)-len(negative) {
		t.Errorf("similarity_min=0.0 returned %d rows; absent returned %d of which %d "+
			"scored below zero, so a floor of zero should return %d",
			len(atZero), len(absent), len(negative), len(absent)-len(negative))
	}
}

// The floor is honored only where cosine similarity IS the score. Everywhere
// else it is refused rather than ignored, on both doors, with the same code.
//
// Silently dropping the knob would hand back a differently-filtered result set
// under the name the caller asked for, which is the failure search_mode's own
// validation was written to prevent.
func TestSimilarityMin_RefusedWhereThereIsNoSimilarity(t *testing.T) {
	a, srv := estimateSurfaces(t)

	for _, tc := range []struct {
		name     string
		extra    map[string]any
		bodyFrag string
		wantOK   bool
	}{
		{"ranking=similarity", map[string]any{"ranking": "similarity", "query": "alpha"},
			`"ranking":"similarity","query":"alpha"`, true},
		{"relevance+semantic", map[string]any{"ranking": "relevance", "search_mode": "semantic", "query": "alpha"},
			`"ranking":"relevance","search_mode":"semantic","query":"alpha"`, true},
		// hybrid is the DEFAULT search mode, so this is the combination a
		// caller reaches by accident. Its score is an RRF fusion of arm
		// positions times activation modifiers — not a similarity — and its
		// BM25 arm admits rows with no embedding at all, so a floor could not
		// bound what comes back.
		{"relevance+hybrid", map[string]any{"ranking": "relevance", "search_mode": "hybrid", "query": "alpha"},
			`"ranking":"relevance","search_mode":"hybrid","query":"alpha"`, false},
		{"relevance+lexical", map[string]any{"ranking": "relevance", "search_mode": "lexical", "query": "alpha"},
			`"ranking":"relevance","search_mode":"lexical","query":"alpha"`, false},
		{"activation", map[string]any{"ranking": "activation"},
			`"ranking":"activation"`, false},
		{"chronological", map[string]any{"ranking": "chronological"},
			`"ranking":"chronological"`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := map[string]any{"namespaces": estNSJSON, "similarity_min": 0.1}
			for k, v := range tc.extra {
				args[k] = v
			}
			raw := estCall(t, a, "memory_recall", args)
			gotErr := strings.Contains(raw, "validation_error")
			if gotErr == tc.wantOK {
				t.Errorf("MCP: wantOK=%v but validation_error=%v; raw=%s", tc.wantOK, gotErr, raw)
			}

			code, body := callRoute(t, srv, "/v1/memory/recall",
				`{"namespaces":`+estNSJSON+`,"similarity_min":0.1,`+tc.bodyFrag+`}`)
			wantCode := http.StatusBadRequest
			if tc.wantOK {
				wantCode = http.StatusOK
			}
			if code != wantCode {
				t.Errorf("HTTP: status=%d want %d; body=%s", code, wantCode, body)
			}
			// The doors must not merely both fail — they must fail the same
			// way. A 400 with a different code is still a divergence an agent
			// branching on error codes would trip over.
			if !tc.wantOK && !strings.Contains(body, "validation_error") {
				t.Errorf("HTTP rejected %s without validation_error: %s", tc.name, body)
			}
		})
	}
}

// The range check, on both doors. A floor outside [-1, 1] cannot ever bind or
// ever release, so it is a caller mistake whose only symptom would be an empty
// page that reads exactly like an empty corpus.
func TestSimilarityMin_RangeRejectedOnBothDoors(t *testing.T) {
	a, srv := estimateSurfaces(t)

	for _, tc := range []struct {
		name   string
		value  float64
		wantOK bool
	}{
		{"below range", -1.5, false},
		{"above range", 1.5, false},
		{"lower bound is legal", -1, true},
		{"upper bound is legal", 1, true},
		{"zero is legal", 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := estCall(t, a, "memory_recall", map[string]any{
				"namespaces": estNSJSON, "ranking": "similarity", "query": "alpha",
				"similarity_min": tc.value,
			})
			gotErr := strings.Contains(raw, "validation_error")
			if gotErr == tc.wantOK {
				t.Errorf("MCP similarity_min=%v: wantOK=%v got validation_error=%v; raw=%s",
					tc.value, tc.wantOK, gotErr, raw)
			}

			code, body := callRoute(t, srv, "/v1/memory/recall",
				`{"namespaces":`+estNSJSON+`,"ranking":"similarity","query":"alpha","similarity_min":`+
					ftoa(tc.value)+`}`)
			wantCode := http.StatusBadRequest
			if tc.wantOK {
				wantCode = http.StatusOK
			}
			if code != wantCode {
				t.Errorf("HTTP similarity_min=%v: status=%d want %d; body=%s",
					tc.value, code, wantCode, body)
			}
		})
	}
}

// Both doors must produce the identical response for the identical floor, on
// BOTH tools. This is the argument-parity assertion the route-existence
// harness cannot make.
//
// tesseract_lookup is covered as well as memory_recall because they decode the
// argument through different structs — memoryRecallRequest declares it flat
// beside `filters`, tesseractLookupRequest flattens every filter — so a knob
// wired into one says nothing about the other. A mutation that dropped
// similarity_min from the HTTP lookup route was not caught until this loop
// covered both routes.
func TestSimilarityMinParity_MCPvsHTTP(t *testing.T) {
	a, srv := estimateSurfaces(t)

	for _, tool := range []struct{ name, route, ns string }{
		{"memory_recall", "/v1/memory/recall", estNSJSON},
		{"tesseract_lookup", "/v1/tesseract/lookup", estBothJSON},
	} {
		for _, floor := range []float64{0, 0.25, 0.5} {
			t.Run(tool.name+"/similarity_min="+ftoa(floor), func(t *testing.T) {
				raw := estCall(t, a, tool.name, map[string]any{
					"namespaces": tool.ns, "ranking": "similarity", "query": "alpha",
					"payload_mode": "summary", "similarity_min": floor,
				})
				mcpM, ok := tryManifest([]byte(raw))
				if !ok {
					t.Fatalf("MCP returned no manifest: %s", raw)
				}

				code, body := callRoute(t, srv, tool.route,
					`{"namespaces":`+tool.ns+`,"ranking":"similarity","query":"alpha",`+
						`"payload_mode":"summary","similarity_min":`+ftoa(floor)+`}`)
				if code != http.StatusOK {
					t.Fatalf("HTTP %s: %d %s", tool.route, code, body)
				}
				httpM := decodeManifest(t, []byte(body))
				sameManifest(t, tool.name+" similarity_min="+ftoa(floor), mcpM, httpM)

				// Without this the parity comparison could be two identical
				// responses to a floor that bound nothing.
				if mcpM.ResultsReturned == 0 {
					t.Fatalf("floor %v returned nothing; parity here is vacuous", floor)
				}
				// And the floor has to have REMOVED something at the tighter
				// settings, or both doors are being compared unfiltered.
				if floor > 0 && !mcpM.Truncated && mcpM.ResultsTotal == 0 {
					t.Fatalf("floor %v produced an empty total; nothing is being compared", floor)
				}
			})
		}

		// The binding check per tool: a tight floor must return strictly fewer
		// rows than a floor that admits everything, through the HTTP door too.
		t.Run(tool.name+"/floor binds on HTTP", func(t *testing.T) {
			read := func(floor string) memory.Manifest {
				code, body := callRoute(t, srv, tool.route,
					`{"namespaces":`+tool.ns+`,"ranking":"similarity","query":"alpha",`+
						`"payload_mode":"summary","similarity_min":`+floor+`}`)
				if code != http.StatusOK {
					t.Fatalf("HTTP %s floor=%s: %d %s", tool.route, floor, code, body)
				}
				return decodeManifest(t, []byte(body))
			}
			loose, tight := read("-1"), read("0.9")
			if loose.ResultsTotal == 0 {
				t.Fatalf("floor -1 returned nothing on %s; the comparison is vacuous", tool.route)
			}
			if tight.ResultsTotal >= loose.ResultsTotal {
				t.Errorf("on %s, similarity_min=0.9 matched %d rows and -1 matched %d; "+
					"the floor is being dropped by this route",
					tool.route, tight.ResultsTotal, loose.ResultsTotal)
			}
		})
	}

	// A floor that binds must return FEWER rows than one that does not, or
	// every comparison above is between two unfiltered responses.
	loose := decodeScored(t, estCall(t, a, "memory_recall", map[string]any{
		"namespaces": estNSJSON, "ranking": "similarity", "query": "alpha",
		"payload_mode": "summary", "similarity_min": -1.0,
	}))
	tight := decodeScored(t, estCall(t, a, "memory_recall", map[string]any{
		"namespaces": estNSJSON, "ranking": "similarity", "query": "alpha",
		"payload_mode": "summary", "similarity_min": 0.9,
	}))
	if len(tight) >= len(loose) {
		t.Errorf("similarity_min=0.9 returned %d rows and -1.0 returned %d; the floor "+
			"is not binding, so the parity cases above prove nothing", len(tight), len(loose))
	}
}

// The floor must narrow the qualifying set BEFORE limit windows it, not thin a
// page after the fact. A floor applied after LIMIT returns "the top N, minus
// whichever happened to be weak" — a sample of the qualifying set rather than
// the set, and results_total would describe a population the caller cannot
// reach by paging.
func TestSimilarityMin_AppliesBeforeLimit(t *testing.T) {
	a, _ := estimateSurfaces(t)

	full := decodeEnvelope(t, estCall(t, a, "memory_recall", map[string]any{
		"namespaces": estNSJSON, "ranking": "similarity", "query": "alpha",
		"payload_mode": "summary", "limit": float64(50), "similarity_min": 0.5,
	}))
	paged := decodeEnvelope(t, estCall(t, a, "memory_recall", map[string]any{
		"namespaces": estNSJSON, "ranking": "similarity", "query": "alpha",
		"payload_mode": "summary", "limit": float64(1), "similarity_min": 0.5,
	}))

	if full.Manifest.ResultsTotal == 0 {
		t.Fatal("floor 0.5 admitted no rows; this test cannot discriminate")
	}
	if paged.Manifest.ResultsTotal != full.Manifest.ResultsTotal {
		t.Errorf("results_total is %d at limit=1 and %d at limit=50; the floor is being "+
			"applied to the page rather than to the candidate set",
			paged.Manifest.ResultsTotal, full.Manifest.ResultsTotal)
	}
	// And the unfiltered total must be LARGER, or "applies before limit" is
	// being asserted about a floor that removes nothing.
	unfiltered := decodeEnvelope(t, estCall(t, a, "memory_recall", map[string]any{
		"namespaces": estNSJSON, "ranking": "similarity", "query": "alpha",
		"payload_mode": "summary", "limit": float64(50),
	}))
	if unfiltered.Manifest.ResultsTotal <= full.Manifest.ResultsTotal {
		t.Errorf("unfiltered total %d is not greater than filtered total %d; the floor "+
			"removed nothing", unfiltered.Manifest.ResultsTotal, full.Manifest.ResultsTotal)
	}
}

// The two knobs must compose: an estimate of a floored read reports the floored
// numbers. If similarity_min were applied on one path and not the other, the
// pre-flight would size a read the caller never gets.
func TestEstimateOnly_ReflectsTheSimilarityFloor(t *testing.T) {
	a, _ := estimateSurfaces(t)

	args := func(estimate bool, floor float64) map[string]any {
		out := map[string]any{
			"namespaces": estNSJSON, "ranking": "similarity", "query": "alpha",
			"payload_mode": "summary", "limit": float64(50), "similarity_min": floor,
		}
		if estimate {
			out["estimate_only"] = true
		}
		return out
	}

	actual := decodeEnvelope(t, estCall(t, a, "memory_recall", args(false, 0.5)))
	if actual.Manifest.ResultsReturned == 0 || actual.Manifest.BytesReturned == 0 {
		t.Fatalf("floored read is empty; the identity below would be vacuous: %+v", actual.Manifest)
	}
	est := decodeEnvelope(t, estCall(t, a, "memory_recall", args(true, 0.5)))
	if got, want := manifestKey(t, est.Manifest), manifestKey(t, actual.Manifest); got != want {
		t.Errorf("estimate of a floored read does not match the floored read:\n"+
			"estimate: %s\n    real: %s", got, want)
	}

	// The floor must have moved the numbers, or the composition is untested.
	unfloored := decodeEnvelope(t, estCall(t, a, "memory_recall", map[string]any{
		"namespaces": estNSJSON, "ranking": "similarity", "query": "alpha",
		"payload_mode": "summary", "limit": float64(50), "estimate_only": true,
	}))
	if unfloored.Manifest.ResultsReturned == est.Manifest.ResultsReturned {
		t.Errorf("the estimate reports %d rows with and without a floor of 0.5; "+
			"estimate_only is not seeing similarity_min",
			est.Manifest.ResultsReturned)
	}
}

// A non-numeric similarity_min must be refused rather than coerced. GetFloat
// would return 0 for the string "0.5" — silently installing a floor of zero
// where the caller asked for half, which is a wrong answer rather than an
// error.
func TestSimilarityMin_NonNumericIsRejected(t *testing.T) {
	a, _ := estimateSurfaces(t)
	raw := estCall(t, a, "memory_recall", map[string]any{
		"namespaces": estNSJSON, "ranking": "similarity", "query": "alpha",
		"similarity_min": "0.5",
	})
	if !strings.Contains(raw, "validation_error") {
		t.Errorf("a string similarity_min was accepted: %s", raw)
	}
}

// Both knobs must work across MEMORY AND KNOWLEDGE, not memory alone. That is
// the point of putting them on recall rather than leaving them as
// context-domain tools: context_estimate estimates over a context selector and
// has no memory or knowledge equivalent, and context_rag_query's similarity
// threshold reaches neither.
//
// The two domains share memory_revisions, so this is not a separate code path
// — but "it should work" and "it does work" are different claims, and only one
// of them is checkable.
func TestNewKnobs_SpanMemoryAndKnowledge(t *testing.T) {
	a, _ := estimateSurfaces(t)

	base := map[string]any{
		"namespaces": estBothJSON, "ranking": "relevance", "search_mode": "semantic",
		"query": "alpha", "payload_mode": "summary", "limit": float64(50),
	}
	with := func(extra map[string]any) map[string]any {
		out := map[string]any{}
		for k, v := range base {
			out[k] = v
		}
		for k, v := range extra {
			out[k] = v
		}
		return out
	}

	unfiltered := decodeEnvelope(t, estCall(t, a, "tesseract_lookup", with(nil)))
	if unfiltered.Facets == nil {
		t.Fatal("lookup returned no facets")
	}
	// Non-vacuity: BOTH domains must be present, or a knob that silently
	// dropped one of them would pass unnoticed.
	for _, d := range []string{"memory", "knowledge"} {
		if unfiltered.Facets.Domains[d] == 0 {
			t.Fatalf("domain %q contributed no rows (%v); this test cannot show the "+
				"knobs reach both domains", d, unfiltered.Facets.Domains)
		}
	}

	// estimate_only over both domains, identity per facet.
	est := decodeEnvelope(t, estCall(t, a, "tesseract_lookup",
		with(map[string]any{"estimate_only": true})))
	if est.Facets == nil {
		t.Fatal("cross-domain estimate returned no facets")
	}
	assertFacetIdentity(t, "cross-domain", *est.Facets, *unfiltered.Facets)
	if got, want := manifestKey(t, est.Manifest), manifestKey(t, unfiltered.Manifest); got != want {
		t.Errorf("cross-domain estimate is not the identity of the real read:\n"+
			"estimate: %s\n    real: %s", got, want)
	}

	// similarity_min over both domains: the floor must bind on rows from each.
	scored := decodeScored(t, estCall(t, a, "tesseract_lookup", with(nil)))
	if len(scored) < 3 {
		t.Fatalf("cross-domain lookup returned %d rows; too few to floor", len(scored))
	}
	floor := (scored[0].Score + scored[len(scored)-1].Score) / 2
	filtered := decodeEnvelope(t, estCall(t, a, "tesseract_lookup",
		with(map[string]any{"similarity_min": floor})))
	if filtered.Manifest.ResultsTotal >= unfiltered.Manifest.ResultsTotal {
		t.Errorf("similarity_min=%v did not narrow the cross-domain set: %d vs %d",
			floor, filtered.Manifest.ResultsTotal, unfiltered.Manifest.ResultsTotal)
	}
	if filtered.Manifest.ResultsTotal == 0 {
		t.Errorf("similarity_min=%v removed everything; the floor is not discriminating", floor)
	}
}

// ── AC3: the descriptions must be accurate ───────────────────────────────────

// Every error code these two argument descriptions name must be emitted by a
// path a caller can actually reach.
//
// CW-20260825-0006 shipped a description advertising `service_unavailable`
// where the path emits `similarity_unavailable`. An agent branching on error
// codes gets one that never appears, and no test noticed, because a
// description was treated as prose rather than as a claim about behavior.
//
// The guard works by extraction rather than by a hand-kept list: it pulls every
// code-shaped token out of the description text and requires each to have a
// PROVOKER below that produces it from a real call. Naming a new code in a
// description without proving it reachable fails here — which is the direction
// the failure needs to run, since prose is the easy thing to edit.
func TestNewKnobDescriptions_NameOnlyReachableErrorCodes(t *testing.T) {
	a, _ := estimateSurfaces(t)

	// provokers maps an error code to a call that must produce it.
	provokers := map[string]map[string]any{
		"validation_error": {
			"namespaces": estNSJSON, "ranking": "similarity", "query": "alpha",
			"similarity_min": 9.0, // outside [-1, 1]
		},
	}

	for _, d := range []struct{ name, text string }{
		{"similarityMinArgDescription", similarityMinArgDescription},
		{"estimateOnlyArgDescription", estimateOnlyArgDescription},
	} {
		for _, code := range codeTokens(d.text) {
			args, ok := provokers[code]
			if !ok {
				t.Errorf("%s names the error code %q, but no call in this test produces it. "+
					"Either the description is advertising a code this path never emits — the "+
					"CW-20260825-0006 defect — or the provoker is missing. Do not delete this "+
					"failure by deleting the code from the map.", d.name, code)
				continue
			}
			raw := estCall(t, a, "memory_recall", args)
			if !strings.Contains(raw, `"`+code+`"`) {
				t.Errorf("%s names %q but the provoking call returned: %s", d.name, code, raw)
			}
		}
	}

	// Non-vacuity: the extractor must actually find something, or every
	// description passes by returning an empty list.
	if len(codeTokens(similarityMinArgDescription)) == 0 {
		t.Fatal("no error codes extracted from similarityMinArgDescription; " +
			"the extractor is not looking at what it claims to look at")
	}
	// Positive control for the extractor: it finds a code in text that has one.
	if got := codeTokens("this path returns similarity_unavailable when unset"); len(got) != 1 ||
		got[0] != "similarity_unavailable" {
		t.Errorf("extractor positive control failed: got %v, want [similarity_unavailable]", got)
	}
	// Negative control: prose with no code yields nothing.
	if got := codeTokens("no codes are mentioned in this sentence at all"); len(got) != 0 {
		t.Errorf("extractor negative control failed: got %v, want none", got)
	}
}

// codeTokens pulls tesseract error codes out of prose. Codes in this repo are
// lower_snake_case ending in _error or _unavailable.
func codeTokens(text string) []string {
	var out []string
	seen := map[string]bool{}
	for _, field := range strings.FieldsFunc(text, func(r rune) bool {
		return r != '_' && (r < 'a' || r > 'z')
	}) {
		if !strings.HasSuffix(field, "_error") && !strings.HasSuffix(field, "_unavailable") {
			continue
		}
		if seen[field] {
			continue
		}
		seen[field] = true
		out = append(out, field)
	}
	return out
}

// Every capability the descriptions claim must be exercised somewhere. These
// are the load-bearing sentences an agent will act on, restated as assertions
// so that editing one without editing the code fails.
func TestNewKnobDescriptions_ClaimsMatchBehavior(t *testing.T) {
	for _, c := range []struct{ name, text, must string }{
		// The floor's range, stated in the description and enforced by
		// RecallPage — TestSimilarityMin_RangeRejectedOnBothDoors.
		{"similarity_min states its range", similarityMinArgDescription, "[-1, 1]"},
		// The 0.0-is-not-absent rule — TestSimilarityMin_ZeroIsAFloorNotAnAbsence.
		{"similarity_min distinguishes 0.0 from omitted", similarityMinArgDescription, "0.0 is NOT the same as omitting it"},
		// The boundary is inclusive, and stating it wrongly is not a wording
		// nit: a first draft of this description said a floor of 0.0 drops
		// orthogonal results, which is what a `>` floor would do. The code
		// implements `>=`, so the prose described a tool that does not exist.
		{"similarity_min states the boundary is inclusive", similarityMinArgDescription, "The floor is inclusive"},
		{"similarity_min says 0.0 drops the negatives", similarityMinArgDescription, "drops every result with a NEGATIVE score"},
		// The applicability rule — TestSimilarityMin_RefusedWhereThereIsNoSimilarity.
		{"similarity_min names ranking=similarity", similarityMinArgDescription, "`ranking=similarity`"},
		{"similarity_min names search_mode=semantic", similarityMinArgDescription, "`search_mode=semantic`"},
		{"similarity_min warns about the hybrid default", similarityMinArgDescription, "`search_mode=hybrid`"},
		// The confidence_min contrast, which is why this is a second knob.
		{"similarity_min distinguishes itself from confidence_min", similarityMinArgDescription, "`confidence_min`"},
		// estimate_only's central claim — TestEstimateOnly_ExactIdentityPerPayloadMode.
		{"estimate_only claims exactness", estimateOnlyArgDescription, "exact, not approximate"},
		{"estimate_only names the absent results key", estimateOnlyArgDescription, "no `results` key"},
		// The cursor claim — TestEstimateOnly_CursorResumesTheRealRead.
		{"estimate_only claims its cursor is usable", estimateOnlyArgDescription, "valid cursor for the real read"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if !strings.Contains(c.text, c.must) {
				t.Errorf("the description no longer contains %q.\n"+
					"If the behavior changed, change the test and the code together; "+
					"if only the wording changed, restore the claim — a test named for a "+
					"capability that the description no longer states is coverage theater.\n"+
					"description: %s", c.must, c.text)
			}
		})
	}
}

// ── Decoding helpers ─────────────────────────────────────────────────────────

type scoredRow struct {
	RevisionID string
	Score      float64
}

// decodeScored pulls (revision_id, score) out of a recall response under any
// projection. Both ProjectedResult and RecallResult nest identity under
// `revision` and carry `score` beside it, so one shape reads both.
func decodeScored(t *testing.T, raw string) []scoredRow {
	t.Helper()
	var env struct {
		Results []struct {
			Revision struct {
				RevisionID string `json:"revision_id"`
			} `json:"revision"`
			Score *float64 `json:"score"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(raw), &env); err != nil {
		t.Fatalf("decode scored results: %v; raw=%s", err, raw)
	}
	out := make([]scoredRow, 0, len(env.Results))
	for _, r := range env.Results {
		var s float64
		if r.Score != nil {
			s = *r.Score
		}
		out = append(out, scoredRow{RevisionID: r.Revision.RevisionID, Score: s})
	}
	return out
}

// ftoa renders a float for a JSON body without pulling strconv into the
// body-building above, matching itoa's role in the budget/cursor parity file.
func ftoa(f float64) string {
	raw, err := json.Marshal(f)
	if err != nil {
		return "0"
	}
	return string(raw)
}
