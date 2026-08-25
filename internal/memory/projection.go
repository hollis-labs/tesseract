package memory

import (
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
//	summary  keys + status, tags, confidence, and payload.summary. The
//	         working default: enough to triage without carrying bodies.
//	full     the complete RecallResult, including payload.body and state.
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
// memory_get_revision.
//
// Full mode does not use this type at all: it serializes RecallResult
// unchanged, so `payload_mode` is absent there and full responses stay
// byte-identical to their pre-projection form.
type ProjectedResult struct {
	Revision    ProjectedRevision `json:"revision"`
	Score       *float64          `json:"score,omitempty"`
	PayloadMode PayloadMode       `json:"payload_mode"`
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
		}
		out = append(out, pr)
	}
	return out
}
