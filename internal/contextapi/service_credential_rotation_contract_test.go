package contextapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/tesseract/internal/contextstore"
)

// Legacy mutation routes must not let an unprivileged caller bypass the
// managed administrator boundary on the service credential issuer.
func TestServiceCredentialLegacyMutationsRequireManagedAdmin(t *testing.T) {
	for _, mode := range []string{"none", "static", "limited", "unknown"} {
		for _, action := range []string{"create", "revoke"} {
			t.Run(mode+"/"+action, func(t *testing.T) {
				srv := newTestServer(t)
				ctx := context.Background()
				serviceRaw, service, err := srv.Store.CreateAuthToken(ctx, contextstore.TokenCreateInput{Label: "service", ClientID: "tether-proxy", Scopes: []string{"write"}, NamespaceGlobs: []string{"project/example/*"}})
				if err != nil {
					t.Fatal("fixture service issuance failed")
				}
				headers := map[string]string{}
				callerRaw := ""
				switch mode {
				case "static":
					callerRaw = "isolated-static-credential"
					srv.AuthToken = callerRaw
				case "limited":
					srv.ManagedAuth = true
					callerRaw, _, err = srv.Store.CreateAuthToken(ctx, contextstore.TokenCreateInput{Label: "limited", ClientID: "ordinary-client", Scopes: []string{"write"}, NamespaceGlobs: []string{"*"}})
					if err != nil {
						t.Fatal("fixture caller issuance failed")
					}
				case "unknown":
					srv.ManagedAuth = true
					callerRaw = "isolated-unknown-credential"
				}
				if callerRaw != "" {
					headers["Authorization"] = "Bearer " + callerRaw
				}
				before, err := srv.Store.ListAuthTokens(ctx, 100)
				if err != nil {
					t.Fatal("fixture metadata read failed")
				}
				var body any = map[string]any{"name": "forbidden-admin", "client_id": "ordinary-client", "scopes": []string{"admin"}, "namespace_globs": []string{"*"}}
				if action == "revoke" {
					body = map[string]any{"id": service.TokenID}
				}
				res := performJSONWithHeaders(t, srv, http.MethodPost, "/v1/auth/tokens/"+action, body, headers)
				if res.Code < 400 || res.Code >= 500 {
					t.Errorf("unprivileged legacy mutation was not refused: HTTP %d", res.Code)
				}
				if strings.Contains(res.Body.String(), serviceRaw) || (callerRaw != "" && strings.Contains(res.Body.String(), callerRaw)) {
					t.Error("response disclosed a credential")
				}
				after, err := srv.Store.ListAuthTokens(ctx, 100)
				if err != nil {
					t.Fatal("result metadata read failed")
				}
				if !reflect.DeepEqual(rotationCredentialState(before), rotationCredentialState(after)) {
					t.Error("refused mutation changed credential metadata")
				}
				if err := srv.Store.ValidateAuthToken(ctx, serviceRaw); err != nil {
					t.Error("refused mutation invalidated the existing service credential")
				}
			})
		}
	}
}

// Authentication may update last-used metadata even on an authorization
// refusal. Compare credential eligibility and grants, not that observation.
func rotationCredentialState(tokens []contextstore.AuthToken) []contextstore.AuthToken {
	out := make([]contextstore.AuthToken, len(tokens))
	for i, token := range tokens {
		out[i] = contextstore.AuthToken{TokenID: token.TokenID, Label: token.Label, ClientID: token.ClientID, Scopes: token.Scopes, NamespaceGlobs: token.NamespaceGlobs, CreatedAt: token.CreatedAt, ExpiresAt: token.ExpiresAt, RevokedAt: token.RevokedAt}
	}
	return out
}

type rotationAPIResult struct {
	PrincipalID         string `json:"principal_id"`
	CredentialID        string `json:"credential_id"`
	Generation          int64  `json:"generation"`
	OperationGeneration int64  `json:"operation_generation"`
	Replayed            bool   `json:"replayed"`
	SecretAvailable     bool   `json:"secret_available"`
	Token               string `json:"token"`
}

func rotationAPIRequest(t *testing.T, srv *Server, admin, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatal("fixture request encoding failed")
	}
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(payload))
	req.RemoteAddr = "127.0.0.1:32123"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+admin)
	res := httptest.NewRecorder()
	srv.ServeHTTP(res, req)
	return res
}

func rotationAPIDecode(t *testing.T, res *httptest.ResponseRecorder) rotationAPIResult {
	t.Helper()
	if res.Code != http.StatusOK {
		t.Fatalf("credential request failed: HTTP %d", res.Code)
	}
	var result rotationAPIResult
	if json.Unmarshal(res.Body.Bytes(), &result) != nil {
		t.Fatal("credential response decoding failed")
	}
	return result
}

