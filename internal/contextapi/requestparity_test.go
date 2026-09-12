package contextapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/server"

	"github.com/hollis-labs/tesseract/internal/contextstore"
	"github.com/hollis-labs/tesseract/internal/event"
	"github.com/hollis-labs/tesseract/internal/knowledge"
	"github.com/hollis-labs/tesseract/internal/mcpadapter"
	"github.com/hollis-labs/tesseract/internal/memory"
	"github.com/hollis-labs/tesseract/internal/surfacefields"
)

// The request-shape half of surface parity.
//
// tests/parity asserts an operation has a door on both surfaces.
// crossdomain_parity_test.go asserts the two doors ANSWER the same bytes.
// Neither looks at what they ACCEPT, which is the only half a caller can get
// wrong — and the gap is visible in the second one's own fixtures, where
// mcpArgs says "key" and httpPath says "memory_key" side by side, green.
//
// This file closes it, in the shape strictargs.go established: derive the
// accepted set from the code rather than restating it, then assert by behavior
// what reflection cannot reach.

// httpRequestPaths lists the dotted JSON paths a request struct accepts.
//
// jsonFieldNames answers the top level, which is all an unknown-field message
// can honestly use; this goes all the way down, because the table declares
// leaves and a field buried in a sub-object is exactly the kind that gets added
// without anyone noticing.
func httpRequestPaths(v any) []string {
	var out []string
	var walk func(t reflect.Type, prefix string)
	walk = func(t reflect.Type, prefix string) {
		for t.Kind() == reflect.Pointer {
			t = t.Elem()
		}
		if t.Kind() != reflect.Struct {
			return
		}
		for i := range t.NumField() {
			f := t.Field(i)
			if !f.IsExported() {
				continue
			}
			name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
			if name == "-" {
				continue
			}
			if name == "" {
				name = f.Name
			}
			path := name
			if prefix != "" {
				path = prefix + "." + name
			}
			out = append(out, path)
			if ft := f.Type; descendsIntoJSONObject(ft) {
				walk(ft, path)
			}
		}
	}
	walk(reflect.TypeOf(v), "")
	return out
}

// descendsIntoJSONObject reports whether encoding/json renders this type as an
// object built from its own fields — the only case where a sub-path exists.
//
// json.RawMessage and time.Time are structs or slices that serialize as a
// single value, so recursing into them would invent paths no caller can send.
func descendsIntoJSONObject(t reflect.Type) bool {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct || t == reflect.TypeOf(time.Time{}) {
		return false
	}
	return !reflect.PointerTo(t).Implements(reflect.TypeOf((*json.Marshaler)(nil)).Elem())
}

// requestStructs is the inverse of requestDoors, so this test can reach the
// struct a door names. Keeping them separate keeps the production map free of
// test-only entries.
var requestStructs = map[string]any{
	"memory.write":    memoryWriteRequest{},
	"knowledge.write": knowledgeWriteRequest{},
	"event.write":     eventWriteRequest{},
}

func registeredToolArgs(t *testing.T) map[string]map[string]any {
	t.Helper()
	root := t.TempDir()
	cs, err := contextstore.Open(context.Background(), contextstore.Config{RootDir: root})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })

	mem := memory.NewStore(cs.DB(), nil, "", 0, memory.NoopQueue{})
	adapter := mcpadapter.New(cs, "")
	adapter.MemoryStore = mem
	adapter.KnowledgeStore = knowledge.New(mem)
	adapter.EventStore = event.New(mem)

	srv := server.NewMCPServer("request-parity", "0.0.0", server.WithToolCapabilities(true))
	adapter.RegisterAllTools(srv)

	args := map[string]map[string]any{}
	for name, tool := range srv.ListTools() {
		args[name] = tool.Tool.InputSchema.Properties
	}
	return args
}

