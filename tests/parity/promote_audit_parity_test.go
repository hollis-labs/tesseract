// Promote-stage audit parity across all three doors.
//
// The sibling parity tests in this package check that an operation is
// *reachable* on both surfaces. This one checks something the catalog cannot
// see: that the two doors, plus the CLI, describe the same action with the
// same word once it lands in context_audit.
//
// Before CW-20260419-0058 they did not. The HTTP handlers emitted
// "promote.request.created" and "promote.request.approved", MCP emitted
// "promote.request" and "promote.approve", and the CLI emitted nothing at all
// for those two stages — three surfaces, three answers, no error anywhere. An
// operator filtering context_audit for approvals saw one door's worth and had
// no way to tell. The event types are now unified on the short MCP forms, with
// apply keeping the "promote" both surfaces already agreed on.
//
// The rename shipped without a data migration, deliberately: a scan of the
// live store found zero rows with any promote% event_type across 6,566 audit
// events, so neither spelling had ever been persisted and there was nothing to
// rewrite. See internal/contextstore/audittypes.go.
//
// These tests drive the real emit path on each surface — the HTTP router, the
// registered MCP tool over an in-process JSON-RPC client, and the CLI command
// dispatcher — rather than calling EmitPromote directly, so a divergence
// reintroduced at any one call site fails here. Metadata contains the IDs and
// source/target coordinates needed to reconstruct the lifecycle. It
// deliberately has no surface-only marker: the actor and stored request record
// already provide attribution, while a key emitted by only one door breaks the
// shared row schema.
package parity

import (
	"bytes"
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	mcpclient "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/hollis-labs/tesseract/internal/contextapi"
	"github.com/hollis-labs/tesseract/internal/contextcli"
	"github.com/hollis-labs/tesseract/internal/contextpolicy"
	"github.com/hollis-labs/tesseract/internal/contextstore"
	"github.com/hollis-labs/tesseract/internal/knowledge"
	"github.com/hollis-labs/tesseract/internal/mcpadapter"
	"github.com/hollis-labs/tesseract/internal/memory"
)

// promoteStageEvents records the full promote-family audit rows a surface
// emitted during each promotion stage, together with the lifecycle IDs needed
// to prove that subjects and metadata point at the records they claim to.
// Slices keep "emitted nothing" and "emitted twice" visible failures.
type promoteStageEvents struct {
	Request []contextstore.AuditEvent
	Approve []contextstore.AuditEvent
	Apply   []contextstore.AuditEvent

	store            *contextstore.Store
	RequestID        string
	ApprovalID       string
	RequestNamespace string
	SourceNamespace  string
	SourceKey        string
	TargetNamespace  string
	TargetKey        string
}

// stage returns the events for a named stage, so assertions can loop over the
// three stages instead of repeating themselves three times.
func (p promoteStageEvents) stage(name string) []contextstore.AuditEvent {
	switch name {
	case "request":
		return p.Request
	case "approve":
		return p.Approve
	case "apply":
		return p.Apply
	}
	return nil
}

var promoteStageNames = []string{"request", "approve", "apply"}

// canonicalPromoteStageEvents is what every surface must emit. Apply keeps the
// bare "promote" it has always had on all three doors; renaming the one name
// that was already consistent would churn the most-used value for symmetry
// alone.
var canonicalPromoteStageEvents = map[string][]string{
	"request": {contextstore.EventPromoteRequest},
	"approve": {contextstore.EventPromoteApprove},
	"apply":   {contextstore.EventPromote},
}

// auditProbe reports the promote-family audit events added to a store since
// the last call, oldest-first. Diffing around each stage attributes an event
// to the stage that produced it, which is what lets the assertions distinguish
// "the approve stage emitted the wrong name" from "the approve stage emitted
// nothing and some other stage emitted twice".
type auditProbe struct {
	store  *contextstore.Store
	lastID int64
}

func newAuditProbe(t *testing.T, s *contextstore.Store) *auditProbe {
	t.Helper()
	p := &auditProbe{store: s}
	p.since(t) // drain anything the fixture wrote while seeding
	return p
}

