package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseMCPTokenSources(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	tokenFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenFile, []byte(" file-token\n"), 0600); err != nil {
		t.Fatal(err)
	}
	emptyFile := filepath.Join(t.TempDir(), "empty")
	if err := os.WriteFile(emptyFile, []byte(" \n"), 0600); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, env string
		args      []string
		want      string
		wantErr   bool
	}{
		{name: "unscoped"},
		{name: "legacy flag", args: []string{"--token", " flag-token "}, want: "flag-token"},
		{name: "environment without argv", env: " env-token\n", want: "env-token"},
		{name: "environment overrides flag", env: "env-token", args: []string{"--token", "flag-token"}, want: "env-token"},
		{name: "blank environment falls back", env: " \n", args: []string{"--token", "flag-token"}, want: "flag-token"},
		{name: "file without token argv", args: []string{"--token-file", tokenFile}, want: "file-token"},
		{name: "file overrides environment and flag", env: "env-token", args: []string{"--token-file", tokenFile, "--token", "flag-token"}, want: "file-token"},
		{name: "missing file fails closed", env: "env-token", args: []string{"--token-file", tokenFile + "-missing"}, wantErr: true},
		{name: "empty file fails closed", env: "env-token", args: []string{"--token-file", emptyFile}, wantErr: true},
		{name: "invalid flag", env: "env-token", args: []string{"--invalid"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("TESSERACT_MCP_TOKEN", tt.env)
			got, err := parseMCPArgs(tt.args)
			if (err != nil) != tt.wantErr {
				t.Fatalf("unexpected error state: %v", err)
			}
			if got != tt.want {
				t.Fatal("unexpected token source")
			}
		})
	}
}
