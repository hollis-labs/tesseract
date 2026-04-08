package conduit

import (
	"context"
	"encoding/json"
	"fmt"

	queue "github.com/hollis-labs/go-queue"
)

// embedJobPayload is the JSON structure expected in embed job payloads
// dispatched by the memory write path.
type embedJobPayload struct {
	RevisionID string `json:"revision_id"`
}

// NewEmbedHandler returns a queue.Handler that processes embed jobs. The
// handler decodes the revision ID from the job payload and logs receipt.
//
// TODO: wire actual memory revision embedding once the embed path for revisions is designed
func NewEmbedHandler(logger func(string, ...any)) queue.Handler {
	if logger == nil {
		logger = func(string, ...any) {}
	}
	return func(ctx context.Context, job *queue.QueuedJob) error {
		var p embedJobPayload
		if err := json.Unmarshal(job.Payload, &p); err != nil {
			return fmt.Errorf("embed handler: failed to decode payload: %w", err)
		}

		logger("embed job received: revision_id=%s", p.RevisionID)
		return nil
	}
}