func TestServiceCredentialAPIOverlapClaimsRevokeAndReplay(t *testing.T) {
	for _, empty := range []bool{false, true} {
		t.Run(fmt.Sprintf("empty-grants-%t", empty), func(t *testing.T) {
			srv := newTestServer(t)
			srv.ManagedAuth = true
			ctx := context.Background()
			admin, _, err := srv.Store.CreateAuthToken(ctx, contextstore.TokenCreateInput{Label: "administrator", Scopes: []string{"admin"}})
			if err != nil {
				t.Fatal("fixture admin issuance failed")
			}
			old, root, err := srv.Store.CreateAuthToken(ctx, contextstore.TokenCreateInput{Label: "existing-service", ClientID: "tether-proxy", Scopes: []string{"write"}, NamespaceGlobs: []string{"project/example/*"}, TTL: 30 * time.Second})
			if err != nil {
				t.Fatal("fixture service issuance failed")
			}
			if empty {
				if _, err = srv.Store.DB().ExecContext(ctx, "UPDATE auth_tokens SET client_id='',scopes='[]',namespace_globs='[]' WHERE token_id=?", root.TokenID); err != nil {
					t.Fatal("empty-claims fixture failed")
				}
			}
			original, err := srv.Store.ValidateAuthTokenWithClaims(ctx, old)
			if err != nil {
				t.Fatal("original credential did not validate")
			}
			in := map[string]any{"principal_id": root.TokenID, "credential_id": root.TokenID, "expected_generation": 1, "idempotency_key": "issue-once", "overlap_seconds": 60, "ttl_seconds": 3600}
			response := rotationAPIRequest(t, srv, admin, "/v1/auth/tokens/rotate", in)
			issued := rotationAPIDecode(t, response)
			if issued.Token == "" || !issued.SecretAvailable || issued.Replayed || issued.Generation != 2 || issued.OperationGeneration != 2 || issued.PrincipalID != root.TokenID {
				t.Fatal("first issuance metadata or delivery is incorrect")
			}
			if response.Header().Get("Cache-Control") != "no-store" {
				t.Error("first secret response is cacheable")
			}
			replacement, err := srv.Store.ValidateAuthTokenWithClaims(ctx, issued.Token)
			if err != nil {
				t.Fatal("replacement credential did not validate")
			}
			if replacement.ClientID != original.ClientID || replacement.Label != original.Label || !reflect.DeepEqual(replacement.Scopes, original.Scopes) || !reflect.DeepEqual(replacement.NamespaceGlobs, original.NamespaceGlobs) {
				t.Error("rotation changed service identity or grants")
			}
			oldClaims, err := srv.Store.ValidateAuthTokenWithClaims(ctx, old)
			if err != nil {
				t.Fatal("old credential did not validate during overlap")
			}
			if oldClaims.ExpiresAt != original.ExpiresAt {
				t.Error("overlap extended the earlier original expiry")
			}
			revoked := rotationAPIDecode(t, rotationAPIRequest(t, srv, admin, "/v1/auth/tokens/revoke", map[string]any{"principal_id": root.TokenID, "credential_id": root.TokenID, "expected_generation": 2, "idempotency_key": "revoke-old"}))
			if revoked.Generation != 3 {
				t.Error("revoke did not advance family generation")
			}
			if err := srv.Store.ValidateAuthToken(ctx, old); !errors.Is(err, contextstore.ErrAuthTokenRevoked) {
				t.Error("revoked old credential was not rejected")
			}
			if err := srv.Store.ValidateAuthToken(ctx, issued.Token); err != nil {
				t.Error("targeted revoke invalidated replacement")
			}
			replayResponse := rotationAPIRequest(t, srv, admin, "/v1/auth/tokens/rotate", in)
			replay := rotationAPIDecode(t, replayResponse)
			if !replay.Replayed || replay.SecretAvailable || replay.Token != "" || replay.CredentialID != issued.CredentialID || replay.Generation != 3 || replay.OperationGeneration != 2 {
				t.Error("exact retry changed operation or replayed a secret")
			}
			for _, secret := range []string{old, admin, issued.Token} {
				if strings.Contains(replayResponse.Body.String(), secret) {
					t.Error("metadata-only retry disclosed a credential")
				}
			}
			list := performJSONWithHeaders(t, srv, http.MethodGet, "/v1/auth/tokens/list?principal_id="+root.TokenID, nil, map[string]string{"Authorization": "Bearer " + admin})
			if list.Code != http.StatusOK {
				t.Errorf("managed admin list failed: HTTP %d", list.Code)
			}
			for _, secret := range []string{old, admin, issued.Token} {
				if strings.Contains(list.Body.String(), secret) {
					t.Error("credential list disclosed a secret")
				}
			}
		})
	}
}

