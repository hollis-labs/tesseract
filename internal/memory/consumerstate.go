package memory

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/hollis-labs/tesseract/internal/typeregistry"
)

// Consumer state: the operational bag that belongs to whoever writes the
// record, not to Tesseract.
//
// # It is not memory_state
//
// Tesseract already has a table called memory_state and a Go type called
// State. That is the MUTABLE per-entry row — current_revision, activation,
// access_count, last_decayed_at — and it is Tesseract's own bookkeeping about
// how an entry is being used. Everything in this file is the other thing: an
// IMMUTABLE JSON object hanging off one revision, written by a consumer, about
// that consumer's workflow. The column is `consumer_state`, the field is
// Revision.ConsumerState, and neither is ever abbreviated to `state` in this
// package, because the abbreviation is how the two get confused and confusing
// them writes a consumer's todo status into the activation ledger.
//
// # The discipline line
//
// [[tesseract_consumer_state_separate_from_status]] rev 2, which is canonical:
//
//	Tesseract may INDEX declared fields and FILTER on them.
//	Tesseract must never BRANCH BEHAVIOR on a value.
//
// Answering "todos where section = now" is mechanism: the query names the
// field and the value, and Tesseract renders a comparison it never reads.
// Skipping decay because a value is "done", ranking `pinned` higher, refusing
// a transition from `done` to `open` — those are policy, they belong to the
// consumer, and each one is Tesseract acquiring an opinion about someone
// else's workflow.
//
// The pressure arrives as reasonable-sounding requests, one at a time. The
// guard is TestNoCodePathBranchesOnConsumerStateValue, which parses this
// repository and fails on the first comparison of a consumer-state value
// against a literal anywhere in non-test source. It is written to fail for the
// next person, not only for the one who added the column.
//
// # What Tesseract does validate
//
// Well-formed JSON, and that it is an OBJECT rather than an array or a scalar
// — a bag with no keys has no field to index, which is a structural fact about
// the mechanism rather than a judgment about content. Plus the declaring
// type's `required_fields`, which is presence only: the keys must be there,
// and their values are never looked at. That is the complete list, and the
// decision rejected `json_object_schema_validated` explicitly — validating
// state against a schema makes Tesseract the arbiter of whether a consumer's
// workflow state is valid.

// stateFieldRE constrains a filterable consumer-state field to a bare
// lowercase identifier.
//
// It is deliberately the same shape typeregistry.hotFieldRE enforces on a
// declared hot field, because these are the same names seen from the two ends:
// the registry declares which ones are worth an index, and this is where a
// caller names one to filter on. A value that cannot be anything but an
// identifier cannot become a JSON path fragment that means something else, and
// it cannot carry a quote into the `$.field` literal the clause builder
// interpolates.
var stateFieldRE = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// StateFilter narrows recall to revisions whose consumer_state carries one of
// Values at Field.
//
// Set membership only — no ranges, no negation, no comparison operators. That
// is a real limit and it is worth knowing before designing a consumer around
// it: `due_at < tomorrow` is not expressible today, which is why `due_at` is
// not among the shipped `todos` hot fields. Declaring an index for a query
// shape nobody can write is the mistake the event vocabulary avoided by
// shipping two stream names instead of three.
//
// Values are JSON scalars, not strings-that-look-like-them: a bag holding
// `{"completed": false}` is matched by the JSON literal false, and one holding
// `{"priority": 2}` by the number 2. See normalizeStateValue for how each
// binds, and why a null is refused rather than accepted as a filter that
// silently matches nothing.
type StateFilter struct {
	Field  string `json:"field"`
	Values []any  `json:"values"`
}

