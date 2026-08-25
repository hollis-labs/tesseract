package mcpadapter

// Projection size harness for CW-20260825-0003.
//
// Skipped unless TESS_MEASURE_DB is set, so it never runs in CI. It exists so
// that any figure quoted about payload_mode's effect is re-derivable by
// running one command rather than trusted from a commit message — and so the
// SLICE those figures describe is recorded next to them. A recall byte count
// is meaningless without its namespace set, ranking, and limit.
//
//	mkdir -p /tmp/tessmeasure/data/index
//	sqlite3 -readonly ~/.local/share/tesseract/workspaces/default/main.db \
//	  ".backup /tmp/tessmeasure/data/index/context.db"
//	TESS_MEASURE_DB=/tmp/tessmeasure \
//	  go test ./internal/mcpadapter/ -run TestProjectionSize -v
//
// Snapshot the DB first, and snapshot it with `.backup` — not `cp`.
//
// contextstore.Open runs migrations, so pointing this at a live workspace
// would mutate it. `.backup` over a read-only connection leaves the source
// untouched (verified: mtimes on main.db, -wal and -shm are unchanged after).
//
// `cp main.db` is worse than it looks: it copies the file WITHOUT its -wal
// sidecar, so every committed row still sitting in the write-ahead log is
// silently missing. Measured against this workspace, `cp` reported 1622
// revisions and 2,892,188 body bytes where `.backup` reported 1639 and
// 2,955,792 — 17 revisions and 63,604 bytes invisible, with no error and no
// warning. A figure derived from a `cp` snapshot is not reproducible, and
// nothing about the output says so.
//
// Override the slice with TESS_MEASURE_NS (comma-separated) and
// TESS_MEASURE_RANKING. Both are echoed in the output.
//
// CW-20260825-0004 added two things here. TestProjectionSize now measures the
// RESULTS ARRAY rather than the whole tool response, so its figures and
// manifest.bytes_returned are the same quantity and are asserted equal. And
// TestRecallCostCurve measures the cost of a limit against the store directly,
// below the surface where MaxRecallLimitFull clamps payload_mode=full — the
// cost of 500 full results is a property of the corpus, and it has to stay
// derivable after the surface stopped serving it, since it is the evidence
// the clamp rests on.

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

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

	// call returns the results array only, not the whole envelope. That is
	// the quantity budget_bytes bounds and the quantity manifest.bytes_returned
	// reports, so the figures below and the manifest are the same measurement
	// taken two ways — the assertion at the end of each loop binds them.
	var lastManifest memory.Manifest
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
		var env struct {
			Results json.RawMessage `json:"results"`
			Mani    memory.Manifest `json:"manifest"`
		}
		raw := res.Content[0].(mcp.TextContent).Text
		if err := json.Unmarshal([]byte(raw), &env); err != nil {
			t.Fatalf("recall %s: unmarshal envelope: %v", mode, err)
		}
		lastManifest = env.Mani
		return string(env.Results)
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

	// Both limits sit at or under MaxRecallLimitFull, so all three modes
	// return the same set and every cross-mode identity below stays
	// meaningful. The cost of a limit ABOVE that cap is measured by
	// TestRecallCostCurve, which goes under the surface.
	for _, limit := range []int{30, memory.MaxRecallLimitFull} {
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

		// CW-20260825-0004: manifest.bytes_returned must equal the size of
		// the array it describes. lastManifest is from the final call in the
		// triple above, which is keys — comparing it against anything else
		// would be comparing two different responses.
		t.Logf("manifest(keys) results_total=%d results_returned=%d bytes_returned=%d truncated=%v reason=%q",
			lastManifest.ResultsTotal, lastManifest.ResultsReturned,
			lastManifest.BytesReturned, lastManifest.Truncated, lastManifest.TruncationReason)
		if lastManifest.BytesReturned != len(rawKeys) {
			t.Errorf("limit=%d: manifest.bytes_returned %d != measured keys array %d",
				limit, lastManifest.BytesReturned, len(rawKeys))
		}
		if lastManifest.ResultsReturned != len(keys) {
			t.Errorf("limit=%d: manifest.results_returned %d != decoded keys results %d",
				limit, lastManifest.ResultsReturned, len(keys))
		}
	}
}

