package authn

import (
	"errors"
	"testing"

	auth "github.com/hollis-labs/libs/security/auth"
	"github.com/hollis-labs/tesseract/internal/config"
)

// CW-20261020-0038: a reload may keep or tighten the mode anywhere, but may
// loosen it only on a loopback listener. A tailnet address passes the startup
// bind rule (ValidateBind) and is still not loopback for a reload.

func TestReloadCannotLoosenOnAnyNonLoopbackListenerTailnetIncluded(t *testing.T) {
	for _, addr := range []string{"100.64.0.1:8089", "100.101.102.103:8089", "host.tail1234.ts.net:8089", "0.0.0.0:8089", "10.0.0.5:8089"} {
		for _, to := range []string{"permissive", "off"} {
			f := newFixture(t)
			s := f.serve(config.AuthConfig{Mode: "enforce", JWT: f.jwtConfig()}, func(o *Options) { o.Addr, o.Loopback = addr, false })
			tok := f.jwt("tesseract.read", nil)
			err := s.rt.Reload(config.AuthConfig{Mode: to, JWT: f.jwtConfig()})
			if err == nil {
				t.Fatalf("%s: reload to %s accepted", addr, to)
			}
			if addr != "0.0.0.0:8089" && addr != "10.0.0.5:8089" && !errors.Is(err, auth.ErrLoosenRefused) {
				t.Fatalf("%s: want ErrLoosenRefused (the tailnet case the startup rule lets through), got %v", addr, err)
			}
			if s.rt.Mode() != "enforce" || anonymousStatus(s) != 401 {
				t.Fatalf("%s: a refused reload changed the mode: %s", addr, s.rt.Mode())
			}
			wantStatus(t, s.do("GET", "/v1/namespaces/list", tok, nil), 200, addr+": existing token after the refused reload")
		}
	}
}

func TestReloadOnATailnetListenerKeepsOrTightensTheMode(t *testing.T) {
	f, other := newFixture(t), newFixture(t)
	s := f.serve(config.AuthConfig{Mode: "permissive", JWT: f.jwtConfig()}, func(o *Options) { o.Addr, o.Loopback = "100.64.0.1:8089", false })
	// Same mode: a key rotation is not a loosening.
	if err := s.rt.Reload(config.AuthConfig{Mode: "permissive", JWT: other.jwtConfig()}); err != nil {
		t.Fatalf("same-mode reload refused on a tailnet listener: %v", err)
	}
	wantStatus(t, s.do("GET", "/v1/namespaces/list", other.jwt("tesseract.read", nil), nil), 200, "the rotated key")
	// Tighter.
	if err := s.rt.Reload(config.AuthConfig{Mode: "enforce", JWT: other.jwtConfig()}); err != nil {
		t.Fatalf("tightening refused on a tailnet listener: %v", err)
	}
	if s.rt.Mode() != "enforce" || anonymousStatus(s) != 401 {
		t.Fatalf("mode after tightening: %s", s.rt.Mode())
	}
	// And back down is refused.
	if err := s.rt.Reload(config.AuthConfig{Mode: "permissive", JWT: other.jwtConfig()}); !errors.Is(err, auth.ErrLoosenRefused) {
		t.Fatalf("loosening on a tailnet listener: %v", err)
	}
}

func TestReloadMayLoosenOnLoopback(t *testing.T) {
	f := newFixture(t)
	s := f.serve(config.AuthConfig{Mode: "enforce", JWT: f.jwtConfig()}, func(o *Options) { o.Addr, o.Loopback = "127.0.0.1:8089", true })
	if err := s.rt.Reload(config.AuthConfig{Mode: "permissive", JWT: f.jwtConfig()}); err != nil {
		t.Fatalf("loopback loosening refused: %v", err)
	}
	if s.rt.Mode() != "permissive" {
		t.Fatalf("mode: %s", s.rt.Mode())
	}
}
