//go:build !windows

package contextcli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/hollis-labs/tesseract/internal/contextstore"
)

func sinkCreateArgs(path string, extra ...string) []string {
	args := []string{"context", "token", "create", "--name", "issuer-admin", "--client-id", "admin:issuer-owner", "--scopes", "admin", "--namespaces", "never/matches/*", "--ttl", "720h", "--output", "json"}
	if path != "" {
		args = append(args, "--secret-file", path)
	}
	return append(args, extra...)
}

// privateDir returns an owner-only temp dir; t.TempDir creates the numbered
// directory 0777 before the umask, which the sink's parent check rightly refuses.
func privateDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal("private dir fixture failed")
	}
	return dir
}

func tokenCount(t *testing.T, cli *CLI) int {
	t.Helper()
	tokens, err := cli.Store.ListAuthTokens(context.Background(), 100)
	if err != nil {
		t.Fatal("token metadata read failed")
	}
	return len(tokens)
}

func setTerminal(t *testing.T, v bool) {
	t.Helper()
	prev := tokenOutputIsTerminal
	tokenOutputIsTerminal = func(io.Writer) bool { return v }
	t.Cleanup(func() { tokenOutputIsTerminal = prev })
}

type sinkMetadata struct {
	ID              string   `json:"id"`
	Scopes          []string `json:"scopes"`
	NamespaceGlobs  []string `json:"namespace_globs"`
	SecretFile      string   `json:"secret_file"`
	SecretDelivered bool     `json:"secret_delivered"`
	Token           string   `json:"token"`
}

func TestTokenCreateSinkDeliversOnlyToPrivateFile(t *testing.T) {
	for _, umask := range []int{0o000, 0o022, 0o777} {
		t.Run(strings.ReplaceAll(os.FileMode(umask).String(), "-", "_"), func(t *testing.T) {
			cli, out, errOut := newTestCLI(t)
			path := filepath.Join(privateDir(t), "admin.token")
			prev := syscall.Umask(umask)
			defer syscall.Umask(prev)
			if code := cli.Run(context.Background(), sinkCreateArgs(path)); code != 0 {
				t.Fatalf("sink create failed: %s", errOut)
			}
			info, err := os.Lstat(path)
			if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
				t.Fatal("sink is not a regular 0600 file")
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal("sink unreadable")
			}
			token := strings.TrimSpace(string(raw))
			if token == "" || string(raw) != token+"\n" {
				t.Fatal("sink does not hold exactly one token line")
			}
			if strings.Contains(out.String(), token) || strings.Contains(errOut.String(), token) {
				t.Fatal("token reached stdout or stderr in sink mode")
			}
			var m sinkMetadata
			if json.Unmarshal(out.Bytes(), &m) != nil || m.ID == "" || !m.SecretDelivered || m.SecretFile != path || m.Token != "" {
				t.Fatalf("metadata wrong: %s", out)
			}
			if len(m.Scopes) != 1 || m.Scopes[0] != "admin" || len(m.NamespaceGlobs) != 1 || m.NamespaceGlobs[0] != "never/matches/*" {
				t.Fatal("metadata does not show the explicit admin-only grant")
			}
			if err := cli.Store.ValidateAuthToken(context.Background(), token); err != nil {
				t.Fatal("delivered token is not valid in the store")
			}
		})
	}
}

func TestTokenCreateSinkTableOutputHasNoToken(t *testing.T) {
	cli, out, errOut := newTestCLI(t)
	path := filepath.Join(privateDir(t), "admin.token")
	args := sinkCreateArgs(path)
	args[len(args)-3] = "table" // --output value precedes --secret-file path
	if code := cli.Run(context.Background(), args); code != 0 {
		t.Fatalf("sink create failed: %s", errOut)
	}
	raw, _ := os.ReadFile(path)
	token := strings.TrimSpace(string(raw))
	if token == "" || strings.Contains(out.String(), token) || strings.Contains(out.String(), "Copy this value") || !strings.Contains(out.String(), "Delivered:") {
		t.Fatalf("table sink output wrong: %s", out)
	}
}

