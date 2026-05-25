package mcpadapter

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func TestTesseractSkills_Index(t *testing.T) {
	a := New(newTestStore(t), "")
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{}
	res, err := a.handleTesseractSkills(context.Background(), req)
	if err != nil {
		t.Fatalf("handleTesseractSkills: %v", err)
	}
	if res == nil || len(res.Content) == 0 {
		t.Fatal("empty result")
	}
	tc, ok := res.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("want TextContent, got %T", res.Content[0])
	}
	var arr []map[string]any
	if err := json.Unmarshal([]byte(tc.Text), &arr); err != nil {
		t.Fatalf("unmarshal %q: %v", tc.Text, err)
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
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"name": "start-here"}
	res, err := a.handleTesseractSkills(context.Background(), req)
	if err != nil {
		t.Fatalf("handleTesseractSkills: %v", err)
	}
	if res == nil || len(res.Content) == 0 {
		t.Fatal("empty result")
	}
	tc, ok := res.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("want TextContent, got %T", res.Content[0])
	}
	if !strings.Contains(tc.Text, "Tesseract") {
		t.Errorf("body missing expected content")
	}
}

func TestTesseractSkills_UnknownName_ReturnsToolError(t *testing.T) {
	a := New(newTestStore(t), "")
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"name": "does-not-exist"}
	res, err := a.handleTesseractSkills(context.Background(), req)
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
