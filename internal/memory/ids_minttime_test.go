package memory_test

import (
	"crypto/rand"
	"testing"
	"time"

	"github.com/hollis-labs/tesseract/internal/memory"
	"github.com/oklog/ulid/v2"
)

// ulidAt mints an ID as of t, the way NewULID does but at a time the test picks.
func ulidAt(t time.Time) string {
	return ulid.MustNew(ulid.Timestamp(t), rand.Reader).String()
}

func TestMintTime_DecodesTheTimestampPrefix(t *testing.T) {
	want := time.Date(2026, 9, 18, 5, 19, 56, 123_000_000, time.UTC)
	got, ok := memory.MintTime(ulidAt(want))
	if !ok {
		t.Fatal("a well-formed ULID was not recognized")
	}
	if !got.Equal(want) {
		t.Errorf("MintTime = %s, want %s: the millisecond must survive the round trip", got.Format(time.RFC3339Nano), want.Format(time.RFC3339Nano))
	}
	if got.Location() != time.UTC {
		t.Errorf("MintTime is in %v, want UTC", got.Location())
	}

	// A freshly minted ID decodes to within the clock's resolution of now.
	fresh, ok := memory.MintTime(memory.NewULID())
	if !ok {
		t.Fatal("NewULID's own output was not recognized")
	}
	if d := time.Since(fresh); d < -time.Second || d > 5*time.Second {
		t.Errorf("a just-minted ID decodes to %s, %s away from now", fresh, d)
	}
}

// The ID that motivated this: Nanite chat c395 (2026-09-19) reported it as the
// result of a write that never happened. The decode below is the one the
// incident audit recorded, so a change to how the prefix is read would fail here
// against a value written down before this code existed.
func TestMintTime_DecodesTheIDFromTheIncident(t *testing.T) {
	got, ok := memory.MintTime("01M2SFA0ZQ3K4N6P7R8T9V0WXY")
	if !ok {
		t.Fatal("the incident's ID is well-formed and was not recognized")
	}
	want := time.Date(2026, 9, 18, 5, 19, 56, 0, time.UTC)
	if !got.Truncate(time.Second).Equal(want) {
		t.Errorf("decoded %s, the incident audit recorded %s", got.Format(time.RFC3339), want.Format(time.RFC3339))
	}
}

func TestMintTime_RejectsWhatIsNotAULID(t *testing.T) {
	for name, id := range map[string]string{
		"empty":        "",
		"a memory key": "framework.go-providers",
		"too short":    "01M2SFA0ZQ3K4N6P7R8T9V0WX",
		"too long":     "01M2SFA0ZQ3K4N6P7R8T9V0WXYZ",
		"a character outside Crockford's alphabet (U)": "01M2SFA0ZQ3K4N6P7R8T9V0WXU",
		"a character outside Crockford's alphabet (I)": "01M2SFA0ZQ3K4N6P7R8T9V0WXI",
		"a timestamp that overflows 48 bits":           "8ZZZZZZZZZZZZZZZZZZZZZZZZZ",
		"surrounding whitespace":                       " 01M2SFA0ZQ3K4N6P7R8T9V0WXY",
	} {
		t.Run(name, func(t *testing.T) {
			if got, ok := memory.MintTime(id); ok {
				t.Errorf("MintTime(%q) = %s, want it rejected", id, got)
			}
		})
	}
}

func TestNotFoundDetails(t *testing.T) {
	now := time.Date(2026, 9, 19, 18, 0, 0, 0, time.UTC)

	t.Run("names the field, the ID, the mint time and the age", func(t *testing.T) {
		minted := now.Add(-36*time.Hour - 36*time.Minute) // the incident's 36.6 h
		id := ulidAt(minted)
		got := memory.NotFoundDetails("item_id", id, now)
		if got["field"] != "item_id" || got["id"] != id {
			t.Errorf("details = %v, want field=item_id id=%s", got, id)
		}
		if got["minted_at"] != minted.Format("2006-01-02T15:04:05.000Z") {
			t.Errorf("minted_at = %v, want %s", got["minted_at"], minted.Format("2006-01-02T15:04:05.000Z"))
		}
		if got["age_seconds"] != int64((36*time.Hour + 36*time.Minute).Seconds()) {
			t.Errorf("age_seconds = %v, want %d", got["age_seconds"], int64((36*time.Hour + 36*time.Minute).Seconds()))
		}
	})

	t.Run("an ID from the future has a negative age rather than being hidden", func(t *testing.T) {
		got := memory.NotFoundDetails("revision_id", ulidAt(now.Add(2*time.Hour)), now)
		age, _ := got["age_seconds"].(int64)
		if age >= 0 {
			t.Errorf("age_seconds = %v, want a negative number for an ID minted after now", got["age_seconds"])
		}
	})

	t.Run("nothing to say about an ID that is not a ULID", func(t *testing.T) {
		if got := memory.NotFoundDetails("item_id", "not-an-id", now); got != nil {
			t.Errorf("details = %v, want nil so the error is exactly what it was", got)
		}
	})
}
