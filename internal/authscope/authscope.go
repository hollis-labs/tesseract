// Package authscope is the vocabulary that joins Tesseract's per-token
// capability scopes to the shared go-auth scope names (tesseract.<verb>).
//
// Two layers use it, and the split is the point:
//
//   - The go-auth guard makes a COARSE decision per route or MCP tool: does the
//     principal hold the one tesseract.<verb> the route needs. Verbs are few on
//     purpose (read, write, promote, maintain, admin) so a Tether-issued JWT can
//     be minted with a readable scope set.
//   - Tesseract's own handlers keep the FINE decision they always made
//     (requireScope on "memory:write", "promote.apply", ...; namespace globs).
//     A verified principal is translated back to that vocabulary by ToLegacy so
//     those checks apply unchanged to managed tokens, the static token and JWTs.
//
// Managed tokens translate legacy -> canonical (FromLegacy) so a token minted
// before go-auth keeps working with no re-minting; JWT principals translate
// canonical -> legacy (ToLegacy). Neither direction ever invents authority a
// token did not hold: a legacy scope grants only the canonical verb that
// contains it, and a canonical verb grants only the legacy scopes under it.
package authscope

import (
	"sort"
	"strings"
)

// Canonical go-auth scope names for this app.
const (
	App      = "tesseract"
	Read     = "tesseract.read"     // recall, lookup, history, packet/pack, list, whoami
	Write    = "tesseract.write"    // context/memory/knowledge/event/workspace writes, status changes
	Promote  = "tesseract.promote"  // promote request/approve/apply (context + workspace)
	Maintain = "tesseract.maintain" // repair, compaction, queue, namespace registration
	Admin    = "tesseract.admin"    // settings, config backup/restore, token lifecycle, auth summary
)

// canonical lists every verb in a stable order.
var canonical = []string{Read, Write, Promote, Maintain, Admin}

// Canonical returns a copy of the canonical verbs.
func Canonical() []string { return append([]string(nil), canonical...) }

// legacyOwner maps each legacy scope to the one canonical verb that contains it.
var legacyOwner = map[string]string{
	"memory:read":        Read,
	"packet":             Read,
	"write":              Write,
	"memory:write":       Write,
	"promote.request":    Promote,
	"promote.approve":    Promote,
	"promote.apply":      Promote,
	"repair":             Maintain,
	"namespace.register": Maintain,
	"namespace.admin":    Maintain,
	"admin":              Admin,
}

// legacyUnder lists, per canonical verb, the legacy scopes it expands to.
var legacyUnder = func() map[string][]string {
	m := map[string][]string{}
	for legacy, verb := range legacyOwner {
		m[verb] = append(m[verb], legacy)
	}
	for _, v := range m {
		sort.Strings(v)
	}
	return m
}()

// FromLegacy translates a stored token's legacy scopes into canonical ones.
//
// Every valid managed token also holds Read. That is not a grant: it records
// existing behavior. Context-domain reads have never required a scope, only a
// valid token, and the memory:read check stays in the handler (fine layer).
// Unknown legacy scopes are dropped here and remain visible to the fine layer.
func FromLegacy(legacy []string) []string {
	have := map[string]bool{Read: true}
	for _, s := range legacy {
		if v, ok := legacyOwner[strings.TrimSpace(s)]; ok {
			have[v] = true
		}
	}
	out := make([]string, 0, len(have))
	for _, v := range canonical {
		if have[v] {
			out = append(out, v)
		}
	}
	return out
}

// IsGrantAll reports whether a grant is the app wildcard or the global one.
func isGrantAll(g string) bool { return g == "*" || g == App+".*" }

// ToLegacy translates a principal's canonical grants into the legacy scope set
// the fine-grained handler checks understand. Grants for other apps and
// unknown tesseract verbs expand to nothing.
func ToLegacy(grants []string) []string {
	have := map[string]bool{}
	for _, g := range grants {
		g = strings.TrimSpace(g)
		if isGrantAll(g) {
			for _, legacy := range allLegacy() {
				have[legacy] = true
			}
			continue
		}
		for _, legacy := range legacyUnder[g] {
			have[legacy] = true
		}
	}
	out := make([]string, 0, len(have))
	for s := range have {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func allLegacy() []string {
	out := make([]string, 0, len(legacyOwner))
	for s := range legacyOwner {
		out = append(out, s)
	}
	return out
}
