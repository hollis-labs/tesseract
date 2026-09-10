package memory

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/hollis-labs/tesseract/domains"
)

// The event log's linear read path (CW-20260909-0035).
//
// # Why this is not ranking=chronological
//
// Recall can already sort by time, and reaching for that instead of this was
// the obvious shortcut. It is the wrong shape for a log, for one reason that
// is about cost and one that is about correctness.
//
// Cost: fetchCandidates issues no ORDER BY and no LIMIT. It loads EVERY row
// matching the filters into Go, scores the lot, sorts it, and then windows the
// result — see recallOrdered. On a curated corpus of ~2,000 revisions that is
// invisible. An event log is projected at 10-100x that and grows without a
// natural bound, so paging it through recall reads the whole log into memory
// once per page. The read below pushes the order and the window into SQL, so
// one page costs one page.
//
// Correctness: recall's cursor is an OFFSET into a re-derived ordering, and
// its own documentation says offsets "are positions in a re-derived ordering,
// not stable keys". A log is appended to at the head. Under newest-first
// paging, every write between page 1 and page 2 shifts every offset by one and
// the reader silently sees a row twice. A keyset cursor names a POSITION —
// the (created_at, revision_id) it stopped at — so concurrent appends are
// invisible to a read already in progress, which is the one property a log
// reader actually needs.
//
// What this deliberately does not do is rank. There is no score, no status
// weighting, no activation: the order is the order things happened. Retrieval
// selects, it never merges or infers, and a log's selection is a range.

// LogDirection is the reading direction of a linear log read.
type LogDirection string

const (
	// LogNewestFirst reads backward from the head — the default, because the
	// question a log is usually asked is "what just happened".
	LogNewestFirst LogDirection = "newest_first"

	// LogOldestFirst reads forward from the tail. This is the replay
	// direction: reconstructing what an agent was thinking, in the order it
	// thought it, which is the whole reason an Event carries prose.
	LogOldestFirst LogDirection = "oldest_first"
)

// Valid reports whether d is one of the two canonical directions.
func (d LogDirection) Valid() bool {
	return d == LogNewestFirst || d == LogOldestFirst
}

const (
	// DefaultEventLogLimit is the page size when the caller does not pass one.
	DefaultEventLogLimit = 50

	// MaxEventLogLimit is the ceiling on one page.
	MaxEventLogLimit = 500
)

// EventLogInput selects one window of the event log.
//
// There is no ranking knob and no status knob, and both absences are the
// design rather than a first cut.
//
// Ranking: the order is chronological or it is not a log. A log that can be
// re-sorted by relevance is recall, and recall is one door over.
//
// Status: deprecated revisions are excluded, always. `tesseract_deprecate` is
// how a log entry is retracted — "soft-remove one revision; history keeps it"
// — and a retraction that still appears in the linear read has not retracted
// anything. The entry is not gone: tesseract_history still returns it, and a
// recall naming statuses explicitly still finds it. This composes with
// supersede for free: superseding a keyed event deprecates the old revision in
// the same transaction, so the log shows exactly one row per logical entry
// without needing a join through memory_state.current_revision.
type EventLogInput struct {
	// Namespaces selects which logs to read, exact or by prefix — the same
	// vocabulary recall accepts, so `user/chrispian/event` reads every stream
	// and `user/chrispian/event/journal` reads one. Required: a log read with
	// no namespace is a request for every event ever written by anyone, which
	// is never the question being asked.
	Namespaces []string

	// Since and Until bound created_at, inclusive at both ends. They are the
	// reason time is not in the namespace grammar: a range over an indexed
	// column answers "what did I write in September" without turning it into a
	// multi-namespace query.
	Since *time.Time
	Until *time.Time

	// Direction defaults to LogNewestFirst.
	Direction LogDirection

	// Limit is the page size. Zero takes DefaultEventLogLimit; anything above
	// MaxEventLogLimit is clamped down to it.
	Limit int

	// Cursor resumes a previous page. It carries the position the last page
	// stopped at and a fingerprint of the selection it was issued for, so
	// resuming a cursor against a different namespace set, direction or time
	// window is an error rather than a plausible wrong page.
	Cursor string
}

