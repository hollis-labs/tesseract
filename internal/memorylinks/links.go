// Package memorylinks owns the memory_links edge table: its schema, its
// relation vocabulary, and the resolution rule that turns a written `[[key]]`
// into the entry it points at.
//
// It sits between two packages that cannot import each other. internal/memory
// writes edges on the write path; internal/contextstore backfills them from
// the schema-17 migration. Both are siblings over one *sql.DB, so the rule
// they must agree on — what a target resolves to — lives here rather than
// being spelled twice and drifting.
//
// # What the table is
//
// memory_links is an INDEX over the link graph, in the same sense that
// memory_revisions_fts is an index over text. It is derived, it is rebuildable
// from memory_revisions alone, and it never becomes the source of truth for
// anything: `target` retains what the author wrote, and
// memory_revisions.supersedes stays authoritative for lineage — the
// deprecation logic in recall reads the column, not this table. Reading it as
// a second copy of the log is the one way to misuse it.
//
// # Two relations, two grains
//
// The relation vocabulary is closed at two members from the day it ships,
// rather than Loom's `DEFAULT 'references'` with a taxonomy deferred, because
// Tesseract already had a second relation before this table existed.
//
//	references   revision → memory.   A `[[key]]` names an ENTRY, whose current
//	             revision moves over time, so pinning one would freeze the edge
//	             to whatever was current the moment it was parsed.
//	supersedes   revision → revision.  Lineage pins an exact revision; that is
//	             what lineage IS.
//
// The two grains are why `to_memory_id` and `to_revision_id` are separate
// nullable columns instead of one polymorphic target. Collapsing them would
// mean either discarding which revision a supersedes edge names, or claiming a
// references edge points at a revision it does not name.
//
// A consequence worth stating, because it looks like a modeling error and is
// not: every supersedes edge is intra-entry. WriteRevision rejects a supersedes
// pointing at another memory (internal/memory/write.go), and the corpus this
// landed against holds 336 supersedes edges with zero exceptions. So a
// supersedes row always has to_memory_id == from_memory_id. That is a fact
// about Tesseract's lineage, not an artifact of this table, and it is why the
// `related` recall expansion is useful under `references` and self-referential
// under `supersedes`.
package memorylinks

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/hollis-labs/tesseract/internal/wikilink"
)

// Relation is the closed vocabulary of edge types in memory_links.
type Relation string

const (
	// RelationReferences is a `[[key]]` citation parsed out of a revision's
	// payload. Its to_memory_id is NULL whenever the target names no entry —
	// an expected steady state, not an error: 15% of the targets in the
	// corpus this shipped against are keys renamed away under an older
	// convention, or prose about the link syntax itself.
	RelationReferences Relation = "references"

	// RelationSupersedes projects memory_revisions.supersedes into the edge
	// table. Written by a trigger rather than by Go, so the projection cannot
	// drift from the column it mirrors — the same reason the FTS5 index is
	// trigger-maintained.
	RelationSupersedes Relation = "supersedes"
)

// Vocabulary returns the accepted relation values, for surfaces that render
// the vocabulary into help text or validate against it.
func Vocabulary() []string {
	return []string{string(RelationReferences), string(RelationSupersedes)}
}

// Valid reports whether r is a member of the vocabulary.
func (r Relation) Valid() bool {
	return r == RelationReferences || r == RelationSupersedes
}

