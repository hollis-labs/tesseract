//go:build linux

package contextcli

import (
	"os"
	"testing"
)

// A pty master answers the terminal-attributes query, so it is the portable way
// to exercise the positive detection without a real console.
func TestWriterIsTerminalDetectsAPty(t *testing.T) {
	pty, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		t.Skip("no pty available")
	}
	defer pty.Close()
	if !writerIsTerminal(pty) {
		t.Fatal("a pty was not detected as a terminal")
	}
}
