package mcpadapter

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	gomcpserver "github.com/hollis-labs/go-mcp/server"
	"github.com/hollis-labs/tesseract/internal/knowledge"
)

// The guards are declared on both revisioned write tools, so an MCP caller can
// send them at all (an undeclared argument is refused), and none is required —
// they are opt-in.
func TestWriteToolsDeclareTheOptInGuards(t *testing.T) {
	a := newMemoryAdapter(t, "memory:write")
	a.KnowledgeStore = knowledge.New(a.MemoryStore)
	srv := gomcpserver.NewServer("test", "0.0.0")
	a.RegisterAllTools(srv)

	for tool, want := range map[string][]string{
		"knowledge_write": {"create_only", "expected_revision_id", "status", "derived_from"},
		"memory_write":    {"create_only", "expected_revision_id"},
	} {
		st, ok := toolDef(srv, tool)
		if !ok {
			t.Fatalf("%s not registered", tool)
		}
		raw, err := json.Marshal(st.InputSchema)
		if err != nil {
			t.Fatalf("marshal %s schema: %v", tool, err)
		}
		var schema struct {
			Properties map[string]struct {
				Type string `json:"type"`
			} `json:"properties"`
			Required []string `json:"required"`
		}
		if err := json.Unmarshal(raw, &schema); err != nil {
			t.Fatalf("decode %s schema: %v", tool, err)
		}
		for _, name := range want {
			if _, declared := schema.Properties[name]; !declared {
				t.Errorf("%s does not declare %q", tool, name)
			}
			for _, req := range schema.Required {
				if req == name {
					t.Errorf("%s requires %q; the guards are opt-in", tool, name)
				}
			}
		}
		if got := schema.Properties["create_only"].Type; got != "boolean" {
			t.Errorf("%s create_only type = %q, want boolean", tool, got)
		}
	}
}

func knowledgeGuardArgs(key string, extra map[string]any) map[string]any {
	args := map[string]any{
		"namespace":       "project/tesseract/knowledge/guards",
		"key":             key,
		"kind":            "note",
		"source":          "manual",
		"pointer_scheme":  "nil",
		"pointer_locator": "guards/" + key,
		"summary":         "guard test for " + key,
		"author_agent_id": "test",
		"session_id":      "sess-guards",
	}
	for k, v := range extra {
		args[k] = v
	}
	return args
}

func TestKnowledgeWrite_GuardConflictsAnswerTheirOwnCodes(t *testing.T) {
	a := newMemoryAdapter(t, "memory:write")
	a.KnowledgeStore = knowledge.New(a.MemoryStore)
	call := func(args map[string]any) map[string]any {
		t.Helper()
		res, err := a.handleKnowledgeWrite(context.Background(), args)
		if err != nil {
			t.Fatalf("handleKnowledgeWrite: %v", err)
		}
		return parseResult(t, res)
	}

	first := call(knowledgeGuardArgs("guard.one", nil))
	if first["write_outcome"] != "created" {
		t.Fatalf("first write_outcome = %v, want created; body=%v", first["write_outcome"], first)
	}
	// Omitting the ranking fields still gives what every knowledge write always got.
	if first["status"] != "canonical" || first["derived_from"] != "reference" {
		t.Errorf("defaults status=%v derived_from=%v, want canonical and reference", first["status"], first["derived_from"])
	}
	head, _ := first["revision_id"].(string)

	// create_only: the collision that used to succeed silently.
	dup := call(knowledgeGuardArgs("guard.one", map[string]any{"create_only": true}))
	if dup["code"] != string(codeKeyConflict) {
		t.Fatalf("create_only on an existing key: code = %v, want %s; body=%v", dup["code"], codeKeyConflict, dup)
	}
	if msg, _ := dup["message"].(string); !strings.Contains(msg, head) {
		t.Errorf("key_conflict message does not name the current head %s: %s", head, msg)
	}

	// expected_revision_id: a stale base.
	stale := call(knowledgeGuardArgs("guard.one", map[string]any{"expected_revision_id": "01HZZZZZZZZZZZZZZZZZZZZZZZ"}))
	if stale["code"] != string(codeRevisionConflict) {
		t.Fatalf("stale expected_revision_id: code = %v, want %s; body=%v", stale["code"], codeRevisionConflict, stale)
	}
	if msg, _ := stale["message"].(string); !strings.Contains(msg, head) {
		t.Errorf("revision_conflict message does not name the current head %s: %s", head, msg)
	}

	// The current head is accepted, and the response says what happened.
	next := call(knowledgeGuardArgs("guard.one", map[string]any{"expected_revision_id": head, "supersedes": head}))
	if next["write_outcome"] != "appended" || next["previous_revision_id"] != head {
		t.Errorf("guarded edit: write_outcome=%v previous_revision_id=%v, want appended and %s", next["write_outcome"], next["previous_revision_id"], head)
	}

	// The two guards contradict each other.
	both := call(knowledgeGuardArgs("guard.two", map[string]any{"create_only": true, "expected_revision_id": head}))
	if both["code"] != string(codeValidationError) {
		t.Errorf("both guards: code = %v, want %s; body=%v", both["code"], codeValidationError, both)
	}
}

func TestKnowledgeWrite_StatusAndDerivedFromPassThrough(t *testing.T) {
	a := newMemoryAdapter(t, "memory:write")
	a.KnowledgeStore = knowledge.New(a.MemoryStore)
	call := func(args map[string]any) map[string]any {
		t.Helper()
		res, err := a.handleKnowledgeWrite(context.Background(), args)
		if err != nil {
			t.Fatalf("handleKnowledgeWrite: %v", err)
		}
		return parseResult(t, res)
	}

	body := call(knowledgeGuardArgs("chosen", map[string]any{"status": "draft", "derived_from": "observation"}))
	if body["status"] != "draft" || body["derived_from"] != "observation" {
		t.Errorf("status=%v derived_from=%v, want draft and observation; body=%v", body["status"], body["derived_from"], body)
	}

	for _, bad := range []map[string]any{{"status": "bogus"}, {"derived_from": "bogus"}} {
		got := call(knowledgeGuardArgs("bad", bad))
		if got["code"] != string(codeValidationError) {
			t.Errorf("%v: code = %v, want %s", bad, got["code"], codeValidationError)
		}
	}
}

func TestMemoryWrite_CreateOnlyAnswersKeyConflict(t *testing.T) {
	a := newMemoryAdapter(t, "memory:write")
	args := func(extra map[string]any) map[string]any {
		m := map[string]any{
			"namespace":       "user/chrispian/memory/notes",
			"memory_key":      "guards.memory",
			"author_agent_id": "claude",
			"trigger":         "explicit",
			"session_id":      "sess-001",
			"derived_from":    "user",
			"confidence":      0.9,
			"payload_summary": "guard test",
			"actor":           "user",
		}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}
	first := writeViaHandler(t, a, args(map[string]any{"create_only": true}))
	if first["write_outcome"] != "created" {
		t.Fatalf("write_outcome = %v, want created; body=%v", first["write_outcome"], first)
	}
	dup := writeViaHandler(t, a, args(map[string]any{"create_only": true}))
	if dup["code"] != string(codeKeyConflict) {
		t.Fatalf("code = %v, want %s; body=%v", dup["code"], codeKeyConflict, dup)
	}
}
