package memory

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/hollis-labs/tesseract/domains"
)

// Receipts commit with the event revision. They scope retry keys to namespace
// and authenticated writer; anonymous writes share the empty principal scope.
// This is transport retry deduplication, independent of semantic deduplication.
func (s *Store) eventWriteReplay(ctx context.Context, tx *sql.Tx, in WriteInput, principal string) (string, *Revision, error) {
	if in.IdempotencyKey == "" {
		return "", nil, nil
	}
	if in.Domain != domains.Event || in.MemoryKey != "" {
		return "", nil, fmt.Errorf("%w: idempotency_key requires a keyless event write", ErrInvalidInput)
	}
	if len(in.IdempotencyKey) > 256 {
		return "", nil, fmt.Errorf("%w: idempotency_key exceeds 256 bytes", ErrInvalidInput)
	}
	raw, err := json.Marshal(in)
	if err != nil {
		return "", nil, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	hash := sha256.Sum256(raw)
	digest := hex.EncodeToString(hash[:])
	var stored, revisionID string
	err = tx.QueryRowContext(ctx, `SELECT digest, revision_id FROM event_write_receipts WHERE namespace = ? AND principal = ? AND idempotency_key = ?`, in.Namespace, principal, in.IdempotencyKey).Scan(&stored, &revisionID)
	if errors.Is(err, sql.ErrNoRows) {
		return digest, nil, nil
	}
	if err != nil {
		return "", nil, err
	}
	if stored != digest {
		return "", nil, fmt.Errorf("%w: idempotency_key was already used for a different event write", ErrInvalidInput)
	}
	rev, err := scanRevision(tx.QueryRowContext(ctx, `SELECT `+revisionColumns+` FROM memory_revisions WHERE revision_id = ?`, revisionID))
	if err != nil {
		return "", nil, err
	}
	rev.replayed = true
	rev.WriteOutcome = WriteOutcomeCreated
	return digest, &rev, nil
}
