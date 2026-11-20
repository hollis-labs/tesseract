package contextapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hollis-labs/tesseract/internal/contextstore"
	"github.com/hollis-labs/tesseract/internal/memory"
)

func TestStatusLegacyHTTPCredentialProvenanceAndRetryIsolation(t *testing.T) {
	srv := newEventTestServer(t)
	ctx := context.Background()
	create := func() (string, string) {
		// Same labels/client IDs must never merge authenticated identities.
		token, _, err := srv.Store.CreateAuthToken(ctx, contextstore.TokenCreateInput{Label: "trusted-dispatcher", ClientID: "trusted-dispatcher", Scopes: []string{"memory:write", "memory:read", "write"}, NamespaceGlobs: []string{"project/status-legacy/event/*"}})
		if err != nil {
			t.Fatal(err)
		}
		claims, err := srv.Store.ValidateAuthTokenWithClaims(ctx, token)
		if err != nil {
			t.Fatal(err)
		}
		return token, "tesseract-token/" + claims.TokenID
	}
	a, principalA := create()
	b, principalB := create()
	body := `{"namespace":"project/status-legacy/event/status","summary":"done","author":{"agent_id":"trusted-dispatcher"},"session_id":"s","idempotency_key":"same-retry-key","consumer_state":{"state":"done","subject_urn":"msg://agent/test/a"}}`
	seen := map[string]bool{}
	for _, tc := range []struct {
		name, token, principal string
		managed                bool
	}{
		{name: "anonymous"},
		{name: "static", token: "test-static-status-credential", principal: "static-token"},
		{name: "managed-a", token: a, principal: principalA, managed: true},
		{name: "managed-b", token: b, principal: principalB, managed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv.ManagedAuth = tc.managed
			srv.AuthToken = ""
			if tc.name == "static" {
				srv.AuthToken = tc.token
			}
			post := func(token string) *httptest.ResponseRecorder {
				req := httptest.NewRequest(http.MethodPost, "/v1/event/write", bytes.NewBufferString(body))
				req.Header.Set("Content-Type", "application/json")
				if token != "" {
					req.Header.Set("Authorization", "Bearer "+token)
				}
				rr := httptest.NewRecorder()
				srv.ServeHTTP(rr, req)
				return rr
			}
			var first memory.Revision
			for attempt := 0; attempt < 2; attempt++ {
				rr := post(tc.token)
				if rr.Code != 200 {
					t.Fatalf("write: %d %s", rr.Code, rr.Body.String())
				}
				var rev memory.Revision
				if err := json.Unmarshal(rr.Body.Bytes(), &rev); err != nil {
					t.Fatal(err)
				}
				if rev.WriterPrincipal != tc.principal {
					t.Fatalf("writer_principal=%q want credential %q", rev.WriterPrincipal, tc.principal)
				}
				if attempt == 0 {
					first = rev
					if seen[rev.RevisionID] {
						t.Fatal("distinct credentials shared an anonymous retry receipt")
					}
					seen[rev.RevisionID] = true
				} else if rev.RevisionID != first.RevisionID {
					t.Fatal("same credential retry appended")
				}
			}
			stored, err := srv.MemoryStore.GetRevisionByID(ctx, first.RevisionID)
			if err != nil || stored.WriterPrincipal != tc.principal {
				t.Fatalf("stored receipt: %+v %v", stored, err)
			}
			if tc.name != "anonymous" {
				if rr := post("invalid-test-credential"); rr.Code != 401 {
					t.Fatalf("invalid credential admitted: %d", rr.Code)
				}
			}
		})
	}
	if len(seen) != 4 {
		t.Fatalf("credential isolation: %d entries, want 4", len(seen))
	}
}
