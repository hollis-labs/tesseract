package mcpadapter

import (
	"errors"

	"github.com/hollis-labs/tesseract/internal/memory"
)

// The opt-in write guards (CW-20260919-0019) are declared on both revisioned
// write tools, memory_write and knowledge_write. Their wording lives here, once,
// so the two tools cannot describe the same guard two ways — the reason
// domainBoundaryLine and the payload-data descriptions are shared the same way.
// The semantics are memory.WriteInput's; internal/memory/writeguards.go says why
// they exist.
const (
	createOnlyArgDescription = "Opt-in guard, default off: fail with key_conflict if the key already has an item, instead of appending a revision to it. " +
		"Use it when a new record's key must not collide with an existing one. Cannot be combined with expected_revision_id."

	expectedRevisionIDArgDescription = "Opt-in guard, default off: fail with revision_conflict unless this revision is the key's current head, so two writers working from one read cannot both succeed. " +
		"Requires a key. The error names the current head; re-read it, reapply your change, retry with that id. " +
		"Independent of supersedes: pass the same id to both to also deprecate the revision you replaced. Cannot be combined with create_only."
)

// writeGuardToolError maps a failed opt-in write guard to its tool error. It
// reports false for any other error, which stays with the caller's own mapping.
func writeGuardToolError(err error) (any, bool) {
	switch {
	case errors.Is(err, memory.ErrKeyConflict):
		return toolError(codeKeyConflict, err.Error()), true
	case errors.Is(err, memory.ErrRevisionConflict):
		return toolError(codeRevisionConflict, err.Error()), true
	}
	return nil, false
}
