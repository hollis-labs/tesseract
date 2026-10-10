//go:build !linux && !darwin && !freebsd && !netbsd && !openbsd && !dragonfly

package contextcli

// fdIsTerminal has no kernel query on this platform; writerIsTerminal falls
// back to its conservative character-device rule.
func fdIsTerminal(int) (terminal, known bool) { return false, false }
