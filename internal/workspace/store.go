package workspace

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hollis-labs/tesseract/internal/memory"
	"github.com/hollis-labs/tesseract/internal/memorytime"
	"modernc.org/sqlite"
)

const itemColumns = `item_id, version_token, namespace, key_name, summary, body,
	data, data_schema_hash, tags, consumer_state, author_agent_id, author_version,
	session_id, created_at, updated_at, activation, access_count, last_used_at, last_decayed_at,
	workstream_id, write_context`

var errRetryReceiptRace = errors.New("retry workspace receipt race")

type Store struct {
	db    *sql.DB
	now   func() time.Time
	newID func() string
}

type Option func(*Store)

func WithClock(now func() time.Time) Option {
	return func(s *Store) { s.now = now }
}

func WithIDGenerator(newID func() string) Option {
	return func(s *Store) { s.newID = newID }
}

func NewStore(db *sql.DB, options ...Option) *Store {
	s := &Store{db: db, now: time.Now, newID: memory.NewULID}
	for _, option := range options {
		option(s)
	}
	return s
}

func (s *Store) Create(ctx context.Context, in CreateInput) (Item, error) {
	if err := validateCreate(in); err != nil {
		return Item{}, err
	}
	return retryMutation(ctx, "create", func() (Item, error) {
		return s.createOnce(ctx, in)
	})
}

// CreateWithReceipt atomically creates an item and its retry receipt. An exact
// replay returns the original identity without reading or reinforcing content.
func (s *Store) CreateWithReceipt(ctx context.Context, req CreateRequest) (MutationReceipt, error) {
	if err := validateCreate(req.CreateInput); err != nil {
		return MutationReceipt{}, err
	}
	if req.Key == "" && req.IdempotencyKey == "" {
		return MutationReceipt{}, fmt.Errorf("%w: idempotency_key is required for keyless create", ErrInvalidInput)
	}
	digest, err := createDigest(req.CreateInput)
	if err != nil {
		return MutationReceipt{}, err
	}
	return retryMutation(ctx, "create", func() (MutationReceipt, error) {
		return s.createWithReceiptOnce(ctx, req, digest)
	})
}

