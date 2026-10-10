//go:build !windows

package contextcli

import (
	"os"
	"path/filepath"
	"syscall"
)

// openTokenSink creates the sink exclusively. euid is the identity that must
// own the parent directory and the new file.
//
// The parent is lstat'ed (a symlink is refused), opened ONCE as an os.Root
// (dirfd-relative), and its identity is tied to the lstat result with SameFile.
// Ownership and mode are then checked on the opened handle: a real directory,
// owned by euid, no group/other write. Everything after that is relative to
// that handle: the sink name must be absent, and is created with
// O_CREATE|O_EXCL|O_NOFOLLOW mode 0600, forced to 0600 against the umask, and
// verified through the open descriptor (regular, owned, single link, same
// device as the parent). Nothing is renamed, so there is no cross-filesystem
// case. Ancestors of the parent are resolved once, when the directory is
// opened; only the verified directory identity is relied on afterwards. A
// failure after the file exists removes it through the same handle, because
// nothing has been minted yet.
func openTokenSink(path string, euid int) (*tokenSink, error) {
	if path == "" {
		return nil, errTokenSink
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, errTokenSink
	}
	parent, base := filepath.Dir(abs), filepath.Base(abs)
	if base == "." || base == ".." || base == string(filepath.Separator) {
		return nil, errTokenSink
	}
	linfo, err := os.Lstat(parent)
	if err != nil || !linfo.IsDir() {
		return nil, errTokenSink
	}
	root, err := os.OpenRoot(parent)
	if err != nil {
		return nil, errTokenSink
	}
	refuse := func() (*tokenSink, error) {
		_ = root.Close()
		return nil, errTokenSink
	}
	pinfo, err := root.Stat(".")
	if err != nil || !pinfo.IsDir() || !os.SameFile(linfo, pinfo) || pinfo.Mode().Perm()&0o022 != 0 {
		return refuse()
	}
	pst, ok := pinfo.Sys().(*syscall.Stat_t)
	if !ok || int(pst.Uid) != euid {
		return refuse()
	}
	if _, err := root.Lstat(base); !os.IsNotExist(err) {
		return refuse()
	}
	f, err := root.OpenFile(base, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return refuse()
	}
	discard := func() (*tokenSink, error) {
		_ = f.Close()
		_ = root.Remove(base)
		return refuse()
	}
	if f.Chmod(0o600) != nil {
		return discard()
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		return discard()
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(st.Uid) != euid || uint64(st.Nlink) != 1 || uint64(st.Dev) != uint64(pst.Dev) {
		return discard()
	}
	return &tokenSink{file: f, root: root, base: base, path: abs, info: info}, nil
}
