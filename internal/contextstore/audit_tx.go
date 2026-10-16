package contextstore

// Audit inside the mutation's own transaction (CW-20261016-0140, O1 step 2b).
//
// A mutation that carries an AuditSpec writes its audit_events row and outbox
// envelope in the SAME transaction as the record itself, so a crash between the
// mutation and its audit can no longer lose one without the other.
//
// Recording an event never rejects a mutation that is otherwise valid (architect
// ruling): the whole audit+outbox write runs under a savepoint, and a failure
// rolls back to it, logs at ERROR, counts (AuditDropped) and lets the mutation
// commit. A failure of the transaction itself loses mutation, audit and outbox
// together.

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
)

// AuditSpec describes the audit event a mutation records about itself.
// Namespace, key, revision and record id come from the mutation; the caller
// supplies only what the store cannot know.
type AuditSpec struct {
	// EventType is one of the Event* constants (write, typed_write,
	// promote.request, ...). Promote stages are restricted to the same three
	// names EmitPromote allows.
	EventType string
	// Actor is the claimed actor string for the audit row; empty uses the
	// mutation's actor.
	Actor    string
	Metadata json.RawMessage
	// MetadataFor, when set, builds the metadata from the identifiers the
	// mutation assigns (the new record id and revision). It overrides Metadata.
	MetadataFor func(recordID string, revision int64) json.RawMessage
}

// auditDropped counts mutations that committed without their audit row because
// the audit write failed and was rolled back to its savepoint.
var auditDropped atomic.Int64

// AuditDropped returns how many mutations committed without an audit row since
// start. Alongside OutboxSkipped it is the signal a gap exists.
func AuditDropped() int64 { return auditDropped.Load() }

func (a *AuditSpec) validate() error {
	if a == nil {
		return nil
	}
	if strings.TrimSpace(a.EventType) == "" {
		return fmt.Errorf("audit spec: event_type required")
	}
	if strings.HasPrefix(a.EventType, "promote") {
		switch a.EventType {
		case EventPromoteRequest, EventPromoteApprove, EventPromote:
		default:
			return fmt.Errorf("audit spec: unknown promote event type %q", a.EventType)
		}
	}
	return nil
}

// auditWithinMutation records the mutation's audit event inside tx. It returns
// an error only if the savepoint machinery itself fails; the audit write's own
// failure is absorbed as described above.
func auditWithinMutation(ctx context.Context, tx *sql.Tx, spec *AuditSpec, namespace, key string, revision int64, recordID, mutationActor, createdAt string) error {
	if spec == nil {
		return nil
	}
	actor := spec.Actor
	if strings.TrimSpace(actor) == "" {
		actor = mutationActor
	}
	metadata := spec.Metadata
	if spec.MetadataFor != nil {
		metadata = spec.MetadataFor(recordID, revision)
	}
	ev := AuditEvent{
		EventType: spec.EventType,
		Actor:     actor,
		Namespace: namespace,
		Key:       key,
		Revision:  revision,
		RecordID:  recordID,
		CreatedAt: createdAt,
		Metadata:  metadata,
	}
	return auditEventWithinTx(ctx, tx, ev)
}

// auditEventWithinTx records ev (audit row + outbox envelope) inside tx under a
// savepoint, absorbing a failure of the audit write so it never rejects the
// caller's mutation. See the file comment.
func auditEventWithinTx(ctx context.Context, tx *sql.Tx, ev AuditEvent) error {
	if _, err := tx.ExecContext(ctx, `SAVEPOINT audit_in_mutation`); err != nil {
		return err
	}
	if err := recordAuditWithOutbox(ctx, tx, ev); err != nil {
		if _, rbErr := tx.ExecContext(ctx, `ROLLBACK TO SAVEPOINT audit_in_mutation`); rbErr != nil {
			return rbErr
		}
		if _, relErr := tx.ExecContext(ctx, `RELEASE SAVEPOINT audit_in_mutation`); relErr != nil {
			return relErr
		}
		auditDropped.Add(1)
		typ, id := auditSubject(ev)
		slog.Default().ErrorContext(ctx, "mutation committed without its audit event",
			"kind", outboxKindAudit,
			"subject", "msg://"+typ+"/"+outboxApp+"/"+id,
			"event_type", ev.EventType, "err", err)
		return nil
	}
	_, err := tx.ExecContext(ctx, `RELEASE SAVEPOINT audit_in_mutation`)
	return err
}
