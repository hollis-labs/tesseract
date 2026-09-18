package mcpadapter

import (
	"context"
	"errors"
	"time"

	"github.com/hollis-labs/tesseract/internal/contextpolicy"
	"github.com/hollis-labs/tesseract/internal/knowledge"
	"github.com/hollis-labs/tesseract/internal/memory"
)

// domainBoundaryLine is the memory/knowledge fork, in the shortest form that
// decides a write. It is ONE string used by both write tools on purpose: the
// full statement lives in skills/start-here.md, and this is the only place the
// short form exists, so the two tools cannot drift apart from each other or
// from the skill.
//
// It replaces "agent-authored content with no external source — use
// memory_write", which was false for every populated knowledge kind that has an
// agent as its author: `investigation`, `session_close` and `project_canonical`
// are all agent-written with nothing outside Tesseract to point at. DerivedFrom
// never decided this; how the content gets found again does. See
// docs/knowledge-memory-boundary.md for the corpus test behind that.
const domainBoundaryLine = "• **Which domain:** **knowledge is content you go TO** — addressed by key, read whole, expected to stay true. " +
	"**Memory is content that comes TO you** — recall surfaces it while you are working on something nearby, dated, superseded rather than edited. " +
	"DerivedFrom does not decide this: both domains are mostly agent-authored, and a knowledge entry with no external source is normal (`pointer_scheme: \"nil\"`). " +
	"**\"I might look this up later\" is not the test** — nearly all memory is looked up eventually. The test is whether you could NAME it before you went looking. " +
	"Full statement, with what it does not cover: `tesseract_skills start-here`.\n"

func (a *Adapter) registerKnowledgeTools(s *toolRegistrar) {
	a.addTool(s, gomcpTool("knowledge_write",
		"**Write a knowledge revision** — reference content a later session will go looking for by name.\n"+
			"• **Read this first:** call `tesseract_skills knowledge` before composing a body. It carries the canonical request shape as a copy-pasteable payload on both this surface and HTTP (which nests `pointer` and `author` where this one takes them flat), and states what belongs in `body` versus `pointer_locator` — the single decision that determines whether the entry still carries anything once the pointer rots.\n"+
			"• **Kind of content:** records carrying `kind`/`source`/`pointer` facets. `kind` is a closed vocabulary — see the `kind` parameter.\n"+
			"• **Scope:** `memory:write`.\n"+
			domainBoundaryLine+
			"• **Use this when:** a project's canonical, an ADR, a playbook or template, an investigation dossier, a doc or package reference — something someone will come back for deliberately. Whether it points at anything outside Tesseract is a separate question: `pointer_scheme: \"nil\"` is a first-class answer.\n"+
			"• **Don't use this for:** content nobody would know to ask for — a decision and its rationale, a limitation, a deferred follow-up, what a session learned. Recall is how those get found, so they are `memory_write`. Generic records — use `context_write`.\n"+
			"• **Deeper:** `tesseract_skills facets-and-kinds` for facet vocabulary.",
		inputSchema(
			strProp("namespace", "Knowledge namespace; must contain a 'knowledge' segment (e.g. project/tesseract/knowledge/framework)", true),
			strProp("key", "Optional logical key (slug, path, id) — same key on re-write creates a new revision. "+
				"Free-form: knowledge keys are NOT held to the memory domain's lowercase dot-notation rule, so hyphens, slashes and mixed case from an external source are accepted as written.", false),
			strProp("workstream_id", "Optional opaque workstream association. Omit to preserve; send an empty string to clear.", false),
			// The allowed set is rendered from the enforced vocabulary rather than
			// restated, so this description cannot advertise a set the write path
			// does not accept.
			strProp("kind",
				"Facet: the kind of entry. Closed vocabulary — any other value is rejected. Allowed: "+
					memory.KnowledgeKindList(), true),
			strProp("source", "Facet: where this knowledge came from (e.g. filesystem, obsidian, nil, web, manual)", true),
			strProp("pointer_scheme", "Pointer scheme (e.g. file, http, https, obsidian, nil)", true),
			strProp("pointer_locator", "Pointer locator: scheme-specific address (path, URL, vault id, ...)", true),
			strProp("pointer_resolved_at", "Optional RFC3339 timestamp for when the pointer was last verified. Defaults to now.", false),
			strProp("summary", "Short summary text (feeds embeddings)", true),
			strProp("body", "Optional longer body (feeds embeddings when present)", false),
			strProp("author_agent_id", "Agent ID of the writer", true),
			strProp("author_version", "Agent version string", false),
			strProp("session_id", "Session identifier", true),
			strProp("consumer_state", consumerStateArgDescription, false),
			strProp("data", payloadDataArgDescription, false),
			strProp("data_schema_hash", payloadDataSchemaHashArgDescription, false),
			strProp("tags", "Optional JSON array of string tags", false),
			numProp("ttl_seconds", "Optional TTL in seconds (0 = no expiry)", false),
			numProp("confidence", "Confidence score in [0, 1.0] (default 0.9)", false),
			strProp("supersedes", "Optional revision_id this entry supersedes", false),
			strProp("actor", "Actor asserting the write (default: agent). Writing to user/ namespaces requires actor=user.", false),
		),
		toolAnnotations{},
		a.handleKnowledgeWrite,
	))
}

