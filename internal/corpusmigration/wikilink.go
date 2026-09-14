package corpusmigration

import (
	"context"
	"database/sql"
	"fmt"
)

// WikilinkEdge represents one references edge in memory_links.
type WikilinkEdge struct {
	LinkID         int64  `json:"link_id"`
	FromRevisionID string `json:"from_revision_id"`
	FromMemoryID   string `json:"from_memory_id"`
	FromKey        string `json:"from_key"`
	FromNamespace  string `json:"from_namespace"`
	Relation       string `json:"relation"`
	Target         string `json:"target"`
	ToMemoryID     string `json:"to_memory_id"`
	ToKey          string `json:"to_key"`
	ToNamespace    string `json:"to_namespace"`
	Resolved       bool   `json:"resolved"`
}

// WikilinkGraph is an immutable snapshot of all wikilink reference edges.
type WikilinkGraph struct {
	TotalLinks    int                    `json:"total_links"`
	ResolvedLinks int                    `json:"resolved_links"`
	Edges         map[int64]WikilinkEdge `json:"edges"`
}

// WikilinkDiff summarizes the differences between two WikilinkGraph snapshots.
type WikilinkDiff struct {
	BeforeTotal    int            `json:"before_total"`
	AfterTotal     int            `json:"after_total"`
	BeforeResolved int            `json:"before_resolved"`
	AfterResolved  int            `json:"after_resolved"`
	BrokenLinks    []WikilinkEdge `json:"broken_links"` // Was resolved before, now unresolved (FATAL DEFECT)
	NewlyResolved  []WikilinkEdge `json:"newly_resolved"`
	ChangedTargets []WikilinkEdge `json:"changed_targets"`
}

// EnumerateWikilinkGraph reads all relation='references' rows from memory_links.
func EnumerateWikilinkGraph(ctx context.Context, db *sql.DB) (*WikilinkGraph, error) {
	query := `
SELECT 
    l.link_id,
    l.from_revision_id,
    l.from_memory_id,
    COALESCE(fs.memory_key, ''),
    COALESCE(fs.namespace, ''),
    l.relation,
    l.target,
    COALESCE(l.to_memory_id, ''),
    COALESCE(ts.memory_key, ''),
    COALESCE(ts.namespace, ''),
    (l.to_memory_id IS NOT NULL AND l.to_memory_id <> '')
FROM memory_links l
LEFT JOIN memory_state fs ON fs.memory_id = l.from_memory_id
LEFT JOIN memory_state ts ON ts.memory_id = l.to_memory_id
WHERE l.relation = 'references'
ORDER BY l.link_id`

	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("enumerate wikilink graph: %w", err)
	}
	defer func() { _ = rows.Close() }()

	edges := make(map[int64]WikilinkEdge)
	resolvedCount := 0

	for rows.Next() {
		var e WikilinkEdge
		if err := rows.Scan(
			&e.LinkID,
			&e.FromRevisionID,
			&e.FromMemoryID,
			&e.FromKey,
			&e.FromNamespace,
			&e.Relation,
			&e.Target,
			&e.ToMemoryID,
			&e.ToKey,
			&e.ToNamespace,
			&e.Resolved,
		); err != nil {
			return nil, fmt.Errorf("scan wikilink edge: %w", err)
		}
		if e.Resolved {
			resolvedCount++
		}
		edges[e.LinkID] = e
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate wikilink edges: %w", err)
	}

	return &WikilinkGraph{
		TotalLinks:    len(edges),
		ResolvedLinks: resolvedCount,
		Edges:         edges,
	}, nil
}

// DiffWikilinkGraphs compares the before and after snapshots of the wikilink graph.
func DiffWikilinkGraphs(before, after *WikilinkGraph) *WikilinkDiff {
	diff := &WikilinkDiff{
		BeforeTotal:    before.TotalLinks,
		AfterTotal:     after.TotalLinks,
		BeforeResolved: before.ResolvedLinks,
		AfterResolved:  after.ResolvedLinks,
	}

	for id, beforeEdge := range before.Edges {
		afterEdge, exists := after.Edges[id]
		if !exists {
			// Link disappeared
			if beforeEdge.Resolved {
				diff.BrokenLinks = append(diff.BrokenLinks, beforeEdge)
			}
			continue
		}

		if beforeEdge.Resolved && !afterEdge.Resolved {
			// Defect: was resolved before, but unresolved after!
			diff.BrokenLinks = append(diff.BrokenLinks, afterEdge)
		} else if !beforeEdge.Resolved && afterEdge.Resolved {
			diff.NewlyResolved = append(diff.NewlyResolved, afterEdge)
		} else if beforeEdge.ToMemoryID != afterEdge.ToMemoryID {
			diff.ChangedTargets = append(diff.ChangedTargets, afterEdge)
		}
	}

	return diff
}

// LineageEdge represents one supersedes edge in memory_links.
type LineageEdge struct {
	LinkID         int64  `json:"link_id"`
	FromRevisionID string `json:"from_revision_id"`
	FromMemoryID   string `json:"from_memory_id"`
	ToRevisionID   string `json:"to_revision_id"`
	ToMemoryID     string `json:"to_memory_id"`
	Target         string `json:"target"`
}

// LineageReport summarizes supersedes relations in the corpus.
type LineageReport struct {
	TotalLineageEdges int           `json:"total_lineage_edges"`
	SampleEdges       []LineageEdge `json:"sample_edges"`
}

// EnumerateLineage reads supersedes rows from memory_links.
func EnumerateLineage(ctx context.Context, db *sql.DB, sampleLimit int) (*LineageReport, error) {
	query := `
SELECT 
    l.link_id,
    l.from_revision_id,
    l.from_memory_id,
    COALESCE(l.to_revision_id, ''),
    COALESCE(l.to_memory_id, ''),
    l.target
FROM memory_links l
WHERE l.relation = 'supersedes'
ORDER BY l.link_id`

	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("enumerate lineage: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var samples []LineageEdge
	total := 0

	for rows.Next() {
		var e LineageEdge
		if err := rows.Scan(
			&e.LinkID,
			&e.FromRevisionID,
			&e.FromMemoryID,
			&e.ToRevisionID,
			&e.ToMemoryID,
			&e.Target,
		); err != nil {
			return nil, fmt.Errorf("scan lineage edge: %w", err)
		}
		total++
		if len(samples) < sampleLimit {
			samples = append(samples, e)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate lineage: %w", err)
	}

	return &LineageReport{
		TotalLineageEdges: total,
		SampleEdges:       samples,
	}, nil
}
