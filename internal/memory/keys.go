package memory

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

const (
	maxKeySegments  = 6
	maxSegmentChars = 64
	maxKeyChars     = 256
)

var (
	// segmentRE validates a single segment: lowercase alphanumeric + underscore.
	segmentRE = regexp.MustCompile(`^[a-z0-9_]+$`)

	// reservedPrefixes matches the top-level segments the spec reserves for
	// conventional use (D8). Enforcement is advisory at 1.0 — we only report
	// membership, we do not reject non-reserved keys.
	reservedPrefixes = map[string]struct{}{
		"user":    {},
		"project": {},
		"session": {},
		"contact": {},
		"agent":   {},
	}
)

// keyPolicySummary states the whole rule in one clause, so a rejection can
// teach the shape instead of only naming the character that broke it.
const keyPolicySummary = "memory keys are dot-separated lowercase segments of a-z, 0-9 and _ " +
	"(max 6 segments, 64 chars per segment, 256 chars total)"

// ErrInvalidKey is returned when a memory key fails validation.
var ErrInvalidKey = errors.New("invalid memory key")

// Memory keys are strict on purpose, and the strictness is not cosmetic.
// CW-20260514-0022 asked whether a memory write of "user-preferences" should
// be silently normalized to "user_preferences" instead of rejected. The answer
// is no, and the reasons are structural rather than stylistic:
//
//  1. The key space is shared with the knowledge domain, and uniqueness is
//     domain-agnostic. memory_state is UNIQUE(namespace, memory_key), while
//     ValidateKey is applied at exactly one place (write.go) and only for
//     domains.Memory — knowledge writes bypass key validation entirely,
//     because knowledge keys carry slugs from external sources. A namespace
//     can therefore already hold "foo-bar" and "foo_bar" as two distinct
//     knowledge rows. Folding a memory write of "foo-bar" onto "foo_bar"
//     would silently land it on whichever row happens to occupy that slot —
//     a collision the caller never asked for and cannot see.
//
//  2. Retrieval cannot tell the two spellings apart anyway. memory_key is
//     FTS5-indexed at weight 10.0 (bm25.go), and everything outside
//     [A-Za-z0-9_] is a separator on both sides of that index, so "foo-bar"
//     and "foo bar" collapse to the same two tokens before FTS5 sees either.
//     Accepting hyphens would create keys that are distinct in storage and
//     identical in search.
//
// So the rule stays a rejection. What the rejection owes the caller is a
// diagnosis rather than a refusal: the error states the rule and names the
// valid spelling of what was passed (SuggestKey). It suggests; it never
// applies. Reads are held to the same story — see explainMemoryKeyMiss in
// read.go — so the same mistake gets one diagnosis rather than two.

// ValidateKey checks a dot-notation memory key against the rules in D8/D9:
//   - regex ^[a-z0-9_]+(\.[a-z0-9_]+)*$
//   - max 6 segments
//   - max 64 chars per segment
//   - max 256 chars total
//
// Returns a wrapped ErrInvalidKey on failure, carrying the specific reason,
// the rule, and — when one exists — the valid form of the key passed.
func ValidateKey(key string) error {
	if reason := keyViolation(key); reason != "" {
		return newInvalidKeyError(key, reason)
	}
	return nil
}

// keyViolation returns the first rule key breaks, phrased as a reason, or ""
// when the key is valid. Split out from ValidateKey so SuggestKey can check a
// candidate without recursing back through error construction.
func keyViolation(key string) string {
	if key == "" {
		return "empty key"
	}
	if len(key) > maxKeyChars {
		return fmt.Sprintf("total length %d exceeds max %d", len(key), maxKeyChars)
	}
	segments := strings.Split(key, ".")
	if len(segments) > maxKeySegments {
		return fmt.Sprintf("%d segments exceeds max %d", len(segments), maxKeySegments)
	}
	for i, seg := range segments {
		if seg == "" {
			return fmt.Sprintf("empty segment at position %d", i)
		}
		if len(seg) > maxSegmentChars {
			return fmt.Sprintf("segment %d length %d exceeds max %d", i, len(seg), maxSegmentChars)
		}
		if !segmentRE.MatchString(seg) {
			return fmt.Sprintf("segment %q contains invalid characters (allowed: a-z 0-9 _)", seg)
		}
	}
	return ""
}

// newInvalidKeyError assembles the rejection: what broke, what the rule is,
// and what the caller probably meant.
func newInvalidKeyError(key, reason string) error {
	msg := reason + "; " + keyPolicySummary
	if suggestion, ok := SuggestKey(key); ok {
		msg += fmt.Sprintf("; did you mean %q?", suggestion)
	}
	return fmt.Errorf("%w: %s", ErrInvalidKey, msg)
}

// SuggestKey returns the valid memory key closest to the one passed, and
// whether a suggestion could be made at all.
//
// It is advisory only. Nothing in the write path calls it to rewrite a key —
// see the note above ValidateKey for why normalization is not applied
// silently. Its only job is to turn "that key is invalid" into "that key is
// invalid; you probably meant this one".
//
// The mapping is deliberately narrow. Characters people actually type in place
// of "_" (space, "-", "/", ":", "+", "@") fold to "_", letters fold to
// lowercase, anything else outside the charset is dropped, runs of "_"
// collapse, and empty segments disappear. If the result is empty, unchanged,
// or still invalid (too many segments, still too long), no suggestion is
// offered — a wrong suggestion teaches worse than none.
func SuggestKey(key string) (string, bool) {
	var b strings.Builder
	for _, r := range strings.ToLower(key) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_', r == '.':
			b.WriteRune(r)
		case r == '-', r == ' ', r == '\t', r == '/', r == ':', r == '+', r == '@':
			b.WriteRune('_')
		default:
			// Dropped: no plausible ASCII stand-in (punctuation, accented
			// letters, emoji). Dropping keeps the suggestion readable rather
			// than filling it with placeholder underscores.
		}
	}

	segments := strings.Split(b.String(), ".")
	out := make([]string, 0, len(segments))
	for _, seg := range segments {
		if seg = collapseUnderscores(seg); seg != "" {
			out = append(out, seg)
		}
	}
	suggestion := strings.Join(out, ".")

	if suggestion == "" || suggestion == key || keyViolation(suggestion) != "" {
		return "", false
	}
	return suggestion, true
}

// collapseUnderscores squeezes runs of "_" into one and trims them from both
// ends of a segment, so "user - preferences" suggests "user_preferences"
// rather than "user___preferences".
func collapseUnderscores(seg string) string {
	var b strings.Builder
	prevUnderscore := false
	for _, r := range seg {
		if r == '_' {
			if prevUnderscore {
				continue
			}
			prevUnderscore = true
		} else {
			prevUnderscore = false
		}
		b.WriteRune(r)
	}
	return strings.Trim(b.String(), "_")
}

// IsReservedPrefix reports whether the first segment of key is one of the
// reserved top-level prefixes from D8. Advisory only — not enforced.
func IsReservedPrefix(key string) bool {
	if key == "" {
		return false
	}
	first := key
	if idx := strings.Index(key, "."); idx >= 0 {
		first = key[:idx]
	}
	_, ok := reservedPrefixes[first]
	return ok
}
