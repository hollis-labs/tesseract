package contextapi

import (
	"fmt"
	"net/http"
	"testing"
)

const recallNS = "user/chrispian/memory/notes"

func recallMetaOf(t *testing.T, srv *Server, query string) map[string]any {
	t.Helper()
	rr := performJSON(t, srv, http.MethodGet, "/v1/recall?"+query, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /v1/recall?%s status=%d body=%s", query, rr.Code, rr.Body.String())
	}
	meta, ok := decodeHTTPJSON(t, rr.Body.Bytes())["meta"].(map[string]any)
	if !ok {
		t.Fatalf("no meta object in %s", rr.Body.String())
	}
	return meta
}

func seedRecallNotes(t *testing.T, srv *Server, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		seedMemoryWithTags(t, srv, recallNS, fmt.Sprintf("bulk.k%d", i), fmt.Sprintf("note %d", i), nil)
	}
}

// A result cut short by limit used to look exactly like a complete one: meta
// carried only what was returned. It now says how many matched and that the
// response is a window, and where to go for the rest.
func TestRecallMeta_DisclosesATruncatedWindow(t *testing.T) {
	srv := newRecallServer(t)
	seedRecallNotes(t, srv, 7)

	meta := recallMetaOf(t, srv, "namespace="+recallNS+"&limit=3")
	if meta["returned"] != float64(3) || meta["total"] != float64(7) {
		t.Errorf("returned=%v total=%v, want 3 of 7", meta["returned"], meta["total"])
	}
	if meta["truncated"] != true {
		t.Errorf("truncated = %v, want true for 3 of 7", meta["truncated"])
	}
	if meta["truncation_reason"] != "limit" {
		t.Errorf("truncation_reason = %v, want limit: raising the caller's limit would help", meta["truncation_reason"])
	}
	if meta["paged_route"] != "POST /v1/tesseract/lookup" {
		t.Errorf("paged_route = %v, want a pointer to the route that pages", meta["paged_route"])
	}
}

// `truncated: false` is the statement that the response is everything, so it
// must be present, not merely absent. The reason and route appear only when
// there is something to say.
func TestRecallMeta_SaysOutrightWhenNothingWasCut(t *testing.T) {
	srv := newRecallServer(t)
	seedRecallNotes(t, srv, 4)

	meta := recallMetaOf(t, srv, "namespace="+recallNS+"&limit=10")
	if v, present := meta["truncated"]; !present || v != false {
		t.Errorf("truncated = %v (present=%v), want an explicit false", v, present)
	}
	if meta["total"] != float64(4) || meta["returned"] != float64(4) {
		t.Errorf("total=%v returned=%v, want 4 and 4", meta["total"], meta["returned"])
	}
	for _, field := range []string{"truncation_reason", "paged_route"} {
		if _, present := meta[field]; present {
			t.Errorf("%s present on a response that was not cut short", field)
		}
	}

	// An empty result is a zero, stated, not an absence.
	empty := recallMetaOf(t, srv, "namespace=user/chrispian/memory/nothing-here")
	if v, present := empty["total"]; !present || v != float64(0) {
		t.Errorf("empty result total = %v (present=%v), want an explicit 0", v, present)
	}
}

// Additive means the four fields callers already read are untouched, because
// the mux boot profiles build their slot URLs on this route.
func TestRecallMeta_TheOriginalFieldsAreUnchanged(t *testing.T) {
	srv := newRecallServer(t)
	seedRecallNotes(t, srv, 5)

	meta := recallMetaOf(t, srv, "namespace="+recallNS+"&limit=3&format=full")
	if meta["namespace"] != recallNS || meta["limit"] != float64(3) || meta["returned"] != float64(3) || meta["format"] != "full" {
		t.Errorf("meta = %v, want namespace=%s limit=3 returned=3 format=full unchanged", meta, recallNS)
	}
	// The default is still 15.
	if def := recallMetaOf(t, srv, "namespace="+recallNS); def["limit"] != float64(15) {
		t.Errorf("default limit = %v, want 15", def["limit"])
	}
}