// validateConsumerState checks a consumer_state bag at the write boundary.
//
// The complete check, in order: well-formed JSON, a JSON object, and every
// name in required present as a key. Nothing here reads a value, and nothing
// here may learn to.
//
// An empty bag (`{}`) is legal when the type declares no required fields. A
// consumer that has nothing to say about lifecycle yet should be able to write
// the record anyway and fill the bag on a later revision.
func validateConsumerState(raw json.RawMessage, required []string) error {
	if len(raw) == 0 {
		if len(required) > 0 {
			return fmt.Errorf("%w: consumer_state is required by this type and must carry %s",
				ErrInvalidInput, strings.Join(required, ", "))
		}
		return nil
	}
	if !json.Valid(raw) {
		return fmt.Errorf("%w: consumer_state must be well-formed JSON", ErrInvalidInput)
	}

	// Decoded into a map rather than checked for a leading `{`, so that a
	// duplicate key or a trailing token is rejected here rather than stored and
	// found later by json_extract, which resolves duplicates by its own rule.
	var bag map[string]json.RawMessage
	if err := json.Unmarshal(raw, &bag); err != nil {
		return fmt.Errorf("%w: consumer_state must be a JSON object, not an array or a scalar: %w",
			ErrInvalidInput, err)
	}
	// A literal `null` decodes into a map WITHOUT error and leaves it nil, so
	// the unmarshal above does not catch it. Refused rather than stored,
	// because a column holding the four bytes `null` and one holding SQL NULL
	// would be two spellings of "no bag" that json_extract treats differently.
	// Send no consumer_state at all, or send `{}`.
	if bag == nil {
		return fmt.Errorf("%w: consumer_state must be a JSON object; a literal null is not one — "+
			"omit the field entirely, or send {}", ErrInvalidInput)
	}

	var missing []string
	for _, f := range required {
		if _, ok := bag[f]; !ok {
			missing = append(missing, f)
		}
	}
	if len(missing) > 0 {
		// Presence, never value. A key present and set to null, false or ""
		// satisfies this — the type said the field must be THERE, and what it
		// says is the consumer's business.
		return fmt.Errorf("%w: consumer_state is missing required field(s) %s",
			ErrInvalidInput, strings.Join(missing, ", "))
	}
	return nil
}

// normalizeStateValue converts one decoded JSON filter value into something
// database/sql can bind such that it compares equal to what json_extract
// returns for the same JSON.
//
// The conversions exist because json_extract does not answer JSON, it answers
// SQLite: a JSON true comes back as the integer 1, and a JSON number as an
// integer or a real. Binding the Go bool `true` through the driver and hoping
// is how a filter on `{"completed": false}` silently returns nothing, which is
// indistinguishable from a corpus with no incomplete todos.
//
// This is type coercion for a comparison, not interpretation: it maps every
// boolean the same way regardless of which field it came from or what the
// field is called. Nothing here may grow a case that treats one value
// differently from another of the same JSON type.
func normalizeStateValue(field string, v any) (any, error) {
	switch t := v.(type) {
	case string:
		return t, nil
	case bool:
		if t {
			return int64(1), nil
		}
		return int64(0), nil
	case float64:
		return t, nil
	case json.Number:
		if f, err := t.Float64(); err == nil {
			return f, nil
		}
		return nil, fmt.Errorf("%w: state filter %q: value %q is not a number",
			ErrInvalidInput, field, t.String())
	case nil:
		// A missing key and a key set to null both extract to SQL NULL, and
		// `NULL IN (NULL)` is NULL rather than true, so a null filter value
		// would match nothing at all. Refusing it names the problem instead of
		// serving a confidently empty page.
		return nil, fmt.Errorf("%w: state filter %q: null is not a filterable value — "+
			"a JSON null and an absent key are indistinguishable to an index", ErrInvalidInput, field)
	default:
		return nil, fmt.Errorf("%w: state filter %q: values must be strings, numbers or booleans, got %T — "+
			"a hot field is a scalar, and matching an object or an array would mean comparing serializations",
			ErrInvalidInput, field, v)
	}
}

