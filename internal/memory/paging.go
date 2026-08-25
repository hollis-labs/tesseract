package memory

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/hollis-labs/tesseract/domains"
)

// ── Budget ───────────────────────────────────────────────────────────────────

// Budget bounds how much of a read response is serialized.
//
// A zero field means "no ceiling from this dimension" — it is the unset
// state, not a budget of zero. Surfaces that accept these as call arguments
// decode them into pointers so an explicitly-passed 0 stays distinguishable
// from an absent field, and reject the explicit 0 as a validation error: a
// zero budget can only produce an empty page, which contradicts the
// always-make-progress rule in budgetCut.
type Budget struct {
	// Bytes caps the marshaled size of the results array.
	Bytes int
	// Tokens caps EstimateTokens over that same array.
	Tokens int
}

// Set reports whether the budget constrains anything.
func (b Budget) Set() bool { return b.Bytes > 0 || b.Tokens > 0 }

// Truncation reasons. Closed vocabulary — every value Manifest.TruncationReason
// can take when Truncated is true.
//
// The FIELD names truncated / truncation_reason are taken verbatim from the
// context_packet manifest (internal/mcpadapter/tools.go, handlePacket), which
// is the repo's existing budget envelope. The VALUES are this domain's own,
// because context_packet's ("budget.max_items", "budget.max_tokens_estimate")
// name its internal variables rather than its arguments, so copying them would
// import a naming inconsistency rather than a vocabulary. Each value below
// names the knob a caller would change to get a different answer.
const (
	// TruncationBudgetBytes — budget_bytes cut the page.
	TruncationBudgetBytes = "budget_bytes"
	// TruncationBudgetTokens — budget_tokens cut the page.
	TruncationBudgetTokens = "budget_tokens"
	// TruncationLimit — more rows matched than limit allowed. Page with
	// next_cursor to continue.
	TruncationLimit = "limit"
	// TruncationPayloadModeLimitCap — limit was clamped down because
	// payload_mode=full carries bodies. See ClampRecallLimit.
	TruncationPayloadModeLimitCap = "payload_mode_limit_cap"
)

// ── Limits ───────────────────────────────────────────────────────────────────

const (
	// DefaultRecallLimit is the page size when the caller does not pass one.
	DefaultRecallLimit = 30
	// MaxRecallLimit is the advertised ceiling for the projected payload
	// modes (keys, summary).
	MaxRecallLimit = 500
	// MaxRecallLimitFull is the ceiling under payload_mode=full.
	//
	// full mode carries payload.body, so its per-result cost is set by content
	// rather than by the response envelope, and runs an order of magnitude
	// above the projected modes. Re-derive rather than trust the figures
	// below — they are measurements over mutable data, and a command that is
	// repeatable is not the same as a number that is reproducible:
	//
	//	sqlite3 -readonly ~/.local/share/tesseract/workspaces/default/main.db \
	//	  ".backup /tmp/tessmeasure/data/index/context.db"
	//	TESS_MEASURE_DB=/tmp/tessmeasure \
	//	  go test ./internal/mcpadapter/ -run TestRecallCostCurve -v
	//
	// Measured at corpus revisions=1639, max created_at
	// 2026-08-25T19:18:16.373021Z, slice namespaces=["user/chrispian/memory"]
	// ranking=activation (1,136 current revisions matched) — results-array
	// bytes:
	//
	//	limit=100   full   235,482   summary  62,320   keys  31,703
	//	limit=500   full 1,559,859   summary 364,677   keys 154,884
	//
	// 500 under full is ~390K tokens by the 4-chars-per-token heuristic, which
	// no caller can receive. Capping full at 100 puts its worst case in the
	// same order as a large projected page rather than an order above it; keys
	// and summary keep the full 500 because they stay cheap there.
	//
	// The clamp is never silent: it sets truncated + a truncation_reason of
	// TruncationPayloadModeLimitCap and issues a next_cursor, so the rows past
	// the cap stay reachable by paging rather than by raising limit.
	MaxRecallLimitFull = 100
	// MaxHistoryLimit is the ceiling for memory_history / knowledge_history.
	MaxHistoryLimit = 500
)

