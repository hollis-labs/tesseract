package memory

import (
	"encoding/json"
	"time"

	"github.com/hollis-labs/tesseract/domains"
)

// PayloadMode controls how much of each recall result is serialized on a
// read surface. It is the projection knob for recall/lookup responses.
//
// The three modes form a strict ladder — each one is a superset of the one
// before it:
//
//	keys     identity only: revision_id, memory_id, domain, namespace,
//	         memory_key, created_at (+ score). The browse/enumerate shape.
//	summary  keys + status, tags, confidence, payload.summary and
//	         consumer_state. The working default: enough to triage without
//	         carrying bodies.
//	full     the complete RecallResult, including payload.body and the
//	         memory_state block.
//
// Field paths are stable across modes: a caller reads
// `revision.payload.summary` in both summary and full mode. Only presence
// varies, never nesting.
type PayloadMode string

const (
	// PayloadModeKeys returns identity rows only. This is the enumerate /
	// browse affordance — no separate "list keys" tool is needed (D6).
	PayloadModeKeys PayloadMode = "keys"
	// PayloadModeSummary returns identity plus payload.summary.
	PayloadModeSummary PayloadMode = "summary"
	// PayloadModeFull returns the unprojected RecallResult.
	PayloadModeFull PayloadMode = "full"
)

// DefaultPayloadMode is the canonical fallback when neither a per-call
// argument nor app config selects a mode.
//
// config.Defaults().Read.PayloadMode must equal this string;
// TestConfigDefaultMatchesMemoryDefault in internal/mcpadapter binds the two
// so they cannot drift.
const DefaultPayloadMode = PayloadModeSummary

// Valid reports whether m is one of the three canonical payload modes.
func (m PayloadMode) Valid() bool {
	switch m {
	case PayloadModeKeys, PayloadModeSummary, PayloadModeFull:
		return true
	}
	return false
}

// ProjectedRevision is the identity-and-triage subset of a Revision that
// survives keys and summary projection.
//
// The field set under summary mode deliberately matches the long-standing
// condensed recall shape served by GET /v1/recall?format=brief
// (contextapi.recallBriefItem) — revision_id, memory_id, domain, namespace,
// memory_key, tags, confidence, summary, created_at — so the repo carries
// one condensed-recall field set rather than two. It differs only in that
// the fields stay nested under `revision`, matching full mode, and that
// `status` is carried (a triage signal: draft vs canonical vs deprecated).
type ProjectedRevision struct {
	RevisionID string         `json:"revision_id"`
	MemoryID   string         `json:"memory_id"`
	Domain     domains.Domain `json:"domain"`
	Namespace  string         `json:"namespace"`
	MemoryKey  string         `json:"memory_key,omitempty"`
	CreatedAt  time.Time      `json:"created_at"`

	// Present under summary mode only.
	//
	// Confidence is a pointer for the same reason RecallResult.Score is:
	// 0 is a legitimate value (write validates confidence into [0, 1.0]
	// inclusive), so a `float64` with omitempty would silently drop the
	// field on exactly the lowest-confidence revisions — the ones a triage
	// pass most wants to see. Absent means "this mode does not carry it",
	// never "zero".
	//
	// Tags keeps omitempty instead: for a list, absent and empty carry the
	// same meaning, so nothing is lost.
	Status     Status   `json:"status,omitempty"`
	Tags       []string `json:"tags,omitempty"`
	Confidence *float64 `json:"confidence,omitempty"`
	Payload    *Payload `json:"payload,omitempty"`

	// ConsumerState rides on summary and not on keys, by the same argument
	// PointerHealth does (CW-20260909-0036).
	//
	// Carrying it under summary is what makes structured objects usable at the
	// DEFAULT projection: a consumer listing its todos wants section, completed
	// and due_at, and if the only way to see them is payload_mode=full then
	// every list view drags every body through the wire to read four scalars.
	// That is the opposite of what the projection ladder is for.
	//
	// Withholding it under keys keeps that rung identity-only. A caller
	// enumerating IDs who then wants lifecycle re-requests at summary — the
	// same recall → choose → hydrate step the modes already ask for.
	//
	// omitempty is correct here where it would be wrong for Confidence: a
	// json.RawMessage is absent or it is a JSON object, and there is no
	// zero-valued bag that means something different from no bag at all.
	ConsumerState json.RawMessage `json:"consumer_state,omitempty"`
}

