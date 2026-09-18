package mcpadapter

import (
	"context"
	"errors"
	"time"

	"github.com/hollis-labs/tesseract/domains"
	"github.com/hollis-labs/tesseract/internal/contextpolicy"
	"github.com/hollis-labs/tesseract/internal/itemservice"
	"github.com/hollis-labs/tesseract/internal/memory"
)

func (a *Adapter) registerMemoryTools(s *toolRegistrar) {
	// ── memory_write ─────────────────────────────────────────────────────────
	a.addTool(s, gomcpTool("memory_write",
		"**Append an agent memory revision** under `(namespace, memory_key)`.\n"+
			"• **Read this first:** call `tesseract_skills memory` before composing a write. It carries the complete request shape as a copy-pasteable payload — for this surface AND for the HTTP peer, which takes content flat and keeps author structured. Seven arguments are required, and `trigger`, `derived_from` and the namespace `{type}` segment are closed vocabularies; the skill is faster than finding that out one validation_error at a time.\n"+
			"• **Kind of content:** agent observations, preferences, session notes — content you'll want to recall by similarity, activation, or chronological order.\n"+
			"• **Scope:** `memory:write`.\n"+
			domainBoundaryLine+
			"• **Use this when:** a decision and its rationale, a limitation, a deferred follow-up, feedback, an outcome, what a session learned — something a later session should meet while working nearby, without having known to ask for it.\n"+
			"• **Don't use this for:** content someone will come back for by name — a project's canonical, an ADR, a playbook, a doc or package reference (knowledge_write). Active drafts, handoffs, or session plans — workspace_write. Generic revisioned records — context_write.\n"+
			"• **Deeper:** `tesseract_skills namespaces` for namespace rules.",
		inputSchema(
			strProp("namespace", "Typed memory namespace {scope}/{id}/memory/{type} — e.g. project/tesseract/memory/decisions. "+
				"Scope is one of: "+memory.ScopeList()+"; `system` is a singleton and takes no id (system/memory/{type}). "+
				"Allowed types: "+memory.TypeList()+". `notes` is the deliberate catch-all when no stronger type fits.", true),
			strProp("memory_key", "Optional logical key for keyed memories (e.g. user.prefs.style). "+
				"Dot-separated segments, each matching ^[a-z0-9_]+$ — lowercase letters, digits and underscore only. "+
				"At most 6 segments, 64 characters per segment, 256 characters total. "+
				"Hyphens, uppercase and spaces are REJECTED with a validation_error, not normalized away: `user.prefs-style` and `User.Prefs.Style` both fail. "+
				"This rule is memory-domain only; `knowledge_write` keys are free-form slugs.", false),
			strProp("workstream_id", "Optional opaque workstream association. Omit to preserve an existing item's association; send an empty string to clear it.", false),
			strProp("supersedes", "Revision ID this revision supersedes (e.g. 01HX...)", false),
			strProp("status", "Status: draft|reviewed|canonical (default: draft)", false),
			strProp("author_agent_id", "Agent ID of the author (e.g. claude, nanite)", true),
			strProp("author_version", "Agent version string", false),
			strProp("trigger", "Trigger: explicit|post_compact|per_turn|promotion|manual", true),
			strProp("session_id", "Session identifier (e.g. 2026-04-19:backend)", true),
			strProp("derived_from", "DerivedFrom: user|feedback|project|reference|observation", true),
			numProp("confidence", "Confidence score in [0, 1.0] (e.g. 0.9)", true),
			strProp("tags", "JSON array of string tags (e.g. [\"preference\",\"style\"])", false),
			numProp("ttl_seconds", "Time-to-live in seconds (0 = no expiry)", false),
			strProp("payload_summary", "Summary text for the memory payload", true),
			strProp("payload_body", "Optional body text for the memory payload", false),
			strProp("consumer_state", consumerStateArgDescription, false),
			strProp("data", payloadDataArgDescription, false),
			strProp("data_schema_hash", payloadDataSchemaHashArgDescription, false),
			strProp("dedup", "Dedup mode: none (default) or semantic", false),
			numProp("dedup_threshold", "Similarity threshold override for semantic dedup (0 = use config default 0.85)", false),
			strProp("actor", "Actor asserting the write (default: agent). Writing to user/ namespaces requires actor=user.", false),
		),
		toolAnnotations{},
		a.handleMemoryWrite,
	))

	// ── memory_promote ───────────────────────────────────────────────────────
	a.addTool(s, gomcpTool("memory_promote",
		"**Promote a session-scoped memory** to user or project scope.\n"+
			"• **Read this first:** call `tesseract_skills promotion` before promoting. It carries a worked call on both surfaces and the two invariants this tool enforces silently in the namespace strings — source must be session-scoped, and the `{type}` segment must be identical on both sides.\n"+
			"• **Kind of content:** a copy of the source memory revision, re-scoped to the target namespace.\n"+
			"• **Scope:** `memory:write`.\n"+
			"• **Use this when:** a session memory has proven durable and you want it to survive session boundaries.\n"+
			"• **Don't use this for:** cross-ownership promotion (app/* → user/*) — use the `context_promote` three-stage workflow.",
		inputSchema(
			strProp("source_namespace", "Source session memory namespace session/{sid}/memory/{type} (e.g. session/2026-04-19:backend/memory/decisions)", true),
			strProp("source_memory_id", "Source memory ID to promote", true),
			strProp("target_namespace", "Target project or user memory namespace; the {type} segment MUST match the source (e.g. project/tesseract/memory/decisions)", true),
			strProp("actor_agent_id", "Agent ID performing the promotion", true),
			strProp("actor_version", "Agent version string", false),
		),
		toolAnnotations{},
		a.handleMemoryPromote,
	))

}

