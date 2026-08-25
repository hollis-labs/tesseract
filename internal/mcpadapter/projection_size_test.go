package mcpadapter

// Projection size harness for CW-20260825-0003.
//
// Skipped unless TESS_MEASURE_DB is set, so it never runs in CI. It exists so
// that any figure quoted about payload_mode's effect is re-derivable by
// running one command rather than trusted from a commit message — and so the
// SLICE those figures describe is recorded next to them. A recall byte count
// is meaningless without its namespace set, ranking, and limit.
//
//	cp ~/.local/share/tesseract/workspaces/default/main.db \
//	   /tmp/tessmeasure/data/index/context.db
//	TESS_MEASURE_DB=/tmp/tessmeasure \
//	  go test ./internal/mcpadapter/ -run TestProjectionSize -v
//
// Copy the DB first: contextstore.Open runs migrations, so pointing this at
// a live workspace would mutate it.
//
// Override the slice with TESS_MEASURE_NS (comma-separated) and
// TESS_MEASURE_RANKING. Both are echoed in the output.

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/hollis-labs/tesseract/internal/contextstore"
	"github.com/hollis-labs/tesseract/internal/memory"
	"github.com/mark3labs/mcp-go/mcp"
)

type projSizeItem struct {
	Revision struct {
		RevisionID string `json:"revision_id"`
		Payload    struct {
			Summary string `json:"summary"`
			Body    string `json:"body"`
		} `json:"payload"`
	} `json:"revision"`
}

func TestProjectionSize(t *testing.T) {
	root := os.Getenv("TESS_MEASURE_DB")
	if root == "" {
		t.Skip("TESS_MEASURE_DB not set; see the file comment for the command")
	}

	namespaces := []string{"user/chrispian/memory"}
	if raw := os.Getenv("TESS_MEASURE_NS"); raw != "" {
		namespaces = strings.Split(raw, ",")
	}
	ranking := "activation"
	if raw := os.Getenv("TESS_MEASURE_RANKING"); raw != "" {
		ranking = raw
	}
	nsJSON, err := json.Marshal(namespaces)
	if err != nil {
		t.Fatalf("marshal namespaces: %v", err)
	}

	ctx := context.Background()
	cs, err := contextstore.Open(ctx, contextstore.Config{RootDir: root})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer cs.Close()

	ms := memory.NewStore(cs.DB(), nil, "", 0, memory.NoopQueue{})
	tok, _, err := cs.CreateAuthToken(ctx, contextstore.TokenCreateInput{
		Label: "projection-size", Scopes: []string{"memory:read"},
	})
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	a := New(cs, tok)
	a.MemoryStore = ms

	call := func(mode string, limit int) string {
		req := mcp.CallToolRequest{}
		req.Params.Arguments = map[string]any{
			"namespaces":   string(nsJSON),
			"ranking":      ranking,
			"limit":        float64(limit),
			"payload_mode": mode,
		}
		res, err := a.handleMemoryRecall(ctx, req)
		if err != nil {
			t.Fatalf("recall %s: %v", mode, err)
		}
		return res.Content[0].(mcp.TextContent).Text
	}
	decode := func(raw string) []projSizeItem {
		var out []projSizeItem
		if err := json.Unmarshal([]byte(raw), &out); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		return out
	}
	payloadBytes := func(items []projSizeItem) int {
		n := 0
		for _, it := range items {
			n += len(it.Revision.Payload.Summary) + len(it.Revision.Payload.Body)
		}
		return n
	}

	t.Logf("SLICE namespaces=%s ranking=%s", nsJSON, ranking)

	for _, limit := range []int{30, 500} {
		rawFull, rawSummary, rawKeys := call("full", limit), call("summary", limit), call("keys", limit)
		full, summ, keys := decode(rawFull), decode(rawSummary), decode(rawKeys)

		// The three calls must have returned the same set or no comparison
		// between them means anything.
		if len(full) != len(summ) || len(full) != len(keys) {
			t.Fatalf("limit=%d: set sizes differ (full=%d summary=%d keys=%d)",
				limit, len(full), len(summ), len(keys))
		}
		for i := range full {
			if full[i].Revision.RevisionID != summ[i].Revision.RevisionID ||
				full[i].Revision.RevisionID != keys[i].Revision.RevisionID {
				t.Fatalf("limit=%d: result %d differs across modes", limit, i)
			}
		}

		var sumOnly, bodyOnly int
		for _, it := range full {
			sumOnly += len(it.Revision.Payload.Summary)
			bodyOnly += len(it.Revision.Payload.Body)
		}
		fullPayload := sumOnly + bodyOnly
		summPayload, keysPayload := payloadBytes(summ), payloadBytes(keys)

		missingID := 0
		for _, set := range [][]projSizeItem{full, summ, keys} {
			for _, it := range set {
				if it.Revision.RevisionID == "" {
					missingID++
				}
			}
		}

		t.Logf("--- limit=%d n=%d ---", limit, len(full))
		t.Logf("full    wire=%8d payload=%8d (summary=%7d body=%8d) envelope=%8d",
			len(rawFull), fullPayload, sumOnly, bodyOnly, len(rawFull)-fullPayload)
		t.Logf("summary wire=%8d payload=%8d envelope=%8d (%d B/result)",
			len(rawSummary), summPayload, len(rawSummary)-summPayload, (len(rawSummary)-summPayload)/len(summ))
		t.Logf("keys    wire=%8d payload=%8d envelope=%8d (%d B/result)",
			len(rawKeys), keysPayload, len(rawKeys)-keysPayload, (len(rawKeys)-keysPayload)/len(keys))
		t.Logf("AC1 summary payload(%d) == sum(payload_summary) over returned set(%d) -> %v",
			summPayload, sumOnly, summPayload == sumOnly)
		t.Logf("keys payload == 0 -> %v ; revision_id missing in any mode: %d",
			keysPayload == 0, missingID)
		t.Logf("wire reduction vs full: summary %.2fx  keys %.2fx",
			float64(len(rawFull))/float64(len(rawSummary)),
			float64(len(rawFull))/float64(len(rawKeys)))

		// AC1 is an exact identity, not a threshold — assert it here so the
		// harness fails loudly rather than printing a wrong number.
		if summPayload != sumOnly {
			t.Errorf("limit=%d: AC1 violated: summary payload %d != sum(payload_summary) %d",
				limit, summPayload, sumOnly)
		}
		if keysPayload != 0 {
			t.Errorf("limit=%d: keys mode carried %d bytes of payload text", limit, keysPayload)
		}
		if missingID != 0 {
			t.Errorf("limit=%d: %d results lack revision_id", limit, missingID)
		}
	}
}
