package contextcli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/hollis-labs/tesseract/internal/contextstore"
	"github.com/hollis-labs/tesseract/internal/fsperm"
)

// Reject repeated spellings before FlagSet can silently keep the last value.
func credentialArgumentsUnique(fs *flag.FlagSet, args []string) bool {
	seen := map[string]bool{}
	for index := 0; index < len(args); index++ {
		arg := args[index]
		if arg == "--" {
			return index == len(args)-1
		}
		if !strings.HasPrefix(arg, "-") {
			return false
		}
		name := strings.TrimPrefix(strings.TrimPrefix(arg, "-"), "-")
		name, _, hasValue := strings.Cut(name, "=")
		if name == "help" || name == "h" {
			if seen["help"] {
				return false
			}
			seen["help"] = true
			continue
		}
		setting := fs.Lookup(name)
		if setting == nil || seen[name] {
			return false
		}
		seen[name] = true
		boolean, ok := setting.Value.(interface{ IsBoolFlag() bool })
		if !hasValue && (!ok || !boolean.IsBoolFlag()) {
			index++
			if index >= len(args) {
				return false
			}
		}
	}
	return true
}

func credentialCLIMessage(err error) string {
	switch {
	case errors.Is(err, contextstore.ErrCredentialForbidden):
		return "managed administrator credential required"
	case errors.Is(err, contextstore.ErrCredentialConflict):
		return "credential generation or idempotency conflict"
	case errors.Is(err, contextstore.ErrAuthTokenRevoked):
		return "credential is already revoked"
	case errors.Is(err, contextstore.ErrCredentialRequest), errors.Is(err, contextstore.ErrAuthTokenInvalid):
		return "invalid credential request"
	default:
		return "credential operation unavailable"
	}
}

