package mcpadapter

import (
	"fmt"
	"sort"
	"strings"
)

// The tool-name vocabulary: one verb per operation, one prefix rule.
//
// An agent that has never seen a tool name should be able to work out what it
// does, and an agent that knows an operation should be able to guess its name
// in a domain it has not used. That only holds if the same operation carries
// the same verb everywhere, and if the prefix carries information rather than
// decoration.
//
// This is DATA, not prose. The conformance test reads it (toolvocab_test.go in
// tests/parity), and the "Tool naming" section of docs/MCP_TOOLS.md is rendered
// from it (RenderVerbTableMarkdown). Adding a verb is one edit here that
// propagates, rather than three edits that can disagree.
//
// ────────────────────────────────────────────────────────────────────────────
// WHAT THIS STRUCTURE CANNOT DO FOR YOU
//
// Deriving the conformance test from this table proves every registered name
// matches THE TABLE. It does not prove the table is RIGHT. A wrong verb here
// produces a surface that is consistently wrong and passes every check.
//
// That is why tests/parity/toolvocab_test.go carries a hand-written list of
// names that must be accepted and names that must be rejected, stated as
// literals that do not move when this table moves. If you change anything here,
// expect that test to have an opinion about it. If it does not, the check has
// gone tautological and the fix is a better anchor, not a looser table.
// ────────────────────────────────────────────────────────────────────────────

// ToolPrefix is one row of the prefix rule.
type ToolPrefix struct {
	Prefix string
	Means  string
}

// ToolPrefixRule is the closed set of first name segments.
//
// The rule in one line: `tesseract_` when the tool spans domains or serves the
// surface itself; `<domain>_` when it is domain-specific.
var ToolPrefixRule = []ToolPrefix{
	{"context", "the context domain only — generic revisioned records"},
	{"knowledge", "the knowledge domain only — pointer-first references"},
	{"memory", "the memory domain only — agent-authored revisions"},
	{"tesseract", "spans every domain, or serves the surface itself"},
}

// ToolOperation is one row of the verb table.
//
// Verb is the TRAILING segment or segments of a tool name. Anything between the
// prefix and the verb is a free-form subject naming what is operated on
// (`registry` in context_registry_list, `typed` in context_typed_write). The
// vocabulary governs the verb; the subject is the tool author's to choose.
type ToolOperation struct {
	Verb     string
	Means    string
	Prefixes []string // which prefixes may carry this verb
}

// ToolVerbTable is the vocabulary. Keep sorted by Verb.
var ToolVerbTable = []ToolOperation{
	{"deprecate", "Soft-remove one revision; history keeps it.", []string{"tesseract"}},
	{"embed", "Compute and store an embedding vector for a record.", []string{"context"}},
	{"estimate", "Size what a selector would return, without returning it.", []string{"context"}},
	{"get", "Fetch the current entry at one identity.", []string{"tesseract"}},
	{"get_revision", "Fetch one revision by its revision_id.", []string{"tesseract"}},
	{"history", "Every revision of one entry, newest first.", []string{"tesseract"}},
	{"ingest", "Write many records, or one document split into many, in a single call.", []string{"context"}},
	{"list", "Enumerate the entries of a registry or a log.", []string{"context"}},
	{"pack", "Assemble a budget-bounded bundle of records.", []string{"context"}},
	{"plan", "Produce a fetch plan for an intent, and optionally run it.", []string{"context"}},
	{"promote", "Move an entry across scope or ownership.", []string{"context", "memory"}},
	{"recall", "Ranked multi-result retrieval across domains.", []string{"tesseract"}},
	{"register", "Add an entry to a registry.", []string{"context"}},
	{"search", "Rank records of the context store by vector similarity.", []string{"context"}},
	{"set", "Move a record to a named value of a closed field.", []string{"context"}},
	{"touch", "Report deliberate use, so it counts toward activation.", []string{"tesseract"}},
	{"view", "Evaluate a view or selector and return what it matches.", []string{"context"}},
	{"write", "Append a revision or record.", []string{"context", "knowledge", "memory"}},
}

// ToolNameExemptions are registered names that do NOT match the vocabulary,
// each with the reason it was not brought into line.
//
// An exemption is a hole in the rule. It is here, and not laundered into the
// verb table as a one-off entry, so that the non-conformance stays visible to a
// reader instead of being dressed up as vocabulary. The conformance test
// reports these separately from the names that actually conform.
var ToolNameExemptions = map[string]string{
	"context_rag_query": "`rag_query` is not an operation in the vocabulary, and it is the one name on the " +
		"surface that does not fit. Left as-is under an explicit CW-20260825-0012 scope fence covering " +
		"context_rag_query, context_search and context_embed; the other two turned out to fit " +
		"(`search`, `embed`) and were admitted to the table on their merits.",
	"tesseract_skills": "Named for what it serves rather than for a verb, and both its arms — the catalog " +
		"and one skill body — are covered by the plural noun. Kept because it is the most-referenced " +
		"identifier on the surface (every tool description ends in a `tesseract_skills <name>` pointer) " +
		"and no verb form read better than the noun: it neither purely lists nor purely gets.",
}

