package contextstore

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ErrNamespaceQuery marks a ListNamespacePolicyPage failure caused by the
// caller's arguments rather than by the database. Transports check it with
// errors.Is to answer "you asked wrong" instead of "we broke", which is a
// distinction a caller can act on and a substring match on the message is not.
var ErrNamespaceQuery = errors.New("invalid namespace query")

// NamespaceMatchMode selects how NamespaceQuery.Match is compared against a
// namespace.
//
// Prefix is the default and the historical behavior of every surface. Glob is
// opt-in and explicit: the MCP arm once treated its `prefix` argument as a glob
// while HTTP treated it literally (CW-20260428-0005), so glob matching returns
// here as a mode a caller names rather than a difference a caller discovers.
type NamespaceMatchMode string

const (
	NamespaceMatchPrefix   NamespaceMatchMode = "prefix"
	NamespaceMatchContains NamespaceMatchMode = "contains"
	NamespaceMatchGlob     NamespaceMatchMode = "glob"
)

// NamespaceSortField names an ordering for a namespace listing. Every ordering
// is made total by appending namespace as a final tiebreak — that is what lets
// a keyset cursor resume without skipping or repeating a row when two rows
// share an updated_at or an owner.
type NamespaceSortField string

const (
	NamespaceSortNamespace NamespaceSortField = "namespace"
	NamespaceSortOwner     NamespaceSortField = "owner"
	NamespaceSortUpdatedAt NamespaceSortField = "updated_at"
)

// NamespaceQuery narrows, orders and pages a namespace listing. The zero value
// is "every namespace, ordered by namespace ascending, unpaged" — which is what
// ListNamespacePolicies asks for.
type NamespaceQuery struct {
	// Match is the pattern compared against the namespace under MatchMode.
	// Empty matches everything, whatever the mode.
	Match     string
	MatchMode NamespaceMatchMode

	// OwnerType and OwnerID are exact-match filters. Empty means unfiltered.
	OwnerType string
	OwnerID   string

	Sort NamespaceSortField
	Desc bool

	// Limit caps the page. Zero means no limit, in which case NextCursor is
	// always empty because there is nothing left to ask for.
	Limit int

	// Cursor resumes a previous page. It is bound to the Sort and Desc it was
	// issued under; resuming it under a different ordering is an error rather
	// than a silently wrong page.
	Cursor string
}

// NamespacePage is one page of a namespace listing plus what a caller needs to
// know about the rest of the result set.
type NamespacePage struct {
	Items []NamespacePolicyEntry

	// Total counts every row matching the filter, ignoring Limit and Cursor.
	// It is the size of the set the caller is paging through, not the size of
	// this page.
	Total int

	// NextCursor is empty when this page is the last one. A caller that pages
	// until NextCursor is empty has seen the complete set.
	NextCursor string
}

// namespaceSortColumns returns the ORDER BY columns for a sort field, always
// ending in namespace so the ordering is total.
func namespaceSortColumns(sort NamespaceSortField) ([]string, error) {
	switch sort {
	case "", NamespaceSortNamespace:
		return []string{"namespace"}, nil
	case NamespaceSortOwner:
		return []string{"owner_type", "owner_id", "namespace"}, nil
	case NamespaceSortUpdatedAt:
		return []string{"updated_at", "namespace"}, nil
	default:
		return nil, fmt.Errorf("%w: unknown sort %q, want namespace|owner|updated_at", ErrNamespaceQuery, sort)
	}
}

