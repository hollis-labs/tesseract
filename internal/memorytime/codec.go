// Package memorytime owns the sortable timestamp encoding used by the memory
// subsystem's SQLite tables.
package memorytime

import "time"

// Layout is RFC3339 in UTC with an always-present, fixed-width nanosecond
// fraction. Values produced by Format sort lexicographically in chronological
// order, which lets SQLite TEXT indexes serve ORDER BY and range predicates.
const Layout = "2006-01-02T15:04:05.000000000Z07:00"

// Format returns the canonical fixed-width UTC representation of t.
func Format(t time.Time) string {
	return t.UTC().Format(Layout)
}

// Parse accepts both the canonical encoding and the historical formats used
// before it: variable-width RFC3339Nano and SQLite's UTC time.DateTime default.
func Parse(raw string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, raw)
	if err == nil {
		return parsed, nil
	}
	return time.Parse(time.DateTime, raw)
}
