package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	secrets "github.com/hollis-labs/go-secrets"
	"github.com/hollis-labs/tesseract/internal/config"
)

// putSecret writes value into the managed go-secrets store under the test's
// XDG_STATE_HOME, the same way `secrets set` would.
func putSecret(t *testing.T, ref, value string) {
	t.Helper()
	if os.Getenv("XDG_STATE_HOME") == "" {
		t.Fatal("XDG_STATE_HOME must be a test directory")
	}
	if err := secrets.New().Set(context.Background(), ref, strings.NewReader(value)); err != nil {
		t.Fatalf("put %s: %v", ref, err)
	}
}

func isolatedState(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
}

func TestMCPTokenReferences(t *testing.T) {
	isolatedState(t)
	putSecret(t, "file://tesseract/mcp-token", "ref-token")
	t.Setenv("TESSERACT_MCP_TOKEN", "")

	got, err := parseMCPArgs([]string{"--token-file", "file://tesseract/mcp-token"})
	if err != nil || got != "ref-token" {
		t.Fatalf("token-file reference: err=%v ok=%v", err, got == "ref-token")
	}
	t.Setenv("TESSERACT_MCP_TOKEN", "file://tesseract/mcp-token")
	if got, err = parseMCPArgs(nil); err != nil || got != "ref-token" {
		t.Fatalf("env reference: err=%v", err)
	}
	t.Setenv("TESSERACT_MCP_TOKEN", "")
	if got, err = parseMCPArgs([]string{"--token", "file://tesseract/mcp-token"}); err != nil || got != "ref-token" {
		t.Fatalf("flag reference: err=%v", err)
	}
	// file-first still wins over a reference in the lower sources.
	f := filepath.Join(t.TempDir(), "tok")
	if err := os.WriteFile(f, []byte("file-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TESSERACT_MCP_TOKEN", "file://tesseract/mcp-token")
	if got, _ = parseMCPArgs([]string{"--token-file", f}); got != "file-token" {
		t.Fatal("token-file did not take precedence")
	}
}

func TestMCPTokenMissingReferenceFailsClosed(t *testing.T) {
	isolatedState(t)
	for name, c := range map[string]struct {
		env  string
		args []string
	}{
		"token-file ref": {args: []string{"--token-file", "file://tesseract/absent"}},
		"env ref":        {env: "file://tesseract/absent"},
		"flag ref":       {args: []string{"--token", "file://tesseract/absent"}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("TESSERACT_MCP_TOKEN", c.env)
			got, err := parseMCPArgs(c.args)
			if err == nil || got != "" {
				t.Fatalf("expected fail-closed error, got token=%q err=%v", got, err)
			}
		})
	}
}

func TestMCPTokenFileRejectsLooseMode(t *testing.T) {
	f := filepath.Join(t.TempDir(), "tok")
	if err := os.WriteFile(f, []byte("t"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TESSERACT_MCP_TOKEN", "")
	if _, err := parseMCPArgs([]string{"--token-file", f}); err == nil {
		t.Fatal("0644 token file accepted")
	}
}

func TestStaticTokenReference(t *testing.T) {
	isolatedState(t)
	putSecret(t, "file://tesseract/static-token", "static-ref-token")
	cfg, err := parseServeArgs([]string{"--static-token", "file://tesseract/static-token"})
	if err != nil || cfg.StaticToken != "static-ref-token" {
		t.Fatalf("static token reference: err=%v", err)
	}
	if _, err := parseServeArgs([]string{"--static-token", "file://tesseract/absent"}); err == nil {
		t.Fatal("missing static-token reference did not fail closed")
	}
}

func TestEmbedderKeyResolution(t *testing.T) {
	isolatedState(t)
	cfg := config.Defaults()

	// Default reference absent: documented degrade, no error.
	if e, err := createEmbedder(cfg); e != nil || err != nil {
		t.Fatalf("absent default key: embedder=%v err=%v", e != nil, err)
	}
	// Explicit reference absent: fail closed.
	cfg.Embedding.APIKeyRef = "file://tesseract/absent"
	if e, err := createEmbedder(cfg); e != nil || err == nil {
		t.Fatalf("absent explicit key: embedder=%v err=%v", e != nil, err)
	}
	// A literal pasted into api_key_ref is rejected without echoing it.
	cfg.Embedding.APIKeyRef = "sk-literal-should-never-appear"
	_, err := createEmbedder(cfg)
	if err == nil || strings.Contains(err.Error(), "sk-literal") {
		t.Fatalf("literal in api_key_ref: err=%v", err)
	}
	// Present: embedder built, no env var involved.
	t.Setenv("OPENAI_API_KEY", "")
	putSecret(t, "file://tesseract/openai-api-key", "k")
	cfg.Embedding.APIKeyRef = ""
	if e, err := createEmbedder(cfg); e == nil || err != nil {
		t.Fatalf("present key: embedder=%v err=%v", e != nil, err)
	}
	// Environment alone does not supply a key.
	isolatedState(t)
	t.Setenv("OPENAI_API_KEY", "env-key-ignored")
	if e, _ := createEmbedder(config.Defaults()); e != nil {
		t.Fatal("OPENAI_API_KEY env var was honored")
	}
}

func TestSynthesisKeyResolution(t *testing.T) {
	isolatedState(t)
	cfg := config.Defaults()
	cfg.Synthesis.Provider = "anthropic"
	if p, err := createSynthesisProvider(cfg, os.Stderr); p != nil || err != nil {
		t.Fatalf("absent default: p=%v err=%v", p != nil, err)
	}
	cfg.Synthesis.APIKeyRef = "file://tesseract/absent"
	if _, err := createSynthesisProvider(cfg, os.Stderr); err == nil {
		t.Fatal("absent explicit synthesis ref did not fail closed")
	}
	cfg.Synthesis.APIKeyRef = ""
	putSecret(t, "file://tesseract/anthropic-api-key", "k")
	if p, err := createSynthesisProvider(cfg, os.Stderr); p == nil || err != nil {
		t.Fatalf("present: p=%v err=%v", p != nil, err)
	}
}
