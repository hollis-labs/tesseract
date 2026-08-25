// Package sqlitedsn centralizes SQLite connection-string construction.
//
// Tesseract uses modernc.org/sqlite, which registers itself as "sqlite".
// That driver configures per-connection pragmas through repeated
// _pragma=name(value) query parameters. It does not understand the
// mattn/go-sqlite3 spelling (_busy_timeout=, _fk=), and it does not reject
// parameters it doesn't recognize — it ignores them silently. A DSN written
// in the wrong dialect therefore looks correct, opens without error, and
// applies nothing.
//
// Every DSN in this repo is built here so that spelling is stated once.
package sqlitedsn

import (
	"strconv"
	"strings"
)

// BusyTimeoutMS is how long a connection waits for a competing writer before
// returning SQLITE_BUSY. SQLite permits exactly one writer at a time; WAL adds
// concurrent readers, not concurrent writers. With no timeout the loser of a
// write race fails immediately instead of waiting.
const BusyTimeoutMS = 5000

// DSN builds a modernc.org/sqlite connection string for the database at path.
//
// It always applies busy_timeout and foreign_keys. Foreign key enforcement is
// off by default in SQLite and is scoped to a single connection, so it must be
// set on every connection or the declared REFERENCES clauses are inert.
//
// extraPragmas are appended verbatim as additional _pragma= values and must
// already be in name(value) form, e.g. "journal_mode(WAL)".
func DSN(path string, extraPragmas ...string) string {
	var b strings.Builder
	b.WriteString("file:")
	b.WriteString(path)
	b.WriteString("?_pragma=busy_timeout(")
	b.WriteString(strconv.Itoa(BusyTimeoutMS))
	b.WriteString(")&_pragma=foreign_keys(1)")
	for _, p := range extraPragmas {
		b.WriteString("&_pragma=")
		b.WriteString(p)
	}
	return b.String()
}
