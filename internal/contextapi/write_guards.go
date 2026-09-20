package contextapi

import (
	"errors"
	"net/http"

	"github.com/hollis-labs/tesseract/internal/memory"
)

// writeGuardError reports a failed opt-in write guard (CW-20260919-0019) as a
// 409 carrying the conflict's structured details, and reports whether it did.
// It reports false for any other error, which stays with the caller's own
// mapping.
//
// The codes match what workspace already answers for the same two situations —
// key_conflict for a create onto an occupied key — with revision_conflict for a
// stale expectation, because a revisioned item's head is a revision rather than
// a version_token. The details name the head the write lost to, so a caller can
// re-read and retry without a second lookup that could race another writer.
func writeGuardError(w http.ResponseWriter, err error) bool {
	var conflict *memory.WriteConflict
	if !errors.As(err, &conflict) {
		return false
	}
	code := "revision_conflict"
	if errors.Is(err, memory.ErrKeyConflict) {
		code = "key_conflict"
	}
	writeError(w, http.StatusConflict, code, err.Error(), conflict.Details())
	return true
}
