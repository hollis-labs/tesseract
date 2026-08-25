package memory

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"time"
)

// --- Pointer health: what a verification observed, and what a reader sees ---
//
// Two vocabularies live here and they are deliberately NOT the same size.
//
//   - PointerOutcome is what a resolver can OBSERVE. Three values. Every row
//     in pointer_verifications carries exactly one of them.
//   - PointerHealthStatus is what a READER sees. The same three values, plus
//     two more that the table structurally cannot hold, because they are not
//     observations at all: "nobody has looked" and "there is nothing to look
//     at".
//
// The second vocabulary is a superset of the first rather than a translation
// of it, so a caller never has to map between them. A status that names an
// outcome means exactly that outcome was the most recent observation.

// PointerOutcome is the result of one attempt to resolve a pointer against
// the outside world.
type PointerOutcome string

const (
	// OutcomeResolved: the resolver got a definitive positive — the target
	// is there and we saw it.
	OutcomeResolved PointerOutcome = "resolved"

	// OutcomeUnresolvable: the resolver got a definitive NEGATIVE from an
	// authority that answered. A file whose parent directory is readable and
	// which is not in it; an HTTP origin that replied 404 or 410. This is the
	// only value that asserts a pointer is dead, and it is reserved for
	// answers, never for silence.
	OutcomeUnresolvable PointerOutcome = "unresolvable"

	// OutcomeUnverifiable: the resolver could not obtain a definitive answer.
	// A timeout, a DNS failure, a 5xx, a rate limit, a 403 from a host that
	// clearly exists, or a scheme no resolver in this build knows how to
	// check. The pointer may be perfectly alive.
	//
	// This value exists because the alternative — folding "we could not tell"
	// into "dead" — is the failure mode that makes a health signal worse than
	// no health signal. A transient outage would permanently brand a live
	// reference, and the first false positive teaches every reader to ignore
	// the field.
	OutcomeUnverifiable PointerOutcome = "unverifiable"
)

// Valid reports whether o is a recognized observation outcome.
func (o PointerOutcome) Valid() bool {
	switch o {
	case OutcomeResolved, OutcomeUnresolvable, OutcomeUnverifiable:
		return true
	}
	return false
}

// PointerOutcomeVocabulary returns every storable outcome, sorted.
//
// The pointer_verifications.outcome CHECK constraint (schema 13) must permit
// exactly this set and nothing else. The two are separate renderings of one
// rule — Go cannot generate the DDL without contextstore importing this
// package, which would invert the dependency — so
// TestPointerVerificationOutcomeCheckMatchesVocabulary drives every member
// through a real INSERT, and drives non-members through too, to catch either
// side drifting.
func PointerOutcomeVocabulary() []string {
	out := []string{
		string(OutcomeResolved),
		string(OutcomeUnresolvable),
		string(OutcomeUnverifiable),
	}
	sort.Strings(out)
	return out
}

// PointerHealthStatus is the pointer state a read surface reports.
type PointerHealthStatus string

const (
	// The three that mirror an observation. Each means "this was the most
	// recent thing a resolver observed".
	PointerHealthResolved     PointerHealthStatus = PointerHealthStatus(OutcomeResolved)
	PointerHealthUnresolvable PointerHealthStatus = PointerHealthStatus(OutcomeUnresolvable)
	PointerHealthUnverifiable PointerHealthStatus = PointerHealthStatus(OutcomeUnverifiable)

	// PointerHealthUnchecked: the pointer names an external resource and NO
	// observation exists for this revision.
	//
	// This is the value that makes absence expressible. Without it, a
	// never-verified pointer and a pointer verified-and-found-missing both
	// render as "not resolved", and the corpus cannot answer "what have we
	// never looked at?" — which is the larger population by far on any store
	// where the job has not yet run everywhere.
	PointerHealthUnchecked PointerHealthStatus = "unchecked"

	// PointerHealthNotApplicable: the record declares it has NO external
	// source (scheme "nil"). There is nothing to resolve, and that is a
	// correct, first-class way to write knowledge — the body is the artifact.
	//
	// Derived from the scheme; never stored. Verification never runs against
	// these, so recording an observation would be recording a fiction. It is
	// separate from unverifiable because lumping the two together would list
	// the recommended pattern alongside genuinely suspect entries.
	PointerHealthNotApplicable PointerHealthStatus = "not_applicable"
)

