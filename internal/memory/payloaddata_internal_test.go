package memory

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestPayloadDataIsNotEmbeddingInput is the embedding half of the retrieval
// guard, asserted at the function that decides what gets embedded rather than
// inferred from the struct.
//
// The external TestPayloadDataDoesNotReachTheFTSIndex covers the lexical arm
// through the real FTS index. It cannot cover this one: it issues a lexical
// query, so the embedding input is invisible to it, and a test store has no
// embedder for the semantic arm to use either. The two halves are asserted in
// the two places they are decided and NEITHER STANDS IN FOR THE OTHER — the FTS
// test was briefly named as though it did, which is why that is said twice.
//
// Why it would be wired in: embedding the data makes structured records turn up
// in semantic recall, which is useful and is exactly the erosion. A consumer
// who wants their record found writes prose in the summary or body — that is
// their call to make, and making it for them means Tesseract deciding what
// someone else's JSON means.
func TestPayloadDataIsNotEmbeddingInput(t *testing.T) {
	const needle = "zarquonthirtyseven"

	withData := Revision{Payload: Payload{
		Summary: "a summary",
		Body:    "a body",
		Data:    json.RawMessage(`{"component":"` + needle + `"}`),
	}}
	withoutData := Revision{Payload: Payload{
		Summary: "a summary",
		Body:    "a body",
	}}

	got := revisionEmbedText(withData)
	if strings.Contains(got, needle) {
		t.Errorf("revisionEmbedText included a token that appears only in payload.data.\n"+
			"got: %q\n"+
			"payload.data must not reach the embedding input (CW-20260912-0036). Embedding it "+
			"would put the consumer's field names and values into the vector that decides "+
			"semantic recall, which is Tesseract forming an opinion about what their shape means.",
			got)
	}
	if got != revisionEmbedText(withoutData) {
		t.Errorf("adding payload.data changed the embedding input:\n with: %q\nwithout: %q",
			got, revisionEmbedText(withoutData))
	}
}
