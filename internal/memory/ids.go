package memory

import (
	"crypto/rand"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"
)

var (
	entropy     = ulid.Monotonic(rand.Reader, 0)
	entropyLock sync.Mutex
)

// NewULID returns a new lexicographically sortable ULID as a string.
// Uses a monotonic entropy source to guarantee ordering within the
// same millisecond.
func NewULID() string {
	entropyLock.Lock()
	defer entropyLock.Unlock()
	return ulid.MustNew(ulid.Timestamp(time.Now()), entropy).String()
}

// mintedAtFormat is RFC 3339 in UTC at the millisecond a ULID carries. It is
// fixed-width rather than time.RFC3339Nano, which drops trailing zeros and so
// gives two values of the same field different lengths.
const mintedAtFormat = "2006-01-02T15:04:05.000Z"

// MintTime decodes the timestamp a ULID carries in its first ten characters:
// milliseconds since the Unix epoch, which is when the ID was minted. It reports
// false for anything that is not a well-formed ULID, so a caller can tell "not
// an ID of the kind Tesseract mints" apart from "an old one".
//
// This is a fact about the string, not about the store. A well-formed ULID says
// when it would have been minted; whether Tesseract minted it is what the lookup
// that just failed already answered.
func MintTime(id string) (time.Time, bool) {
	parsed, err := ulid.ParseStrict(id)
	if err != nil {
		return time.Time{}, false
	}
	return ulid.Time(parsed.Time()).UTC(), true
}

// NotFoundDetails is the structured half of a not_found error about one
// specific ID: which selector it was, the ID, when a ULID with that prefix would
// have been minted, and how long before now that was.
//
// It exists because an agent sometimes reports an ID that was never minted — a
// write that never happened, cited as if it had — and "not found" alone gives a
// verifier nothing to tell an invented ID from a real one that was deleted or
// mistyped. The decoded time does: an ID that would have been minted a day and a
// half before the claim is not the result of that claim, and one minted seconds
// ago that still does not resolve is a real problem.
//
// It states the facts and draws no conclusion; Tesseract does not reason over
// what it stores. age_seconds is negative when the ID's timestamp is ahead of
// now, which is itself evidence: either the ID was not minted here or a clock is
// wrong.
//
// It returns nil for an ID that is not a well-formed ULID, so a caller can hand
// the result straight to an error envelope and the error is then exactly what it
// was before this existed. now is a parameter so a caller supplies the clock it
// wants and a test can fix it.
func NotFoundDetails(field, id string, now time.Time) map[string]any {
	minted, ok := MintTime(id)
	if !ok {
		return nil
	}
	return map[string]any{
		"field":       field,
		"id":          id,
		"minted_at":   minted.Format(mintedAtFormat),
		"age_seconds": int64(now.Sub(minted).Seconds()),
	}
}
