// Package runtimeimage verifies executable identity without managing processes.
// The shared comparison and race handling live in go-mcp/staleness.
package runtimeimage

import (
	"context"
	"debug/buildinfo"
	"fmt"
	"runtime"
	"runtime/debug"

	"github.com/hollis-labs/libs/plugin-mcp/go-mcp/staleness"
)

// Running binds a digest to the running image using platform evidence. It must
// never fall back to hashing os.Executable(), a version label, or a VCS revision.
func Running() (staleness.Identity, error) {
	info, ok := debug.ReadBuildInfo()
	if !ok || info.Path == "" {
		return staleness.Identity{}, fmt.Errorf("running product identity unavailable")
	}
	scheme, digest, err := runningDigest()
	if err != nil {
		return staleness.Identity{}, err
	}
	return staleness.Identity{Scheme: scheme, Digest: digest, Product: info.Path, Platform: runtime.GOOS + "/" + runtime.GOARCH}, nil
}

// Inspect validates the immutable snapshot supplied by staleness.Observe.
// Reading Go build metadata establishes the product/platform relation only;
// platform signature or content verification supplies the image identity.
func Inspect(ctx context.Context, path string) (staleness.Identity, error) {
	if err := ctx.Err(); err != nil {
		return staleness.Identity{}, err
	}
	info, err := buildinfo.ReadFile(path)
	if err != nil || info.Path == "" {
		return staleness.Identity{}, fmt.Errorf("candidate Go product identity unavailable")
	}
	var goos, goarch string
	for _, setting := range info.Settings {
		switch setting.Key {
		case "GOOS":
			goos = setting.Value
		case "GOARCH":
			goarch = setting.Value
		}
	}
	if goos != runtime.GOOS || goarch != runtime.GOARCH {
		return staleness.Identity{}, fmt.Errorf("candidate execution platform differs or is unavailable")
	}
	scheme, digest, err := candidateDigest(ctx, path)
	if err != nil {
		return staleness.Identity{}, err
	}
	return staleness.Identity{Scheme: scheme, Digest: digest, Product: info.Path, Platform: goos + "/" + goarch}, nil
}
