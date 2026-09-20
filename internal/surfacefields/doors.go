package surfacefields

// Doors is the table. One row per concept per door; a difference between the
// two surfaces carries either a Why that says it is meant to stay or a Pending
// that names the task removing it.
//
// # Scope, and why it stops where it does
//
// The write doors are Derived: their HTTP side decodes into a struct, so
// TestDoorsMatchTheSurfaces reads both columns off the code and fails when
// either surface grows a field this table does not carry. Those three are also
// where `workspace` (CW-20260912-0079) becomes a fourth, which is why this
// table lands first.
//
// The read doors are not derived, because their HTTP side reads query
// parameters by literal string and nothing reflects that. They are here anyway,
// because they carry the keyed-read contract. The check earns the column back
// by asserting each door's behavior instead of its shape.
//
// The remaining catalog pairs are deliberately absent rather than forgotten.
// This table covers the doors where a caller supplies the record's own fields
// and can therefore put one in the wrong place; the context_* operations over
// the legacy records store take an opaque payload and are on
// CW-20260909-0037's retirement path. Extending scope is adding rows, which is
// the point of the shape.
var Doors = []Door{
	// ── Write doors (both columns derived) ──────────────────────────────

	{
		Name:       "memory.write",
		MCPTool:    "memory_write",
		HTTPMethod: "POST",
		HTTPPath:   "/v1/memory/write",
		Derived:    true,
		Fields: []Field{
			{Concept: "namespace", MCP: "namespace", HTTP: "namespace"},
			{
				Concept: "key", MCP: "memory_key", HTTP: "memory_key",
			},
			{Concept: "supersedes", MCP: "supersedes", HTTP: "supersedes"},
			{Concept: "status", MCP: "status", HTTP: "status"},
			{Concept: "trigger", MCP: "trigger", HTTP: "trigger"},
			{Concept: "session_id", MCP: "session_id", HTTP: "session_id"},
			{Concept: "derived_from", MCP: "derived_from", HTTP: "derived_from"},
			{Concept: "confidence", MCP: "confidence", HTTP: "confidence"},
			{Concept: "tags", MCP: "tags", HTTP: "tags"},
			{Concept: "ttl_seconds", MCP: "ttl_seconds", HTTP: "ttl_seconds"},
			{Concept: "dedup", MCP: "dedup", HTTP: "dedup"},
			{Concept: "dedup_threshold", MCP: "dedup_threshold", HTTP: "dedup_threshold"},
			{Concept: "create_only", MCP: "create_only", HTTP: "create_only"},
			{Concept: "expected_revision_id", MCP: "expected_revision_id", HTTP: "expected_revision_id"},
			{Concept: "consumer_state", MCP: "consumer_state", HTTP: "consumer_state"},
			{Concept: "workstream_id", MCP: "workstream_id", HTTP: "workstream_id"},
			{Concept: "actor", MCP: "actor", HTTP: "actor"},

			{Concept: "author_agent_id", MCP: "author_agent_id", HTTP: "author.agent_id"},
			{
				Concept: "author_version", MCP: "author_version", HTTP: "author.agent_version",
				MCPAliases: []string{"author_agent_version"},
				Why: "MCP drops the second `agent` because the argument is already prefixed " +
					"`author_`; `author_agent_version` would stutter. The alias is the caller " +
					"who flattened the HTTP path mechanically and is right about the fact.",
			},

			{Concept: "summary", MCP: "payload_summary", HTTP: "summary", Why: "The initial S2 dispatch retains memory MCP payload_summary pending the naming decision; HTTP mirrors the flat library input."},
			{Concept: "body", MCP: "payload_body", HTTP: "body", Why: "See summary: memory MCP payload_body is retained by the initial S2 dispatch."},
			{Concept: "data", MCP: "data", HTTP: "data", MCPAliases: []string{"payload_data"}},
			{Concept: "data_schema_hash", MCP: "data_schema_hash", HTTP: "data_schema_hash", MCPAliases: []string{"payload_data_schema_hash"}},

			{
				Concept: "domain", HTTP: "domain",
				Why: "HTTP-only, and the route accepts it only to refuse the wrong value: a " +
					"body naming anything but `memory` gets `wrong_endpoint` rather than being " +
					"written to the wrong domain. memory_write is memory-only by construction, " +
					"so there is nothing for the argument to say.",
			},
			{
				Concept: "facets", HTTP: "facets",
				Why: "HTTP-only, and NOT ruled on. The same three facets — kind, source, " +
					"pointer — are reachable over both surfaces on knowledge_write, where MCP " +
					"takes them flat as `kind`, `source` and `pointer_*`. On memory they are " +
					"reachable over HTTP alone. Recorded as a row rather than fixed here " +
					"because adding arguments to a shipped tool is a capability decision, not " +
					"a placement one; raised out of CW-20260912-0088.",
			},
		},
	},

	{
		Name:       "knowledge.write",
		MCPTool:    "knowledge_write",
		HTTPMethod: "POST",
		HTTPPath:   "/v1/knowledge/write",
		Derived:    true,
		Fields: []Field{
			{Concept: "namespace", MCP: "namespace", HTTP: "namespace"},
			{Concept: "key", MCP: "key", HTTP: "key"},
			{Concept: "kind", MCP: "kind", HTTP: "kind"},
			{Concept: "source", MCP: "source", HTTP: "source"},
			{Concept: "summary", MCP: "summary", HTTP: "summary"},
			{Concept: "body", MCP: "body", HTTP: "body"},
			{Concept: "session_id", MCP: "session_id", HTTP: "session_id"},
			{Concept: "tags", MCP: "tags", HTTP: "tags"},
			{Concept: "ttl_seconds", MCP: "ttl_seconds", HTTP: "ttl_seconds"},
			{Concept: "confidence", MCP: "confidence", HTTP: "confidence"},
			{Concept: "supersedes", MCP: "supersedes", HTTP: "supersedes"},
			{Concept: "status", MCP: "status", HTTP: "status"},
			{Concept: "derived_from", MCP: "derived_from", HTTP: "derived_from"},
			{Concept: "create_only", MCP: "create_only", HTTP: "create_only"},
			{Concept: "expected_revision_id", MCP: "expected_revision_id", HTTP: "expected_revision_id"},
			{Concept: "consumer_state", MCP: "consumer_state", HTTP: "consumer_state"},
			{Concept: "workstream_id", MCP: "workstream_id", HTTP: "workstream_id"},
			{Concept: "actor", MCP: "actor", HTTP: "actor"},

			{Concept: "pointer_scheme", MCP: "pointer_scheme", HTTP: "pointer.scheme"},
			{Concept: "pointer_locator", MCP: "pointer_locator", HTTP: "pointer.locator"},
			{Concept: "pointer_resolved_at", MCP: "pointer_resolved_at", HTTP: "pointer.resolved_at"},

			{Concept: "author_agent_id", MCP: "author_agent_id", HTTP: "author.agent_id"},
			{
				Concept: "author_version", MCP: "author_version", HTTP: "author.agent_version",
				MCPAliases: []string{"author_agent_version"},
				Why:        "See memory.write; MCP shortens the same way on every door that takes an author.",
			},

			{Concept: "data", MCP: "data", HTTP: "data", MCPAliases: []string{"payload_data"}},
			{Concept: "data_schema_hash", MCP: "data_schema_hash", HTTP: "data_schema_hash", MCPAliases: []string{"payload_data_schema_hash"}},
		},
	},

	{
		Name:       "event.write",
		MCPTool:    "event_write",
		HTTPMethod: "POST",
		HTTPPath:   "/v1/event/write",
		Derived:    true,
		Fields: []Field{
			{Concept: "namespace", MCP: "namespace", HTTP: "namespace"},
			{Concept: "key", MCP: "key", HTTP: "key"},
			{Concept: "summary", MCP: "summary", HTTP: "summary"},
			{Concept: "body", MCP: "body", HTTP: "body"},
			{Concept: "session_id", MCP: "session_id", HTTP: "session_id"},
			{Concept: "tags", MCP: "tags", HTTP: "tags"},
			{Concept: "ttl_seconds", MCP: "ttl_seconds", HTTP: "ttl_seconds"},
			{Concept: "confidence", MCP: "confidence", HTTP: "confidence"},
			{Concept: "supersedes", MCP: "supersedes", HTTP: "supersedes"},
			{Concept: "consumer_state", MCP: "consumer_state", HTTP: "consumer_state"},
			{Concept: "workstream_id", MCP: "workstream_id", HTTP: "workstream_id"},

			{Concept: "author_agent_id", MCP: "author_agent_id", HTTP: "author.agent_id"},
			{
				Concept: "author_version", MCP: "author_version", HTTP: "author.agent_version",
				MCPAliases: []string{"author_agent_version"},
				Why:        "See memory.write.",
			},

			{Concept: "data", MCP: "data", HTTP: "data", MCPAliases: []string{"payload_data"}},
			{Concept: "data_schema_hash", MCP: "data_schema_hash", HTTP: "data_schema_hash", MCPAliases: []string{"payload_data_schema_hash"}},

			{
				Concept: "derived_from", HTTP: "derived_from",
				Why: "HTTP-only, and NOT ruled on. event.Store.Write defaults an empty value " +
					"to `observation`, so an MCP writer never chooses and never knows it did " +
					"not — and `derived_from` is a recall ranking multiplier, not a label. " +
					"AGENTS.md already names this shape as the reason " +
					"memory.WriteInput.Domain stopped defaulting: a default is " +
					"indistinguishable from a choice in the audit log. Raised out of " +
					"CW-20260912-0088.",
			},
			{
				Concept: "trigger", HTTP: "trigger",
				Why: "HTTP-only, and NOT ruled on. Defaults to `manual` in the store, with " +
					"the same consequence as derived_from above. Both are required arguments " +
					"on memory_write, which is what makes their absence here look like an " +
					"oversight rather than a decision — but only the absence is established.",
			},
		},
	},
	{
		Name: "workspace.write", MCPTool: "workspace_write",
		HTTPMethod: "POST", HTTPPath: "/v1/workspace/write", Derived: true,
		Fields: []Field{
			{Concept: "namespace", MCP: "namespace", HTTP: "namespace"},
			{Concept: "item_id", MCP: "item_id", HTTP: "item_id"},
			{Concept: "version_token", MCP: "version_token", HTTP: "version_token"},
			{Concept: "idempotency_key", MCP: "idempotency_key", HTTP: "idempotency_key"},
			{Concept: "key", MCP: "key", HTTP: "key"},
			{Concept: "summary", MCP: "summary", HTTP: "summary"},
			{Concept: "body", MCP: "body", HTTP: "body"},
			{Concept: "data", MCP: "data", HTTP: "data"},
			{Concept: "data_schema_hash", MCP: "data_schema_hash", HTTP: "data_schema_hash"},
			{Concept: "tags", MCP: "tags", HTTP: "tags"},
			{Concept: "consumer_state", MCP: "consumer_state", HTTP: "consumer_state"},
			{Concept: "workstream_id", MCP: "workstream_id", HTTP: "workstream_id"},
			{Concept: "clear_fields", MCP: "clear_fields", HTTP: "clear_fields"},
			{Concept: "author_agent_id", MCP: "author_agent_id", HTTP: "author.agent_id"},
			{Concept: "author_version", MCP: "author_version", HTTP: "author.agent_version", Why: "MCP flattens and shortens the structured HTTP author field consistently with the other write doors."},
			{Concept: "session_id", MCP: "session_id", HTTP: "session_id"},
		},
	},
	{
		Name: "workspace.delete", MCPTool: "workspace_delete",
		HTTPMethod: "POST", HTTPPath: "/v1/workspace/delete", Derived: true,
		Fields: []Field{
			{Concept: "item_id", MCP: "item_id", HTTP: "item_id"},
			{Concept: "version_token", MCP: "version_token", HTTP: "version_token"},
		},
	},
	{
		Name: "workspace.promote", MCPTool: "workspace_promote",
		HTTPMethod: "POST", HTTPPath: "/v1/workspace/promote/request", Derived: true,
		Fields: []Field{
			{Concept: "stage", MCP: "stage", Why: "MCP selects request, approve, or apply; HTTP uses one route per stage."},
			{Concept: "source_item_id", MCP: "source_item_id", HTTP: "source_item_id"},
			{Concept: "source_version_token", MCP: "source_version_token", HTTP: "source_version_token"},
			{Concept: "request_id", MCP: "request_id", HTTP: "request_id"},
			{Concept: "actor", MCP: "actor", HTTP: "actor"},
			{Concept: "reason", MCP: "reason", HTTP: "reason"},
			{Concept: "notes", MCP: "notes", HTTP: "notes"},
			{Concept: "target_domain", MCP: "target_domain", HTTP: "target.domain"},
			{Concept: "target_namespace", MCP: "target_namespace", HTTP: "target.namespace"},
			{Concept: "target_key", MCP: "target_key", HTTP: "target.key"},
			{Concept: "target_item_id", MCP: "target_item_id", HTTP: "target.item_id"},
			{Concept: "expected_target_revision_id", MCP: "expected_target_revision_id", HTTP: "target.expected_revision_id", Why: "The HTTP target object already supplies target context; MCP retains the explicit target prefix on its flat field."},
			{Concept: "target_author_agent_id", MCP: "target_author_agent_id", HTTP: "target.author.agent_id"},
			{Concept: "target_author_version", MCP: "target_author_version", HTTP: "target.author.agent_version", Why: "MCP follows the existing flat author_version spelling while HTTP retains the Author object field."},
			{Concept: "target_session_id", MCP: "target_session_id", HTTP: "target.session_id"},
			{Concept: "target_tags", MCP: "target_tags", HTTP: "target.tags"},
			{Concept: "target_consumer_state", MCP: "target_consumer_state", HTTP: "target.consumer_state"},
			{Concept: "target_confidence", MCP: "target_confidence", HTTP: "target.confidence"},
			{Concept: "target_ttl_seconds", MCP: "target_ttl_seconds", HTTP: "target.ttl_seconds"},
			{Concept: "target_data_schema_hash", MCP: "target_data_schema_hash", HTTP: "target.data_schema_hash"},
			{Concept: "target_workstream_id", MCP: "target_workstream_id", HTTP: "target.workstream_id"},
			{Concept: "target_status", MCP: "target_status", HTTP: "target.status"},
			{Concept: "target_trigger", MCP: "target_trigger", HTTP: "target.trigger"},
			{Concept: "target_derived_from", MCP: "target_derived_from", HTTP: "target.derived_from"},
			{Concept: "target_kind", MCP: "target_kind", HTTP: "target.kind"},
			{Concept: "target_source", MCP: "target_source", HTTP: "target.source"},
			{Concept: "target_pointer_scheme", MCP: "target_pointer_scheme", HTTP: "target.pointer.scheme"},
			{Concept: "target_pointer_locator", MCP: "target_pointer_locator", HTTP: "target.pointer.locator"},
			{Concept: "target_pointer_resolved_at", MCP: "target_pointer_resolved_at", HTTP: "target.pointer.resolved_at"},
		},
	},
	{
		Name: "reference.resolve", MCPTool: "tesseract_ref_resolve",
		HTTPMethod: "POST", HTTPPath: "/v1/refs/resolve", Derived: true,
		Fields: []Field{
			{Concept: "item_id", MCP: "item_id", HTTP: "item_id"},
			{Concept: "revision_id", MCP: "revision_id", HTTP: "revision_id"},
			{Concept: "domain", MCP: "domain", HTTP: "domain"},
			{Concept: "namespace", MCP: "namespace", HTTP: "namespace"},
			{Concept: "key", MCP: "key", HTTP: "key"},
			{Concept: "uri", MCP: "uri", HTTP: "uri"},
		},
	},

	// ── Read doors (HTTP column declared, behavior asserted) ────────────
	//
	// tesseract_get, tesseract_history and all their HTTP peers take key.
	// Stored revisions and read responses still name the field memory_key.

	{
		Name: "context.get", MCPTool: "tesseract_get",
		HTTPMethod: "GET", HTTPPath: "/v1/context/head",
		Fields: []Field{
			{Concept: "namespace", MCP: "namespace", HTTP: "namespace"},
			{Concept: "key", MCP: "key", HTTP: "key"},
		},
	},
	{
		Name: "item.get", MCPTool: "tesseract_get",
		HTTPMethod: "GET", HTTPPath: "/v1/items/{item_id}",
		Fields: []Field{
			{Concept: "item_id", MCP: "item_id", HTTP: "item_id"},
		},
	},
	{
		Name: "memory.get", MCPTool: "tesseract_get",
		HTTPMethod: "GET", HTTPPath: "/v1/memory/current",
		Fields: []Field{
			{Concept: "namespace", MCP: "namespace", HTTP: "namespace"},
			{Concept: "key", MCP: "key", HTTP: "key"},
		},
	},
	{
		Name: "knowledge.get", MCPTool: "tesseract_get",
		HTTPMethod: "GET", HTTPPath: "/v1/knowledge/current",
		Fields: []Field{
			{Concept: "namespace", MCP: "namespace", HTTP: "namespace"},
			{Concept: "key", MCP: "key", HTTP: "key"},
		},
	},
	{
		Name: "workspace.get", MCPTool: "tesseract_get",
		HTTPMethod: "GET", HTTPPath: "/v1/workspace/current",
		Fields: []Field{{Concept: "namespace", MCP: "namespace", HTTP: "namespace"}, {Concept: "key", MCP: "key", HTTP: "key"}},
	},
	{
		Name: "context.history", MCPTool: "tesseract_history",
		HTTPMethod: "GET", HTTPPath: "/v1/context/history",
		Fields: []Field{
			{Concept: "namespace", MCP: "namespace", HTTP: "namespace"},
			{Concept: "key", MCP: "key", HTTP: "key"},
		},
	},
	{
		Name: "item.history", MCPTool: "tesseract_history",
		HTTPMethod: "GET", HTTPPath: "/v1/items/{item_id}/history",
		Fields: []Field{
			{Concept: "item_id", MCP: "item_id", HTTP: "item_id"},
		},
	},
	{
		Name: "memory.history", MCPTool: "tesseract_history",
		HTTPMethod: "GET", HTTPPath: "/v1/memory/history",
		Fields: []Field{
			{Concept: "namespace", MCP: "namespace", HTTP: "namespace"},
			{Concept: "key", MCP: "key", HTTP: "key"},
		},
	},
	{
		Name: "knowledge.history", MCPTool: "tesseract_history",
		HTTPMethod: "GET", HTTPPath: "/v1/knowledge/history",
		Fields: []Field{
			{Concept: "namespace", MCP: "namespace", HTTP: "namespace"},
			{Concept: "key", MCP: "key", HTTP: "key"},
		},
	},
}
