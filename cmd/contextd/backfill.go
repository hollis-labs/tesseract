package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/hollis-labs/vanta-conduit/internal/config"
	"github.com/hollis-labs/vanta-conduit/internal/contextstore"
	"github.com/hollis-labs/vanta-conduit/internal/memory"
)

func runBackfill(ctx context.Context, store *contextstore.Store, cfg config.Config, args []string, stdout, stderr *os.File) int {
	fs := flag.NewFlagSet("backfill-embeddings", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	namespace := fs.String("namespace", "", "filter by namespace (optional)")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}

	embedder := createEmbedder(cfg)
	if embedder == nil {
		fmt.Fprintln(stderr, "error: no embedding provider configured (check config.yaml and OPENAI_API_KEY)")
		return 1
	}

	ms := memory.NewStore(store.DB(), embedder, cfg.Embedding.Model, cfg.Dedup.SimilarityThreshold, nil)
	ms.SetAuditSink(store)

	// Query unembedded revisions.
	query := `SELECT revision_id FROM memory_revisions WHERE embedding_vector IS NULL`
	var queryArgs []interface{}
	if *namespace != "" {
		query += ` AND namespace = ?`
		queryArgs = append(queryArgs, *namespace)
	}
	query += ` ORDER BY created_at ASC`

	rows, err := store.DB().QueryContext(ctx, query, queryArgs...)
	if err != nil {
		fmt.Fprintf(stderr, "error: query unembedded revisions: %v\n", err)
		return 1
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			fmt.Fprintf(stderr, "error: scan: %v\n", err)
			return 1
		}
		ids = append(ids, id)
	}
	if rows.Err() != nil {
		fmt.Fprintf(stderr, "error: %v\n", rows.Err())
		return 1
	}

	total := len(ids)
	if total == 0 {
		fmt.Fprintln(stdout, "No unembedded revisions found.")
		return 0
	}

	fmt.Fprintf(stdout, "Found %d unembedded revision(s). Embedding with %s...\n", total, cfg.Embedding.Model)

	var errCount int
	for i, id := range ids {
		if ctx.Err() != nil {
			fmt.Fprintf(stderr, "\nInterrupted after %d/%d revisions.\n", i, total)
			return 1
		}

		if err := ms.EmbedRevision(ctx, id, cfg.Embedding.Model); err != nil {
			fmt.Fprintf(stderr, "[%d/%d] error embedding %s: %v\n", i+1, total, id, err)
			errCount++
			continue
		}
		fmt.Fprintf(stdout, "[%d/%d] embedded %s\n", i+1, total, id)
	}

	fmt.Fprintf(stdout, "\nEmbedded %d revision(s) (%d errors).\n", total-errCount, errCount)
	return 0
}