// Schema is the DDL for the edge table, its indexes, and the trigger that
// projects lineage into it. Executed in order by the schema-17 migration.
//
// The CHECK constraint is what makes the vocabulary closed in storage rather
// than only in Go. A relation value that no reader understands is worse than a
// rejected insert, because the table is read as a graph and an unknown edge
// type is silently traversed or silently skipped depending on the query.
//
// Indexes cover both directions deliberately. "What does X cite" and "what
// cites X" are the same question asked from opposite ends, and a graph indexed
// one way answers half of it with a full scan.
var Schema = []string{
	`CREATE TABLE IF NOT EXISTS memory_links (
	link_id          INTEGER PRIMARY KEY,
	from_revision_id TEXT NOT NULL REFERENCES memory_revisions(revision_id) ON DELETE CASCADE,
	from_memory_id   TEXT NOT NULL,
	to_memory_id     TEXT NULL REFERENCES memory_state(memory_id) ON DELETE SET NULL,
	to_revision_id   TEXT NULL REFERENCES memory_revisions(revision_id) ON DELETE SET NULL,
	relation         TEXT NOT NULL CHECK (relation IN ('references','supersedes')),
	kind             TEXT NULL,
	target           TEXT NOT NULL,
	label            TEXT NULL,
	position         INTEGER NOT NULL DEFAULT 0,
	created_at       TEXT NOT NULL
)`,

	// Forward traversal, and the delete path: removing a revision's edges
	// before re-parsing it is a from_revision_id range.
	`CREATE INDEX IF NOT EXISTS idx_memory_links_from_revision ON memory_links(from_revision_id)`,
	// Forward traversal at entry grain, which is what the `related` expansion
	// asks: relation is in the key so a relation-filtered walk stays in the
	// index instead of filtering rows it already fetched.
	`CREATE INDEX IF NOT EXISTS idx_memory_links_from_memory ON memory_links(from_memory_id, relation)`,
	// Reverse traversal: "what cites X". Partial because an unresolved edge
	// has no incoming end to walk to, and 15% of rows are unresolved.
	`CREATE INDEX IF NOT EXISTS idx_memory_links_to_memory ON memory_links(to_memory_id, relation) WHERE to_memory_id IS NOT NULL`,
	// Re-resolution: when a key is finally written, the edges waiting on it
	// are found by target text, not by any id.
	`CREATE INDEX IF NOT EXISTS idx_memory_links_target ON memory_links(target)`,

	// Resolution reads memory_state by key alone, across every namespace.
	// UNIQUE(namespace, memory_key) indexes the pair with namespace leading,
	// so it cannot serve a key-only lookup — without this index every link
	// resolved during a write is a full scan of memory_state.
	`CREATE INDEX IF NOT EXISTS idx_memory_state_memory_key ON memory_state(memory_key) WHERE memory_key IS NOT NULL`,

	// Lineage projection. A trigger rather than Go because memory_revisions
	// is written from more than one path (WriteRevision, Promote, the kinds
	// migration), and a projection maintained at each call site is a
	// projection that drifts the first time a fourth path is added. The
	// trigger fires inside the same transaction as the insert it mirrors, so
	// the two cannot commit apart.
	//
	// target carries the superseded revision_id: it is the raw, resolution-
	// independent identity of what the edge names, which is the same job
	// target does for a parsed link. kind is NULL because a lineage edge was
	// never written in any syntax — kind classifies HOW a link was written,
	// and there is no answer here.
	`CREATE TRIGGER IF NOT EXISTS memory_links_supersedes_ai
AFTER INSERT ON memory_revisions
WHEN new.supersedes IS NOT NULL
BEGIN
	INSERT INTO memory_links (
		from_revision_id, from_memory_id, to_memory_id, to_revision_id,
		relation, kind, target, label, position, created_at
	) VALUES (
		new.revision_id, new.memory_id, new.memory_id, new.supersedes,
		'supersedes', NULL, new.supersedes, NULL, 0, new.created_at
	);
END`,
}

// Resolver turns a link target into a memory_id.
//
// It is an interface over one query so the write path can hit the database per
// revision while the backfill loads memory_state once — 2029 revisions times a
// key lookup is a different problem from one revision times a key lookup, and
// the resolution RULE has to stay identical across both or a backfilled corpus
// disagrees with everything written after it.
type Resolver interface {
	// Resolve returns the memory_id target names when read from namespace, or
	// "" when it names nothing resolvable.
	Resolve(ctx context.Context, namespace, target string) (string, error)
}