func TestServiceCredentialAPILostDeliveryAndFreshReplayAuthorization(t *testing.T) {
	srv := newTestServer(t)
	srv.ManagedAuth = true
	ctx := context.Background()
	admin, adminMeta, err := srv.Store.CreateAuthToken(ctx, contextstore.TokenCreateInput{Label: "administrator", Scopes: []string{"admin"}})
	if err != nil {
		t.Fatal("fixture admin issuance failed")
	}
	old, root, err := srv.Store.CreateAuthToken(ctx, contextstore.TokenCreateInput{Label: "service", Scopes: []string{"write"}})
	if err != nil {
		t.Fatal("fixture service issuance failed")
	}
	in := map[string]any{"principal_id": root.TokenID, "credential_id": root.TokenID, "expected_generation": 1, "idempotency_key": "lost-response"}
	payload, _ := json.Marshal(in)
	request := httptest.NewRequest(http.MethodPost, "/v1/auth/tokens/rotate", bytes.NewReader(payload))
	request.RemoteAddr = "127.0.0.1:32123"
	request.Header.Set("Authorization", "Bearer "+admin)
	sink := &rotationLostResponse{header: make(http.Header)}
	srv.ServeHTTP(sink, request)
	if sink.status != http.StatusOK || !sink.attempted {
		t.Fatal("lost-delivery fixture did not reach response write")
	}
	replay := rotationAPIDecode(t, rotationAPIRequest(t, srv, admin, "/v1/auth/tokens/rotate", in))
	if !replay.Replayed || replay.SecretAvailable || replay.Token != "" || replay.CredentialID == "" || replay.Generation != 2 {
		t.Fatal("lost delivery did not retain a metadata-only issued obligation")
	}
	if err := srv.Store.ValidateAuthToken(ctx, old); err != nil {
		t.Error("lost delivery invalidated the working predecessor")
	}
	if err := srv.Store.RevokeAuthTokenByID(ctx, adminMeta.TokenID); err != nil {
		t.Fatal("fixture administrator revocation failed")
	}
	denied := rotationAPIRequest(t, srv, admin, "/v1/auth/tokens/rotate", in)
	if denied.Code != http.StatusUnauthorized {
		t.Errorf("revoked administrator replay was not refused: HTTP %d", denied.Code)
	}
	if strings.Contains(denied.Body.String(), admin) || strings.Contains(denied.Body.String(), old) {
		t.Error("authorization refusal disclosed a credential")
	}
}

type rotationLostResponse struct {
	header    http.Header
	status    int
	attempted bool
}

func (w *rotationLostResponse) Header() http.Header    { return w.header }
func (w *rotationLostResponse) WriteHeader(status int) { w.status = status }
func (w *rotationLostResponse) Write([]byte) (int, error) {
	w.attempted = true
	return 0, io.ErrClosedPipe
}

func TestServiceCredentialAPIGenerationContention(t *testing.T) {
	srv := newTestServer(t)
	srv.ManagedAuth = true
	ctx := context.Background()
	admin, _, err := srv.Store.CreateAuthToken(ctx, contextstore.TokenCreateInput{Label: "administrator", Scopes: []string{"admin"}})
	if err != nil {
		t.Fatal("fixture administrator issuance failed")
	}
	_, root, err := srv.Store.CreateAuthToken(ctx, contextstore.TokenCreateInput{Label: "service", Scopes: []string{"write"}})
	if err != nil {
		t.Fatal("fixture service issuance failed")
	}
	ready := make(chan struct{})
	results := make(chan int, 2)
	for _, key := range []string{"concurrent-a", "concurrent-b"} {
		go func(key string) {
			<-ready
			// This helper's encoding cannot fail for the scalar fixture map.
			response := rotationAPIRequest(t, srv, admin, "/v1/auth/tokens/rotate", map[string]any{"principal_id": root.TokenID, "credential_id": root.TokenID, "expected_generation": 1, "idempotency_key": key})
			results <- response.Code
		}(key)
	}
	close(ready)
	first, second := <-results, <-results
	if !((first == http.StatusOK && second == http.StatusConflict) || (second == http.StatusOK && first == http.StatusConflict)) {
		t.Errorf("same-generation requests did not yield one winner and one conflict: HTTP %d,%d", first, second)
	}
	list := performJSONWithHeaders(t, srv, http.MethodGet, "/v1/auth/tokens/list?principal_id="+root.TokenID, nil, map[string]string{"Authorization": "Bearer " + admin})
	var metadata struct {
		Generation  int64                    `json:"generation"`
		Credentials []contextstore.AuthToken `json:"credentials"`
	}
	if list.Code != http.StatusOK || json.Unmarshal(list.Body.Bytes(), &metadata) != nil {
		t.Fatal("contention metadata read failed")
	}
	if metadata.Generation != 2 || len(metadata.Credentials) != 2 {
		t.Error("contention did not retain exactly one committed issuance")
	}
}