// Valid reports whether s is a recognized pointer health status.
func (s PointerHealthStatus) Valid() bool {
	switch s {
	case PointerHealthResolved, PointerHealthUnresolvable, PointerHealthUnverifiable,
		PointerHealthUnchecked, PointerHealthNotApplicable:
		return true
	}
	return false
}

// PointerHealthStatusVocabulary returns every valid status, sorted. Used to
// render allowed values in tool descriptions and validation errors so those
// cannot advertise a set the filter does not accept.
func PointerHealthStatusVocabulary() []string {
	out := []string{
		string(PointerHealthNotApplicable),
		string(PointerHealthResolved),
		string(PointerHealthUnchecked),
		string(PointerHealthUnresolvable),
		string(PointerHealthUnverifiable),
	}
	sort.Strings(out)
	return out
}

// SchemeNil is the pointer scheme meaning "this record has no external
// source". knowledge.Store.Write requires a non-empty scheme and locator, so
// an entry whose content IS the artifact says so with this scheme rather than
// by omitting the pointer.
const SchemeNil = "nil"

// PointerHealth is the verification state of one revision's pointer, as seen
// by a reader.
type PointerHealth struct {
	Status PointerHealthStatus `json:"status"`

	// CheckedAt is when the observation behind Status was made. Absent for
	// unchecked and not_applicable, which are not observations.
	CheckedAt *time.Time `json:"checked_at,omitempty"`

	// Detail is the resolver's short machine-ish reason, e.g. "http_404",
	// "not_found", "timeout", "unsupported_scheme:conduit". It is what
	// separates the several very different situations that all land on
	// unverifiable — without it, a rate limit and a scheme nothing can check
	// are indistinguishable.
	Detail string `json:"detail,omitempty"`

	// LastResolvedAt is the most recent observation of this pointer that
	// resolved, if any.
	//
	// It is the transient-vs-dead discriminator at READ time: a pointer whose
	// status is unverifiable but which resolved an hour ago is a blip; one
	// that has never resolved is a different problem. Latest-outcome alone
	// cannot tell those apart, and asking every reader to go query the
	// observation log would mean nobody does.
	LastResolvedAt *time.Time `json:"last_resolved_at,omitempty"`
}

// DerivePointerHealth computes the reader-facing health of a revision's
// pointer from the pointer itself plus the latest observation (nil when none
// exists).
//
// This is the Go half of a rule that also exists in SQL, as
// pointerHealthStatusExpr, so the recall filter can apply it before LIMIT.
// TestPointerHealth_FilterCoversEveryState binds the two: it drives every
// state through both renderings and fails if they disagree.
func DerivePointerHealth(p *Pointer, latest *PointerObservation, lastResolvedAt *time.Time) *PointerHealth {
	// No pointer facet at all — memory-domain revisions, and any knowledge
	// revision written before facets existed. The field is omitted entirely
	// rather than given a status, because "this record has no pointer" is not
	// a health state of a pointer.
	if p == nil || p.Scheme == "" {
		return nil
	}
	if p.Scheme == SchemeNil {
		return &PointerHealth{Status: PointerHealthNotApplicable}
	}
	if latest == nil {
		return &PointerHealth{Status: PointerHealthUnchecked, LastResolvedAt: lastResolvedAt}
	}
	checked := latest.CheckedAt
	return &PointerHealth{
		Status:         PointerHealthStatus(latest.Outcome),
		CheckedAt:      &checked,
		Detail:         latest.Detail,
		LastResolvedAt: lastResolvedAt,
	}
}

