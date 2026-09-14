package contextpolicy

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestParseWorkspaceRetentionPolicy(t *testing.T) {
	policy, present, err := ParseWorkspaceRetentionPolicy(map[string]any{
		"retention":           "24h",
		"workspace_retention": map[string]any{"purge_enabled": false, "minimum_idle": "1440h"},
	})
	if err != nil || !present {
		t.Fatalf("parse = %#v present=%t err=%v", policy, present, err)
	}
	if policy.PurgeEnabled == nil || *policy.PurgeEnabled || policy.MinimumIdle != 60*24*time.Hour {
		t.Fatalf("parsed policy = %#v", policy)
	}
	if _, present, err := ParseWorkspaceRetentionPolicy(map[string]any{"retention": "1h"}); err != nil || present {
		t.Fatalf("generic retention became workspace policy: present=%t err=%v", present, err)
	}
	for _, raw := range []map[string]any{
		{"workspace_retention": "30d"},
		{"workspace_retention": map[string]any{"minimum_idle": "24h"}},
		{"workspace_retention": map[string]any{"purge_enabled": "false"}},
		{"workspace_retention": map[string]any{"unknown": true}},
	} {
		if _, _, err := ParseWorkspaceRetentionPolicy(raw); err == nil {
			t.Fatalf("invalid policy accepted: %#v", raw)
		}
	}
}

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
		"tier":        "draft",
		"allowed_ops": []any{"promote.request"},
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
		"tier":              "cache",
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

func TestTierPolicyRequiredSchemaKeys_EmptyPayload(t *testing.T) {
	e := New()
	if err := e.RegisterNamespace("user/memory/structured", "user", "user", map[string]any{
		"tier":                 "memory",
		"required_schema_keys": []any{"fact", "source"},
	}); err != nil {
		t.Fatalf("register: %v", err)
	}

	for _, payload := range []json.RawMessage{nil, []byte{}, []byte(""), []byte("null")} {
		err := e.ValidateTierPolicy("user/memory/structured", "write", 0, payload)
		if err == nil {
			t.Fatalf("expected required_schema_keys violation for empty payload %q", string(payload))
		}
		if !strings.Contains(err.Error(), "required_schema_keys") {
			t.Fatalf("expected required_schema_keys error, got: %v", err)
		}
	}
}

func TestTierPolicy_ScopeHeadFallback(t *testing.T) {
	e := New()
	// Declare policy on scope head project/torque.
	if err := e.RegisterNamespace("project/torque", "project", "torque", map[string]any{
		"tier":              "project",
		"max_bytes_per_key": 50,
	}); err != nil {
		t.Fatalf("register head: %v", err)
	}

	// Exact child namespace registered with inferred policy.
	if err := e.RegisterNamespace("project/torque/memory/notes", "project", "torque", map[string]any{
		"source": "inferred",
	}); err != nil {
		t.Fatalf("register child: %v", err)
	}

	// Policy should be inherited from project/torque.
	small := json.RawMessage(`{"v":"ok"}`)
	if err := e.ValidateTierPolicy("project/torque/memory/notes", "write", len(small), small); err != nil {
		t.Fatalf("expected small write allowed, got: %v", err)
	}

	err := e.ValidateTierPolicy("project/torque/memory/notes", "write", 100, json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected max_bytes_per_key violation via inherited scope head policy")
	}
	if !strings.Contains(err.Error(), "max_bytes_per_key") {
		t.Fatalf("expected max_bytes_per_key error, got: %v", err)
	}
}

func TestExtractScopeHead(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"user/chrispian", "user/chrispian"},
		{"user/chrispian/memory/notes", "user/chrispian"},
		{"project/tesseract", "project/tesseract"},
		{"project/tesseract/knowledge/arch", "project/tesseract"},
		{"app/agent-1/state", "app/agent-1"},
		{"org/hollis-labs/team", "org/hollis-labs"},
		{"session/s-123/context", "session/s-123"},
		{"system", "system"},
		{"system/audit", "system"},
		{"unknown/foo", ""},
		{"single", ""},
		{"", ""},
		{"   ", ""},
	}
	for _, tt := range tests {
		got := ExtractScopeHead(tt.input)
		if got != tt.want {
			t.Errorf("ExtractScopeHead(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestCanWrite_UserScopeRejectionTeachesCorrectScope(t *testing.T) {
	e := New()
	err := e.CanWrite("agent-1", "agent", "user/chrispian/memory/notes")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var spv *ScopePolicyViolation
	if !errors.As(err, &spv) {
		t.Fatalf("expected *ScopePolicyViolation, got %T: %v", err, err)
	}
	msg := err.Error()
	if !strings.Contains(msg, "writes to protected namespace \"user/chrispian/memory/notes\" require actor=user") {
		t.Errorf("expected attempted namespace in error, got: %s", msg)
	}
	if !strings.Contains(msg, `attempted scope "user/chrispian" with actor "agent"`) {
		t.Errorf("expected attempted scope and actor in error, got: %s", msg)
	}
	if !strings.Contains(msg, "project/{slug}") {
		t.Errorf("expected project/{slug} guidance in error, got: %s", msg)
	}
	if !strings.Contains(msg, "system") {
		t.Errorf("expected system guidance in error, got: %s", msg)
	}
	if !strings.Contains(msg, "tesseract_skills namespaces") {
		t.Errorf("expected tesseract_skills namespaces in error, got: %s", msg)
	}
}

func TestCanWrite_RegisteredUserScopeRejectionTeachesCorrectScope(t *testing.T) {
	e := New()
	if err := e.RegisterNamespace("user/chrispian", "user", "chrispian", map[string]any{"tier": "memory"}); err != nil {
		t.Fatalf("RegisterNamespace: %v", err)
	}
	err := e.CanWrite("agent-1", "agent", "user/chrispian/memory/notes")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "tesseract_skills namespaces") {
		t.Errorf("expected teaching error, got: %s", err.Error())
	}
	// user actor succeeds
	if err := e.CanWrite("", "user", "user/chrispian/memory/notes"); err != nil {
		t.Errorf("expected user actor allowed on registered user namespace, got: %v", err)
	}
}

func TestCanWrite_RegisteredProjectScopePermitsAgent(t *testing.T) {
	e := New()
	if err := e.RegisterNamespace("project/tesseract", "project", "tesseract", map[string]any{"tier": "memory"}); err != nil {
		t.Fatalf("RegisterNamespace: %v", err)
	}
	if err := e.CanWrite("agent-1", "agent", "project/tesseract/memory/notes"); err != nil {
		t.Errorf("expected agent allowed on registered project namespace, got: %v", err)
	}
}
