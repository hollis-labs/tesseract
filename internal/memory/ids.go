package memory

import (
	"crypto/rand"
	"time"

	"github.com/oklog/ulid/v2"
)

// NewULID returns a new lexicographically sortable ULID as a string.
// Used for memory_id and revision_id.
func NewULID() string {
	return ulid.MustNew(ulid.Timestamp(time.Now()), rand.Reader).String()
}
