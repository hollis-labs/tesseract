package contextstore

// Events outbox (CW-20261016-0140, O1 step 1).
//
// Every audit event is also written, as a complete event-stream envelope v1.1,
// to events_outbox in the SAME transaction as its audit_events row, so the two
// cannot disagree: either both exist or neither does, and a failure is
// returned instead of dropped.
//
// What this is not yet: atomic with the MUTATION. Store.emit still runs after
// the caller's own commit (the Emit* callers discard its error), so a crash
// between the mutation and the emit loses both rows exactly as before. Step 2
// threads the mutation's transaction through each surface.
//
// The outbox is local. Nothing here publishes, relays or serves it; the hub
// drainer and SSE read it through ReadOutbox/OutboxBounds in later steps.
//
// TODO(CW-20261016-0140): the table grows without bound. Retention (prune at or
// below an acknowledged cursor, recording a floor so a stale cursor gets
// cursor_expired) belongs to the drainer/SSE step; no pruning exists yet.

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/hollis-labs/libs/harness/core/adapters/runtimeevents"
)

const (
	outboxApp          = "tesseract"
	outboxKindAudit    = "audit.recorded"
	outboxChannelAudit = runtimeevents.SourceChannel("audit")
	storeIdentityGen   = "generation"
)

// OutboxEntry is one outbox row. Envelope is the complete event, ready to
// publish: its id is stable across redeliveries, Sequence equals Seq and
// Generation is the DB identity. The log-assigned cursor and recorded_at are
// deliberately absent; the hub assigns those.
type OutboxEntry struct {
	Seq          int64           `json:"seq"`
	EventID      string          `json:"event_id"`
	Kind         string          `json:"kind"`
	Subject      string          `json:"subject"`
	Namespace    string          `json:"namespace,omitempty"`
	WorkstreamID string          `json:"workstream_id,omitempty"`
	AuditEventID int64           `json:"audit_event_id,omitempty"`
	CreatedAt    string          `json:"created_at"`
	Envelope     json.RawMessage `json:"envelope"`
}

// OutboxBounds describes what ReadOutbox can serve. HighWater is the newest
// sequence (0 when empty); Earliest is the oldest still present (0 when empty).
type OutboxBounds struct {
	Generation uint64 `json:"generation"`
	Earliest   int64  `json:"earliest_available"`
	HighWater  int64  `json:"high_water"`
}

func migrateEventsOutbox(ctx context.Context, tx *sql.Tx) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS events_outbox (
	seq INTEGER PRIMARY KEY AUTOINCREMENT,
	evt_id TEXT NOT NULL UNIQUE,
	kind TEXT NOT NULL,
	subject TEXT NOT NULL,
	namespace TEXT NOT NULL DEFAULT '',
	workstream_id TEXT NOT NULL DEFAULT '',
	audit_event_id INTEGER,
	envelope_json TEXT NOT NULL,
	created_at TEXT NOT NULL
)`,
		`CREATE INDEX IF NOT EXISTS idx_events_outbox_namespace ON events_outbox(namespace, seq)`,
		`CREATE TABLE IF NOT EXISTS store_identity (
	key TEXT PRIMARY KEY,
	value TEXT NOT NULL
)`,
	}
	for _, q := range stmts {
		if _, err := tx.ExecContext(ctx, q); err != nil {
			return err
		}
	}
	// A fresh generation per database. A restore stages a new file and
	// migrates it, so a restored or recreated DB gets a new one and a consumer
	// holding a cursor from the old DB can tell. INSERT OR IGNORE keeps the
	// replayed-migration case (tests roll schema_version back) stable.
	gen, err := randomGeneration()
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO store_identity (key, value) VALUES (?, ?)`,
		storeIdentityGen, strconv.FormatUint(gen, 10))
	return err
}

// randomGeneration returns a positive 31-bit value: nonzero (zero means
// "unknown" in the envelope) and small enough to be safe in any JSON consumer.
func randomGeneration() (uint64, error) {
	var b [4]byte
	for {
		if _, err := rand.Read(b[:]); err != nil {
			return 0, err
		}
		g := uint64(b[0]&0x7f)<<24 | uint64(b[1])<<16 | uint64(b[2])<<8 | uint64(b[3])
		if g != 0 {
			return g, nil
		}
	}
}

