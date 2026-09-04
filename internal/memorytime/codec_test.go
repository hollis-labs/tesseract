package memorytime_test

import (
	"testing"
	"time"

	"github.com/hollis-labs/tesseract/internal/memorytime"
)

func TestFormatIsFixedWidthUTCAndLexicallyChronological(t *testing.T) {
	shortPrefix := time.Date(2026, 9, 4, 13, 34, 55, 92_340_000, time.FixedZone("offset", -5*60*60))
	shortPrefix = shortPrefix.UTC()
	laterPrefix := shortPrefix.Add(2 * time.Microsecond)

	gotShort := memorytime.Format(shortPrefix)
	gotLater := memorytime.Format(laterPrefix)
	if gotShort != "2026-09-04T18:34:55.092340000Z" {
		t.Fatalf("Format(short) = %q", gotShort)
	}
	if gotLater != "2026-09-04T18:34:55.092342000Z" {
		t.Fatalf("Format(later) = %q", gotLater)
	}
	if len(gotShort) != len(gotLater) || gotShort >= gotLater {
		t.Fatalf("canonical timestamps are not fixed-width chronological TEXT: %q, %q", gotShort, gotLater)
	}
}

func TestParseAcceptsLegacyFormatsWithoutLosingNanoseconds(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "variable width RFC3339Nano",
			raw:  "2026-09-04T13:34:55.092342987Z",
			want: "2026-09-04T13:34:55.092342987Z",
		},
		{
			name: "RFC3339 offset becomes UTC",
			raw:  "2026-09-04T08:34:55.1234-05:00",
			want: "2026-09-04T13:34:55.123400000Z",
		},
		{
			name: "SQLite time.DateTime",
			raw:  "2026-09-04 13:34:55",
			want: "2026-09-04T13:34:55.000000000Z",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			parsed, err := memorytime.Parse(tc.raw)
			if err != nil {
				t.Fatalf("Parse(%q): %v", tc.raw, err)
			}
			if got := memorytime.Format(parsed); got != tc.want {
				t.Fatalf("Format(Parse(%q)) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}
