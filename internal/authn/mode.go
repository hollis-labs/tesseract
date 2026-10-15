// Package authn wires the shared go-auth guard into Tesseract: it resolves the
// mode, builds the verifiers (the managed token store, the legacy static
// token, and the Tether JWT/JWKS verifier), translates a verified principal
// into the claims Tesseract's handlers check, and keeps the would-deny summary
// that finds callers still sending no bearer.
//
// See docs/auth.md for the operator view.
package authn

import (
	"fmt"
	"os"
	"strings"

	auth "github.com/hollis-labs/libs/security/auth"
	"github.com/hollis-labs/tesseract/internal/config"
)

// Mode sources, reported in the startup line and whoami.
const (
	SourceDefault = "default"
	SourceConfig  = "config"
	SourceEnv     = "env"
)

// ModeInput is everything that decides the mode.
type ModeInput struct {
	Config config.AuthConfig
	// Loopback is true when the process serves only the loopback interface, or
	// has no listener at all (stdio MCP).
	Loopback bool
	// Credentialed is true when --managed-auth or --static-token was given. The
	// operator asked for authentication explicitly, so the code default is
	// enforce even on loopback. Without this the in-code "permissive on
	// loopback" default would silently downgrade a listener that enforces
	// today (the hub runs `serve -managed-auth` on 127.0.0.1).
	Credentialed bool
	// ConfigFailed is true when config.yaml could not be loaded and defaults
	// were substituted. An unreadable config must not relax auth: the default
	// becomes enforce.
	ConfigFailed bool
}

// DefaultMode is the code default, before config and environment.
//
//	non-loopback listener            -> enforce
//	--managed-auth / --static-token  -> enforce
//	config.yaml failed to load       -> enforce (fail closed)
//	bare loopback listener / stdio   -> permissive (would-deny log)
func DefaultMode(in ModeInput) auth.Mode {
	if !in.Loopback || in.Credentialed || in.ConfigFailed {
		return auth.ModeEnforce
	}
	return auth.ModePermissive
}

// ResolveMode applies precedence: HOLLIS_AUTH_TESSERACT, then HOLLIS_AUTH, then
// config auth.mode, then DefaultMode. An invalid value anywhere is an error,
// never a fall-through (a typo must not leave auth weaker than intended).
func ResolveMode(in ModeInput) (mode auth.Mode, source string, err error) {
	def := DefaultMode(in)
	source = SourceDefault
	if v := strings.TrimSpace(in.Config.Mode); v != "" {
		m, perr := auth.ParseMode(v)
		if perr != nil {
			return "", "", fmt.Errorf("config auth.mode: %w", perr)
		}
		def, source = m, SourceConfig
	}
	if envSet() {
		source = SourceEnv
	}
	mode, err = auth.ModeFromEnv("tesseract", def)
	if err != nil {
		return "", "", fmt.Errorf("HOLLIS_AUTH: %w", err)
	}
	return mode, source, nil
}

func envSet() bool {
	for _, k := range []string{"HOLLIS_AUTH_TESSERACT", "HOLLIS_AUTH"} {
		if strings.TrimSpace(os.Getenv(k)) != "" {
			return true
		}
	}
	return false
}
