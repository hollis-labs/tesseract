package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hollis-labs/go-apppaths/paths"
	"github.com/hollis-labs/tesseract/internal/config"
	"github.com/hollis-labs/tesseract/internal/contextstore"
)

// hermeticLayout isolates the daemon's go-apppaths resolution into a per-test
// temp directory by pinning all four $XDG_*_HOME roots. Without this, a
// run()/runServe-based test would resolve the real
// ~/.local/state/tesseract/queue.db and collide with the live daemon and the
// per-agent MCP servers. CW-20260517-0066.
func hermeticLayout(t *testing.T) paths.Layout {
	t.Helper()
	base := t.TempDir()
	t.Setenv("XDG_DATA_HOME", filepath.Join(base, "data"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(base, "state"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(base, "cache"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(base, "config"))
	layout, err := config.ResolveLayout()
	if err != nil {
		t.Fatalf("resolve hermetic layout: %v", err)
	}
	return layout
}

func TestParseServeArgsDefaults(t *testing.T) {
	cfg, err := parseServeArgs(nil)
	if err != nil {
		t.Fatalf("parse defaults: %v", err)
	}
	if cfg.Addr != defaultServeAddr || cfg.ManagedAuth || cfg.StaticToken != "" || cfg.EnableMetrics || cfg.EnableRequestLogs {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
	if cfg.AllowUnauthenticatedRemote {
		t.Fatalf("unauthenticated remote must be opt-in, got %+v", cfg)
	}
	// The default must bind loopback only — an unauthenticated daemon on all
	// interfaces is the failure this default exists to prevent.
	if !isLoopbackAddr(cfg.Addr) {
		t.Fatalf("default addr %q is not loopback", cfg.Addr)
	}
	if cfg.RequestLogMode != "redacted" {
		t.Fatalf("unexpected default request log mode: %q", cfg.RequestLogMode)
	}
	if cfg.ShutdownTimeout != 5*time.Second {
		t.Fatalf("unexpected default shutdown timeout: %s", cfg.ShutdownTimeout)
	}
}

func TestParseServeArgsMetricsFlag(t *testing.T) {
	cfg, err := parseServeArgs([]string{"--metrics"})
	if err != nil {
		t.Fatalf("parse metrics flag: %v", err)
	}
	if !cfg.EnableMetrics {
		t.Fatalf("expected metrics to be enabled")
	}
}

func TestParseServeArgsShutdownTimeoutFlag(t *testing.T) {
	cfg, err := parseServeArgs([]string{"--shutdown-timeout", "2s"})
	if err != nil {
		t.Fatalf("parse shutdown-timeout: %v", err)
	}
	if cfg.ShutdownTimeout != 2*time.Second {
		t.Fatalf("unexpected shutdown-timeout: %s", cfg.ShutdownTimeout)
	}
}

func TestParseServeArgsRequestLogsFlag(t *testing.T) {
	cfg, err := parseServeArgs([]string{"--request-logs"})
	if err != nil {
		t.Fatalf("parse request-logs: %v", err)
	}
	if !cfg.EnableRequestLogs {
		t.Fatalf("expected request logs to be enabled")
	}
}

func TestParseServeArgsRequestLogModeValidation(t *testing.T) {
	cfg, err := parseServeArgs([]string{"--request-log-mode", "full"})
	if err != nil {
		t.Fatalf("parse request-log-mode full: %v", err)
	}
	if cfg.RequestLogMode != "full" {
		t.Fatalf("expected full mode, got %q", cfg.RequestLogMode)
	}
	if _, err := parseServeArgs([]string{"--request-log-mode", "invalid"}); err == nil {
		t.Fatalf("expected invalid mode error")
	}
}

func TestParseServeArgsMutuallyExclusiveAuthModes(t *testing.T) {
	_, err := parseServeArgs([]string{"--managed-auth", "--static-token", "abc"})
	if err == nil {
		t.Fatalf("expected auth mode conflict error")
	}
}

func TestRunServeFlagValidationPath(t *testing.T) {
	hermeticLayout(t)

	stdout, err := os.CreateTemp(t.TempDir(), "stdout-*.log")
	if err != nil {
		t.Fatalf("create stdout temp: %v", err)
	}
	defer stdout.Close()
	stderr, err := os.CreateTemp(t.TempDir(), "stderr-*.log")
	if err != nil {
		t.Fatalf("create stderr temp: %v", err)
	}
	defer stderr.Close()

	code := run(context.Background(), []string{"serve", "--managed-auth", "--static-token", "x"}, stdout, stderr)
	if code == 0 {
		t.Fatalf("expected non-zero code for invalid args")
	}
	if _, err := stderr.Seek(0, 0); err != nil {
		t.Fatalf("seek stderr: %v", err)
	}
	data := &bytes.Buffer{}
	if _, err := data.ReadFrom(stderr); err != nil {
		t.Fatalf("read stderr: %v", err)
	}
	if data.Len() == 0 {
		t.Fatalf("expected stderr output for invalid args")
	}
}

func TestRunServeManagedAuthRequiresActiveToken(t *testing.T) {
	hermeticLayout(t)

	stdout, err := os.CreateTemp(t.TempDir(), "stdout-*.log")
	if err != nil {
		t.Fatalf("create stdout temp: %v", err)
	}
	defer stdout.Close()
	stderr, err := os.CreateTemp(t.TempDir(), "stderr-*.log")
	if err != nil {
		t.Fatalf("create stderr temp: %v", err)
	}
	defer stderr.Close()

	code := run(context.Background(), []string{"serve", "--managed-auth", "--addr", "127.0.0.1:0"}, stdout, stderr)
	if code == 0 {
		t.Fatalf("expected non-zero when no active tokens")
	}
	if _, err := stderr.Seek(0, 0); err != nil {
		t.Fatalf("seek stderr: %v", err)
	}
	buf := &bytes.Buffer{}
	if _, err := buf.ReadFrom(stderr); err != nil {
		t.Fatalf("read stderr: %v", err)
	}
	if !bytes.Contains(buf.Bytes(), []byte("requires at least one active token")) {
		t.Fatalf("unexpected stderr: %s", buf.String())
	}
}

func TestRunServeGracefulShutdownOnContextCancel(t *testing.T) {
	layout := hermeticLayout(t)
	s, err := contextstore.Open(context.Background(), contextstore.Config{
		RootDir:    layout.DataDir(),
		RecordsDir: filepath.Join(layout.StateDir(), "records"),
		DBPath:     layout.MainDB(),
	})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()

	stderr, err := os.CreateTemp(t.TempDir(), "stderr-*.log")
	if err != nil {
		t.Fatalf("create stderr temp: %v", err)
	}
	defer stderr.Close()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() {
		done <- runServe(ctx, s, stderr, serveConfig{
			Addr:            "127.0.0.1:0",
			ShutdownTimeout: 2 * time.Second,
		}, layout, config.Defaults())
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("expected graceful shutdown code 0, got %d", code)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for graceful shutdown")
	}

	if _, err := stderr.Seek(0, 0); err != nil {
		t.Fatalf("seek stderr: %v", err)
	}
	buf := &bytes.Buffer{}
	if _, err := buf.ReadFrom(stderr); err != nil {
		t.Fatalf("read stderr: %v", err)
	}
	if !bytes.Contains(buf.Bytes(), []byte("shutdown complete")) {
		t.Fatalf("expected shutdown log, got: %s", buf.String())
	}
}

func TestHasActiveAuthTokensFromMetadata(t *testing.T) {
	root := t.TempDir()
	s, err := contextstore.Open(context.Background(), contextstore.Config{
		RootDir:    root,
		RecordsDir: filepath.Join(root, "data", "records"),
		DBPath:     filepath.Join(root, "data", "index", "context.db"),
	})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()
	if ok, err := s.HasActiveAuthTokens(context.Background()); err != nil || ok {
		t.Fatalf("expected no active tokens, got ok=%v err=%v", ok, err)
	}
	token, _, err := s.IssueAuthToken(context.Background(), "admin", 0)
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}
	if ok, err := s.HasActiveAuthTokens(context.Background()); err != nil || !ok {
		t.Fatalf("expected active token, got ok=%v err=%v", ok, err)
	}
	if err := s.RevokeAuthToken(context.Background(), token); err != nil {
		t.Fatalf("revoke token: %v", err)
	}
	if ok, err := s.HasActiveAuthTokens(context.Background()); err != nil || ok {
		t.Fatalf("expected no active tokens after revoke, got ok=%v err=%v", ok, err)
	}
}
