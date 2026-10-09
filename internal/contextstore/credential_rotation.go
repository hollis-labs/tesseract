package contextstore

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

var (
	ErrCredentialUnsupported = errors.New("legacy rotation unsupported; use the authorized credential issuer")
	ErrCredentialForbidden   = errors.New("managed administrator credential required")
	ErrCredentialConflict    = errors.New("credential generation or idempotency conflict")
	ErrCredentialRequest     = errors.New("invalid credential request")
	ErrCredentialUnavailable = errors.New("credential persistence unavailable")
)

const DefaultCredentialOverlapSeconds int64 = 900
const MaxCredentialOverlapSeconds int64 = 86400
const MaxCredentialTTLSeconds int64 = 31536000

// CredentialAdmin cannot be minted by a caller-supplied actor or claims object.
// The issuer validates this proof again within its mutation transaction.
type CredentialAdmin struct{ id, digest string }

type CredentialIssueInput struct {
	PrincipalID        string `json:"principal_id"`
	CredentialID       string `json:"credential_id"`
	ExpectedGeneration int64  `json:"expected_generation"`
	IdempotencyKey     string `json:"idempotency_key"`
	OverlapSeconds     *int64 `json:"overlap_seconds,omitempty"`
	TTLSeconds         *int64 `json:"ttl_seconds,omitempty"`
}
type CredentialRevokeInput struct {
	PrincipalID        string `json:"principal_id"`
	CredentialID       string `json:"credential_id"`
	ExpectedGeneration int64  `json:"expected_generation"`
	IdempotencyKey     string `json:"idempotency_key"`
}

// CredentialResult never contains a secret or digest. Generation is current;
// OperationGeneration identifies the original commit on an idempotent retry.
type CredentialResult struct {
	PrincipalID         string `json:"principal_id"`
	CredentialID        string `json:"credential_id"`
	Generation          int64  `json:"generation"`
	OperationGeneration int64  `json:"operation_generation"`
	CreatedAt           string `json:"created_at"`
	ExpiresAt           string `json:"expires_at,omitempty"`
	LastUsedAt          string `json:"last_used_at,omitempty"`
	RevokedAt           string `json:"revoked_at,omitempty"`
	OverlapUntil        string `json:"overlap_until,omitempty"`
	Replayed            bool   `json:"replayed"`
	SecretAvailable     bool   `json:"secret_available"`
	Status              string `json:"status"`
}
type CredentialList struct {
	PrincipalID string      `json:"principal_id"`
	Generation  int64       `json:"generation"`
	Credentials []AuthToken `json:"credentials"`
}

func migrateCredentialFamilies(ctx context.Context, tx *sql.Tx) error {
	for _, column := range []struct{ name, ddl string }{
		{"principal_id", `ALTER TABLE auth_tokens ADD COLUMN principal_id TEXT NOT NULL DEFAULT ''`},
		{"last_used_at", `ALTER TABLE auth_tokens ADD COLUMN last_used_at TEXT NOT NULL DEFAULT ''`},
		{"overlap_until", `ALTER TABLE auth_tokens ADD COLUMN overlap_until TEXT NOT NULL DEFAULT ''`},
	} {
		exists, err := columnExists(ctx, tx, "auth_tokens", column.name)
		if err != nil {
			return err
		}
		if !exists {
			if _, err = tx.ExecContext(ctx, column.ddl); err != nil {
				return err
			}
		}
	}
	for _, query := range []string{
		`CREATE TABLE IF NOT EXISTS auth_credential_families (principal_id TEXT PRIMARY KEY, generation INTEGER NOT NULL CHECK(generation>0))`,
		`CREATE TABLE IF NOT EXISTS auth_credential_operations (principal_id TEXT NOT NULL, key_digest TEXT NOT NULL, actor_id TEXT NOT NULL, request_digest TEXT NOT NULL, credential_id TEXT NOT NULL, operation_generation INTEGER NOT NULL, kind TEXT NOT NULL, PRIMARY KEY(principal_id,key_digest))`,
		`UPDATE auth_tokens SET principal_id=token_id WHERE principal_id=''`,
		`INSERT OR IGNORE INTO auth_credential_families SELECT principal_id,1 FROM auth_tokens`,
		// Also covers legacy backup import and local offline bootstrap, without
		// merging caller-supplied client IDs or applying new grant defaults.
		`CREATE TRIGGER IF NOT EXISTS auth_credential_root AFTER INSERT ON auth_tokens WHEN NEW.principal_id='' BEGIN
   UPDATE auth_tokens SET principal_id=NEW.token_id WHERE token_id=NEW.token_id;
   INSERT INTO auth_credential_families(principal_id,generation) VALUES(NEW.token_id,1);
  END`,
	} {
		if _, err := tx.ExecContext(ctx, query); err != nil {
			return err
		}
	}
	return nil
}

