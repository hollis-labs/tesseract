package typeregistry_test

import (
	"testing"

	"github.com/hollis-labs/tesseract/internal/typeregistry"
)

// The context-record surface, carried over from internal/contexttypes.
//
// These assert the behavior that had to survive the promotion unchanged. The
// two exceptions are stated where they are: the dropped rank bias, and the
// dropped promotion rule.

func TestContextTypesArePresent(t *testing.T) {
	r := typeregistry.NewRegistry()
	for _, id := range []string{
		"strategy/goal", "strategy/constraints", "strategy/roadmap", "system/map",
		"task/spec", "runbook", "contract/api", "contract/data",
		"decision/adr", "brief/summary", "note/volatile", "principles",
		"session/snapshot", "config/service", "project/identity",
	} {
		if _, ok := r.ContextType(id); !ok {
			t.Errorf("missing core context type %q", id)
		}
	}
	if got := len(r.ListContextTypes()); got != 15 {
		t.Errorf("context types = %d, want 15", got)
	}
}

func TestContextViewsArePresent(t *testing.T) {
	r := typeregistry.NewRegistry()
	for _, id := range []string{"task_exec", "strategy", "agent_boot", "briefing"} {
		v, ok := r.GetView(id)
		if !ok {
			t.Errorf("missing view %q", id)
			continue
		}
		if len(v.Types) == 0 {
			t.Errorf("view %q has no types", id)
		}
	}
	views := r.ListViews()
	for i := 1; i < len(views); i++ {
		if views[i].ViewID < views[i-1].ViewID {
			t.Fatalf("views not sorted: %s < %s", views[i].ViewID, views[i-1].ViewID)
		}
	}
}

// TestCustomPrefixStaysOpen is the reason context.record_type declares
// OpenPrefixes rather than closed:false. The vocabulary rejects an
// unregistered type but has always accepted anything under `custom/`, and
// modeling that as an open vocabulary would have quietly accepted everything
// — a behavior change hiding inside a move.
func TestCustomPrefixStaysOpen(t *testing.T) {
	r := typeregistry.NewRegistry()
	if !r.IsKnownContextType("decision/adr") {
		t.Error("decision/adr should be known")
	}
	if !r.IsKnownContextType("custom/my-app/widget") {
		t.Error("custom/* should always be accepted")
	}
	if r.IsKnownContextType("unknown/thing") {
		t.Error("unknown/thing should not be known")
	}
	if r.IsKnownContextType("") {
		t.Error("the empty type should not be known")
	}
}

func TestValidateContextType(t *testing.T) {
	r := typeregistry.NewRegistry()
	if err := r.ValidateContextType("task/spec"); err != nil {
		t.Errorf("task/spec should be valid: %v", err)
	}
	if err := r.ValidateContextType(""); err != nil {
		t.Errorf("record_type is optional: %v", err)
	}
	if err := r.ValidateContextType("unknown/thing"); err == nil {
		t.Error("unknown type should fail validation")
	}
}

func TestValidateContextStatus(t *testing.T) {
	r := typeregistry.NewRegistry()
	if err := r.ValidateContextStatus("task/spec", "draft"); err != nil {
		t.Errorf("draft should be valid for task/spec: %v", err)
	}
	if err := r.ValidateContextStatus("note/volatile", "draft"); err != nil {
		t.Errorf("draft should be valid for note/volatile: %v", err)
	}
	if err := r.ValidateContextStatus("note/volatile", "canonical"); err == nil {
		t.Error("note/volatile declares draft only; canonical should fail")
	}
	if err := r.ValidateContextStatus("", "draft"); err != nil {
		t.Errorf("draft should be valid with no type: %v", err)
	}
	if err := r.ValidateContextStatus("", "invalid_status"); err == nil {
		t.Error("invalid_status should fail")
	}
}

func TestIsValidStatus(t *testing.T) {
	for _, s := range []string{"draft", "reviewed", "canonical", "deprecated"} {
		if !typeregistry.IsValidStatus(s) {
			t.Errorf("%s should be valid", s)
		}
	}
	if typeregistry.IsValidStatus("bogus") {
		t.Error("bogus should not be valid")
	}
}

func TestNoteVolatileKeepsItsDefaultTTL(t *testing.T) {
	r := typeregistry.NewRegistry()
	ct, ok := r.ContextType("note/volatile")
	if !ok {
		t.Fatal("note/volatile should exist")
	}
	if ct.ParseDefaultTTL() <= 0 {
		t.Fatalf("note/volatile default TTL = %v, want positive", ct.ParseDefaultTTL())
	}
}