// outboxExecutor is the tx surface the outbox writer needs.
type outboxExecutor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// AppendOutbox writes one outbox envelope per record inside the caller's
// transaction and returns the minted event ids in order. Several records may
// share a tx (and a CorrelationID); nothing is committed here, so the caller's
// commit or rollback covers all of them. This is the entry point per-surface
// writers use to make an event atomic with their own mutation.
func (s *Store) AppendOutbox(ctx context.Context, tx *sql.Tx, recs ...OutboxRecord) ([]string, error) {
	if tx == nil {
		return nil, errors.New("outbox: a transaction is required")
	}
	ids := make([]string, 0, len(recs))
	for _, r := range recs {
		id, err := writeOutboxRecord(ctx, tx, r)
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func writeOutboxEvent(ctx context.Context, x outboxExecutor, ev AuditEvent, auditID int64) error {
	r := recordFromAudit(ev)
	r.AuditEventID = auditID
	_, err := writeOutboxRecord(ctx, x, r)
	return err
}

// writeOutboxRecord inserts the envelope for one record. It runs inside the
// caller's transaction and returns an error for anything it cannot store.
func writeOutboxRecord(ctx context.Context, x outboxExecutor, r OutboxRecord) (string, error) {
	var genText string
	if err := x.QueryRowContext(ctx, `SELECT value FROM store_identity WHERE key = ?`, storeIdentityGen).Scan(&genText); err != nil {
		return "", fmt.Errorf("outbox: read generation: %w", err)
	}
	gen, err := strconv.ParseUint(genText, 10, 64)
	if err != nil || gen == 0 {
		return "", fmt.Errorf("outbox: invalid generation %q", genText)
	}

	evtID := runtimeevents.NewEventID()
	env, subject, err := buildEnvelope(ctx, r, evtID, gen)
	if err != nil {
		return "", err
	}
	created := r.OccurredAt
	if created == "" {
		created = time.Now().UTC().Format(time.RFC3339)
	}
	// Reserve the row first: the envelope carries the sequence the table
	// assigns, so the final JSON is written back inside the same transaction.
	res, err := x.ExecContext(ctx, `
INSERT INTO events_outbox (evt_id, kind, subject, namespace, workstream_id, audit_event_id, envelope_json, created_at)
VALUES (?, ?, ?, ?, ?, ?, '', ?)`,
		evtID, outboxKindAudit, subject, r.Namespace, r.WorkstreamID, nullableInt64(r.AuditEventID), created)
	if err != nil {
		return "", fmt.Errorf("outbox: insert: %w", err)
	}
	seq, err := res.LastInsertId()
	if err != nil {
		return "", fmt.Errorf("outbox: sequence: %w", err)
	}
	env.Sequence = uint64(seq)
	if err := env.Validate(); err != nil {
		return "", fmt.Errorf("outbox: %w", err)
	}
	raw, err := json.Marshal(env)
	if err != nil {
		return "", fmt.Errorf("outbox: marshal: %w", err)
	}
	if _, err := x.ExecContext(ctx, `UPDATE events_outbox SET envelope_json = ? WHERE seq = ?`, string(raw), seq); err != nil {
		return "", fmt.Errorf("outbox: store envelope: %w", err)
	}
	return evtID, nil
}

func nullableInt64(v int64) any {
	if v == 0 {
		return nil
	}
	return v
}

// ReadOutbox returns up to limit entries with seq > afterSeq in ascending
// order. afterSeq 0 starts from the beginning. This is Tesseract's own resume
// token; it is never written into an envelope's cursor field.
func (s *Store) ReadOutbox(ctx context.Context, afterSeq int64, limit int) ([]OutboxEntry, error) {
	if afterSeq < 0 {
		return nil, errors.New("after must be >= 0")
	}
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT seq, evt_id, kind, subject, namespace, workstream_id, COALESCE(audit_event_id, 0), envelope_json, created_at
FROM events_outbox WHERE seq > ? ORDER BY seq ASC LIMIT ?`, afterSeq, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []OutboxEntry
	for rows.Next() {
		var e OutboxEntry
		var env string
		if err := rows.Scan(&e.Seq, &e.EventID, &e.Kind, &e.Subject, &e.Namespace, &e.WorkstreamID, &e.AuditEventID, &env, &e.CreatedAt); err != nil {
			return nil, err
		}
		e.Envelope = json.RawMessage(env)
		out = append(out, e)
	}
	return out, rows.Err()
}

// OutboxBounds reports the DB generation and the readable sequence range.
func (s *Store) OutboxBounds(ctx context.Context) (OutboxBounds, error) {
	var b OutboxBounds
	var genText string
	if err := s.db.QueryRowContext(ctx, `SELECT value FROM store_identity WHERE key = ?`, storeIdentityGen).Scan(&genText); err != nil {
		return b, err
	}
	gen, err := strconv.ParseUint(genText, 10, 64)
	if err != nil {
		return b, err
	}
	b.Generation = gen
	err = s.db.QueryRowContext(ctx, `SELECT COALESCE(MIN(seq), 0), COALESCE(MAX(seq), 0) FROM events_outbox`).Scan(&b.Earliest, &b.HighWater)
	return b, err
}
