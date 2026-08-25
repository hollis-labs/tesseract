package contextapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/tesseract/internal/knowledge"
	"github.com/hollis-labs/tesseract/internal/memory"
)

// CW-20260825-0015. The HTTP peer of the MCP tesseract_lookup pointer_health
// argument. Same vocabulary, same semantics — these tests exist so the two
// surfaces cannot drift into accepting different things.

func seedPointerHealthCorpus(t *testing.T, srv *Server) (deadID, liveID string) {
	t.Helper()
	ctx := context.Background()
	write := func(key, locator string) memory.Revision {
		rev, err := srv.KnowledgeStore.Write(ctx, knowledge.WriteInput{
			Namespace: "user/chrispian/knowledge/ph",
			Key:       key,
			Kind:      "note",
			Source:    "manual",
			Pointer:   memory.Pointer{Scheme: "file", Locator: locator},
			Summary:   "http pointer health probe " + key,
			Author:    memory.Author{AgentID: "test", AgentVersion: "1.0"},
			SessionID: "sess:ph",
		})
		if err != nil {
			t.Fatalf("seed %s: %v", key, err)
		}
		return rev
	}
	dead := write("hph.dead", "/tmp/hph-dead")
	live := write("hph.live", "/tmp/hph-live")

	now := time.Now().UTC()
	if _, err := memory.ApplyVerificationPlan(ctx, srv.MemoryStore.DB(), memory.VerificationPlan{
		Rows: []memory.PointerObservation{
			{RevisionID: dead.RevisionID, Scheme: "file", Locator: "/tmp/hph-dead",
				Outcome: memory.OutcomeUnresolvable, Detail: "not_found", CheckedAt: now},
			{RevisionID: live.RevisionID, Scheme: "file", Locator: "/tmp/hph-live",
				Outcome: memory.OutcomeResolved, Detail: "stat_ok", CheckedAt: now},
		},
	}); err != nil {
		t.Fatalf("apply observations: %v", err)
	}
	return dead.RevisionID, live.RevisionID
}

func TestTesseractLookup_PointerHealthFilter(t *testing.T) {
	srv := newLookupServer(t)
	deadID, liveID := seedPointerHealthCorpus(t, srv)

	rr := postLookup(t, srv, `{
		"namespaces":["user/chrispian/knowledge/ph"],
		"ranking":"chronological",
		"pointer_health":["unresolvable"]
	}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", rr.Code, rr.Body.String())
	}
	resp := decodeLookup(t, rr.Body.Bytes())
	if len(resp.Results) != 1 {
		t.Fatalf("want 1 result, got %d: %s", len(resp.Results), rr.Body.String())
	}
	if resp.Results[0].Revision.RevisionID != deadID {
		t.Errorf("got revision %s, want the dead one %s", resp.Results[0].Revision.RevisionID, deadID)
	}
	if strings.Contains(rr.Body.String(), liveID) {
		t.Error("the resolved revision came back from an unresolvable filter")
	}
}

func TestTesseractLookup_PointerHealthOnResults(t *testing.T) {
	srv := newLookupServer(t)
	seedPointerHealthCorpus(t, srv)

	rr := postLookup(t, srv, `{
		"namespaces":["user/chrispian/knowledge/ph"],
		"ranking":"chronological"
	}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", rr.Code, rr.Body.String())
	}

	var doc struct {
		Results []struct {
			PointerHealth *struct {
				Status    string     `json:"status"`
				Detail    string     `json:"detail"`
				CheckedAt *time.Time `json:"checked_at"`
			} `json:"pointer_health"`
		} `json:"results"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode: %v", err)
	}
	seen := map[string]bool{}
	for _, r := range doc.Results {
		if r.PointerHealth == nil {
			t.Fatalf("a knowledge result carries no pointer_health under the default projection: %s", rr.Body.String())
		}
		seen[r.PointerHealth.Status] = true
		if r.PointerHealth.CheckedAt == nil {
			t.Error("a recorded observation surfaced without checked_at")
		}
	}
	if !seen[string(memory.PointerHealthUnresolvable)] || !seen[string(memory.PointerHealthResolved)] {
		t.Errorf("results do not distinguish resolved from unresolvable: %v", seen)
	}
}

// TestTesseractLookup_InvalidPointerHealthIsValidationError: an unknown status
// must be a 400, not an empty 200. A silently-empty result set from a typo is
// indistinguishable from a corpus with no dead pointers.
func TestTesseractLookup_InvalidPointerHealthIsValidationError(t *testing.T) {
	srv := newLookupServer(t)
	seedPointerHealthCorpus(t, srv)

	rr := postLookup(t, srv, `{
		"namespaces":["user/chrispian/knowledge/ph"],
		"pointer_health":["dead"]
	}`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "validation_error") {
		t.Errorf("body does not carry validation_error: %s", rr.Body.String())
	}
	for _, status := range memory.PointerHealthStatusVocabulary() {
		if !strings.Contains(rr.Body.String(), status) {
			t.Errorf("error does not name allowed status %q: %s", status, rr.Body.String())
		}
	}
}
