package authn

import (
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hollis-labs/tesseract/internal/config"
)

// Archie's requirement on #86: the verifier, the mode and the namespace ceiling
// are ONE snapshot, taken once per request. The two configurations here differ
// in key and ceiling (both enforce); a request must never see one
// configuration's verifier with the other's ceiling. Mode straddling is covered
// by TestConcurrentRequestsNeverStraddleModes.
func TestConcurrentRequestsNeverSeeAMixedVerifierAndCeiling(t *testing.T) {
	x := newFixture(t) // issuer key X, ceiling app/x/*
	y := newFixture(t) // issuer key Y, ceiling app/y/*
	cfgX := config.AuthConfig{Mode: "enforce", JWT: func() config.AuthJWTConfig {
		c := x.jwtConfig()
		c.NamespaceGlobs = []string{"app/x/*"}
		return c
	}()}
	cfgY := config.AuthConfig{Mode: "enforce", JWT: func() config.AuthJWTConfig {
		c := y.jwtConfig()
		c.NamespaceGlobs = []string{"app/y/*"}
		return c
	}()}
	s := x.serve(cfgX, nil)
	tokX := x.jwt("tesseract.read", nil)
	tokY := y.jwt("tesseract.read", nil)
	const inX = "/v1/memory/current?namespace=app/x/memory/m&key=k"
	const inY = "/v1/memory/current?namespace=app/y/memory/m&key=k"

	stop := make(chan struct{})
	var wg sync.WaitGroup
	var mixed atomic.Int32
	var checked atomic.Int64
	probe := func(tok, path string) {
		rec := s.do("GET", path, tok, nil)
		checked.Add(1)
		// Admitted by its own configuration and inside that configuration's
		// ceiling => the key is absent (404). Not admitted => 401. A 403 means
		// the token was verified by one configuration and judged against the
		// other's ceiling.
		if rec.Code == 403 {
			if mixed.Add(1) == 1 {
				t.Logf("first 403: %s %s -> %s", tok[:12], path, strings.TrimSpace(rec.Body.String()))
			}
		}
	}
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					probe(tokX, inX)
					probe(tokY, inY)
				}
			}
		}()
	}
	// Keep swapping generations until the readers have made plenty of requests.
	deadline := time.Now().Add(30 * time.Second)
	for i := 0; checked.Load() < 4000 && time.Now().Before(deadline); i++ {
		cfg := cfgY
		if i%2 == 1 {
			cfg = cfgX
		}
		if err := s.rt.Reload(cfg); err != nil {
			t.Fatalf("Reload %d: %v", i, err)
		}
		time.Sleep(time.Millisecond) // let requests interleave with the swaps
	}
	close(stop)
	wg.Wait()
	if checked.Load() < 4000 {
		t.Fatalf("only %d requests ran during the reloads", checked.Load())
	}
	if n := mixed.Load(); n != 0 {
		t.Fatalf("%d of %d requests were judged by one configuration's verifier and another's namespace ceiling", n, checked.Load())
	}
}

// Two configurations that differ in MODE as well as key: A enforces with key A,
// B is permissive with key B. /v1/auth/whoami reports the mode the request was
// judged under and whether its token verified, so a request can be checked for
// a straddle without relying on any one component.
func TestConcurrentRequestsNeverStraddleModes(t *testing.T) {
	a := newFixture(t)
	b := newFixture(t)
	cfgA := config.AuthConfig{Mode: "enforce", JWT: a.jwtConfig()}
	cfgB := config.AuthConfig{Mode: "permissive", JWT: b.jwtConfig()}
	s := a.serve(cfgA, quietStderr)
	tokA := a.jwt("tesseract.read", nil)
	tokB := b.jwt("tesseract.read", nil)

	type reply struct {
		Mode     string `json:"mode"`
		Verified bool   `json:"verified"`
	}
	var bad atomic.Int32
	var checked atomic.Int64
	check := func(tok string, enforceVerified, permissiveVerified bool) {
		rec := s.do("GET", "/v1/auth/whoami", tok, nil)
		checked.Add(1)
		if rec.Code == 401 {
			// Refused: only possible under the enforcing configuration A, with a
			// token A's key cannot verify (token B).
			if tok != tokB {
				bad.Add(1)
			}
			return
		}
		var r reply
		if err := json.Unmarshal(rec.Body.Bytes(), &r); err != nil {
			bad.Add(1)
			return
		}
		// Each configuration allows exactly one (mode, verified) pair per token.
		okEnforce := r.Mode == "enforce" && r.Verified == enforceVerified
		okPermissive := r.Mode == "permissive" && r.Verified == permissiveVerified
		if !okEnforce && !okPermissive {
			if bad.Add(1) == 1 {
				t.Logf("straddle: token=%s mode=%s verified=%v", tok[:8], r.Mode, r.Verified)
			}
		}
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					// token A: verified under A (enforce); unverified under B (its key is not B's).
					check(tokA, true, false)
					// token B: refused under A (checked above); verified under B (permissive).
					check(tokB, false, true)
				}
			}
		}()
	}
	deadline := time.Now().Add(30 * time.Second)
	for i := 0; checked.Load() < 4000 && time.Now().Before(deadline); i++ {
		cfg := cfgB
		if i%2 == 1 {
			cfg = cfgA
		}
		if err := s.rt.Reload(cfg); err != nil {
			t.Fatalf("Reload %d: %v", i, err)
		}
		time.Sleep(time.Millisecond)
	}
	close(stop)
	wg.Wait()
	if checked.Load() < 4000 {
		t.Fatalf("only %d requests ran during the reloads", checked.Load())
	}
	if n := bad.Load(); n != 0 {
		t.Fatalf("%d of %d requests were judged by a mix of one configuration's verifier and the other's mode", n, checked.Load())
	}
}
