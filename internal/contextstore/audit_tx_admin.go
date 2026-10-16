package contextstore

// Namespace-registry and maintenance events recorded in the mutation's own
// transaction (CW-20261016-0140, step 2b follow-up). Same failure policy as
// audit_tx.go: the audit write runs under a savepoint and never rejects the
// mutation.

import (
	"context"
	"database/sql"
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

// UpsertNamespacePolicyAudited is UpsertNamespacePolicy plus the namespace
// register/update audit event, written in the same transaction. exists selects
// namespace.update over namespace.register; metadata is the event metadata.
func (s *Store) UpsertNamespacePolicyAudited(ctx context.Context, entry NamespacePolicyEntry, exists bool, metadata json.RawMessage) (err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	if err = upsertNamespacePolicy(ctx, tx, entry); err != nil {
		return err
	}
	eventType := EventNamespaceRegister
	if exists {
		eventType = EventNamespaceUpdate
	}
	// The CALLER'S ctx, not asInternalPath: this is the admin HTTP path, so a
	// verified principal is the actor and an unauthenticated caller gets no
	// actor (its "system" claim stays in claimed_actor). Only genuinely internal
	// paths (seeding, inferred registration) may mark themselves internal.
	if err = auditEventWithinTx(ctx, tx, AuditEvent{
		EventType: eventType,
		Actor:     "system",
		Namespace: entry.Namespace,
		Key:       entry.Namespace,
		Revision:  1,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		Metadata:  metadata,
	}); err != nil {
		return err
	}
	return tx.Commit()
}

// registerNamespaceAudited inserts a registry row for an inferred namespace and
// its namespace.register event in one transaction. It reports whether it wrote
// the row.
func (s *Store) registerNamespaceAudited(ctx context.Context, namespace, source string) (inserted bool, err error) {
	ns := strings.TrimSpace(namespace)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() {
		if err != nil || !inserted {
			_ = tx.Rollback()
		}
	}()
	inserted, err = insertNamespaceRegistration(ctx, tx, ns, source)
	if err != nil || !inserted {
		return false, err
	}
	ownerType, ownerID := lookupOrDeriveOwner(ctx, tx, ns)
	meta, mErr := json.Marshal(map[string]any{"source": source, "owner_type": ownerType, "owner_id": ownerID})
	if mErr == nil {
		if err = auditEventWithinTx(asInternalPath(ctx), tx, AuditEvent{
			EventType: EventNamespaceRegister,
			Actor:     "system",
			Namespace: ns,
			Key:       ns,
			Revision:  1,
			CreatedAt: time.Now().UTC().Format(time.RFC3339),
			Metadata:  meta,
			// Inferred auto-registration is bookkeeping, not a permission change:
			// it writes {"source":"inferred"} with an owner DERIVED from the
			// namespace name (or its registered scope head), assigns no grants,
			// and the policy engine does not treat inferred rows as declared
			// policy. So an audit outage must not block first writes: ordinary
			// (audit_dropped), unlike admin register/update, which fail closed.
			Bookkeeping: true,
		}); err != nil {
			return false, err
		}
	}
	return true, tx.Commit()
}

// MaintenanceAudit asks TrimRecords / CompactNamespace to record their
// maintenance event: inside the deleting transaction when they delete, and as a
// standalone event when they delete nothing (dry run or no match).
type MaintenanceAudit struct {
	EventType string // EventMaintenanceTrim or EventMaintenanceCompact
	Actor     string
	Pattern   string
	DryRun    bool
	// OnlyWhenAffected skips the standalone event when nothing was deleted (the
	// CLI never recorded dry runs or empty runs).
	OnlyWhenAffected bool
}

type maintenanceAuditKey struct{}

// WithMaintenanceAudit attaches a maintenance audit request to ctx.
func WithMaintenanceAudit(ctx context.Context, m MaintenanceAudit) context.Context {
	return context.WithValue(ctx, maintenanceAuditKey{}, m)
}

func maintenanceAuditFrom(ctx context.Context) (MaintenanceAudit, bool) {
	m, ok := ctx.Value(maintenanceAuditKey{}).(MaintenanceAudit)
	return m, ok
}

// finishMaintenanceAudit records the requested maintenance event with the
// number of records affected. tx is the deleting transaction, or nil when
// nothing was deleted (the event is then a standalone emit).
func (s *Store) finishMaintenanceAudit(ctx context.Context, tx *sql.Tx, affected int64) error {
	m, ok := maintenanceAuditFrom(ctx)
	if !ok {
		return nil
	}
	metadata := json.RawMessage(`{"records_affected":` + strconv.FormatInt(affected, 10) + `,"dry_run":` + strconv.FormatBool(m.DryRun) + `}`)
	if tx == nil {
		if m.OnlyWhenAffected {
			return nil
		}
		_ = s.EmitMaintenance(ctx, m.EventType, m.Actor, m.Pattern, metadata)
		return nil
	}
	return auditEventWithinTx(ctx, tx, AuditEvent{
		EventType: m.EventType,
		Actor:     m.Actor,
		Namespace: m.Pattern,
		Key:       m.Pattern,
		Revision:  1,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		Metadata:  metadata,
	})
}
