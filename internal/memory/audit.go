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

// AuditSink receives audit events emitted by the memory and knowledge domains.
// Injected via Store.SetAuditSink. nil is a valid value (emits become no-ops).
//
// contextstore.Store satisfies this interface structurally via its Emit*
// methods added in CW-20260419-0040.
type AuditSink interface {
	EmitMemoryWrite(ctx context.Context, actor, namespace, key, recordID string, metadata json.RawMessage) error
	EmitMemorySupersede(ctx context.Context, actor, namespace, key, recordID string, metadata json.RawMessage) error
	EmitMemoryDeprecate(ctx context.Context, actor, namespace, key, recordID string, metadata json.RawMessage) error
	EmitMemoryPromote(ctx context.Context, actor, namespace, key, recordID string, metadata json.RawMessage) error
	EmitKnowledgeWrite(ctx context.Context, actor, namespace, key, recordID string, metadata json.RawMessage) error
	EmitKnowledgeSupersede(ctx context.Context, actor, namespace, key, recordID string, metadata json.RawMessage) error
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
