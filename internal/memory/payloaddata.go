package memory

import (
	"encoding/json"
	"fmt"
	"regexp"
)

// payload.data: the record's own shape, belonging to whoever wrote it.
//
// # What it is, and what it is not
//
// `consumer_state` is the writer's OPERATIONAL metadata about a record — is
// this todo done, which section is it in. `payload.data` is the RECORD ITSELF
// in the writer's own shape: an ADR's decision and alternatives, a contact's
// email and phone, a bug report's steps and severity. Summary and body stay
// prose; data takes everything a consumer would otherwise have to flatten into
// prose and re-parse on the way out.
//
// The two bags are siblings and the distinction is worth keeping: one is about
// how a record is being worked, the other is what the record IS.
//
// # The contract
//
//	It parses. It is an object. That is the whole list.
//
// No typing, no schema enforcement, no required keys, no vocabulary, no
// coupling to the type registry, no indexing, and NO CODE PATH READS A VALUE
// OUT OF IT. Not embedded, not in FTS, not ranked, not filtered on by us, no
// branch anywhere.
//
// # Why this needs a guard rather than a convention
//
// Every line of this is easy to write wrong in the direction of HELPFULNESS.
// Validating a little, indexing one field, defaulting a missing value, reading
// `data.title` when `summary` is empty — each is a small kindness, each turns
// an opaque bag into a thing Tesseract has an opinion about, and the opinion is
// unremovable the moment a consumer depends on it.
//
// `consumer_state` survived that pressure because its guard is MECHANICAL:
// TestNoCodePathBranchesOnConsumerStateValue parses this repository and fails
// on the first comparison of a bag value against a literal. payload.data is
// covered by the same walk. The retrieval half takes TWO guards, because the
// two arms are decided in different places and neither test can see the other's:
// TestPayloadDataDoesNotReachTheFTSIndex for the lexical index, and
// TestPayloadDataIsNotEmbeddingInput for the embedding input. That erosion would
// not look like a branch at all — indexing data's text arrives as "make
// structured records searchable," and it is the one change here that would be
// both obviously useful and quietly fatal to the contract.
//
// # What is deliberately NOT here
//
// No per-type size ceiling. `MaxSummaryBytes` is the cautionary tale: declared
// on Type, validated as a declaration, and enforced by no write path — a knob
// that reads as a limit and is not one. Rather than add a fourth dead per-type
// field, data is bounded by the same global request cap every other body is
// (contextapi's maxRequestBodyBytes) plus the sanity ceiling below.

// maxPayloadDataBytes is a global sanity cap, not a policy knob.
//
// It exists to keep one write from parking megabytes in a column nothing
// indexes, and it is deliberately far above any plausible record: an ADR with
// every field filled is kilobytes. It is NOT per-type and must not become so —
// see the note above about declared-but-dead ceilings.
const maxPayloadDataBytes = 1 << 20 // 1 MiB

// schemaClaimRE matches a hex sha256 digest.
//
// The same shape typeregistry enforces on a declared SchemaRef.SchemaHash, so
// a claim and a declaration are comparable without either side normalizing.
// This is the ONLY thing checked about the claim: that it is the right KIND of
// string. Whether it matches the type's current hash, whether that schema
// exists, and whether data conforms to it are all the consumer's business.
var schemaClaimRE = regexp.MustCompile(`^[a-f0-9]{64}$`)

// validatePayloadData checks the two structural facts and nothing else.
//
// Structured exactly like validateConsumerState, including the literal-null
// case, because the two bags have the same contract and a reader who learns one
// should not have to relearn the other.
func validatePayloadData(p Payload) error {
	if len(p.Data) > 0 {
		if len(p.Data) > maxPayloadDataBytes {
			return fmt.Errorf("%w: payload.data is %d bytes, over the %d byte ceiling",
				ErrInvalidInput, len(p.Data), maxPayloadDataBytes)
		}
		if !json.Valid(p.Data) {
			return fmt.Errorf("%w: payload.data must be well-formed JSON", ErrInvalidInput)
		}

		// Decoded into a map rather than checked for a leading `{`, because a
		// leading brace does not make something an object — `{"a":1} trailing`
		// has one and is not valid JSON at all.
		//
		// This does NOT reject duplicate keys, and that is correct here rather
		// than an oversight: encoding/json takes the last one silently, the
		// bytes are stored and returned exactly as sent, and which duplicate
		// wins is decided by whoever parses them — never us. (consumerstate.go
		// carries a comment claiming this decode rejects duplicates. It does
		// not, and there it matters, because json_extract does read those
		// values.)
		var bag map[string]json.RawMessage
		if err := json.Unmarshal(p.Data, &bag); err != nil {
			return fmt.Errorf("%w: payload.data must be a JSON object, not an array or a scalar: %w",
				ErrInvalidInput, err)
		}
		// A literal `null` unmarshals into a map WITHOUT error and leaves it
		// nil, so the check above does not catch it. Refused rather than
		// stored: a column holding the four bytes `null` and one holding SQL
		// NULL would be two spellings of "no data" that every reader has to
		// know to collapse. Omit the field, or send {}.
		if bag == nil {
			return fmt.Errorf("%w: payload.data must be a JSON object; a literal null is not one — "+
				"omit the field entirely, or send {}", ErrInvalidInput)
		}
	}

	if p.DataSchemaHash != "" {
		if !schemaClaimRE.MatchString(p.DataSchemaHash) {
			return fmt.Errorf("%w: payload.data_schema_hash %q is not a hex sha256 digest. "+
				"It records WHICH schema this data claims to follow, so that drift is detectable "+
				"later; Tesseract never opens the schema and never validates against it",
				ErrInvalidInput, p.DataSchemaHash)
		}
		// A claim about absent data is refused rather than stored. It cannot be
		// true of anything, and storing it would put a row in the corpus that
		// says a schema was followed by no content — the exact false evidence
		// the claim exists to avoid manufacturing.
		if len(p.Data) == 0 {
			return fmt.Errorf("%w: payload.data_schema_hash was given without payload.data. "+
				"The claim describes the data; there is nothing here for it to describe",
				ErrInvalidInput)
		}
	}
	return nil
}
