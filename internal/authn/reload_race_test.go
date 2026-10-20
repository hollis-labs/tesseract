package authn

import (
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hollis-labs/tesseract/internal/config"
)

// Archie's requirement on #86: the verifier, the mode and the namespace ceiling
// are ONE snapshot, taken once per request. Two configurations differ in key,
// ceiling and mode together; a request must never see one configuration's
// verifier with the other's ceiling.
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
