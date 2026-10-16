package authn

import (
	"testing"

	auth "github.com/hollis-labs/libs/security/auth"
	"github.com/hollis-labs/tesseract/internal/config"
)

func TestDefaultMode(t *testing.T) {
	cases := []struct {
		name string
		in   ModeInput
		want auth.Mode
	}{
		{"bare loopback", ModeInput{Loopback: true}, auth.ModePermissive},
		{"non-loopback listener", ModeInput{Loopback: false}, auth.ModeEnforce},
		{"loopback with --managed-auth or --static-token", ModeInput{Loopback: true, Credentialed: true}, auth.ModeEnforce},
		{"config failed to load", ModeInput{Loopback: true, ConfigFailed: true}, auth.ModeEnforce},
	}
	for _, tc := range cases {
		if got := DefaultMode(tc.in); got != tc.want {
			t.Errorf("%s: %s, want %s", tc.name, got, tc.want)
		}
	}
}

func TestResolveModePrecedence(t *testing.T) {
	in := ModeInput{Loopback: true, Config: config.AuthConfig{Mode: "off"}}

	t.Setenv("HOLLIS_AUTH", "")
	t.Setenv("HOLLIS_AUTH_TESSERACT", "")
	if m, src, err := ResolveMode(in); err != nil || m != auth.ModeOff || src != SourceConfig {
		t.Fatalf("config only = %v %v %v", m, src, err)
	}
	t.Setenv("HOLLIS_AUTH", "permissive")
	if m, src, _ := ResolveMode(in); m != auth.ModePermissive || src != SourceEnv {
		t.Fatalf("global env must beat config, got %v %v", m, src)
	}
	t.Setenv("HOLLIS_AUTH_TESSERACT", "enforce")
	if m, _, _ := ResolveMode(in); m != auth.ModeEnforce {
		t.Fatalf("per-app env must beat global env, got %v", m)
	}
}

func TestResolveModeRejectsTyposInsteadOfFallingThrough(t *testing.T) {
	t.Setenv("HOLLIS_AUTH", "")
	t.Setenv("HOLLIS_AUTH_TESSERACT", "")
	if _, _, err := ResolveMode(ModeInput{Loopback: true, Config: config.AuthConfig{Mode: "enforse"}}); err == nil {
		t.Error("an invalid config auth.mode must be an error")
	}
	t.Setenv("HOLLIS_AUTH_TESSERACT", "enforse")
	if _, _, err := ResolveMode(ModeInput{Loopback: true}); err == nil {
		t.Error("an invalid HOLLIS_AUTH_TESSERACT must be an error")
	}
}
