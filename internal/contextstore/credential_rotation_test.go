package contextstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func rotationFixture(t *testing.T) (*Store, CredentialAdmin, string, AuthToken) {
	t.Helper()
	s, err := Open(context.Background(), Config{RootDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	raw, _, err := s.CreateAuthToken(context.Background(), TokenCreateInput{Label: "operator", Scopes: []string{"admin"}, NamespaceGlobs: []string{"system/*"}})
	if err != nil {
		t.Fatal("operator fixture")
	}
	admin, err := s.AuthorizeCredentialAdmin(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	for _, format := range []string{"%v", "%+v", "%#v"} {
		if strings.Contains(fmt.Sprintf(format, admin), admin.digest) || strings.Contains(fmt.Sprintf(format, admin), raw) {
			t.Fatal("administrator proof debug output disclosed verifier")
		}
	}
	service, root, err := s.CreateAuthToken(context.Background(), TokenCreateInput{Label: "service", ClientID: "", Scopes: []string{"read"}, NamespaceGlobs: []string{"project/private/*"}})
	if err != nil {
		t.Fatal("service fixture")
	}
	return s, admin, service, root
}
func rotationInput(root AuthToken, key string) CredentialIssueInput {
	return CredentialIssueInput{PrincipalID: root.PrincipalID, CredentialID: root.TokenID, ExpectedGeneration: root.Generation, IdempotencyKey: key}
}

func TestCredentialRotationPreservesClaimsAndMetadataReplay(t *testing.T) {
	s, admin, old, root := rotationFixture(t)
	ctx := context.Background()
	// Exact empty grants are valid persisted legacy state, not permission defaults.
	if _, err := s.db.Exec(`UPDATE auth_tokens SET scopes='[]',namespace_globs='null' WHERE token_id=?`, root.TokenID); err != nil {
		t.Fatal(err)
	}
	in := rotationInput(root, "one")
	secret, result, err := s.IssueServiceCredential(ctx, admin, in, "cli")
	if err != nil {
		t.Fatal(err)
	}
	if secret == "" || !result.SecretAvailable || result.Generation != 2 {
		t.Fatal("new issuance metadata")
	}
	newMeta, err := s.ValidateAuthTokenWithClaims(ctx, secret)
	if err != nil {
		t.Fatal(err)
	}
	if len(newMeta.Scopes) != 0 || newMeta.NamespaceGlobs != nil || newMeta.ClientID != "" || newMeta.Label != root.Label {
		t.Fatal("claims changed")
	}
	if err = s.ValidateAuthToken(ctx, old); err != nil {
		t.Fatal("overlap invalidated predecessor")
	}
	oldMeta, _ := s.GetAuthToken(ctx, root.TokenID)
	if oldMeta.OverlapUntil == "" {
		t.Fatal("missing overlap bound")
	}
	replaySecret, replay, err := s.IssueServiceCredential(ctx, admin, in, "cli")
	if err != nil || replaySecret != "" || replay.SecretAvailable || !replay.Replayed || replay.CredentialID != result.CredentialID {
		t.Fatal("secret replay or missing receipt")
	}
	if replay.LastUsedAt == "" || replay.Generation != 2 {
		t.Fatal("current observation metadata missing")
	}
	in.IdempotencyKey = "different"
	_, _, err = s.IssueServiceCredential(ctx, admin, in, "cli")
	if !errors.Is(err, ErrCredentialConflict) {
		t.Fatal("stale generation accepted")
	}
	in.IdempotencyKey = "one"
	zero := int64(0)
	in.OverlapSeconds = &zero
	_, _, err = s.IssueServiceCredential(ctx, admin, in, "cli")
	if !errors.Is(err, ErrCredentialConflict) {
		t.Fatal("changed replay accepted")
	}
	var persisted string
	if err = s.db.QueryRow(`SELECT token_hash FROM auth_tokens WHERE token_id=?`, result.CredentialID).Scan(&persisted); err != nil {
		t.Fatal(err)
	}
	if persisted == secret || strings.Contains(persisted, secret) {
		t.Fatal("plaintext persisted")
	}
	list, err := s.ListServiceCredentials(ctx, admin, root.PrincipalID)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Credentials) != 2 || list.Credentials[0].Status == "" {
		t.Fatal("family status missing")
	}
}

func TestCredentialRotationAtomicAuditFailureAndFamilyRefusal(t *testing.T) {
	s, admin, old, root := rotationFixture(t)
	ctx := context.Background()
	_, other, err := s.CreateAuthToken(ctx, TokenCreateInput{Label: "other", ClientID: root.ClientID})
	if err != nil {
		t.Fatal(err)
	}
	in := rotationInput(root, "foreign")
	in.CredentialID = other.TokenID
	if _, _, err = s.IssueServiceCredential(ctx, admin, in, "http"); !errors.Is(err, ErrCredentialRequest) {
		t.Fatal("foreign credential accepted")
	}
	if _, err = s.db.Exec(`CREATE TRIGGER rotation_audit_failure BEFORE INSERT ON audit_events BEGIN SELECT RAISE(ABORT,'forced audit failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.IssueServiceCredential(ctx, admin, rotationInput(root, "rollback"), "http"); !errors.Is(err, ErrCredentialUnavailable) {
		t.Fatal("audit failure accepted")
	}
	meta, err := s.GetAuthToken(ctx, root.TokenID)
	if err != nil {
		t.Fatal(err)
	}
	if meta.Generation != 1 || meta.OverlapUntil != "" {
		t.Fatal("partial family mutation")
	}
	var count int
	if err = s.db.QueryRow(`SELECT count(*) FROM auth_tokens WHERE principal_id=?`, root.PrincipalID).Scan(&count); err != nil || count != 1 {
		t.Fatal("partial credential insert")
	}
	if err = s.ValidateAuthToken(ctx, old); err != nil {
		t.Fatal("rollback invalidated predecessor")
	}
}

func TestCredentialRotationConcurrentGenerationAndFreshAuthority(t *testing.T) {
	s, admin, _, root := rotationFixture(t)
	ctx := context.Background()
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			<-start
			_, _, err := s.IssueServiceCredential(ctx, admin, rotationInput(root, fmt.Sprintf("competing-%d", index)), "http")
			results <- err
		}(i)
	}
	close(start)
	wg.Wait()
	close(results)
	success, conflict := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, ErrCredentialConflict) {
			conflict++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatal("generation admitted multiple issuances")
	}
	if err := s.RevokeAuthTokenByID(ctx, admin.id); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.IssueServiceCredential(ctx, admin, rotationInput(root, "competing-0"), "http"); !errors.Is(err, ErrCredentialForbidden) {
		t.Fatal("revoked actor reused receipt or issued")
	}
	if _, _, err := s.CreateAuthTokenAuthorized(ctx, admin, TokenCreateInput{Label: "bypass"}); !errors.Is(err, ErrCredentialForbidden) {
		t.Fatal("legacy create bypassed revoked authority")
	}
}

func TestCredentialRotationZeroOverlapRevokeAndBounds(t *testing.T) {
	s, admin, old, root := rotationFixture(t)
	ctx := context.Background()
	in := rotationInput(root, "zero")
	zero := int64(0)
	in.OverlapSeconds = &zero
	secret, result, err := s.IssueServiceCredential(ctx, admin, in, "cli")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ValidateAuthToken(ctx, old); !errors.Is(err, ErrAuthTokenExpired) {
		t.Fatal("zero overlap did not expire old credential")
	}
	if err = s.ValidateAuthToken(ctx, secret); err != nil {
		t.Fatal("new credential invalid")
	}
	revoke := CredentialRevokeInput{PrincipalID: root.PrincipalID, CredentialID: result.CredentialID, ExpectedGeneration: 2, IdempotencyKey: "revoke"}
	revoked, err := s.RevokeServiceCredential(ctx, admin, revoke, "http")
	if err != nil || revoked.Generation != 3 || revoked.Status != "revoked" {
		t.Fatal("revoke outcome")
	}
	replay, err := s.RevokeServiceCredential(ctx, admin, revoke, "http")
	if err != nil || !replay.Replayed || replay.OperationGeneration != 3 {
		t.Fatal("revoke metadata replay")
	}
	refusal := revoke
	refusal.ExpectedGeneration = 3
	refusal.IdempotencyKey = "new-already-revoked"
	current, refuseErr := s.RevokeServiceCredential(ctx, admin, refusal, "http")
	if !errors.Is(refuseErr, ErrAuthTokenRevoked) || current.Generation != 3 || current.Status != "revoked" || current.CredentialID != result.CredentialID || current.OperationGeneration != 0 {
		t.Fatal("already-revoked refusal omitted current metadata or claimed commit")
	}
	if err = s.ValidateAuthToken(ctx, secret); !errors.Is(err, ErrAuthTokenRevoked) {
		t.Fatal("revoked credential accepted")
	}
	for _, ttl := range []int64{0, -1, MaxCredentialTTLSeconds + 1} {
		invalid := rotationInput(root, "invalid")
		invalid.TTLSeconds = &ttl
		_, _, err = s.IssueServiceCredential(ctx, admin, invalid, "cli")
		if !errors.Is(err, ErrCredentialRequest) {
			t.Fatal("invalid TTL accepted")
		}
	}
}

func TestCredentialRotationBackupRetainsReceiptsAndRevocation(t *testing.T) {
	s, admin, _, root := rotationFixture(t)
	ctx := context.Background()
	in := rotationInput(root, "backup")
	secret, issued, err := s.IssueServiceCredential(ctx, admin, in, "cli")
	if err != nil {
		t.Fatal(err)
	}
	revoked, err := s.RevokeServiceCredential(ctx, admin, CredentialRevokeInput{PrincipalID: root.PrincipalID, CredentialID: issued.CredentialID, ExpectedGeneration: 2, IdempotencyKey: "backup-revoke"}, "cli")
	if err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(t.TempDir(), "backup")
	if err = s.ExportBackup(ctx, backup); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(backup, "main.db"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), secret) {
		t.Fatal("backup contains plaintext")
	}
	if err = s.RestoreBackup(ctx, backup); err != nil {
		t.Fatal(err)
	}
	raw, replay, err := s.IssueServiceCredential(ctx, admin, in, "cli")
	if err != nil || raw != "" || replay.Generation != revoked.Generation || replay.OperationGeneration != 2 || replay.Status != "revoked" {
		t.Fatal("backup lost generation, receipt or revoked status")
	}
	if err = s.ValidateAuthToken(ctx, secret); !errors.Is(err, ErrAuthTokenRevoked) {
		t.Fatal("restored credential revived")
	}
}

func TestCredentialRotationLegacyBackupSeedsSeparateFamilies(t *testing.T) {
	s, err := Open(context.Background(), Config{RootDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	now := time.Now().UTC().Format(time.RFC3339)
	snapshot := backupSnapshot{Version: 1, ExportedAt: now, AuthTokens: []backupAuthToken{
		{TokenID: "legacy-a", TokenHash: hashToken("synthetic-a"), ClientID: "same-metadata", Scopes: "[]", NamespaceGlobs: "[]", CreatedAt: now},
		{TokenID: "legacy-b", TokenHash: hashToken("synthetic-b"), ClientID: "same-metadata", Scopes: "[]", NamespaceGlobs: "[]", CreatedAt: now},
	}}
	snapshot.Checksum, err = backupChecksum(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "legacy.json")
	if err = os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err = s.RestoreBackup(context.Background(), path); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"legacy-a", "legacy-b"} {
		m, e := s.GetAuthToken(context.Background(), id)
		if e != nil || m.PrincipalID != id || m.Generation != 1 || len(m.Scopes) != 0 || len(m.NamespaceGlobs) != 0 {
			t.Fatal("legacy claims changed or client IDs merged")
		}
	}
}

func TestCredentialRotationSchema28UpgradePreservesClaims(t *testing.T) {
	s, _, _, root := rotationFixture(t)
	ctx := context.Background()
	// Reconstruct the actual predecessor schema, rather than merely changing its version.
	for _, ddl := range []string{
		`DROP TRIGGER auth_credential_root`, `DROP TABLE auth_credential_operations`, `DROP TABLE auth_credential_families`,
		`ALTER TABLE auth_tokens DROP COLUMN principal_id`, `ALTER TABLE auth_tokens DROP COLUMN last_used_at`, `ALTER TABLE auth_tokens DROP COLUMN overlap_until`,
		`DELETE FROM schema_version WHERE version>28`,
	} {
		if _, err := s.db.Exec(ddl); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.migrate(ctx); err != nil {
		t.Fatal(err)
	}
	m, err := s.GetAuthToken(ctx, root.TokenID)
	if err != nil {
		t.Fatal(err)
	}
	if m.PrincipalID != root.TokenID || m.Generation != 1 || m.ClientID != root.ClientID || strings.Join(m.Scopes, ",") != strings.Join(root.Scopes, ",") || strings.Join(m.NamespaceGlobs, ",") != strings.Join(root.NamespaceGlobs, ",") {
		t.Fatal("upgrade changed identity or claims")
	}
}

func TestCredentialRotationNeverExtendsOldCaps(t *testing.T) {
	s, admin, _, root := rotationFixture(t)
	ctx := context.Background()
	short := int64(60)
	first := rotationInput(root, "short")
	first.OverlapSeconds = &short
	_, created, err := s.IssueServiceCredential(ctx, admin, first, "cli")
	if err != nil {
		t.Fatal(err)
	}
	before, err := s.GetAuthToken(ctx, root.TokenID)
	if err != nil {
		t.Fatal(err)
	}
	longer := int64(86400)
	second := rotationInput(root, "long")
	second.ExpectedGeneration = created.Generation
	second.CredentialID = created.CredentialID
	second.OverlapSeconds = &longer
	if _, _, err = s.IssueServiceCredential(ctx, admin, second, "cli"); err != nil {
		t.Fatal(err)
	}
	after, err := s.GetAuthToken(ctx, root.TokenID)
	if err != nil || before.OverlapUntil != after.OverlapUntil {
		t.Fatal("later rotation extended predecessor cap")
	}
}