func TestTokenCreateSinkRefusesBeforeCommit(t *testing.T) {
	for _, kind := range []string{"existing-file", "existing-loose-file", "symlink-live", "symlink-dangling", "directory", "parent-symlink", "parent-group-writable", "parent-other-writable", "parent-missing"} {
		t.Run(kind, func(t *testing.T) {
			cli, out, errOut := newTestCLI(t)
			dir := privateDir(t)
			path := filepath.Join(dir, "admin.token")
			var original []byte
			switch kind {
			case "existing-file", "existing-loose-file":
				original = []byte("owned-original")
				mode := os.FileMode(0o600)
				if kind == "existing-loose-file" {
					mode = 0o644
				}
				if os.WriteFile(path, original, mode) != nil || os.Chmod(path, mode) != nil {
					t.Fatal("fixture failed")
				}
			case "symlink-live":
				target := filepath.Join(t.TempDir(), "target")
				original = []byte("owned-original")
				if os.WriteFile(target, original, 0o600) != nil || os.Symlink(target, path) != nil {
					t.Fatal("fixture failed")
				}
			case "symlink-dangling":
				if os.Symlink(filepath.Join(t.TempDir(), "absent"), path) != nil {
					t.Fatal("fixture failed")
				}
			case "directory":
				if os.Mkdir(path, 0o700) != nil {
					t.Fatal("fixture failed")
				}
			case "parent-symlink":
				real := privateDir(t)
				link := filepath.Join(privateDir(t), "link")
				if os.Symlink(real, link) != nil {
					t.Fatal("fixture failed")
				}
				path = filepath.Join(link, "admin.token")
			case "parent-group-writable":
				if os.Chmod(dir, 0o770) != nil {
					t.Fatal("fixture failed")
				}
			case "parent-other-writable":
				if os.Chmod(dir, 0o702) != nil {
					t.Fatal("fixture failed")
				}
			case "parent-missing":
				path = filepath.Join(dir, "absent", "admin.token")
			}
			before := tokenCount(t, cli)
			if cli.Run(context.Background(), sinkCreateArgs(path)) == 0 {
				t.Fatal("unsafe sink was accepted")
			}
			if tokenCount(t, cli) != before {
				t.Fatal("refusal minted a token")
			}
			if out.Len() != 0 {
				t.Fatal("refusal printed to stdout")
			}
			if strings.Contains(errOut.String(), dir) {
				t.Fatal("refusal disclosed the path")
			}
			if original != nil {
				if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, original) {
					t.Fatal("refusal changed an existing file")
				}
			}
			if kind == "symlink-live" {
				target, _ := os.Readlink(path)
				if got, _ := os.ReadFile(target); !bytes.Equal(got, original) {
					t.Fatal("refusal wrote through a symlink")
				}
			}
			if kind == "symlink-dangling" {
				if target, _ := os.Readlink(path); target != "" {
					if _, err := os.Lstat(target); err == nil {
						t.Fatal("refusal created the symlink target")
					}
				}
			}
		})
	}
}

func TestTokenCreateSinkRefusesWrongOwnerBeforeCommit(t *testing.T) {
	cli, out, _ := newTestCLI(t)
	prev := tokenSinkEUID
	tokenSinkEUID = func() int { return os.Geteuid() + 1 }
	t.Cleanup(func() { tokenSinkEUID = prev })
	path := filepath.Join(privateDir(t), "admin.token")
	before := tokenCount(t, cli)
	if cli.Run(context.Background(), sinkCreateArgs(path)) == 0 {
		t.Fatal("sink under a parent owned by another identity was accepted")
	}
	if tokenCount(t, cli) != before || out.Len() != 0 {
		t.Fatal("wrong-owner refusal had effects")
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatal("wrong-owner refusal left a file")
	}
}

func TestTokenCreateSinkFailureAfterCommitMakesNoDeliveryClaim(t *testing.T) {
	cli, out, errOut := newTestCLI(t)
	var attempted string
	prev := tokenSinkWrite
	tokenSinkWrite = func(f *os.File, data string) error {
		attempted = strings.TrimSpace(data)
		_, _ = f.WriteString(data[:8]) // partial write, then failure
		return errors.New("injected write failure")
	}
	t.Cleanup(func() { tokenSinkWrite = prev })
	path := filepath.Join(privateDir(t), "admin.token")
	before := tokenCount(t, cli)
	if cli.Run(context.Background(), sinkCreateArgs(path)) == 0 {
		t.Fatal("lost delivery was reported as success")
	}
	if attempted == "" {
		t.Fatal("write hook did not run")
	}
	if tokenCount(t, cli) != before+1 {
		t.Fatal("expected exactly one committed token")
	}
	var m sinkMetadata
	if json.Unmarshal(out.Bytes(), &m) != nil || m.ID == "" || m.SecretDelivered {
		t.Fatalf("metadata must name the issued ID and deny delivery: %s", out)
	}
	if !strings.Contains(errOut.String(), m.ID) || !strings.Contains(errOut.String(), "do not re-mint") {
		t.Fatal("failure message omitted the issued ID or the re-mint warning")
	}
	if strings.Contains(out.String(), attempted) || strings.Contains(errOut.String(), attempted) {
		t.Fatal("token disclosed on failure")
	}
	if raw, err := os.ReadFile(path); err != nil || len(raw) != 0 {
		t.Fatal("failed delivery left partial token bytes in the sink")
	}
	if cli.Store.ValidateAuthToken(context.Background(), attempted) != nil {
		t.Fatal("committed credential must stay active and revocable")
	}
	// A repeat run on the same path refuses before minting a second credential.
	tokenSinkWrite = prev
	out.Reset()
	errOut.Reset()
	if cli.Run(context.Background(), sinkCreateArgs(path)) == 0 || tokenCount(t, cli) != before+1 {
		t.Fatal("repeat run on the used sink was not refused before commit")
	}
}

