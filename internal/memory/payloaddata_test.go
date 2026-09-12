package memory_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/hollis-labs/tesseract/internal/memory"
)

// ── The retrieval half of the discipline line ───────────────────────────────

// TestPayloadDataDoesNotReachTheFTSIndex is the LEXICAL half of the retrieval
// guard. The embedding half is TestPayloadDataIsNotEmbeddingInput, in
// payloaddata_internal_test.go, and the split is named rather than implied
// because this test used to claim both.
//
// It is the guard the AST walk cannot be: indexing `data` would not look like a
// branch on a value, it would look like "make structured records searchable,"
// and it would arrive as an obviously useful change.
//
// Behavioral rather than structural — it writes a revision whose data carries a
// needle appearing NOWHERE else and asks the real lexical arm for it. Folding
// data into an indexed column, or adding it to the FTS triggers, fails this;
// neither would fail a test that merely inspected the column list.
//
// WHAT IT DOES NOT COVER, stated because the first version of this comment got
// it wrong. This test issues a LEXICAL query, so the embedding input is
// invisible to it: `revisionEmbedText` gaining `rev.Payload.Data` passes this
// test GREEN. Verified by injection — after the claim had been written and not
// checked. A reader who took this test's old name for the whole guarantee would
// have been relying on something that was never true.
//
// THE CONTROL IS THE LOAD-BEARING PART. An assertion that a search returns
// nothing passes just as well when search is broken, when the fixture never
// indexed anything, or when the query was malformed — and it would then keep
// passing forever while proving nothing. So the same distinctive needle is
// written into another revision's BODY and must be found. If the control fails,
// the test says so instead of quietly reporting success.
func TestPayloadDataDoesNotReachTheFTSIndex(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	// A token with no chance of appearing in stopword lists, stemmed forms, or
	// any other fixture in this package.
	const needle = "zarquonthirtyseven"

	inData := sampleInput("data.holder")
	inData.Payload = memory.Payload{
		Summary: "a record whose fields live in data",
		Body:    "prose that does not mention the token",
		Data:    json.RawMessage(`{"component":"` + needle + `","severity":3}`),
	}
	if _, err := ms.WriteRevision(ctx, inData); err != nil {
		t.Fatalf("write data revision: %v", err)
	}

	inBody := sampleInput("body.holder")
	inBody.Payload = memory.Payload{
		Summary: "a record whose prose mentions it",
		Body:    "the component is " + needle + " and that is prose",
	}
	if _, err := ms.WriteRevision(ctx, inBody); err != nil {
		t.Fatalf("write body revision: %v", err)
	}

	results, err := ms.Recall(ctx, memory.RecallInput{
		Namespaces: []string{"user/chrispian/memory/notes"},
		Ranking:    memory.RankingRelevance,
		SearchMode: memory.SearchModeLexical,
		Query:      needle,
		Limit:      10,
	})
	if err != nil {
		t.Fatalf("lexical recall: %v", err)
	}

	found := map[string]bool{}
	for _, r := range results {
		found[r.Revision.MemoryKey] = true
	}

	// The control, checked FIRST so a broken search arm is reported as a broken
	// control rather than as a passing guard.
	if !found["body.holder"] {
		t.Fatalf("the control revision was not found by a lexical search for %q, so this test "+
			"cannot distinguish 'data is not indexed' from 'nothing is indexed'. Fix the control "+
			"before trusting the assertion below. Got: %v", needle, found)
	}

	if found["data.holder"] {
		t.Errorf("a lexical search for a needle that appears ONLY inside payload.data returned the " +
			"record carrying it. payload.data must not reach the FTS index " +
			"(CW-20260912-0036): it is the consumer's object, stored verbatim and never " +
			"interpreted, and indexing its text is Tesseract deciding what the shape MEANS. " +
			"If records need to be findable by something in data, the writer puts it in the " +
			"summary or body as prose — that choice is theirs, not ours.")
	}
}

// ── Round trip ──────────────────────────────────────────────────────────────

