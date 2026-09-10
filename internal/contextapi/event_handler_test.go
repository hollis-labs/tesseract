package contextapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/hollis-labs/tesseract/internal/event"
	"github.com/hollis-labs/tesseract/internal/memory"
)

func newEventTestServer(t *testing.T) *Server {
	t.Helper()
	srv := newMemoryTestServer(t)
	srv.EventStore = event.New(srv.MemoryStore)
	return srv
}

func postEvent(t *testing.T, srv *Server, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/event/write", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	return rr
}

func getEventLog(t *testing.T, srv *Server, q url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/v1/event/log?"+q.Encode(), nil)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	return rr
}

func TestEventWrite_Success(t *testing.T) {
	srv := newEventTestServer(t)

	rr := postEvent(t, srv, `{
		"namespace":"user/chrispian/event/journal",
		"summary":"settled the namespace grammar",
		"body":"went with memory's shape rather than knowledge's deep hierarchy",
		"author":{"agent_id":"chrispian","agent_version":""},
		"session_id":"manual:01HX",
		"tags":["tesseract","design"]
	}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var rev memory.Revision
	if err := json.Unmarshal(rr.Body.Bytes(), &rev); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if rev.Domain != "event" {
		t.Errorf("domain = %q, want event", rev.Domain)
	}
	if rev.MemoryKey != "" {
		t.Errorf("memory_key = %q; a keyless write is the norm for this domain", rev.MemoryKey)
	}
	if rev.Status != memory.StatusCanonical {
		t.Errorf("status = %q, want canonical", rev.Status)
	}
	if rev.Payload.Body == "" {
		t.Error("the body was dropped; it is the field this domain exists for")
	}
}

// TestEventWrite_RejectsAFlatAuthor covers the strict-decoding contract this
// surface has. An MCP-shaped body used to decode "successfully" into a
// zero-valued struct and fail later with a message naming none of the fields
// the caller sent.
func TestEventWrite_RejectsAFlatAuthor(t *testing.T) {
	srv := newEventTestServer(t)

	rr := postEvent(t, srv, `{
		"namespace":"user/chrispian/event/journal",
		"summary":"flat body",
		"author_agent_id":"chrispian",
		"session_id":"manual:01HX"
	}`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
	if !bytes.Contains(rr.Body.Bytes(), []byte("author_agent_id")) {
		t.Errorf("the rejection does not name the offending field: %s", rr.Body.String())
	}
}

func TestEventWrite_RejectsANonEventNamespace(t *testing.T) {
	srv := newEventTestServer(t)

	rr := postEvent(t, srv, `{
		"namespace":"user/chrispian/memory/notes",
		"summary":"wrong door",
		"author":{"agent_id":"chrispian"},
		"session_id":"manual:01HX"
	}`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
}

func TestEventLog_ReadsBackInOrderAndPages(t *testing.T) {
	srv := newEventTestServer(t)

	for _, s := range []string{"one", "two", "three"} {
		rr := postEvent(t, srv, `{
			"namespace":"user/chrispian/event/reasoning",
			"summary":"`+s+`",
			"author":{"agent_id":"claude-code"},
			"session_id":"sess-1"
		}`)
		if rr.Code != http.StatusOK {
			t.Fatalf("seed %q: status %d body=%s", s, rr.Code, rr.Body.String())
		}
	}

	type page struct {
		Entries  []memory.ProjectedRevision `json:"entries"`
		Manifest struct {
			Returned    int     `json:"returned"`
			Direction   string  `json:"direction"`
			HasMore     bool    `json:"has_more"`
			NextCursor  *string `json:"next_cursor"`
			PayloadMode string  `json:"payload_mode"`
		} `json:"manifest"`
	}

	q := url.Values{}
	q.Set("namespace", "user/chrispian/event/reasoning")
	q.Set("limit", "2")
	rr := getEventLog(t, srv, q)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var first page
	if err := json.Unmarshal(rr.Body.Bytes(), &first); err != nil {
		t.Fatalf("decode: %v (body=%s)", err, rr.Body.String())
	}
	if first.Manifest.Returned != 2 || !first.Manifest.HasMore || first.Manifest.NextCursor == nil {
		t.Fatalf("first page manifest = %+v, want 2 returned with more to come", first.Manifest)
	}
	if first.Entries[0].Payload == nil || first.Entries[0].Payload.Summary != "three" {
		t.Errorf("first entry = %+v, want the newest (\"three\")", first.Entries[0])
	}
	// The default projection withholds bodies and says so.
	if first.Manifest.PayloadMode != string(memory.DefaultPayloadMode) {
		t.Errorf("payload_mode = %q, want the default %q", first.Manifest.PayloadMode, memory.DefaultPayloadMode)
	}

	q.Set("cursor", *first.Manifest.NextCursor)
	rr = getEventLog(t, srv, q)
	if rr.Code != http.StatusOK {
		t.Fatalf("second page: status %d body=%s", rr.Code, rr.Body.String())
	}
	var second page
	if err := json.Unmarshal(rr.Body.Bytes(), &second); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if second.Manifest.Returned != 1 || second.Manifest.HasMore {
		t.Errorf("second page manifest = %+v, want the last entry", second.Manifest)
	}
	if second.Entries[0].Payload.Summary != "one" {
		t.Errorf("last entry = %q, want the oldest", second.Entries[0].Payload.Summary)
	}
}

// TestEventLog_RepeatableNamespaceParam: `namespace` repeats rather than being
// comma-separated, because a namespace is a path.
func TestEventLog_RepeatableNamespaceParam(t *testing.T) {
	srv := newEventTestServer(t)

	for _, ns := range []string{"user/chrispian/event/reasoning", "user/chrispian/event/journal"} {
		rr := postEvent(t, srv, `{
			"namespace":"`+ns+`",
			"summary":"in `+ns+`",
			"author":{"agent_id":"claude-code"},
			"session_id":"sess-1"
		}`)
		if rr.Code != http.StatusOK {
			t.Fatalf("seed %s: %s", ns, rr.Body.String())
		}
	}

	q := url.Values{}
	q.Add("namespace", "user/chrispian/event/reasoning")
	q.Add("namespace", "user/chrispian/event/journal")
	rr := getEventLog(t, srv, q)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", rr.Code, rr.Body.String())
	}
	var got struct {
		Manifest struct {
			Returned int `json:"returned"`
		} `json:"manifest"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Manifest.Returned != 2 {
		t.Errorf("returned = %d across two namespaces, want 2", got.Manifest.Returned)
	}
}

func TestEventLog_ValidatesItsQuery(t *testing.T) {
	srv := newEventTestServer(t)

	for _, tc := range []struct {
		name string
		q    url.Values
	}{
		{"no namespace", url.Values{}},
		{"empty namespace", url.Values{"namespace": {"  "}}},
		{"bad direction", url.Values{"namespace": {"user/chrispian/event/journal"}, "direction": {"sideways"}}},
		{"bad limit", url.Values{"namespace": {"user/chrispian/event/journal"}, "limit": {"lots"}}},
		{"bad since", url.Values{"namespace": {"user/chrispian/event/journal"}, "since": {"yesterday"}}},
		{"bad payload_mode", url.Values{"namespace": {"user/chrispian/event/journal"}, "payload_mode": {"everything"}}},
		{"garbage cursor", url.Values{"namespace": {"user/chrispian/event/journal"}, "cursor": {"nonsense"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rr := getEventLog(t, srv, tc.q)
			if rr.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400; body=%s", rr.Code, rr.Body.String())
			}
		})
	}
}

// TestEventRoutes_UnavailableWithoutAStore: a deployment that wired no event
// store says so rather than answering an empty log, which is the same
// distinction domain_unavailable draws on the MCP side.
func TestEventRoutes_UnavailableWithoutAStore(t *testing.T) {
	srv := newMemoryTestServer(t) // no EventStore

	rr := postEvent(t, srv, `{"namespace":"user/chrispian/event/journal","summary":"x",
		"author":{"agent_id":"a"},"session_id":"s"}`)
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("write status = %d, want 503; body=%s", rr.Code, rr.Body.String())
	}

	rr = getEventLog(t, srv, url.Values{"namespace": {"user/chrispian/event/journal"}})
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("log status = %d, want 503; body=%s", rr.Code, rr.Body.String())
	}
}