// pointerHealthStatusExpr is the SQL rendering of DerivePointerHealth's status
// rule, over memory_revisions aliased as r.
//
// It is a scalar expression rather than a JOIN so it can drop into the shared
// WHERE-fragment builder, which does not own the FROM clause. The
// no-pointer case maps to the sentinel below rather than to NULL so that
// `IN (...)` behaves — a NULL would silently match nothing and turn a filter
// bug into an empty result set, which is exactly the shape of failure this
// ticket exists to remove.
const pointerHealthStatusExpr = `
CASE
  WHEN r.facet_pointer_scheme IS NULL OR r.facet_pointer_scheme = '' THEN '` + pointerHealthNoPointer + `'
  WHEN r.facet_pointer_scheme = '` + SchemeNil + `' THEN '` + string(PointerHealthNotApplicable) + `'
  ELSE COALESCE((
    SELECT pv.outcome FROM pointer_verifications pv
    WHERE pv.revision_id = r.revision_id
    ORDER BY pv.id DESC LIMIT 1
  ), '` + string(PointerHealthUnchecked) + `')
END`

// pointerHealthNoPointer is the SQL-side sentinel for "this revision carries
// no pointer facet". It is not part of PointerHealthStatusVocabulary: on the
// wire that case is expressed by the pointer_health field being absent, and
// accepting it as a filter value would mean advertising a status no result
// can ever display.
const pointerHealthNoPointer = "none"

// PointerObservation is one recorded row of pointer_verifications.
type PointerObservation struct {
	RevisionID string         `json:"revision_id"`
	Scheme     string         `json:"scheme"`
	Locator    string         `json:"locator"`
	Outcome    PointerOutcome `json:"outcome"`
	CheckedAt  time.Time      `json:"checked_at"`
	Detail     string         `json:"detail,omitempty"`
}

// attachPointerHealth batch-loads verification state for a result set and
// hangs it off each result.
//
// It runs after ranking and truncation in both recall paths, so the number of
// revisions it looks up is bounded by the caller's limit rather than by the
// candidate set. Mirrors fetchStates, which solves the same problem for
// memory_state.
//
// Best-effort by design: if the lookup fails, recall still returns its
// results. A verification side table going wrong must not be able to take
// recall down with it — recall worked before this table existed and has to
// keep working if it is unreadable.
//
// What a failed lookup must NOT do is omit the field. Absent is contractually
// "this revision has no pointer", which is a claim about the record; an
// unreadable log is a claim about us. Omitting it would make an entire
// knowledge namespace read as pointer-free, which is both false and
// unfalsifiable from the caller's side. A failed lookup falls through with no
// observations, so every pointer renders as `unchecked` — the value that
// exists to say nobody has looked. On this path nobody could.
func (s *Store) attachPointerHealth(ctx context.Context, results []RecallResult) ([]RecallResult, error) {
	if len(results) == 0 {
		return results, nil
	}

	ids := make([]string, 0, len(results))
	for _, r := range results {
		p := r.Revision.Facets.Pointer
		// Skip revisions that can never have an observation: no pointer, or a
		// scheme that declares no external source. Keeps the IN-list to the
		// revisions actually worth looking up.
		if p == nil || p.Scheme == "" || p.Scheme == SchemeNil {
			continue
		}
		ids = append(ids, r.Revision.RevisionID)
	}

	latest := map[string]PointerObservation{}
	lastResolved := map[string]time.Time{}
	if len(ids) > 0 {
		obs, resolved, err := s.fetchPointerObservations(ctx, ids)
		if err == nil {
			latest, lastResolved = obs, resolved
		}
		// On error the maps stay empty and we fall through rather than
		// returning early: DerivePointerHealth with no observation yields
		// `unchecked`, which is the honest rendering of an unreadable log.
		// Returning here would omit the field and assert the revisions have
		// no pointers.
	}

	for i := range results {
		rev := results[i].Revision
		var obs *PointerObservation
		if o, ok := latest[rev.RevisionID]; ok {
			obs = &o
		}
		var lr *time.Time
		if t, ok := lastResolved[rev.RevisionID]; ok {
			lr = &t
		}
		results[i].PointerHealth = DerivePointerHealth(rev.Facets.Pointer, obs, lr)
	}
	return results, nil
}