func (s *Store) createWithReceiptOnce(ctx context.Context, req CreateRequest, digest string) (MutationReceipt, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return MutationReceipt{}, fmt.Errorf("begin workspace create: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if req.IdempotencyKey != "" {
		var storedDigest, itemID string
		err = tx.QueryRowContext(ctx, `SELECT request_digest, item_id FROM workspace_creation_receipts
			WHERE namespace = ? AND operation = 'create' AND idempotency_key = ?`, req.Namespace, req.IdempotencyKey).
			Scan(&storedDigest, &itemID)
		switch {
		case err == nil:
			if storedDigest != digest {
				return MutationReceipt{}, fmt.Errorf("%w: idempotency_key was already used for different create arguments", ErrIdempotencyConflict)
			}
			availability, availabilityErr := receiptAvailability(ctx, tx, itemID)
			if availabilityErr != nil {
				return MutationReceipt{}, availabilityErr
			}
			return MutationReceipt{Status: "replayed", ItemID: itemID, Availability: availability}, nil
		case !errors.Is(err, sql.ErrNoRows):
			return MutationReceipt{}, fmt.Errorf("read workspace create receipt: %w", err)
		}
	}

	item, err := s.createInTx(ctx, tx, req.CreateInput)
	if err != nil {
		return MutationReceipt{}, err
	}
	if req.IdempotencyKey != "" {
		_, err = tx.ExecContext(ctx, `INSERT INTO workspace_creation_receipts
			(namespace, operation, idempotency_key, request_digest, item_id)
			VALUES (?, 'create', ?, ?, ?)`, req.Namespace, req.IdempotencyKey, digest, item.ItemID)
		if err != nil {
			if strings.Contains(err.Error(), "UNIQUE constraint failed") {
				return MutationReceipt{}, errRetryReceiptRace
			}
			return MutationReceipt{}, fmt.Errorf("insert workspace create receipt: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return MutationReceipt{}, fmt.Errorf("commit workspace create: %w", err)
	}
	return MutationReceipt{Status: "created", ItemID: item.ItemID, VersionToken: item.VersionToken}, nil
}

func (s *Store) createOnce(ctx context.Context, in CreateInput) (Item, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Item{}, fmt.Errorf("begin workspace create: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	item, err := s.createInTx(ctx, tx, in)
	if err != nil {
		return Item{}, err
	}
	if err := tx.Commit(); err != nil {
		return Item{}, fmt.Errorf("commit workspace create: %w", err)
	}
	return item, nil
}

func (s *Store) createInTx(ctx context.Context, tx *sql.Tx, in CreateInput) (Item, error) {
	key := optionalText(in.Key)
	if key.Valid {
		occupied, checkErr := liveKeyExists(ctx, tx, in.Namespace, in.Key, "")
		if checkErr != nil {
			return Item{}, checkErr
		}
		if occupied {
			return Item{}, fmt.Errorf("%w: live key is already occupied", ErrKeyConflict)
		}
	}
	itemID, err := s.nextItemID(ctx, tx)
	if err != nil {
		return Item{}, err
	}
	versionToken, err := s.nextVersionToken(ctx, tx, "")
	if err != nil {
		return Item{}, err
	}
	now := s.now().UTC()
	tags, err := encodeTags(in.Tags)
	if err != nil {
		return Item{}, err
	}
	workstreamID := ""
	if in.WorkstreamID != nil {
		workstreamID = *in.WorkstreamID
	} else if received := memory.WriteContextFromContext(ctx); received != nil {
		workstreamID = received.WorkstreamID
	}
	writeContextValue, _, err := memory.EncodeWriteContext(ctx)
	if err != nil {
		return Item{}, err
	}

	_, err = tx.ExecContext(ctx, `INSERT INTO workspace_items (`+itemColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1.0, 0, ?, ?, ?, ?)`,
		itemID, versionToken, in.Namespace, key, in.Summary, optionalText(in.Body), optionalJSON(in.Data),
		optionalText(in.DataSchemaHash), tags, optionalJSON(in.ConsumerState), in.Author.AgentID,
		in.Author.AgentVersion, in.SessionID, memorytime.Format(now), memorytime.Format(now),
		memorytime.Format(now), memorytime.Format(now), optionalText(workstreamID), writeContextValue)
	if err != nil {
		if isLiveKeyConstraint(err) {
			return Item{}, fmt.Errorf("%w: live key is already occupied", ErrKeyConflict)
		}
		return Item{}, fmt.Errorf("insert workspace item: %w", err)
	}
	item, err := getLive(ctx, tx, itemID)
	if err != nil {
		return Item{}, err
	}
	return item, nil
}

func createDigest(in CreateInput) (string, error) {
	// RawMessage is serialized without decoding through float64, preserving the
	// caller's JSON number representation in data and consumer_state.
	b, err := json.Marshal(struct {
		Namespace      string          `json:"namespace"`
		Key            string          `json:"key"`
		Summary        string          `json:"summary"`
		Body           string          `json:"body"`
		Data           json.RawMessage `json:"data"`
		DataSchemaHash string          `json:"data_schema_hash"`
		Tags           []string        `json:"tags"`
		ConsumerState  json.RawMessage `json:"consumer_state"`
		Author         memory.Author   `json:"author"`
		SessionID      string          `json:"session_id"`
		WorkstreamID   *string         `json:"workstream_id,omitempty"`
	}{in.Namespace, in.Key, in.Summary, in.Body, in.Data, in.DataSchemaHash, in.Tags, in.ConsumerState, in.Author, in.SessionID, in.WorkstreamID})
	if err != nil {
		return "", fmt.Errorf("encode workspace create digest: %w", err)
	}
	sum := sha256.Sum256(b)
	return fmt.Sprintf("%x", sum[:]), nil
}

func receiptAvailability(ctx context.Context, tx *sql.Tx, itemID string) (string, error) {
	var live, deleted int
	if err := tx.QueryRowContext(ctx, `SELECT
		EXISTS(SELECT 1 FROM workspace_items WHERE item_id = ?),
		EXISTS(SELECT 1 FROM workspace_tombstones WHERE item_id = ?)`, itemID, itemID).Scan(&live, &deleted); err != nil {
		return "", fmt.Errorf("read workspace receipt availability: %w", err)
	}
	switch {
	case live != 0:
		return "live", nil
	case deleted != 0:
		return "deleted", nil
	default:
		return "", fmt.Errorf("workspace create receipt points to missing item %s", itemID)
	}
}

func (s *Store) Edit(ctx context.Context, in EditInput) (Item, error) {
	clears, err := validateEdit(in)
	if err != nil {
		return Item{}, err
	}
	return retryMutation(ctx, "edit", func() (Item, error) {
		return s.editOnce(ctx, in, clears)
	})
}

func (s *Store) editOnce(ctx context.Context, in EditInput, clears map[ClearField]bool) (Item, error) {
	newToken, err := s.nextTokenCandidate(in.VersionToken)
	if err != nil {
		return Item{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Item{}, fmt.Errorf("begin workspace edit: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	state, err := readRetentionState(tx.QueryRowContext(ctx, `SELECT item_id, namespace, activation, last_used_at, last_decayed_at FROM workspace_items WHERE item_id = ?`, in.ItemID))
	if errors.Is(err, sql.ErrNoRows) {
		return Item{}, classifyConditionalMiss(ctx, tx, in.ItemID)
	}
	if err != nil {
		return Item{}, fmt.Errorf("read workspace edit activation: %w", err)
	}
	sets := []string{
		"version_token = ?", "updated_at = ?", "author_agent_id = ?", "author_version = ?", "session_id = ?",
		"access_count = access_count + 1", "last_used_at = ?", "activation = ?", "last_decayed_at = ?", "write_context = ?",
	}
	now := s.now().UTC()
	usedAt := now
	if state.lastUsedAt.After(usedAt) {
		usedAt = state.lastUsedAt
	}
	decayedAt := now
	if state.lastDecayedAt.After(decayedAt) {
		decayedAt = state.lastDecayedAt
	}
	activation := reinforcedActivation(state.activation, state.lastDecayedAt, now)
	writeContextValue, _, err := memory.EncodeWriteContext(ctx)
	if err != nil {
		return Item{}, err
	}
	args := []any{newToken, memorytime.Format(now), in.Author.AgentID, in.Author.AgentVersion, in.SessionID,
		memorytime.Format(usedAt), activation, memorytime.Format(decayedAt), writeContextValue}
	if in.Key != nil {
		sets = append(sets, "key_name = ?")
		args = append(args, *in.Key)
	} else if clears[ClearKey] {
		sets = append(sets, "key_name = NULL")
	}
	if in.Summary != nil {
		sets = append(sets, "summary = ?")
		args = append(args, *in.Summary)
	}
	if in.Body != nil {
		sets = append(sets, "body = ?")
		args = append(args, *in.Body)
	} else if clears[ClearBody] {
		sets = append(sets, "body = NULL")
	}
	if in.Data != nil {
		sets = append(sets, "data = ?", "data_schema_hash = ?")
		args = append(args, string(*in.Data))
		if in.DataSchemaHash == nil || *in.DataSchemaHash == "" {
			args = append(args, nil)
		} else {
			args = append(args, *in.DataSchemaHash)
		}
	} else if clears[ClearData] {
		sets = append(sets, "data = NULL", "data_schema_hash = NULL")
	}
	if in.Tags != nil {
		encoded, encodeErr := json.Marshal(*in.Tags)
		if encodeErr != nil {
			return Item{}, fmt.Errorf("encode tags: %w", encodeErr)
		}
		sets = append(sets, "tags = ?")
		args = append(args, string(encoded))
	} else if clears[ClearTags] {
		sets = append(sets, "tags = NULL")
	}
	if in.ConsumerState != nil {
		sets = append(sets, "consumer_state = ?")
		args = append(args, string(*in.ConsumerState))
	} else if clears[ClearConsumerState] {
		sets = append(sets, "consumer_state = NULL")
	}
	if in.WorkstreamID != nil {
		sets = append(sets, "workstream_id = ?")
		args = append(args, optionalText(*in.WorkstreamID))
	} else if clears[ClearWorkstreamID] {
		sets = append(sets, "workstream_id = NULL")
	}
	args = append(args, in.ItemID, in.VersionToken)
	// #nosec G202 -- every SET clause above is a fixed internal literal; only
	// values supplied by callers are bound through args.
	res, err := tx.ExecContext(ctx, `UPDATE workspace_items SET `+strings.Join(sets, ", ")+`
		WHERE item_id = ? AND version_token = ?`, args...)
	if err != nil {
		if isLiveKeyConstraint(err) {
			return Item{}, fmt.Errorf("%w: live key is already occupied", ErrKeyConflict)
		}
		return Item{}, fmt.Errorf("update workspace item: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return Item{}, fmt.Errorf("workspace edit rows affected: %w", err)
	}
	if n == 0 {
		return Item{}, classifyConditionalMiss(ctx, tx, in.ItemID)
	}
	item, err := getLive(ctx, tx, in.ItemID)
	if err != nil {
		return Item{}, err
	}
	if err := tx.Commit(); err != nil {
		return Item{}, fmt.Errorf("commit workspace edit: %w", err)
	}
	return item, nil
}

func (s *Store) Delete(ctx context.Context, in DeleteInput) (Metadata, error) {
	if in.ItemID == "" || in.VersionToken == "" {
		return Metadata{}, fmt.Errorf("%w: item_id and version_token are required", ErrInvalidInput)
	}
	return retryMutation(ctx, "delete", func() (Metadata, error) {
		return s.deleteOnce(ctx, in)
	})
}

// DeleteWithReceipt applies a conditional first delete and makes later retries
// converge on the original tombstone receipt.
func (s *Store) DeleteWithReceipt(ctx context.Context, in DeleteInput) (DeleteReceipt, error) {
	if strings.TrimSpace(in.ItemID) == "" || in.VersionToken == "" {
		return DeleteReceipt{}, fmt.Errorf("%w: item_id and version_token are required", ErrInvalidInput)
	}
	return retryMutation(ctx, "delete receipt", func() (DeleteReceipt, error) {
		return s.deleteWithReceiptOnce(ctx, in)
	})
}

func (s *Store) deleteWithReceiptOnce(ctx context.Context, in DeleteInput) (DeleteReceipt, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return DeleteReceipt{}, fmt.Errorf("begin workspace delete receipt: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var namespace, currentToken string
	err = tx.QueryRowContext(ctx, `SELECT namespace, version_token FROM workspace_items WHERE item_id = ?`, in.ItemID).Scan(&namespace, &currentToken)
	if errors.Is(err, sql.ErrNoRows) {
		var deletedRaw string
		err = tx.QueryRowContext(ctx, `SELECT namespace, deleted_at FROM workspace_tombstones WHERE item_id = ?`, in.ItemID).Scan(&namespace, &deletedRaw)
		if errors.Is(err, sql.ErrNoRows) {
			return DeleteReceipt{}, fmt.Errorf("%w: item_id %s", ErrNotFound, in.ItemID)
		}
		if err != nil {
			return DeleteReceipt{}, err
		}
		deletedAt, parseErr := memorytime.Parse(deletedRaw)
		if parseErr != nil {
			return DeleteReceipt{}, fmt.Errorf("parse workspace deleted_at: %w", parseErr)
		}
		return DeleteReceipt{Status: "deleted", ItemID: in.ItemID, DeletedAt: &deletedAt}, nil
	}
	if err != nil {
		return DeleteReceipt{}, fmt.Errorf("read workspace delete target: %w", err)
	}
	if currentToken != in.VersionToken {
		return DeleteReceipt{}, fmt.Errorf("%w: item_id %s", ErrVersionConflict, in.ItemID)
	}

	deletedAt := s.now().UTC()
	if _, err = deleteLiveInTx(ctx, tx, in.ItemID, in.VersionToken, deletedAt); err != nil {
		return DeleteReceipt{}, err
	}
	if err := tx.Commit(); err != nil {
		return DeleteReceipt{}, fmt.Errorf("commit workspace delete receipt: %w", err)
	}
	return DeleteReceipt{Status: "deleted", ItemID: in.ItemID, DeletedAt: &deletedAt}, nil
}

func (s *Store) deleteOnce(ctx context.Context, in DeleteInput) (Metadata, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Metadata{}, fmt.Errorf("begin workspace delete: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	deletedAt := s.now().UTC()
	namespace, err := deleteLiveInTx(ctx, tx, in.ItemID, in.VersionToken, deletedAt)
	if err != nil {
		return Metadata{}, err
	}
	if err := tx.Commit(); err != nil {
		return Metadata{}, fmt.Errorf("commit workspace delete: %w", err)
	}
	return Metadata{ItemID: in.ItemID, Domain: Domain, Namespace: namespace, Deleted: true, DeletedAt: &deletedAt}, nil
}

func (s *Store) LookupMetadata(ctx context.Context, itemID string) (Metadata, error) {
	var namespace string
	err := s.db.QueryRowContext(ctx, `SELECT namespace FROM workspace_items WHERE item_id = ?`, itemID).Scan(&namespace)
	if err == nil {
		return Metadata{ItemID: itemID, Domain: Domain, Namespace: namespace}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Metadata{}, err
	}
	var deletedRaw string
	err = s.db.QueryRowContext(ctx, `SELECT namespace, deleted_at FROM workspace_tombstones WHERE item_id = ?`, itemID).Scan(&namespace, &deletedRaw)
	if errors.Is(err, sql.ErrNoRows) {
		return Metadata{}, fmt.Errorf("%w: item_id %s", ErrNotFound, itemID)
	}
	if err != nil {
		return Metadata{}, err
	}
	deletedAt, err := memorytime.Parse(deletedRaw)
	if err != nil {
		return Metadata{}, fmt.Errorf("parse workspace deleted_at: %w", err)
	}
	return Metadata{ItemID: itemID, Domain: Domain, Namespace: namespace, Deleted: true, DeletedAt: &deletedAt}, nil
}

// LookupMetadataByKey resolves the live owner of one exact legacy key without
// loading content or recording use. Tombstones intentionally carry no key, so
// a deleted old key is not resolvable unless a new live item owns it.
func (s *Store) LookupMetadataByKey(ctx context.Context, namespace, key string) (Metadata, error) {
	var itemID string
	err := s.db.QueryRowContext(ctx, `
SELECT item_id FROM workspace_items
WHERE namespace = ? AND key_name = ?`, namespace, key).Scan(&itemID)
	if errors.Is(err, sql.ErrNoRows) {
		return Metadata{}, fmt.Errorf("%w: no live item at exact key", ErrNotFound)
	}
	if err != nil {
		return Metadata{}, err
	}
	return Metadata{ItemID: itemID, Domain: Domain, Namespace: namespace}, nil
}

func (s *Store) GetCurrent(ctx context.Context, itemID string) (Item, error) {
	item, err := getLive(ctx, s.db, itemID)
	if errors.Is(err, sql.ErrNoRows) {
		return Item{}, s.readMiss(ctx, itemID)
	}
	return item, err
}

func (s *Store) GetCurrentByKey(ctx context.Context, namespace, key string) (Item, error) {
	if err := memory.ValidateWorkspaceNamespace(namespace); err != nil {
		return Item{}, fmt.Errorf("%w: %w", ErrInvalidInput, err)
	}
	if key == "" {
		return Item{}, fmt.Errorf("%w: keyless items cannot be read by key", ErrInvalidInput)
	}
	item, err := getLiveByKey(ctx, s.db, namespace, key)
	if errors.Is(err, sql.ErrNoRows) {
		return Item{}, fmt.Errorf("%w: no live item at exact key", ErrNotFound)
	}
	return item, err
}

func (s *Store) GetCurrentAndUse(ctx context.Context, itemID string) (Item, error) {
	return retryMutation(ctx, "read use", func() (Item, error) {
		return s.getCurrentAndUseOnce(ctx, itemID)
	})
}

func (s *Store) getCurrentAndUseOnce(ctx context.Context, itemID string) (Item, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Item{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if useErr := s.markUsed(ctx, tx, itemID); useErr != nil {
		return Item{}, useErr
	}
	item, err := getLive(ctx, tx, itemID)
	if err != nil {
		return Item{}, err
	}
	if err := tx.Commit(); err != nil {
		return Item{}, err
	}
	return item, nil
}

// GetCurrentByKeyAndUse resolves an exact legacy key and reinforces the item
// in one transaction, preventing a rename/delete race between lookup and use.
func (s *Store) GetCurrentByKeyAndUse(ctx context.Context, namespace, key string) (Item, error) {
	if err := memory.ValidateWorkspaceNamespace(namespace); err != nil {
		return Item{}, fmt.Errorf("%w: %w", ErrInvalidInput, err)
	}
	if key == "" {
		return Item{}, fmt.Errorf("%w: keyless items cannot be read by key", ErrInvalidInput)
	}
	return retryMutation(ctx, "key read use", func() (Item, error) {
		return s.getCurrentByKeyAndUseOnce(ctx, namespace, key)
	})
}

func (s *Store) getCurrentByKeyAndUseOnce(ctx context.Context, namespace, key string) (Item, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Item{}, err
	}
	defer func() { _ = tx.Rollback() }()
	item, err := getLiveByKey(ctx, tx, namespace, key)
	if errors.Is(err, sql.ErrNoRows) {
		return Item{}, fmt.Errorf("%w: no live item at exact key", ErrNotFound)
	}
	if err != nil {
		return Item{}, err
	}
	if markErr := s.markUsed(ctx, tx, item.ItemID); markErr != nil {
		return Item{}, markErr
	}
	item, err = getLive(ctx, tx, item.ItemID)
	if err != nil {
		return Item{}, err
	}
	if err := tx.Commit(); err != nil {
		return Item{}, err
	}
	return item, nil
}

func (s *Store) MarkUsed(ctx context.Context, itemID string) error {
	_, err := retryMutation(ctx, "mark used", func() (struct{}, error) {
		return struct{}{}, s.markUsedOnce(ctx, itemID)
	})
	return err
}

func (s *Store) markUsedOnce(ctx context.Context, itemID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := s.markUsed(ctx, tx, itemID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) markUsed(ctx context.Context, tx *sql.Tx, itemID string) error {
	now := s.now().UTC()
	state, err := readRetentionState(tx.QueryRowContext(ctx, `SELECT item_id, namespace, activation, last_used_at, last_decayed_at FROM workspace_items WHERE item_id = ?`, itemID))
	if errors.Is(err, sql.ErrNoRows) {
		return classifyConditionalMiss(ctx, tx, itemID)
	}
	if err != nil {
		return err
	}
	usedAt := now
	if state.lastUsedAt.After(usedAt) {
		usedAt = state.lastUsedAt
	}
	decayedAt := now
	if state.lastDecayedAt.After(decayedAt) {
		decayedAt = state.lastDecayedAt
	}
	activation := reinforcedActivation(state.activation, state.lastDecayedAt, now)
	res, err := tx.ExecContext(ctx, `UPDATE workspace_items
		SET access_count = access_count + 1, last_used_at = ?, activation = ?, last_decayed_at = ?
		WHERE item_id = ?`, memorytime.Format(usedAt), activation, memorytime.Format(decayedAt), itemID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return classifyConditionalMiss(ctx, tx, itemID)
	}
	return nil
}

// deleteLiveInTx is the single workspace privacy boundary shared by manual
// delete and retention purge. An empty expectedToken is reserved for a purge
// transaction that has already established current eligibility.
func deleteLiveInTx(ctx context.Context, tx *sql.Tx, itemID, expectedToken string, deletedAt time.Time) (string, error) {
	where := "item_id = ?"
	args := []any{Domain, memorytime.Format(deletedAt), itemID}
	if expectedToken != "" {
		where += " AND version_token = ?"
		args = append(args, expectedToken)
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO workspace_tombstones (item_id, domain, namespace, deleted_at)
		SELECT item_id, ?, namespace, ? FROM workspace_items WHERE `+where, args...)
	if err != nil {
		return "", fmt.Errorf("create workspace tombstone: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return "", fmt.Errorf("workspace delete rows affected: %w", err)
	}
	if n == 0 {
		return "", classifyConditionalMiss(ctx, tx, itemID)
	}
	var namespace string
	if scanErr := tx.QueryRowContext(ctx, `SELECT namespace FROM workspace_tombstones WHERE item_id = ?`, itemID).Scan(&namespace); scanErr != nil {
		return "", fmt.Errorf("read workspace tombstone: %w", scanErr)
	}
	deleteQuery := `DELETE FROM workspace_items WHERE ` + where
	deleteArgs := []any{itemID}
	if expectedToken != "" {
		deleteArgs = append(deleteArgs, expectedToken)
	}
	res, err = tx.ExecContext(ctx, deleteQuery, deleteArgs...)
	if err != nil {
		return "", fmt.Errorf("remove workspace content: %w", err)
	}
	n, err = res.RowsAffected()
	if err != nil {
		return "", fmt.Errorf("remove workspace content rows affected: %w", err)
	}
	if n != 1 {
		return "", fmt.Errorf("remove workspace content: affected %d rows, want 1", n)
	}
	return namespace, nil
}

type scanner interface{ Scan(...any) error }

func getLive(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, itemID string) (Item, error) {
	return scanItem(q.QueryRowContext(ctx, `SELECT `+itemColumns+` FROM workspace_items WHERE item_id = ?`, itemID))
}

func getLiveByKey(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, namespace, key string) (Item, error) {
	return scanItem(q.QueryRowContext(ctx, `SELECT `+itemColumns+` FROM workspace_items WHERE namespace = ? AND key_name = ?`, namespace, key))
}

func scanItem(row scanner) (Item, error) {
	var item Item
	var key, body, data, dataHash, tags, consumer, workstreamID, writeContext sql.NullString
	var createdRaw, updatedRaw, usedRaw, decayedRaw string
	err := row.Scan(&item.ItemID, &item.VersionToken, &item.Namespace, &key, &item.Summary, &body,
		&data, &dataHash, &tags, &consumer, &item.Author.AgentID, &item.Author.AgentVersion,
		&item.SessionID, &createdRaw, &updatedRaw, &item.Activation, &item.AccessCount, &usedRaw, &decayedRaw,
		&workstreamID, &writeContext)
	if err != nil {
		return Item{}, err
	}
	item.Domain = Domain
	item.Key, item.Body, item.DataSchemaHash = key.String, body.String, dataHash.String
	item.WorkstreamID = workstreamID.String
	if writeContext.Valid {
		item.Provenance, err = memory.DecodeWriteContext(writeContext.String)
		if err != nil {
			return Item{}, err
		}
	}
	if data.Valid {
		item.Data = json.RawMessage(data.String)
	}
	if consumer.Valid {
		item.ConsumerState = json.RawMessage(consumer.String)
	}
	if tags.Valid {
		if err := json.Unmarshal([]byte(tags.String), &item.Tags); err != nil {
			return Item{}, fmt.Errorf("decode workspace tags: %w", err)
		}
	}
	var parseErr error
	if item.CreatedAt, parseErr = memorytime.Parse(createdRaw); parseErr != nil {
		return Item{}, parseErr
	}
	if item.UpdatedAt, parseErr = memorytime.Parse(updatedRaw); parseErr != nil {
		return Item{}, parseErr
	}
	if item.LastUsedAt, parseErr = memorytime.Parse(usedRaw); parseErr != nil {
		return Item{}, parseErr
	}
	if item.LastDecayedAt, parseErr = memorytime.Parse(decayedRaw); parseErr != nil {
		return Item{}, parseErr
	}
	return item, nil
}

func (s *Store) readMiss(ctx context.Context, itemID string) error {
	var exists int
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM workspace_tombstones WHERE item_id = ?)`, itemID).Scan(&exists)
	if err != nil {
		return err
	}
	if exists != 0 {
		return fmt.Errorf("%w: item_id %s", ErrDeleted, itemID)
	}
	return fmt.Errorf("%w: item_id %s", ErrNotFound, itemID)
}

func classifyConditionalMiss(ctx context.Context, tx *sql.Tx, itemID string) error {
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM workspace_tombstones WHERE item_id = ?)`, itemID).Scan(&exists); err != nil {
		return err
	}
	if exists != 0 {
		return fmt.Errorf("%w: item_id %s", ErrDeleted, itemID)
	}
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM workspace_items WHERE item_id = ?)`, itemID).Scan(&exists); err != nil {
		return err
	}
	if exists != 0 {
		return fmt.Errorf("%w: item_id %s", ErrVersionConflict, itemID)
	}
	return fmt.Errorf("%w: item_id %s", ErrNotFound, itemID)
}

func (s *Store) nextItemID(ctx context.Context, tx *sql.Tx) (string, error) {
	for range 64 {
		id := s.newID()
		if id == "" {
			continue
		}
		var exists int
		err := tx.QueryRowContext(ctx, `SELECT EXISTS(
			SELECT 1 FROM workspace_items WHERE item_id = ?
			UNION ALL SELECT 1 FROM workspace_tombstones WHERE item_id = ?
			UNION ALL SELECT 1 FROM memory_state WHERE memory_id = ?
			UNION ALL SELECT 1 FROM memory_revisions WHERE revision_id = ?
		)`, id, id, id, id).Scan(&exists)
		if err != nil {
			return "", fmt.Errorf("check workspace item identity: %w", err)
		}
		if exists == 0 {
			return id, nil
		}
	}
	return "", fmt.Errorf("allocate workspace item identity after collision retries")
}

func (s *Store) nextVersionToken(ctx context.Context, tx *sql.Tx, previous string) (string, error) {
	for range 64 {
		token, err := s.nextTokenCandidate(previous)
		if err != nil {
			return "", err
		}
		var exists int
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM workspace_items WHERE version_token = ?)`, token).Scan(&exists); err != nil {
			return "", err
		}
		if exists == 0 {
			return token, nil
		}
	}
	return "", fmt.Errorf("allocate workspace version token after collision retries")
}