func TestServiceCredentialAPISchemaAndFamilyRefusalsPreserveState(t *testing.T) {
	srv := newTestServer(t)
	srv.ManagedAuth = true
	ctx := context.Background()
	admin, _, err := srv.Store.CreateAuthToken(ctx, contextstore.TokenCreateInput{Label: "administrator", Scopes: []string{"admin"}})
	if err != nil {
		t.Fatal("fixture administrator issuance failed")
	}
	_, root, err := srv.Store.CreateAuthToken(ctx, contextstore.TokenCreateInput{Label: "service", ClientID: "same-client", Scopes: []string{"write"}})
	if err != nil {
		t.Fatal("fixture service issuance failed")
	}
	_, foreign, err := srv.Store.CreateAuthToken(ctx, contextstore.TokenCreateInput{Label: "independent", ClientID: "same-client", Scopes: []string{"write"}})
	if err != nil {
		t.Fatal("fixture independent issuance failed")
	}
	valid := fmt.Sprintf(`{"principal_id":%q,"credential_id":%q,"expected_generation":1,"idempotency_key":"refused"}`, root.TokenID, root.TokenID)
	cases := map[string]string{
		"unknown":             strings.TrimSuffix(valid, "}") + `,"unexpected":"isolated-sensitive-value"}`,
		"duplicate":           strings.TrimSuffix(valid, "}") + `,"expected_generation":1}`,
		"fractional":          strings.Replace(valid, `"expected_generation":1`, `"expected_generation":1.5`, 1),
		"overflow":            strings.Replace(valid, `"expected_generation":1`, `"expected_generation":9223372036854775808`, 1),
		"negative-overlap":    strings.TrimSuffix(valid, "}") + `,"overlap_seconds":-1}`,
		"excess-overlap":      strings.TrimSuffix(valid, "}") + `,"overlap_seconds":86401}`,
		"zero-ttl":            strings.TrimSuffix(valid, "}") + `,"ttl_seconds":0}`,
		"excess-ttl":          strings.TrimSuffix(valid, "}") + `,"ttl_seconds":31536001}`,
		"foreign-same-client": strings.Replace(valid, root.TokenID, foreign.TokenID, 1),
	}
	before, err := srv.Store.ListAuthTokens(ctx, 100)
	if err != nil {
		t.Fatal("fixture state read failed")
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/v1/auth/tokens/rotate", strings.NewReader(body))
			req.RemoteAddr = "127.0.0.1:32123"
			req.Header.Set("Authorization", "Bearer "+admin)
			response := httptest.NewRecorder()
			srv.ServeHTTP(response, req)
			if response.Code != http.StatusBadRequest {
				t.Errorf("invalid request did not refuse: HTTP %d", response.Code)
			}
			if strings.Contains(response.Body.String(), admin) || strings.Contains(response.Body.String(), "isolated-sensitive-value") {
				t.Error("schema refusal disclosed supplied sensitive content")
			}
			after, err := srv.Store.ListAuthTokens(ctx, 100)
			if err != nil {
				t.Fatal("result state read failed")
			}
			if !reflect.DeepEqual(rotationCredentialState(before), rotationCredentialState(after)) {
				t.Error("invalid request changed credential eligibility or grants")
			}
		})
	}
}

