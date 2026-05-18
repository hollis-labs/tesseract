package main

import (
	"os"
)

// applyContextdRootShim is the one-release deprecation shim for the retired
// CONTEXTD_ROOT environment variable (CW-20260517-0066, go-apppaths migration).
//
// Before this migration, CONTEXTD_ROOT was a single base directory that every
// on-disk path nested under ($ROOT/data/index/context.db, $ROOT/data/queue.db,
// $ROOT/data/records, $ROOT/config.yaml). go-apppaths deliberately splits
// those into independent XDG data/state/config roots, and there is no native
// "one base for everything" knob in XDG mode.
//
// To preserve the "everything under one base" contract for existing callers
// (the e2e script, the Makefile contract targets) for one release, this shim
// maps a set CONTEXTD_ROOT onto all four $XDG_*_HOME variables *before*
// config.ResolveLayout runs. The app roots then resolve to
// <CONTEXTD_ROOT>/tesseract for data/state/cache/config — every file still
// nests under CONTEXTD_ROOT.
//
// CONTEXTD_ROOT is set → win over any pre-existing $XDG_*_HOME, matching the
// legacy contract that CONTEXTD_ROOT alone controlled the whole layout.
//
// REMOVAL: this shim is scheduled for removal one release after the
// go-apppaths cutover. When it goes, callers switch to setting $XDG_*_HOME
// directly (or TESSERACT_DB_PATH / TESSERACT_WORKSPACE for the common cases).
// Deleting this file plus the single call site in run() removes it cleanly.
func applyContextdRootShim(stderr *os.File) {
	root := os.Getenv("CONTEXTD_ROOT")
	if root == "" {
		return
	}
	_, _ = stderr.WriteString(
		"warning: CONTEXTD_ROOT is deprecated and will be removed after the " +
			"go-apppaths migration (CW-20260517-0066). Mapping it onto " +
			"$XDG_DATA_HOME/$XDG_STATE_HOME/$XDG_CACHE_HOME/$XDG_CONFIG_HOME " +
			"for this run; switch to the $XDG_*_HOME vars, TESSERACT_DB_PATH, " +
			"or TESSERACT_WORKSPACE.\n")
	for _, xdg := range []string{
		"XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME", "XDG_CONFIG_HOME",
	} {
		_ = os.Setenv(xdg, root)
	}
}