func TestValidateContextRequiredFields(t *testing.T) {
	r := typeregistry.NewRegistry()

	if err := r.ValidateContextRequiredFields("task/spec", map[string]any{"title": "My Task"}); err != nil {
		t.Errorf("valid payload should pass: %v", err)
	}
	if err := r.ValidateContextRequiredFields("task/spec", map[string]any{"description": "no title"}); err == nil {
		t.Error("missing title should fail")
	}
	if err := r.ValidateContextRequiredFields("task/spec", map[string]any{"title": ""}); err == nil {
		t.Error("an empty title counts as missing")
	}
	if err := r.ValidateContextRequiredFields("custom/my-app", map[string]any{}); err != nil {
		t.Errorf("custom types declare nothing to require: %v", err)
	}
	if err := r.ValidateContextRequiredFields("", map[string]any{}); err != nil {
		t.Errorf("empty type should skip validation: %v", err)
	}
	if err := r.ValidateContextRequiredFields("strategy/goal", map[string]any{}); err != nil {
		t.Errorf("a type with no required_fields should pass: %v", err)
	}
}

// ---- Status lifecycle ----------------------------------------------------

func TestCanTransition(t *testing.T) {
	for _, tc := range []struct {
		from, to string
		want     bool
	}{
		{"draft", "reviewed", true},
		{"draft", "deprecated", true},
		{"draft", "canonical", false},
		{"reviewed", "canonical", true},
		{"canonical", "deprecated", true},
		{"deprecated", "draft", false},
		{"bogus", "draft", false},
	} {
		if got := typeregistry.CanTransition(tc.from, tc.to); got != tc.want {
			t.Errorf("CanTransition(%s, %s) = %v, want %v", tc.from, tc.to, got, tc.want)
		}
	}
}

func TestValidateContextTransition(t *testing.T) {
	r := typeregistry.NewRegistry()
	if err := r.ValidateContextTransition("task/spec", "draft", "reviewed"); err != nil {
		t.Errorf("draft -> reviewed should be allowed: %v", err)
	}
	if err := r.ValidateContextTransition("task/spec", "draft", "canonical"); err == nil {
		t.Error("draft -> canonical skips a step and should fail")
	}
	if err := r.ValidateContextTransition("note/volatile", "draft", "reviewed"); err == nil {
		t.Error("note/volatile allows draft only; the move should fail")
	}
	if err := r.ValidateContextTransition("task/spec", "draft", ""); err == nil {
		t.Error("an empty target status should fail")
	}
	if err := r.ValidateContextTransition("task/spec", "", "reviewed"); err != nil {
		t.Errorf("an empty current status means draft: %v", err)
	}
}

// TestPromotionNoLongerGatesOnActor records the one behavior change on this
// surface, so it reads as a decision rather than an oversight.
//
// decision/adr used to declare "draft->reviewed:requires_human_approval", and
// ValidateTransition refused the move for a non-user actor. promotion_rules
// left with the field set ([[tesseract_type_declaration_field_set]]), so the
// move is now allowed for any caller.
//
// The guard was already vacuous: every promote path — HTTP, MCP and CLI —
// defaults actor to "user" when the caller omits it, so it only ever fired for
// a caller that volunteered a non-user actor. It gated honesty, not authority.
func TestPromotionNoLongerGatesOnActor(t *testing.T) {
	r := typeregistry.NewRegistry()
	if err := r.ValidateContextTransition("decision/adr", "draft", "reviewed"); err != nil {
		t.Fatalf("decision/adr draft -> reviewed: %v", err)
	}
	ct, ok := r.ContextType("decision/adr")
	if !ok {
		t.Fatal("decision/adr should exist")
	}
	if ct.ParseDefaultTTL() != 0 {
		t.Errorf("decision/adr picked up a TTL it never had: %v", ct.ParseDefaultTTL())
	}
}

func TestPromotionChain(t *testing.T) {
	if !typeregistry.IsPromotable("draft") || !typeregistry.IsPromotable("reviewed") {
		t.Error("draft and reviewed should be promotable")
	}
	if typeregistry.IsPromotable("canonical") || typeregistry.IsPromotable("deprecated") {
		t.Error("canonical and deprecated should not be promotable")
	}
	if got := typeregistry.NextPromotionStatus("draft"); got != "reviewed" {
		t.Errorf("next(draft) = %q, want reviewed", got)
	}
	if got := typeregistry.NextPromotionStatus("reviewed"); got != "canonical" {
		t.Errorf("next(reviewed) = %q, want canonical", got)
	}
	if got := typeregistry.NextPromotionStatus("canonical"); got != "" {
		t.Errorf("next(canonical) = %q, want empty", got)
	}
}
