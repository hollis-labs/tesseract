package memory

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// MaxEventWorkstreamIDs bounds one event-list association filter.
const MaxEventWorkstreamIDs = 500

// ParseEventWorkstreamSelectors accepts HTTP's repeated scalar values or a
// JSON-array value. Empty lists and malformed elements never widen a read.
func ParseEventWorkstreamSelectors(values []string) ([]string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	var out []string
	for _, value := range values {
		if strings.HasPrefix(strings.TrimSpace(value), "[") {
			var ids []string
			if err := json.Unmarshal([]byte(value), &ids); err != nil {
				return nil, fmt.Errorf("%w: workstream_id must be a string or array of strings", ErrInvalidInput)
			}
			if len(ids) == 0 {
				return nil, fmt.Errorf("%w: workstream_id list must not be empty", ErrInvalidInput)
			}
			out = append(out, ids...)
		} else {
			out = append(out, value)
		}
	}
	return canonicalEventWorkstreams("", out)
}

func canonicalEventWorkstreams(scalar string, ids []string) ([]string, error) {
	if ids != nil && len(ids) == 0 {
		return nil, fmt.Errorf("%w: workstream_id list must not be empty", ErrInvalidInput)
	}
	if len(ids) > MaxEventWorkstreamIDs {
		return nil, fmt.Errorf("%w: workstream_id accepts at most %d entries", ErrInvalidInput, MaxEventWorkstreamIDs)
	}
	out := append([]string(nil), ids...)
	if scalar != "" {
		out = append(out, scalar)
	}
	for _, id := range out {
		if err := ValidateWorkstreamFilter(id); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrInvalidInput, err)
		}
	}
	sort.Strings(out)
	unique := out[:0]
	for _, id := range out {
		if len(unique) == 0 || unique[len(unique)-1] != id {
			unique = append(unique, id)
		}
	}
	if len(unique) > MaxEventWorkstreamIDs {
		return nil, fmt.Errorf("%w: workstream_id accepts at most %d entries", ErrInvalidInput, MaxEventWorkstreamIDs)
	}
	return unique, nil
}

// ApplyEventLogSince accepts an inclusive RFC3339 lower bound or an opaque
// log cursor. Cursor-as-since restores the original lower bound carried in new
// cursors, so switching from a time-bound first page does not lose that filter.
// All other selection fields still have to match the cursor fingerprint.
func ApplyEventLogSince(in *EventLogInput, raw string) error {
	if raw == "" {
		return nil
	}
	if stamp, err := time.Parse(time.RFC3339, raw); err == nil {
		in.Since = &stamp
		return nil
	}
	if in.Cursor != "" {
		return fmt.Errorf("%w: since as cursor cannot be combined with cursor", ErrInvalidInput)
	}
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return fmt.Errorf("%w: since must be RFC3339 or an event cursor", ErrInvalidInput)
	}
	var token logCursorPayload
	if err := json.Unmarshal(decoded, &token); err != nil || token.V != logCursorVersion || token.P.CreatedAt == "" || token.P.RevisionID == "" {
		return fmt.Errorf("%w: since must be RFC3339 or an event cursor", ErrInvalidInput)
	}
	if token.Since != "" {
		stamp, parseErr := time.Parse(time.RFC3339Nano, token.Since)
		if parseErr != nil {
			return fmt.Errorf("%w: cursor carries an invalid since bound", ErrInvalidCursor)
		}
		in.Since = &stamp
	}
	in.Cursor = raw
	return nil
}

// NormalizeEventWorkstreamIDs validates and canonicalizes a native ID list.
func NormalizeEventWorkstreamIDs(ids []string) ([]string, error) {
	if ids == nil {
		return nil, fmt.Errorf("%w: workstream_id list must not be empty", ErrInvalidInput)
	}
	return canonicalEventWorkstreams("", ids)
}