// TestRecallCostCurve measures what a limit costs per payload_mode against the
// store directly, bypassing the surface where MaxRecallLimitFull clamps full.
//
// This is the evidence behind that clamp, and it has to outlive it: once the
// tool refuses limit=500 under full, the only way to re-derive "500 full
// results cost X bytes" is to ask the layer underneath. Ship the number with
// the corpus it was measured on — revision count and max created_at are
// printed first for exactly that reason.
//
//	sqlite3 -readonly ~/.local/share/tesseract/workspaces/default/main.db \
//	  ".backup /tmp/tessmeasure/data/index/context.db"
//	TESS_MEASURE_DB=/tmp/tessmeasure \
//	  go test ./internal/mcpadapter/ -run TestRecallCostCurve -v
func TestRecallCostCurve(t *testing.T) {
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

	ctx := context.Background()
	cs, err := contextstore.Open(ctx, contextstore.Config{RootDir: root})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer cs.Close()
	ms := memory.NewStore(cs.DB(), nil, "", 0, memory.NoopQueue{})

	// Corpus stamp. A byte count over mutable data is repeatable without
	// being reproducible; these two numbers are what make it the latter.
	var revCount int
	var maxCreated string
	if err := cs.DB().QueryRowContext(ctx,
		`SELECT COUNT(*), COALESCE(MAX(created_at), '') FROM memory_revisions`).
		Scan(&revCount, &maxCreated); err != nil {
		t.Fatalf("corpus stamp: %v", err)
	}
	t.Logf("CORPUS revisions=%d max_created_at=%s", revCount, maxCreated)
	t.Logf("SLICE namespaces=%v ranking=%s", namespaces, ranking)
	t.Logf("CAPS max=%d max_full=%d default=%d",
		memory.MaxRecallLimit, memory.MaxRecallLimitFull, memory.DefaultRecallLimit)

	for _, limit := range []int{30, 100, 500} {
		page, err := ms.RecallPage(ctx, memory.RecallInput{
			Namespaces: namespaces,
			Ranking:    memory.Ranking(ranking),
			Limit:      limit,
		})
		if err != nil {
			t.Fatalf("limit=%d: recall: %v", limit, err)
		}
		for _, mode := range []memory.PayloadMode{
			memory.PayloadModeFull, memory.PayloadModeSummary, memory.PayloadModeKeys,
		} {
			raw, err := json.Marshal(memory.ProjectResults(page.Results, mode))
			if err != nil {
				t.Fatalf("limit=%d mode=%s: marshal: %v", limit, mode, err)
			}
			per := 0
			if len(page.Results) > 0 {
				per = len(raw) / len(page.Results)
			}
			t.Logf("limit=%-3d n=%-3d total=%-5d mode=%-7s bytes=%8d (%5d B/result)",
				limit, len(page.Results), page.Total, mode, len(raw), per)
		}
	}
}