// resolveFrom applies the shared resolution rule to a candidate set.
//
// The rule, in order:
//
//  1. An entry with this key in the SAME namespace wins outright. Namespaces
//     are the unit of ownership, so a local key is what a local author meant.
//  2. Otherwise, if exactly one entry anywhere carries the key, that is it.
//     This is not a fallback so much as the common case: 35% of the resolvable
//     link occurrences in the corpus cross namespaces, and the crossings are
//     the interesting half of the graph (followups ↔ decisions, learnings →
//     decisions, knowledge → memory). Resolving within a namespace only would
//     drop a third of the edges and exactly the third worth having.
//  3. Otherwise the target is AMBIGUOUS and stays unresolved. Guessing between
//     two namespaces that both claim a key would put a silent wrong edge in a
//     graph, and an unresolved edge still retains its target text, so nothing
//     is lost but the guess. One key in the corpus is ambiguous.
func resolveFrom(candidates []candidate, namespace string) string {
	if len(candidates) == 0 {
		return ""
	}
	for _, c := range candidates {
		if c.namespace == namespace {
			return c.memoryID
		}
	}
	if len(candidates) == 1 {
		return candidates[0].memoryID
	}
	return ""
}

type candidate struct {
	memoryID  string
	namespace string
}

// TxResolver resolves against a live transaction, one target at a time. Used
// by the write path, where a revision carries a handful of links and the
// lookup is an indexed point read.
type TxResolver struct {
	Tx *sql.Tx
}

