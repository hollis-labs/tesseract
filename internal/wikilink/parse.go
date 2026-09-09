// Package wikilink extracts `[[target]]` and `[[target|label]]` references
// from the markdown bodies Tesseract already stores.
//
// It is a leaf: it parses text and returns structs, and it knows nothing about
// SQLite, revisions or namespaces. That is what lets both sides of the memory
// subsystem use it — internal/memory calls it on the write path, and
// internal/contextstore calls it from the schema-17 backfill, and those two
// packages are siblings that cannot import each other.
//
// Resolution is deliberately NOT here. A target is a memory_key, and turning a
// key into a memory_id is a database question with a namespace-preference rule
// attached (see internal/memory/links.go). Parsing answers "what did the author
// write"; resolution answers "what does it point at today", and those two
// answers have different lifetimes — which is the whole reason the raw target
// is retained as a column rather than discarded once resolution succeeds.
package wikilink

import (
	"strings"
	"unicode/utf8"
)

// Kind classifies the syntax a link was written in.
//
// Only KindWiki is produced today, because `[[...]]` is the only syntax this
// package parses: Tesseract stores markdown but has never treated an inline
// URL as a graph edge, and inferring edges from prose is an explicit non-goal
// of this cycle. The type exists because the shape this table was modeled on
// (Loom's 007_wiki_links_redesign.sql, itself mirroring Fragments Engine's
// fragment_links) carries it, and because the column is where `external` and
// `anchor` go the day Tesseract parses them. A reader who finds only one value
// in it is seeing the current parser, not a vestigial column.
type Kind string

// KindWiki is a `[[...]]` reference to another entry by key.
const KindWiki Kind = "wiki"

// Link is one `[[...]]` occurrence, as written.
type Link struct {
	// Target is the text left of the pipe, trimmed. It is a memory_key
	// candidate, not a resolved anything — 15% of the targets in the corpus
	// this landed against name keys that no longer exist (renamed away under
	// an older dotted convention) or are prose about the syntax itself, and
	// both are expected steady states rather than errors.
	Target string

	// Label is the text right of the pipe, trimmed, or "" when the link
	// carried no pipe. `[[target|label]]` appears exactly three times in the
	// corpus and every occurrence is documentation OF the syntax rather than a
	// use of it, so this is parsed for fidelity to what was written, not
	// because anything reads it yet.
	Label string

	// Kind is the syntax classification. Always KindWiki today.
	Kind Kind

	// Position is the 0-based ordinal of this link within the scanned text,
	// counting every occurrence including repeats. It orders a revision's
	// links by where they appeared, which is the only ordering the source text
	// carries; it is not a byte offset, so it stays meaningful if the same
	// body is later re-parsed by a scanner with different boundaries.
	Position int
}

// Parse returns every `[[...]]` occurrence in s, in the order they appear.
//
// Duplicates are NOT collapsed: a body that cites the same key three times
// yields three links. Deduplication is a storage decision, and making it here
// would throw away Position — the one thing that distinguishes the three.
//
// Nesting is not supported and is not a thing markdown wikilinks do. A `[[`
// with no closing `]]` is dropped rather than swallowing the rest of the
// document, and an inner `[[` inside an unterminated one restarts the scan, so
// the worst case for malformed input is a missed link rather than a link whose
// target is the remainder of the body.
func Parse(s string) []Link {
	if !strings.Contains(s, "[[") {
		return nil
	}

	var links []Link
	pos := 0

	for i := 0; i+1 < len(s); {
		if s[i] != '[' || s[i+1] != '[' {
			i++
			continue
		}

		inner := s[i+2:]
		// A nested `[[` means the outer one was never closed. Restart at the
		// inner opener rather than letting the outer one consume it.
		if next := strings.Index(inner, "[["); next >= 0 {
			if end := strings.Index(inner, "]]"); end < 0 || next < end {
				i += 2 + next
				continue
			}
		}

		end := strings.Index(inner, "]]")
		if end < 0 {
			// Unterminated: nothing after this point can close it either.
			break
		}

		if link, ok := parseInner(inner[:end], pos); ok {
			links = append(links, link)
			pos++
		}

		i += 2 + end + 2
	}

	return links
}

// parseInner splits one `[[...]]` body into target and label.
//
// It rejects rather than stores two shapes, and the reason is the same in
// both: a row here claims something is a link, and a wrong claim is worse than
// a missing one because the edge table is read as a graph.
//
//   - An empty or whitespace-only target. `[[]]` names nothing.
//   - A target carrying a newline. Every real target in the corpus is a bare
//     key on one line; a multi-line span means the `[[` and `]]` came from
//     unrelated places in the text and pairing them is an accident.
//
// A target containing `{` is kept, not rejected, even though the corpus holds
// `[[UI_PROMPT:{...}]]` — prose about a prompt format rather than a link. That
// is a judgment about MEANING, and this package deliberately does not make
// those: it stays unresolved on its own, costs one row, and is visible as
// exactly what was written. Rejecting it would mean encoding a guess about
// intent in a parser, which is the write-path inference this cycle rules out.
func parseInner(inner string, pos int) (Link, bool) {
	if strings.ContainsAny(inner, "\n\r") {
		return Link{}, false
	}

	target, label, hasPipe := strings.Cut(inner, "|")
	target = strings.TrimSpace(target)
	if target == "" {
		return Link{}, false
	}
	if !utf8.ValidString(target) {
		return Link{}, false
	}
	if hasPipe {
		label = strings.TrimSpace(label)
	} else {
		label = ""
	}

	return Link{
		Target:   target,
		Label:    label,
		Kind:     KindWiki,
		Position: pos,
	}, true
}
