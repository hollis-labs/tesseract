package credentials

import (
	"context"
	"errors"
	"strings"
	"testing"

	secrets "github.com/hollis-labs/go-secrets"
)

func state(t *testing.T) Resolver {
	t.Helper()
	return secrets.New(secrets.WithStateDir(t.TempDir()))
}

func put(t *testing.T, r Resolver, ref, v string) {
	t.Helper()
	if err := r.(*secrets.Resolver).Set(context.Background(), ref, strings.NewReader(v)); err != nil {
		t.Fatal(err)
	}
}

func TestProviderKeyContract(t *testing.T) {
	ctx := context.Background()
	r := state(t)
	if k, err := ProviderKey(ctx, r, "openai", ""); k != "" || err != nil {
		t.Fatalf("default absent should degrade: %q %v", k, err)
	}
	if _, err := ProviderKey(ctx, r, "openai", "file://x/y"); !errors.Is(err, secrets.ErrNotFound) {
		t.Fatalf("explicit absent should fail closed: %v", err)
	}
	put(t, r, DefaultOpenAIRef, "v1")
	if k, err := ProviderKey(ctx, r, "OpenAI", ""); k != "v1" || err != nil {
		t.Fatalf("default present: %v", err)
	}
	if k, err := ProviderKey(ctx, r, "unknown", ""); k != "" || err != nil {
		t.Fatalf("keyless provider: %v", err)
	}
	if _, err := ProviderKey(ctx, r, "openai", "env://OPENAI_API_KEY"); err == nil {
		t.Fatal("env:// must not resolve without WithEnv")
	}
}

func TestTokenLiteralVersusReference(t *testing.T) {
	ctx := context.Background()
	r := state(t)
	if v, isRef, err := Token(ctx, r, " lit \n"); v != "lit" || isRef || err != nil {
		t.Fatalf("literal: %q %v %v", v, isRef, err)
	}
	put(t, r, "file://a/b", "from-ref")
	if v, isRef, err := Token(ctx, r, "file://a/b"); v != "from-ref" || !isRef || err != nil {
		t.Fatalf("ref: %v %v", isRef, err)
	}
	if _, isRef, err := Token(ctx, r, "file://a/missing"); !isRef || err == nil {
		t.Fatal("missing ref must error")
	}
}

func TestErrorsNeverRenderValue(t *testing.T) {
	r := state(t)
	_, err := ProviderKey(context.Background(), r, "openai", "sk-secret-literal")
	if err == nil || strings.Contains(err.Error(), "sk-secret-literal") {
		t.Fatalf("err=%v", err)
	}
}