// ProjectedResult is the wire shape of one recall hit under keys or summary
// projection. It mirrors RecallResult's nesting so callers read the same
// paths in every mode.
//
// PayloadMode is always populated here and is the load-bearing part of the
// contract. Payload.Body carries `omitempty`, so an omitted body is
// otherwise indistinguishable from a body that is genuinely empty. A caller
// that intends to edit and write back a body MUST NOT treat a missing body
// as empty when this field is present and not "full" — it must either
// re-request with payload_mode=full or hydrate the revision by ID via
// tesseract_get_revision.
//
// Full mode does not use this type at all: it serializes RecallResult
// unchanged, so `payload_mode` is absent there and full responses stay
// byte-identical to their pre-projection form.
// PointerHealth rides on summary (and therefore on the default projection)
// but NOT on keys, and both halves of that are deliberate.
//
// Carrying it under summary is the whole point: `summary` is
// DefaultPayloadMode, and a staleness signal that only appears when a caller
// opts into `full` is not discoverable — nobody opts in to look for a problem
// they have not been told about. It is a triage signal, which is exactly what
// summary mode is for.
//
// Withholding it under keys keeps that mode's documented contract intact:
// keys is identity-only, the enumerate/browse shape, and the three modes form
// a strict ladder. Adding a non-identity field there would widen the narrowest
// rung for every caller that asked only for IDs. A caller enumerating keys who
// then wants health re-requests at summary — the same recall → choose →
// hydrate step the modes already ask for.
//
// It sits beside Revision rather than inside it so the field path is
// `pointer_health` in both summary and full, matching how RecallResult nests
// it. Absent means the revision carries no pointer at all — never "healthy",
// and never "we don't know", which is what PointerHealthUnchecked is for.
type ProjectedResult struct {
	Revision      ProjectedRevision `json:"revision"`
	Score         *float64          `json:"score,omitempty"`
	PayloadMode   PayloadMode       `json:"payload_mode"`
	PointerHealth *PointerHealth    `json:"pointer_health,omitempty"`
}

// ProjectResults renders results under mode for serialization.
//
// Under PayloadModeFull it returns results unchanged — the caller marshals
// []RecallResult exactly as before projection existed. Under keys and
// summary it returns []ProjectedResult. The return type is `any` because
// the two shapes are genuinely different documents, not two fillings of one
// struct; callers hand the value straight to a JSON encoder.
//
// An unrecognized mode is treated as DefaultPayloadMode. Surfaces that can
// report an error to their caller should validate with Valid() first and
// reject rather than relying on this fallback.
func ProjectResults(results []RecallResult, mode PayloadMode) any {
	if mode == PayloadModeFull {
		return results
	}
	if !mode.Valid() {
		mode = DefaultPayloadMode
	}

	out := make([]ProjectedResult, 0, len(results))
	for _, r := range results {
		pr := ProjectedResult{
			Revision: ProjectedRevision{
				RevisionID: r.Revision.RevisionID,
				MemoryID:   r.Revision.MemoryID,
				Domain:     r.Revision.Domain,
				Namespace:  r.Revision.Namespace,
				MemoryKey:  r.Revision.MemoryKey,
				CreatedAt:  r.Revision.CreatedAt,
			},
			Score:       r.Score,
			PayloadMode: mode,
		}
		if mode == PayloadModeSummary {
			confidence := r.Revision.Confidence
			pr.Revision.Status = r.Revision.Status
			pr.Revision.Tags = r.Revision.Tags
			pr.Revision.Confidence = &confidence
			// Summary only — Body is dropped, never truncated. A caller
			// that needs it hydrates by revision_id.
			pr.Revision.Payload = &Payload{Summary: r.Revision.Payload.Summary}
			pr.Revision.ConsumerState = r.Revision.ConsumerState
			pr.PointerHealth = r.PointerHealth
		}
		out = append(out, pr)
	}
	return out
}

// ProjectRevisions renders bare revisions under mode, for read paths that
// return revisions rather than ranked results — the event log, today.
//
// It exists rather than routing the log through ProjectResults because a
// RecallResult carries a Score and a State, and a log entry has neither. Score
// would be nil, which is honest; State would be the zero value, which is not.
// An event's memory_state row exists but never moves — the domain opts out of
// activation — so serializing an activation of 0 and a nil last_accessed_at
// would answer a question the caller did not ask with a number that means
// nothing. Better to have no field than a fabricated one.
//
// Under PayloadModeFull it returns revs unchanged. Under keys and summary it
// returns []ProjectedRevision — the same field set ProjectResults projects,
// so `revision.payload.summary` is the same path here as there, one nesting
// level shallower because there is no result envelope to nest inside.
//
// An unrecognized mode is treated as DefaultPayloadMode, matching
// ProjectResults. Surfaces that can report an error should validate with
// Valid() first.
func ProjectRevisions(revs []Revision, mode PayloadMode) any {
	if mode == PayloadModeFull {
		return revs
	}
	if !mode.Valid() {
		mode = DefaultPayloadMode
	}

	out := make([]ProjectedRevision, 0, len(revs))
	for _, r := range revs {
		pr := ProjectedRevision{
			RevisionID: r.RevisionID,
			MemoryID:   r.MemoryID,
			Domain:     r.Domain,
			Namespace:  r.Namespace,
			MemoryKey:  r.MemoryKey,
			CreatedAt:  r.CreatedAt,
		}
		if mode == PayloadModeSummary {
			confidence := r.Confidence
			pr.Status = r.Status
			pr.Tags = r.Tags
			pr.Confidence = &confidence
			// Summary only — Body is dropped, never truncated. A caller that
			// needs the reasoning itself re-reads at full or hydrates by
			// revision_id.
			pr.Payload = &Payload{Summary: r.Payload.Summary}
			pr.ConsumerState = r.ConsumerState
		}
		out = append(out, pr)
	}
	return out
}
