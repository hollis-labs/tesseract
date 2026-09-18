package mcpadapter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/go-mcp/staleness"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func observationIdentity(data []byte) staleness.Identity {
	h := sha256.Sum256(data)
	return staleness.Identity{Scheme: "sha256", Digest: hex.EncodeToString(h[:]), Product: "fixture/tesseract", Platform: "fixture/platform"}
}

func observationRequest(path string) *mcpsdk.InitializeRequest {
	return &mcpsdk.InitializeRequest{Params: &mcpsdk.InitializeParams{
		ClientInfo: &mcpsdk.Implementation{Name: "agent-mux-proxy", Version: "review"},
		Capabilities: &mcpsdk.ClientCapabilities{Experimental: map[string]any{
			runtimeCapability: map[string]any{
				"schema_version": 1, "mode": "observation-only",
				"owner":    map[string]any{"schema_version": 1, "instance_id": "8a5013c5-a690-497e-940e-59eea16c709a", "pid": 100, "build": map[string]any{"private": "do-not-publish"}},
				"launch":   map[string]any{"selector": path, "selector_kind": "absolute", "resolution": "pre-spawn-path-observation", "relaunch_lookup": "selector", "pid": 200, "resolved_path": "do-not-use-cached-path", "args": []string{"private-argument"}},
				"recovery": map[string]any{"attempts_remaining": 5, "exit_permitted": true, "reservation": "fake-permission"},
			},
		}},
	}}
}

func TestRuntimeObservationRequiresCurrentParentAndLeaf(t *testing.T) {
	path := filepath.Join(t.TempDir(), "image")
	if err := os.WriteFile(path, []byte("original"), 0700); err != nil { // #nosec G306 -- Executable fixture inside t.TempDir; the observer requires an execute bit.
		t.Fatal(err)
	}
	for _, mode := range []string{"valid", "old-owner", "wrong-leaf", "direct-host", "wrong-schema", "path-selector", "relative-selector", "redacted", "missing"} {
		t.Run(mode, func(t *testing.T) {
			req := observationRequest(path)
			raw := req.Params.Capabilities.Experimental[runtimeCapability].(map[string]any)
			switch mode {
			case "old-owner":
				raw["owner"].(map[string]any)["pid"] = 99
			case "wrong-leaf":
				raw["launch"].(map[string]any)["pid"] = 201
			case "direct-host":
				req.Params.ClientInfo.Name = "unknown-host"
			case "wrong-schema":
				raw["schema_version"] = 2
			case "path-selector":
				raw["launch"].(map[string]any)["selector_kind"] = "path"
			case "relative-selector":
				raw["launch"].(map[string]any)["selector"] = "./image"
			case "redacted":
				raw["launch"].(map[string]any)["resolution"] = "redacted"
			case "missing":
				delete(req.Params.Capabilities.Experimental, runtimeCapability)
			}
			called := false
			o := &runtimeObserver{pid: 200, parent: 100, running: observationIdentity([]byte("original")), reason: "launch-context-unavailable", inspect: func(_ context.Context, snapshot string) (staleness.Identity, error) {
				called = true
				b, err := os.ReadFile(snapshot) // #nosec G304 -- Private snapshot supplied by staleness.Observe, inside its temporary directory.
				return observationIdentity(b), err
			}}
			o.accept(req)
			got := o.observe(context.Background(), "display-only")
			if mode == "valid" {
				if got.Observation.State != staleness.Same || !called {
					t.Fatalf("valid owner failed: %+v", got)
				}
			} else if got.Observation.State != staleness.Unknown || called {
				t.Fatalf("unverified context inspected a file: %+v", got)
			}
			encoded, err := json.Marshal(got)
			if err != nil {
				t.Fatal(err)
			}
			for _, private := range []string{"do-not-publish", "private-argument", "fake-permission", "do-not-use-cached-path"} {
				if strings.Contains(string(encoded), private) {
					t.Fatalf("published discarded metadata: %s", private)
				}
			}
			if got.ExitPermitted || got.Reservation != "none" {
				t.Fatalf("owner metadata authorized exit: %+v", got)
			}
		})
	}
}

func TestRuntimeObservationRetainsRunningIdentityAndLaunchContract(t *testing.T) {
	path := filepath.Join(t.TempDir(), "image")
	if err := os.WriteFile(path, []byte("original"), 0700); err != nil { // #nosec G306 -- Executable fixture inside t.TempDir; the observer requires an execute bit.
		t.Fatal(err)
	}
	o := &runtimeObserver{pid: 200, parent: 100, running: observationIdentity([]byte("original")), inspect: func(_ context.Context, snapshot string) (staleness.Identity, error) {
		b, err := os.ReadFile(snapshot) // #nosec G304 -- Private snapshot supplied by staleness.Observe, inside its temporary directory.
		return observationIdentity(b), err
	}}
	o.accept(observationRequest(path))
	if err := os.WriteFile(path, []byte("new-code"), 0700); err != nil { // #nosec G306 -- Replacement executable fixture inside t.TempDir.
		t.Fatal(err)
	}
	// A duplicate initialize cannot switch the retained launch contract.
	o.accept(observationRequest("/different-installation"))
	got := o.observe(context.Background(), "same-release-label")
	if got.Observation.State != staleness.Different || got.Observation.Running != observationIdentity([]byte("original")) || got.Observation.Target.Selector != path {
		t.Fatalf("lost running identity or selector: %+v", got)
	}
	// Native inspection is serialized without queuing another request behind it.
	o.inspectMu.Lock()
	busy := o.observe(context.Background(), "same-release-label")
	o.inspectMu.Unlock()
	if busy.Observation.State != staleness.Unknown || busy.Observation.Reason != "inspection-in-progress" {
		t.Fatalf("busy observer: %+v", busy)
	}
}
