package mcpadapter

import (
	"context"
	"crypto/rand"
	"math"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"
)

func notFoundTestULID(at time.Time) string {
	return ulid.MustNew(ulid.Timestamp(at), rand.Reader).String()
}

// An ID that was never minted, reported by an agent as the result of a write
// that never happened, used to answer only "not found". It now also says when a
// ULID with that prefix would have been minted, and how long ago, so a verifier
// can see at once that an ID from a day and a half before the claim is not the
// claim's result. The message text is unchanged: a client reading only that sees
// exactly what it always did.
func TestNotFound_ForAnUnmintedULIDCarriesItsMintTime(t *testing.T) {
	a := newMemoryAdapter(t, "memory:read")
	minted := time.Now().Add(-36*time.Hour - 36*time.Minute)
	id := notFoundTestULID(minted)

	for _, tc := range []struct {
		name    string
		call    func() (any, error)
		field   string
		message string
	}{
		{"tesseract_get by item_id",
			func() (any, error) { return a.handleTesseractGet(context.Background(), map[string]any{"item_id": id}) },
			"item_id", "memory not found: item_id " + id},
		{"tesseract_history by item_id",
			func() (any, error) {
				return a.handleTesseractHistory(context.Background(), map[string]any{"item_id": id})
			},
			"item_id", "memory not found: item_id " + id},
		{"tesseract_get_revision",
			func() (any, error) {
				return a.handleTesseractGetRevision(context.Background(), map[string]any{"revision_id": id})
			},
			"revision_id", "memory not found: revision_id " + id},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res, err := tc.call()
			if err != nil {
				t.Fatalf("handler error: %v", err)
			}
			body := parseResult(t, res)
			if body["code"] != string(codeNotFound) {
				t.Fatalf("code = %v, want %s; body=%v", body["code"], codeNotFound, body)
			}
			if body["message"] != tc.message {
				t.Errorf("message = %q, want %q: the message text must not change", body["message"], tc.message)
			}
			details, ok := body["details"].(map[string]any)
			if !ok {
				t.Fatalf("no details object on a not_found for a ULID-shaped ID: %v", body)
			}
			if details["field"] != tc.field || details["id"] != id {
				t.Errorf("details field=%v id=%v, want %s and %s", details["field"], details["id"], tc.field, id)
			}
			if want := minted.UTC().Format("2006-01-02T15:04:05.000Z"); details["minted_at"] != want {
				t.Errorf("minted_at = %v, want %s", details["minted_at"], want)
			}
			age, _ := details["age_seconds"].(float64)
			want := (36*time.Hour + 36*time.Minute).Seconds()
			if math.Abs(age-want) > 120 {
				t.Errorf("age_seconds = %v, want about %v", age, want)
			}
		})
	}
}

// Anything that gets no details must answer exactly as it did before: no
// `details` key at all, not an empty one.
func TestNotFound_WithoutAnIDToDecodeIsUnchanged(t *testing.T) {
	a := newMemoryAdapter(t, "memory:read")

	for name, call := range map[string]func() (any, error){
		"an item_id that is not a ULID": func() (any, error) {
			return a.handleTesseractGet(context.Background(), map[string]any{"item_id": "not-a-ulid"})
		},
		"a revision_id that is not a ULID": func() (any, error) {
			return a.handleTesseractGetRevision(context.Background(), map[string]any{"revision_id": "not-a-ulid"})
		},
		"a lookup by key, which has no ID to decode": func() (any, error) {
			return a.handleTesseractGet(context.Background(), map[string]any{
				"domain": "memory", "namespace": "user/chrispian/memory/notes", "key": "no.such.key",
			})
		},
	} {
		t.Run(name, func(t *testing.T) {
			res, err := call()
			if err != nil {
				t.Fatalf("handler error: %v", err)
			}
			body := parseResult(t, res)
			if body["code"] != string(codeNotFound) {
				t.Fatalf("code = %v, want %s; body=%v", body["code"], codeNotFound, body)
			}
			if _, present := body["details"]; present {
				t.Errorf("details present on an error with nothing to add: %v", body["details"])
			}
		})
	}
}

func TestToolErrorWithDetails_EmptyDetailsIsExactlyToolError(t *testing.T) {
	plain := parseResult(t, toolError(codeNotFound, "gone"))
	for name, details := range map[string]map[string]any{"nil": nil, "empty": {}} {
		got := parseResult(t, toolErrorWithDetails(codeNotFound, "gone", details))
		if len(got) != len(plain) || got["code"] != plain["code"] || got["message"] != plain["message"] {
			t.Errorf("%s details: %v, want exactly %v", name, got, plain)
		}
		if _, present := got["details"]; present {
			t.Errorf("%s details still emitted a details key", name)
		}
	}
}
