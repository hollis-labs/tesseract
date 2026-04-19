package contextstore

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"
)

// emit is the internal entry point all Emit* helpers funnel through.
// It normalizes CreatedAt, calls the unexported record path, and
// structured-logs any error before returning it. Callers that previously
// discarded the error via `_ =` can continue to do so; the log covers the
// observability gap without forcing every caller to grow error handling.
func (s *Store) emit(ctx context.Context, ev AuditEvent) error {
	if ev.CreatedAt == "" {
		ev.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	if err := s.recordAuditEvent(ctx, ev); err != nil {
		slog.Default().Warn("audit emit failed",
			"event_type", ev.EventType,
			"actor", ev.Actor,
			"namespace", ev.Namespace,
			"key", ev.Key,
			"revision", ev.Revision,
			"err", err,
		)
		return err
	}
	return nil
}

// EmitWrite records a "write" audit event for a context_write mutation.
func (s *Store) EmitWrite(ctx context.Context, actor, namespace, key string, revision int64, recordID string, metadata json.RawMessage) error {
	return s.emit(ctx, AuditEvent{
		EventType: EventWrite,
		Actor:     actor,
		Namespace: namespace,
		Key:       key,
		Revision:  revision,
		RecordID:  recordID,
		Metadata:  metadata,
	})
}

// EmitPromote records a promote-stage audit event. eventType MUST be one of
// EventPromoteRequest, EventPromoteApprove, EventPromote,
// EventPromoteRequestCreated, EventPromoteRequestApproved. The duplication
// preserves the MCP-vs-HTTP naming distinction in persisted audit data.
func (s *Store) EmitPromote(ctx context.Context, eventType, actor, namespace, key string, revision int64, recordID string, metadata json.RawMessage) error {
	switch eventType {
	case EventPromoteRequest, EventPromoteApprove, EventPromote,
		EventPromoteRequestCreated, EventPromoteRequestApproved:
	default:
		return fmt.Errorf("EmitPromote: unknown event type %q", eventType)
	}
	return s.emit(ctx, AuditEvent{
		EventType: eventType,
		Actor:     actor,
		Namespace: namespace,
		Key:       key,
		Revision:  revision,
		RecordID:  recordID,
		Metadata:  metadata,
	})
}

// EmitTypedWrite records a "typed_write" audit event.
func (s *Store) EmitTypedWrite(ctx context.Context, actor, namespace, key string, revision int64, recordID string, metadata json.RawMessage) error {
	return s.emit(ctx, AuditEvent{
		EventType: EventTypedWrite,
		Actor:     actor,
		Namespace: namespace,
		Key:       key,
		Revision:  revision,
		RecordID:  recordID,
		Metadata:  metadata,
	})
}

// EmitStatusPromote records a "status_promote" audit event.
func (s *Store) EmitStatusPromote(ctx context.Context, actor, namespace, key string, revision int64, recordID string, metadata json.RawMessage) error {
	return s.emit(ctx, AuditEvent{
		EventType: EventStatusPromote,
		Actor:     actor,
		Namespace: namespace,
		Key:       key,
		Revision:  revision,
		RecordID:  recordID,
		Metadata:  metadata,
	})
}

// EmitStatusDeprecate records a "status_deprecate" audit event.
func (s *Store) EmitStatusDeprecate(ctx context.Context, actor, namespace, key string, revision int64, recordID string, metadata json.RawMessage) error {
	return s.emit(ctx, AuditEvent{
		EventType: EventStatusDeprecate,
		Actor:     actor,
		Namespace: namespace,
		Key:       key,
		Revision:  revision,
		RecordID:  recordID,
		Metadata:  metadata,
	})
}

// EmitBulkIngest records a "bulk_ingest" audit event for one item in a bulk write.
func (s *Store) EmitBulkIngest(ctx context.Context, actor, namespace, key string, revision int64, recordID string, metadata json.RawMessage) error {
	return s.emit(ctx, AuditEvent{
		EventType: EventBulkIngest,
		Actor:     actor,
		Namespace: namespace,
		Key:       key,
		Revision:  revision,
		RecordID:  recordID,
		Metadata:  metadata,
	})
}

// EmitChunkedIngest records a "chunked_ingest" audit event for one chunk.
func (s *Store) EmitChunkedIngest(ctx context.Context, actor, namespace, key string, revision int64, recordID string, metadata json.RawMessage) error {
	return s.emit(ctx, AuditEvent{
		EventType: EventChunkedIngest,
		Actor:     actor,
		Namespace: namespace,
		Key:       key,
		Revision:  revision,
		RecordID:  recordID,
		Metadata:  metadata,
	})
}

// EmitSessionSnapshot records a "session_snapshot" audit event.
func (s *Store) EmitSessionSnapshot(ctx context.Context, actor, namespace, key string, revision int64, recordID string, metadata json.RawMessage) error {
	return s.emit(ctx, AuditEvent{
		EventType: EventSessionSnapshot,
		Actor:     actor,
		Namespace: namespace,
		Key:       key,
		Revision:  revision,
		RecordID:  recordID,
		Metadata:  metadata,
	})
}

// EmitPacket records a "packet" audit event. Packet events don't have a
// backing record; the store requires Revision > 0 and non-empty Key, so the
// helper synthesizes Revision=1 and expects callers to pass the request_id
// as key.
func (s *Store) EmitPacket(ctx context.Context, actor, namespace, key string, metadata json.RawMessage) error {
	return s.emit(ctx, AuditEvent{
		EventType: EventPacket,
		Actor:     actor,
		Namespace: namespace,
		Key:       key,
		Revision:  1,
		Metadata:  metadata,
	})
}

// EmitMaintenance records a maintenance event. eventType MUST be
// EventMaintenanceTrim or EventMaintenanceCompact. Maintenance operates on a
// whole namespace rather than a specific record; the helper synthesizes
// Key=namespace and Revision=1 to satisfy the store's validation.
func (s *Store) EmitMaintenance(ctx context.Context, eventType, actor, namespace string, metadata json.RawMessage) error {
	switch eventType {
	case EventMaintenanceTrim, EventMaintenanceCompact:
	default:
		return fmt.Errorf("EmitMaintenance: unknown event type %q", eventType)
	}
	return s.emit(ctx, AuditEvent{
		EventType: eventType,
		Actor:     actor,
		Namespace: namespace,
		Key:       namespace,
		Revision:  1,
		Metadata:  metadata,
	})
}
