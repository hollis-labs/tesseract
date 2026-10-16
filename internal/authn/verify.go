package authn

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	auth "github.com/hollis-labs/libs/security/auth"
	"github.com/hollis-labs/tesseract/internal/authscope"
	"github.com/hollis-labs/tesseract/internal/contextstore"
)

// idPrefixToken marks a principal that came from the managed token store.
const idPrefixToken = "tesseract-token/"

// StaticTokenID is the principal id of the legacy --static-token credential.
const StaticTokenID = "static-token"

// StoreVerifier is the go-auth Verifier backed by Tesseract's managed token
// store (auth_tokens). It reuses the store's own validation, so the existing
// hashes, expiry, revocation, scopes and namespace globs apply with no
// re-minting. It remembers each verified token's stored claims so the fine
// layer can enforce legacy scopes and namespace globs on exactly what the
// store said (see Claims).
type StoreVerifier struct {
	Store *contextstore.Store

	mu     sync.Mutex
	claims map[string]contextstore.AuthToken // by principal id; bounded by live token count
}

// Verify implements auth.Verifier.
func (v *StoreVerifier) Verify(ctx context.Context, token string) (auth.Principal, error) {
	c, err := v.Store.ValidateAuthTokenWithClaims(ctx, token)
	switch {
	case err == nil:
	case errors.Is(err, contextstore.ErrAuthTokenInvalid),
		errors.Is(err, contextstore.ErrAuthTokenRevoked),
		errors.Is(err, contextstore.ErrAuthTokenExpired):
		return auth.Principal{}, auth.ErrInvalidToken
	default:
		return auth.Principal{}, auth.ErrUnavailable
	}
	id := idPrefixToken + c.TokenID
	v.mu.Lock()
	if v.claims == nil {
		v.claims = map[string]contextstore.AuthToken{}
	}
	v.claims[id] = c
	v.mu.Unlock()
	p := auth.Principal{
		Verified: true,
		ID:       id,
		Kind:     auth.KindService,
		Display:  firstNonEmpty(c.Label, c.ClientID, c.TokenID),
		Scopes:   authscope.FromLegacy(c.Scopes),
		TokenID:  c.TokenID,
	}
	if t, perr := time.Parse(time.RFC3339, strings.TrimSpace(c.ExpiresAt)); perr == nil {
		p.ExpiresAt = t
	}
	return p, nil
}

// claimsFor returns the stored claims for a principal this verifier produced.
func (v *StoreVerifier) claimsFor(p auth.Principal) (contextstore.AuthToken, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	c, ok := v.claims[p.ID]
	return c, ok
}

// routed sends each bearer to the one verifier that can own it: a JWT-shaped
// token (three dot-separated segments, header starting "eyJ") goes to the JWT verifier, anything else
// to the opaque-token verifiers. Without this a JWKS outage would turn every
// invalid opaque token into a 503 instead of a 401, and every JWT would cost a
// pointless database lookup.
type routed struct {
	jwt    auth.Verifier // may be nil
	opaque auth.Verifier
}

func (r routed) Verify(ctx context.Context, token string) (auth.Principal, error) {
	if strings.Count(token, ".") == 2 && strings.HasPrefix(token, "eyJ") {
		if r.jwt == nil {
			return auth.Principal{}, auth.ErrInvalidToken
		}
		return r.jwt.Verify(ctx, token)
	}
	return r.opaque.Verify(ctx, token)
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
