package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestParseMCPSourceRereadsTokenFilePath proves --token-file keeps working for
// launchers that refresh the file in place (Tether agent tokens).
func TestParseMCPSourceRereadsTokenFilePath(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("TESSERACT_MCP_TOKEN", "")
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte("first\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	initial, source, err := parseMCPSource([]string{"--token-file", path})
	if err != nil || initial != "first" {
		t.Fatalf("initial = %q, %v", initial, err)
	}
	if err := os.WriteFile(path, []byte("second\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, _ := source(); got != "second" {
		t.Fatalf("token file not re-read: %q", got)
	}
	// a transient failure keeps the last good value instead of dropping auth mid-session
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if got, _ := source(); got != "second" {
		t.Fatalf("a rejected re-read must keep the last good token, got %q", got)
	}
}

func TestParseMCPSourceFixedForNonFileSources(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("TESSERACT_MCP_TOKEN", "")
	initial, source, err := parseMCPSource([]string{"--token", "literal"})
	if err != nil || initial != "literal" {
		t.Fatalf("initial = %q, %v", initial, err)
	}
	if got, _ := source(); got != "literal" {
		t.Fatalf("fixed source = %q", got)
	}
	if _, _, err := parseMCPSource([]string{"--token-file", filepath.Join(t.TempDir(), "missing")}); err == nil {
		t.Fatal("a missing token file must still fail closed")
	}
}