// The route's 500-row ceiling is preserved, and now disclosed rather than hit
// silently. Raising limit cannot help past it, so the reason is not `limit`.
func TestRecallMeta_TheLegacyCeilingIsDisclosedNotRaised(t *testing.T) {
	if testing.Short() {
		t.Skip("seeds 505 revisions")
	}
	srv := newRecallServer(t)
	seedRecallNotes(t, srv, 505)

	over := recallMetaOf(t, srv, "namespace="+recallNS+"&limit=1000&format=brief")
	if over["returned"] != float64(500) || over["total"] != float64(505) {
		t.Errorf("limit=1000: returned=%v total=%v, want the 500-row ceiling and 505 matched", over["returned"], over["total"])
	}
	if over["truncated"] != true || over["truncation_reason"] != "ceiling" {
		t.Errorf("limit=1000: truncated=%v reason=%v, want true and ceiling", over["truncated"], over["truncation_reason"])
	}

	// At the ceiling exactly, the caller's own limit is what cut it.
	at := recallMetaOf(t, srv, "namespace="+recallNS+"&limit=500&format=brief")
	if at["returned"] != float64(500) || at["truncated"] != true || at["truncation_reason"] != "limit" {
		t.Errorf("limit=500: returned=%v truncated=%v reason=%v, want 500, true and limit", at["returned"], at["truncated"], at["truncation_reason"])
	}
}

// This route has no cursor and ignores one. That is unchanged, and stated here
// so a later change to it is a decision rather than an accident: a caller who
// sends `cursor` gets the first page again, which is why meta now says the page
// was cut short.
func TestRecallMeta_ACursorIsStillIgnored(t *testing.T) {
	srv := newRecallServer(t)
	seedRecallNotes(t, srv, 6)

	plain := recallMetaOf(t, srv, "namespace="+recallNS+"&limit=2")
	withCursor := recallMetaOf(t, srv, "namespace="+recallNS+"&limit=2&cursor=anything")
	for _, field := range []string{"returned", "total", "truncated"} {
		if plain[field] != withCursor[field] {
			t.Errorf("%s changed when a cursor was sent: %v vs %v", field, plain[field], withCursor[field])
		}
	}
	if _, present := withCursor["next_cursor"]; present {
		t.Error("this route has no cursor to hand back, yet meta carries next_cursor")
	}
}

// The workspace path is served by the paged engine, which already computed all
// of this, so its meta reports the manifest's own answer.
func TestRecallMeta_TheWorkspacePathReportsItsManifest(t *testing.T) {
	srv := newWorkspaceTestServer(t)
	for i := 0; i < 3; i++ {
		rr := performJSON(t, srv, http.MethodPost, "/v1/workspace/write", map[string]any{
			"namespace": httpWorkspaceNS, "idempotency_key": fmt.Sprintf("recall-meta-%d", i),
			"summary": fmt.Sprintf("draft %d", i),
			"author":  map[string]any{"agent_id": "http-test"}, "session_id": "http-session",
		})
		if rr.Code != http.StatusOK {
			t.Fatalf("seed workspace item %d: status=%d body=%s", i, rr.Code, rr.Body.String())
		}
	}

	meta := recallMetaOf(t, srv, "namespace="+httpWorkspaceNS+"&domains=workspace&limit=2")
	if meta["returned"] != float64(2) || meta["total"] != float64(3) {
		t.Errorf("returned=%v total=%v, want 2 of 3", meta["returned"], meta["total"])
	}
	if meta["truncated"] != true || meta["paged_route"] != "POST /v1/tesseract/lookup" {
		t.Errorf("truncated=%v paged_route=%v, want true and the paged route", meta["truncated"], meta["paged_route"])
	}
	if reason, _ := meta["truncation_reason"].(string); reason == "" {
		t.Error("a truncated workspace response names no reason")
	}

	whole := recallMetaOf(t, srv, "namespace="+httpWorkspaceNS+"&domains=workspace&limit=10")
	if whole["truncated"] != false || whole["total"] != float64(3) {
		t.Errorf("limit=10: truncated=%v total=%v, want false and 3", whole["truncated"], whole["total"])
	}
}
