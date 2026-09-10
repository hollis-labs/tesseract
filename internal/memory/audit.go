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