// ── Handlers ─────────────────────────────────────────────────────────────────

func (a *Adapter) handleMemoryWrite(ctx context.Context, req map[string]any) (any, error) {
	if res, _ := a.checkScope(ctx, "memory:write"); res != nil {
		return res, nil
	}

	// Parse tags — accept both native JSON array and JSON-encoded string.
	tags, _, err := parseStringArrayArg(req, "tags")
	if err != nil {
		return toolError(codeValidationError, "tags "+err.Error()), nil //nolint:nilerr // MCP tool pattern
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

	in := memory.WriteInput{
		// `memory_write` is the memory surface and declares no `domain`
		// argument, so the caller has nothing to fill in. The domain is a
		// property of the TOOL, and it is set here rather than defaulted in
		// the store — see errDomainRequired in internal/memory/write.go.
		Domain:       domains.Memory,
		Namespace:    ns,
		Actor:        actor,
		MemoryKey:    argString(req, "memory_key", ""),
		WorkstreamID: workstreamID,
		Supersedes:   argString(req, "supersedes", ""),
		Status:       memory.Status(argString(req, "status", "")),
		Author: memory.Author{
			AgentID:      argString(req, "author_agent_id", ""),
			AgentVersion: argString(req, "author_version", ""),
		},
		Trigger:        memory.Trigger(argString(req, "trigger", "")),
		SessionID:      argString(req, "session_id", ""),
		DerivedFrom:    memory.DerivedFrom(argString(req, "derived_from", "")),
		Confidence:     argFloat(req, "confidence", 0),
		Tags:           tags,
		TTL:            time.Duration(ttlSeconds) * time.Second,
		Summary:        argString(req, "payload_summary", ""),
		Body:           argString(req, "payload_body", ""),
		Data:           payloadDataArg(req),
		DataSchemaHash: argString(req, "data_schema_hash", ""),
		Dedup:          argString(req, "dedup", ""),
		DedupThreshold: argFloat(req, "dedup_threshold", 0),
		ConsumerState:  consumerStateArg(req),
	}

	rev, err := a.MemoryStore.WriteRevision(ctx, in)
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

// handleTesseractTouch backs the tesseract_touch tool. Scope is memory:read
// rather than memory:write on purpose — see the tool description.
//
// The batch cap is enforced by the store rather than restated here, so this door
// and its HTTP peer refuse the same size with the same message by construction.
func (a *Adapter) handleTesseractTouch(ctx context.Context, req map[string]any) (any, error) {
	if res, _ := a.checkScope(ctx, "memory:read"); res != nil {
		return res, nil
	}

	revisionIDs, present, err := parseStringArrayArg(req, "revision_ids")
	if err != nil {
		return toolError(codeValidationError, "revision_ids "+err.Error()), nil //nolint:nilerr // MCP tool pattern
	}
	itemIDs, itemPresent, itemErr := parseStringArrayArg(req, "item_ids")
	if itemErr != nil {
		return toolError(codeValidationError, "item_ids "+itemErr.Error()), nil //nolint:nilerr // MCP application errors are tool results
	}
	if present == itemPresent {
		return toolError(codeValidationError, "choose exactly one of revision_ids or item_ids"), nil
	}
	if itemPresent {
		if len(itemIDs) > memory.MaxTouchRevisions {
			return toolError(codeValidationError, "at most 100 item_ids per touch"), nil
		}
		_, claims := a.checkScope(ctx, "memory:read")
		metas := make([]itemservice.Metadata, 0, len(itemIDs))
		seen := map[string]bool{}
		for _, id := range itemIDs {
			if seen[id] {
				continue
			}
			seen[id] = true
			meta, lookupErr := a.itemService().LookupMetadata(ctx, id)
			if errors.Is(lookupErr, memory.ErrNotFound) {
				metas = append(metas, itemservice.Metadata{ItemID: id})
				continue
			}
			if lookupErr != nil {
				return nil, lookupErr
			}
			if !globsPermit(claims.NamespaceGlobs, meta.Namespace) {
				return toolError(codeNamespaceNotPermitted, "token namespace globs do not permit touching: "+meta.Namespace), nil
			}
			metas = append(metas, meta)
		}
		out, touchErr := a.itemService().TouchItems(ctx, metas)
		if touchErr != nil {
			if errors.Is(touchErr, memory.ErrInvalidInput) {
				return toolError(codeValidationError, touchErr.Error()), nil
			}
			return nil, touchErr
		}
		return toolJSON(out), nil
	}

	res, err := a.revisionStore().TouchRevisions(ctx, revisionIDs)
	if err != nil {
		if errors.Is(err, memory.ErrInvalidInput) {
			return toolError(codeValidationError, err.Error()), nil
		}
		return nil, err
	}
	return toolJSON(res), nil
}

func (a *Adapter) handleMemoryPromote(ctx context.Context, req map[string]any) (any, error) {
	if res, _ := a.checkScope(ctx, "memory:write"); res != nil {
		return res, nil
	}

	in := memory.PromoteInput{
		SourceNamespace: argString(req, "source_namespace", ""),
		SourceMemoryID:  argString(req, "source_memory_id", ""),
		TargetNamespace: argString(req, "target_namespace", ""),
		ActorAgentID:    argString(req, "actor_agent_id", ""),
		ActorVersion:    argString(req, "actor_version", ""),
	}

	promoted, err := a.MemoryStore.Promote(ctx, in)
	if err != nil {
		if errors.Is(err, memory.ErrInvalidInput) {
			return toolError(codeValidationError, err.Error()), nil
		}
		if errors.Is(err, memory.ErrNotFound) {
			return toolError(codeNotFound, err.Error()), nil
		}
		return nil, err
	}
	return toolJSON(promoted), nil
}