// buildStateFilterClauses renders StateFilters into WHERE fragments over the
// revision alias `r`, matching buildRecallFilters' contract.
//
// Each filter emits two conjuncts:
//
//	r.consumer_state IS NOT NULL
//	AND json_extract(r.consumer_state, '$.field') IN (?, ...)
//
// The IS NOT NULL is not redundant defensive noise. The hot-field indexes
// migration 19 creates are PARTIAL on exactly that predicate, and SQLite will
// not use a partial index unless the query carries a predicate it can prove
// implies the index's own. Without this conjunct the filter is correct and
// scans the whole table, which is the difference between the declaration
// meaning something and meaning nothing.
//
// Filtering is NOT restricted to declared hot fields. The declaration says
// which fields carry an index, not which ones a caller may name — restricting
// it would leave a consumer unable to ask about `archived` at all until a
// release shipped, and [[tesseract_consumer_state_separate_from_status]] says
// Tesseract "may index declared fields and filter on them", which is a grant
// and not a fence. An undeclared field answers correctly and scans; that cost
// is documented at the surfaces rather than converted into a refusal.
func buildStateFilterClauses(filters []StateFilter) ([]string, []interface{}, error) {
	var where []string
	var args []interface{}

	seen := make(map[string]struct{}, len(filters))
	for _, f := range filters {
		if !stateFieldRE.MatchString(f.Field) {
			return nil, nil, fmt.Errorf("%w: state filter field %q must be a lowercase identifier "+
				"matching ^[a-z][a-z0-9_]*$", ErrInvalidInput, f.Field)
		}
		if _, dup := seen[f.Field]; dup {
			// Two filters on one field would AND together and select the
			// intersection of two sets, which is empty whenever they differ —
			// so a caller who meant "section is now OR soon" and wrote it as
			// two entries would get nothing back and no explanation.
			return nil, nil, fmt.Errorf("%w: state filter field %q named twice; "+
				"put every value for one field in a single filter's values", ErrInvalidInput, f.Field)
		}
		seen[f.Field] = struct{}{}
		if len(f.Values) == 0 {
			return nil, nil, fmt.Errorf("%w: state filter %q has no values; "+
				"omit the filter rather than sending an empty set", ErrInvalidInput, f.Field)
		}

		bound := make([]interface{}, 0, len(f.Values))
		for _, v := range f.Values {
			nv, err := normalizeStateValue(f.Field, v)
			if err != nil {
				return nil, nil, err
			}
			bound = append(bound, nv)
		}

		// f.Field is interpolated into the JSON path rather than bound,
		// because SQLite will not accept a parameter inside the path argument
		// of json_extract in a form an index can be built against. stateFieldRE
		// above is what makes that safe: the value is already proven to be a
		// bare lowercase identifier, so there is nothing in it to escape.
		where = append(where,
			fmt.Sprintf("(r.consumer_state IS NOT NULL AND json_extract(r.consumer_state, '$.%s') IN (%s))",
				f.Field, placeholders(len(bound))))
		args = append(args, bound...)
	}
	return where, args, nil
}

// stateFilterFingerprint renders StateFilters into the canonical string the
// cursor ordering fingerprint hashes.
//
// A state filter narrows the candidate set in SQL, so a cursor issued under
// one and resumed under another offsets into a different sequence and returns
// rows that look right and are wrong — the exact failure the fingerprint
// exists to prevent. Fields are sorted and each field's values are sorted, so
// two callers who asked the same question in a different order page against
// one ordering instead of restarting.
//
// Values render through %v rather than being marshaled back to JSON: this
// string is hashed, never parsed, and the only property it needs is that two
// different filter sets produce two different strings.
func stateFilterFingerprint(filters []StateFilter) []string {
	out := make([]string, 0, len(filters))
	for _, f := range filters {
		vals := make([]string, 0, len(f.Values))
		for _, v := range f.Values {
			switch t := v.(type) {
			case string:
				vals = append(vals, "s:"+t)
			case bool:
				vals = append(vals, "b:"+strconv.FormatBool(t))
			case float64:
				vals = append(vals, "n:"+strconv.FormatFloat(t, 'g', -1, 64))
			default:
				vals = append(vals, fmt.Sprintf("?:%v", v))
			}
		}
		sort.Strings(vals)
		out = append(out, f.Field+"="+strings.Join(vals, ","))
	}
	sort.Strings(out)
	return out
}

// validateConsumerStateFor checks a write's consumer_state against the
// declaration of the type that write names.
//
// Resolving the type is the domain's job (DomainPolicy.TypeRef), so a memory
// write is checked against its namespace's {type} segment, a knowledge write
// against its facet_kind, and an event write against its stream — without this
// function knowing that any of those axes exist.
//
// A type that declares no required_fields, or a revision naming no declared
// type at all, still gets the well-formedness and object checks. Those are
// properties of the mechanism rather than of any declaration: an index cannot
// be built over a scalar, whoever wrote it.
//
// # Why required_fields means STATE here and PAYLOAD on the context vocabulary
//
// The same field name governs two different documents, and it is not an
// overload so much as the only document each vocabulary has. A context record
// carries a free-form JSON payload, so required_fields there can only mean
// payload keys — ValidateContextRequiredFields. A memory, knowledge or event
// revision has no free-form payload at all: Payload is a fixed struct of
// summary and body, both already required or optional by the write validator.
// The only place a declared field name can point is consumer_state.
func validateConsumerStateFor(in WriteInput) error {
	var required []string
	if policy, err := policyFor(in.Domain); err == nil {
		if vocabID, typeID := policy.TypeRef(in.Namespace, in.Facets); typeID != "" {
			if t, ok := typeregistry.Default().Lookup(vocabID, typeID); ok {
				required = t.RequiredFields
			}
		}
	}
	return validateConsumerState(in.ConsumerState, required)
}