// since returns promote-family audit rows recorded since the previous call.
// The "promote" prefix deliberately catches the retired spellings too
// ("promote.request.created", "promote.request.approved"), so a regression
// shows up as a wrong name rather than as an empty stage. It does not match
// status_promote or memory.promote, which are unrelated events.
func (p *auditProbe) since(t *testing.T) []contextstore.AuditEvent {
	t.Helper()
	evs, _, err := p.store.QueryAuditEvents(context.Background(), contextstore.AuditQuery{Limit: 200})
	if err != nil {
		t.Fatalf("query audit events: %v", err)
	}
	var out []contextstore.AuditEvent
	maxID := p.lastID
	// QueryAuditEvents returns newest-first; walk backwards for chronological order.
	for i := len(evs) - 1; i >= 0; i-- {
		ev := evs[i]
		if ev.ID <= p.lastID {
			continue
		}
		if ev.ID > maxID {
			maxID = ev.ID
		}
		if strings.HasPrefix(ev.EventType, "promote") {
			out = append(out, ev)
		}
	}
	p.lastID = maxID
	return out
}

// ── Surface drivers ─────────────────────────────────────────────────────

// newPromoteStore opens a store and seeds the record every surface promotes.
func newPromoteStore(t *testing.T, sourceNS, sourceKey string) *contextstore.Store {
	t.Helper()
	cs, err := contextstore.Open(context.Background(), contextstore.Config{RootDir: t.TempDir()})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	if _, err := cs.AppendRecord(context.Background(), contextstore.AppendInput{
		Namespace: sourceNS,
		Key:       sourceKey,
		Actor:     "app:fixture",
		Payload:   json.RawMessage(`{"text":"ready to promote"}`),
	}); err != nil {
		t.Fatalf("seed source record: %v", err)
	}
	return cs
}

// drivePromoteOverHTTP runs request → approve → apply through the HTTP router.
func drivePromoteOverHTTP(t *testing.T) promoteStageEvents {
	t.Helper()
	const sourceNS, sourceKey = "app/httpdoor/session", "summary"
	cs := newPromoteStore(t, sourceNS, sourceKey)
	srv := contextapi.NewServer(cs, contextpolicy.New())
	// No AuthToken and no ManagedAuth: with no auth mode configured the scope
	// guards pass. Authorization is not what this test is measuring, and the
	// promote.* scope names are a different namespace from the event types.
	probe := newAuditProbe(t, cs)

	post := func(path string, body map[string]any) map[string]any {
		t.Helper()
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal %s body: %v", path, err)
		}
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		srv.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("POST %s: status %d, body %s", path, rr.Code, rr.Body.String())
		}
		var out map[string]any
		if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode %s response %q: %v", path, rr.Body.String(), err)
		}
		return out
	}

	got := promoteStageEvents{
		store:            cs,
		RequestNamespace: "app/httpdoor/promotions",
		SourceNamespace:  sourceNS,
		SourceKey:        sourceKey,
		TargetNamespace:  "user/notes",
		TargetKey:        "http-summary",
	}
	reqBody := post("/v1/context/promote/request", map[string]any{
		"actor":            "app:httpdoor",
		"client_id":        "httpdoor",
		"source_namespace": sourceNS,
		"source_key":       sourceKey,
		"target_namespace": "user/notes",
		"target_key":       "http-summary",
		"reason":           "cross-surface audit parity",
	})
	got.Request = probe.since(t)

	requestID, _ := reqBody["request_id"].(string)
	if requestID == "" {
		t.Fatalf("HTTP promote/request returned no request_id: %v", reqBody)
	}
	got.RequestID = requestID

	approveBody := post("/v1/context/promote/approve", map[string]any{
		"actor": "user", "request_id": requestID, "notes": "ok",
	})
	got.Approve = probe.since(t)
	got.ApprovalID, _ = approveBody["approval_id"].(string)
	if got.ApprovalID == "" {
		t.Fatalf("HTTP promote/approve returned no approval_id: %v", approveBody)
	}

	post("/v1/context/promote/apply", map[string]any{
		"actor": "user", "request_id": requestID,
	})
	got.Apply = probe.since(t)
	return got
}

