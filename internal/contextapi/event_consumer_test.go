package contextapi

import (
	"context"
	"encoding/json"
	"net/url"
	"testing"

	"github.com/hollis-labs/tesseract/internal/event"
	"github.com/hollis-labs/tesseract/internal/memory"
)

func TestEventConsumerHTTPWorkstreamListAndSinceCursor(t *testing.T) {
	srv := newEventTestServer(t)
	const a = "01M7WQC9BNP2BY914K3H69WHR9"
	const b = "01M7WQC9BNP2BY914K3H69WHRA"
	for i, ws := range []string{a, b, "other"} {
		ns := []string{"project/alpha/event/status", "project/beta/event/status", "project/gamma/event/status"}[i]
		_, err := srv.EventStore.Write(context.Background(), event.WriteInput{Namespace: ns, WorkstreamID: &ws, Summary: "done", Body: "details", Author: memory.Author{AgentID: "dispatcher"}, SessionID: "s", ConsumerState: json.RawMessage(`{"state":"done","subject_urn":"msg://agent/test/a","task_id":"T-1","run_id":"R-1","team_id":"team-1"}`)})
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, selector := range [][]string{{a, b}, {`["` + a + `","` + b + `"]`}} {
		q := url.Values{"namespace": {"project/"}, "stream": {"status"}, "workstream_id": selector, "since": {"2000-01-01T00:00:00Z"}, "limit": {"1"}, "state_filters": {`[{"field":"task_id","values":["T-1"]}]`}}
		var previous string
		for n := 0; n < 2; n++ {
			rr := getEventLog(t, srv, q)
			if rr.Code != 200 {
				t.Fatal(rr.Body.String())
			}
			var wire struct {
				Entries []struct {
					ID           string `json:"id"`
					RevisionID   string `json:"revision_id"`
					At           string `json:"at"`
					WorkstreamID string `json:"workstream_id"`
					TaskID       string `json:"task_id"`
					RunID        string `json:"run_id"`
					TeamID       string `json:"team_id"`
					State        string `json:"state"`
					Summary      string `json:"summary"`
				}
				Manifest memory.EventLogManifest
			}
			if err := json.Unmarshal(rr.Body.Bytes(), &wire); err != nil || len(wire.Entries) != 1 {
				t.Fatalf("page: %s %v", rr.Body.String(), err)
			}
			row := wire.Entries[0]
			if row.ID == "" || row.ID != row.RevisionID || row.ID == previous || row.At == "" || row.TaskID != "T-1" || row.RunID != "R-1" || row.TeamID != "team-1" || row.State != "done" || row.Summary != "done" || (row.WorkstreamID != a && row.WorkstreamID != b) {
				t.Fatal(rr.Body.String())
			}
			previous = row.ID
			if n == 0 {
				if wire.Manifest.NextCursor == nil {
					t.Fatal("missing cursor")
				}
				q.Set("since", *wire.Manifest.NextCursor)
				q["workstream_id"] = []string{b, a, a}
			} else if wire.Manifest.HasMore {
				t.Fatal("list failed to filter before paging")
			}
		}
		q.Set("workstream_id", a)
		if rr := getEventLog(t, srv, q); rr.Code != 400 {
			t.Fatalf("changed list accepted cursor: %s", rr.Body.String())
		}
	}
	for _, value := range []string{"[]", "[null]", "[1]", "[", ""} {
		rr := getEventLog(t, srv, url.Values{"namespace": {"project/"}, "stream": {"status"}, "workstream_id": {value}})
		if rr.Code != 400 {
			t.Fatalf("invalid list %q: %d %s", value, rr.Code, rr.Body.String())
		}
	}
}
