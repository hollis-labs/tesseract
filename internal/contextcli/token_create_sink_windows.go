//go:build windows

package contextcli

// Windows has no POSIX owner/mode/no-follow semantics to verify, so a private
// sink is refused rather than approximated.
func openTokenSink(string, int) (*tokenSink, error) { return nil, errTokenSink }