// ClampRecallLimit resolves a requested limit against the ceiling for mode.
// It returns the effective limit and whether the ceiling bound it.
//
// A limit of 0 or less means "unspecified" and resolves to DefaultRecallLimit,
// which is never reported as capped: the caller expressed no preference, so
// nothing of theirs was overridden.
func ClampRecallLimit(limit int, mode PayloadMode) (effective int, capped bool) {
	if limit <= 0 {
		return DefaultRecallLimit, false
	}
	ceiling := MaxRecallLimit
	if mode == PayloadModeFull {
		ceiling = MaxRecallLimitFull
	}
	if limit > ceiling {
		return ceiling, true
	}
	return limit, false
}

// ClampHistoryLimit resolves a requested history limit. Zero or less means
// unlimited, which is history's pre-existing behavior and stays the default.
func ClampHistoryLimit(limit int) int {
	if limit <= 0 {
		return 0
	}
	if limit > MaxHistoryLimit {
		return MaxHistoryLimit
	}
	return limit
}

// ── Manifest ─────────────────────────────────────────────────────────────────

// Manifest is the read-envelope sidecar carried next to a paged result array.
//
// Every field is emitted unconditionally. That is the point of the type: a
// caller must be able to tell "the answer is zero / false / none" from "the
// server did not say", and `omitempty` on any of these collapses exactly the
// case the caller most needs. Three fields here are in the class that has
// already produced shipped bugs in this repo (RecallResult.Score,
// synthesisSource.Score, Payload.Confidence — all fixed by becoming pointers):
//
//	Truncated        false is the load-bearing value. It means "you got
//	                 everything", which is the only way a caller learns its
//	                 result set is complete. omitempty would erase it.
//	TruncationReason emitted as "" when Truncated is false, rather than
//	                 omitted, so the pair is always read together. The
//	                 biconditional (Truncated <=> reason != "") is asserted
//	                 in tests.
//	NextCursor       a *string, null when there is no further page. An empty
//	                 string would be indistinguishable from a cursor that
//	                 happens to encode to "" — null says "exhausted" and
//	                 nothing else. Matches GET /v1/context/history, which
//	                 already emits "next_cursor": null when exhausted.
//
// ResultsTotal / ResultsReturned / BytesReturned / TokensEstimate are plain
// ints without omitempty for the same reason: an empty result set has
// ResultsTotal 0, and that must be visible.
//
// BytesReturned measures the marshaled results array — brackets and commas
// included — not the enclosing envelope, and not the payload alone. It is the
// quantity budget_bytes bounds. This differs deliberately from
// context_packet's bytes_returned, which counts record payloads only: for a
// projected recall the structural bytes outweigh the payload (measured at
// limit=100 under summary: 62,320 wire against 15,238 of payload, so ~76% of
// the response is not payload), and a payload-only count would not bound the
// response it claims to bound. Re-derive with TestProjectionSize; the corpus
// stamp for that figure is on MaxRecallLimitFull above.
type Manifest struct {
	// ResultsTotal is how many rows matched before offset and limit
	// windowing. For ranking=relevance it is bounded by the per-arm
	// candidate cap (relevanceArmLimit), not by the corpus.
	ResultsTotal int `json:"results_total"`
	// ResultsReturned is how many rows this response actually carries.
	ResultsReturned int `json:"results_returned"`
	// BytesReturned is the marshaled size of the results array.
	BytesReturned int `json:"bytes_returned"`
	// TokensEstimate is EstimateTokens over BytesReturned.
	TokensEstimate int `json:"tokens_estimate"`
	// Truncated is false only when this response carries every remaining row.
	Truncated bool `json:"truncated"`
	// TruncationReason is one of the Truncation* constants, or "" when
	// Truncated is false.
	TruncationReason string `json:"truncation_reason"`
	// NextCursor resumes paging after the last row in this response. Null
	// when there is nothing left.
	NextCursor *string `json:"next_cursor"`
}

