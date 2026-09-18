package mcpadapter

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/hollis-labs/tesseract/internal/memory"
)

// argString, argInt, argFloat and argBool replace mark3labs'
// CallToolRequest.GetString/GetInt/GetFloat/GetBool: go-mcp hands a handler a
// plain map[string]any with no accessor methods of its own, so this package
// gets its own — with the exact same per-type coercions mark3labs' versions
// used, so behavior is unchanged by the port.

// argString returns a string argument by key, or defaultValue if not found or
// not a string.
func argString(args map[string]any, key, defaultValue string) string {
	if val, ok := args[key]; ok {
		if str, ok := val.(string); ok {
			return str
		}
	}
	return defaultValue
}

// argInt returns an int argument by key, or defaultValue if not found or not
// convertible to int.
func argInt(args map[string]any, key string, defaultValue int) int {
	if val, ok := args[key]; ok {
		switch v := val.(type) {
		case int:
			return v
		case float64:
			return int(v)
		case string:
			if i, err := strconv.Atoi(v); err == nil {
				return i
			}
		}
	}
	return defaultValue
}

// argFloat returns a float64 argument by key, or defaultValue if not found or
// not convertible to float64.
func argFloat(args map[string]any, key string, defaultValue float64) float64 {
	if val, ok := args[key]; ok {
		switch v := val.(type) {
		case float64:
			return v
		case int:
			return float64(v)
		case string:
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				return f
			}
		}
	}
	return defaultValue
}

// argBool returns a bool argument by key, or defaultValue if not found or not
// convertible to bool.
func argBool(args map[string]any, key string, defaultValue bool) bool {
	if val, ok := args[key]; ok {
		switch v := val.(type) {
		case bool:
			return v
		case string:
			if b, err := strconv.ParseBool(v); err == nil {
				return b
			}
		case int:
			return v != 0
		case float64:
			return v != 0
		}
	}
	return defaultValue
}

// parseStringArrayArg extracts a JSON-array-of-strings argument from an MCP
// tool call's arguments, accepting BOTH a native JSON array (sent by clients
// that don't stringify, including the mux MCP proxy) and a JSON-encoded string
// (sent by clients that honor the WithString schema declaration).
//
// Returns (values, present, error). `present` is true whenever the key exists
// in the request arguments; this lets callers distinguish "unset" from
// "deliberately empty list" if needed.
//
// Schemas in this package declare these arrays as a string property for
// historical reasons. Without this helper the native-array path silently
// drops the value because a naive string read returns "" for non-string args.
func parseStringArrayArg(args map[string]any, key string) ([]string, bool, error) {
	raw, ok := args[key]
	if !ok || raw == nil {
		return nil, false, nil
	}
	switch v := raw.(type) {
	case []string:
		return v, true, nil
	case []any:
		out := make([]string, 0, len(v))
		for i, item := range v {
			s, ok := item.(string)
			if !ok {
				return nil, true, fmt.Errorf("item %d is not a string", i)
			}
			out = append(out, s)
		}
		return out, true, nil
	case string:
		if v == "" {
			return nil, false, nil
		}
		var out []string
		if err := json.Unmarshal([]byte(v), &out); err != nil {
			return nil, true, fmt.Errorf("must be a JSON array of strings: %w", err)
		}
		return out, true, nil
	}
	return nil, true, fmt.Errorf("must be a string or JSON array of strings")
}

// stateFiltersArgDescription is the shared prose for the `state_filters`
// argument on the recall doors (CW-20260909-0036).
const stateFiltersArgDescription = "JSON array of consumer-state filters, e.g. " +
	`[{"field":"section","values":["now","soon"]},{"field":"completed","values":[false]}]` +
	". Narrows to revisions whose `consumer_state` bag carries one of `values` at `field` — " +
	"**not** the `state` block a full-mode result carries, which is Tesseract's own activation bookkeeping. " +
	"**Values are JSON, not strings that look like it** — `[false]` matches the literal false, `[\"false\"]` matches the string; a null is refused. " +
	"Values within one filter OR together; separate filters AND; naming the same field twice is a validation_error. " +
	"See `tesseract_skills recall-and-ranking` for indexed `hot_fields`, the set-membership-only limits, and the SQL-before-`limit` guarantee."

// parseStateFiltersArg decodes the `state_filters` argument.
//
// It is separate from parseStringArrayArg because a state filter is an object,
// not a string: the values inside it must survive as JSON scalars all the way
// to the bind parameter, since a bag holding `false` and one holding `"false"`
// are different rows. Decoding through []string would flatten exactly that
// distinction and answer the wrong question with no error.
func parseStateFiltersArg(args map[string]any) ([]memory.StateFilter, any) {
	raw, ok := args["state_filters"]
	if !ok || raw == nil {
		return nil, nil
	}

	var data []byte
	switch v := raw.(type) {
	case string:
		if strings.TrimSpace(v) == "" {
			return nil, nil
		}
		data = []byte(v)
	default:
		// A client that sends native JSON rather than a JSON-encoded string.
		// Re-marshaling is the shortest honest path from `any` back to the
		// typed shape, and it preserves the scalar types the filter depends on.
		b, err := json.Marshal(v)
		if err != nil {
			return nil, toolError(codeValidationError, "state_filters must be a JSON array of {field, values} objects")
		}
		data = b
	}

	var out []memory.StateFilter
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, toolError(codeValidationError,
			"state_filters must be a JSON array of {field, values} objects: "+err.Error())
	}
	return out, nil
}

