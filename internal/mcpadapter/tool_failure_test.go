package mcpadapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	gomcpserver "github.com/hollis-labs/go-mcp/server"
	"github.com/hollis-labs/tesseract/internal/contextstore"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func connectToolClient(t *testing.T, srv *gomcpserver.Server) *mcpsdk.ClientSession {
	t.Helper()
	ctx := context.Background()
	st, ct := mcpsdk.NewInMemoryTransports()
	ss, err := srv.SDKServer().Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ss.Close() })
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test-client", Version: "test"}, nil)
	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func callWire(t *testing.T, client *mcpsdk.ClientSession, name string, args map[string]any) (*mcpsdk.CallToolResult, map[string]any) {
	t.Helper()
	res, err := client.CallTool(context.Background(), &mcpsdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s returned a protocol error: %v", name, err)
	}
	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if len(res.Content) != 1 {
		t.Fatalf("expected mirrored JSON text: %+v", res)
	}
	text, ok := res.Content[0].(*mcpsdk.TextContent)
	if !ok {
		t.Fatalf("expected text, got %T", res.Content[0])
	}
	var mirror map[string]any
	if err := json.Unmarshal([]byte(text.Text), &mirror); err != nil {
		t.Fatal(err)
	}
	mirrored, _ := json.Marshal(mirror)
	if string(mirrored) != string(raw) {
		t.Fatalf("structured/text content differ: %s vs %s", raw, mirrored)
	}
	return res, body
}

func TestToolRefusalsAreErrorsOnTheWire(t *testing.T) {
	srv := registeredSurface(t)
	client := connectToolClient(t, srv)
	for _, tool := range srv.ToolDefinitions() {
		t.Run(tool.Name, func(t *testing.T) {
			res, body := callWire(t, client, tool.Name, map[string]any{"undeclared_test_argument": true})
			if !res.IsError || body["code"] != "validation_error" {
				t.Fatalf("expected structured tool refusal with isError: %+v, %v", res, body)
			}
		})
	}
	res, body := callWire(t, client, "context_write", map[string]any{"namespace": "app/test", "key": "key", "payload": "{}"})
	if !res.IsError || body["code"] != "auth_required" {
		t.Fatalf("auth refusal: %+v %v", res, body)
	}
	res, body = callWire(t, client, "context_estimate", map[string]any{"selector": "{"})
	if !res.IsError || body["code"] != "validation_error" {
		t.Fatalf("selector refusal: %+v %v", res, body)
	}
	res, _ = callWire(t, client, "context_estimate", map[string]any{"selector": "{}"})
	if res.IsError {
		t.Fatal("successful read marked as error")
	}
	srv.RegisterTool(gomcpserver.Tool{Name: "explicit_details", InputSchema: inputSchema(), Handler: func(context.Context, map[string]any) (any, error) {
		return toolErrorWithDetails(codeValidationError, "add a title", map[string]any{"field": "title"}), nil
	}})
	res, body = callWire(t, client, "explicit_details", map[string]any{})
	if !res.IsError || body["details"].(map[string]any)["field"] != "title" {
		t.Fatalf("details refusal changed: %+v %v", res, body)
	}
	// A successful payload with these fields is data, not a heuristic refusal.
	srv.RegisterTool(gomcpserver.Tool{Name: "success_fields", InputSchema: inputSchema(), Handler: func(context.Context, map[string]any) (any, error) {
		return map[string]any{"code": "example", "message": "data"}, nil
	}})
	res, _ = callWire(t, client, "success_fields", map[string]any{})
	if res.IsError {
		t.Fatal("success containing code/message marked as error")
	}
	srv.RegisterTool(gomcpserver.Tool{Name: "go_error", InputSchema: inputSchema(), Handler: func(context.Context, map[string]any) (any, error) {
		return nil, errors.New("ordinary handler failure")
	}})
	failure, err := client.CallTool(context.Background(), &mcpsdk.CallToolParams{Name: "go_error", Arguments: map[string]any{}})
	if err != nil || !failure.IsError {
		t.Fatalf("ordinary error changed: %+v %v", failure, err)
	}
}

func TestTypedPayloadRequiredFieldsOnTheWire(t *testing.T) {
	store := newTestStore(t)
	token, _, err := store.CreateAuthToken(context.Background(), contextstore.TokenCreateInput{Label: "test", Scopes: []string{"write"}})
	if err != nil {
		t.Fatal(err)
	}
	a := New(store, token)
	srv := gomcpserver.NewServer("test", "test")
	a.RegisterAllTools(srv)
	client := connectToolClient(t, srv)
	for i, payload := range []string{`[]`, `"text"`, `42`, `true`, `null`, `{}`, `{"title":""}`} {
		t.Run(payload, func(t *testing.T) {
			key := fmt.Sprintf("invalid-%d", i)
			res, body := callWire(t, client, "context_typed_write", map[string]any{"namespace": "app/test", "key": key, "payload": payload, "record_type": "task/spec"})
			message, _ := body["message"].(string)
			if !res.IsError || body["code"] != "validation_error" || !strings.Contains(message, "title") {
				t.Fatalf("expected teaching refusal: %+v %v", res, body)
			}
			if _, err := store.Head(context.Background(), "app/test", key); err == nil {
				t.Fatal("invalid record persisted")
			}
		})
	}
	res, body := callWire(t, client, "context_typed_write", map[string]any{"namespace": "app/test", "key": "valid", "payload": `{"title":"Example"}`, "record_type": "task/spec"})
	if res.IsError {
		t.Fatalf("valid payload refused: %v", body)
	}
	res, body = callWire(t, client, "context_typed_write", map[string]any{"namespace": "app/test", "key": "custom", "payload": `[1,2]`, "record_type": "custom/test"})
	if res.IsError {
		t.Fatalf("custom type without required fields refused: %v", body)
	}
}

func TestReadToolHintsOnTheWire(t *testing.T) {
	client := connectToolClient(t, registeredSurface(t))
	result, err := client.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	tools := map[string]*mcpsdk.Tool{}
	for _, tool := range result.Tools {
		tools[tool.Name] = tool
		if strings.TrimSpace(tool.Title) == "" {
			t.Errorf("%s has no title", tool.Name)
		}
	}
	for _, name := range []string{"context_view", "context_typed_view", "context_pack", "context_plan", "context_estimate", "context_registry_list", "context_promotion_list", "context_audit_list", "context_search", "context_rag_query", "tesseract_runtime_get"} {
		tool := tools[name]
		if tool == nil || tool.Annotations == nil || !tool.Annotations.ReadOnlyHint || tool.Annotations.DestructiveHint == nil || *tool.Annotations.DestructiveHint {
			t.Errorf("%s must advertise a non-destructive read: %+v", name, tool)
		}
	}
	for _, name := range []string{"context_search", "context_rag_query", "context_embed"} {
		if tools[name].Annotations.OpenWorldHint == nil || !*tools[name].Annotations.OpenWorldHint {
			t.Errorf("%s may call the configured embedding provider", name)
		}
	}
	for _, name := range []string{"context_write", "context_typed_write", "tesseract_get", "tesseract_get_revision"} {
		if tools[name].Annotations.ReadOnlyHint {
			t.Errorf("%s mutates state", name)
		}
	}
}
