package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/hollis-labs/tesseract/internal/config"
	"github.com/hollis-labs/tesseract/internal/contextstore"
	"github.com/hollis-labs/tesseract/internal/memory"
)

// runNoveltyFitBasis fits the frozen PCA-16 basis that ν's second series is
// measured in (CW-20260911-0043).
//
// This is a CLI command and not an HTTP route or an MCP tool on purpose.
// Fitting is one-shot, offline, and takes minutes of CPU over the whole corpus;
// nothing about it belongs on a serving surface, and keeping it off one is what
// guarantees no request path can cause a refit as a side effect.
//
// It needs no embedding provider and no memory subsystem — it reads vectors
// that are already stored and writes one row — which is why it works from the
// CLI at all, where memory and knowledge are otherwise unreachable.
func runNoveltyFitBasis(ctx context.Context, store *contextstore.Store, cfg config.Config, args []string, stdout, stderr *os.File) int {
	fs := flag.NewFlagSet("novelty-fit-basis", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	model := fs.String("model", "", "embedding model to fit for (default: the configured model)")
	snapshot := fs.String("snapshot-at", "", "RFC3339 instant bounding the snapshot (default: now)")
	replace := fs.Bool("replace", false, "fit a new basis even though one already exists")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}

	embeddingModel := *model
	if embeddingModel == "" {
		embeddingModel = cfg.Embedding.Model
	}
	if embeddingModel == "" {
		fmt.Fprintln(stderr, "error: no embedding model configured; pass -model")
		return 1
	}
	snapshotAt := *snapshot
	if snapshotAt == "" {
		snapshotAt = time.Now().UTC().Format(time.RFC3339Nano)
	}

	fmt.Fprintf(stdout, "fitting a frozen basis for %s over the snapshot at %s\n", embeddingModel, snapshotAt)
	started := time.Now()
	report, err := memory.FitNoveltyBasis(ctx, store.DB(), embeddingModel, snapshotAt, *replace)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	elapsed := time.Since(started)

	fmt.Fprintf(stdout, "\nbasis %s\n", report.Version)
	if report.Replaced != "" {
		fmt.Fprintf(stdout, "  REPLACED        %s (rows already scored keep their own stamp)\n", report.Replaced)
	}
	fmt.Fprintf(stdout, "  snapshot        %d vectors at %s\n", report.SnapshotN, report.SnapshotAt)
	fmt.Fprintf(stdout, "  snapshot hash   %s\n", report.SnapshotHash)
	fmt.Fprintf(stdout, "  projection      %d -> %d\n", report.SourceDim, report.TargetDim)
	fmt.Fprintf(stdout, "  fit             %s, seed %d, %d iterations, %s\n",
		elapsed.Round(time.Millisecond), report.Seed, report.Iterations, report.Algorithm)
	fmt.Fprintf(stdout, "  explained var   %.4f of total %.6g\n",
		report.ExplainedVarianceRatio, report.TotalVariance)
	fmt.Fprintf(stdout, "  eigenvalues     ")
	for i, ev := range report.EigenValues {
		if i > 0 {
			fmt.Fprintf(stdout, " ")
		}
		fmt.Fprintf(stdout, "%.4g", ev)
	}
	fmt.Fprintln(stdout)

	// Said here rather than only in a comment, because it is the one operational
	// consequence of caching the basis for the life of a Store.
	fmt.Fprintln(stdout, "\nA running daemon will not use this basis until it restarts.")
	fmt.Fprintln(stdout, "Revisions embedded before this fit carry NULL in the projected columns;")
	fmt.Fprintln(stdout, "ν is an embed-time value and is never recomputed.")
	return 0
}
