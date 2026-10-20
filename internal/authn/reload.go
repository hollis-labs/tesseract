package authn

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	auth "github.com/hollis-labs/libs/security/auth"
	"github.com/hollis-labs/tesseract/internal/config"
)

// Reloading auth configuration (CW-20261020-0004).
//
// `auth.mode`, `auth.jwt` and `auth.deny_list` used to be read once at startup,
// so adding an issuer, rotating the bootstrap key or flipping the posture dial
// meant a restart. Reload re-reads them while the daemon serves requests.
//
// What reloads: the mode (config auth.mode; HOLLIS_AUTH_TESSERACT / HOLLIS_AUTH
// still win, and the environment does not change under a running process), the
// JWT verifier (issuer, audience, JWKS URL, bootstrap key, namespace ceiling)
// and the deny-list (URL, bearer reference). What does not: the managed-token
// store, the static token and the listener, which come from flags and the
// database, not from the config file.
//
// All or nothing. The new generation is built completely (verifiers, the
// deny-list bearer, a fresh JWKS refresh loop) before anything is installed. If
// any step fails, including the bind guard that refuses to relax auth on a
// non-loopback listener, the running configuration is untouched and the error
// says why. Startup is fail-closed on an unreadable config; a reload of an
// unreadable config does nothing, so a half-saved file cannot change
// anything.
//
// Install is two steps: Guard.Apply swaps the mode and verifier together, then
// the runtime state (verifier chain, namespace ceiling) is published. A request
// that is verified in the instant between them can be judged by the new
// verifier with the previous ceiling, or the other way round for the request
// that straddles it. The window is a single in-flight request at the moment of
// the swap; the next request sees the new generation entirely.

// Reload applies cfg as the new auth configuration. It is safe to call
// concurrently with requests; calls are serialized.
func (rt *Runtime) Reload(cfg config.AuthConfig) error {
	rt.reload.Lock()
	defer rt.reload.Unlock()

	next, err := rt.buildState(cfg, false)
	if err != nil {
		return fmt.Errorf("auth reload: %w", err)
	}
	if err := rt.Guard.Apply(next.mode, next.verify); err != nil {
		next.cancel()
		return fmt.Errorf("auth reload: %w", err)
	}
	prev := rt.state.Swap(next)
	prev.cancel() // stops the previous generation's JWKS refresh and deny-list poll
	rt.startSummaryIfNeeded()
	if rt.stderr != nil {
		_, _ = fmt.Fprintf(rt.stderr, "  Auth reloaded: %s\n", rt.Describe())
	}
	return nil
}

// Watch re-reads the config file when its content changes, on SIGHUP, and on
// Trigger of the returned Reloader, and applies its auth section with Reload.
// A file that fails to parse, or an auth section that fails to build, is
// reported and ignored: the running configuration stays. It returns when ctx is
// done.
func (rt *Runtime) Watch(ctx context.Context, path string, interval time.Duration, logger *slog.Logger) {
	r := rt.Reloader(path, interval, logger)
	_ = r.Run(ctx)
}

// Reloader is the watcher Watch runs, exposed so a caller (or a test) can
// Trigger a reload.
func (rt *Runtime) Reloader(path string, interval time.Duration, logger *slog.Logger) *auth.Reloader {
	return &auth.Reloader{
		Path:     path,
		Interval: interval,
		Logger:   logger,
		Load: func(context.Context) error {
			// config.Load treats a missing file as "defaults". A reload must
			// not: a deleted config would silently drop the JWT verifier.
			if _, err := os.Stat(path); err != nil {
				return fmt.Errorf("stat %s: %w", path, err)
			}
			cfg, err := config.Load(path)
			if err != nil {
				return fmt.Errorf("load %s: %w", path, err)
			}
			return rt.Reload(cfg.Auth)
		},
	}
}
