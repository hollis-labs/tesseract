package authn

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	embedcontracts "github.com/hollis-labs/libs/llm/core/embedcontracts"
	gomcpserver "github.com/hollis-labs/libs/mcp/plugin-mcp/go-mcp/server"
	"github.com/hollis-labs/tesseract/domains"
	"github.com/hollis-labs/tesseract/internal/config"
	"github.com/hollis-labs/tesseract/internal/contextstore"
	"github.com/hollis-labs/tesseract/internal/embedding"
	"github.com/hollis-labs/tesseract/internal/event"
	"github.com/hollis-labs/tesseract/internal/knowledge"
	"github.com/hollis-labs/tesseract/internal/mcpadapter"
	"github.com/hollis-labs/tesseract/internal/memory"
	"github.com/hollis-labs/tesseract/internal/promotion"
	"github.com/hollis-labs/tesseract/internal/workspace"
)

// Deliberately mislabels a denied record as allowed. Authorization must use
// authoritative record metadata, not trust the vector index's namespace.
type namespaceIndex struct {
	embedding.VectorIndex
	results []embedding.SearchResult
}

func (i namespaceIndex) Search(context.Context, []float32, embedding.SearchOptions) ([]embedding.SearchResult, error) {
	return i.results, nil
}

type namespaceEmbedder struct{}

func (namespaceEmbedder) Embed(context.Context, string, string) (*embedcontracts.EmbeddingResult, error) {
	return &embedcontracts.EmbeddingResult{Embedding: []float32{1, 0}}, nil
}
func (namespaceEmbedder) EmbedBatch(_ context.Context, texts []string, _ string) ([]embedcontracts.EmbeddingResult, error) {
	out := make([]embedcontracts.EmbeddingResult, len(texts))
	for i := range out {
		out[i].Embedding = []float32{1, 0}
	}
	return out, nil
}
func (namespaceEmbedder) EmbeddingDimensions(string) int { return 2 }

func namespaceMCP(t *testing.T, mode string) (*fixture, *mcpHarness, *mcpadapter.Adapter) {
	t.Helper()
	f := newFixture(t)
	tok := f.token("restricted", []string{"memory:read", "memory:write", "write", "namespace.admin", "promote.request", "promote.approve", "promote.apply"}, []string{"app/allowed/*", "project/allowed/*", "session/allowed/*"})
	h := f.mcp(config.AuthConfig{Mode: mode}, tok, nil)
	a := mcpadapter.New(f.store, tok)
	a.Auth = h.rt.NewMCPSession(func() (string, error) { return h.tok, nil })
	a.MemoryStore = memory.NewStore(f.store.DB(), nil, "", 0, memory.NoopQueue{})
	a.KnowledgeStore = knowledge.New(a.MemoryStore)
	a.EventStore = event.New(a.MemoryStore)
	a.WorkspaceStore = workspace.NewStore(f.store.DB())
	a.WorkspacePromotionStore = promotion.NewStore(f.store, a.MemoryStore)
	a.EmbeddingProvider = namespaceEmbedder{}
	a.EmbeddingModel = "namespace-test"
	h.srv = gomcpserver.NewServer("namespace-test", "0")
	a.RegisterAllTools(h.srv)
	return f, h, a
}

