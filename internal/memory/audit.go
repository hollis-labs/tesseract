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
