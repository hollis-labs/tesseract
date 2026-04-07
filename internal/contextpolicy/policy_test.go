package contextpolicy

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRegisterGetAndValidatePayload(t *testing.T) {
	e := New()
	if err := e.RegisterNamespace("app/editor/session", "app", "editor", map[string]any{
		"required_keys": []any{"title", "summary"},
	}); err != nil {
		t.Fatalf("register: %v", err)
	}

	owner, ok := e.GetNamespace("app/editor/session")
	if !ok {
		t.Fatalf("expected namespace owner")
	}
	if owner.OwnerID != "editor" {
		t.Fatalf("unexpected owner: %+v", owner)
	}

	if err := e.ValidatePayload("app/editor/session", json.RawMessage(`{"title":"x","summary":"y"}`)); err != nil {
		t.Fatalf("expected valid payload, got %v", err)
	}
	if err := e.ValidatePayload("app/editor/session", json.RawMessage(`{"title":"x"}`)); err == nil {
		t.Fatalf("expected schema validation error")
	}
}

func TestTierPolicyAllowedOps(t *testing.T) {
	e := New()
	if err := e.RegisterNamespace("app/test/draft/x", "app", "test", map[string]any{
		"tier":         "draft",
		"allowed_ops":  []any{"promote.request"},
	}); err != nil {
		t.Fatalf("register: %v", err)
	}

	// "write" is NOT in allowed_ops — should be blocked.
	err := e.ValidateTierPolicy("app/test/draft/x", "write", 10, json.RawMessage(`{"v":1}`))
	if err == nil {
		t.Fatal("expected policy_violation for blocked write op")
	}
	if !strings.Contains(err.Error(), "allowed_ops") {
		t.Fatalf("expected allowed_ops error, got: %v", err)
	}

	// "promote.request" is allowed.
	if err := e.ValidateTierPolicy("app/test/draft/x", "promote.request", 10, nil); err != nil {
		t.Fatalf("expected promote.request allowed, got: %v", err)
	}
}

func TestTierPolicyMaxBytes(t *testing.T) {
	e := New()
	if err := e.RegisterNamespace("user/cache/small", "user", "user", map[string]any{
		"tier":             "cache",
		"max_bytes_per_key": 100,
	}); err != nil {
		t.Fatalf("register: %v", err)
	}

	smallPayload := json.RawMessage(`{"v":"small"}`)
	if err := e.ValidateTierPolicy("user/cache/small", "write", len(smallPayload), smallPayload); err != nil {
		t.Fatalf("expected small payload allowed, got: %v", err)
	}

	bigPayload := make([]byte, 200)
	for i := range bigPayload {
		bigPayload[i] = 'a'
	}
	// Wrap as JSON string to make it valid JSON.
	wrapped := json.RawMessage(`"` + string(bigPayload) + `"`)
	err := e.ValidateTierPolicy("user/cache/small", "write", len(wrapped), wrapped)
	if err == nil {
		t.Fatal("expected max_bytes_per_key violation")
	}
	if !strings.Contains(err.Error(), "max_bytes_per_key") {
		t.Fatalf("expected max_bytes_per_key error, got: %v", err)
	}
}

func TestTierPolicyRequiredSchemaKeys(t *testing.T) {
	e := New()
	if err := e.RegisterNamespace("user/memory/structured", "user", "user", map[string]any{
		"tier":                 "memory",
		"required_schema_keys": []any{"fact", "source"},
	}); err != nil {
		t.Fatalf("register: %v", err)
	}

	good := json.RawMessage(`{"fact":"x","source":"user"}`)
	if err := e.ValidateTierPolicy("user/memory/structured", "write", len(good), good); err != nil {
		t.Fatalf("expected valid, got: %v", err)
	}

	bad := json.RawMessage(`{"fact":"x"}`)
	err := e.ValidateTierPolicy("user/memory/structured", "write", len(bad), bad)
	if err == nil {
		t.Fatal("expected required_schema_keys violation")
	}
	if !strings.Contains(err.Error(), "required_schema_keys") {
		t.Fatalf("expected required_schema_keys error, got: %v", err)
	}
}

func TestTierPolicyEmptyAllowedOpsPermitsAll(t *testing.T) {
	e := New()
	// No tier policy at all — all ops permitted (backward compat).
	if err := e.RegisterNamespace("user/memory/plain", "user", "user", map[string]any{}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := e.ValidateTierPolicy("user/memory/plain", "write", 5, json.RawMessage(`{}`)); err != nil {
		t.Fatalf("expected write allowed with empty policy, got: %v", err)
	}
}