// escapeLike neutralizes the wildcards SQLite's LIKE would otherwise read in a
// caller-supplied literal, so a namespace containing _ or % is matched as
// itself. Paired with ESCAPE '\' at the call site.
func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// namespaceFilterClause builds the WHERE fragment and its arguments for a
// query's filters, excluding the cursor predicate.
func namespaceFilterClause(q NamespaceQuery) ([]string, []any, error) {
	var where []string
	var args []any

	if q.Match != "" {
		switch q.MatchMode {
		case "", NamespaceMatchPrefix:
			where = append(where, `namespace LIKE ? ESCAPE '\'`)
			args = append(args, escapeLike(q.Match)+"%")
		case NamespaceMatchContains:
			where = append(where, `namespace LIKE ? ESCAPE '\'`)
			args = append(args, "%"+escapeLike(q.Match)+"%")
		case NamespaceMatchGlob:
			where = append(where, `namespace GLOB ?`)
			args = append(args, q.Match)
		default:
			return nil, nil, fmt.Errorf("%w: unknown match mode %q, want prefix|contains|glob", ErrNamespaceQuery, q.MatchMode)
		}
	} else if q.MatchMode != "" {
		// Validate the mode even when it cannot bind, so a caller learns about
		// a typo from the first call rather than from the first non-empty one.
		switch q.MatchMode {
		case NamespaceMatchPrefix, NamespaceMatchContains, NamespaceMatchGlob:
		default:
			return nil, nil, fmt.Errorf("%w: unknown match mode %q, want prefix|contains|glob", ErrNamespaceQuery, q.MatchMode)
		}
	}

	if q.OwnerType != "" {
		where = append(where, `owner_type = ?`)
		args = append(args, q.OwnerType)
	}
	if q.OwnerID != "" {
		where = append(where, `owner_id = ?`)
		args = append(args, q.OwnerID)
	}

	return where, args, nil
}

// namespaceCursor is the decoded form of NamespacePage.NextCursor. It carries
// the ordering it was issued under so a caller cannot resume it into a
// different one and silently get a page with holes in it.
type namespaceCursor struct {
	Version int      `json:"v"`
	Sort    string   `json:"s"`
	Desc    bool     `json:"d"`
	Keys    []string `json:"k"`
}

func encodeNamespaceCursor(sort NamespaceSortField, desc bool, keys []string) string {
	if sort == "" {
		sort = NamespaceSortNamespace
	}
	raw, err := json.Marshal(namespaceCursor{Version: 1, Sort: string(sort), Desc: desc, Keys: keys})
	if err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeNamespaceCursor(token string, sort NamespaceSortField, desc bool, wantKeys int) ([]string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return nil, fmt.Errorf("%w: cursor is not a valid pagination token", ErrNamespaceQuery)
	}
	var cur namespaceCursor
	if err := json.Unmarshal(raw, &cur); err != nil {
		return nil, fmt.Errorf("%w: cursor is not a valid pagination token", ErrNamespaceQuery)
	}
	if cur.Version != 1 {
		return nil, fmt.Errorf("%w: cursor version %d is not supported", ErrNamespaceQuery, cur.Version)
	}
	if sort == "" {
		sort = NamespaceSortNamespace
	}
	if cur.Sort != string(sort) || cur.Desc != desc {
		return nil, fmt.Errorf("%w: cursor was issued for sort=%s desc=%t and cannot be resumed under sort=%s desc=%t",
			ErrNamespaceQuery, cur.Sort, cur.Desc, sort, desc)
	}
	if len(cur.Keys) != wantKeys {
		return nil, fmt.Errorf("%w: cursor does not match the requested ordering", ErrNamespaceQuery)
	}
	return cur.Keys, nil
}

// namespaceKeysetClause builds the "everything strictly after this row" test
// for a keyset cursor over cols, expanded into an OR of AND-chains:
//
//	(c1 > ?) OR (c1 = ? AND c2 > ?) OR (c1 = ? AND c2 = ? AND c3 > ?)
//
// Row-value comparison would say the same thing in one line, but SQLite only
// grew it in 3.15 and the expanded form is what every version optimizes.
func namespaceKeysetClause(cols []string, keys []string, desc bool) (string, []any) {
	op := ">"
	if desc {
		op = "<"
	}
	var branches []string
	var args []any
	for i := range cols {
		var terms []string
		for j := 0; j < i; j++ {
			terms = append(terms, cols[j]+" = ?")
			args = append(args, keys[j])
		}
		terms = append(terms, cols[i]+" "+op+" ?")
		args = append(args, keys[i])
		branches = append(branches, "("+strings.Join(terms, " AND ")+")")
	}
	return "(" + strings.Join(branches, " OR ") + ")", args
}

// namespaceRowKeys extracts the ordering key of an entry for the given sort
// columns, so it can be encoded into the next cursor.
func namespaceRowKeys(entry NamespacePolicyEntry, cols []string) []string {
	keys := make([]string, len(cols))
	for i, col := range cols {
		switch col {
		case "namespace":
			keys[i] = entry.Namespace
		case "owner_type":
			keys[i] = entry.OwnerType
		case "owner_id":
			keys[i] = entry.OwnerID
		case "updated_at":
			keys[i] = entry.UpdatedAt
		}
	}
	return keys
}