const credentialSelect = `SELECT token_id,label,COALESCE(client_id,''),scopes,namespace_globs,created_at,COALESCE(expires_at,''),COALESCE(revoked_at,''),principal_id,
 COALESCE((SELECT generation FROM auth_credential_families f WHERE f.principal_id=auth_tokens.principal_id),0),last_used_at,overlap_until FROM auth_tokens `

type credentialQuery interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func readCredential(ctx context.Context, db credentialQuery, suffix string, value string) (AuthToken, error) {
	var m AuthToken
	var scopes, globs string
	err := db.QueryRowContext(ctx, credentialSelect+suffix, value).Scan(&m.TokenID, &m.Label, &m.ClientID, &scopes, &globs, &m.CreatedAt, &m.ExpiresAt, &m.RevokedAt, &m.PrincipalID, &m.Generation, &m.LastUsedAt, &m.OverlapUntil)
	if errors.Is(err, sql.ErrNoRows) {
		return m, ErrAuthTokenInvalid
	}
	if err != nil {
		return m, ErrCredentialUnavailable
	}
	if json.Unmarshal([]byte(scopes), &m.Scopes) != nil || json.Unmarshal([]byte(globs), &m.NamespaceGlobs) != nil {
		return AuthToken{}, ErrCredentialUnavailable
	}
	return m, nil
}
func credentialState(m AuthToken, now time.Time) error {
	if m.RevokedAt != "" {
		return ErrAuthTokenRevoked
	}
	for _, deadline := range []string{m.ExpiresAt, m.OverlapUntil} {
		if deadline != "" {
			t, err := time.Parse(time.RFC3339Nano, deadline)
			if err != nil {
				return ErrCredentialUnavailable
			}
			if !now.Before(t) {
				return ErrAuthTokenExpired
			}
		}
	}
	return nil
}
func (s *Store) validateCredential(ctx context.Context, token string) (AuthToken, error) {
	digest := hashToken(strings.TrimSpace(token))
	if strings.TrimSpace(token) == "" {
		return AuthToken{}, ErrAuthTokenInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AuthToken{}, ErrCredentialUnavailable
	}
	defer tx.Rollback()
	// Reserve the writer before reading; a concurrent revoke cannot pass between
	// validation and observation. The no-op is rolled back for invalid attempts.
	if _, err = tx.ExecContext(ctx, `UPDATE auth_tokens SET token_id=token_id WHERE token_hash=?`, digest); err != nil {
		return AuthToken{}, ErrCredentialUnavailable
	}
	m, err := readCredential(ctx, tx, "WHERE token_hash=?", digest)
	if err != nil {
		return m, err
	}
	now := time.Now().UTC()
	if err = credentialState(m, now); err != nil {
		return AuthToken{}, err
	}
	m.LastUsedAt = now.Format(time.RFC3339Nano)
	if _, err = tx.ExecContext(ctx, `UPDATE auth_tokens SET last_used_at=? WHERE token_id=?`, m.LastUsedAt, m.TokenID); err != nil {
		return AuthToken{}, ErrCredentialUnavailable
	}
	if tx.Commit() != nil {
		return AuthToken{}, ErrCredentialUnavailable
	}
	return m, nil
}
func (s *Store) AuthorizeCredentialAdmin(ctx context.Context, token string) (CredentialAdmin, error) {
	m, err := s.ValidateAuthTokenWithClaims(ctx, token)
	if err != nil || !slices.Contains(m.Scopes, "admin") {
		return CredentialAdmin{}, ErrCredentialForbidden
	}
	return CredentialAdmin{id: m.TokenID, digest: hashToken(strings.TrimSpace(token))}, nil
}
func authorizeCredentialTx(ctx context.Context, tx *sql.Tx, admin CredentialAdmin) error {
	if admin.id == "" || admin.digest == "" {
		return ErrCredentialForbidden
	}
	m, err := readCredential(ctx, tx, "WHERE token_hash=?", admin.digest)
	if err != nil || m.TokenID != admin.id || !slices.Contains(m.Scopes, "admin") || credentialState(m, time.Now().UTC()) != nil {
		return ErrCredentialForbidden
	}
	return nil
}
func credentialRequestValid(principal, credential, key string, generation int64) bool {
	return principal != "" && credential != "" && key != "" && len(principal) <= 256 && len(credential) <= 256 && len(key) <= 128 && generation > 0
}
func digestCredentialRequest(v any) string {
	b, _ := json.Marshal(v)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
func resultFor(m AuthToken, opGeneration int64, replayed bool) CredentialResult {
	status := "active"
	if errors.Is(credentialState(m, time.Now().UTC()), ErrAuthTokenRevoked) {
		status = "revoked"
	} else if credentialState(m, time.Now().UTC()) != nil {
		status = "expired"
	}
	return CredentialResult{PrincipalID: m.PrincipalID, CredentialID: m.TokenID, Generation: m.Generation, OperationGeneration: opGeneration, CreatedAt: m.CreatedAt, ExpiresAt: m.ExpiresAt, LastUsedAt: m.LastUsedAt, RevokedAt: m.RevokedAt, OverlapUntil: m.OverlapUntil, Replayed: replayed, Status: status}
}
func (s *Store) beginCredentialOperation(ctx context.Context, admin CredentialAdmin, principal, key, requestDigest string) (*sql.Tx, *CredentialResult, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, ErrCredentialUnavailable
	}
	fail := func(e error) (*sql.Tx, *CredentialResult, error) { _ = tx.Rollback(); return nil, nil, e }
	if _, err = tx.ExecContext(ctx, `UPDATE auth_credential_families SET generation=generation WHERE principal_id=?`, principal); err != nil {
		return fail(ErrCredentialUnavailable)
	}
	if err = authorizeCredentialTx(ctx, tx, admin); err != nil {
		return fail(err)
	}
	var storedActor, storedDigest, credential string
	var generation int64
	err = tx.QueryRowContext(ctx, `SELECT actor_id,request_digest,credential_id,operation_generation FROM auth_credential_operations WHERE principal_id=? AND key_digest=?`, principal, digestCredentialRequest(key)).Scan(&storedActor, &storedDigest, &credential, &generation)
	if err == nil {
		if storedActor != admin.id || storedDigest != requestDigest {
			return fail(ErrCredentialConflict)
		}
		m, e := readCredential(ctx, tx, "WHERE token_id=?", credential)
		if e != nil {
			return fail(e)
		}
		r := resultFor(m, generation, true)
		if tx.Commit() != nil {
			return nil, nil, ErrCredentialUnavailable
		}
		return nil, &r, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fail(ErrCredentialUnavailable)
	}
	return tx, nil, nil
}
func advanceCredentialGeneration(ctx context.Context, tx *sql.Tx, principal string, expected int64) error {
	r, err := tx.ExecContext(ctx, `UPDATE auth_credential_families SET generation=generation+1 WHERE principal_id=? AND generation=? AND generation<9223372036854775807`, principal, expected)
	if err != nil {
		return ErrCredentialUnavailable
	}
	n, err := r.RowsAffected()
	if err != nil {
		return ErrCredentialUnavailable
	}
	if n != 1 {
		return ErrCredentialConflict
	}
	return nil
}
func recordCredentialOperation(ctx context.Context, tx *sql.Tx, actor, principal, key, digest, credential, kind, source string, generation int64) error {
	if source != "cli" && source != "http" {
		return ErrCredentialRequest
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO auth_credential_operations VALUES(?,?,?,?,?,?,?)`, principal, digestCredentialRequest(key), actor, digest, credential, generation, kind); err != nil {
		return ErrCredentialUnavailable
	}
	return auditCredentialMutation(ctx, tx, actor, principal, credential, kind, source, generation)
}
func auditCredentialMutation(ctx context.Context, tx *sql.Tx, actor, principal, credential, kind, source string, generation int64) error {
	metadata, _ := json.Marshal(map[string]string{"principal_id": principal, "credential_id": credential, "source": source})
	if err := recordAuditEventWith(ctx, tx, AuditEvent{EventType: "credential." + kind, Actor: "credential:" + actor, Namespace: "system/security/credentials", Key: principal, Revision: generation, RecordID: credential, Metadata: metadata}); err != nil {
		return ErrCredentialUnavailable
	}
	return nil
}

func normalizeCredentialIssue(in CredentialIssueInput) (CredentialIssueInput, string, error) {
	if !credentialRequestValid(in.PrincipalID, in.CredentialID, in.IdempotencyKey, in.ExpectedGeneration) {
		return in, "", ErrCredentialRequest
	}
	overlap := DefaultCredentialOverlapSeconds
	if in.OverlapSeconds != nil {
		overlap = *in.OverlapSeconds
	}
	if overlap < 0 || overlap > MaxCredentialOverlapSeconds || (in.TTLSeconds != nil && (*in.TTLSeconds <= 0 || *in.TTLSeconds > MaxCredentialTTLSeconds)) {
		return in, "", ErrCredentialRequest
	}
	in.OverlapSeconds = &overlap
	digest := digestCredentialRequest(struct {
		Kind  string
		Input CredentialIssueInput
	}{"issue", in})
	return in, digest, nil
}

// ReplayServiceCredentialIssue checks an existing receipt without issuing a secret.
// Callers may use it before opening a delivery sink; issuance still revalidates.
func (s *Store) ReplayServiceCredentialIssue(ctx context.Context, admin CredentialAdmin, in CredentialIssueInput) (*CredentialResult, error) {
	in, digest, err := normalizeCredentialIssue(in)
	if err != nil {
		return nil, err
	}
	tx, replay, err := s.beginCredentialOperation(ctx, admin, in.PrincipalID, in.IdempotencyKey, digest)
	if tx != nil {
		_ = tx.Rollback()
	}
	return replay, err
}

// IssueServiceCredential returns plaintext ONLY after a new atomic commit.
// An exact retry returns an empty string and current metadata, never raw replay.
func (s *Store) IssueServiceCredential(ctx context.Context, admin CredentialAdmin, in CredentialIssueInput, source string) (string, CredentialResult, error) {
	in, digest, err := normalizeCredentialIssue(in)
	if err != nil {
		return "", CredentialResult{}, err
	}
	tx, replay, err := s.beginCredentialOperation(ctx, admin, in.PrincipalID, in.IdempotencyKey, digest)
	if err != nil {
		return "", CredentialResult{}, err
	}
	if replay != nil {
		return "", *replay, nil
	}
	defer tx.Rollback()
	root, err := readCredential(ctx, tx, "WHERE token_id=?", in.PrincipalID)
	if err != nil || root.PrincipalID != in.PrincipalID {
		return "", CredentialResult{}, ErrCredentialRequest
	}
	previous, err := readCredential(ctx, tx, "WHERE token_id=?", in.CredentialID)
	if err != nil || previous.PrincipalID != in.PrincipalID || previous.ClientID != root.ClientID || previous.Label != root.Label || !slices.Equal(previous.Scopes, root.Scopes) || !slices.Equal(previous.NamespaceGlobs, root.NamespaceGlobs) {
		return "", CredentialResult{}, ErrCredentialRequest
	}
	if err = advanceCredentialGeneration(ctx, tx, in.PrincipalID, in.ExpectedGeneration); err != nil {
		return "", CredentialResult{}, err
	}
	secret, err := generateToken()
	if err != nil {
		return "", CredentialResult{}, ErrCredentialUnavailable
	}
	id, err := generateTokenID()
	if err != nil {
		return "", CredentialResult{}, ErrCredentialUnavailable
	}
	now := time.Now().UTC()
	expires := ""
	if in.TTLSeconds != nil {
		expires = now.Add(time.Duration(*in.TTLSeconds) * time.Second).Format(time.RFC3339Nano)
	}
	// Copy the exact persisted claims, including null/empty arrays, without any
	// caller-supplied identity or CreateAuthToken's permissive defaults.
	_, err = tx.ExecContext(ctx, `INSERT INTO auth_tokens(token_id,token_hash,label,client_id,scopes,namespace_globs,created_at,expires_at,revoked_at,principal_id)
 SELECT ?,?,label,client_id,scopes,namespace_globs,?,?,NULL,principal_id FROM auth_tokens WHERE token_id=?`, id, hashToken(secret), now.Format(time.RFC3339Nano), expires, in.PrincipalID)
	if err != nil {
		return "", CredentialResult{}, ErrCredentialUnavailable
	}
	rows, err := tx.QueryContext(ctx, `SELECT token_id FROM auth_tokens WHERE principal_id=? AND token_id<>?`, in.PrincipalID, id)
	if err != nil {
		return "", CredentialResult{}, ErrCredentialUnavailable
	}
	var ids []string
	for rows.Next() {
		var oldID string
		if rows.Scan(&oldID) != nil {
			_ = rows.Close()
			return "", CredentialResult{}, ErrCredentialUnavailable
		}
		ids = append(ids, oldID)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return "", CredentialResult{}, ErrCredentialUnavailable
	}
	capAt := now.Add(time.Duration(*in.OverlapSeconds) * time.Second)
	for _, oldID := range ids {
		m, e := readCredential(ctx, tx, "WHERE token_id=?", oldID)
		if e != nil {
			return "", CredentialResult{}, e
		}
		if credentialState(m, now) != nil {
			continue
		}
		deadline := capAt
		if m.OverlapUntil != "" {
			old, e := time.Parse(time.RFC3339Nano, m.OverlapUntil)
			if e != nil {
				return "", CredentialResult{}, ErrCredentialUnavailable
			}
			if old.Before(deadline) {
				deadline = old
			}
		}
		if m.ExpiresAt != "" {
			old, e := time.Parse(time.RFC3339Nano, m.ExpiresAt)
			if e != nil {
				return "", CredentialResult{}, ErrCredentialUnavailable
			}
			if old.Before(deadline) {
				deadline = old
			}
		}
		if _, e = tx.ExecContext(ctx, `UPDATE auth_tokens SET overlap_until=? WHERE token_id=?`, deadline.Format(time.RFC3339Nano), oldID); e != nil {
			return "", CredentialResult{}, ErrCredentialUnavailable
		}
	}
	generation := in.ExpectedGeneration + 1
	if err = recordCredentialOperation(ctx, tx, admin.id, in.PrincipalID, in.IdempotencyKey, digest, id, "issue", source, generation); err != nil {
		return "", CredentialResult{}, err
	}
	m, err := readCredential(ctx, tx, "WHERE token_id=?", id)
	if err != nil {
		return "", CredentialResult{}, err
	}
	if tx.Commit() != nil {
		return "", CredentialResult{}, ErrCredentialUnavailable
	}
	r := resultFor(m, generation, false)
	r.SecretAvailable = true
	return secret, r, nil
}
func (s *Store) RevokeServiceCredential(ctx context.Context, admin CredentialAdmin, in CredentialRevokeInput, source string) (CredentialResult, error) {
	if !credentialRequestValid(in.PrincipalID, in.CredentialID, in.IdempotencyKey, in.ExpectedGeneration) {
		return CredentialResult{}, ErrCredentialRequest
	}
	digest := digestCredentialRequest(struct {
		Kind  string
		Input CredentialRevokeInput
	}{"revoke", in})
	tx, replay, err := s.beginCredentialOperation(ctx, admin, in.PrincipalID, in.IdempotencyKey, digest)
	if err != nil {
		return CredentialResult{}, err
	}
	if replay != nil {
		return *replay, nil
	}
	defer tx.Rollback()
	m, err := readCredential(ctx, tx, "WHERE token_id=?", in.CredentialID)
	if err != nil || m.PrincipalID != in.PrincipalID {
		return CredentialResult{}, ErrCredentialRequest
	}
	if m.RevokedAt != "" {
		return resultFor(m, 0, false), ErrAuthTokenRevoked
	}
	if err = advanceCredentialGeneration(ctx, tx, in.PrincipalID, in.ExpectedGeneration); err != nil {
		return CredentialResult{}, err
	}
	m.RevokedAt = time.Now().UTC().Format(time.RFC3339Nano)
	m.Generation = in.ExpectedGeneration + 1
	if _, err = tx.ExecContext(ctx, `UPDATE auth_tokens SET revoked_at=? WHERE token_id=?`, m.RevokedAt, m.TokenID); err != nil {
		return CredentialResult{}, ErrCredentialUnavailable
	}
	if err = recordCredentialOperation(ctx, tx, admin.id, in.PrincipalID, in.IdempotencyKey, digest, m.TokenID, "revoke", source, m.Generation); err != nil {
		return CredentialResult{}, err
	}
	if tx.Commit() != nil {
		return CredentialResult{}, ErrCredentialUnavailable
	}
	return resultFor(m, m.Generation, false), nil
}
func (s *Store) ListServiceCredentials(ctx context.Context, admin CredentialAdmin, principal string) (CredentialList, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return CredentialList{}, ErrCredentialUnavailable
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `UPDATE auth_credential_families SET generation=generation WHERE principal_id=?`, principal); err != nil {
		return CredentialList{}, ErrCredentialUnavailable
	}
	if err = authorizeCredentialTx(ctx, tx, admin); err != nil {
		return CredentialList{}, err
	}
	out := CredentialList{PrincipalID: principal, Credentials: []AuthToken{}}
	if err = tx.QueryRowContext(ctx, `SELECT generation FROM auth_credential_families WHERE principal_id=?`, principal).Scan(&out.Generation); err != nil {
		return out, ErrCredentialRequest
	}
	rows, err := tx.QueryContext(ctx, `SELECT token_id FROM auth_tokens WHERE principal_id=? ORDER BY created_at,token_id`, principal)
	if err != nil {
		return out, ErrCredentialUnavailable
	}
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) != nil {
			_ = rows.Close()
			return out, ErrCredentialUnavailable
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return out, ErrCredentialUnavailable
	}
	for _, id := range ids {
		m, e := readCredential(ctx, tx, "WHERE token_id=?", id)
		if e != nil {
			return out, e
		}
		m.Status = resultFor(m, 0, false).Status
		out.Credentials = append(out.Credentials, m)
	}
	if tx.Commit() != nil {
		return out, ErrCredentialUnavailable
	}
	return out, nil
}
func (s *Store) revokeLegacyCredential(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ErrCredentialUnavailable
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `UPDATE auth_tokens SET token_id=token_id WHERE token_id=?`, id); err != nil {
		return ErrCredentialUnavailable
	}
	m, err := readCredential(ctx, tx, "WHERE token_id=?", id)
	if err != nil {
		return err
	}
	if m.RevokedAt != "" {
		return ErrAuthTokenInvalid
	}
	if err = advanceCredentialGeneration(ctx, tx, m.PrincipalID, m.Generation); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE auth_tokens SET revoked_at=? WHERE token_id=?`, time.Now().UTC().Format(time.RFC3339Nano), id); err != nil {
		return ErrCredentialUnavailable
	}
	if err = auditCredentialMutation(ctx, tx, "local-store-owner", m.PrincipalID, id, "revoke", "local-owner", m.Generation+1); err != nil {
		return err
	}
	if tx.Commit() != nil {
		return ErrCredentialUnavailable
	}
	return nil
}

func (a CredentialAdmin) String() string { return fmt.Sprintf("CredentialAdmin(%s)", a.id) }

// GoString keeps debug formatting from disclosing the private verifier digest.
func (a CredentialAdmin) GoString() string { return a.String() }
