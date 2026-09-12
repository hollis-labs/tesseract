package contextapi

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/hollis-labs/tesseract/domains"

	"github.com/hollis-labs/tesseract/internal/memory"
)

// CW-20260825-0017. The HTTP peer of the MCP tesseract_recall related_to
// argument.
//
// These go over the wire rather than through the store because the store is
// already covered (internal/memory/links_test.go) and the thing that is NOT
// covered there is the JSON tag. A misspelled `json:"related_to"` decodes to
// the zero value, the filter silently does nothing, and the response is a
// perfectly well-formed page of the wrong rows — no error anywhere.

const relatedNS = "user/chrispian/memory/notes"

func seedLinkedCorpus(t *testing.T, srv *Server) {
	t.Helper()
	ctx := context.Background()
	write := func(key, body string) {
		t.Helper()
		if _, err := srv.MemoryStore.WriteRevision(ctx, memory.WriteInput{
			Domain:     domains.Memory,
			Namespace:  relatedNS,
			MemoryKey:  key,
			Author:     memory.Author{AgentID: "test", AgentVersion: "1.0"},
			Trigger:    memory.TriggerExplicit,
			SessionID:  "manual:01HX",
			Origin:     memory.OriginUser,
			Confidence: 0.9,
			Status:     memory.StatusCanonical,
			Payload:    memory.Payload{Summary: "seed " + key, Body: body},
		}); err != nil {
			t.Fatalf("seed %s: %v", key, err)
		}
	}
	write("http_anchor", "cites [[http_outbound]]")
	write("http_outbound", "a leaf")
	write("http_inbound", "refers to [[http_anchor]]")
	write("http_unrelated", "mentions nobody")
}

func lookupKeys(t *testing.T, resp lookupTestResponse) []string {
	t.Helper()
	keys := make([]string, 0, len(resp.Results))
	for _, r := range resp.Results {
		keys = append(keys, r.Revision.MemoryKey)
	}
	return keys
}

func containsKey(keys []string, want string) bool {
	for _, k := range keys {
		if k == want {
			return true
		}
	}
	return false
}

func TestTesseractLookup_RelatedToFilter(t *testing.T) {
	srv := newLookupServer(t)
	seedLinkedCorpus(t, srv)

	rr := postLookup(t, srv, `{
		"namespaces":["`+relatedNS+`"],
		"ranking":"chronological",
		"related_to":["http_anchor"]
	}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", rr.Code, rr.Body.String())
	}

	keys := lookupKeys(t, decodeLookup(t, rr.Body.Bytes()))
	if !containsKey(keys, "http_outbound") {
		t.Errorf("outbound neighbor missing over the wire: %v", keys)
	}
	if !containsKey(keys, "http_inbound") {
		t.Errorf("inbound neighbor missing over the wire: %v", keys)
	}
	if containsKey(keys, "http_unrelated") {
		t.Errorf("related_to did not narrow anything — check the JSON tag: %v", keys)
	}
}

func TestTesseractLookup_RelatedRelationsFilter(t *testing.T) {
	srv := newLookupServer(t)
	seedLinkedCorpus(t, srv)

	rr := postLookup(t, srv, `{
		"namespaces":["`+relatedNS+`"],
		"ranking":"chronological",
		"related_to":["http_anchor"],
		"related_relations":["references"]
	}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", rr.Code, rr.Body.String())
	}
	keys := lookupKeys(t, decodeLookup(t, rr.Body.Bytes()))
	if !containsKey(keys, "http_outbound") {
		t.Errorf("references relation dropped a citation: %v", keys)
	}
}

// An unknown relation must be a 400, not an empty page: an empty
// neighborhood from a typo reads exactly like an entry with no links.
func TestTesseractLookup_RelatedRelationsRejectsUnknownValue(t *testing.T) {
	srv := newLookupServer(t)
	seedLinkedCorpus(t, srv)

	rr := postLookup(t, srv, `{
		"namespaces":["`+relatedNS+`"],
		"ranking":"chronological",
		"related_to":["http_anchor"],
		"related_relations":["mentions"]
	}`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "references") {
		t.Errorf("error should render the vocabulary: %s", rr.Body.String())
	}
}

// A relation filter with no anchor names edges nobody asked to walk.
func TestTesseractLookup_RelatedRelationsWithoutAnchorRejected(t *testing.T) {
	srv := newLookupServer(t)
	seedLinkedCorpus(t, srv)

	rr := postLookup(t, srv, `{
		"namespaces":["`+relatedNS+`"],
		"ranking":"chronological",
		"related_relations":["references"]
	}`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", rr.Code, rr.Body.String())
	}
}
