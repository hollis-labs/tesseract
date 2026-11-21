package contextapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	auth "github.com/hollis-labs/libs/security/auth"
	"github.com/hollis-labs/tesseract/internal/event"
	"github.com/hollis-labs/tesseract/internal/memory"
)

func TestEventStreamHTTPAcrossProjects(t *testing.T) {
	srv := newEventTestServer(t)
	ctx := auth.WithPrincipal(context.Background(), auth.Principal{Verified: true, ID: "msg://service/test/dispatcher"})
	ws := "ws"
	for _, ns := range []string{"project/alpha/event/status", "project/beta/event/status", "project/beta/event/reasoning"} {
		_, err := srv.EventStore.Write(ctx, event.WriteInput{Namespace: ns, WorkstreamID: &ws, Summary: "done", Author: memory.Author{AgentID: "dispatcher"}, SessionID: "s", ConsumerState: json.RawMessage(`{"state":"done","subject_urn":"msg://agent/test/a","task_id":"T-1","run_id":"R-1"}`)})
		if err != nil {
			t.Fatal(err)
		}
	}
	q := url.Values{"namespace": {"project/"}, "stream": {"status"}, "workstream_id": {"ws"}, "state_filters": {`[{"field":"task_id","values":["T-1"]},{"field":"run_id","values":["R-1"]}]`}, "limit": {"1"}}
	seen := map[string]bool{}
	for n := 0; n < 2; n++ {
		rr := getEventLog(t, srv, q)
		if rr.Code != 200 {
			t.Fatal(rr.Body.String())
		}
		var page memory.EventLogPage
		if err := json.Unmarshal(rr.Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
		if len(page.Entries) != 1 {
			t.Fatal(rr.Body.String())
		}
		rev := page.Entries[0]
		if seen[rev.Namespace] || rev.WriterPrincipal != "msg://service/test/dispatcher" {
			t.Fatal(rr.Body.String())
		}
		seen[rev.Namespace] = true
		if n == 0 {
			if page.Manifest.NextCursor == nil {
				t.Fatal("missing cursor")
			}
			q.Set("cursor", *page.Manifest.NextCursor)
		}
	}
	q.Set("stream", "reasoning")
	if rr := getEventLog(t, srv, q); rr.Code != 400 {
		t.Fatalf("changed stream cursor accepted: %s", rr.Body.String())
	}
	q.Del("cursor")
	q.Set("stream", "bogus")
	if rr := getEventLog(t, srv, q); rr.Code != 400 {
		t.Fatal("invalid stream accepted")
	}
	// A single-project credential cannot read a cross-project prefix, even with
	// stream narrowing; all selectors must be authorized before reading.
	srv.ManagedAuth = true
	token := issueTokenWithScopes(t, srv, "single-project", []string{"memory:read"}, []string{"project/alpha/*"})
	q.Set("stream", "status")
	for _, selector := range []string{"project/", "project/*"} {
		q.Set("namespace", selector)
		req := httptest.NewRequest(http.MethodGet, "/v1/event/log?"+q.Encode(), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rr := httptest.NewRecorder()
		srv.ServeHTTP(rr, req)
		if rr.Code != 403 {
			t.Fatalf("broad selector %q: %d %s", selector, rr.Code, rr.Body.String())
		}
	}
}
