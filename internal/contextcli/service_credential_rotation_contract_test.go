package contextcli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/hollis-labs/tesseract/internal/contextstore"
	"github.com/hollis-labs/tesseract/internal/fsperm"
)

func TestServiceCredentialLegacyRawRotateRefusesBeforeEffects(t *testing.T) {
	cli, out, errOut := newTestCLI(t)
	ctx := context.Background()
	raw, _, err := cli.Store.CreateAuthToken(ctx, contextstore.TokenCreateInput{Label: "service", ClientID: "tether-proxy", Scopes: []string{"write"}, NamespaceGlobs: []string{"project/example/*"}})
	if err != nil {
		t.Fatal("fixture issuance failed")
	}
	before, err := cli.Store.ListAuthTokens(ctx, 100)
	if err != nil {
		t.Fatal("fixture metadata read failed")
	}
	code := cli.Run(ctx, []string{"context", "token", "rotate", "--token", raw})
	if code == 0 {
		t.Error("legacy raw-token rotation was accepted")
	}
	if strings.Contains(out.String(), raw) || strings.Contains(errOut.String(), raw) {
		t.Error("legacy refusal disclosed the supplied credential")
	}
	after, err := cli.Store.ListAuthTokens(ctx, 100)
	if err != nil {
		t.Fatal("result metadata read failed")
	}
	if !reflect.DeepEqual(before, after) {
		t.Error("legacy refusal changed credential metadata")
	}
	if err := cli.Store.ValidateAuthToken(ctx, raw); err != nil {
		t.Error("legacy refusal invalidated the existing service credential")
	}
}

func rotationCLIFixture(t *testing.T) (*CLI, *bytes.Buffer, *bytes.Buffer, string, string, string) {
	t.Helper()
	if !fsperm.Supported {
		t.Skip("private credential transport unsupported on this platform")
	}
	cli, out, errOut := newTestCLI(t)
	ctx := context.Background()
	admin, _, err := cli.Store.CreateAuthToken(ctx, contextstore.TokenCreateInput{Label: "administrator", Scopes: []string{"admin"}})
	if err != nil {
		t.Fatal("fixture admin issuance failed")
	}
	raw, root, err := cli.Store.CreateAuthToken(ctx, contextstore.TokenCreateInput{Label: "service", ClientID: "tether-proxy", Scopes: []string{"write"}, NamespaceGlobs: []string{"project/example/*"}})
	if err != nil {
		t.Fatal("fixture service issuance failed")
	}
	adminPath := filepath.Join(t.TempDir(), "administrator")
	if os.WriteFile(adminPath, []byte(admin), 0o600) != nil {
		t.Fatal("fixture administrator file creation failed")
	}
	return cli, out, errOut, adminPath, raw, root.TokenID
}

func rotationCLIArgs(principal, admin, key string) []string {
	return []string{"context", "token", "rotate", "--principal-id", principal, "--credential-id", principal, "--expected-generation", "1", "--idempotency-key", key, "--admin-token-file", admin, "--overlap", "15m", "--ttl", "1h"}
}

type rotationCLIMetadata struct {
	CredentialID    string `json:"credential_id"`
	Generation      int64  `json:"generation"`
	Replayed        bool   `json:"replayed"`
	SecretAvailable bool   `json:"secret_available"`
}