// TestPayloadDataRoundTripsVerbatim pins BYTES, not equivalence.
//
// The tempting implementation stores data as map[string]any, and it passes any
// test that compares parsed values. It also reorders keys and pushes every
// number through float64, so a consumer's 64-bit identifier comes back changed
// and their object no longer hashes to what they signed. "Stored verbatim" is
// only meaningful if it means the bytes.
func TestPayloadDataRoundTripsVerbatim(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	// Deliberately: keys NOT in alphabetical order, an integer too large for
	// float64 to hold exactly, a nested object and an array.
	const raw = `{"zeta":1,"id":9007199254740993,"alpha":{"nested":true},"list":[3,2,1]}`

	in := sampleInput("adr.0001")
	in.Payload = memory.Payload{Summary: "an ADR", Data: json.RawMessage(raw)}
	written, err := ms.WriteRevision(ctx, in)
	if err != nil {
		t.Fatalf("WriteRevision: %v", err)
	}
	if string(written.Payload.Data) != raw {
		t.Errorf("the write response changed the bytes:\n got %s\nwant %s", written.Payload.Data, raw)
	}

	read, err := ms.GetRevisionByID(ctx, written.RevisionID)
	if err != nil {
		t.Fatalf("GetRevisionByID: %v", err)
	}
	if string(read.Payload.Data) != raw {
		t.Errorf("payload.data did not survive the round trip verbatim:\n got %s\nwant %s\n"+
			"Key order and integer precision both matter: a consumer that hashes or signs its "+
			"object gets a different answer from re-serialized JSON.", read.Payload.Data, raw)
	}
}

// TestPayloadDataAbsentStaysAbsent covers the revisions that already exist.
//
// Every revision written before this field did. They must read back with no
// data and no claim — not with `{}`, not with the four bytes `null`, which are
// three spellings of nothing that every future reader would have to collapse.
func TestPayloadDataAbsentStaysAbsent(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	written, err := ms.WriteRevision(ctx, sampleInput("no.data"))
	if err != nil {
		t.Fatalf("WriteRevision: %v", err)
	}
	read, err := ms.GetRevisionByID(ctx, written.RevisionID)
	if err != nil {
		t.Fatalf("GetRevisionByID: %v", err)
	}
	if read.Payload.Data != nil {
		t.Errorf("payload.data = %q for a write that sent none; absent must stay absent",
			read.Payload.Data)
	}
	if read.Payload.DataSchemaHash != "" {
		t.Errorf("payload.data_schema_hash = %q for a write that made no claim",
			read.Payload.DataSchemaHash)
	}

	// And it must not appear in the serialized revision at all, because a
	// consumer diffing two JSON responses should not see a key arrive.
	blob, err := json.Marshal(read)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(blob), `"data"`) || strings.Contains(string(blob), `"data_schema_hash"`) {
		t.Errorf("an absent payload.data serialized a key anyway: %s", blob)
	}
}

// ── Refusals ────────────────────────────────────────────────────────────────

// TestPayloadDataRefusesNonObjects covers the entire validation contract, which
// is two facts wide. Anything this test does NOT refuse is deliberate.
func TestPayloadDataRefusesNonObjects(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	for _, tc := range []struct {
		name string
		data string
		why  string
	}{
		{"invalid JSON", `{"a":`, "does not parse"},
		{"array", `[1,2,3]`, "an array has no fields to be a record's shape"},
		{"string scalar", `"just a string"`, "a scalar is not an object"},
		{"number scalar", `42`, "a scalar is not an object"},
		{"literal null", `null`, "unmarshals into a nil map without error; two spellings of nothing"},
		{"trailing token", `{"a":1} and then some`, "does not parse as one value"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := sampleInput("bad.data")
			in.Payload = memory.Payload{Summary: "s", Data: json.RawMessage(tc.data)}
			if _, err := ms.WriteRevision(ctx, in); err == nil {
				t.Fatalf("accepted %s as payload.data (%s)", tc.data, tc.why)
			}
		})
	}

	// What is deliberately ACCEPTED, stated so nobody reads the list above as
	// a schema: an empty object, unknown keys, any nesting, any value type.
	// Tesseract has no opinion about content and this is what that looks like.
	// A DUPLICATE KEY is on this list on purpose, and it is the interesting
	// entry. `{"a":1,"a":2}` parses and is an object, so the contract accepts
	// it, and the bytes come back exactly as sent. Which `a` wins is decided by
	// whoever parses it — and since Tesseract never does, that question is the
	// consumer's, not ours. Refusing it would be an opinion about content
	// wearing a structural disguise.
	//
	// Note this differs from consumer_state, whose comment claims a map decode
	// rejects duplicates. It does not (verified 2026-09-12) — but there it
	// MATTERS, because json_extract does read those values for state_filters.
	// See the note in consumerstate.go.
	for _, ok := range []string{
		`{}`,
		`{"anything":"at all","nested":{"deep":[1,{"x":null}]}}`,
		`{"a key with spaces":true,"unicode":"日本語"}`,
		`{"a":1,"a":2}`,
	} {
		in := sampleInput("good.data")
		in.Payload = memory.Payload{Summary: "s", Data: json.RawMessage(ok)}
		if _, err := ms.WriteRevision(ctx, in); err != nil {
			t.Errorf("refused %s, but Tesseract has no opinion about what is inside: %v", ok, err)
		}
	}
}

