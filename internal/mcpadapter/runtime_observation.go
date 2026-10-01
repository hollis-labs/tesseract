package mcpadapter

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	"github.com/hollis-labs/go-mcp/staleness"
	"github.com/hollis-labs/tesseract/internal/runtimeimage"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const runtimeCapability = "hollis-labs.dev/mcp-runtime"

var (
	processImage     = sync.OnceValues(runtimeimage.Running)
	processIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
)

// Keep only fields used as evidence. Never retain arbitrary build settings,
// arguments, environment values, or an unbounded copy of initialize metadata.
type runtimeOwner struct {
	SchemaVersion int    `json:"schema_version"`
	InstanceID    string `json:"instance_id"`
	PID           int    `json:"pid"`
}

type runtimeContext struct {
	SchemaVersion int          `json:"schema_version"`
	Mode          string       `json:"mode"`
	Owner         runtimeOwner `json:"owner"`
	Launch        struct {
		Selector       string `json:"selector"`
		SelectorKind   string `json:"selector_kind"`
		Resolution     string `json:"resolution"`
		RelaunchLookup string `json:"relaunch_lookup"`
		PID            int    `json:"pid"`
	} `json:"launch"`
}

type runtimeObserver struct {
	mu          sync.Mutex
	inspectMu   sync.Mutex
	running     staleness.Identity
	inspect     staleness.Inspector
	pid         int
	parent      int
	owner       *runtimeOwner
	target      staleness.Target
	reason      string
	initialized bool
}

func newRuntimeObserver() *runtimeObserver {
	identity, _ := processImage() // Missing platform proof stays unknown.
	return &runtimeObserver{running: identity, inspect: runtimeimage.Inspect,
		pid: os.Getpid(), parent: os.Getppid(), reason: "launch-context-unavailable"}
}

// runtimeObserverMiddleware intercepts the "initialize" request so the
// runtime observer can inspect the client's launch-context capability before
// the handshake completes.
//
// go-mcp deliberately does not wrap initialize/session lifecycle (see its
// README: "Prompts, resources, and session lifecycle are not wrapped").
// mark3labs exposed this via server.Hooks.AddBeforeInitialize; the official
// SDK has no equivalent hook, so this installs directly on the SDK server via
// AddReceivingMiddleware — the same install point sanitize.Middleware uses —
// and passes every method through unconditionally, including "initialize"
// itself: this is observation, never a gate.
func runtimeObserverMiddleware(a *Adapter) mcpsdk.Middleware {
	return func(next mcpsdk.MethodHandler) mcpsdk.MethodHandler {
		return func(ctx context.Context, method string, req mcpsdk.Request) (mcpsdk.Result, error) {
			if initReq, ok := req.(*mcpsdk.InitializeRequest); ok {
				a.acceptRuntimeContext(initReq)
			}
			return next(ctx, method, req)
		}
	}
}

func (a *Adapter) acceptRuntimeContext(req *mcpsdk.InitializeRequest) {
	if a.runtime == nil {
		return
	}
	a.runtime.accept(req)
}

func (o *runtimeObserver) accept(req *mcpsdk.InitializeRequest) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.initialized {
		return
	}
	o.initialized = true
	if req.Params == nil || req.Params.Capabilities == nil || req.Params.ClientInfo == nil {
		return
	}
	raw, exists := req.Params.Capabilities.Experimental[runtimeCapability]
	if !exists {
		return
	}
	o.reason = "launch-context-unverified"
	data, err := json.Marshal(raw)
	if err != nil || len(data) > 32<<10 {
		return
	}
	var launch runtimeContext
	if json.Unmarshal(data, &launch) != nil || req.Params.ClientInfo.Name != "tether-proxy" ||
		launch.SchemaVersion != 1 || launch.Mode != "observation-only" || launch.Owner.SchemaVersion != 1 ||
		launch.Owner.PID != o.parent || launch.Launch.PID != o.pid ||
		!processIDPattern.MatchString(launch.Owner.InstanceID) {
		return
	}
	o.owner = &launch.Owner
	// The proxy resolves PATH and relative selectors using its own environment.
	// A pre-spawn resolved_path cannot reconstruct its next lookup. In those
	// cases, report unknown without touching a file.
	if launch.Launch.SelectorKind != "absolute" || launch.Launch.RelaunchLookup != "selector" ||
		launch.Launch.Resolution != "pre-spawn-path-observation" ||
		len(launch.Launch.Selector) > 4096 || !filepath.IsAbs(launch.Launch.Selector) {
		o.reason = "replacement-selector-unverified"
		return
	}
	o.target = staleness.Target{Kind: "absolute", Selector: launch.Launch.Selector}
	o.reason = ""
}

type runtimeReport struct {
	SchemaVersion int                   `json:"schema_version"`
	Mode          string                `json:"mode"`
	PID           int                   `json:"pid"`
	Version       string                `json:"version"`
	ObservedAt    time.Time             `json:"observed_at"`
	Owner         *runtimeOwner         `json:"owner,omitempty"`
	Observation   staleness.Observation `json:"observation"`
	ExitPermitted bool                  `json:"exit_permitted"`
	Reservation   string                `json:"reservation"`
}

func (o *runtimeObserver) observe(ctx context.Context, version string) runtimeReport {
	o.mu.Lock()
	result := runtimeReport{SchemaVersion: 1, Mode: "observation-only", PID: o.pid, Version: version,
		Owner: o.owner, Reservation: "none", Observation: staleness.Observation{
			State: staleness.Unknown, Reason: o.reason, Running: o.running, Target: o.target}}
	o.mu.Unlock()
	// Serialization caps snapshot disk use and native validation work. Another
	// request never queues behind a platform inspector that has not returned.
	if result.Observation.Reason == "" {
		if o.inspectMu.TryLock() {
			defer o.inspectMu.Unlock()
			result.Observation = staleness.Observe(ctx, o.running, result.Observation.Target, o.inspect)
		} else {
			result.Observation.Reason = "inspection-in-progress"
		}
	}
	result.ObservedAt = time.Now().UTC()
	return result
}

func (a *Adapter) registerRuntimeTool(s *toolRegistrar) {
	a.addTool(s, gomcpTool("tesseract_runtime_get",
		"Observe this MCP process's verified running image and the replacement selected by its owning proxy. Returns same, different replacement available, or unknown with evidence. Does not exit, restart, reserve a retry, or change background workers. Missing owner context, unverified wrappers, PATH/relative selectors and unavailable image proof stay unknown.",
		inputSchema(),
		toolAnnotations{},
		func(ctx context.Context, _ map[string]any) (any, error) {
			if a.runtime == nil {
				return toolJSON(runtimeReport{SchemaVersion: 1, Mode: "observation-only", PID: os.Getpid(),
					Version: a.version(), ObservedAt: time.Now().UTC(), Reservation: "none",
					Observation: staleness.Observation{State: staleness.Unknown, Reason: "observer-unavailable"}}), nil
			}
			return toolJSON(a.runtime.observe(ctx, a.version())), nil
		}))
}
