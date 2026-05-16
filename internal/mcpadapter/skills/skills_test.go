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
	if !strings.Contains(body, "Tesseract") {
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

func TestList_HasExpectedCount(t *testing.T) {
	const expected = 11 // start-here + 4 primitives + 4 domain skills + 2 feature skills (context-packet, audit)
	got, err := List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != expected {
		names := make([]string, len(got))
		for i, m := range got {
			names[i] = m.Name
		}
		t.Errorf("skill count = %d, want %d. Skills: %v", len(got), expected, names)
	}
}
