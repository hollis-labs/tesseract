package contextapi

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	"github.com/hollis-labs/tesseract/internal/memory"
)

func TestStatusHTTPFiltersRetryAndCursor(t *testing.T) {
	srv := newEventTestServer(t)
	body := `{"namespace":"project/status-http/event/status","summary":"done","author":{"agent_id":"dispatcher"},"session_id":"s","consumer_state":{"state":"done","subject_urn":"msg://agent/test/a"},"idempotency_key":"retry-1"}`
	decode := func(raw []byte) memory.Revision {
		var r memory.Revision
		if err := json.Unmarshal(raw, &r); err != nil {
			t.Fatal(err)
		}
		return r
	}
	first := postEvent(t, srv, body)
	if first.Code != 200 {
		t.Fatal(first.Body.String())
	}
	a := decode(first.Body.Bytes())
	retry := postEvent(t, srv, body)
	if retry.Code != 200 || decode(retry.Body.Bytes()).RevisionID != a.RevisionID {
		t.Fatalf("retry: %d %s", retry.Code, retry.Body.String())
	}
	for _, author := range []string{"dispatcher", "other", "other"} {
		request := map[string]any{"namespace": "project/status-http/event/status", "summary": "done", "author": map[string]any{"agent_id": author}, "session_id": "s", "consumer_state": map[string]any{"state": "done", "subject_urn": "msg://agent/test/a"}}
		raw, _ := json.Marshal(request)
		if rr := postEvent(t, srv, string(raw)); rr.Code != 200 {
			t.Fatal(rr.Body.String())
		}
	}
	q := url.Values{"namespace": {"project/status-http/event/status"}, "author_agent_id": {"dispatcher"}, "state_filters": {`[{"field":"state","values":["done"]}]`}, "limit": {"1"}, "payload_mode": {"full"}}
	rr := getEventLog(t, srv, q)
	if rr.Code != 200 {
		t.Fatal(rr.Body.String())
	}
	var page memory.EventLogPage
	if err := json.Unmarshal(rr.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Entries) != 1 || page.Entries[0].Author.AgentID != "dispatcher" || page.Manifest.NextCursor == nil {
		t.Fatalf("filter before LIMIT: %s", rr.Body.String())
	}
	q.Set("cursor", *page.Manifest.NextCursor)
	for _, field := range []string{"author_agent_id", "state_filters"} {
		old := q.Get(field)
		if field == "state_filters" {
			q.Set(field, `[{"field":"state","values":["blocked"]}]`)
		} else {
			q.Set(field, "other")
		}
		if got := getEventLog(t, srv, q); got.Code != http.StatusBadRequest {
			t.Fatalf("changed %s accepted cursor: %s", field, got.Body.String())
		}
		q.Set(field, old)
	}
	q.Del("cursor")
	q.Set("state_filters", `[{"field":"bad.path","values":["x"]}]`)
	if rr := getEventLog(t, srv, q); rr.Code != 400 {
		t.Fatal("invalid state field accepted")
	}
	q.Set("state_filters", "not-json")
	if rr := getEventLog(t, srv, q); rr.Code != 400 {
		t.Fatal("malformed state_filters accepted")
	}
}

func TestStatusHTTPRefusesWriterPrincipalInput(t *testing.T) {
	srv := newEventTestServer(t)
	rr := postEvent(t, srv, `{"namespace":"project/status-http/event/status","summary":"done","author":{"agent_id":"dispatcher"},"session_id":"s","consumer_state":{"state":"done","subject_urn":"msg://agent/test/a"},"writer_principal":"msg://service/dispatcher"}`)
	if rr.Code != 400 {
		t.Fatalf("caller-set principal accepted: %d %s", rr.Code, rr.Body.String())
	}
}