// consumerStateArgDescription is the shared prose for the `consumer_state`
// argument on the three write doors (CW-20260909-0036).
const consumerStateArgDescription = "Optional JSON OBJECT stored as this revision's `consumer_state` — " +
	"your own operational state for it, e.g. " + `{"kind":"todo","section":"now","completed":false}` + ". " +
	"**It is yours, not Tesseract's.** Tesseract checks that it is well-formed JSON and an object, plus " +
	"any `required_fields` the type declares, and never reads a value out of it — no vocabulary, no " +
	"transition checking, ever. What the values MEAN is entirely your business, and nothing here will " +
	"decay, rank or expire differently because of one. " +
	"**Not the same thing as the `state` block on a full-mode recall result**, which is Tesseract's " +
	"activation bookkeeping (activation, access_count, current_revision) and is not writable. " +
	"Immutable with the revision that carries it: changing state means writing a new revision, which is " +
	"what makes the history of a todo readable. " +
	"Filter on it with `state_filters` on the recall doors. Use it for lifecycle — status, section, " +
	"completion, a due date, an external correlation key — and NOT for content, which belongs in " +
	"`payload_body`, or for cross-cutting labels, which are `tags`."

// consumerStateArg reads the `consumer_state` write argument.
//
// An MCP client may send it either as a JSON-encoded string (the flat-scalar
// shape these tool schemas favor, matching `tags`) or as a native object.
// Neither is normalized here beyond re-marshaling the native case: the store is
// the authority on whether the bytes are a well-formed JSON object, so a
// malformed value is reported by the one validator every write path reaches
// rather than by whichever door it happened to arrive at.
// NOTE (PR #41 review, 2026-09-12): this collapses an EXPLICIT
// `consumer_state: null` into "not sent", the way payloadDataArg used to.
// validateConsumerState refuses a literal null, so MCP silently accepts here
// what HTTP and the library refuse. Left as behavior deliberately: fixing it
// turns a currently-succeeding call into an error on a shipped surface, which
// is a change to this tool's contract and belongs in its own task rather than
// riding along with payload.data. Filed as CW-20260912-0045.
func consumerStateArg(args map[string]any) json.RawMessage {
	raw, ok := args["consumer_state"]
	if !ok || raw == nil {
		return nil
	}
	if s, isStr := raw.(string); isStr {
		if strings.TrimSpace(s) == "" {
			return nil
		}
		return json.RawMessage(s)
	}
	b, err := json.Marshal(raw)
	if err != nil {
		// Unreachable for anything an MCP transport can deliver, and if it
		// were reached, handing the store an invalid bag is the right failure:
		// it reports the one error message every surface reports.
		return json.RawMessage("null")
	}
	return b
}

// payloadDataArgDescription is the shared prose for the `data`
// argument on the three write doors (CW-20260912-0036).
const payloadDataArgDescription = "Optional JSON OBJECT holding THE RECORD'S OWN FIELDS in your shape — " +
	"an ADR's decision and alternatives, a contact's email and phone, a bug report's steps and severity. " +
	"The summary and body arguments stay PROSE; this is everything else. " +
	"**Tesseract checks that it parses and that it is an object, and nothing else.** No typing, " +
	"no schema enforcement, no required keys, not indexed, not embedded, not searched, never ranked or " +
	"filtered on by us, and no value inside it changes anything Tesseract does. " +
	"**Send it as a JSON-ENCODED STRING if the exact bytes matter to you** — a native JSON object is " +
	"read through our sanitized and checked decoded-arguments path, which can reformat JSON and round " +
	"integers beyond 2^53 through a float. The string form is stored exactly as you sent it. " +
	"**Not `consumer_state`**, which is your OPERATIONAL state about the record — is it done, which " +
	"section — and IS filterable via `state_filters`. This is what the record IS; that is how it is being " +
	"worked. Search still finds a record by its summary and body, so put anything you want to be " +
	"findable in prose as well."

// payloadDataSchemaHashArgDescription is the shared prose for the optional
// schema CLAIM.
const payloadDataSchemaHashArgDescription = "Optional hex sha256 recording WHICH schema your `data` " +
	"follows, matching a type's `schema_ref.schema_hash`. **Tesseract never opens the schema and never " +
	"validates against it** — it stores what you said, so that a record written under an older schema is " +
	"later distinguishable from one that drifted. Omit it if you are making no claim; it is never " +
	"defaulted for you."

// payloadDataArg reads the `data` write argument.
//
// A client may send a JSON-encoded string or a native object. Neither is
// normalized here beyond what the transport already did, because the store is
// the single authority on whether the bytes are an object — a malformed bag is
// reported by the one validator every write path reaches rather than by
// whichever door it arrived at.
//
// TWO SHAPES, ONE OF WHICH PRESERVES BYTES. The JSON-encoded string survives
// the decoded arguments path unchanged. Native objects on that path have
// already passed through float64, so re-marshaling can round large integers
// and change key order and whitespace. go-mcp's official-SDK dispatch also
// decodes CallToolRequest.Params.Arguments once, upstream of this handler, so
// there is no separate raw-bytes path to prefer here either — the args map is
// the one path a sanitized, strict-argument-checked call reaches this
// function through.
//
// ABSENT IS NOT NULL. `data: null` is a value the caller supplied, and
// the store rejects a literal null as a non-object. Collapsing it into "not
// sent" would make MCP silently accept something HTTP refuses, and would hide a
// client bug that produced null where an object was meant.
func payloadDataArg(args map[string]any) json.RawMessage {
	raw, ok := args["data"]
	if !ok {
		return nil
	}
	if raw == nil {
		// Present and explicitly null. Hand the four bytes to the validator so
		// the caller gets the same refusal every other surface gives.
		return json.RawMessage("null")
	}
	if s, isStr := raw.(string); isStr {
		if strings.TrimSpace(s) == "" {
			return nil
		}
		return json.RawMessage(s)
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return json.RawMessage("null")
	}
	return b
}