// EstimateTokens approximates tokens from a byte count using the same
// ~4-chars-per-token heuristic as github.com/hollis-labs/go-mcp/budget's
// EstimateTokens. Reimplemented over an int rather than a []byte so the
// count can be derived from sizes already computed, without re-marshaling.
func EstimateTokens(n int) int {
	if n <= 0 {
		return 0
	}
	return (n + 3) / 4
}

// ── Cursor ───────────────────────────────────────────────────────────────────

// ErrInvalidCursor is returned when a cursor is malformed, carries an
// unsupported version, or was issued against a different ordering.
var ErrInvalidCursor = errors.New("invalid cursor")

// cursorVersion is bumped whenever the encoded shape changes. A cursor from a
// different version is rejected rather than reinterpreted.
const cursorVersion = 1

// cursorPayload is the decoded interior of a cursor. Callers never see it —
// the wire form is opaque base64url and must be treated as such.
type cursorPayload struct {
	V int    `json:"v"`
	O int    `json:"o"`
	F string `json:"f"`
}

// EncodeCursor produces the opaque resume token for the given offset under
// the given ordering fingerprint.
func EncodeCursor(offset int, fingerprint string) string {
	raw, err := json.Marshal(cursorPayload{V: cursorVersion, O: offset, F: fingerprint})
	if err != nil {
		// cursorPayload is three scalars; Marshal cannot fail. Returning ""
		// would look like "no next page", so panic-free fallback is to
		// encode nothing and let the caller see an absent cursor.
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}

// DecodeCursor validates raw against fingerprint and returns its offset.
//
// The fingerprint check is the reason a cursor is not just an integer. An
// offset is a position in an ordering; resuming one ordering's offset into a
// different ordering yields rows that look plausible and are wrong. Any
// change to the inputs that determine the sequence — ranking, namespaces,
// revision_scope, query, any filter, the reranker — changes the fingerprint
// and makes the cursor an error rather than a silent misread.
//
// payload_mode and limit are deliberately NOT part of the fingerprint. Neither
// changes the sequence: ProjectResults is a per-element map that cannot
// reorder, and limit only sets where pages break. Binding them would reject
// legitimate paging (browsing under keys, then continuing under summary; or
// paging the same query with a smaller page size) with no correctness gain.
//
// Reranker and RerankerTopK ARE in the fingerprint, even though RecallPaged
// refuses a reranker before any cursor is issued or checked. They belong to
// the ordering, so if that refusal is ever lifted for a rerank-then-page
// design, the fingerprint must already account for them rather than have to
// remember to.
func DecodeCursor(raw, fingerprint string) (int, error) {
	if raw == "" {
		return 0, nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return 0, fmt.Errorf("%w: not a valid cursor token", ErrInvalidCursor)
	}
	var p cursorPayload
	if err := json.Unmarshal(decoded, &p); err != nil {
		return 0, fmt.Errorf("%w: not a valid cursor token", ErrInvalidCursor)
	}
	if p.V != cursorVersion {
		return 0, fmt.Errorf("%w: cursor version %d is not supported", ErrInvalidCursor, p.V)
	}
	if p.O < 0 {
		return 0, fmt.Errorf("%w: cursor carries a negative offset", ErrInvalidCursor)
	}
	if p.F != fingerprint {
		return 0, fmt.Errorf("%w: cursor was issued for a different query — "+
			"ranking, namespaces, revision_scope, query, filters or reranker changed since it was issued; "+
			"restart paging without a cursor", ErrInvalidCursor)
	}
	return p.O, nil
}

// ── Ordering fingerprints ────────────────────────────────────────────────────

// orderingKey is the canonical form of everything that determines the ordered
// candidate sequence for a recall. Field order here is the serialization
// order, so it must stay stable: changing it invalidates every outstanding
// cursor. That is safe (cursors are per-session paging tokens, not durable
// references) but should be deliberate.
type orderingKey struct {
	Namespaces    []string `json:"ns"`
	RevisionScope string   `json:"scope"`
	Ranking       string   `json:"rank"`
	Query         string   `json:"q"`
	Origins       []string `json:"origins"`
	Statuses      []string `json:"statuses"`
	Tags          []string `json:"tags"`
	ConfidenceMin float64  `json:"conf"`
	Since         string   `json:"since"`
	Until         string   `json:"until"`
	Domains       []string `json:"domains"`
	FacetKinds    []string `json:"kinds"`
	FacetSources  []string `json:"sources"`
	PointerHealth []string `json:"pointer_health"`
	Reranker      string   `json:"reranker"`
	RerankerTopK  int      `json:"reranker_topk"`
}

// RecallOrderingFingerprint derives the ordering fingerprint for in.
//
// It resolves defaults first (ranking, revision_scope, statuses) so that an
// omitted argument and its explicit default value produce the same
// fingerprint — otherwise a caller who passed ranking="" on page 1 and
// ranking="activation" on page 2 would be told the sort changed when it did
// not.
func RecallOrderingFingerprint(in RecallInput) string {
	in = resolveRecallDefaults(in)

	key := orderingKey{
		Namespaces:    sortedCopy(in.Namespaces),
		RevisionScope: string(in.RevisionScope),
		Ranking:       string(in.Ranking),
		Query:         in.Query,
		Origins:       sortedStringsFrom(in.Filters.Origins, func(o Origin) string { return string(o) }),
		Statuses:      sortedStringsFrom(in.Filters.Statuses, func(s Status) string { return string(s) }),
		Tags:          sortedCopy(in.Filters.Tags),
		ConfidenceMin: in.Filters.ConfidenceMin,
		Since:         formatTimePtr(in.Filters.Since),
		Until:         formatTimePtr(in.Filters.Until),
		Domains:       sortedStringsFrom(in.Filters.Domains, func(d domains.Domain) string { return string(d) }),
		FacetKinds:    sortedCopy(in.Filters.FacetKinds),
		FacetSources:  sortedCopy(in.Filters.FacetSources),
		PointerHealth: sortedCopy(in.Filters.PointerHealth),
		Reranker:      in.Reranker,
		RerankerTopK:  in.RerankerTopK,
	}
	return fingerprintOf(key)
}

// HistoryOrderingFingerprint derives the ordering fingerprint for a revision
// history read. History ordering is fixed (created_at DESC, revision_id DESC),
// so only the identity of the series can change it — but the domain is part of
// the key because memory_history and knowledge_history read the same table
// through different filters.
func HistoryOrderingFingerprint(domain, namespace, memoryKey string) string {
	return fingerprintOf(struct {
		D string `json:"d"`
		N string `json:"n"`
		K string `json:"k"`
	}{domain, namespace, memoryKey})
}

// fingerprintOf hashes a canonical key to 16 hex characters. Truncating
// SHA-256 to 8 bytes is ample here: the fingerprint guards against a caller
// reusing a cursor across a changed query, not against an adversary
// constructing a collision, and a wrong-ordering resume would have to hit 1
// in 2^64 to slip through.
func fingerprintOf(key any) string {
	raw, err := json.Marshal(key)
	if err != nil {
		// Every field is a scalar or a []string; Marshal cannot fail. A
		// distinct sentinel is safer than "" because "" would compare equal
		// across two different failed keys.
		return "unfingerprintable"
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:8])
}

