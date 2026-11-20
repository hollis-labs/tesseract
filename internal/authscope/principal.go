package authscope

import auth "github.com/hollis-labs/libs/security/auth"

const (
	// ManagedPrincipalPrefix scopes identity to the immutable stored credential
	// ID, never its caller-controlled display label or client ID.
	ManagedPrincipalPrefix = "tesseract-token/"
	// StaticPrincipalID identifies the single configured legacy shared credential.
	StaticPrincipalID = "static-token"
)

// ManagedPrincipal translates claims AFTER successful credential validation.
// It is shared by the guard verifier and legacy HTTP/MCP admission boundaries
// so every supported transport records the same credential identity.
func ManagedPrincipal(tokenID string, scopes []string) auth.Principal {
	return auth.Principal{Verified: true, ID: ManagedPrincipalPrefix + tokenID,
		Kind: auth.KindService, TokenID: tokenID, Scopes: FromLegacy(scopes)}
}
