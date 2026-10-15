// Package credentials is Tesseract's only seam to go-secrets. It does not
// resolve anything itself: it names the references Tesseract uses, decides
// which failures are fatal, and hands the resolved value to the one call that
// needs it. Values never pass through a log line, an error string or JSON.
package credentials

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	secrets "github.com/hollis-labs/libs/security/secrets"
	"github.com/hollis-labs/libs/security/secrets/tokenfile"
)

// Default managed-store references. They live in the go-secrets file store
// (file://<ns>/<key>, 0600 in a 0700 tree under $XDG_STATE_HOME/hollis/secrets)
// and are written with `secrets set`, never by Tesseract.
const (
	DefaultOpenAIRef    = "file://tesseract/openai-api-key"
	DefaultAnthropicRef = "file://tesseract/anthropic-api-key"
	DefaultMCPTokenRef  = "file://tesseract/mcp-token"
)

// Resolver is the slice of *secrets.Resolver Tesseract uses. Tests substitute a
// fake; production uses New.
type Resolver interface {
	Resolve(ctx context.Context, ref string) (secrets.Resolved, error)
}

// New builds the process resolver: keychain, helper, file and op. The
// environment is deliberately not a scheme here (no WithEnv).
func New() Resolver { return secrets.New(secrets.WithTimeout(10 * time.Second)) }

// ErrNotConfigured means no reference applies and nothing was attempted.
var ErrNotConfigured = errors.New("credentials: no reference configured")

// IsRef reports whether s is shaped like a secret reference.
func IsRef(s string) bool { return secrets.IsRef(strings.TrimSpace(s)) }

// DefaultProviderRef is the managed-store reference for a provider's API key,
// or "" for a provider with no key.
func DefaultProviderRef(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "openai":
		return DefaultOpenAIRef
	case "anthropic":
		return DefaultAnthropicRef
	}
	return ""
}

// ProviderRef returns the reference to use for provider and whether the
// operator named it (explicit) rather than it being the default.
func ProviderRef(provider, configured string) (ref string, explicit bool) {
	if c := strings.TrimSpace(configured); c != "" {
		return c, true
	}
	return DefaultProviderRef(provider), false
}

// Resolve returns the value behind ref. The error is a *secrets.Error whose
// text never renders the value; callers may log it.
func Resolve(ctx context.Context, r Resolver, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", ErrNotConfigured
	}
	if r == nil {
		r = New()
	}
	res, err := r.Resolve(ctx, ref)
	if err != nil {
		return "", err
	}
	v := strings.TrimSpace(res.Secret.Reveal())
	if v == "" {
		return "", fmt.Errorf("credentials: %s resolved to an empty value", ref)
	}
	return v, nil
}

// ProviderKey resolves the API key for provider.
//
// Fail-closed contract: a reference the operator named that cannot be resolved
// is an error (the caller must refuse to start). The default reference being
// absent is the documented no-credential path: it returns ("", nil) so the
// caller can degrade (BM25-only recall, synthesis 503) exactly as before. Any
// error other than not_found on the default reference (denied, unavailable,
// invalid, empty) is still an error.
func ProviderKey(ctx context.Context, r Resolver, provider, configured string) (string, error) {
	ref, explicit := ProviderRef(provider, configured)
	if ref == "" {
		return "", nil
	}
	v, err := Resolve(ctx, r, ref)
	if err != nil {
		if !explicit && errors.Is(err, secrets.ErrNotFound) {
			return "", nil
		}
		return "", fmt.Errorf("%s api key: %w", provider, err)
	}
	return v, nil
}

// ProviderKeyPresent reports whether the provider key resolves, for status
// surfaces. It never returns the value.
func ProviderKeyPresent(ctx context.Context, r Resolver, provider, configured string) (ref string, present bool, err error) {
	ref, _ = ProviderRef(provider, configured)
	if ref == "" {
		return "", false, nil
	}
	if _, err := Resolve(ctx, r, ref); err != nil {
		return ref, false, err
	}
	return ref, true, nil
}

// Token resolves a bearer-token value that may be a literal (legacy) or a
// reference. A reference that fails to resolve is an error; a literal is
// returned trimmed. The bool reports whether the input was a reference.
func Token(ctx context.Context, r Resolver, value string) (string, bool, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", false, nil
	}
	if !IsRef(value) {
		return value, false, nil
	}
	v, err := Resolve(ctx, r, value)
	return v, true, err
}

// ReadTokenFile reads a bearer-token file under go-secrets' strict rules
// (regular file, mode 0600, caller-owned, final component not a symlink).
func ReadTokenFile(path string) (string, error) { return tokenfile.Read(path) }