func TestServiceCredentialCLIExclusiveDeliveryAndReplay(t *testing.T) {
	cli, out, errOut, admin, old, principal := rotationCLIFixture(t)
	path := filepath.Join(t.TempDir(), "issued")
	args := append(rotationCLIArgs(principal, admin, "private-file"), "--secret-file", path)
	if cli.Run(context.Background(), args) != 0 {
		t.Fatal("private file issuance failed")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("issued private file could not be read")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		t.Error("issued sink was not an owner-only regular file")
	}
	token := strings.TrimSpace(string(raw))
	if token == "" {
		t.Fatal("private delivery was empty")
	}
	if err := cli.Store.ValidateAuthToken(context.Background(), token); err != nil {
		t.Error("privately delivered credential was invalid")
	}
	if strings.Contains(out.String(), token) || strings.Contains(errOut.String(), token) || strings.Contains(out.String(), old) || strings.Contains(errOut.String(), old) {
		t.Error("credential escaped into metadata or diagnostics")
	}
	var first rotationCLIMetadata
	if json.Unmarshal(out.Bytes(), &first) != nil || first.CredentialID == "" || !first.SecretAvailable || first.Replayed {
		t.Fatal("first private issuance metadata was invalid")
	}
	// An exact retry must not even attempt to open its original sink. Replace
	// that owned fixture file with a directory, which a fresh delivery cannot use.
	if os.Remove(path) != nil || os.Mkdir(path, 0o700) != nil {
		t.Fatal("retry sink fixture setup failed")
	}
	out.Reset()
	errOut.Reset()
	if cli.Run(context.Background(), args) != 0 {
		t.Fatal("metadata-only retry depended on reopening its sink")
	}
	var replay rotationCLIMetadata
	if json.Unmarshal(out.Bytes(), &replay) != nil || !replay.Replayed || replay.SecretAvailable || replay.CredentialID != first.CredentialID || replay.Generation != 2 {
		t.Error("retry did not preserve metadata-only operation identity")
	}
	info, err = os.Lstat(path)
	if err != nil || !info.IsDir() {
		t.Error("retry changed the original sink")
	}
	if strings.Contains(out.String(), token) || strings.Contains(errOut.String(), token) {
		t.Error("retry replayed a credential")
	}
}

func TestServiceCredentialCLIBrokenPipeRetainsIssuedObligation(t *testing.T) {
	cli, _, errOut, admin, old, principal := rotationCLIFixture(t)
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal("pipe fixture creation failed")
	}
	if reader.Close() != nil {
		t.Fatal("pipe reader close failed")
	}
	defer writer.Close()
	cli.Stdout = writer
	args := append(rotationCLIArgs(principal, admin, "broken-private-pipe"), "--secret-stdout")
	if cli.Run(context.Background(), args) == 0 {
		t.Fatal("failed private delivery was reported successful")
	}
	var first rotationCLIMetadata
	if json.NewDecoder(bytes.NewReader(errOut.Bytes())).Decode(&first) != nil || first.CredentialID == "" || first.Generation != 2 || first.SecretAvailable {
		t.Fatal("failed private delivery omitted the issued obligation")
	}
	if strings.Contains(errOut.String(), old) {
		t.Error("failed delivery diagnostics disclosed old credential")
	}
	if err := cli.Store.ValidateAuthToken(context.Background(), old); err != nil {
		t.Error("failed delivery invalidated working predecessor")
	}
	// Replay remains metadata-only even when stdout is not a supported sink.
	var unsupported bytes.Buffer
	cli.Stdout = &unsupported
	errOut.Reset()
	if cli.Run(context.Background(), args) != 0 {
		t.Fatal("lost-delivery retry attempted another secret delivery")
	}
	var replay rotationCLIMetadata
	if json.Unmarshal(errOut.Bytes(), &replay) != nil || !replay.Replayed || replay.SecretAvailable || replay.CredentialID != first.CredentialID {
		t.Error("lost-delivery retry changed or replayed issued credential")
	}
	if unsupported.Len() != 0 {
		t.Error("lost-delivery retry wrote to secret stream")
	}
}

func TestServiceCredentialCLIUnsafeDeliveryRefusesBeforeIssuance(t *testing.T) {
	for _, kind := range []string{"existing", "symlink", "buffered-stdout", "fractional-overlap", "negative-overlap", "zero-ttl", "excess-ttl", "duplicate-overlap", "duplicate-secret-file", "conflicting-sinks"} {
		t.Run(kind, func(t *testing.T) {
			cli, out, errOut, admin, old, principal := rotationCLIFixture(t)
			path := filepath.Join(t.TempDir(), "secret")
			args := rotationCLIArgs(principal, admin, "refused-"+kind)
			switch kind {
			case "existing":
				if os.WriteFile(path, []byte("owned-original"), 0o600) != nil {
					t.Fatal("existing fixture creation failed")
				}
			case "symlink":
				target := filepath.Join(t.TempDir(), "target")
				if os.WriteFile(target, []byte("owned-original"), 0o600) != nil || os.Symlink(target, path) != nil {
					t.Fatal("symlink fixture creation failed")
				}
			case "buffered-stdout":
				args = append(args, "--secret-stdout")
			case "fractional-overlap":
				args[len(args)-3] = "1ms"
			case "negative-overlap":
				args[len(args)-3] = "-1s"
			case "zero-ttl":
				args[len(args)-1] = "0s"
			case "excess-ttl":
				args[len(args)-1] = "8761h"
			case "duplicate-overlap":
				args = append(args, "--overlap", "15m")
			case "duplicate-secret-file":
				args = append(args, "-secret-file="+path)
			case "conflicting-sinks":
				args = append(args, "--secret-stdout")
			}
			if kind != "buffered-stdout" {
				args = append(args, "--secret-file", path)
			}
			before, err := cli.Store.ListAuthTokens(context.Background(), 100)
			if err != nil {
				t.Fatal("fixture state read failed")
			}
			if cli.Run(context.Background(), args) == 0 {
				t.Error("unsupported private delivery or duration was accepted")
			}
			after, err := cli.Store.ListAuthTokens(context.Background(), 100)
			if err != nil {
				t.Fatal("result state read failed")
			}
			if !reflect.DeepEqual(rotationCLICredentialState(before), rotationCLICredentialState(after)) {
				t.Error("preflight refusal changed credential eligibility or grants")
			}
			if err := cli.Store.ValidateAuthToken(context.Background(), old); err != nil {
				t.Error("preflight refusal invalidated predecessor")
			}
			if strings.Contains(out.String(), old) || strings.Contains(errOut.String(), old) {
				t.Error("preflight refusal disclosed a credential")
			}
			if kind == "existing" || kind == "symlink" {
				contents, err := os.ReadFile(path)
				if err != nil || string(contents) != "owned-original" {
					t.Error("preflight refusal changed an existing sink")
				}
			}
		})
	}
}

