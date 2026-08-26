// CLI guards for the packet payload vocabulary change of CW-20260825-0011.
//
// `context packet -payload-mode head_only` used to cut each payload at 512
// bytes mid-JSON; every other value fell through to full payloads silently.
// Byte capping is now -payload-max-bytes, and -payload-mode accepts only
// "full" so the flag cannot quietly do nothing.
package contextcli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func seedPacketRecord(t *testing.T, cli *CLI, ns, key, payload string) {
	t.Helper()
	if code := cli.Run(context.Background(), []string{
		"context", "put",
		"--client-id", "test",
		"--actor", "user",
		"--namespace", ns,
		"--key", key,
		"--json", payload,
	}); code != 0 {
		t.Fatalf("seed put failed: stderr=%q", cli.Stderr)
	}
}

// TestCLIPacketRetiredPayloadModeIsRejected pins the fail-closed half.
func TestCLIPacketRetiredPayloadModeIsRejected(t *testing.T) {
	cli, out, errOut := newTestCLI(t)
	seedPacketRecord(t, cli, "user/memory/cli-cap", "doc", `{"v":1}`)

	for _, mode := range []string{"head_only", "keys", "bogus"} {
		t.Run(mode, func(t *testing.T) {
			out.Reset()
			errOut.Reset()
			code := cli.Run(context.Background(), []string{
				"context", "packet", "--namespace", "user/memory/cli-cap",
				"--no-pins", "--payload-mode", mode, "--output", "json",
			})
			if code == 0 {
				t.Fatalf("-payload-mode=%s exited 0; it must be rejected. stdout: %s", mode, out.String())
			}
			if !strings.Contains(errOut.String(), "payload-max-bytes") {
				t.Errorf("rejection does not name the replacement flag: %s", errOut.String())
			}
		})
	}

	// Positive control: "full" is accepted, so the rejections are about the
	// retired values and not about the flag being refused outright.
	out.Reset()
	errOut.Reset()
	if code := cli.Run(context.Background(), []string{
		"context", "packet", "--namespace", "user/memory/cli-cap",
		"--no-pins", "--payload-mode", "full", "--output", "json",
	}); code != 0 {
		t.Fatalf("-payload-mode=full exited non-zero: %s", errOut.String())
	}
}

// TestCLIPacketPayloadMaxBytesCaps pins the replacement: a capped item carries
// payload_head rather than a shortened, unparseable payload.
func TestCLIPacketPayloadMaxBytesCaps(t *testing.T) {
	cli, out, errOut := newTestCLI(t)
	seedPacketRecord(t, cli, "user/memory/cli-cap", "doc", `{"body":"`+strings.Repeat("x", 600)+`"}`)

	out.Reset()
	errOut.Reset()
	if code := cli.Run(context.Background(), []string{
		"context", "packet", "--namespace", "user/memory/cli-cap",
		"--no-pins", "--payload-max-bytes", "64", "--output", "json",
	}); code != 0 {
		t.Fatalf("packet failed: %s", errOut.String())
	}

	var body map[string]any
	if err := json.Unmarshal(out.Bytes(), &body); err != nil {
		t.Fatalf("capped output is not valid JSON (%v): %s", err, out.String())
	}
	items, ok := body["items"].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("items = %v", body["items"])
	}
	item := items[0].(map[string]any)
	if _, present := item["payload"]; present {
		t.Errorf("a capped item must not carry `payload`; got %v", item)
	}
	if head, _ := item["payload_head"].(string); len(head) != 64 {
		t.Errorf("payload_head = %d bytes, want 64", len(item["payload_head"].(string)))
	}
	if item["payload_truncated"] != true {
		t.Errorf("payload_truncated = %v, want true", item["payload_truncated"])
	}

	// Positive control: uncapped, the same record comes back with a payload.
	out.Reset()
	if code := cli.Run(context.Background(), []string{
		"context", "packet", "--namespace", "user/memory/cli-cap",
		"--no-pins", "--output", "json",
	}); code != 0 {
		t.Fatalf("uncapped packet failed: %s", errOut.String())
	}
	var plain map[string]any
	if err := json.Unmarshal(out.Bytes(), &plain); err != nil {
		t.Fatalf("uncapped output is not valid JSON: %v", err)
	}
	only := plain["items"].([]any)[0].(map[string]any)
	if _, present := only["payload"]; !present {
		t.Fatalf("uncapped item lost its payload: %v", only)
	}
}

// TestCLIPacketUncappedPayloadIsRawJSONNotBase64 pins the SHAPE of an uncapped
// payload, not merely its presence.
//
// `item["payload"]` holds a json.RawMessage, which encodes as the record's own
// JSON. Handing encoding/json a plain []byte instead encodes it as a base64
// STRING — still present, still a value under the same key, and a presence-only
// assertion cannot tell the two apart. This test decodes the field as an object
// and reads a key out of it, so only the raw form passes.
func TestCLIPacketUncappedPayloadIsRawJSONNotBase64(t *testing.T) {
	cli, out, errOut := newTestCLI(t)
	seedPacketRecord(t, cli, "user/memory/cli-shape", "doc", `{"marker":"visible","n":7}`)

	out.Reset()
	errOut.Reset()
	if code := cli.Run(context.Background(), []string{
		"context", "packet", "--namespace", "user/memory/cli-shape",
		"--no-pins", "--output", "json",
	}); code != 0 {
		t.Fatalf("packet failed: %s", errOut.String())
	}

	var body map[string]any
	if err := json.Unmarshal(out.Bytes(), &body); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	item := body["items"].([]any)[0].(map[string]any)

	payload, ok := item["payload"].(map[string]any)
	if !ok {
		t.Fatalf("payload is %T, want a JSON object — a base64 string here means the raw form was lost: %v",
			item["payload"], item["payload"])
	}
	if payload["marker"] != "visible" {
		t.Errorf("payload.marker = %v, want \"visible\"", payload["marker"])
	}
	if n, _ := payload["n"].(float64); int(n) != 7 {
		t.Errorf("payload.n = %v, want 7", payload["n"])
	}
}
