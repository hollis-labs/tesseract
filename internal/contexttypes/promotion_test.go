package contexttypes

import (
	"testing"
)

func TestCanTransition(t *testing.T) {
	cases := []struct {
		from, to string
		want     bool
	}{
		{"draft", "reviewed", true},
		{"draft", "deprecated", true},
		{"draft", "canonical", false},
		{"reviewed", "canonical", true},
		{"reviewed", "deprecated", true},
		{"reviewed", "draft", false},
		{"canonical", "deprecated", true},
		{"canonical", "draft", false},
		{"canonical", "reviewed", false},
		{"deprecated", "draft", false},
		{"deprecated", "reviewed", false},
		{"deprecated", "canonical", false},
	}
	for _, tc := range cases {
		got := CanTransition(tc.from, tc.to)
		if got != tc.want {
			t.Errorf("CanTransition(%s, %s) = %v, want %v", tc.from, tc.to, got, tc.want)
		}
	}
}

func TestValidateTransition(t *testing.T) {
	r := NewRegistry()

	// Basic valid transition.
	if err := r.ValidateTransition("task/spec", "draft", "reviewed", "user"); err != nil {
		t.Errorf("draft->reviewed should be valid: %v", err)
	}

	// Invalid transition.
	if err := r.ValidateTransition("task/spec", "draft", "canonical", "user"); err == nil {
		t.Error("draft->canonical should be invalid")
	}

	// note/volatile cannot go to reviewed.
	if err := r.ValidateTransition("note/volatile", "draft", "reviewed", "user"); err == nil {
		t.Error("note/volatile draft->reviewed should be invalid (status not allowed)")
	}

	// decision/adr requires human approval for draft->reviewed.
	if err := r.ValidateTransition("decision/adr", "draft", "reviewed", "app:carrier"); err == nil {
		t.Error("decision/adr draft->reviewed by non-user should fail")
	}
	if err := r.ValidateTransition("decision/adr", "draft", "reviewed", "user"); err != nil {
		t.Errorf("decision/adr draft->reviewed by user should succeed: %v", err)
	}
}

func TestRequiresHumanApproval(t *testing.T) {
	r := NewRegistry()
	if !r.RequiresHumanApproval("decision/adr", "draft", "reviewed") {
		t.Error("decision/adr draft->reviewed should require human approval")
	}
	if r.RequiresHumanApproval("task/spec", "draft", "reviewed") {
		t.Error("task/spec draft->reviewed should not require human approval")
	}
}

func TestIsPromotable(t *testing.T) {
	if !IsPromotable("draft") {
		t.Error("draft should be promotable")
	}
	if !IsPromotable("reviewed") {
		t.Error("reviewed should be promotable")
	}
	if IsPromotable("canonical") {
		t.Error("canonical should not be promotable")
	}
	if IsPromotable("deprecated") {
		t.Error("deprecated should not be promotable")
	}
}

func TestNextPromotionStatus(t *testing.T) {
	if s := NextPromotionStatus("draft"); s != "reviewed" {
		t.Errorf("draft next: want reviewed, got %s", s)
	}
	if s := NextPromotionStatus("reviewed"); s != "canonical" {
		t.Errorf("reviewed next: want canonical, got %s", s)
	}
	if s := NextPromotionStatus("canonical"); s != "" {
		t.Errorf("canonical next: want empty, got %s", s)
	}
}