func credentialLegacyArgument(args []string) bool {
	for _, arg := range args {
		if arg == "--token" || arg == "-token" || strings.HasPrefix(arg, "--token=") || strings.HasPrefix(arg, "-token=") {
			return true
		}
	}
	return false
}
func (c *CLI) credentialAdmin(ctx context.Context, path string) (contextstore.CredentialAdmin, error) {
	// Do not tighten an operator-supplied path or follow a symlink. Compare the
	// opened inode with the checked path, and never echo file contents/errors.
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() || !fsperm.Supported || before.Mode().Perm() != 0o600 {
		return contextstore.CredentialAdmin{}, contextstore.ErrCredentialForbidden
	}
	f, err := os.Open(path)
	if err != nil {
		return contextstore.CredentialAdmin{}, contextstore.ErrCredentialForbidden
	}
	defer f.Close()
	after, err := f.Stat()
	if err != nil || !os.SameFile(before, after) {
		return contextstore.CredentialAdmin{}, contextstore.ErrCredentialForbidden
	}
	raw, err := io.ReadAll(io.LimitReader(f, 4097))
	if err != nil || len(raw) > 4096 {
		return contextstore.CredentialAdmin{}, contextstore.ErrCredentialForbidden
	}
	return c.Store.AuthorizeCredentialAdmin(ctx, strings.TrimSpace(string(raw)))
}
func credentialSeconds(value string) (*int64, error) {
	if value == "" {
		return nil, nil
	}
	duration, err := time.ParseDuration(value)
	if err != nil || duration%time.Second != 0 {
		return nil, contextstore.ErrCredentialRequest
	}
	seconds := int64(duration / time.Second)
	return &seconds, nil
}
func credentialPipe(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeNamedPipe != 0
}
func credentialFileAvailable(path string) bool {
	if path == "" || !fsperm.Supported {
		return false
	}
	_, err := os.Lstat(path)
	return os.IsNotExist(err)
}
func writeCredentialFile(path, secret string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return contextstore.ErrCredentialUnavailable
	}
	// Keep a partial private file on failure as an explicit delivery obligation;
	// the CLI never deletes/replaces an operator path or retries the secret.
	if f.Chmod(0o600) != nil {
		_ = f.Close()
		return contextstore.ErrCredentialUnavailable
	}
	_, err = io.WriteString(f, secret+"\n")
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		return contextstore.ErrCredentialUnavailable
	}
	return nil
}
func writeCredentialMetadata(w io.Writer, v any) error { return json.NewEncoder(w).Encode(v) }
func (c *CLI) runCredentialIssue(ctx context.Context, args []string) int {
	if credentialLegacyArgument(args) {
		return c.fail("legacy raw-token rotation unsupported; use principal/credential IDs and --admin-token-file")
	}
	fs := flag.NewFlagSet("token rotate", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	principal := fs.String("principal-id", "", "existing credential family root ID")
	id := fs.String("credential-id", "", "existing family credential ID")
	generation := fs.Int64("expected-generation", 0, "current family generation")
	key := fs.String("idempotency-key", "", "unique operation key")
	overlap := fs.String("overlap", "", "old credential overlap (default 15m, maximum 24h)")
	ttl := fs.String("ttl", "", "optional positive expiry, maximum 8760h")
	adminPath := fs.String("admin-token-file", "", "owner-only managed administrator credential file")
	secretPath := fs.String("secret-file", "", "exclusive new 0600 output file")
	secretStdout := fs.Bool("secret-stdout", false, "write secret only to a pipe; metadata on stderr")
	if !credentialArgumentsUnique(fs, args) {
		return c.fail("invalid or duplicate credential command arguments")
	}
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			fs.SetOutput(c.Stdout)
			_, _ = fmt.Fprintln(c.Stdout, "tesseract context "+fs.Name())
			fs.PrintDefaults()
			return 0
		}
		return c.fail("invalid credential command arguments")
	}
	if len(fs.Args()) != 0 {
		return c.fail("unexpected credential command arguments")
	}
	ov, err := credentialSeconds(*overlap)
	if err != nil {
		return c.fail("invalid credential overlap")
	}
	exp, err := credentialSeconds(*ttl)
	if err != nil {
		return c.fail("invalid credential TTL")
	}
	admin, err := c.credentialAdmin(ctx, *adminPath)
	if err != nil {
		return c.fail("managed administrator credential required")
	}
	input := contextstore.CredentialIssueInput{PrincipalID: *principal, CredentialID: *id, ExpectedGeneration: *generation, IdempotencyKey: *key, OverlapSeconds: ov, TTLSeconds: exp}
	replay, err := c.Store.ReplayServiceCredentialIssue(ctx, admin, input)
	if err != nil {
		return c.fail(credentialCLIMessage(err))
	}
	if replay != nil {
		out := c.Stdout
		if *secretStdout {
			out = c.Stderr
		}
		if writeCredentialMetadata(out, *replay) != nil {
			return c.fail("credential metadata delivery failed")
		}
		return 0
	}
	// Reject unsupported sinks before issuance. The exclusive open after commit
	// still may fail/race; that is lost delivery, never a rollback/replay license.
	if (*secretStdout && *secretPath != "") || (!*secretStdout && *secretPath == "") || (*secretStdout && !credentialPipe(c.Stdout)) {
		return c.fail("select an exclusive private file or a stdout pipe")
	}
	if *secretPath != "" && !credentialFileAvailable(*secretPath) {
		return c.fail("private output file must be new and supported")
	}
	secret, result, err := c.Store.IssueServiceCredential(ctx, admin, input, "cli")
	if err != nil {
		return c.fail(credentialCLIMessage(err))
	}
	metadataOut := c.Stdout
	if *secretStdout {
		metadataOut = c.Stderr
	}
	if secret != "" {
		if *secretStdout {
			var n int
			n, err = io.WriteString(c.Stdout, secret+"\n")
			if n != len(secret)+1 {
				err = io.ErrShortWrite
			}
		} else {
			err = writeCredentialFile(*secretPath, secret)
		}
		if err != nil {
			result.SecretAvailable = false
			_ = writeCredentialMetadata(metadataOut, result)
			return c.fail("private credential delivery failed; issued credential remains visible and revocable")
		}
	}
	if writeCredentialMetadata(metadataOut, result) != nil {
		return c.fail("credential metadata delivery failed; operation remains committed")
	}
	return 0
}
func (c *CLI) runCredentialRevoke(ctx context.Context, args []string) int {
	if credentialLegacyArgument(args) {
		return c.fail("raw-token revocation unsupported; use credential IDs and --admin-token-file")
	}
	fs := flag.NewFlagSet("token revoke", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	principal := fs.String("principal-id", "", "existing family ID")
	id := fs.String("credential-id", "", "credential ID to revoke")
	generation := fs.Int64("expected-generation", 0, "current generation")
	key := fs.String("idempotency-key", "", "unique operation key")
	adminPath := fs.String("admin-token-file", "", "owner-only managed administrator file")
	if !credentialArgumentsUnique(fs, args) {
		return c.fail("invalid or duplicate credential command arguments")
	}
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			fs.SetOutput(c.Stdout)
			_, _ = fmt.Fprintln(c.Stdout, "tesseract context "+fs.Name())
			fs.PrintDefaults()
			return 0
		}
		return c.fail("invalid credential command arguments")
	}
	if len(fs.Args()) != 0 {
		return c.fail("unexpected credential command arguments")
	}
	admin, err := c.credentialAdmin(ctx, *adminPath)
	if err != nil {
		return c.fail("managed administrator credential required")
	}
	result, err := c.Store.RevokeServiceCredential(ctx, admin, contextstore.CredentialRevokeInput{PrincipalID: *principal, CredentialID: *id, ExpectedGeneration: *generation, IdempotencyKey: *key}, "cli")
	if err != nil {
		if result.CredentialID != "" {
			_ = writeCredentialMetadata(c.Stdout, result)
		}
		return c.fail(credentialCLIMessage(err))
	}
	if writeCredentialMetadata(c.Stdout, result) != nil {
		return c.fail("credential metadata delivery failed")
	}
	return 0
}
func (c *CLI) runCredentialList(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("token list", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	principal := fs.String("principal-id", "", "family ID")
	adminPath := fs.String("admin-token-file", "", "owner-only managed administrator file")
	if !credentialArgumentsUnique(fs, args) || fs.Parse(args) != nil || len(fs.Args()) != 0 {
		return c.fail("invalid credential command arguments")
	}
	admin, err := c.credentialAdmin(ctx, *adminPath)
	if err != nil {
		return c.fail("managed administrator credential required")
	}
	out, err := c.Store.ListServiceCredentials(ctx, admin, *principal)
	if err != nil {
		return c.fail(credentialCLIMessage(err))
	}
	if writeCredentialMetadata(c.Stdout, out) != nil {
		return c.fail("credential metadata delivery failed")
	}
	return 0
}
