package main

import (
	"strings"
	"testing"
)

func TestIsLoopbackAddr(t *testing.T) {
	cases := []struct {
		addr string
		want bool
		why  string
	}{
		{"127.0.0.1:8089", true, "canonical IPv4 loopback"},
		{"127.0.0.1", true, "loopback without a port"},
		{"127.4.5.6:8089", true, "the whole 127/8 block is loopback"},
		{"[::1]:8089", true, "IPv6 loopback in bracket form"},
		{"::1", true, "IPv6 loopback bare"},
		{"localhost:8089", true, "the name every dev types"},
		{"LOCALHOST:8089", true, "host matching is case-insensitive"},
		{"localhost", true, "loopback name without a port"},
		{":8089", false, "empty host means every interface — the old default"},
		{"", false, "empty address means every interface"},
		{"0.0.0.0:8089", false, "explicit wildcard"},
		{"[::]:8089", false, "IPv6 wildcard"},
		{"192.168.1.10:8089", false, "LAN address"},
		{"example.com:8089", false, "a name we will not resolve is not loopback"},
	}
	for _, tc := range cases {
		if got := isLoopbackAddr(tc.addr); got != tc.want {
			t.Errorf("isLoopbackAddr(%q) = %v, want %v (%s)", tc.addr, got, tc.want, tc.why)
		}
	}
}

func TestValidateExposureRefusesUnauthenticatedRemote(t *testing.T) {
	cases := []struct {
		name    string
		cfg     serveConfig
		wantErr bool
	}{
		{"loopback without auth is the dev default", serveConfig{Addr: "127.0.0.1:8089"}, false},
		{"localhost without auth", serveConfig{Addr: "localhost:8089"}, false},
		{"ipv6 loopback without auth", serveConfig{Addr: "[::1]:8089"}, false},
		{"bare port binds all interfaces", serveConfig{Addr: ":8089"}, true},
		{"wildcard binds all interfaces", serveConfig{Addr: "0.0.0.0:8089"}, true},
		{"lan address", serveConfig{Addr: "192.168.1.10:8089"}, true},
		{"remote with managed auth", serveConfig{Addr: ":8089", ManagedAuth: true}, false},
		{"remote with static token", serveConfig{Addr: ":8089", StaticToken: "s3cret"}, false},
		{"remote with blank static token is still unauthenticated", serveConfig{Addr: ":8089", StaticToken: "   "}, true},
		{"remote with explicit opt-in", serveConfig{Addr: ":8089", AllowUnauthenticatedRemote: true}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateExposure(tc.cfg)
			if tc.wantErr && err == nil {
				t.Fatalf("expected refusal for %+v", tc.cfg)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected refusal for %+v: %v", tc.cfg, err)
			}
		})
	}
}

// TestValidateExposureErrorNamesEveryWayOut keeps the refusal actionable: an
// operator who hits it should not have to read the source to get unstuck.
func TestValidateExposureErrorNamesEveryWayOut(t *testing.T) {
	err := validateExposure(serveConfig{Addr: "0.0.0.0:8089"})
	if err == nil {
		t.Fatal("expected refusal")
	}
	msg := err.Error()
	for _, want := range []string{
		"0.0.0.0:8089",
		"--addr",
		defaultServeAddr,
		"--managed-auth",
		"--static-token",
		"--allow-unauthenticated-remote",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("refusal message does not mention %q:\n%s", want, msg)
		}
	}
}

func TestParseServeArgsRejectsUnauthenticatedRemoteAddr(t *testing.T) {
	if _, err := parseServeArgs([]string{"--addr", "0.0.0.0:8089"}); err == nil {
		t.Fatal("expected parseServeArgs to refuse an unauthenticated non-loopback bind")
	}
	if _, err := parseServeArgs([]string{"--addr", ":8089"}); err == nil {
		t.Fatal("expected parseServeArgs to refuse a bare-port bind without auth")
	}
}

func TestParseServeArgsAllowsRemoteWithTokenModeOrOptIn(t *testing.T) {
	if _, err := parseServeArgs([]string{"--addr", "0.0.0.0:8089", "--managed-auth"}); err != nil {
		t.Fatalf("managed auth should permit a remote bind: %v", err)
	}
	if _, err := parseServeArgs([]string{"--addr", "0.0.0.0:8089", "--static-token", "abc"}); err != nil {
		t.Fatalf("static token should permit a remote bind: %v", err)
	}
	cfg, err := parseServeArgs([]string{"--addr", "0.0.0.0:8089", "--allow-unauthenticated-remote"})
	if err != nil {
		t.Fatalf("explicit opt-in should permit a remote bind: %v", err)
	}
	if !cfg.AllowUnauthenticatedRemote {
		t.Fatal("opt-in flag did not reach the config")
	}
}