// Resolve implements Resolver.
func (r TxResolver) Resolve(ctx context.Context, namespace, target string) (string, error) {
	rows, err := r.Tx.QueryContext(ctx,
		`SELECT memory_id, namespace FROM memory_state WHERE memory_key = ?`, target)
	if err != nil {
		return "", fmt.Errorf("resolve link target: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var candidates []candidate
	for rows.Next() {
		var c candidate
		if scanErr := rows.Scan(&c.memoryID, &c.namespace); scanErr != nil {
			return "", fmt.Errorf("scan link target: %w", scanErr)
		}
		candidates = append(candidates, c)
		// Rule 1 short-circuits on an in-namespace hit; rule 2 only needs to
		// know whether there is more than one. Nothing past the second row
		// changes the answer.
		if c.namespace == namespace {
			break
		}
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("resolve link target: %w", err)
	}
	return resolveFrom(candidates, namespace), nil
}

// MapResolver resolves against an in-memory snapshot of memory_state. Used by
// the backfill, which would otherwise issue one query per link across the
// whole corpus.
type MapResolver struct {
	byKey map[string][]candidate
}

// NewMapResolver loads every keyed entry from memory_state through q.
func NewMapResolver(ctx context.Context, q Querier) (*MapResolver, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT memory_id, namespace, memory_key FROM memory_state WHERE memory_key IS NOT NULL AND memory_key <> ''`)
	if err != nil {
		return nil, fmt.Errorf("load memory_state for link resolution: %w", err)
	}
	defer func() { _ = rows.Close() }()

	byKey := make(map[string][]candidate)
	for rows.Next() {
		var c candidate
		var key string
		if scanErr := rows.Scan(&c.memoryID, &c.namespace, &key); scanErr != nil {
			return nil, fmt.Errorf("scan memory_state: %w", scanErr)
		}
		byKey[key] = append(byKey[key], c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("load memory_state for link resolution: %w", err)
	}
	return &MapResolver{byKey: byKey}, nil
}

// Resolve implements Resolver.
func (r *MapResolver) Resolve(_ context.Context, namespace, target string) (string, error) {
	return resolveFrom(r.byKey[target], namespace), nil
}

// Querier is the read subset of *sql.Tx / *sql.DB this package needs.
type Querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// Execer is the write subset of *sql.Tx / *sql.DB this package needs.
type Execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// RevisionRef identifies the revision an edge is being written for.
type RevisionRef struct {
	RevisionID string
	MemoryID   string
	Namespace  string
	CreatedAt  string
}

// WriteReferences parses text for `[[links]]`, resolves each target, and
// inserts one references row per occurrence.
//
// It replaces rather than appends: a revision's reference edges are deleted
// first, so re-parsing the same revision is idempotent and a rebuild does not
// double every edge. Only references rows are touched — the supersedes
// projection belongs to the trigger, and deleting it here would leave lineage
// missing until the revision was reinserted, which never happens on an
// append-only log.
func WriteReferences(ctx context.Context, ex Execer, res Resolver, rev RevisionRef, text string) error {
	if _, err := ex.ExecContext(ctx,
		`DELETE FROM memory_links WHERE from_revision_id = ? AND relation = ?`,
		rev.RevisionID, string(RelationReferences),
	); err != nil {
		return fmt.Errorf("clear reference links: %w", err)
	}

	links := wikilink.Parse(text)
	if len(links) == 0 {
		return nil
	}

	for _, l := range links {
		toMemoryID, err := res.Resolve(ctx, rev.Namespace, l.Target)
		if err != nil {
			return err
		}
		if _, err := ex.ExecContext(ctx, `
INSERT INTO memory_links (
	from_revision_id, from_memory_id, to_memory_id, to_revision_id,
	relation, kind, target, label, position, created_at
) VALUES (?, ?, ?, NULL, ?, ?, ?, ?, ?, ?)`,
			rev.RevisionID,
			rev.MemoryID,
			nullString(toMemoryID),
			string(RelationReferences),
			string(l.Kind),
			l.Target,
			nullString(l.Label),
			l.Position,
			rev.CreatedAt,
		); err != nil {
			return fmt.Errorf("insert reference link: %w", err)
		}
	}
	return nil
}

// ResolvePending binds unresolved reference edges that name key, now that an
// entry carrying it exists.
//
// Without this, resolution would be forward-only: an edge is resolved once, at
// the instant it is parsed, against whatever had been written by then. That
// sounds like a rare edge case and is in fact the ordinary authoring order —
// a decision that cites the followup it spawns is written BEFORE that followup
// exists, so the edge would be stored unresolved and stay that way forever
// while the entry it names sat in the store. The graph would silently
// under-report, which is worse than reporting nothing: a caller cannot tell a
// sparse neighborhood from a stale one.
//
// It applies the SAME rule as first-pass resolution rather than a blanket
// UPDATE, and that is load-bearing. A blanket "bind every unresolved edge with
// this target" would ignore namespace preference and ambiguity both: an edge
// that declined to guess between two claimants would silently bind to a third.
// Instead each waiting source namespace is re-asked the original question, so
// an edge only ever resolves to what resolution would have said all along.
//
// Idempotent: it touches only rows that are still NULL.
func ResolvePending(ctx context.Context, tx *sql.Tx, res Resolver, key string) error {
	if key == "" {
		return nil
	}

	rows, err := tx.QueryContext(ctx, `
SELECT DISTINCT s.namespace
FROM memory_links l
INNER JOIN memory_state s ON s.memory_id = l.from_memory_id
WHERE l.target = ? AND l.to_memory_id IS NULL AND l.relation = ?`,
		key, string(RelationReferences))
	if err != nil {
		return fmt.Errorf("find pending links: %w", err)
	}
	var namespaces []string
	for rows.Next() {
		var ns string
		if scanErr := rows.Scan(&ns); scanErr != nil {
			_ = rows.Close()
			return fmt.Errorf("scan pending link namespace: %w", scanErr)
		}
		namespaces = append(namespaces, ns)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("find pending links: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close pending link scan: %w", err)
	}

	for _, ns := range namespaces {
		memoryID, resErr := res.Resolve(ctx, ns, key)
		if resErr != nil {
			return resErr
		}
		if memoryID == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
UPDATE memory_links
SET to_memory_id = ?
WHERE target = ? AND to_memory_id IS NULL AND relation = ?
  AND from_memory_id IN (SELECT memory_id FROM memory_state WHERE namespace = ?)`,
			memoryID, key, string(RelationReferences), ns,
		); err != nil {
			return fmt.Errorf("bind pending links: %w", err)
		}
	}
	return nil
}

