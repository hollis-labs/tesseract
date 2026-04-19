package skills

import (
	"errors"
	"strings"
	"testing"
)

func TestList_ReturnsStartHereFirst(t *testing.T) {
	got, err := List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("List returned 0 entries; expected at least start-here")
	}
	if got[0].Name != "start-here" {
		t.Fatalf("first skill = %q, want %q", got[0].Name, "start-here")
	}
	if got[0].Description == "" {
		t.Error("start-here description is empty")
	}
}

func TestGet_ReturnsStartHereBody(t *testing.T) {
	body, err := Get("start-here")
	if err != nil {
		t.Fatalf("Get(start-here): %v", err)
	}
	if !strings.Contains(body, "Vanta") {
		t.Errorf("start-here body missing expected content; got %q", body)
	}
}

func TestGet_UnknownSkill_ReturnsTypedError(t *testing.T) {
	_, err := Get("does-not-exist")
	if !errors.Is(err, ErrSkillNotFound) {
		t.Fatalf("Get(missing) err = %v, want ErrSkillNotFound", err)
	}
	if !strings.Contains(err.Error(), "start-here") {
		t.Errorf("error message should list available skills; got %q", err.Error())
	}
}