// fetchPointerObservations loads, for each of the given revision IDs, the most
// recent observation and the timestamp of the most recent RESOLVED observation.
//
// Two queries rather than one join: each is independently obviously correct,
// and the second reads a different slice of the log (any resolved row, not the
// newest row).
func (s *Store) fetchPointerObservations(ctx context.Context, revisionIDs []string) (map[string]PointerObservation, map[string]time.Time, error) {
	ph := placeholders(len(revisionIDs))
	args := make([]interface{}, len(revisionIDs))
	for i, id := range revisionIDs {
		args[i] = id
	}

	// Latest observation per revision. `id` is monotonic (AUTOINCREMENT), so
	// MAX(id) is the newest row even when two checks land in the same
	// timestamp tick — ordering on checked_at alone would be ambiguous there.
	latestQuery := fmt.Sprintf( //nolint:gosec // ph is parameterized ?s, not user input
		`SELECT pv.revision_id, pv.scheme, pv.locator, pv.outcome, pv.checked_at, pv.detail
FROM pointer_verifications pv
WHERE pv.revision_id IN (%s)
  AND pv.id = (SELECT MAX(pv2.id) FROM pointer_verifications pv2 WHERE pv2.revision_id = pv.revision_id)`, ph)

	rows, err := s.db.QueryContext(ctx, latestQuery, args...)
	if err != nil {
		return nil, nil, fmt.Errorf("latest pointer observations: %w", err)
	}
	defer func() { _ = rows.Close() }()

	latest := make(map[string]PointerObservation, len(revisionIDs))
	for rows.Next() {
		var o PointerObservation
		var outcome, checkedAt string
		if scanErr := rows.Scan(&o.RevisionID, &o.Scheme, &o.Locator, &outcome, &checkedAt, &o.Detail); scanErr != nil {
			return nil, nil, fmt.Errorf("scan pointer observation: %w", scanErr)
		}
		o.Outcome = PointerOutcome(outcome)
		o.CheckedAt, _ = parseMemoryTime(checkedAt)
		latest[o.RevisionID] = o
	}
	if rows.Err() != nil {
		return nil, nil, fmt.Errorf("iterate pointer observations: %w", rows.Err())
	}

	resolvedQuery := fmt.Sprintf( //nolint:gosec // ph is parameterized ?s, not user input
		`SELECT revision_id, MAX(checked_at)
FROM pointer_verifications
WHERE revision_id IN (%s) AND outcome = ?
GROUP BY revision_id`, ph)
	resolvedArgs := append(append([]interface{}{}, args...), string(OutcomeResolved))

	rrows, err := s.db.QueryContext(ctx, resolvedQuery, resolvedArgs...)
	if err != nil {
		return nil, nil, fmt.Errorf("last resolved observations: %w", err)
	}
	defer func() { _ = rrows.Close() }()

	lastResolved := make(map[string]time.Time, len(revisionIDs))
	for rrows.Next() {
		var id string
		var ts sql.NullString
		if scanErr := rrows.Scan(&id, &ts); scanErr != nil {
			return nil, nil, fmt.Errorf("scan last resolved: %w", scanErr)
		}
		if ts.Valid {
			t, parseErr := parseMemoryTime(ts.String)
			if parseErr == nil {
				lastResolved[id] = t
			}
		}
	}
	if rrows.Err() != nil {
		return nil, nil, fmt.Errorf("iterate last resolved: %w", rrows.Err())
	}

	return latest, lastResolved, nil
}