// TestChronologicalLogOnRealCorpus checks the composition claim at scale.
//
// CW-20260825-0004 asserts that ranking=chronological + payload_mode=summary +
// cursor IS the linear history/log the episodic domain lacks, and that no
// separate log tool is therefore needed. A seeded unit test
// (TestChronologicalLogComposition in internal/memory) proves the mechanics on
// a dozen rows; this one streams a real corpus through the MCP tool to check
// that the properties a log needs — strict newest-first order ACROSS page
// boundaries, every entry exactly once, terminates — survive thousands of rows
// and real timestamps.
//
//	sqlite3 -readonly ~/.local/share/tesseract/workspaces/default/main.db \
//	  ".backup /tmp/tessmeasure/data/index/context.db"
//	TESS_MEASURE_DB=/tmp/tessmeasure \
//	  go test ./internal/mcpadapter/ -run TestChronologicalLogOnRealCorpus -v
func TestChronologicalLogOnRealCorpus(t *testing.T) {
	root := os.Getenv("TESS_MEASURE_DB")
	if root == "" {
		t.Skip("TESS_MEASURE_DB not set; see the file comment for the command")
	}

	namespaces := []string{"user/chrispian/memory"}
	if raw := os.Getenv("TESS_MEASURE_NS"); raw != "" {
		namespaces = strings.Split(raw, ",")
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

	var revCount int
	var maxCreated string
	err = cs.DB().QueryRowContext(ctx,
		`SELECT COUNT(*), COALESCE(MAX(created_at), '') FROM memory_revisions`).
		Scan(&revCount, &maxCreated)
	if err != nil {
		t.Fatalf("corpus stamp: %v", err)
	}
	t.Logf("CORPUS revisions=%d max_created_at=%s", revCount, maxCreated)

	tok, _, err := cs.CreateAuthToken(ctx, contextstore.TokenCreateInput{
		Label: "chrono-log", Scopes: []string{"memory:read"},
	})
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	a := New(cs, tok)
	a.MemoryStore = ms

	type logLine struct {
		Revision struct {
			RevisionID string    `json:"revision_id"`
			CreatedAt  time.Time `json:"created_at"`
			Payload    *struct {
				Summary string `json:"summary"`
				Body    string `json:"body"`
			} `json:"payload"`
		} `json:"revision"`
	}

	const pageSize = 200
	seen := map[string]int{}
	var prev time.Time
	var lines, bytesStreamed, pages, total int
	var withoutSummary, withBody int
	cursor := ""

	for {
		args := map[string]any{
			"namespaces":   string(nsJSON),
			"ranking":      "chronological",
			"payload_mode": "summary",
			"limit":        float64(pageSize),
		}
		if cursor != "" {
			args["cursor"] = cursor
		}
		req := mcp.CallToolRequest{}
		req.Params.Arguments = args
		res, err := a.handleMemoryRecall(ctx, req)
		if err != nil {
			t.Fatalf("page %d: %v", pages, err)
		}
		raw := res.Content[0].(mcp.TextContent).Text

		var env struct {
			Results  []logLine       `json:"results"`
			Manifest memory.Manifest `json:"manifest"`
		}
		if err := json.Unmarshal([]byte(raw), &env); err != nil {
			t.Fatalf("page %d: decode: %v", pages, err)
		}
		total = env.Manifest.ResultsTotal
		bytesStreamed += env.Manifest.BytesReturned
		pages++

		for _, l := range env.Results {
			lines++
			seen[l.Revision.RevisionID]++
			if l.Revision.Payload == nil || l.Revision.Payload.Summary == "" {
				withoutSummary++
			}
			if l.Revision.Payload != nil && l.Revision.Payload.Body != "" {
				withBody++
			}
			// Strictly newest-first, including across the page boundary.
			if !prev.IsZero() && l.Revision.CreatedAt.After(prev) {
				t.Errorf("entry %s (%s) is newer than the one before it (%s): stream is not ordered",
					l.Revision.RevisionID, l.Revision.CreatedAt, prev)
			}
			prev = l.Revision.CreatedAt
		}

		if env.Manifest.Truncated != (env.Manifest.NextCursor != nil) {
			t.Fatalf("page %d: truncated=%v but next_cursor=%v",
				pages, env.Manifest.Truncated, env.Manifest.NextCursor)
		}
		if env.Manifest.NextCursor == nil {
			break
		}
		cursor = *env.Manifest.NextCursor
		if pages > 10000 {
			t.Fatal("log stream did not terminate")
		}
	}

	repeats := 0
	for _, n := range seen {
		if n > 1 {
			repeats++
		}
	}

	t.Logf("STREAMED entries=%d distinct=%d repeats=%d pages=%d page_size=%d bytes=%d (%d B/entry)",
		lines, len(seen), repeats, pages, pageSize, bytesStreamed, bytesStreamed/max(lines, 1))
	t.Logf("manifest.results_total=%d ; entries streamed=%d", total, lines)
	t.Logf("summary projection: entries with no summary=%d ; entries carrying a body=%d",
		withoutSummary, withBody)

	if lines != total {
		t.Errorf("streamed %d entries but results_total said %d", lines, total)
	}
	if len(seen) != lines {
		t.Errorf("%d entries streamed but only %d distinct — the log repeated rows", lines, len(seen))
	}
	if withBody != 0 {
		t.Errorf("%d log lines carried a body; summary projection is not holding", withBody)
	}
}