func (a *Adapter) handleKnowledgeWrite(ctx context.Context, req map[string]any) (any, error) {
	if res, _ := a.checkScope(ctx, "memory:write"); res != nil {
		return res, nil
	}

	tags, _, err := parseStringArrayArg(req, "tags")
	if err != nil {
		return toolError(codeValidationError, "tags "+err.Error()), nil //nolint:nilerr // MCP tool pattern
	}

	pointer := memory.Pointer{
		Scheme:  argString(req, "pointer_scheme", ""),
		Locator: argString(req, "pointer_locator", ""),
	}
	if raw := argString(req, "pointer_resolved_at", ""); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return toolError(codeValidationError, "pointer_resolved_at must be RFC3339: "+err.Error()), nil //nolint:nilerr // MCP tool pattern
		}
		pointer.ResolvedAt = &t
	}

	ttlSeconds := int64(argFloat(req, "ttl_seconds", 0))
	workstreamID, workstreamErr := workstreamWriteArg(req)
	if workstreamErr != nil {
		return workstreamErr, nil
	}

	actor := argString(req, "actor", "")
	if actor == "" {
		actor = "agent"
	}
	ns := argString(req, "namespace", "")
	if policyErr := a.policy().CanWrite("", actor, ns); policyErr != nil {
		return toolError(codeNamespaceNotPermitted, policyErr.Error()), nil
	}

	in := knowledge.WriteInput{
		Namespace:      ns,
		Actor:          actor,
		Key:            argString(req, "key", ""),
		WorkstreamID:   workstreamID,
		Kind:           argString(req, "kind", ""),
		Source:         argString(req, "source", ""),
		Pointer:        pointer,
		Summary:        argString(req, "summary", ""),
		Body:           argString(req, "body", ""),
		Data:           payloadDataArg(req),
		DataSchemaHash: argString(req, "data_schema_hash", ""),
		Author: memory.Author{
			AgentID:      argString(req, "author_agent_id", ""),
			AgentVersion: argString(req, "author_version", ""),
		},
		SessionID:     argString(req, "session_id", ""),
		Tags:          tags,
		TTL:           time.Duration(ttlSeconds) * time.Second,
		Confidence:    argFloat(req, "confidence", 0),
		Supersedes:    argString(req, "supersedes", ""),
		ConsumerState: consumerStateArg(req),
	}

	rev, err := a.KnowledgeStore.Write(ctx, in)
	if err != nil {
		var spv *contextpolicy.ScopePolicyViolation
		if errors.As(err, &spv) {
			return toolError(codeNamespaceNotPermitted, err.Error()), nil
		}
		if errors.Is(err, memory.ErrInvalidInput) {
			return toolError(codeValidationError, err.Error()), nil
		}
		return nil, err
	}
	return toolJSON(rev), nil
}