// drivePromoteOverMCP runs the three stages through the registered
// context_promote tool, over mcp-go's in-process transport. Going through the
// registered tool rather than the handler method keeps this honest about what
// an agent actually reaches.
func drivePromoteOverMCP(t *testing.T) promoteStageEvents {
	t.Helper()
	const sourceNS, sourceKey = "app/mcpdoor/session", "summary"
	cs := newPromoteStore(t, sourceNS, sourceKey)

	token, _, err := cs.CreateAuthToken(context.Background(), contextstore.TokenCreateInput{
		Label:          "promote-parity",
		Scopes:         []string{"write", "promote.request", "promote.approve", "promote.apply"},
		NamespaceGlobs: []string{"*"},
		TTL:            time.Hour,
	})
	if err != nil {
		t.Fatalf("create auth token: %v", err)
	}
	mem := memory.NewStore(cs.DB(), nil, "", 0, memory.NoopQueue{})
	adapter := &mcpadapter.Adapter{Store: cs, Token: token, MemoryStore: mem, KnowledgeStore: knowledge.New(mem)}

	mcpSrv := server.NewMCPServer("promote-parity", "0.0.0", server.WithToolCapabilities(true))
	adapter.RegisterAllTools(mcpSrv)

	cl, err := mcpclient.NewInProcessClient(mcpSrv)
	if err != nil {
		t.Fatalf("in-process MCP client: %v", err)
	}
	t.Cleanup(func() { _ = cl.Close() })
	if err := cl.Start(context.Background()); err != nil {
		t.Fatalf("start MCP client: %v", err)
	}
	initReq := mcp.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{Name: "promote-parity", Version: "0.0.0"}
	if _, err := cl.Initialize(context.Background(), initReq); err != nil {
		t.Fatalf("initialize MCP client: %v", err)
	}

	probe := newAuditProbe(t, cs)

	call := func(args map[string]any) map[string]any {
		t.Helper()
		req := mcp.CallToolRequest{}
		req.Params.Name = "context_promote"
		req.Params.Arguments = args
		res, err := cl.CallTool(context.Background(), req)
		if err != nil {
			t.Fatalf("context_promote %v: %v", args["stage"], err)
		}
		if len(res.Content) == 0 {
			t.Fatalf("context_promote %v returned no content", args["stage"])
		}
		text, ok := res.Content[0].(mcp.TextContent)
		if !ok {
			t.Fatalf("context_promote %v returned %T, want TextContent", args["stage"], res.Content[0])
		}
		var out map[string]any
		if err := json.Unmarshal([]byte(text.Text), &out); err != nil {
			t.Fatalf("decode context_promote %v result %q: %v", args["stage"], text.Text, err)
		}
		if code, bad := out["code"].(string); bad {
			t.Fatalf("context_promote %v failed: %s — %v", args["stage"], code, out["message"])
		}
		return out
	}

	got := promoteStageEvents{
		store:            cs,
		RequestNamespace: "app/mcp-agent/promotions",
		SourceNamespace:  sourceNS,
		SourceKey:        sourceKey,
		TargetNamespace:  "user/notes",
		TargetKey:        "mcp-summary",
	}
	reqBody := call(map[string]any{
		"stage":            "request",
		"source_namespace": sourceNS,
		"source_key":       sourceKey,
		"target_namespace": "user/notes",
		"target_key":       "mcp-summary",
	})
	got.Request = probe.since(t)

	requestID, _ := reqBody["request_id"].(string)
	if requestID == "" {
		t.Fatalf("MCP promote request returned no request_id: %v", reqBody)
	}
	got.RequestID = requestID

	approveBody := call(map[string]any{"stage": "approve", "request_id": requestID})
	got.Approve = probe.since(t)
	got.ApprovalID, _ = approveBody["approval_id"].(string)
	if got.ApprovalID == "" {
		t.Fatalf("MCP promote approve returned no approval_id: %v", approveBody)
	}

	call(map[string]any{"stage": "apply", "request_id": requestID})
	got.Apply = probe.since(t)
	return got
}

