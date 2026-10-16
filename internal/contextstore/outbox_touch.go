package contextstore

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"sort"
)

// touchEventItemCap bounds the item_ids carried by one touch event. A touch
// request is itself capped at 100 items, so this only bites if that changes.
const touchEventItemCap = 100

// RecordTouches writes the audit.recorded action=touch events of ONE touch
// call into the caller's transaction (the same transaction as the activation
// UPDATE), so the events commit or roll back with the reinforcement.
//
// Ruling (decisions/tesseract_touch_events_per_workstream_2026_10_16): one
// event per distinct workstream_id among the touched items, plus one event for
// the items with none, which omits scope.workstream_id. workstream_id comes
// from the stored rows (the caller passes it from them), never from the
// request. All events share correlationID; each gets its own evt_ id. Order is
// workstream_id ascending with the no-workstream event last. The payload
// carries ids only, never content.
//
// itemIDs and workstreamIDs are parallel; "" in workstreamIDs means none.
// The signature is primitive so memory can declare it without importing this
// package (see memory.TouchSink).
func (s *Store) RecordTouches(ctx context.Context, tx *sql.Tx, correlationID string, itemIDs, workstreamIDs []string) error {
	if len(itemIDs) != len(workstreamIDs) {
		return errors.New("outbox: touch itemIDs and workstreamIDs differ in length")
	}
	if len(itemIDs) == 0 {
		return nil
	}
	byWS := map[string][]string{}
	for i, id := range itemIDs {
		byWS[workstreamIDs[i]] = append(byWS[workstreamIDs[i]], id)
	}
	keys := make([]string, 0, len(byWS))
	for ws := range byWS {
		if ws != "" {
			keys = append(keys, ws)
		}
	}
	sort.Strings(keys)
	if _, ok := byWS[""]; ok {
		keys = append(keys, "")
	}

	recs := make([]OutboxRecord, 0, len(keys))
	for _, ws := range keys {
		ids := append([]string(nil), byWS[ws]...)
		sort.Strings(ids)
		extra := map[string]any{}
		if len(ids) > touchEventItemCap {
			extra["item_count"] = len(ids)
			extra["truncated"] = true
			ids = ids[:touchEventItemCap]
		}
		extra["item_ids"] = ids
		rec := OutboxRecord{
			Action:        "touch",
			CorrelationID: correlationID,
			WorkstreamID:  ws,
			Extra:         extra,
		}
		if ws != "" {
			rec.SubjectType, rec.SubjectID = "workstream", ws
		} else {
			rec.SubjectType, rec.SubjectID = "context", "unscoped"
		}
		recs = append(recs, rec)
	}
	return appendTouchEventsBestEffort(ctx, s, tx, recs)
}

// appendTouchEventsBestEffort writes the touch events under a SAVEPOINT. The
// events are derived; the activation UPDATE they describe is the mutation, and
// a mapper or validation failure must never reject a valid touch
// (orch/architect ruling). On failure the savepoint rolls back, the touch still
// commits, and the gap is logged at ERROR and counted (OutboxSkipped). A touch
// has no audit row, so nothing is written to outbox_skipped. A failure of the
// SAVEPOINT statements themselves, or of the surrounding transaction, is still
// returned.
func appendTouchEventsBestEffort(ctx context.Context, s *Store, tx *sql.Tx, recs []OutboxRecord) error {
	if _, err := tx.ExecContext(ctx, `SAVEPOINT outbox_touch`); err != nil {
		return err
	}
	if _, err := s.AppendOutbox(ctx, tx, recs...); err != nil {
		if _, rbErr := tx.ExecContext(ctx, `ROLLBACK TO SAVEPOINT outbox_touch`); rbErr != nil {
			return errors.Join(err, rbErr)
		}
		if _, relErr := tx.ExecContext(ctx, `RELEASE SAVEPOINT outbox_touch`); relErr != nil {
			return errors.Join(err, relErr)
		}
		outboxSkipped.Add(int64(len(recs)))
		slog.Default().ErrorContext(ctx, "touch recorded without outbox envelopes",
			"kind", outboxKindAudit, "action", "touch", "events", len(recs), "class", failureClass(err))
		return nil
	}
	_, err := tx.ExecContext(ctx, `RELEASE SAVEPOINT outbox_touch`)
	return err
}
