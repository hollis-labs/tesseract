package contextapi

import (
	"context"
	"net/http"
	"reflect"
	"strings"
	"testing"

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
