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
// because they carry the `key` versus `memory_key` divergence, and the check
// earns the column back by asserting each door's behavior instead of its
// shape.
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
			{Concept: "consumer_state", MCP: "consumer_state", HTTP: "consumer_state"},

			{Concept: "author_agent_id", MCP: "author_agent_id", HTTP: "author.agent_id"},
			{
				Concept: "author_version", MCP: "author_version", HTTP: "author.agent_version",
				MCPAliases: []string{"author_agent_version"},
				Why: "MCP drops the second `agent` because the argument is already prefixed " +
					"`author_`; `author_agent_version` would stutter. The alias is the caller " +
					"who flattened the HTTP path mechanically and is right about the fact.",
			},

			{Concept: "summary", MCP: "payload_summary", HTTP: "payload.summary"},
			{Concept: "body", MCP: "payload_body", HTTP: "payload.body"},
			{Concept: "data", MCP: "payload_data", HTTP: "payload.data"},
			{Concept: "data_schema_hash", MCP: "payload_data_schema_hash", HTTP: "payload.data_schema_hash"},

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
			{Concept: "consumer_state", MCP: "consumer_state", HTTP: "consumer_state"},

			{Concept: "pointer_scheme", MCP: "pointer_scheme", HTTP: "pointer.scheme"},
			{Concept: "pointer_locator", MCP: "pointer_locator", HTTP: "pointer.locator"},
			{Concept: "pointer_resolved_at", MCP: "pointer_resolved_at", HTTP: "pointer.resolved_at"},

			{Concept: "author_agent_id", MCP: "author_agent_id", HTTP: "author.agent_id"},
			{
				Concept: "author_version", MCP: "author_version", HTTP: "author.agent_version",
				MCPAliases: []string{"author_agent_version"},
				Why:        "See memory.write; MCP shortens the same way on every door that takes an author.",
			},

			{
				Concept: "data", MCP: "payload_data", HTTP: "data",
				Pending: "CW-20260912-0089",
				Why: "MCP says `payload_data` where this door says `data`. CW-20260912-0064 " +
					"ruled MCP flat, so the argument becomes `data` and this row loses its " +
					"Pending. Until then it is the divergence that motivated the table: it " +
					"landed on 2026-09-12 and the literal hint map could not express it at " +
					"all, being flat-to-nested only.",
			},
			{
				Concept: "data_schema_hash", MCP: "payload_data_schema_hash", HTTP: "data_schema_hash",
				Pending: "CW-20260912-0089",
				Why:     "Travels with `data`.",
			},
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

			{Concept: "author_agent_id", MCP: "author_agent_id", HTTP: "author.agent_id"},
			{
				Concept: "author_version", MCP: "author_version", HTTP: "author.agent_version",
				MCPAliases: []string{"author_agent_version"},
				Why:        "See memory.write.",
			},

			{
				Concept: "data", MCP: "payload_data", HTTP: "data",
				Pending: "CW-20260912-0089",
				Why:     "See knowledge.write; the rename covers all three write tools at once.",
			},
			{
				Concept: "data_schema_hash", MCP: "payload_data_schema_hash", HTTP: "data_schema_hash",
				Pending: "CW-20260912-0089",
				Why:     "Travels with `data`.",
			},

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

	// ── Read doors (HTTP column declared, behavior asserted) ────────────
	//
	// One tool serves three arms, and it agrees with one of them. tesseract_get
	// and tesseract_history take `key`; /v1/context/* takes `key`;
	// /v1/memory/* and /v1/knowledge/* take `memory_key`. So the divergence is
	// two arms out of three, which is both smaller and more awkward than a
	// clean surface-versus-surface split — the knowledge door in particular
	// takes `key` when writing and `memory_key` when reading.

	{
		Name: "context.get", MCPTool: "tesseract_get",
		HTTPMethod: "GET", HTTPPath: "/v1/context/head",
		Fields: []Field{
			{Concept: "namespace", MCP: "namespace", HTTP: "namespace"},
			{Concept: "key", MCP: "key", HTTP: "key"},
		},
	},
	{
		Name: "memory.get", MCPTool: "tesseract_get",
		HTTPMethod: "GET", HTTPPath: "/v1/memory/current",
		Fields: []Field{
			{Concept: "namespace", MCP: "namespace", HTTP: "namespace"},
			{
				Concept: "key", MCP: "key", HTTP: "memory_key",
				Pending: "CW-20260912-0089",
				Why: "The read routes normalized across domains onto `memory_key` so one " +
					"identifier targets either store; the MCP cross-domain tool normalized " +
					"onto `key`. Both were reasonable alone. The result is that the same " +
					"concept has two names by door, and on knowledge also two names by " +
					"direction.",
			},
		},
	},
	{
		Name: "knowledge.get", MCPTool: "tesseract_get",
		HTTPMethod: "GET", HTTPPath: "/v1/knowledge/current",
		Fields: []Field{
			{Concept: "namespace", MCP: "namespace", HTTP: "namespace"},
			{
				Concept: "key", MCP: "key", HTTP: "memory_key",
				Pending: "CW-20260912-0089",
				Why: "See memory.get. This is the door where the split is visible from one " +
					"side: knowledge.write takes `key` and this takes `memory_key`.",
			},
		},
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
		Name: "memory.history", MCPTool: "tesseract_history",
		HTTPMethod: "GET", HTTPPath: "/v1/memory/history",
		Fields: []Field{
			{Concept: "namespace", MCP: "namespace", HTTP: "namespace"},
			{
				Concept: "key", MCP: "key", HTTP: "memory_key",
				Pending: "CW-20260912-0089",
				Why:     "See memory.get.",
			},
		},
	},
	{
		Name: "knowledge.history", MCPTool: "tesseract_history",
		HTTPMethod: "GET", HTTPPath: "/v1/knowledge/history",
		Fields: []Field{
			{Concept: "namespace", MCP: "namespace", HTTP: "namespace"},
			{
				Concept: "key", MCP: "key", HTTP: "memory_key",
				Pending: "CW-20260912-0089",
				Why:     "See knowledge.get.",
			},
		},
	},
}