// ── The opt-in schema claim ─────────────────────────────────────────────────

// TestPayloadDataSchemaClaim covers the three behaviors that make the claim
// evidence rather than decoration.
func TestPayloadDataSchemaClaim(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	const hash = "3b1f8c1d2e4a5b6c7d8e9f0a1b2c3d4e5f60718293a4b5c6d7e8f9a0b1c2d3e4"

	t.Run("round trips", func(t *testing.T) {
		in := sampleInput("claimed")
		in.Payload = memory.Payload{
			Summary:        "a record claiming a schema",
			Data:           json.RawMessage(`{"decision":"use WAL"}`),
			DataSchemaHash: hash,
		}
		w, err := ms.WriteRevision(ctx, in)
		if err != nil {
			t.Fatalf("WriteRevision: %v", err)
		}
		read, err := ms.GetRevisionByID(ctx, w.RevisionID)
		if err != nil {
			t.Fatalf("GetRevisionByID: %v", err)
		}
		if read.Payload.DataSchemaHash != hash {
			t.Errorf("data_schema_hash = %q, want %q", read.Payload.DataSchemaHash, hash)
		}
	})

	// The one that matters most. Stamping a type's current hash onto a write
	// that claimed nothing would manufacture the exact evidence the claim
	// exists to provide, and drift would become undetectable precisely for the
	// records that never claimed anything.
	t.Run("a write making no claim stores none", func(t *testing.T) {
		in := sampleInput("unclaimed")
		in.Payload = memory.Payload{Summary: "s", Data: json.RawMessage(`{"decision":"use WAL"}`)}
		w, err := ms.WriteRevision(ctx, in)
		if err != nil {
			t.Fatalf("WriteRevision: %v", err)
		}
		read, err := ms.GetRevisionByID(ctx, w.RevisionID)
		if err != nil {
			t.Fatalf("GetRevisionByID: %v", err)
		}
		if read.Payload.DataSchemaHash != "" {
			t.Errorf("data_schema_hash = %q for a write that claimed nothing — a default here is "+
				"indistinguishable from a choice, and it is the choice this field records",
				read.Payload.DataSchemaHash)
		}
	})

	t.Run("refuses a claim that is not a digest", func(t *testing.T) {
		in := sampleInput("badclaim")
		in.Payload = memory.Payload{Summary: "s", Data: json.RawMessage(`{}`), DataSchemaHash: "v1.2.3"}
		if _, err := ms.WriteRevision(ctx, in); err == nil {
			t.Error("accepted a non-digest schema claim")
		}
	})

	t.Run("refuses a claim about absent data", func(t *testing.T) {
		in := sampleInput("emptyclaim")
		in.Payload = memory.Payload{Summary: "s", DataSchemaHash: hash}
		if _, err := ms.WriteRevision(ctx, in); err == nil {
			t.Error("accepted a schema claim with no data for it to describe")
		}
	})
}