// EventLogManifest describes the page without describing the corpus.
//
// It carries no total, and that omission is load-bearing. Counting the rows a
// selection matches means scanning the whole partition on every page — the
// exact work keyset paging exists to avoid — and on an append-only log the
// answer would be stale before the caller read it. HasMore answers the
// question a total is usually a proxy for, and costs one extra row.
type EventLogManifest struct {
	Returned   int          `json:"returned"`
	Limit      int          `json:"limit"`
	Direction  LogDirection `json:"direction"`
	HasMore    bool         `json:"has_more"`
	NextCursor *string      `json:"next_cursor"`
}

// EventLogPage is one window of the log plus its manifest.
type EventLogPage struct {
	Entries  []Revision       `json:"entries"`
	Manifest EventLogManifest `json:"manifest"`
}

// EventLogResponse is the wire shape both read surfaces serve: a projected
// page under a declared payload mode.
//
// PayloadMode rides on the manifest rather than on every entry, which is where
// ProjectedResult carries it for recall. The contract it exists to state is the
// same one — an absent payload.body means WITHHELD, never empty — and a log
// page is homogeneous by construction, so saying it once per page says exactly
// as much as saying it per row.
type EventLogResponse struct {
	Entries  any                      `json:"entries"`
	Manifest EventLogResponseManifest `json:"manifest"`
}

// EventLogResponseManifest is EventLogManifest plus the projection the entries
// were rendered under.
type EventLogResponseManifest struct {
	EventLogManifest
	PayloadMode PayloadMode `json:"payload_mode"`
}

// ProjectEventLogPage renders a page for serialization under mode.
//
// It lives here rather than in each surface so the MCP tool and the HTTP route
// cannot drift on the envelope. Both call this and marshal the result.
func ProjectEventLogPage(page EventLogPage, mode PayloadMode) EventLogResponse {
	if !mode.Valid() {
		mode = DefaultPayloadMode
	}
	return EventLogResponse{
		Entries: ProjectRevisions(page.Entries, mode),
		Manifest: EventLogResponseManifest{
			EventLogManifest: page.Manifest,
			PayloadMode:      mode,
		},
	}
}