func (s *Store) nextTokenCandidate(previous string) (string, error) {
	for range 64 {
		token := s.newID()
		if token == "" || token == previous {
			continue
		}
		return token, nil
	}
	return "", fmt.Errorf("allocate workspace version token after collision retries")
}

func liveKeyExists(ctx context.Context, tx *sql.Tx, namespace, key, exceptItemID string) (bool, error) {
	var exists int
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(
		SELECT 1 FROM workspace_items WHERE namespace = ? AND key_name = ? AND item_id <> ?
	)`, namespace, key, exceptItemID).Scan(&exists)
	return exists != 0, err
}

func optionalText(value string) sql.NullString {
	return sql.NullString{String: value, Valid: value != ""}
}

func optionalJSON(value json.RawMessage) sql.NullString {
	return sql.NullString{String: string(value), Valid: len(value) != 0}
}

func encodeTags(tags []string) (sql.NullString, error) {
	if tags == nil {
		return sql.NullString{}, nil
	}
	encoded, err := json.Marshal(tags)
	if err != nil {
		return sql.NullString{}, fmt.Errorf("encode workspace tags: %w", err)
	}
	return sql.NullString{String: string(encoded), Valid: true}, nil
}

func isLiveKeyConstraint(err error) bool {
	return strings.Contains(err.Error(), "UNIQUE constraint failed: workspace_items.namespace, workspace_items.key_name")
}

func isSQLiteBusy(err error) bool {
	var sqliteErr *sqlite.Error
	return errors.As(err, &sqliteErr) && sqliteErr.Code()&0xff == 5
}

func retryMutation[T any](ctx context.Context, operation string, fn func() (T, error)) (T, error) {
	var zero T
	var lastErr error
	for attempt := range 8 {
		result, err := fn()
		if !isSQLiteBusy(err) && !errors.Is(err, errRetryReceiptRace) {
			return result, err
		}
		lastErr = err
		select {
		case <-ctx.Done():
			return zero, ctx.Err()
		case <-time.After(time.Duration(1<<attempt) * time.Millisecond):
		}
	}
	return zero, fmt.Errorf("workspace %s exhausted SQLite snapshot retries: %w", operation, lastErr)
}
