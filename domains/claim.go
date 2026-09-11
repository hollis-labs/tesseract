package domains

import "strings"

// Claim names the write surface that owns a domain's address space. Callers
// that must refuse a write report these so the caller learns where the write
// belonged, not merely that it was rejected.
type Claim struct {
	Domain   Domain
	MCPTool  string // e.g. "memory_write"
	HTTPPath string // e.g. "/v1/memory/write"
}

// claims is keyed by domain and holds the paired surface for each. The pairs
// are the ones tests/parity's surfaceCatalog asserts, so a rename that moves
// the tool without moving this map fails TestShippedProseNamesOnlyRegisteredTools
// on the error strings these feed.
var claims = map[Domain]Claim{
	Memory:    {Domain: Memory, MCPTool: "memory_write", HTTPPath: "/v1/memory/write"},
	Knowledge: {Domain: Knowledge, MCPTool: "knowledge_write", HTTPPath: "/v1/knowledge/write"},
	Event:     {Domain: Event, MCPTool: "event_write", HTTPPath: "/v1/event/write"},
}

// domainSegments are the positions a domain name occupies in the curated
// namespace grammars. The flat scope puts it third —
// user/{id}/memory/{type}, {user|app}/{id}/knowledge/... — and the project and
// session scopes push it to fifth:
// user/{id}/project/{pid}/memory/{type}, user/{id}/session/{sid}/event/{type}.
//
// No other position is checked, so a namespace that merely contains the word
// (app/memory-tools/config) is not claimed.
var domainSegments = []int{2, 4}

// ClaimedBy reports which curated domain's address space ns falls inside.
//
// This is a NAME test, not a grammar test: it asks whether a domain segment
// sits where a curated namespace would put it, and deliberately knows nothing
// about scopes, type vocabularies or id shapes. That keeps this package at the
// "identity and nothing else" contract its doc comment sets out — the parser
// that answers whether ns is a VALID memory namespace stays in internal/memory,
// and contextstore does not have to import memory's domain model to find out
// that a namespace is not its own.
//
// The consequence of testing names rather than grammar is that ClaimedBy is
// deliberately the broader of the two. app/foo/memory/notes is claimed even
// though the memory grammar requires a user/ prefix and would reject it. That
// is the safe direction for a reserved word: a near-miss inside the reserved
// space is refused rather than silently landing in the wrong store, which is
// the exact failure this exists to stop (CW-20260909-0013).
func ClaimedBy(ns string) (Claim, bool) {
	segs := strings.Split(ns, "/")
	for _, i := range domainSegments {
		if i >= len(segs) {
			break
		}
		if c, ok := claims[Domain(segs[i])]; ok {
			return c, true
		}
	}
	return Claim{}, false
}