// LinkText is the text a revision's links are parsed from: its summary and its
// body, in that order.
//
// Both are scanned because both are authored prose and either can carry a
// citation — the summary of tesseract_vnext_target_architecture cites nothing,
// but its body cites eleven records, and other entries put the link in the
// summary. Concatenating with a newline keeps a link from being formed across
// the seam: a `[[` at the end of the summary cannot pair with a `]]` at the
// start of the body, because Parse rejects a span containing a newline.
func LinkText(summary, body string) string {
	return summary + "\n" + body
}

func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// Backfill parses every existing revision into the edge table and projects
// existing lineage, inside the caller's migration transaction.
//
// Reference edges are rebuilt from scratch and lineage is projected with a
// single INSERT ... SELECT rather than by replaying inserts through the
// trigger — the trigger fires on INSERT ON memory_revisions, and a migration
// does not reinsert the log it is migrating.
func Backfill(ctx context.Context, tx *sql.Tx) error {
	resolver, err := NewMapResolver(ctx, tx)
	if err != nil {
		return err
	}

	rows, err := tx.QueryContext(ctx, `
SELECT revision_id, memory_id, namespace, created_at,
       COALESCE(payload_summary, ''), COALESCE(payload_body, '')
FROM memory_revisions`)
	if err != nil {
		return fmt.Errorf("scan revisions for link backfill: %w", err)
	}

	type pending struct {
		ref  RevisionRef
		text string
	}
	// Collected before writing: SQLite will not accept writes on the same
	// transaction while a read cursor over the table being written is still
	// open, and the corpus this runs against is thousands of rows, not
	// millions.
	var work []pending
	for rows.Next() {
		var p pending
		var summary, body string
		if scanErr := rows.Scan(
			&p.ref.RevisionID, &p.ref.MemoryID, &p.ref.Namespace, &p.ref.CreatedAt,
			&summary, &body,
		); scanErr != nil {
			_ = rows.Close()
			return fmt.Errorf("scan revision for link backfill: %w", scanErr)
		}
		p.text = LinkText(summary, body)
		work = append(work, p)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("scan revisions for link backfill: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close revision scan: %w", err)
	}

	for _, p := range work {
		if err := WriteReferences(ctx, tx, resolver, p.ref, p.text); err != nil {
			return err
		}
	}

	// NOT EXISTS rather than a bare INSERT ... SELECT: reference edges are
	// rebuilt by WriteReferences, which clears before it writes, and the
	// lineage arm has to be re-runnable on the same terms or a second pass
	// over the same log doubles every lineage edge. Migrations run once per
	// version in production, so this guards a rebuild rather than an upgrade.
	if _, err := tx.ExecContext(ctx, `
INSERT INTO memory_links (
	from_revision_id, from_memory_id, to_memory_id, to_revision_id,
	relation, kind, target, label, position, created_at
)
SELECT r.revision_id, r.memory_id, r.memory_id, r.supersedes,
       'supersedes', NULL, r.supersedes, NULL, 0, r.created_at
FROM memory_revisions r
WHERE r.supersedes IS NOT NULL
  AND NOT EXISTS (
      SELECT 1 FROM memory_links l
      WHERE l.from_revision_id = r.revision_id AND l.relation = 'supersedes'
  )`); err != nil {
		return fmt.Errorf("project supersedes lineage: %w", err)
	}

	return nil
}
