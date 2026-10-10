//go:build darwin || freebsd || netbsd || openbsd || dragonfly

package contextcli

import "golang.org/x/sys/unix"

// fdIsTerminal asks the kernel whether fd has terminal attributes.
func fdIsTerminal(fd int) (terminal, known bool) {
	_, err := unix.IoctlGetTermios(fd, unix.TIOCGETA)
	return err == nil, true
}