// A store error cannot prove whether the row committed, so the CLI keeps an
// empty marker at the sink path and says so. The branch is reached through the
// finisher because a failing store call cannot be injected through Run.
func TestTokenCreateSinkStoreErrorLeavesEmptyMarkerAndDiscloseNothing(t *testing.T) {
	cli, out, errOut := newTestCLI(t)
	path := filepath.Join(privateDir(t), "admin.token")
	sink, err := openTokenSink(path, os.Geteuid())
	if err != nil {
		t.Fatal("sink fixture failed")
	}
	if cli.finishTokenCreateToSink(sink, "", contextstore.AuthToken{}, contextstore.ErrCredentialUnavailable, "json") == 0 {
		t.Fatal("store failure was reported as success")
	}
	if out.Len() != 0 || !strings.Contains(errOut.String(), "not established") || strings.Contains(errOut.String(), "unavailable") {
		t.Fatalf("store failure output wrong: out=%q err=%q", out, errOut)
	}
	if info, err := os.Lstat(path); err != nil || info.Size() != 0 || info.Mode().Perm() != 0o600 {
		t.Fatal("expected an empty 0600 marker at the sink path")
	}
	if _, err := openTokenSink(path, os.Geteuid()); err == nil {
		t.Fatal("marker did not make a repeat run on the same path refuse")
	}
}

func TestTokenCreateAdminRefusalsBeforeCommit(t *testing.T) {
	cases := map[string][]string{
		"admin-without-namespaces": {"context", "token", "create", "--name", "a", "--scopes", "admin", "--secret-file", "SINK"},
		"admin-without-sink":       {"context", "token", "create", "--name", "a", "--scopes", "admin", "--namespaces", "never/*"},
		"admin-among-scopes":       {"context", "token", "create", "--name", "a", "--scopes", "write, admin", "--secret-file", "SINK"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			cli, out, _ := newTestCLI(t)
			sink := filepath.Join(privateDir(t), "admin.token")
			for i, a := range args {
				if a == "SINK" {
					args[i] = sink
				}
			}
			before := tokenCount(t, cli)
			if cli.Run(context.Background(), args) == 0 {
				t.Fatal("unsafe admin create was accepted")
			}
			if tokenCount(t, cli) != before || out.Len() != 0 {
				t.Fatal("refusal had effects")
			}
			if _, err := os.Lstat(sink); !os.IsNotExist(err) {
				t.Fatal("refusal created a sink")
			}
		})
	}
}

func TestTokenCreateWithoutSinkRefusesTerminalAndKeepsPipeCompatibility(t *testing.T) {
	args := []string{"context", "token", "create", "--name", "svc", "--scopes", "write", "--namespaces", "project/x/*", "--output", "json"}
	cli, out, errOut := newTestCLI(t)
	setTerminal(t, true)
	before := tokenCount(t, cli)
	if cli.Run(context.Background(), args) == 0 || tokenCount(t, cli) != before || out.Len() != 0 {
		t.Fatal("terminal stdout was not refused before minting")
	}
	if !strings.Contains(errOut.String(), "--secret-file") {
		t.Fatal("refusal does not point to --secret-file")
	}

	setTerminal(t, false)
	out.Reset()
	if cli.Run(context.Background(), args) != 0 {
		t.Fatal("non-terminal stdout compatibility was broken")
	}
	var m sinkMetadata
	if json.Unmarshal(out.Bytes(), &m) != nil || m.Token == "" || cli.Store.ValidateAuthToken(context.Background(), m.Token) != nil {
		t.Fatalf("non-terminal json no longer returns a usable token: %s", out)
	}
	// Table form keeps its banner and Token row for existing scripts.
	out.Reset()
	if cli.Run(context.Background(), args[:len(args)-2]) != 0 || !strings.Contains(out.String(), "Token:") {
		t.Fatal("non-terminal table output changed")
	}
}

func TestTokenSinkIgnoresTerminalState(t *testing.T) {
	cli, out, errOut := newTestCLI(t)
	setTerminal(t, true)
	if code := cli.Run(context.Background(), sinkCreateArgs(filepath.Join(privateDir(t), "admin.token"))); code != 0 {
		t.Fatalf("sink mode must work from a terminal: %s", errOut)
	}
	if out.Len() == 0 {
		t.Fatal("expected metadata")
	}
}

func TestWriterIsTerminal(t *testing.T) {
	if writerIsTerminal(&bytes.Buffer{}) {
		t.Error("buffer treated as terminal")
	}
	null, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Skip("no /dev/null")
	}
	defer null.Close()
	if writerIsTerminal(null) {
		t.Error("/dev/null treated as terminal")
	}
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal("pipe")
	}
	defer r.Close()
	defer w.Close()
	if writerIsTerminal(w) {
		t.Error("pipe treated as terminal")
	}
}
