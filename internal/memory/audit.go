// Package memory — audit sink definition.
//
// The memory domain emits audit events through an AuditSink interface rather
// than depending on a concrete store. This inverts the dependency: memory
// defines what it needs; contextstore happens to satisfy it structurally.
// Nil sinks are valid — emits become no-ops — so test code can construct a
// memory.Store without an audit dependency.
package memory

import (
	"context"
	"database/sql"
	"encoding/json"
)

// Audit operations on a revision. The emitted event_type is "{domain}.{op}",
// so these are half of the audit vocabulary and the domain supplies the other
// half. The strings are wire vocabulary — "memory.write" and friends are
// filterable in the audit API, the web UI and the audit skill — so they are
// renamed only alongside those.
const (
	auditOpWrite     = "write"
	auditOpSupersede = "supersede"
	auditOpDeprecate = "deprecate"
	auditOpPromote   = "promote"
)

// AuditSink receives audit events emitted by the domains sharing the revision
// store. Injected via Store.SetAuditSink. nil is a valid value (emits become
// no-ops).
//
// One method, with the domain as an argument, replaced the six domain-named
// methods this interface used to declare (CW-20260909-0033). The old shape
// forced the write path to switch on domain to pick a method name, and that
// switch had a silent default: a domain with no arm emitted memory.write, so
// the audit log would report a memory write that never happened. A domain
// cannot be misfiled through this shape, because there is no name to choose.
//
// domain is a string rather than domains.Domain so contextstore keeps
// satisfying this structurally, without importing memory's domain model to
// name the parameter. The caller passes a validated Domain; the sink treats it
// as an opaque event-type prefix.
//
// contextstore.Store satisfies this interface structurally via EmitRevision.
type AuditSink interface {
	EmitRevision(ctx context.Context, domain, op, actor, namespace, key, recordID string, metadata json.RawMessage) error
}

// TxAuditSink is the transaction-aware half of an AuditSink. A sink that
// implements it records the audit event inside the caller's transaction, so the
// event commits with the revision it describes (CW-20261016-0140). itemID is
// the stable item id, used for the event subject. contextstore.Store satisfies
// this structurally via EmitRevisionTx.
//
// A sink that does not implement it keeps the post-commit EmitRevision path.
type TxAuditSink interface {
	EmitRevisionTx(ctx context.Context, tx *sql.Tx, domain, op, actor, namespace, key, recordID, itemID string, metadata json.RawMessage) error
}

// txAuditSink returns the transaction-aware sink, or nil.
func (s *Store) txAuditSink() TxAuditSink {
	if s.auditSink == nil {
		return nil
	}
	t, _ := s.auditSink.(TxAuditSink)
	return t
}

// AuditRevisionInTx records the audit event for a revision written through
// WriteRevisionInTx / WriteRevisionToItemInTx, inside the caller's transaction.
// Callers that own the transaction (the promotion store) call it before commit;
// FinishRevisionWrite then skips the post-commit emit. It reports whether the
// event was recorded in the transaction (false with a sink that is not
// transaction-aware, in which case FinishRevisionWrite still emits).
func (s *Store) AuditRevisionInTx(ctx context.Context, tx *sql.Tx, rev Revision, in WriteInput) bool {
	sink := s.txAuditSink()
	if sink == nil {
		return false
	}
	key := in.MemoryKey
	if key == "" {
		key = rev.MemoryID
	}
	op := auditOpWrite
	if in.Supersedes != "" {
		op = auditOpSupersede
	}
	_ = sink.EmitRevisionTx(ctx, tx, string(in.Domain), op, in.Author.AgentID, in.Namespace, key, rev.RevisionID, revisionItemID(rev), nil)
	return true
}

func revisionItemID(rev Revision) string {
	if rev.ItemID != "" {
		return rev.ItemID
	}
	return rev.MemoryID
}

// NamespaceRegistrar lets the memory write path keep namespace_policies in
// sync with actual data without taking a hard dependency on contextstore.
// contextstore.Store satisfies this structurally (CW-20260428-0005).
//
// Injected via Store.SetNamespaceRegistrar. nil is valid — calls become
// no-ops, which is the right default for unit tests that construct a bare
// memory.Store.
type NamespaceRegistrar interface {
	EnsureNamespaceRegistered(ctx context.Context, namespace string) error
}
