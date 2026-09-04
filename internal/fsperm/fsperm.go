// Package fsperm holds Tesseract's on-disk permission policy and the helpers
// that enforce it.
//
// Everything Tesseract writes for itself is owner-only: 0700 for directories,
// 0600 for files. That covers the SQLite database (which stores auth token
// verifiers), the payload record tree, the embed-queue state, config.yaml
// (which carries provider credentials) and the config backup tree beside it.
// Before CW-20260904-0078 every one of those paths was created 0755/0644, so
// on a multi-account machine any local user could read the store. The backup
// writer in internal/contextstore was hardened first and established the
// pattern this package generalizes.
//
// # Why the mode is always set a second time
//
// Passing a mode to MkdirAll or WriteFile is not enough, for two independent
// reasons:
//
//   - Both are masked by the process umask, so the environment the daemon
//     happened to start in gets a vote on the result.
//   - Neither applies its mode to a path that already exists. MkdirAll is a
//     no-op on an existing directory, and O_CREATE's mode is ignored when the
//     file is already there — which is exactly the case for every install
//     created by an older build, whose 0755/0644 would otherwise persist
//     forever.
//
// So every helper here creates and then chmods.
//
// # Owned paths versus paths the operator named
//
// These helpers force a mode onto whatever they are handed, which is only
// defensible for paths Tesseract owns. The line is:
//
//   - Owned, and force-tightened: directories Tesseract creates for its own
//     use, and files Tesseract writes into them. TightenTree additionally
//     walks the record tree, whose entire contents Tesseract generated.
//   - Not owned, and left alone: any path the operator named. A backup --out
//     directory, a restore source, and the plugins directory (./plugins by
//     default, overridden wholesale by TESSERACT_PLUGINS_DIR) may all be
//     shared with other tools or other people. Tesseract creates those with
//     the ordinary 0755 and never chmods them; deciding another owner's
//     directory should be 0700 is not Tesseract's call to make.
//
// Adding a caller means placing the path on one side of that line first.
package fsperm

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
)

const (
	// DirMode is the mode for a directory Tesseract owns.
	DirMode os.FileMode = 0o700
	// FileMode is the mode for a file Tesseract owns.
	FileMode os.FileMode = 0o600
)

// Supported reports whether the platform has POSIX mode semantics worth
// enforcing. Windows maps os.Chmod onto the read-only attribute alone, so
// 0600 there is neither achievable nor meaningful: the helpers still create
// the paths, they just do not pretend to a mode they cannot deliver, and the
// permission tests skip rather than fail. runtime.GOOS is a constant, so this
// folds away at compile time on both platforms.
const Supported = runtime.GOOS != "windows"

// EnsureDir creates dir and any missing parents, then forces DirMode on dir
// itself — including when dir already existed at a looser mode.
func EnsureDir(dir string) error {
	if err := os.MkdirAll(dir, DirMode); err != nil {
		return err
	}
	return chmod(dir, DirMode)
}

// EnsureTree guarantees that dir and everything already beneath it carry the
// owner-only modes, creating dir when it does not exist.
//
// The tighten pass runs before the create pass, and that order is load-bearing
// rather than incidental: EnsureDir would set dir to DirMode, and dir's own
// mode is precisely the marker TightenTree reads to decide the tree below it
// has already been converted. Reversing the two would silently leave a legacy
// tree's contents at 0644 forever.
//
// Only pass a directory whose entire contents Tesseract wrote. See the package
// comment for that boundary.
func EnsureTree(dir string) error {
	if err := TightenTree(dir); err != nil {
		return err
	}
	return EnsureDir(dir)
}

// WriteFile writes data to path at FileMode, forcing the mode even when the
// file already existed at a looser one.
func WriteFile(path string, data []byte) error {
	if err := os.WriteFile(path, data, FileMode); err != nil {
		return err
	}
	return chmod(path, FileMode)
}

// TightenPath forces the owner-only mode on a single existing path, choosing
// DirMode or FileMode from what it finds.
//
// A missing path is not an error: callers use this for optional companions
// such as SQLite's -wal and -shm sidecars, which exist only some of the time.
func TightenPath(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	// A symlink's own mode means nothing, and chmod would follow it to a
	// target Tesseract may not own.
	if info.Mode()&fs.ModeSymlink != 0 {
		return nil
	}
	if info.IsDir() {
		return chmod(path, DirMode)
	}
	return chmod(path, FileMode)
}

// TightenTree forces the owner-only modes on root and everything beneath it,
// so an install created by an older build converges on the next open.
//
// Only pass a tree whose entire contents Tesseract wrote — the record tree,
// the config backup directory. Never a path the operator named; see the
// package comment.
//
// The walk is skipped when root already carries DirMode, because doing it on
// every open would mean a full walk of a store that can hold hundreds of
// thousands of payload files. Root is therefore tightened last, after its
// children, so its mode is an honest marker: a pass that dies part way leaves
// root loose and is simply retried on the next open, rather than being
// recorded as done over a tree that is still half converted.
func TightenTree(root string) error {
	info, err := os.Stat(root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return TightenPath(root)
	}
	if !Supported || info.Mode().Perm() == DirMode {
		return nil
	}
	err = filepath.WalkDir(root, func(path string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == root {
			return nil
		}
		return TightenPath(path)
	})
	if err != nil {
		return err
	}
	return chmod(root, DirMode)
}

func chmod(path string, mode os.FileMode) error {
	if !Supported {
		return nil
	}
	return os.Chmod(path, mode)
}