// TestDoorsMatchTheSurfaces is the check the task asked for: both columns read
// off the code, and any drift in either fails here rather than in a caller.
//
// It is deliberately symmetric. A field the table declares and the surface does
// not have is a stale row — the failure the literal hint map had and nothing
// caught. A field the surface has and the table does not declare is the other
// one, and it is the one `workspace` would have produced.
func TestDoorsMatchTheSurfaces(t *testing.T) {
	toolArgs := registeredToolArgs(t)

	for _, door := range surfacefields.Doors {
		if !door.Derived {
			continue
		}
		t.Run(door.Name, func(t *testing.T) {
			dst, ok := requestStructs[door.Name]
			if !ok {
				t.Fatalf("door %q is Derived but requestStructs has no entry for it", door.Name)
			}
			actualHTTP := httpRequestPaths(dst)
			actualMCP, ok := toolArgs[door.MCPTool]
			if !ok {
				t.Fatalf("door %q names MCP tool %q, which no adapter registers", door.Name, door.MCPTool)
			}

			declaredHTTP := map[string]bool{}
			declaredMCP := map[string]bool{}
			// A one-sided HTTP row covers its own subtree: declaring that
			// `facets` exists on HTTP alone says nothing is flattened out of it,
			// so its leaves need no rows of their own. A two-sided row does not
			// get that, because a flattened object's leaves are exactly what MCP
			// has to spell.
			var httpSubtrees []string
			for _, f := range door.Fields {
				if f.HTTP != "" {
					declaredHTTP[f.HTTP] = true
					if f.MCP == "" {
						httpSubtrees = append(httpSubtrees, f.HTTP+".")
					}
				}
				if f.MCP != "" {
					declaredMCP[f.MCP] = true
				}
			}

			covered := func(path string) bool {
				if declaredHTTP[path] {
					return true
				}
				for _, prefix := range httpSubtrees {
					if strings.HasPrefix(path, prefix) {
						return true
					}
				}
				// A parent object whose leaves are all declared needs no row of
				// its own: `payload` exists only to carry payload.summary and
				// friends, and a caller never sends it as a unit.
				for declared := range declaredHTTP {
					if strings.HasPrefix(declared, path+".") {
						return true
					}
				}
				return false
			}

			for _, path := range actualHTTP {
				if !covered(path) {
					t.Errorf("%s accepts HTTP field %q, which no surfacefields row declares. "+
						"Add a row — that is how a difference becomes visible instead of inherited.",
						door.HTTPPath, path)
				}
			}
			for path := range declaredHTTP {
				if !slices.Contains(actualHTTP, path) {
					t.Errorf("surfacefields declares HTTP field %q on %s, which the request "+
						"struct does not have. The row is stale.", path, door.Name)
				}
			}
			for name := range actualMCP {
				if !declaredMCP[name] {
					t.Errorf("MCP tool %s accepts argument %q, which no surfacefields row "+
						"declares on door %s.", door.MCPTool, name, door.Name)
				}
			}
			for name := range declaredMCP {
				if _, ok := actualMCP[name]; !ok {
					t.Errorf("surfacefields declares MCP argument %q on door %s, which tool %s "+
						"does not accept. The row is stale.", name, door.Name, door.MCPTool)
				}
			}
		})
	}
}

// TestDoorsNameRealSurfaces keeps a door from declaring a pairing that does not
// exist. surfaceCatalog owns existence; this only checks that what the table
// says it is describing is really there.
func TestDoorsNameRealSurfaces(t *testing.T) {
	toolArgs := registeredToolArgs(t)
	srv := newTestServer(t)

	for _, door := range surfacefields.Doors {
		if _, ok := toolArgs[door.MCPTool]; !ok {
			t.Errorf("door %q names MCP tool %q, which no adapter registers", door.Name, door.MCPTool)
		}
		req := httptest.NewRequest(door.HTTPMethod, door.HTTPPath, nil)
		rr := httptest.NewRecorder()
		srv.ServeHTTP(rr, req)
		if rr.Code == http.StatusNotFound {
			t.Errorf("door %q names HTTP route %s %s, which does not route",
				door.Name, door.HTTPMethod, door.HTTPPath)
		}
	}
}