func rotationCLICredentialState(tokens []contextstore.AuthToken) []contextstore.AuthToken {
	out := make([]contextstore.AuthToken, len(tokens))
	for i, token := range tokens {
		out[i] = contextstore.AuthToken{TokenID: token.TokenID, Label: token.Label, ClientID: token.ClientID, Scopes: token.Scopes, NamespaceGlobs: token.NamespaceGlobs, CreatedAt: token.CreatedAt, ExpiresAt: token.ExpiresAt, RevokedAt: token.RevokedAt}
	}
	return out
}

func TestServiceCredentialCLIDuplicateArgumentsPreserveGenerationAndDelivery(t *testing.T) {
	for _, command := range []string{"rotate", "revoke"} {
		for _, spelling := range []string{"--expected-generation", "-expected-generation=1", "--expected-generation=1"} {
			t.Run(command+"/"+spelling, func(t *testing.T) {
				cli, out, errOut, adminPath, old, principal := rotationCLIFixture(t)
				ctx := context.Background()
				adminRaw, err := os.ReadFile(adminPath)
				if err != nil {
					t.Fatal("fixture admin read failed")
				}
				admin, err := cli.Store.AuthorizeCredentialAdmin(ctx, string(adminRaw))
				if err != nil {
					t.Fatal("fixture admin authorization failed")
				}
				before, err := cli.Store.ListServiceCredentials(ctx, admin, principal)
				if err != nil {
					t.Fatal("fixture family read failed")
				}
				args := []string{"context", "token", command, "--principal-id", principal, "--credential-id", principal, "--expected-generation", "1", "--idempotency-key", "duplicate-alias", "--admin-token-file", adminPath}
				path := filepath.Join(t.TempDir(), "not-issued")
				if command == "rotate" {
					args = append(args, "--secret-file", path)
				}
				args = append(args, spelling)
				if spelling == "--expected-generation" {
					args = append(args, "1")
				}
				if cli.Run(ctx, args) == 0 {
					t.Error("duplicate generation argument was accepted")
				}
				after, err := cli.Store.ListServiceCredentials(ctx, admin, principal)
				if err != nil {
					t.Fatal("result family read failed")
				}
				if before.Generation != after.Generation || !reflect.DeepEqual(rotationCLICredentialState(before.Credentials), rotationCLICredentialState(after.Credentials)) {
					t.Error("duplicate refusal changed durable generation or credential inventory")
				}
				if _, err := os.Lstat(path); !os.IsNotExist(err) {
					t.Error("duplicate refusal created a private delivery file")
				}
				if err := cli.Store.ValidateAuthToken(ctx, old); err != nil {
					t.Error("duplicate refusal invalidated predecessor")
				}
				if strings.Contains(out.String(), old) || strings.Contains(errOut.String(), old) || strings.Contains(out.String(), string(adminRaw)) || strings.Contains(errOut.String(), string(adminRaw)) {
					t.Error("duplicate refusal disclosed a credential")
				}
			})
		}
	}
}
