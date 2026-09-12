package mcpadapter

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hollis-labs/tesseract/internal/memory"
	"github.com/mark3labs/mcp-go/mcp"
)

// parseStringArrayArg extracts a JSON-array-of-strings argument from an MCP
// request, accepting BOTH a native JSON array (sent by clients that don't
// stringify, including the mux MCP proxy) and a JSON-encoded string (sent by
// clients that honor the WithString schema declaration).
//
// Returns (values, present, error). `present` is true whenever the key exists
// in the request arguments; this lets callers distinguish "unset" from
// "deliberately empty list" if needed.
//
// Schemas in this package declare these arrays as `mcp.WithString` for
// historical reasons. Without this helper the native-array path silently
// drops the value because `req.GetString` returns "" for non-string args
// (mark3labs/mcp-go v0.47: tools.go GetString casts via val.(string)).
func parseStringArrayArg(req mcp.CallToolRequest, key string) ([]string, bool, error) {
	args := req.GetArguments()
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
	". Narrows results to revisions whose `consumer_state` bag carries one of `values` at `field`. " +
	"**`consumer_state` is not `state`.** The `state` block on a full-mode result is Tesseract's own " +
	"activation bookkeeping for the entry; `consumer_state` is the writer's operational bag on one " +
	"revision, and only the second is filterable or writable. " +
	"**Values are JSON, not strings that look like it** — `[false]` matches a bag holding the literal " +
	"false, `[\"false\"]` matches one holding the string. Strings, numbers and booleans only; a null is " +
	"refused because an absent key and a null are indistinguishable to an index. " +
	"Values within one filter OR together; separate filters AND. Naming the same field twice is a " +
	"validation_error rather than an intersection nobody meant. " +
	"Set membership only — there is no range or negation, so `due before tomorrow` is not expressible. " +
	"Filtering happens in SQL before `limit`, so it enumerates a population rather than sampling one. " +
	"Any lowercase-identifier field name is accepted; a type's declared `hot_fields` are the subset " +
	"carrying an index (`todos`: completed, external_ref, kind, section), and filtering on any other " +
	"field is correct but scans."

// parseStateFiltersArg decodes the `state_filters` argument.
//
// It is separate from parseStringArrayArg because a state filter is an object,
// not a string: the values inside it must survive as JSON scalars all the way
// to the bind parameter, since a bag holding `false` and one holding `"false"`
// are different rows. Decoding through []string would flatten exactly that
// distinction and answer the wrong question with no error.
func parseStateFiltersArg(req mcp.CallToolRequest) ([]memory.StateFilter, *mcp.CallToolResult) {
	raw, ok := req.GetArguments()["state_filters"]
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
func consumerStateArg(req mcp.CallToolRequest) json.RawMessage {
	raw, ok := req.GetArguments()["consumer_state"]
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

// payloadDataArgDescription is the shared prose for the `payload_data`
// argument on the three write doors (CW-20260912-0036).
const payloadDataArgDescription = "Optional JSON OBJECT holding THE RECORD'S OWN FIELDS in your shape — " +
	"an ADR's decision and alternatives, a contact's email and phone, a bug report's steps and severity. " +
	"`payload_summary` and `payload_body` stay PROSE; this is everything else, and you get it back byte " +
	"for byte. **Tesseract checks that it parses and that it is an object, and nothing else.** No typing, " +
	"no schema enforcement, no required keys, not indexed, not embedded, not searched, never ranked or " +
	"filtered on by us, and no value inside it changes anything Tesseract does. " +
	"**Not `consumer_state`**, which is your OPERATIONAL state about the record — is it done, which " +
	"section — and IS filterable via `state_filters`. This is what the record IS; that is how it is being " +
	"worked. Search still finds a record by its summary and body, so put anything you want to be " +
	"findable in prose as well."

// payloadDataSchemaHashArgDescription is the shared prose for the optional
// schema CLAIM.
const payloadDataSchemaHashArgDescription = "Optional hex sha256 recording WHICH schema your `payload_data` " +
	"follows, matching a type's `schema_ref.schema_hash`. **Tesseract never opens the schema and never " +
	"validates against it** — it stores what you said, so that a record written under an older schema is " +
	"later distinguishable from one that drifted. Omit it if you are making no claim; it is never " +
	"defaulted for you."

// payloadDataArg reads the `payload_data` write argument.
//
// Identical in shape to consumerStateArg, and for the same reason: a client may
// send a JSON-encoded string or a native object, and neither is normalized here
// because the store is the single authority on whether the bytes are an object.
// A malformed bag is reported by the one validator every write path reaches
// rather than by whichever door it arrived at.
func payloadDataArg(req mcp.CallToolRequest) json.RawMessage {
	raw, ok := req.GetArguments()["payload_data"]
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
		return json.RawMessage("null")
	}
	return b
}
