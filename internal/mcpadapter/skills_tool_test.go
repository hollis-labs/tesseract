package mcpadapter

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestTesseractSkills_Index(t *testing.T) {
	a := New(newTestStore(t), "")
	res, err := a.handleTesseractSkills(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("handleTesseractSkills: %v", err)
	}
	if res == nil {
		t.Fatal("empty result")
	}
	var arr []map[string]any
	text := mustJSONText(t, res)
	if err := json.Unmarshal([]byte(text), &arr); err != nil {
		t.Fatalf("unmarshal %q: %v", text, err)
	}
	if len(arr) == 0 {
		t.Fatal("index had 0 entries")
	}
	if arr[0]["name"] != "start-here" {
		t.Errorf("first entry name = %v, want start-here", arr[0]["name"])
	}
}

func TestTesseractSkills_GetByName(t *testing.T) {
	a := New(newTestStore(t), "")
	res, err := a.handleTesseractSkills(context.Background(), map[string]any{"name": "start-here"})
	if err != nil {
		t.Fatalf("handleTesseractSkills: %v", err)
	}
	// handleTesseractSkills returns the skill's markdown body verbatim as a
	// string for a named lookup -- go-mcp's ToolHandler contract treats a
	// string result as text content, not something to JSON-marshal.
	body, ok := res.(string)
	if !ok {
		t.Fatalf("want a string result, got %T", res)
	}
	if !strings.Contains(body, "Tesseract") {
		t.Errorf("body missing expected content")
	}
}

func TestTesseractSkills_UnknownName_ReturnsToolError(t *testing.T) {
	a := New(newTestStore(t), "")
	res, err := a.handleTesseractSkills(context.Background(), map[string]any{"name": "does-not-exist"})
	if err != nil {
		t.Fatalf("handler returned err: %v", err)
	}
	m := parseResult(t, res)
	if m["code"] != "skill_not_found" {
		t.Errorf("code = %v, want skill_not_found", m["code"])
	}
	msg, _ := m["message"].(string)
	if !strings.Contains(msg, "start-here") {
		t.Errorf("error message should list available skills; got %q", msg)
	}
}