// ReadEventLog returns one window of the event log in chronological order.
//
// It is scoped to domains.Event rather than parameterized. The shape would
// generalize — nothing in the SQL below is specific to Event beyond the domain
// bind — and it is not generalized because no other domain wants it: memory
// and knowledge are curated corpora read by relevance, and a log read over
// them would be a second door onto the same rows with no question behind it.
func (s *Store) ReadEventLog(ctx context.Context, in EventLogInput) (EventLogPage, error) {
	if len(in.Namespaces) == 0 {
		return EventLogPage{}, fmt.Errorf("%w: at least one namespace is required", ErrInvalidInput)
	}
	for _, ns := range in.Namespaces {
		if strings.TrimSpace(ns) == "" {
			return EventLogPage{}, fmt.Errorf("%w: namespace entries must be non-empty", ErrInvalidInput)
		}
	}
	if in.Direction == "" {
		in.Direction = LogNewestFirst
	}
	if !in.Direction.Valid() {
		return EventLogPage{}, fmt.Errorf("%w: direction must be %s or %s, got %q",
			ErrInvalidInput, LogNewestFirst, LogOldestFirst, in.Direction)
	}
	if in.Since != nil && in.Until != nil && in.Until.Before(*in.Since) {
		return EventLogPage{}, fmt.Errorf("%w: until (%s) is before since (%s), which selects nothing",
			ErrInvalidInput, in.Until.UTC().Format(time.RFC3339), in.Since.UTC().Format(time.RFC3339))
	}
	limit := in.Limit
	if limit <= 0 {
		limit = DefaultEventLogLimit
	}
	if limit > MaxEventLogLimit {
		limit = MaxEventLogLimit
	}

	fingerprint := eventLogOrderingFingerprint(in)
	pos, err := decodeLogCursor(in.Cursor, fingerprint)
	if err != nil {
		return EventLogPage{}, err
	}

	where := []string{"r.domain = ?"}
	args := []any{string(domains.Event)}

	nsFrag, nsArgs := buildNamespaceClause(in.Namespaces)
	where = append(where, nsFrag)
	args = append(args, nsArgs...)

	// See EventLogInput: retraction is what deprecation means for a log entry.
	where = append(where, "r.status != ?")
	args = append(args, string(StatusDeprecated))

	// TTL is honored the same way recall honors it. An event with an expiry is
	// unusual — long retention is the domain's whole point — but a row that has
	// expired must not surface anywhere, and a read path that skipped this
	// check would be the one door that leaked it.
	where = append(where, "(r.expires_at IS NULL OR r.expires_at > ?)")
	args = append(args, time.Now().UTC().Format(memoryTimeFormat))

	if in.Since != nil {
		where = append(where, "r.created_at >= ?")
		args = append(args, in.Since.UTC().Format(memoryTimeFormat))
	}
	if in.Until != nil {
		where = append(where, "r.created_at <= ?")
		args = append(args, in.Until.UTC().Format(memoryTimeFormat))
	}

	// The keyset predicate. Timestamps are fixed-width lexically-chronological
	// TEXT (internal/memorytime), so the string comparison below IS the
	// chronological one — that property is asserted in
	// TestFormatIsFixedWidthUTCAndLexicallyChronological and this read depends
	// on it. revision_id breaks ties inside one nanosecond, matching the
	// ORDER BY exactly; a keyset predicate that disagreed with its own sort
	// order is how a page boundary skips or repeats a row.
	//
	// Written out rather than as an SQLite row-value comparison so it is
	// obvious which column leads and so the two directions read as mirrors.
	cmp := "<"
	order := "DESC"
	if in.Direction == LogOldestFirst {
		cmp = ">"
		order = "ASC"
	}
	if pos != nil {
		where = append(where, fmt.Sprintf(
			"(r.created_at %s ? OR (r.created_at = ? AND r.revision_id %s ?))", cmp, cmp))
		args = append(args, pos.CreatedAt, pos.CreatedAt, pos.RevisionID)
	}

	// limit+1 is the has-more probe: one row past the page tells us whether a
	// next page exists without a COUNT over the partition. The extra row is
	// dropped before the page is returned.
	// Concatenated rather than parameterized, and every interpolated piece is
	// a Go constant or a value from a closed set: recallRevisionColumns is a
	// package const, `where` holds fragments built above with `?` placeholders
	// for every caller value, and `order` is one of two literals chosen by the
	// validated Direction. No caller string reaches the statement text — the
	// namespaces, timestamps, cursor position and limit all arrive as binds.
	//nolint:gosec // G202: fragments are package constants and bound placeholders, never caller data
	query := `SELECT ` + recallRevisionColumns + `
FROM memory_revisions r
WHERE ` + strings.Join(where, " AND ") + `
ORDER BY r.created_at ` + order + `, r.revision_id ` + order + `
LIMIT ?`
	args = append(args, limit+1)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return EventLogPage{}, fmt.Errorf("read event log: %w", err)
	}
	defer func() { _ = rows.Close() }()

	entries := make([]Revision, 0, limit)
	hasMore := false
	for rows.Next() {
		if len(entries) == limit {
			hasMore = true
			break
		}
		rev, scanErr := scanRevision(rows)
		if scanErr != nil {
			return EventLogPage{}, scanErr
		}
		entries = append(entries, rev)
	}
	if err := rows.Err(); err != nil {
		return EventLogPage{}, err
	}

	page := EventLogPage{
		Entries: entries,
		Manifest: EventLogManifest{
			Returned:  len(entries),
			Limit:     limit,
			Direction: in.Direction,
			HasMore:   hasMore,
		},
	}
	if hasMore && len(entries) > 0 {
		last := entries[len(entries)-1]
		next := encodeLogCursor(logPosition{
			CreatedAt:  last.CreatedAt.UTC().Format(memoryTimeFormat),
			RevisionID: last.RevisionID,
		}, fingerprint)
		page.Manifest.NextCursor = &next
	}
	return page, nil
}

// ── The keyset cursor ────────────────────────────────────────────────────────

// logCursorVersion is bumped whenever the encoded shape changes. A cursor from
// a different version is rejected rather than reinterpreted.
const logCursorVersion = 1

