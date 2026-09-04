// Package contextstore — audit event type identifiers.
//
// These constants are the canonical names for the event_type field on
// AuditEvent rows. Every emit path in the codebase MUST use one of these —
// callers should not pass free-form strings.
//
// One name per logical action, across every surface. HTTP, MCP and CLI all
// emit the same event_type for the same promotion stage; an operator filtering
// context_audit for a stage sees every initiator of it, not one door's worth.
// A per-surface name is a bug, not a feature — see EmitPromote, which rejects
// anything outside the allowlist so a private spelling cannot be reintroduced
// at a call site.
//
// Until CW-20260419-0058 the promote request/approve stages carried two names
// each: the HTTP handlers emitted "promote.request.created" and
// "promote.request.approved" while MCP emitted "promote.request" and
// "promote.approve", and the CLI emitted nothing at all for those two stages.
// An earlier version of this header claimed both shapes existed in persisted
// data and warned against collapsing them without a consumer migration. That
// was checked and it was false: a scan of the live store found zero rows with
// any promote% event_type across 6,566 audit events. Nothing had ever been
// persisted under either spelling, so there was no data to rewrite and no
// consumer to migrate, and no migration ships with the rename.
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

	// Promote stages. One name per stage, shared by the HTTP, MCP and CLI
	// surfaces. Apply keeps the bare "promote" it has always had.
	//
	// CAUTION — the literals "promote.request" and "promote.approve" carry
	// three unrelated meanings in this codebase, and only this one is an audit
	// event type. Do not rename any of them by find-and-replace:
	//
	//  1. These constants: the audit event_type. Renaming is a schema change to
	//     the context_audit surface.
	//  2. The Type discriminator on the stored PromoteRequest / PromoteApproval
	//     payloads (see store.go). GetPromoteRequest and GetPromoteApproval
	//     match it exactly, so renaming it orphans every promotion record
	//     already on disk.
	//  3. Capability scope names (contextpolicy, defaultTokenScopes, and the
	//     requireScope / checkScope calls on both doors). Renaming it breaks
	//     authorization.
	EventPromoteRequest = "promote.request"
	EventPromoteApprove = "promote.approve"
	EventPromote        = "promote"

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
