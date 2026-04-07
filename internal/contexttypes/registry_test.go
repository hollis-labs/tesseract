package contexttypes

import (
	"testing"
)

func TestDefaultTypes(t *testing.T) {
	types := DefaultTypes()
	if len(types) < 8 {
		t.Fatalf("expected at least 8 core types, got %d", len(types))
	}
	// Verify key type IDs exist.
	wantIDs := []string{
		"strategy/goal", "strategy/constraints", "strategy/roadmap", "system/map",
		"task/spec", "runbook", "contract/api", "contract/data",
		"decision/adr", "brief/summary", "note/volatile", "principles",
		"session/snapshot", "config/service", "project/identity",
	}
	idSet := map[string]bool{}
	for _, ct := range types {
		idSet[ct.TypeID] = true
	}
	for _, id := range wantIDs {
		if !idSet[id] {
			t.Errorf("missing expected core type: %s", id)
		}
	}
}

func TestDefaultViews(t *testing.T) {
	views := DefaultViews()
	if len(views) < 4 {
		t.Fatalf("expected at least 4 views, got %d", len(views))
	}
	viewIDs := map[string]bool{}
	for _, v := range views {
		viewIDs[v.ViewID] = true
	}
	for _, id := range []string{"task_exec", "strategy", "agent_boot", "briefing"} {
		if !viewIDs[id] {
			t.Errorf("missing %s view", id)
		}
	}
}

func TestRegistryGetType(t *testing.T) {
	r := NewRegistry()
	ct, ok := r.GetType("task/spec")
	if !ok {
		t.Fatal("expected task/spec to be registered")
	}
	if ct.TypeID != "task/spec" {
		t.Fatalf("expected type_id task/spec, got %s", ct.TypeID)
	}
}

func TestRegistryGetView(t *testing.T) {
	r := NewRegistry()
	v, ok := r.GetView("task_exec")
	if !ok {
		t.Fatal("expected task_exec view to be registered")
	}
	if v.ViewID != "task_exec" {
		t.Fatalf("expected view_id task_exec, got %s", v.ViewID)
	}
	if len(v.Types) == 0 {
		t.Fatal("task_exec view should have types")
	}
}

func TestRegistryIsKnownType(t *testing.T) {
	r := NewRegistry()
	if !r.IsKnownType("decision/adr") {
		t.Error("decision/adr should be known")
	}
	if !r.IsKnownType("custom/my-app/widget") {
		t.Error("custom/* types should always be accepted")
	}
	if r.IsKnownType("unknown/thing") {
		t.Error("unknown/thing should not be known")
	}
}

func TestRegistryValidateType(t *testing.T) {
	r := NewRegistry()
	if err := r.ValidateType("task/spec"); err != nil {
		t.Errorf("task/spec should be valid: %v", err)
	}
	if err := r.ValidateType(""); err != nil {
		t.Errorf("empty type should be valid: %v", err)
	}
	if err := r.ValidateType("unknown/thing"); err == nil {
		t.Error("unknown type should fail validation")
	}
}

func TestRegistryValidateStatus(t *testing.T) {
	r := NewRegistry()
	if err := r.ValidateStatus("task/spec", "draft"); err != nil {
		t.Errorf("draft should be valid for task/spec: %v", err)
	}
	if err := r.ValidateStatus("note/volatile", "draft"); err != nil {
		t.Errorf("draft should be valid for note/volatile: %v", err)
	}
	if err := r.ValidateStatus("note/volatile", "canonical"); err == nil {
		t.Error("canonical should not be valid for note/volatile")
	}
	if err := r.ValidateStatus("", "draft"); err != nil {
		t.Errorf("draft should be valid when no type: %v", err)
	}
	if err := r.ValidateStatus("", "invalid_status"); err == nil {
		t.Error("invalid_status should fail")
	}
}

func TestIsValidStatus(t *testing.T) {
	for _, s := range []string{"draft", "reviewed", "canonical", "deprecated"} {
		if !IsValidStatus(s) {
			t.Errorf("%s should be valid", s)
		}
	}
	if IsValidStatus("bogus") {
		t.Error("bogus should not be valid")
	}
}

func TestNoteVolatileDefaultTTL(t *testing.T) {
	r := NewRegistry()
	ct, ok := r.GetType("note/volatile")
	if !ok {
		t.Fatal("note/volatile should exist")
	}
	ttl := ct.ParseDefaultTTL()
	if ttl <= 0 {
		t.Fatalf("note/volatile should have a positive default TTL, got %v", ttl)
	}
}