func TestServiceCredentialAPIAlreadyRevokedRefusalRetainsSafeMetadata(t *testing.T) {
	srv := newTestServer(t)
	srv.ManagedAuth = true
	ctx := context.Background()
	adminRaw, _, err := srv.Store.CreateAuthToken(ctx, contextstore.TokenCreateInput{Label: "administrator", Scopes: []string{"admin"}})
	if err != nil {
		t.Fatal("fixture administrator issuance failed")
	}
	old, root, err := srv.Store.CreateAuthToken(ctx, contextstore.TokenCreateInput{Label: "service", ClientID: "tether-proxy", Scopes: []string{"write"}})
	if err != nil {
		t.Fatal("fixture service issuance failed")
	}
	limited, foreign, err := srv.Store.CreateAuthToken(ctx, contextstore.TokenCreateInput{Label: "limited", Scopes: []string{"write"}})
	if err != nil {
		t.Fatal("fixture limited caller issuance failed")
	}
	issued := rotationAPIDecode(t, rotationAPIRequest(t, srv, adminRaw, "/v1/auth/tokens/rotate", map[string]any{"principal_id": root.TokenID, "credential_id": root.TokenID, "expected_generation": 1, "idempotency_key": "issued-before-revoke"}))
	revoked := rotationAPIDecode(t, rotationAPIRequest(t, srv, adminRaw, "/v1/auth/tokens/revoke", map[string]any{"principal_id": root.TokenID, "credential_id": root.TokenID, "expected_generation": 2, "idempotency_key": "first-revoke"}))
	if revoked.Generation != 3 {
		t.Fatal("fixture revoke did not reach generation three")
	}
	admin, err := srv.Store.AuthorizeCredentialAdmin(ctx, adminRaw)
	if err != nil {
		t.Fatal("fixture administrator authorization failed")
	}
	before, err := srv.Store.ListServiceCredentials(ctx, admin, root.TokenID)
	if err != nil {
		t.Fatal("fixture family read failed")
	}
	persistence := func() [2]int64 {
		t.Helper()
		var counts [2]int64
		if srv.Store.DB().QueryRowContext(ctx, "SELECT count(*) FROM auth_credential_operations").Scan(&counts[0]) != nil || srv.Store.DB().QueryRowContext(ctx, "SELECT count(*) FROM audit_events").Scan(&counts[1]) != nil {
			t.Fatal("fixture receipt/audit observation failed")
		}
		return counts
	}
	priorPersistence := persistence()
	for _, mode := range []string{"authorized", "limited", "unknown", "foreign-family"} {
		t.Run(mode, func(t *testing.T) {
			actor, principal := adminRaw, root.TokenID
			switch mode {
			case "limited":
				actor = limited
			case "unknown":
				actor = "unrecognized-fixture-caller"
			case "foreign-family":
				principal = foreign.TokenID
			}
			res := rotationAPIRequest(t, srv, actor, "/v1/auth/tokens/revoke", map[string]any{"principal_id": principal, "credential_id": root.TokenID, "expected_generation": 3, "idempotency_key": "new-refused-revoke-" + mode})
			var body struct {
				Code    string `json:"code"`
				Details struct {
					Current *struct {
						PrincipalID         string `json:"principal_id"`
						CredentialID        string `json:"credential_id"`
						Generation          int64  `json:"generation"`
						OperationGeneration int64  `json:"operation_generation"`
						Status              string `json:"status"`
						RevokedAt           string `json:"revoked_at"`
						SecretAvailable     bool   `json:"secret_available"`
						Replayed            bool   `json:"replayed"`
					} `json:"current_credential"`
				} `json:"details"`
			}
			if json.Unmarshal(res.Body.Bytes(), &body) != nil {
				t.Fatal("refusal response decoding failed")
			}
			if mode == "authorized" {
				m := body.Details.Current
				if res.Code != http.StatusBadRequest || body.Code != "credential_revoked" || m == nil || m.PrincipalID != root.TokenID || m.CredentialID != root.TokenID || m.Generation != 3 || m.OperationGeneration != 0 || m.Status != "revoked" || m.RevokedAt == "" || m.SecretAvailable || m.Replayed {
					t.Error("authorized refusal lost current revoked metadata or claimed a new operation")
				}
			} else if res.Code < 400 || res.Code >= 500 || body.Details.Current != nil || strings.Contains(res.Body.String(), root.TokenID) || strings.Contains(res.Body.String(), foreign.TokenID) {
				t.Error("untrusted or foreign-family refusal leaked credential metadata")
			}
			for _, secret := range []string{adminRaw, old, limited, issued.Token} {
				if strings.Contains(res.Body.String(), secret) {
					t.Error("refusal disclosed a credential")
				}
			}
			after, err := srv.Store.ListServiceCredentials(ctx, admin, root.TokenID)
			if err != nil {
				t.Fatal("result family read failed")
			}
			if after.Generation != before.Generation || !reflect.DeepEqual(rotationCredentialState(before.Credentials), rotationCredentialState(after.Credentials)) || persistence() != priorPersistence {
				t.Error("refused operation changed family state or wrote a receipt/audit")
			}
		})
	}
	if err := srv.Store.ValidateAuthToken(ctx, issued.Token); err != nil {
		t.Error("refusal invalidated the working replacement")
	}
}
