package contextapi

import (
	"crypto/rand"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"
)

func notFoundGet(t *testing.T, srv *Server, path string) (int, map[string]any) {
	t.Helper()
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, path, nil))
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %s response %q: %v", path, rr.Body.String(), err)
	}
	return rr.Code, body
}

// The HTTP peers of tesseract_get, tesseract_history and tesseract_get_revision
// fail identically for an ID that was never minted, so they carry the same
// structured details the MCP tools do. The message text is unchanged.
func TestNotFound_ForAnUnmintedULIDCarriesItsMintTimeOverHTTP(t *testing.T) {
	srv := newMemoryTestServer(t)
	minted := time.Now().Add(-36*time.Hour - 36*time.Minute)
	id := ulid.MustNew(ulid.Timestamp(minted), rand.Reader).String()

	for _, tc := range []struct {
		name, path, field, message string
	}{
		{"GET /v1/items/{id}", "/v1/items/" + id, "item_id", "item_id not found: " + id},
		{"GET /v1/items/{id}/history", "/v1/items/" + id + "/history", "item_id", "item_id not found: " + id},
		{"GET /v1/memory/revisions/{id}", "/v1/memory/revisions/" + id, "revision_id", "memory not found: revision_id " + id},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status, body := notFoundGet(t, srv, tc.path)
			if status != http.StatusNotFound || body["code"] != "not_found" {
				t.Fatalf("status=%d code=%v, want 404 not_found; body=%v", status, body["code"], body)
			}
			if body["message"] != tc.message {
				t.Errorf("message = %q, want %q: the message text must not change", body["message"], tc.message)
			}
			details, ok := body["details"].(map[string]any)
			if !ok {
				t.Fatalf("no details object for a ULID-shaped ID: %v", body)
			}
			if details["field"] != tc.field || details["id"] != id {
				t.Errorf("details field=%v id=%v, want %s and %s", details["field"], details["id"], tc.field, id)
			}
			if want := minted.UTC().Format("2006-01-02T15:04:05.000Z"); details["minted_at"] != want {
				t.Errorf("minted_at = %v, want %s", details["minted_at"], want)
			}
			age, _ := details["age_seconds"].(float64)
			if want := (36*time.Hour + 36*time.Minute).Seconds(); math.Abs(age-want) > 120 {
				t.Errorf("age_seconds = %v, want about %v", age, want)
			}
		})
	}
}

// An ID with nothing to decode answers exactly as before: details stays null.
func TestNotFound_ForAnIDThatIsNotAULIDIsUnchangedOverHTTP(t *testing.T) {
	srv := newMemoryTestServer(t)
	for _, path := range []string{"/v1/items/not-a-ulid", "/v1/memory/revisions/not-a-ulid"} {
		status, body := notFoundGet(t, srv, path)
		if status != http.StatusNotFound || body["code"] != "not_found" {
			t.Errorf("%s: status=%d code=%v, want 404 not_found", path, status, body["code"])
		}
		if body["details"] != nil {
			t.Errorf("%s: details = %v, want null for an ID with nothing to decode", path, body["details"])
		}
	}
}
