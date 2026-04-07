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

// ErrInvalidKey is returned when a memory key fails validation.
var ErrInvalidKey = errors.New("invalid memory key")

// ValidateKey checks a dot-notation memory key against the rules in D8/D9:
//   - regex ^[a-z0-9_]+(\.[a-z0-9_]+)*$
//   - max 6 segments
//   - max 64 chars per segment
//   - max 256 chars total
//
// Returns a wrapped ErrInvalidKey on failure with a specific reason.
func ValidateKey(key string) error {
	if key == "" {
		return fmt.Errorf("%w: empty key", ErrInvalidKey)
	}
	if len(key) > maxKeyChars {
		return fmt.Errorf("%w: total length %d exceeds max %d", ErrInvalidKey, len(key), maxKeyChars)
	}
	segments := strings.Split(key, ".")
	if len(segments) > maxKeySegments {
		return fmt.Errorf("%w: %d segments exceeds max %d", ErrInvalidKey, len(segments), maxKeySegments)
	}
	for i, seg := range segments {
		if seg == "" {
			return fmt.Errorf("%w: empty segment at position %d", ErrInvalidKey, i)
		}
		if len(seg) > maxSegmentChars {
			return fmt.Errorf("%w: segment %d length %d exceeds max %d", ErrInvalidKey, i, len(seg), maxSegmentChars)
		}
		if !segmentRE.MatchString(seg) {
			return fmt.Errorf("%w: segment %q contains invalid characters (allowed: a-z 0-9 _)", ErrInvalidKey, seg)
		}
	}
	return nil
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
