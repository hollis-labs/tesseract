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
// Parent: a real directory (lstat, so a symlink is refused), owned by euid,
// with no group/other write bit. Sink: absent, then created with
// O_CREATE|O_EXCL|O_NOFOLLOW mode 0600, forced to 0600 against the umask, and
// verified through the open descriptor (regular, owned, single link, same
// device as the parent). Nothing is renamed, so there is no cross-filesystem
// case. A failure after the file exists removes it, because nothing has been
// minted yet.
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
	pinfo, err := os.Lstat(parent)
	if err != nil || !pinfo.IsDir() || pinfo.Mode().Perm()&0o022 != 0 {
		return nil, errTokenSink
	}
	pst, ok := pinfo.Sys().(*syscall.Stat_t)
	if !ok || int(pst.Uid) != euid {
		return nil, errTokenSink
	}
	if _, err := os.Lstat(abs); !os.IsNotExist(err) {
		return nil, errTokenSink
	}
	f, err := os.OpenFile(abs, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, errTokenSink
	}
	discard := func() {
		_ = f.Close()
		_ = os.Remove(abs)
	}
	if f.Chmod(0o600) != nil {
		discard()
		return nil, errTokenSink
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		discard()
		return nil, errTokenSink
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(st.Uid) != euid || uint64(st.Nlink) != 1 || uint64(st.Dev) != uint64(pst.Dev) {
		discard()
		return nil, errTokenSink
	}
	return &tokenSink{file: f, path: abs, parent: parent, info: info}, nil
}

// syncParentDir makes the new directory entry durable.
func syncParentDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	err = d.Sync()
	if closeErr := d.Close(); err == nil {
		err = closeErr
	}
	return err
}