// logPosition is the place in the ordering a page stopped at: the sort key of
// its last row, which is exactly what the next page's predicate excludes.
type logPosition struct {
	CreatedAt  string `json:"t"`
	RevisionID string `json:"r"`
}

// logCursorPayload is the decoded interior of a log cursor. Callers never see
// it — the wire form is opaque base64url and must be treated as such.
//
// It is a separate type from cursorPayload rather than a field added to it,
// and the two encoders sit side by side on purpose. They carry genuinely
// different things: an offset is a COUNT of rows consumed, a position is a KEY
// in the ordering, and a decoder that accepted either would have to guess
// which one it was handed. Sharing the base64url-plus-fingerprint discipline
// is what makes them recognizable as the same kind of token; sharing the
// payload would make them confusable, which is the opposite goal.
type logCursorPayload struct {
	V int         `json:"v"`
	P logPosition `json:"p"`
	F string      `json:"f"`
}

// encodeLogCursor produces the opaque resume token for a position under an
// ordering fingerprint.
//
// Unexported, unlike EncodeCursor. The recall cursor is exported because
// PageRevisions and HistoryOrderingFingerprint are part of the read surface the
// adapters assemble pages with; the log cursor is issued and consumed entirely
// inside ReadEventLog, and the surfaces only ever pass the opaque string
// through. Exporting it would advertise a knob nothing turns and put
// logPosition — a type whose whole contract is that callers do not look at it —
// into the package's public API.
func encodeLogCursor(pos logPosition, fingerprint string) string {
	raw, err := json.Marshal(logCursorPayload{V: logCursorVersion, P: pos, F: fingerprint})
	if err != nil {
		// Three strings and an int; Marshal cannot fail. An empty token reads
		// as "no next page", which under-promises rather than mislead.
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}

// decodeLogCursor validates raw against fingerprint and returns the position it
// names, or nil for an empty cursor (the first page).
//
// The fingerprint check is why a cursor is not just a timestamp. A position is
// a place in ONE ordering; resuming it into a different one — the other
// direction, a different namespace set, a moved time window — yields rows that
// look plausible and are wrong. Changing any of those makes the cursor an
// error rather than a silent misread.
//
// Limit is deliberately NOT in the fingerprint, matching DecodeCursor's rule: it sets
// where pages break, not what the sequence is, so paging the same log with a
// smaller page size must keep working.
func decodeLogCursor(raw, fingerprint string) (*logPosition, error) {
	if raw == "" {
		return nil, nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: not a valid cursor token", ErrInvalidCursor)
	}
	var p logCursorPayload
	if err := json.Unmarshal(decoded, &p); err != nil {
		return nil, fmt.Errorf("%w: not a valid cursor token", ErrInvalidCursor)
	}
	if p.V != logCursorVersion {
		return nil, fmt.Errorf("%w: cursor version %d is not supported", ErrInvalidCursor, p.V)
	}
	if p.P.CreatedAt == "" || p.P.RevisionID == "" {
		return nil, fmt.Errorf("%w: cursor carries an incomplete position", ErrInvalidCursor)
	}
	if p.F != fingerprint {
		return nil, fmt.Errorf("%w: cursor was issued for a different log read — "+
			"namespaces, direction, since or until changed since it was issued; "+
			"restart paging without a cursor", ErrInvalidCursor)
	}
	return &p.P, nil
}

// eventLogOrderingFingerprint derives the ordering fingerprint for in.
//
// Direction is in the key because reversing it reverses the sequence, and a
// position is only meaningful relative to which way the read is walking.
// Namespaces are sorted because a set of namespaces is a set: asking for [a,b]
// and [b,a] selects the same rows in the same order.
func eventLogOrderingFingerprint(in EventLogInput) string {
	direction := in.Direction
	if direction == "" {
		direction = LogNewestFirst
	}
	return fingerprintOf(struct {
		Namespaces []string `json:"ns"`
		Direction  string   `json:"dir"`
		Since      string   `json:"since"`
		Until      string   `json:"until"`
	}{
		Namespaces: sortedCopy(in.Namespaces),
		Direction:  string(direction),
		Since:      formatTimePtr(in.Since),
		Until:      formatTimePtr(in.Until),
	})
}
