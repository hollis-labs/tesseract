//go:build linux

package contextcli

import "golang.org/x/sys/unix"

// fdIsTerminal asks the kernel whether fd has terminal attributes.
func fdIsTerminal(fd int) (terminal, known bool) {
	_, err := unix.IoctlGetTermios(fd, unix.TCGETS)
	return err == nil, true
}