// ListNamespacePolicyPage is the single entry point for reading the namespace
// registry with filtering, sorting and paging. Every narrowing happens in SQL:
// no caller loads the whole table to discard most of it, and no transport
// re-implements the filter.
func (s *Store) ListNamespacePolicyPage(ctx context.Context, q NamespaceQuery) (NamespacePage, error) {
	cols, err := namespaceSortColumns(q.Sort)
	if err != nil {
		return NamespacePage{}, err
	}
	where, args, err := namespaceFilterClause(q)
	if err != nil {
		return NamespacePage{}, err
	}

	// The cursor predicate rides on top of the filters and must NOT be counted
	// in Total — Total is the size of the whole matching set, which is what
	// makes "1000 of 1125" legible to a caller.
	filterWhere := append([]string(nil), where...)
	filterArgs := append([]any(nil), args...)

	if q.Cursor != "" {
		keys, cursorErr := decodeNamespaceCursor(q.Cursor, q.Sort, q.Desc, len(cols))
		if cursorErr != nil {
			return NamespacePage{}, cursorErr
		}
		clause, keyArgs := namespaceKeysetClause(cols, keys, q.Desc)
		where = append(where, clause)
		args = append(args, keyArgs...)
	}

	dir := " ASC"
	if q.Desc {
		dir = " DESC"
	}
	orderTerms := make([]string, len(cols))
	for i, col := range cols {
		orderTerms[i] = col + dir
	}

	query := `
SELECT namespace, owner_type, owner_id, COALESCE(policy_json, ''), updated_at
FROM namespace_policies`
	if len(where) > 0 {
		query += "\nWHERE " + strings.Join(where, " AND ")
	}
	// #nosec G202 -- orderTerms is built only from namespaceSortColumns, which
	// returns a closed allowlist of column names and rejects anything else
	// before a statement exists (see TestNamespaceQueryRejectsUnknownKnobs).
	// A column name cannot be a bound parameter, so this is the one place the
	// statement is assembled rather than parameterized; keeping ORDER BY and
	// the keyset columns derived from the SAME list is what stops the two from
	// drifting and silently breaking paging.
	query += "\nORDER BY " + strings.Join(orderTerms, ", ")

	// Over-read by one to learn whether another page exists without a second
	// round trip; the extra row is dropped before it reaches the caller. Bound
	// rather than formatted in, so no caller-supplied number is ever spliced
	// into the statement text.
	if q.Limit > 0 {
		query += "\nLIMIT ?"
		args = append(args, q.Limit+1)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return NamespacePage{}, err
	}
	defer func() { _ = rows.Close() }()

	var out []NamespacePolicyEntry
	for rows.Next() {
		var entry NamespacePolicyEntry
		var policyJSON string
		if err := rows.Scan(&entry.Namespace, &entry.OwnerType, &entry.OwnerID, &policyJSON, &entry.UpdatedAt); err != nil {
			return NamespacePage{}, err
		}
		if strings.TrimSpace(policyJSON) != "" {
			var policy map[string]any
			if err := json.Unmarshal([]byte(policyJSON), &policy); err != nil {
				return NamespacePage{}, err
			}
			entry.Policy = policy
		}
		out = append(out, entry)
	}
	if err := rows.Err(); err != nil {
		return NamespacePage{}, err
	}

	page := NamespacePage{}
	hasMore := q.Limit > 0 && len(out) > q.Limit
	if hasMore {
		out = out[:q.Limit]
	}
	page.Items = out
	if hasMore && len(out) > 0 {
		page.NextCursor = encodeNamespaceCursor(q.Sort, q.Desc, namespaceRowKeys(out[len(out)-1], cols))
	}

	// An unpaged, uncursored read has already counted the whole set by
	// reading it, so skip the COUNT round trip in the common full-list case.
	if q.Limit == 0 && q.Cursor == "" {
		page.Total = len(out)
		return page, nil
	}

	countQuery := `SELECT COUNT(*) FROM namespace_policies`
	if len(filterWhere) > 0 {
		countQuery += " WHERE " + strings.Join(filterWhere, " AND ")
	}
	if err := s.db.QueryRowContext(ctx, countQuery, filterArgs...).Scan(&page.Total); err != nil {
		return NamespacePage{}, err
	}
	return page, nil
}