func namespaceCall(t *testing.T, h *mcpHarness, tool string, args map[string]any) string {
	t.Helper()
	res, err := h.srv.CallTool(context.Background(), tool, args)
	if err != nil {
		t.Fatalf("%s transport: %v", tool, err)
	}
	if s, ok := res.(string); ok {
		return s
	}
	b, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Walk the real registered surface, including every dispatcher arm that can
// choose a namespace or an ID. A missing fixture for a newly registered tool
// fails the walk instead of silently leaving that tool unaudited.
func TestMCPNamespaceToolWalk(t *testing.T) {
	f, h, a := namespaceMCP(t, "enforce")
	ctx := context.Background()
	const secret = "FORBIDDEN_PAYLOAD_CANARY"
	contexts := map[string]contextstore.Record{}
	revisions := map[string]memory.Revision{}
	for _, side := range []string{"allowed", "blocked"} {
		text := "permitted-payload"
		if side == "blocked" {
			text = secret
		}
		rec, err := f.store.AppendRecord(ctx, contextstore.AppendInput{Namespace: "app/" + side + "/docs", Key: "entry", Actor: "test", Payload: json.RawMessage(`{"title":"` + text + `"}`), RecordType: "task/spec", Status: "draft"})
		if err != nil {
			t.Fatal(err)
		}
		contexts[side] = rec
		if err := f.store.UpsertEmbedding(ctx, contextstore.EmbeddingRow{RecordID: rec.RecordID, Model: a.EmbeddingModel, Dimensions: 2, Vector: []float32{1, 0}}); err != nil {
			t.Fatal(err)
		}
		for _, domain := range []domains.Domain{domains.Memory, domains.Knowledge, domains.Event} {
			ns := "project/" + side + "/" + string(domain) + "/notes"
			if domain == domains.Event {
				ns = "project/" + side + "/event/reasoning"
			}
			in := memory.WriteInput{Domain: domain, Namespace: ns, MemoryKey: "entry", Actor: "agent", Author: memory.Author{AgentID: "test"}, Trigger: memory.TriggerExplicit, SessionID: "test", DerivedFrom: memory.DerivedFromProject, Confidence: 0.9, Summary: text, Body: text, Status: memory.StatusCanonical}
			if domain == domains.Knowledge {
				in.Facets = memory.Facets{Kind: "note", Source: "manual", Pointer: &memory.Pointer{Scheme: "nil", Locator: "none"}}
			}
			rev, err := a.MemoryStore.WriteRevision(ctx, in)
			if err != nil {
				t.Fatalf("seed %s: %v", domain, err)
			}
			revisions[side+string(domain)] = rev
		}
		if err := f.store.UpsertNamespacePolicy(ctx, contextstore.NamespacePolicyEntry{Namespace: "app/" + side + "/docs", OwnerType: "app", OwnerID: side}); err != nil {
			t.Fatal(err)
		}
		if err := f.store.EmitWrite(ctx, "test", rec.Namespace, rec.Key, rec.Revision, rec.RecordID, nil); err != nil {
			t.Fatal(err)
		}
	}
	ws, err := a.WorkspaceStore.Create(ctx, workspace.CreateInput{Namespace: "project/blocked/workspace/drafts", Key: "entry", Summary: secret, Author: memory.Author{AgentID: "test"}, SessionID: "test"})
	if err != nil {
		t.Fatal(err)
	}
	prPayload, _ := json.Marshal(contextstore.PromoteRequest{Type: "promote.request", RequestID: "request", SourceNamespace: contexts["blocked"].Namespace, SourceKey: "entry", SourceRevisionID: contexts["blocked"].RecordID, TargetNamespace: "app/allowed/docs", TargetKey: "promoted", Status: "pending", Reason: "promotion-reason"})
	if _, err := f.store.AppendRecord(ctx, contextstore.AppendInput{Namespace: "app/allowed/promotions", Key: "request", Actor: "test", Payload: prPayload}); err != nil {
		t.Fatal(err)
	}

	restricted := h.tok
	h.setToken(f.token("seed-promoter", []string{"memory:read", "memory:write", "promote.request", "promote.approve", "promote.apply"}, []string{"*"}))
	requestBody := namespaceCall(t, h, "workspace_promote", map[string]any{"stage": "request", "source_item_id": ws.ItemID, "source_version_token": ws.VersionToken, "actor": "test", "target_domain": "memory", "target_namespace": "project/allowed/memory/notes", "target_key": "promoted", "target_author_agent_id": "test", "target_session_id": "test", "target_trigger": "promotion", "target_derived_from": "project"})
	var request map[string]any
	if err := json.Unmarshal([]byte(requestBody), &request); err != nil {
		t.Fatal(err)
	}
	requestID, ok := request["request_id"].(string)
	if !ok || requestID == "" {
		t.Fatalf("seed workspace request: %s", requestBody)
	}
	h.setToken(restricted)
	type probe struct {
		args map[string]any
		want string
	}
	deny := func(args map[string]any) probe { return probe{args, "namespace_not_permitted"} }
	clean := func(args map[string]any) probe { return probe{args, ""} }
	bns := "app/blocked/docs"
	brev := revisions["blockedmemory"]
	probes := map[string][]probe{
		"context_audit_list":         {clean(map[string]any{}), clean(map[string]any{"namespace": bns})},
		"context_embed":              {deny(map[string]any{"record_id": contexts["blocked"].RecordID, "namespace": bns, "key": "entry"}), deny(map[string]any{"record_id": contexts["blocked"].RecordID, "namespace": "app/allowed/docs", "key": "entry"})},
		"context_estimate":           {clean(map[string]any{"selector": `{"namespaces":["*"]}`})},
		"context_ingest":             {deny(map[string]any{"mode": "chunked", "namespace": bns, "key_prefix": "new", "text": "attempt"}), {map[string]any{"items": `[{"namespace":"app/blocked/docs","key":"new","payload":{"title":"attempt"}}]`}, "does not permit"}},
		"context_namespace_register": {deny(map[string]any{"namespace": bns, "owner_type": "app", "owner_id": "changed"})},
		"context_pack":               {clean(map[string]any{"view_id": "task_exec"}), clean(map[string]any{"shape": "packet", "namespaces": "*"})},
		"context_plan":               {clean(map[string]any{"intent": "custom", "execute": true}), clean(map[string]any{"intent": "custom"})},
		"context_promote":            {deny(map[string]any{"stage": "request", "source_namespace": bns, "source_key": "entry", "target_namespace": "app/allowed/docs", "target_key": "new"}), deny(map[string]any{"stage": "approve", "request_id": "request"}), deny(map[string]any{"stage": "apply", "request_id": "request"})},
		"context_promotion_list":     {clean(map[string]any{"status": "all"})},
		"context_rag_query":          {clean(map[string]any{"query": "needle", "threshold": float64(0)})},
		"context_registry_list":      {clean(map[string]any{"kind": "namespaces", "limit": float64(1)}), deny(map[string]any{"kind": "namespaces", "name": bns}), clean(map[string]any{"kind": "types"}), clean(map[string]any{"kind": "views"})},
		"context_search":             {clean(map[string]any{"query": "needle", "threshold": float64(0)})},
		"context_session_write":      {deny(map[string]any{"session_id": "test", "project_id": "blocked", "summary": "attempt"})},
		"context_status_set":         {deny(map[string]any{"namespace": bns, "key": "entry", "status": "reviewed"}), deny(map[string]any{"namespace": bns, "key": "entry", "status": "deprecated"})},
		"context_typed_view":         {clean(map[string]any{"view_id": "task_exec"})},
		"context_typed_write":        {deny(map[string]any{"namespace": bns, "key": "new", "record_type": "task/spec", "payload": `{"title":"attempt"}`})},
		"context_view":               {clean(map[string]any{"namespaces": "*"}), clean(map[string]any{"selector": `{"namespaces":["*"]}`, "full_evaluation": true, "include_payload": true})},
		"context_write":              {deny(map[string]any{"namespace": bns, "key": "new", "payload": `{"title":"attempt"}`})},
		"event_list":                 {deny(map[string]any{"namespaces": `["project/blocked/event/*"]`})},
		"event_write":                {deny(map[string]any{"namespace": "project/blocked/event/reasoning", "summary": "attempt", "author_agent_id": "test", "session_id": "test"})},
		"knowledge_write":            {deny(map[string]any{"namespace": "project/blocked/knowledge/notes", "key": "new", "summary": "attempt", "kind": "note", "source": "manual", "pointer_scheme": "nil", "pointer_locator": "none", "author_agent_id": "test", "session_id": "test"})},
		"memory_promote":             {deny(map[string]any{"source_namespace": "session/blocked/memory/notes", "source_memory_id": brev.MemoryID, "target_namespace": "project/allowed/memory/notes"})},
		"memory_write":               {deny(map[string]any{"namespace": "project/blocked/memory/notes", "memory_key": "new", "payload_summary": "attempt", "author_agent_id": "test", "session_id": "test", "trigger": "explicit", "derived_from": "project", "confidence": 0.9})},
		"tesseract_deprecate":        {deny(map[string]any{"revision_id": brev.RevisionID})},
		"tesseract_get":              {deny(map[string]any{"domain": "context", "namespace": bns, "key": "entry"}), deny(map[string]any{"item_id": brev.MemoryID}), deny(map[string]any{"item_id": ws.ItemID})},
		"tesseract_get_revision":     {deny(map[string]any{"revision_id": brev.RevisionID})},
		"tesseract_history":          {deny(map[string]any{"domain": "context", "namespace": bns, "key": "entry"}), deny(map[string]any{"item_id": brev.MemoryID}), deny(map[string]any{"item_id": ws.ItemID})},
		"tesseract_recall":           {deny(map[string]any{"namespaces": `["project/blocked/*"]`}), clean(map[string]any{"namespaces": `["project/allowed/*"]`})},
		"tesseract_ref_resolve":      {deny(map[string]any{"revision_id": brev.RevisionID}), deny(map[string]any{"item_id": ws.ItemID})},
		"tesseract_runtime_get":      {clean(map[string]any{})},
		"tesseract_skills":           {clean(map[string]any{})},
		"tesseract_touch":            {deny(map[string]any{"revision_ids": []string{revisions["allowedmemory"].RevisionID, brev.RevisionID}}), deny(map[string]any{"item_ids": []string{brev.MemoryID}})},
		"workspace_delete":           {deny(map[string]any{"item_id": ws.ItemID, "version_token": ws.VersionToken})},
		"workspace_promote":          {deny(map[string]any{"stage": "request", "source_item_id": ws.ItemID, "source_version_token": ws.VersionToken, "target_domain": "memory", "target_namespace": "project/allowed/memory/notes", "target_author_agent_id": "test", "target_session_id": "test", "target_trigger": "promotion", "target_derived_from": "project", "actor": "test"})},
		"workspace_write":            {deny(map[string]any{"namespace": "project/blocked/workspace/drafts", "key": "new", "summary": "attempt", "author_agent_id": "test", "session_id": "test"}), deny(map[string]any{"item_id": ws.ItemID, "version_token": ws.VersionToken, "summary": "attempt"})},
	}
	for _, domain := range []string{"memory", "knowledge", "event", "workspace"} {
		ns := "project/blocked/" + domain + "/notes"
		if domain == "event" {
			ns = "project/blocked/event/reasoning"
		}
		if domain == "workspace" {
			ns = ws.Namespace
		}
		for _, tool := range []string{"tesseract_get", "tesseract_history"} {
			probes[tool] = append(probes[tool], deny(map[string]any{"domain": domain, "namespace": ns, "key": "entry"}))
		}
	}

	probes["workspace_promote"] = append(probes["workspace_promote"], deny(map[string]any{"stage": "approve", "request_id": requestID, "actor": "test"}), deny(map[string]any{"stage": "apply", "request_id": requestID, "actor": "test"}))
	probes["memory_promote"] = append(probes["memory_promote"], deny(map[string]any{"source_namespace": "session/allowed/memory/notes", "source_memory_id": brev.MemoryID, "target_namespace": "project/allowed/memory/notes"}), deny(map[string]any{"source_namespace": "session/allowed/memory/notes", "source_memory_id": brev.MemoryID, "target_namespace": "project/blocked/memory/notes"}))
	before := namespaceState(t, f)
	for _, def := range h.srv.ToolDefinitions() {
		cases, ok := probes[def.Name]
		if !ok {
			t.Errorf("registered tool %s lacks namespace probes", def.Name)
			continue
		}
		t.Run(def.Name, func(t *testing.T) {
			for _, p := range cases {

				var accessesBefore int
				if def.Name == "tesseract_touch" {
					if err := f.store.DB().QueryRow("SELECT COALESCE(SUM(access_count),0) FROM memory_state").Scan(&accessesBefore); err != nil {
						t.Fatal(err)
					}
				}
				body := namespaceCall(t, h, def.Name, p.args)
				if def.Name == "tesseract_touch" {
					var accessesAfter int
					if err := f.store.DB().QueryRow("SELECT COALESCE(SUM(access_count),0) FROM memory_state").Scan(&accessesAfter); err != nil {
						t.Fatal(err)
					}
					if accessesAfter != accessesBefore {
						t.Errorf("denied mixed batch partially touched allowed IDs: %d -> %d", accessesBefore, accessesAfter)
					}
				}
				if p.want != "" && !strings.Contains(body, p.want) {
					t.Errorf("args=%v: expected %s, got %s", p.args, p.want, body)
				}
				if p.want == "" && strings.Contains(body, `"code":`) {
					t.Errorf("unexpected tool error: %s", body)
				}
				if p.want == "" && (strings.Contains(body, `"namespace":"app/blocked/`) || strings.Contains(body, `"namespace":"project/blocked/`)) {
					t.Errorf("out-of-glob metadata returned: %s", body)
				}
				if strings.Contains(body, secret) {
					t.Errorf("out-of-glob content returned: %s", body)
				}
			}
		})
	}

	a.VectorIndex = namespaceIndex{results: []embedding.SearchResult{
		{RecordID: contexts["blocked"].RecordID, Namespace: "app/allowed/docs", Score: 1},
		{RecordID: contexts["allowed"].RecordID, Namespace: "app/allowed/docs", Score: 1},
	}}
	indexed := namespaceCall(t, h, "context_rag_query", map[string]any{"query": "needle", "threshold": float64(0)})
	if strings.Contains(indexed, secret) || !strings.Contains(indexed, "permitted-payload") {
		t.Fatalf("indexed RAG namespace ceiling: %s", indexed)
	}
	if after := namespaceState(t, f); after != before {
		t.Fatalf("denied reads/writes changed protected state:\nbefore=%s\nafter=%s", before, after)
	}
	// Positive controls prevent a universal refusal/empty-result implementation
	// from passing the tool walk.
	for _, tool := range []string{"tesseract_get", "tesseract_history"} {
		body := namespaceCall(t, h, tool, map[string]any{"domain": "memory", "namespace": "project/allowed/memory/notes", "key": "entry"})
		if !strings.Contains(body, "permitted-payload") {
			t.Fatalf("allowed %s: %s", tool, body)
		}
	}
	for _, tool := range []string{"context_typed_view", "context_pack"} {
		body := namespaceCall(t, h, tool, map[string]any{"view_id": "task_exec"})
		if !strings.Contains(body, "permitted-payload") {
			t.Fatalf("allowed %s: %s", tool, body)
		}
	}
}

// Capture persistent content and activation for denied namespaces. The walk
// includes reads that normally reinforce, so a refusal after mutation fails.
func namespaceState(t *testing.T, f *fixture) string {
	t.Helper()
	var parts []string
	for _, q := range []string{
		`SELECT record_id||':'||status FROM records ORDER BY record_id`,
		`SELECT revision_id||':'||status FROM memory_revisions ORDER BY revision_id`,
		`SELECT memory_id||':'||access_count||':'||activation FROM memory_state WHERE namespace LIKE '%/blocked/%' ORDER BY memory_id`,
		`SELECT item_id||':'||version_token||':'||summary FROM workspace_items ORDER BY item_id`,
		`SELECT namespace||':'||owner_type||':'||owner_id FROM namespace_policies ORDER BY namespace`,
	} {
		rows, err := f.store.DB().Query(q)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var s string
			if err := rows.Scan(&s); err != nil {
				t.Fatal(err)
			}
			parts = append(parts, s)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
	}
	return strings.Join(parts, "\n")
}

func TestMCPNamespacePermissiveAllowsAndAudits(t *testing.T) {
	for _, mode := range []string{"permissive", "off"} {
		t.Run(mode, func(t *testing.T) {
			f, h, _ := namespaceMCP(t, mode)
			ctx := context.Background()
			for _, ns := range []string{"app/blocked/one", "app/blocked/two"} {
				if _, err := f.store.AppendRecord(ctx, contextstore.AppendInput{Namespace: ns, Key: "entry", Actor: "test", Payload: json.RawMessage(`{"title":"visible-in-observe"}`)}); err != nil {
					t.Fatal(err)
				}
			}
			calls := map[string]map[string]any{
				"tesseract_get": {"domain": "context", "namespace": "app/blocked/one", "key": "entry"},
				"context_view":  {"namespaces": "*", "full_evaluation": true, "include_payload": true},
				"context_write": {"namespace": "app/blocked/one", "key": "new", "payload": `{"title":"written-in-observe"}`},
				"memory_write":  {"namespace": "project/blocked/memory/notes", "memory_key": "new", "payload_summary": "written-in-observe", "author_agent_id": "test", "session_id": "test", "trigger": "explicit", "derived_from": "project", "confidence": 0.9},
			}
			for tool, args := range calls {
				body := namespaceCall(t, h, tool, args)
				if strings.Contains(body, `"code":`) {
					t.Errorf("%s unexpectedly refused %s: %s", mode, tool, body)
				}
			}
			head, err := f.store.Head(ctx, "app/blocked/one", "new")
			if err != nil || !strings.Contains(string(head.Payload), "written-in-observe") {
				t.Fatalf("permissive write not applied: %v %s", err, head.Payload)
			}
			rows, _ := h.rt.Summary.Snapshot()
			seen := map[string]bool{}
			for _, row := range rows {
				if row.Reason == "namespace_not_permitted" {
					seen[row.Route] = true
					if row.Count != 1 {
						t.Errorf("one summary event per call, got %+v", row)
					}
				}
			}
			for tool := range calls {
				if !seen[tool] {
					t.Errorf("missing namespace would-deny audit for %s: %+v", tool, rows)
				}
			}
		})
	}
}

func TestMCPNamespaceJWTUsesConfiguredCeiling(t *testing.T) {
	f := newFixture(t)
	cfg := f.jwtConfig()
	cfg.NamespaceGlobs = []string{"app/allowed/*"}
	h := f.mcp(config.AuthConfig{Mode: "enforce", JWT: cfg}, f.jwt("tesseract.read tesseract.write", nil), nil)
	for _, ns := range []string{"app/allowed/docs", "app/blocked/docs"} {
		if _, err := f.store.AppendRecord(context.Background(), contextstore.AppendInput{Namespace: ns, Key: "entry", Actor: "test", Payload: json.RawMessage(`{"title":"jwt-marker"}`)}); err != nil {
			t.Fatal(err)
		}
	}
	if code := h.call(t, "tesseract_get", map[string]any{"domain": "context", "namespace": "app/blocked/docs", "key": "entry"}); code != "namespace_not_permitted" {
		t.Fatalf("JWT denied namespace = %s", code)
	}
	if code := h.call(t, "context_write", map[string]any{"namespace": "app/blocked/docs", "key": "new", "payload": `{"title":"attempt"}`}); code != "namespace_not_permitted" {
		t.Fatalf("JWT denied write = %s", code)
	}
	body := namespaceCall(t, h, "context_view", map[string]any{"namespaces": "*", "full_evaluation": true, "include_payload": true})
	if !strings.Contains(body, "app/allowed/docs") || strings.Contains(body, "app/blocked/docs") {
		t.Fatalf("JWT view ceiling: %s", body)
	}
}

func TestMCPNamespaceFilteringPrecedesLimitsAndCounts(t *testing.T) {
	f, h, _ := namespaceMCP(t, "enforce")
	ctx := context.Background()
	// Denied rows sort first, so a post-limit filter would lose allowed rows.
	for _, ns := range []string{"app/000-blocked/docs", "app/allowed/a", "app/allowed/b"} {
		rec, err := f.store.AppendRecord(ctx, contextstore.AppendInput{Namespace: ns, Key: "entry", Actor: "test", Payload: json.RawMessage(`{"title":"marker"}`)})
		if err != nil {
			t.Fatal(err)
		}
		if err := f.store.UpsertNamespacePolicy(ctx, contextstore.NamespacePolicyEntry{Namespace: ns, OwnerType: "app", OwnerID: "test"}); err != nil {
			t.Fatal(err)
		}
		if err := f.store.EmitWrite(ctx, "test", ns, "entry", rec.Revision, rec.RecordID, nil); err != nil {
			t.Fatal(err)
		}
	}
	decode := func(tool string, args map[string]any) map[string]any {
		t.Helper()
		body := namespaceCall(t, h, tool, args)
		var out map[string]any
		if err := json.Unmarshal([]byte(body), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	est := decode("context_estimate", map[string]any{"selector": `{"namespaces":["*"]}`})
	if est["record_count"] != float64(2) {
		t.Fatalf("estimate includes denied records: %v", est)
	}
	view := decode("context_view", map[string]any{"namespaces": "*", "full_evaluation": true, "limit": float64(1)})
	if len(view["items"].([]any)) != 1 || !strings.Contains(namespaceCall(t, h, "context_view", map[string]any{"namespaces": "*", "full_evaluation": true, "limit": float64(1)}), "app/allowed/") {
		t.Fatalf("limit before authorization: %v", view)
	}
	first := decode("context_registry_list", map[string]any{"kind": "namespaces", "limit": float64(1)})
	if first["total"] != float64(2) || first["count"] != float64(1) {
		t.Fatalf("registry counts: %v", first)
	}
	cursor, ok := first["next_cursor"].(string)
	if !ok || cursor == "" {
		t.Fatalf("missing cursor: %v", first)
	}
	second := decode("context_registry_list", map[string]any{"kind": "namespaces", "limit": float64(1), "cursor": cursor})
	if second["total"] != float64(2) || second["count"] != float64(1) || second["truncated"] == true {
		t.Fatalf("registry second page: %v", second)
	}
}
