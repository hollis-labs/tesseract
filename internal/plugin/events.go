package plugin

import (
	"time"

	"github.com/hollis-labs/plugin-sdk"
)

// Conduit Event Catalog
// These are the standard events that Conduit emits for plugins to listen to.
const (
	// Context lifecycle events
	EventContextWritten  = "context.written"
	EventContextPromoted = "context.promoted"
	EventContextDeleted  = "context.deleted"

	// Embedding events
	EventEmbedCompleted = "embed.completed"
)

// NewEvent creates a new plugin event with the given type, source, and data.
func NewEvent(eventType, source string, data map[string]interface{}) plugin.Event {
	return plugin.Event{
		Type:      eventType,
		Source:    source,
		Timestamp: time.Now(),
		Data:      data,
	}
}

// EmitContextWritten emits a context.written event.
func (h *Host) EmitContextWritten(namespace, key string) {
	h.EmitEvent(NewEvent(EventContextWritten, "conduit", map[string]interface{}{
		"namespace": namespace,
		"key":       key,
	}))
}

// EmitContextPromoted emits a context.promoted event.
func (h *Host) EmitContextPromoted(namespace, key, fromStatus, toStatus string) {
	h.EmitEvent(NewEvent(EventContextPromoted, "conduit", map[string]interface{}{
		"namespace":   namespace,
		"key":         key,
		"from_status": fromStatus,
		"to_status":   toStatus,
	}))
}

// EmitEmbedCompleted emits an embed.completed event.
func (h *Host) EmitEmbedCompleted(namespace, key string, chunkCount int) {
	h.EmitEvent(NewEvent(EventEmbedCompleted, "conduit", map[string]interface{}{
		"namespace":   namespace,
		"key":         key,
		"chunk_count": chunkCount,
	}))
}

// EmitContextDeleted emits a context.deleted event.
func (h *Host) EmitContextDeleted(namespace, key string) {
	h.EmitEvent(NewEvent(EventContextDeleted, "conduit", map[string]interface{}{
		"namespace": namespace,
		"key":       key,
	}))
}
