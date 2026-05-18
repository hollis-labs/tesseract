package main

import (
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/hollis-labs/go-apppaths/paths"
	"github.com/hollis-labs/tesseract/internal/config"
)

// runPath implements the `contextd path` subcommand. It prints Tesseract's
// resolved on-disk layout — the go-apppaths roots, the active workspace, and
// the main database — plus the contextd-derived extras (config.yaml, the
// records/ file tree, and queue.db). It is the introspection surface the
// go-apppaths cutover uses to confirm where the daemon reads and writes.
//
// Resolution honors every override the running daemon would see: the
// CONTEXTD_ROOT deprecation shim (applied by run() before dispatch),
// TESSERACT_DB_PATH, TESSERACT_WORKSPACE, and the $XDG_*_HOME vars. It uses
// paths.WithoutMaterialize() so introspection never creates directories.
func runPath(stdout, stderr *os.File) int {
	layout, err := config.ResolveLayout(paths.WithoutMaterialize())
	if err != nil {
		_, _ = stderr.WriteString("error: resolve layout: " + err.Error() + "\n")
		return 1
	}

	w := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	for _, e := range layout.Describe() {
		fmt.Fprintf(w, "%s\t%s\n", e.Label, e.Value)
	}
	fmt.Fprintf(w, "config-file\t%s\n", filepath.Join(layout.ConfigDir(), "config.yaml"))
	fmt.Fprintf(w, "records\t%s\n", filepath.Join(layout.StateDir(), "records"))
	fmt.Fprintf(w, "queue-db\t%s\n", filepath.Join(layout.StateDir(), "queue.db"))
	if err := w.Flush(); err != nil {
		_, _ = stderr.WriteString("error: " + err.Error() + "\n")
		return 1
	}
	return 0
}
