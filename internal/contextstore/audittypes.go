// Package contextstore — audit event type identifiers.
//
// These constants are the canonical names for the event_type field on
// AuditEvent rows. Every emit path in the codebase MUST use one of these —
// callers should not pass free-form strings.
//
// Note the intentional duplication between MCP and HTTP promote-stage names:
// both shapes exist in persisted audit data today, and consumers of the
// /v1/context/audit and context_audit_list surfaces rely on exact-match filters.
// Do not collapse them without a coordinated rename and consumer migration.
package contextstore

const (
	// Single-record mutations.
	EventWrite           = "write"
	EventTypedWrite      = "typed_write"
	EventStatusPromote   = "status_promote"
	EventStatusDeprecate = "status_deprecate"
	EventSessionSnapshot = "session_snapshot"

	// Packet (read-path with side effect).
	EventPacket = "packet"

	// Bulk/chunked ingest.
	EventBulkIngest    = "bulk_ingest"
	EventChunkedIngest = "chunked_ingest"

	// Promote stages — MCP naming.
	EventPromoteRequest = "promote.request"
	EventPromoteApprove = "promote.approve"
	EventPromote        = "promote"

	// Promote stages — HTTP naming (intentionally distinct; see file header).
	EventPromoteRequestCreated  = "promote.request.created"
	EventPromoteRequestApproved = "promote.request.approved"

	// Maintenance.
	EventMaintenanceTrim    = "maintenance.trim"
	EventMaintenanceCompact = "maintenance.compact"

	// Memory domain.
	EventMemoryWrite     = "memory.write"
	EventMemorySupersede = "memory.supersede"
	EventMemoryDeprecate = "memory.deprecate"
	EventMemoryPromote   = "memory.promote"

	// Knowledge domain.
	EventKnowledgeWrite     = "knowledge.write"
	EventKnowledgeSupersede = "knowledge.supersede"

	// Namespace registry. Emitted when a namespace_policies row is created,
	// either by an explicit context_namespace_register call or by the
	// auto-register / reconcile path (CW-20260428-0005).
	EventNamespaceRegister = "namespace.register"
	EventNamespaceUpdate   = "namespace.update"
)