// ToolNameError explains why a name does not match the vocabulary. It is a
// distinct type so a caller can tell "malformed" from "unknown verb" without
// matching on message text.
type ToolNameError struct {
	Name   string
	Reason string
}

func (e *ToolNameError) Error() string {
	return fmt.Sprintf("tool name %q does not match the naming vocabulary: %s", e.Name, e.Reason)
}

// ValidateToolName reports whether a registered name is acceptable: it either
// matches the vocabulary or carries an explicit exemption.
func ValidateToolName(name string) error {
	if _, ok := ToolNameExemptions[name]; ok {
		return nil
	}
	return CheckToolNameAgainstVocabulary(name)
}

// CheckToolNameAgainstVocabulary applies the prefix rule and the verb table to
// name, with no exemption short-circuit.
//
// Split out from ValidateToolName so a caller can ask the two questions
// separately — in particular so the exemption list can be checked for entries
// that would pass anyway, without the check having to restate the predicate.
func CheckToolNameAgainstVocabulary(name string) error {
	segs := strings.Split(name, "_")
	if len(segs) < 2 {
		return &ToolNameError{name, "a tool name is <prefix>_[subject_]<verb>; this has no verb segment"}
	}
	for _, s := range segs {
		if s == "" || strings.ToLower(s) != s {
			return &ToolNameError{name, "segments must be non-empty and lower-case"}
		}
	}

	prefix := segs[0]
	if !knownPrefix(prefix) {
		return &ToolNameError{name, fmt.Sprintf("prefix %q is not one of %s", prefix, strings.Join(prefixNames(), ", "))}
	}

	// Longest-suffix-first, so `get_revision` wins over `revision` and a name
	// cannot pick up a shorter verb hiding inside a longer one.
	rest := segs[1:]
	for n := len(rest); n >= 1; n-- {
		verb := strings.Join(rest[len(rest)-n:], "_")
		op, ok := lookupOperation(verb)
		if !ok {
			continue
		}
		for _, p := range op.Prefixes {
			if p == prefix {
				return nil
			}
		}
		return &ToolNameError{name, fmt.Sprintf(
			"verb %q is scoped to %s, so it cannot carry the %q prefix",
			verb, strings.Join(op.Prefixes, "/"), prefix)}
	}

	return &ToolNameError{name, fmt.Sprintf(
		"no trailing segment of %q is a verb in the table; add the operation to ToolVerbTable or rename the tool",
		strings.Join(rest, "_"))}
}

func knownPrefix(p string) bool {
	for _, r := range ToolPrefixRule {
		if r.Prefix == p {
			return true
		}
	}
	return false
}

func prefixNames() []string {
	out := make([]string, 0, len(ToolPrefixRule))
	for _, r := range ToolPrefixRule {
		out = append(out, r.Prefix)
	}
	return out
}

func lookupOperation(verb string) (ToolOperation, bool) {
	for _, op := range ToolVerbTable {
		if op.Verb == verb {
			return op, true
		}
	}
	return ToolOperation{}, false
}

// RenderVerbTableMarkdown renders the prefix rule and the verb table as the
// markdown block that docs/MCP_TOOLS.md carries between its naming markers.
//
// One definition, two consumers: the conformance test reads the structures
// above, and the shipped doc is this function's output. A test asserts the doc
// still matches, so the two cannot drift.
func RenderVerbTableMarkdown() string {
	var b strings.Builder

	b.WriteString("**Prefix rule.** `tesseract_` when the tool spans domains or serves the surface itself; `<domain>_` when it is domain-specific.\n\n")
	b.WriteString("| Prefix | Covers |\n|---|---|\n")
	for _, p := range ToolPrefixRule {
		fmt.Fprintf(&b, "| `%s_` | %s |\n", p.Prefix, p.Means)
	}

	b.WriteString("\n**Verb table.** The verb is the trailing segment(s) of the name; anything between prefix and verb is a subject naming what is operated on.\n\n")
	b.WriteString("| Verb | Means | Prefixes |\n|---|---|---|\n")
	for _, op := range ToolVerbTable {
		prefixes := make([]string, len(op.Prefixes))
		for i, p := range op.Prefixes {
			prefixes[i] = "`" + p + "`"
		}
		fmt.Fprintf(&b, "| `%s` | %s | %s |\n", op.Verb, op.Means, strings.Join(prefixes, ", "))
	}

	names := make([]string, 0, len(ToolNameExemptions))
	for n := range ToolNameExemptions {
		names = append(names, n)
	}
	sort.Strings(names)
	b.WriteString("\n**Exemptions.** Registered names that do not match the vocabulary, and why.\n\n")
	b.WriteString("| Tool | Why |\n|---|---|\n")
	for _, n := range names {
		fmt.Fprintf(&b, "| `%s` | %s |\n", n, collapseSpace(ToolNameExemptions[n]))
	}

	return b.String()
}

// collapseSpace flattens a multi-line Go string constant into one markdown
// table cell.
func collapseSpace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