// drivePromoteOverCLI runs the three stages through the CLI dispatcher.
func drivePromoteOverCLI(t *testing.T) promoteStageEvents {
	t.Helper()
	const sourceNS, sourceKey = "app/clidoor/session", "summary"
	cs := newPromoteStore(t, sourceNS, sourceKey)

	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cli := &contextcli.CLI{Store: cs, Policy: contextpolicy.New(), Stdout: stdout, Stderr: stderr}
	probe := newAuditProbe(t, cs)

	run := func(args ...string) string {
		t.Helper()
		stdout.Reset()
		stderr.Reset()
		if code := cli.Run(context.Background(), args); code != 0 {
			t.Fatalf("cli %v: exit %d, stderr %s", args, code, stderr.String())
		}
		return stdout.String()
	}

	got := promoteStageEvents{
		store:            cs,
		RequestNamespace: "app/clidoor/promotions",
		SourceNamespace:  sourceNS,
		SourceKey:        sourceKey,
		TargetNamespace:  "user/notes",
		TargetKey:        "cli-summary",
	}
	out := run("context", "promote", "request",
		"--actor", "app:clidoor", "--client-id", "clidoor",
		"--source-namespace", sourceNS, "--source-key", sourceKey,
		"--target-namespace", "user/notes", "--target-key", "cli-summary",
		"--reason", "cross-surface audit parity")
	got.Request = probe.since(t)

	requestID := parseCLIRequestID(t, out)
	got.RequestID = requestID

	out = run("context", "promote", "approve", requestID, "--actor", "user", "--notes", "ok")
	got.Approve = probe.since(t)
	got.ApprovalID = parseCLIField(t, out, "Approval ID:")

	run("context", "promote", "apply", requestID, "--actor", "user")
	got.Apply = probe.since(t)
	return got
}

// parseCLIRequestID pulls the request id out of the human-readable
// "  Request ID:  req-…" line the CLI prints; the CLI has no JSON output mode
// for this command.
func parseCLIRequestID(t *testing.T, output string) string {
	t.Helper()
	return parseCLIField(t, output, "Request ID:")
}

func parseCLIField(t *testing.T, output, label string) string {
	t.Helper()
	for _, line := range strings.Split(output, "\n") {
		if _, rest, ok := strings.Cut(line, label); ok {
			if id := strings.TrimSpace(rest); id != "" {
				return id
			}
		}
	}
	t.Fatalf("no %s value in CLI output: %s", label, output)
	return ""
}

// ── Tests ───────────────────────────────────────────────────────────────

// TestPromoteAuditEventTypesAgreeAcrossSurfaces started as the acceptance
// criterion for CW-20260419-0058's event-name unification. CW-20260904-0115
// extends the same invariant to the rest of each row: every stage uses the
// same kind of subject and the same metadata schema on every surface.
func TestPromoteAuditEventTypesAgreeAcrossSurfaces(t *testing.T) {
	surfaces := []struct {
		name  string
		drive func(*testing.T) promoteStageEvents
	}{
		{"HTTP", drivePromoteOverHTTP},
		{"MCP", drivePromoteOverMCP},
		{"CLI", drivePromoteOverCLI},
	}

	observed := make(map[string]promoteStageEvents, len(surfaces))
	for _, s := range surfaces {
		observed[s.name] = s.drive(t)
	}

	// Each surface emits exactly the canonical name, exactly once, per stage.
	for _, s := range surfaces {
		for _, stage := range promoteStageNames {
			got := eventTypes(observed[s.name].stage(stage))
			want := canonicalPromoteStageEvents[stage]
			if !equalStrings(got, want) {
				t.Errorf("%s surface, %s stage: emitted %v, want %v", s.name, stage, got, want)
			}
		}
		assertPromoteAuditDetails(t, s.name, observed[s.name])
	}

	// And the surfaces agree with each other. Redundant while the block above
	// passes, load-bearing the moment someone changes a constant and updates
	// only the door they were working on.
	for _, stage := range promoteStageNames {
		base := eventTypes(observed["HTTP"].stage(stage))
		for _, s := range surfaces[1:] {
			if got := eventTypes(observed[s.name].stage(stage)); !equalStrings(got, base) {
				t.Errorf("%s stage diverged: HTTP emitted %v but %s emitted %v — "+
					"the same action must carry the same event_type on every surface", stage, base, s.name, got)
			}
		}
	}
}

