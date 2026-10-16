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
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"
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
		typ, id := auditSubject(ev)
		subject := "msg://" + typ + "/" + outboxApp + "/" + id
		class := dropClassValidate
		if validateAuditEvent(ev) == nil {
			class = dropClassDB
		}
		// Precedence is mutation > audit > outbox (architect ruling
		// tesseract_in_mutation_audit_precedence_2026_10_16), except for
		// security-class audits, which fail CLOSED: the error is returned and
		// the caller's transaction rolls back, rejecting the mutation.
		if isSecurityClassAudit(ev.EventType) {
			slog.Default().ErrorContext(ctx, "security-class audit failed: mutation rejected",
				"kind", outboxKindAudit, "subject", subject, "event_type", ev.EventType, "class", class, "err", err)
			return fmt.Errorf("security audit %s not recorded (%s): %w", ev.EventType, class, err)
		}
		// Ordinary mutation: it commits, and a durable audit_dropped row in the
		// same tx says which event is missing so a repair pass can find it.
		if dErr := recordAuditDropped(ctx, tx, ev, class); dErr != nil {
			// The repair record could not be written either: the mutation still
			// commits (architect), and this ERROR plus the counter are the only
			// trace.
			slog.Default().ErrorContext(ctx, "audit_dropped repair record not written",
				"kind", outboxKindAudit, "subject", subject, "event_type", ev.EventType, "class", class)
		}
		auditDropped.Add(1)
		slog.Default().ErrorContext(ctx, "mutation committed without its audit event",
			"kind", outboxKindAudit, "subject", subject, "event_type", ev.EventType, "class", class, "err", err)
		return nil
	}
	_, err := tx.ExecContext(ctx, `RELEASE SAVEPOINT audit_in_mutation`)
	return err
}

// The bounded set of audit_dropped.class values. The table never holds error
// text, only one of these.
const (
	dropClassValidate = "validate" // the audit row was invalid
	dropClassDB       = "db"       // a database error confined to the audit insert
)

// securityClassAudits is the one explicit list of audit events that fail
// CLOSED: if they cannot be recorded, the mutation is rejected. Credential
// issue/rotate/revoke, token and grant changes, and namespace permission
// (register/update policy) changes. Everything else is ordinary: the mutation
// wins and the gap is recorded in audit_dropped.
var securityClassAudits = []string{
	"credential.",          // prefix: credential.issue, .rotate, .revoke, ...
	EventNamespaceRegister, // namespace permission/policy registration
	EventNamespaceUpdate,   // namespace permission/policy update
	"token.",               // token mint/revoke, should such events exist
	"grant.",               // grants and revokes, should such events exist
}

func isSecurityClassAudit(eventType string) bool {
	for _, p := range securityClassAudits {
		if eventType == p || (strings.HasSuffix(p, ".") && strings.HasPrefix(eventType, p)) {
			return true
		}
	}
	return false
}

func migrateAuditDropped(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS audit_dropped (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	entity_type TEXT NOT NULL,
	item_id TEXT NOT NULL,
	record_id TEXT NOT NULL DEFAULT '',
	action TEXT NOT NULL,
	class TEXT NOT NULL,
	created_at TEXT NOT NULL,
	repaired_at TEXT
)`)
	return err
}

// recordAuditDropped writes the repair record for an ordinary mutation that
// committed without its audit event: the entity type and its stable id (never
// key text), the action, the revision/record id, and a bounded class. No error
// text.
func recordAuditDropped(ctx context.Context, tx *sql.Tx, ev AuditEvent, class string) error {
	entityType, itemID := droppedEntity(ev)
	_, err := tx.ExecContext(ctx, `INSERT INTO audit_dropped (entity_type, item_id, record_id, action, class, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		entityType, itemID, ev.RecordID, ev.EventType, class, time.Now().UTC().Format(time.RFC3339))
	return err
}

// droppedEntity names what the dropped event was about by a stable id that
// never contains key text: the item id for memory-domain items, the derived
// opaque ctx_ id for context records, the namespace name for namespace events,
// the principal id for credential events.
func droppedEntity(ev AuditEvent) (entityType, itemID string) {
	action := ev.EventType
	switch {
	case strings.HasPrefix(action, "namespace."):
		return "namespace", ev.Namespace
	case strings.HasPrefix(action, "credential."):
		return "credential", ev.Key
	}
	for _, d := range []string{"memory", "knowledge", "workspace", "event"} {
		if strings.HasPrefix(action, d+".") {
			if ev.ItemID != "" {
				return d, ev.ItemID
			}
			return d, contextSubjectID(ev.Namespace, ev.Key)
		}
	}
	return "context", contextSubjectID(ev.Namespace, ev.Key)
}

// contextSubjectID is the stable, opaque id of a context record: "ctx_" + the
// first 32 hex characters of sha256(namespace || 0x00 || key) (architect ruling
// tesseract_context_record_subject_2026_10_16). Context records have no item
// identity (record_id is per revision, heads is keyed by namespace/key), and
// key text must never leave the machine. Stable across revisions, no migration.
//
// It is a hash, NOT a secret: anyone who can guess the namespace and key can
// recompute it. It hides the key from a reader; it does not authenticate.
func contextSubjectID(namespace, key string) string {
	sum := sha256.Sum256([]byte(namespace + "\x00" + key))
	return "ctx_" + hex.EncodeToString(sum[:])[:32]
}