// TestReadDoorsAcceptTheSpellingsTheyDeclare earns back the column reflection
// cannot reach.
//
// A GET reads its parameters by literal string, so nothing derives them. What
// can be derived is the server's answer: the declared name gets past the
// required-parameter check and any other name does not. That asserts the same
// fact reflection would — including, for the rows that carry a Pending, that
// the divergence they claim is real rather than assumed.
func TestReadDoorsAcceptTheSpellingsTheyDeclare(t *testing.T) {
	// Wired, not bare: an unwired memory store answers 503 before the handler
	// reads a parameter at all, which makes every spelling look equally
	// rejected and the assertion below vacuous.
	srv := newMemoryTestServer(t)
	srv.KnowledgeStore = knowledge.New(srv.MemoryStore)

	missingParam := func(path, param, value string) bool {
		req := httptest.NewRequest(http.MethodGet, path+"?namespace=user/x/memory/notes&"+param+"="+value, nil)
		rr := httptest.NewRecorder()
		srv.ServeHTTP(rr, req)
		if rr.Code != http.StatusBadRequest {
			return false
		}
		return strings.Contains(rr.Body.String(), "required")
	}

	for _, door := range surfacefields.Doors {
		if door.Derived {
			continue
		}
		keyField, ok := door.Field("key")
		if !ok {
			t.Errorf("read door %q has no `key` row", door.Name)
			continue
		}
		t.Run(door.Name, func(t *testing.T) {
			if missingParam(door.HTTPPath, keyField.HTTP, "k") {
				t.Errorf("%s rejects its declared parameter %q as missing — the row is wrong",
					door.HTTPPath, keyField.HTTP)
			}
			if !keyField.Diverges() {
				return
			}
			// The row claims MCP spells this differently and HTTP will not take
			// that spelling. If HTTP accepts both, the divergence is smaller
			// than declared and the Pending is overstating the work.
			if !missingParam(door.HTTPPath, keyField.MCP, "k") {
				t.Errorf("%s accepts %q as well as %q, so the declared divergence "+
					"(Pending %s) is not real", door.HTTPPath, keyField.MCP, keyField.HTTP, keyField.Pending)
			}
		})
	}
}

// TestUnknownFieldHintUsesTheSharedTable is the end-to-end for the derivation,
// written against the field that exposed the literal.
//
// `payload_data` is an MCP argument on all three write tools and was absent
// from the hand-written map, so the one field CW-20260912-0048 was filed about
// got the bare rejection. The two doors now answer differently and both answer
// — which the literal could not do, its values being dotted paths.
func TestUnknownFieldHintUsesTheSharedTable(t *testing.T) {
	srv := newMemoryTestServer(t)
	srv.KnowledgeStore = knowledge.New(srv.MemoryStore)

	for _, tc := range []struct {
		path, field, wantSpelling, wantPhrase string
	}{
		{"/v1/memory/write", "payload_data", "payload.data", "nests it as"},
		{"/v1/knowledge/write", "payload_data", "data", "takes it flat as"},
		{"/v1/memory/write", "payload_summary", "payload.summary", "nests it as"},
		{"/v1/memory/write", "author_agent_version", "author.agent_version", "nests it as"},
	} {
		t.Run(tc.path+"/"+tc.field, func(t *testing.T) {
			body := `{"` + tc.field + `":"x"}`
			req := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			rr := httptest.NewRecorder()
			srv.ServeHTTP(rr, req)

			if rr.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", rr.Code, rr.Body.String())
			}
			var got struct {
				Message string         `json:"message"`
				Details map[string]any `json:"details"`
			}
			if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if got.Details["expected_field"] != tc.wantSpelling {
				t.Errorf("expected_field = %v, want %q", got.Details["expected_field"], tc.wantSpelling)
			}
			if !strings.Contains(got.Message, tc.wantPhrase) {
				t.Errorf("message = %q, want it to contain %q", got.Message, tc.wantPhrase)
			}
		})
	}
}
