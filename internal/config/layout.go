package config

import (
	"github.com/hollis-labs/go-apppaths/paths"
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
// Callers that only introspect (the `contextd path` subcommand) pass
// paths.WithoutMaterialize() so resolution does not create directories.
func ResolveLayout(extra ...paths.Option) (paths.Layout, error) {
	return paths.Resolve(AppName, extra...)
}
