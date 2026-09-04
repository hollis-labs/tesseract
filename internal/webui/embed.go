// Package webui serves the embedded Tesseract frontend as an SPA.
//
// The SPA routing itself lives in github.com/hollis-labs/go-webui, which was
// extracted (CW-20260515-0127) from the copy of this file that Tesseract and
// Fragments Engine had each grown independently. This package keeps only what
// is genuinely Tesseract's: the //go:embed of its own bundle and the mount
// point. go-webui deliberately ships no assets, so the host owns the embed.
package webui

import (
	"embed"
	"io/fs"
	"net/http"

	gowebui "github.com/hollis-labs/go-webui"
)

//go:embed all:dist
var distFS embed.FS

// Handler returns an http.Handler that serves the embedded frontend at the
// site root, where cmd/tesseract mounts it as the catch-all behind /v1/.
//
// Routing comes from go-webui: a real file is served as itself, an
// extension-less path that matches no file falls back to index.html so
// client-side routes resolve, and a path with an extension that matches no
// file is a 404 rather than an HTML body served under the asset's name.
//
// When dist holds no built bundle the returned handler serves go-webui's
// placeholder page instead. The previous implementation panicked, which made
// a fresh clone that had not yet run `make frontend` fail at daemon start
// rather than at the one page that could explain the problem.
func Handler() http.Handler {
	// fs.Sub only fails on a malformed path, so this cannot trip while the
	// //go:embed above compiles. A nil FS still selects placeholder mode,
	// which is the right degradation for an unreachable branch: a broken
	// bundle should cost a page, not the process.
	dist, err := fs.Sub(distFS, "dist")
	if err != nil {
		dist = nil
	}

	return gowebui.Handler(gowebui.Config{
		FS:       dist,
		BasePath: "/",
	})
}
