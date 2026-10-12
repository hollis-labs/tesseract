package sqlitedsn

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func TestDSN(t *testing.T) {
	const base = "?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
	tests := []struct {
		name   string
		path   string
		extras []string
		want   string
	}{
		{"absolute path", "/var/data/t.db", nil, "file:/var/data/t.db" + base},
		{"relative path", "data/t.db", nil, "file:data/t.db" + base},
		{"empty path", "", nil, "file:" + base},
		{"spaces kept verbatim", "/tmp/my dir/t.db", nil, "file:/tmp/my dir/t.db" + base},
		{"special characters kept verbatim", "/tmp/a&b#c.db", nil, "file:/tmp/a&b#c.db" + base},
		{"one extra pragma", "t.db", []string{"journal_mode(WAL)"}, "file:t.db" + base + "&_pragma=journal_mode(WAL)"},
		{"extra pragmas keep order", "t.db", []string{"journal_mode(WAL)", "synchronous(NORMAL)"},
			"file:t.db" + base + "&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)"},
		{"empty extras slice", "t.db", []string{}, "file:t.db" + base},
		{"already formatted DSN is wrapped, not detected", "file:t.db?mode=ro", nil, "file:file:t.db?mode=ro" + base},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DSN(tt.path, tt.extras...); got != tt.want {
				t.Errorf("DSN(%q, %q) = %q, want %q", tt.path, tt.extras, got, tt.want)
			}
		})
	}
}

func TestBusyTimeoutMS(t *testing.T) {
	if BusyTimeoutMS != 5000 {
		t.Errorf("BusyTimeoutMS = %d, want 5000", BusyTimeoutMS)
	}
}

// TestDSNAppliesPragmas opens a throwaway database under t.TempDir() and
// checks the driver really applies what the DSN declares.
func TestDSNAppliesPragmas(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	path := filepath.Join(t.TempDir(), "dsn test.db")
	db, err := sql.Open("sqlite", DSN(path, "journal_mode(WAL)"))
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	})
	db.SetMaxOpenConns(1)

	tests := []struct {
		pragma string
		want   string
	}{
		{"busy_timeout", "5000"},
		{"foreign_keys", "1"},
		{"journal_mode", "wal"},
	}
	for _, tt := range tests {
		var got string
		if err := db.QueryRow("PRAGMA " + tt.pragma).Scan(&got); err != nil {
			t.Fatalf("PRAGMA %s: %v", tt.pragma, err)
		}
		if got != tt.want {
			t.Errorf("PRAGMA %s = %q, want %q", tt.pragma, got, tt.want)
		}
	}
}