func TestDecisionADRPromotionRules(t *testing.T) {
	r := NewRegistry()
	ct, ok := r.GetType("decision/adr")
	if !ok {
		t.Fatal("decision/adr should exist")
	}
	if len(ct.PromotionRules) == 0 {
		t.Fatal("decision/adr should have promotion rules")
	}
}

func TestLoadFromBytes(t *testing.T) {
	r := NewRegistry()
	yamlData := []byte(`
types:
  - type_id: custom/test/widget
    default_ttl: "24h"
    allowed_statuses: ["draft", "reviewed"]
views:
  - view_id: test_view
    types: ["custom/test/widget"]
    max_items: 10
`)
	if err := r.LoadFromBytes(yamlData); err != nil {
		t.Fatalf("LoadFromBytes failed: %v", err)
	}
	ct, ok := r.GetType("custom/test/widget")
	if !ok {
		t.Fatal("custom/test/widget should be loaded")
	}
	if ct.DefaultTTL != "24h" {
		t.Errorf("expected default_ttl 24h, got %s", ct.DefaultTTL)
	}
	v, ok := r.GetView("test_view")
	if !ok {
		t.Fatal("test_view should be loaded")
	}
	if v.MaxItems != 10 {
		t.Errorf("expected max_items 10, got %d", v.MaxItems)
	}
	// Verify existing defaults still work.
	if _, ok := r.GetType("task/spec"); !ok {
		t.Error("default type task/spec should still be present")
	}
}

func TestListTypes(t *testing.T) {
	r := NewRegistry()
	types := r.ListTypes()
	if len(types) < 8 {
		t.Fatalf("expected at least 8 types, got %d", len(types))
	}
	// Verify sorted.
	for i := 1; i < len(types); i++ {
		if types[i].TypeID < types[i-1].TypeID {
			t.Fatalf("types not sorted: %s < %s", types[i].TypeID, types[i-1].TypeID)
		}
	}
}

func TestListViews(t *testing.T) {
	r := NewRegistry()
	views := r.ListViews()
	if len(views) < 4 {
		t.Fatalf("expected at least 4 views, got %d", len(views))
	}
	for i := 1; i < len(views); i++ {
		if views[i].ViewID < views[i-1].ViewID {
			t.Fatalf("views not sorted: %s < %s", views[i].ViewID, views[i-1].ViewID)
		}
	}
}

func TestValidateRequiredFields(t *testing.T) {
	r := NewRegistry()

	// task/spec requires "title"
	err := r.ValidateRequiredFields("task/spec", map[string]any{"title": "My Task"})
	if err != nil {
		t.Errorf("valid payload should pass: %v", err)
	}

	// Missing required field
	err = r.ValidateRequiredFields("task/spec", map[string]any{"description": "no title"})
	if err == nil {
		t.Error("missing title should fail")
	}

	// Empty string counts as missing
	err = r.ValidateRequiredFields("task/spec", map[string]any{"title": ""})
	if err == nil {
		t.Error("empty title should fail")
	}

	// custom/ types skip validation
	err = r.ValidateRequiredFields("custom/my-app", map[string]any{})
	if err != nil {
		t.Errorf("custom types should skip validation: %v", err)
	}

	// Empty type skips validation
	err = r.ValidateRequiredFields("", map[string]any{})
	if err != nil {
		t.Errorf("empty type should skip validation: %v", err)
	}

	// Type with no required fields
	err = r.ValidateRequiredFields("strategy/goal", map[string]any{})
	if err != nil {
		t.Errorf("type without required_fields should pass: %v", err)
	}
}

func TestContextTypeHasAllowedStatus(t *testing.T) {
	ct := ContextType{
		TypeID:          "test",
		AllowedStatuses: []string{"draft", "reviewed"},
	}
	if !ct.HasAllowedStatus("draft") {
		t.Error("draft should be allowed")
	}
	if ct.HasAllowedStatus("canonical") {
		t.Error("canonical should not be allowed")
	}

	// Empty allowed = all valid statuses permitted.
	ct2 := ContextType{TypeID: "open"}
	if !ct2.HasAllowedStatus("canonical") {
		t.Error("canonical should be allowed when AllowedStatuses is empty")
	}
}