// TestCLIPromoteEmitsAtEveryStage guards the hole CW-20260419-0058 closed on
// the CLI specifically: `tesseract context promote request` and `... approve`
// used to write their records with AppendRecord and emit no audit event at
// all, so two thirds of a CLI-initiated promotion left no trace in
// context_audit. Only apply was visible. The cross-surface test above would
// also catch a regression here, but this one names the failure.
func TestCLIPromoteEmitsAtEveryStage(t *testing.T) {
	got := drivePromoteOverCLI(t)
	for _, stage := range promoteStageNames {
		evs := got.stage(stage)
		if len(evs) == 0 {
			t.Errorf("CLI %s stage emitted no audit event — a CLI-initiated promotion "+
				"must be reconstructable from context_audit, not just its apply step", stage)
			continue
		}
		if gotTypes := eventTypes(evs); !equalStrings(gotTypes, canonicalPromoteStageEvents[stage]) {
			t.Errorf("CLI %s stage: emitted %v, want %v", stage, gotTypes, canonicalPromoteStageEvents[stage])
		}
	}
}

func assertPromoteAuditDetails(t *testing.T, surface string, got promoteStageEvents) {
	t.Helper()

	checks := []struct {
		stage     string
		namespace string
		key       string
		revision  int64
		metadata  func(contextstore.AuditEvent) map[string]string
	}{
		{
			stage: "request", namespace: got.RequestNamespace, key: got.RequestID, revision: 1,
			metadata: func(contextstore.AuditEvent) map[string]string {
				return map[string]string{
					"request_id":       got.RequestID,
					"source_namespace": got.SourceNamespace,
					"source_key":       got.SourceKey,
					"target_namespace": got.TargetNamespace,
					"target_key":       got.TargetKey,
				}
			},
		},
		{
			stage: "approve", namespace: got.RequestNamespace, key: got.RequestID, revision: 2,
			metadata: func(contextstore.AuditEvent) map[string]string {
				return map[string]string{
					"request_id":  got.RequestID,
					"approval_id": got.ApprovalID,
				}
			},
		},
		{
			stage: "apply", namespace: got.TargetNamespace, key: got.TargetKey, revision: 1,
			metadata: func(ev contextstore.AuditEvent) map[string]string {
				return map[string]string{
					"request_id":  got.RequestID,
					"approval_id": got.ApprovalID,
					"record_id":   ev.RecordID,
				}
			},
		},
	}

	for _, check := range checks {
		events := got.stage(check.stage)
		if len(events) != 1 {
			// The event-type assertion reports the names; avoid indexing an empty
			// or ambiguous stage and obscuring that first failure with a panic.
			continue
		}
		ev := events[0]
		if ev.Namespace != check.namespace || ev.Key != check.key || ev.Revision != check.revision {
			t.Errorf("%s surface, %s stage: subject = %s/%s rev %d, want %s/%s rev %d",
				surface, check.stage, ev.Namespace, ev.Key, ev.Revision,
				check.namespace, check.key, check.revision)
		}
		if ev.RecordID == "" {
			t.Errorf("%s surface, %s stage: audit subject has no record_id", surface, check.stage)
		} else if rec, err := got.store.GetByRecordID(context.Background(), ev.RecordID); err != nil {
			t.Errorf("%s surface, %s stage: audited record_id %q is not stored: %v", surface, check.stage, ev.RecordID, err)
		} else if rec.Namespace != ev.Namespace || rec.Key != ev.Key || rec.Revision != ev.Revision {
			t.Errorf("%s surface, %s stage: audit subject %s/%s rev %d does not match record_id %s (%s/%s rev %d)",
				surface, check.stage, ev.Namespace, ev.Key, ev.Revision,
				ev.RecordID, rec.Namespace, rec.Key, rec.Revision)
		}

		var metadata map[string]string
		if err := json.Unmarshal(ev.Metadata, &metadata); err != nil {
			t.Errorf("%s surface, %s stage: decode metadata %q: %v", surface, check.stage, ev.Metadata, err)
			continue
		}
		wantMetadata := check.metadata(ev)
		if !maps.Equal(metadata, wantMetadata) {
			t.Errorf("%s surface, %s stage: metadata = %v, want exact schema %v",
				surface, check.stage, metadata, wantMetadata)
		}
	}
}

func eventTypes(events []contextstore.AuditEvent) []string {
	out := make([]string, 0, len(events))
	for _, ev := range events {
		out = append(out, ev.EventType)
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