func sortedCopy(in []string) []string {
	if len(in) == 0 {
		return []string{}
	}
	out := make([]string, len(in))
	copy(out, in)
	sort.Strings(out)
	return out
}

func sortedStringsFrom[T any](in []T, f func(T) string) []string {
	out := make([]string, 0, len(in))
	for _, v := range in {
		out = append(out, f(v))
	}
	sort.Strings(out)
	return out
}

func formatTimePtr(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

// ── Budget application ───────────────────────────────────────────────────────

// budgetCut picks how many leading items fit inside b, given each item's
// marshaled size. It returns the count, the resulting array size in bytes,
// and the reason it stopped short ("" if it did not).
//
// The array size accounts for the enclosing brackets and the separating
// commas, so it equals len(json.Marshal(items[:n])) exactly. The token
// estimate is not returned because it is a pure function of the byte count —
// buildManifest derives it, so there is only one place it can be wrong.
//
// When even the first item exceeds the budget, budgetCut still returns 1 and
// reports the reason. Returning 0 would hand the caller an empty page plus a
// cursor pointing at the same offset — an infinite loop that never makes
// progress. One oversized row plus an honest truncation flag is the only
// answer that lets paging terminate.
func budgetCut(sizes []int, b Budget) (n, bytes int, reason string) {
	arrayBytes := func(k int) int {
		if k == 0 {
			return 2 // "[]"
		}
		total := 2 + (k - 1) // brackets + commas
		for i := 0; i < k; i++ {
			total += sizes[i]
		}
		return total
	}

	if !b.Set() || len(sizes) == 0 {
		full := arrayBytes(len(sizes))
		return len(sizes), full, ""
	}

	for k := 1; k <= len(sizes); k++ {
		size := arrayBytes(k)
		if b.Bytes > 0 && size > b.Bytes {
			if k == 1 {
				return 1, size, TruncationBudgetBytes
			}
			prev := arrayBytes(k - 1)
			return k - 1, prev, TruncationBudgetBytes
		}
		if b.Tokens > 0 && EstimateTokens(size) > b.Tokens {
			if k == 1 {
				return 1, size, TruncationBudgetTokens
			}
			prev := arrayBytes(k - 1)
			return k - 1, prev, TruncationBudgetTokens
		}
	}
	full := arrayBytes(len(sizes))
	return len(sizes), full, ""
}

// projectedSizes returns the marshaled size of each result under mode, as it
// will appear inside the results array. Measuring the projected form is the
// point: budget_bytes has to bound what actually goes on the wire, and a
// summary-mode result is roughly a fifth of its full-mode self.
func projectedSizes(results []RecallResult, mode PayloadMode) []int {
	sizes := make([]int, len(results))
	for i := range results {
		raw, err := json.Marshal(ProjectResults(results[i:i+1], mode))
		if err != nil {
			// A result that cannot be marshaled will fail the response
			// marshal too; charge it nothing here and let that surface.
			continue
		}
		sizes[i] = len(raw) - 2 // strip the enclosing [ ]
	}
	return sizes
}

// revisionSizes is projectedSizes for the history surfaces, which serialize
// bare Revisions with no projection.
func revisionSizes(revs []Revision) []int {
	sizes := make([]int, len(revs))
	for i := range revs {
		raw, err := json.Marshal(revs[i])
		if err != nil {
			continue
		}
		sizes[i] = len(raw)
	}
	return sizes
}

// ── Paged read entry points ──────────────────────────────────────────────────

// PageRequest carries the paging and budget half of a read call, separate
// from the query half (RecallInput, or a history namespace/key pair).
//
// Both the MCP tool and its declared HTTP peer build one of these and hand it
// to the same function below. That is deliberate: MCP↔HTTP argument parity
// held only by convention on payload_mode and was found broken in review, so
// these knobs share an implementation rather than a description. Divergence
// now requires editing one function, not forgetting to edit a second file.
type PageRequest struct {
	// Cursor is the opaque resume token from a previous response's
	// next_cursor. Empty starts at the beginning.
	Cursor string
	// Budget bounds the serialized results array. Zero fields mean unbounded.
	Budget Budget
	// PayloadMode is the projection to serialize under. It selects the limit
	// ceiling (see ClampRecallLimit) and determines the byte accounting, but
	// it is not part of the cursor fingerprint — it cannot reorder anything.
	PayloadMode PayloadMode
	// Limit is the caller's requested page size. Zero means unspecified.
	Limit int
}

// Engaged reports whether THE CALLER asked for any bounded-read behavior.
//
// The history surfaces use it to decide between their historical bare-array
// response and the envelope: GET /v1/memory/history and
// GET /v1/knowledge/history are parsed as bare arrays by the shipped web UI,
// whose bundle (internal/webui/dist) is not regenerated here, so an
// unconditional envelope would break it.
//
// Every field it reads must therefore come from the CALL, never from server
// config. That is not obvious and it has already been got wrong once: seeding
// Budget from read.budget_bytes made a deployment-level setting flip both
// history routes to the envelope for every caller, including ones passing no
// knobs at all — a pure shape break, with no rows withheld, that broke the
// shipped UI. The fix is upstream of here: the history surfaces resolve
// PageRequest.Budget from the call only (see resolveHistoryBudget's callers),
// so a configured budget can never make Engaged true on its own.
//
// A caller that passes none of these knobs gets exactly the response it got
// before; one that passes any of them has asked for a bounded read and gets
// the manifest that describes it.
func (p PageRequest) Engaged() bool {
	return p.Cursor != "" || p.Limit > 0 || p.Budget.Set()
}

// PagedRecall is the wire envelope for a paged recall or lookup.
type PagedRecall struct {
	// Results is the projected result array — []RecallResult under full mode,
	// []ProjectedResult otherwise. See ProjectResults.
	Results any `json:"results"`
	// Manifest describes what was withheld and how to get the rest.
	Manifest Manifest `json:"manifest"`
	// Kept is the unprojected form of the same rows. Callers that derive
	// something from the results themselves — tesseract_lookup's facet
	// histogram — read it here rather than reflecting over Results.
	Kept []RecallResult `json:"-"`
}

// PagedRevisions is the wire envelope for a paged revision history.
type PagedRevisions struct {
	Results  []Revision `json:"results"`
	Manifest Manifest   `json:"manifest"`
}

// RecallPaged runs a recall and wraps one page of it in a budget/cursor
// envelope. It is the single implementation behind memory_recall,
// tesseract_lookup, and both of their HTTP peers.
//
// An invalid or mismatched cursor is returned as an error wrapping
// ErrInvalidCursor; surfaces translate that to their own validation error.
// It is never absorbed into an empty page — the whole point of fingerprinting
// a cursor is that resuming the wrong ordering must be loud.
func (s *Store) RecallPaged(ctx context.Context, in RecallInput, pr PageRequest) (PagedRecall, error) {
	// A reranker and a cursor cannot both be honored, so the combination is
	// refused rather than half-supported.
	//
	// applyReranker reorders the window and then, when RerankerTopK is set,
	// cuts it. Both break the invariant this envelope's arithmetic rests on:
	// that the rows DELIVERED are a prefix of the rows CONSUMED. Once they are
	// not, advancing the cursor by results_returned skips rows and repeats
	// others, and the last page reports truncated:false while rows were never
	// delivered at all — the exact lie Truncated exists to prevent. Measured
	// on 10 rows with limit=4, topK=2 and an order-reversing reranker: 8 of 10
	// delivered, 2 twice, 2 never, final truncated:false.
	//
	// Advancing by consumed instead does not rescue it. A budget cut inside a
	// reordered window keeps an arbitrary subset of the consumed range, so no
	// single offset names where to resume. And with RerankerTopK below the
	// page size, WHICH rows a caller ever sees depends on its page size —
	// that is not a paging contract at all.
	//
	// The coherent version of this feature is rerank-then-page: rerank the
	// whole ordered set before windowing. That is a different operation with a
	// different cost profile, and it would change what Recall does today, so
	// it is left as a follow-up rather than smuggled in here.
	//
	// Unpaged reranked recall is untouched: Recall goes through RecallPage,
	// not through here.
	if in.Reranker != "" {
		return PagedRecall{}, fmt.Errorf(
			"%w: reranker %q cannot be combined with cursor paging — a reranker reorders "+
				"(and RerankerTopK truncates) within a page, so a position in the ranked "+
				"ordering does not name a position in what was delivered; use Recall for a "+
				"single reranked result set",
			ErrInvalidInput, in.Reranker)
	}

	fingerprint := RecallOrderingFingerprint(in)

	offset, err := DecodeCursor(pr.Cursor, fingerprint)
	if err != nil {
		return PagedRecall{}, err
	}

	limit, capped := ClampRecallLimit(pr.Limit, pr.PayloadMode)
	in.Limit = limit
	in.Offset = offset

	page, err := s.RecallPage(ctx, in)
	if err != nil {
		return PagedRecall{}, err
	}

	sizes := projectedSizes(page.Results, pr.PayloadMode)
	n, bytes, reason := budgetCut(sizes, pr.Budget)
	kept := page.Results[:n]

	return PagedRecall{
		Results:  ProjectResults(kept, pr.PayloadMode),
		Manifest: buildManifest(page.Total, page.Offset, n, bytes, fingerprint, reason, capped),
		Kept:     kept,
	}, nil
}

// PageRevisions windows an already-fetched revision history and wraps it in
// the same envelope. History is fetched whole because its ordering
// (created_at DESC, revision_id DESC) is fixed by SQL and its depth is
// shallow — 5 revisions is the deepest chain in the reference corpus — so
// paging it in the store would add a query shape for no measured gain. The
// ceiling exists because depth is unbounded by construction, not because it
// is large today.
func PageRevisions(revs []Revision, pr PageRequest, fingerprint string) (PagedRevisions, error) {
	offset, err := DecodeCursor(pr.Cursor, fingerprint)
	if err != nil {
		return PagedRevisions{}, err
	}

	total := len(revs)
	start := offset
	if start > total {
		start = total
	}
	end := total
	if limit := ClampHistoryLimit(pr.Limit); limit > 0 && start+limit < end {
		end = start + limit
	}
	window := revs[start:end]

	sizes := revisionSizes(window)
	n, bytes, reason := budgetCut(sizes, pr.Budget)
	kept := window[:n]

	return PagedRevisions{
		Results:  kept,
		Manifest: buildManifest(total, start, n, bytes, fingerprint, reason, false),
	}, nil
}

// buildManifest assembles the envelope sidecar from one page's numbers.
//
// truncated and next_cursor are two readings of the same fact — "there are
// rows you did not get" — so they are derived from one expression rather than
// set independently. Tests assert the biconditional.
//
// Reason precedence is binding-constraint-first: a budget that cut inside the
// window is more actionable than the limit that sized the window, and a
// payload_mode cap is more actionable than the limit the caller actually
// passed, because it names a knob whose value they did not choose.
func buildManifest(total, offset, returned, bytes int, fingerprint, budgetReason string, limitCapped bool) Manifest {
	hasMore := offset+returned < total
	m := Manifest{
		ResultsTotal:    total,
		ResultsReturned: returned,
		BytesReturned:   bytes,
		TokensEstimate:  EstimateTokens(bytes),
		Truncated:       hasMore,
	}
	if hasMore {
		switch {
		case budgetReason != "":
			m.TruncationReason = budgetReason
		case limitCapped:
			m.TruncationReason = TruncationPayloadModeLimitCap
		default:
			m.TruncationReason = TruncationLimit
		}
		next := EncodeCursor(offset+returned, fingerprint)
		m.NextCursor = &next
	}
	return m
}
