package config

import (
	"path/filepath"

	"github.com/hollis-labs/go-apppaths/paths"

	"github.com/hollis-labs/tesseract/internal/fsperm"
)

// AppName is Tesseract's go-apppaths application identity. It drives the XDG
// roots (~/.local/share/tesseract, ~/.local/state/tesseract,
// ~/.config/tesseract) and the TESSERACT_* env-var prefix go-apppaths reads
// natively (TESSERACT_DB_PATH, TESSERACT_WORKSPACE).
const AppName = "tesseract"

// ResolveLayout resolves Tesseract's on-disk layout via go-apppaths in the
// default (XDG) mode. Project mode is deliberately not used.
//
// No WithLegacyNames is passed: the only pre-migration data is the hand-made
// ~/.tesseract dotdir, which WithLegacyNames cannot reach (it only adopts
// XDG-root → XDG-root keyed by app name). Moving that dotdir is the explicit
// operational data move in the cutover runbook, not something the path lib
// can do — see CW-20260517-0066.
//
// Callers that only introspect (the `tesseract path` subcommand) pass
// paths.WithoutMaterialize() so resolution does not create directories.
func ResolveLayout(extra ...paths.Option) (paths.Layout, error) {
	layout, err := paths.Resolve(AppName, extra...)
	if err != nil {
		return layout, err
	}
	return layout, tightenLayout(layout)
}

// tightenLayout makes the roots go-apppaths creates owner-only.
//
// go-apppaths materializes every root at 0755 and writes active_workspace at
// 0644, and it exposes no mode option — so the four XDG roots, the workspace
// directory and the workspace DB directory would otherwise stay
// world-listable no matter how carefully the packages below them write. Each
// of these paths is keyed by AppName and belongs entirely to Tesseract, so
// tightening them is not reaching into anything an operator chose.
//
// This is deliberately not recursive. Everything beneath these roots has an
// owner that already applies the same policy on the way in — contextstore for
// the records tree and the database, config for config.yaml, contextapi for
// the config-backup tree — and a blind walk from DataDir would also traverse
// a DBPath an embedding caller pointed somewhere of their own choosing.
//
// Missing paths are skipped, so callers that pass paths.WithoutMaterialize()
// (the `tesseract path` subcommand) still resolve without creating or
// touching anything.
func tightenLayout(layout paths.Layout) error {
	owned := []string{
		layout.DataDir(),
		layout.StateDir(),
		layout.CacheDir(),
		layout.ConfigDir(),
		layout.Workspace().Dir,
		filepath.Dir(layout.Workspace().DBPath),
		filepath.Join(layout.StateDir(), "active_workspace"),
	}
	for _, path := range owned {
		if err := fsperm.TightenPath(path); err != nil {
			return err
		}
	}
	return nil
}
