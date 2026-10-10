package contextcli

import (
	"io"
	"os"
)

// writerIsTerminal reports whether w is an interactive terminal. On platforms
// with a terminal-attributes ioctl it asks the kernel; elsewhere it falls back
// to "a character device other than the null device", which can only over-refuse
// (a refusal points the operator at --secret-file), never under-refuse.
func writerIsTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	conn, err := f.SyscallConn()
	if err != nil {
		return false
	}
	terminal := false
	known := false
	_ = conn.Control(func(fd uintptr) { terminal, known = fdIsTerminal(int(fd)) })
	if known {
		return terminal
	}
	info, err := f.Stat()
	if err != nil || info.Mode()&os.ModeCharDevice == 0 {
		return false
	}
	if null, err := os.Stat(os.DevNull); err == nil && os.SameFile(info, null) {
		return false
	}
	return true
}
