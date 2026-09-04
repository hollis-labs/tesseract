package contextstore

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/hollis-labs/tesseract/internal/memorytime"
	"github.com/hollis-labs/tesseract/internal/sqlitedsn"

	_ "modernc.org/sqlite"
)

const (
	schemaVersion = 16

	// defaultTokenScopes is the full-access scopes JSON assigned to legacy tokens and new tokens without explicit scopes.
	defaultTokenScopes = `["write","promote.request","promote.approve","promote.apply","packet","repair","namespace.register"]`
	// defaultTokenNamespaceGlobs grants access to all namespaces.
	defaultTokenNamespaceGlobs = `["*"]`
	// DefaultSelectLimit bounds selector response size when no explicit limit is provided.
	DefaultSelectLimit = 200
	// MaxSelectLimit is the upper bound accepted for selector limits.
	MaxSelectLimit        = 500
	maxSelectorNamespaces = 32
	maxSelectorKeys       = 128
)

var (
	// ErrConsistencyFault indicates indexed metadata pointing at a missing/corrupt payload file.
	ErrConsistencyFault = errors.New("contextstore consistency fault")
	// ErrAuthTokenInvalid indicates token not found.
	ErrAuthTokenInvalid = errors.New("auth token invalid")
	// ErrAuthTokenRevoked indicates token has been revoked.
	ErrAuthTokenRevoked = errors.New("auth token revoked")
	// ErrAuthTokenExpired indicates token has expired.
	ErrAuthTokenExpired = errors.New("auth token expired")
)

// Config defines on-disk layout.
type Config struct {
	RootDir    string
	RecordsDir string
	DBPath     string
}

// Store manages append-only payloads and metadata index/heads.
type Store struct {
	db         *sql.DB
	recordsDir string
	dbPath     string
}

// Record contains a stored revision.
type Record struct {
	RecordID       string          `json:"record_id"`
	Namespace      string          `json:"namespace"`
	Key            string          `json:"key"`
	Revision       int64           `json:"revision"`
	Actor          string          `json:"actor"`
	CreatedAt      string          `json:"created_at"`
	Checksum       string          `json:"checksum"`
	Payload        json.RawMessage `json:"payload"`
	RecordType     string          `json:"record_type,omitempty"`
	Status         string          `json:"status,omitempty"`
	TTL            string          `json:"ttl,omitempty"`
	ContentVersion int64           `json:"content_version,omitempty"`
	Pointers       []string        `json:"pointers,omitempty"`
	Provenance     json.RawMessage `json:"provenance,omitempty"`
}

// Selector defines deterministic record selection criteria.
type Selector struct {
	Namespaces    []string `json:"namespaces,omitempty"`
	Keys          []string `json:"keys,omitempty"`
	RevisionScope string   `json:"revision_scope,omitempty"` // head|all
	Order         []string `json:"order,omitempty"`          // namespace|key|revision|created_asc|created_desc
	Limit         int      `json:"limit,omitempty"`
	TagsAny       []string `json:"tags_any,omitempty"` // records with at least one of these tags
	Types         []string `json:"types,omitempty"`    // filter by record_type
	Statuses      []string `json:"statuses,omitempty"` // filter by status
}

// ConsistencyIssue reports a deterministic storage/index inconsistency finding.
type ConsistencyIssue struct {
	Type      string `json:"type"`
	Namespace string `json:"namespace,omitempty"`
	Key       string `json:"key,omitempty"`
	RecordID  string `json:"record_id,omitempty"`
	Revision  int64  `json:"revision,omitempty"`
	FilePath  string `json:"file_path,omitempty"`
	Detail    string `json:"detail,omitempty"`
}

// AuditEvent captures structured operation metadata for observability/audit queries.
//
// RecordID is intentionally always present in JSON output (no omitempty),
// even when empty. The audit-contract golden (tests/integration/fixtures/
// audit_contract_golden.json) advertises record_id as a fixed item key, and
// some events legitimately lack one (e.g. namespace.register, maintenance.*,
// packet) — emitting "" rather than dropping the key keeps consumers from
// having to special-case key presence.
type AuditEvent struct {
	ID        int64           `json:"id"`
	EventType string          `json:"event_type"`
	Actor     string          `json:"actor"`
	Namespace string          `json:"namespace"`
	Key       string          `json:"key"`
	Revision  int64           `json:"revision"`
	RecordID  string          `json:"record_id"`
	CreatedAt string          `json:"created_at"`
	Metadata  json.RawMessage `json:"metadata,omitempty"`
}

// AuditQuery defines bounded, deterministic audit query filters.
type AuditQuery struct {
	Limit     int
	Cursor    int64
	Namespace string
	EventType string
	// Actor is a case-sensitive substring filter on the actor column. Empty
	// means no filter. Backed by SQL LIKE; small audit_events table makes
	// the absent index acceptable.
	Actor string
	// Since/Until bound created_at on the inclusive lower / inclusive upper
	// edge respectively. Empty means unbounded. Strings are compared
	// lexically — ISO 8601 timestamps sort correctly under string compare.
	Since string
	Until string
}

// AuthToken stores local token lifecycle metadata.
type AuthToken struct {
	TokenID        string   `json:"token_id"`
	Label          string   `json:"label"`
	ClientID       string   `json:"client_id,omitempty"`
	Scopes         []string `json:"scopes,omitempty"`
	NamespaceGlobs []string `json:"namespace_globs,omitempty"`
	CreatedAt      string   `json:"created_at"`
	ExpiresAt      string   `json:"expires_at,omitempty"`
	RevokedAt      string   `json:"revoked_at,omitempty"`
}

// TokenCreateInput holds parameters for creating a scoped auth token.
type TokenCreateInput struct {
	Label          string
	ClientID       string
	Scopes         []string // nil → full access defaults
	NamespaceGlobs []string // nil → ["*"]
	TTL            time.Duration
}

// NamespacePolicyEntry stores persistent namespace owner/policy metadata.
type NamespacePolicyEntry struct {
	Namespace string         `json:"namespace"`
	OwnerType string         `json:"owner_type"`
	OwnerID   string         `json:"owner_id"`
	Policy    map[string]any `json:"policy,omitempty"`
	UpdatedAt string         `json:"updated_at"`
}

// ReadinessReport summarizes operational readiness in a stable shape.
type ReadinessReport struct {
	Healthy           bool   `json:"healthy"`
	Status            string `json:"status"` // healthy|degraded|failing
	DBPath            string `json:"db_path"`
	RecordsDir        string `json:"records_dir"`
	RecordsDirExists  bool   `json:"records_dir_exists"`
	SchemaVersion     int    `json:"schema_version"`
	ConsistencyIssues int    `json:"consistency_issues"`
	GeneratedAt       string `json:"generated_at"`
}

// Open initializes schema and storage directories.
func Open(ctx context.Context, cfg Config) (*Store, error) {
	if cfg.RootDir == "" {
		cfg.RootDir = "."
	}
	if cfg.RecordsDir == "" {
		cfg.RecordsDir = filepath.Join(cfg.RootDir, "data", "records")
	}
	if cfg.DBPath == "" {
		cfg.DBPath = filepath.Join(cfg.RootDir, "data", "index", "context.db")
	}

	// A restore that died between its renames leaves a journal behind. Resolve
	// it before anything else looks at — or creates — the paths it describes,
	// so a store is never opened in a half-swapped state. See restore.go.
	if err := finishInterruptedRestore(cfg.DBPath, cfg.RecordsDir); err != nil {
		return nil, err
	}

	if err := os.MkdirAll(cfg.RecordsDir, 0o755); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(cfg.DBPath), 0o755); err != nil {
		return nil, err
	}

	db, err := openStoreDB(ctx, cfg.DBPath)
	if err != nil {
		return nil, err
	}

	s := &Store{db: db, recordsDir: cfg.RecordsDir, dbPath: cfg.DBPath}
	if err := s.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}

	return s, nil
}

// openStoreDB opens the live database with the store's standard connection
// settings. Restore reopens through the same function after swapping the file,
// so a restored store is configured identically to a freshly opened one.
func openStoreDB(ctx context.Context, dbPath string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", sqlitedsn.DSN(dbPath))
	if err != nil {
		return nil, err
	}
	// Enable WAL mode for better concurrent read/write performance.
	if _, err := db.ExecContext(ctx, `PRAGMA journal_mode=WAL`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("enable WAL mode: %w", err)
	}
	return db, nil
}

// Close closes the DB handle.
func (s *Store) Close() error {
	return s.db.Close()
}

// DB returns the underlying database handle. Used by sibling subsystems
// (memory) that share the same SQLite file but manage their own code paths.
// The returned *sql.DB is owned by Store — callers must not close it.
func (s *Store) DB() *sql.DB {
	return s.db
}

func (s *Store) migrate(ctx context.Context) (err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	if _, err = tx.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS schema_version (
	version INTEGER PRIMARY KEY,
	applied_at TEXT NOT NULL
)`); err != nil {
		return err
	}

	var version int
	err = tx.QueryRowContext(ctx, `SELECT version FROM schema_version ORDER BY version DESC LIMIT 1`).Scan(&version)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		version = 0
	}

	if version > schemaVersion {
		return fmt.Errorf("db schema version %d newer than supported %d", version, schemaVersion)
	}

	for version < schemaVersion {
		switch version + 1 {
		case 1:
			if _, err = tx.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS records (
	record_id TEXT PRIMARY KEY,
	namespace TEXT NOT NULL,
	key_name TEXT NOT NULL,
	revision INTEGER NOT NULL,
	actor TEXT NOT NULL,
	created_at TEXT NOT NULL,
	checksum TEXT,
	file_path TEXT NOT NULL,
	UNIQUE(namespace, key_name, revision)
)`); err != nil {
				return err
			}

			if _, err = tx.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS heads (
	namespace TEXT NOT NULL,
	key_name TEXT NOT NULL,
	head_revision INTEGER NOT NULL,
	head_record_id TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	PRIMARY KEY(namespace, key_name),
	FOREIGN KEY(head_record_id) REFERENCES records(record_id)
)`); err != nil {
				return err
			}

			if _, err = tx.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_records_ns_key_rev ON records(namespace, key_name, revision)`); err != nil {
				return err
			}
		case 2:
			if _, err = tx.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS audit_events (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	event_type TEXT NOT NULL,
	actor TEXT NOT NULL,
	namespace TEXT NOT NULL,
	key_name TEXT NOT NULL,
	revision INTEGER NOT NULL,
	record_id TEXT,
	created_at TEXT NOT NULL,
	metadata_json TEXT
)`); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_audit_events_created ON audit_events(created_at, id)`); err != nil {
				return err
			}
		case 3:
			if _, err = tx.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS auth_tokens (
	token_id TEXT PRIMARY KEY,
	token_hash TEXT NOT NULL UNIQUE,
	label TEXT NOT NULL,
	created_at TEXT NOT NULL,
	expires_at TEXT,
	revoked_at TEXT
)`); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_auth_tokens_created ON auth_tokens(created_at, token_id)`); err != nil {
				return err
			}
		case 4:
			if _, err = tx.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS namespace_policies (
	namespace TEXT PRIMARY KEY,
	owner_type TEXT NOT NULL,
	owner_id TEXT NOT NULL,
	policy_json TEXT,
	updated_at TEXT NOT NULL
)`); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_namespace_policies_updated ON namespace_policies(updated_at, namespace)`); err != nil {
				return err
			}
		case 5:
			if _, err = tx.ExecContext(ctx, `ALTER TABLE records ADD COLUMN metadata_json TEXT`); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS record_tags (
	record_id TEXT NOT NULL,
	tag TEXT NOT NULL,
	UNIQUE(record_id, tag)
)`); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_record_tags_tag ON record_tags(tag)`); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_record_tags_record_id ON record_tags(record_id)`); err != nil {
				return err
			}
		case 6:
			if _, err = tx.ExecContext(ctx, `ALTER TABLE auth_tokens ADD COLUMN client_id TEXT NOT NULL DEFAULT ''`); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `ALTER TABLE auth_tokens ADD COLUMN scopes TEXT NOT NULL DEFAULT '`+defaultTokenScopes+`'`); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `ALTER TABLE auth_tokens ADD COLUMN namespace_globs TEXT NOT NULL DEFAULT '`+defaultTokenNamespaceGlobs+`'`); err != nil {
				return err
			}
			// Backfill any rows with empty/null scopes or namespace_globs.
			if _, err = tx.ExecContext(ctx, `UPDATE auth_tokens SET scopes = '`+defaultTokenScopes+`' WHERE scopes = '' OR scopes = '[]'`); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `UPDATE auth_tokens SET namespace_globs = '`+defaultTokenNamespaceGlobs+`' WHERE namespace_globs = '' OR namespace_globs = '[]'`); err != nil {
				return err
			}
		case 7:
			// Context types: add record_type, status, ttl, version, pointers to records.
			if _, err = tx.ExecContext(ctx, `ALTER TABLE records ADD COLUMN record_type TEXT NOT NULL DEFAULT ''`); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `ALTER TABLE records ADD COLUMN status TEXT NOT NULL DEFAULT ''`); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `ALTER TABLE records ADD COLUMN ttl TEXT NOT NULL DEFAULT ''`); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `ALTER TABLE records ADD COLUMN content_version INTEGER NOT NULL DEFAULT 0`); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `ALTER TABLE records ADD COLUMN pointers_json TEXT NOT NULL DEFAULT '[]'`); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `ALTER TABLE records ADD COLUMN provenance_json TEXT NOT NULL DEFAULT ''`); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_records_type ON records(record_type)`); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_records_status ON records(status)`); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_records_type_status ON records(record_type, status)`); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_records_ttl ON records(ttl)`); err != nil {
				return err
			}
			// Add the same columns to heads for fast type/status queries.
			if _, err = tx.ExecContext(ctx, `ALTER TABLE heads ADD COLUMN record_type TEXT NOT NULL DEFAULT ''`); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `ALTER TABLE heads ADD COLUMN status TEXT NOT NULL DEFAULT ''`); err != nil {
				return err
			}
		case 8:
			// Embeddings table for vector search (ADR-005).
			if _, err = tx.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS embeddings (
	record_id TEXT NOT NULL,
	model TEXT NOT NULL,
	dimensions INTEGER NOT NULL,
	vector BLOB NOT NULL,
	created_at TEXT NOT NULL DEFAULT (datetime('now')),
	PRIMARY KEY (record_id, model),
	FOREIGN KEY (record_id) REFERENCES records(record_id) ON DELETE CASCADE
)`); err != nil {
				return err
			}
		case 9:
			// Memory subsystem tables — mutable state + append-only revision log.
			if _, err = tx.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS memory_state (
	memory_id        TEXT PRIMARY KEY,
	namespace        TEXT NOT NULL,
	memory_key       TEXT NULL,
	current_revision TEXT NULL,
	activation       REAL NOT NULL DEFAULT 1.0,
	access_count     INTEGER NOT NULL DEFAULT 0,
	last_accessed_at TEXT NULL,
	created_at       TEXT NOT NULL DEFAULT (datetime('now')),
	UNIQUE(namespace, memory_key)
)`); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_memory_state_namespace ON memory_state(namespace)`); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_memory_state_activation ON memory_state(activation DESC)`); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS memory_revisions (
	revision_id      TEXT PRIMARY KEY,
	memory_id        TEXT NOT NULL,
	namespace        TEXT NOT NULL,
	memory_key       TEXT NULL,
	status           TEXT NOT NULL,
	supersedes       TEXT NULL,
	created_at       TEXT NOT NULL DEFAULT (datetime('now')),
	author_agent_id  TEXT NOT NULL,
	author_version   TEXT NOT NULL,
	-- "trigger" is a SQLite reserved word; always quote it in DML (INSERT/SELECT/WHERE).
	"trigger"        TEXT NOT NULL,
	session_id       TEXT NOT NULL,
	origin           TEXT NOT NULL,
	confidence       REAL NOT NULL,
	tags             TEXT NOT NULL DEFAULT '[]',
	ttl_seconds      INTEGER NULL,
	expires_at       TEXT NULL,
	payload_summary  TEXT NULL,
	payload_body     TEXT NULL,
	embedding_model  TEXT NULL,
	embedding_vector BLOB NULL,
	FOREIGN KEY (memory_id) REFERENCES memory_state(memory_id) ON DELETE CASCADE,
	FOREIGN KEY (supersedes) REFERENCES memory_revisions(revision_id)
)`); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_memory_revisions_memory_id ON memory_revisions(memory_id)`); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_memory_revisions_namespace ON memory_revisions(namespace)`); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_memory_revisions_created_at ON memory_revisions(created_at)`); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_memory_revisions_status ON memory_revisions(status)`); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_memory_revisions_expires_at ON memory_revisions(expires_at) WHERE expires_at IS NOT NULL`); err != nil {
				return err
			}
		case 10:
			// Domain discriminator: split memory_state + memory_revisions into
			// per-domain policies (memory, knowledge, ...). Existing rows
			// backfill to 'memory' via the column default.
			if _, err = tx.ExecContext(ctx, `ALTER TABLE memory_state ADD COLUMN domain TEXT NOT NULL DEFAULT 'memory'`); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `ALTER TABLE memory_revisions ADD COLUMN domain TEXT NOT NULL DEFAULT 'memory'`); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_memory_state_domain ON memory_state(domain)`); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_memory_revisions_domain ON memory_revisions(domain)`); err != nil {
				return err
			}
		case 11:
			// Knowledge facets: kind, source, pointer{scheme, locator,
			// resolved_at}. Nullable on memory_revisions — only populated for
			// the knowledge domain. Flat columns chosen over a side table for
			// filter/facet histogram performance in tesseract_recall.
			if _, err = tx.ExecContext(ctx, `ALTER TABLE memory_revisions ADD COLUMN facet_kind TEXT NULL`); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `ALTER TABLE memory_revisions ADD COLUMN facet_source TEXT NULL`); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `ALTER TABLE memory_revisions ADD COLUMN facet_pointer_scheme TEXT NULL`); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `ALTER TABLE memory_revisions ADD COLUMN facet_pointer_locator TEXT NULL`); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `ALTER TABLE memory_revisions ADD COLUMN facet_pointer_resolved_at TEXT NULL`); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_memory_revisions_facet_kind ON memory_revisions(facet_kind) WHERE facet_kind IS NOT NULL`); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_memory_revisions_facet_source ON memory_revisions(facet_source) WHERE facet_source IS NOT NULL`); err != nil {
				return err
			}
		case 12:
			// FTS5 external-content virtual table over memory_revisions,
			// indexing the text columns used by the BM25 arm of hybrid
			// relevance recall (EPIC-20260414-19124). Content columns
			// (payload_summary, payload_body, tags) never mutate in
			// production, so only INSERT + DELETE triggers are needed.
			// Status is intentionally NOT indexed — status filtering lives
			// at query time via JOIN to keep the BM25 arm deterministic
			// (see also memory/recall.go).
			if _, err = tx.ExecContext(ctx, `
CREATE VIRTUAL TABLE IF NOT EXISTS memory_revisions_fts USING fts5(
    payload_summary,
    payload_body,
    tags,
    content='memory_revisions',
    content_rowid='rowid'
)`); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `
INSERT INTO memory_revisions_fts(rowid, payload_summary, payload_body, tags)
SELECT rowid, payload_summary, payload_body, tags FROM memory_revisions`); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `
CREATE TRIGGER IF NOT EXISTS memory_revisions_fts_ai
AFTER INSERT ON memory_revisions BEGIN
    INSERT INTO memory_revisions_fts(rowid, payload_summary, payload_body, tags)
    VALUES (new.rowid, new.payload_summary, new.payload_body, new.tags);
END`); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `
CREATE TRIGGER IF NOT EXISTS memory_revisions_fts_ad
AFTER DELETE ON memory_revisions BEGIN
    INSERT INTO memory_revisions_fts(memory_revisions_fts, rowid, payload_summary, payload_body, tags)
    VALUES ('delete', old.rowid, old.payload_summary, old.payload_body, old.tags);
END`); err != nil {
				return err
			}
		case 13:
			// Pointer verification log (CW-20260825-0015).
			//
			// This table is the authority for whether a knowledge pointer was
			// ACTUALLY verified against the outside world.
			// memory_revisions.facet_pointer_resolved_at stays what it has
			// always honestly been: a write-time assertion by the author.
			//
			// It is a side table rather than a column on memory_revisions, and
			// rather than a superseding revision per check, because a
			// verification is an observation about the world — not authored
			// content. Updating the revision in place would rewrite history;
			// minting a revision per check would generate revision churn
			// proportional to corpus size x run frequency for something nobody
			// authored.
			//
			// Observation content is append-only by construction: runtime paths
			// only INSERT rows and never UPDATE or DELETE observations. Schema
			// migration 16 performs a one-time representation-only UPDATE of
			// checked_at into a sortable fixed-width timestamp; it preserves the
			// instant and every observation row. The full history therefore
			// survives, which lets a caller tell one bad afternoon from a pointer
			// that has never resolved.
			//
			// scheme/locator are denormalized onto the row on purpose: a
			// revision is immutable, so they cannot drift, and carrying them
			// makes each observation self-describing in a report without a
			// join back to memory_revisions.
			//
			// The CHECK on `outcome` is the enforcement half of the
			// vocabulary. Without it the three values live only in Go, and
			// anything holding the *sql.DB — a repair script, a future
			// subsystem, a person with sqlite3 — can insert a fourth. It
			// would then surface verbatim on every read surface while being
			// rejected as a filter argument: visible in results, unreachable
			// by query, invisible to enumeration. That is exactly the failure
			// this table exists to remove, one layer down.
			//
			// `not_applicable` is deliberately NOT permitted. It is derived
			// from the pointer scheme at read time and is never an
			// observation, so the constraint makes "never stored" true by
			// construction rather than by convention.
			//
			// It has to land here, in the migration that creates the table:
			// SQLite cannot add a CHECK to an existing table without a full
			// rebuild, so once any store reaches version 13 the cheap moment
			// has passed. memory.PointerOutcomeVocabulary() is the Go half;
			// TestPointerVerificationOutcomeCheckMatchesVocabulary binds them.
			if _, err = tx.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS pointer_verifications (
	id           INTEGER PRIMARY KEY AUTOINCREMENT,
	revision_id  TEXT NOT NULL,
	scheme       TEXT NOT NULL,
	locator      TEXT NOT NULL,
	outcome      TEXT NOT NULL CHECK (outcome IN ('resolved', 'unresolvable', 'unverifiable')),
	checked_at   TEXT NOT NULL,
	detail       TEXT NOT NULL DEFAULT '',
	FOREIGN KEY (revision_id) REFERENCES memory_revisions(revision_id) ON DELETE CASCADE
)`); err != nil {
				return err
			}
			// The latest-observation lookup is (revision_id, id DESC); the
			// outcome index serves the health distribution aggregate.
			if _, err = tx.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_pointer_verifications_revision ON pointer_verifications(revision_id, id DESC)`); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_pointer_verifications_outcome ON pointer_verifications(outcome)`); err != nil {
				return err
			}
		case 14:
			// Decay baseline (CW-20260826-0001).
			//
			// memory_state.last_decayed_at records the instant up to which decay
			// has been applied to a row. The decay pass computes elapsed from it
			// and advances it in the same UPDATE that writes activation, so a
			// pass can never re-apply time it has already applied. Before this
			// column, elapsed was computed from last_accessed_at (else
			// created_at) — a baseline the pass never moved — so every pass
			// re-multiplied the already-decayed value over a growing interval
			// and decay compounded superlinearly.
			//
			// It is a separate column rather than a reuse of last_accessed_at
			// on purpose. Advancing last_accessed_at on decay would fix the
			// compounding by making the decay job forge a read, which destroys
			// the one signal tesseract_touch exists to record and corrupts
			// every "when was this last actually read" diagnostic.
			//
			// NULL means "never decayed"; readers fall back to created_at,
			// which is the honest baseline for a fresh row: activation is the
			// 1.0 insert default, established at created_at. Nothing has to
			// remember to stamp the column on INSERT for that to be right.
			if _, err = tx.ExecContext(ctx, `ALTER TABLE memory_state ADD COLUMN last_decayed_at TEXT NULL`); err != nil {
				return err
			}
			// Existing rows are stamped `now` rather than backfilled from
			// last_accessed_at/created_at, and this is a deliberate choice
			// about damage rather than a default.
			//
			// Backfilling the old baseline would hand the first post-migration
			// pass an elapsed of the row's entire lifetime — months, for most
			// of a mature corpus — and one honest application of the half-life
			// over that interval drives essentially every row to the floor in
			// a single pass. That would finish, in one transaction, exactly the
			// destruction this ticket exists to stop, and it would take the
			// small set of recently-reinforced rows that still carry signal
			// with it.
			//
			// Stamping `now` stops the compounding at the migration instant and
			// freezes each row at its current value, correct or not. It makes
			// no attempt to reconstruct what activation "should" have been:
			// activation is a mutable column with no history — memory_state is
			// overwritten in place and no audit records prior values — so the
			// pre-decay levels are not recoverable from this database. Any
			// backfill curve would be invented, and an invented level is worse
			// than a visibly frozen one because it looks measured.
			//
			// The corpus therefore keeps its existing distribution, most of it
			// crushed to the floor, and re-accumulates real signal from touches
			// after this point. That is the recovery path; there is no other.
			if _, err = tx.ExecContext(ctx, `UPDATE memory_state SET last_decayed_at = ?`,
				memorytime.Format(time.Now())); err != nil {
				return err
			}
		case 15:
			// memory_key joins the lexical index (CW-20260903-0062).
			//
			// Before this, memory_revisions_fts carried payload_summary,
			// payload_body and tags only. The consequence was backwards and
			// load-bearing: an exact-key lexical search returned every record
			// that MENTIONED the key -- records cite each other by key in
			// [[wikilink]] form all over this corpus -- and never the record
			// that WAS it. `migrate_mcp_keys_to_1password` is canonical,
			// current, and in the namespace searched, and searching it
			// returned results_total: 0. An empty page reads as "no such
			// record", so the failure was silent and confident.
			//
			// That is the one field a caller reaching for search_mode=lexical
			// is most likely to be holding, because the tool's own guidance
			// sends identifier lookups here.
			//
			// FTS5 has no ALTER TABLE ADD COLUMN, so widening the index means
			// dropping and rebuilding it. The triggers go first: they name the
			// old column list, and a trigger referencing a dropped table is
			// only an error when it fires, which would be at the next write
			// rather than here.
			//
			// The rebuild reindexes the whole corpus from memory_revisions, so
			// existing records are covered by this migration and no separate
			// reindex step or one-off script is needed. It runs once, inside
			// the same transaction as the rest of the ladder, on the first
			// open after this version ships.
			for _, stmt := range []string{
				`DROP TRIGGER IF EXISTS memory_revisions_fts_ai`,
				`DROP TRIGGER IF EXISTS memory_revisions_fts_ad`,
				`DROP TRIGGER IF EXISTS memory_revisions_fts_au`,
				`DROP TABLE IF EXISTS memory_revisions_fts`,
			} {
				if _, err = tx.ExecContext(ctx, stmt); err != nil {
					return err
				}
			}
			// memory_key leads the column list because bm25() weights are
			// positional: internal/memory/bm25.go pins the weight vector to
			// this order, and TestBM25ColumnWeightsMatchIndexOrder binds the
			// two so a future column cannot silently shift what is boosted.
			if _, err = tx.ExecContext(ctx, `
CREATE VIRTUAL TABLE memory_revisions_fts USING fts5(
    memory_key,
    payload_summary,
    payload_body,
    tags,
    content='memory_revisions',
    content_rowid='rowid'
)`); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `
INSERT INTO memory_revisions_fts(rowid, memory_key, payload_summary, payload_body, tags)
SELECT rowid, memory_key, payload_summary, payload_body, tags FROM memory_revisions`); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `
CREATE TRIGGER memory_revisions_fts_ai
AFTER INSERT ON memory_revisions BEGIN
    INSERT INTO memory_revisions_fts(rowid, memory_key, payload_summary, payload_body, tags)
    VALUES (new.rowid, new.memory_key, new.payload_summary, new.payload_body, new.tags);
END`); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `
CREATE TRIGGER memory_revisions_fts_ad
AFTER DELETE ON memory_revisions BEGIN
    INSERT INTO memory_revisions_fts(memory_revisions_fts, rowid, memory_key, payload_summary, payload_body, tags)
    VALUES ('delete', old.rowid, old.memory_key, old.payload_summary, old.payload_body, old.tags);
END`); err != nil {
				return err
			}
			// An AFTER UPDATE trigger, which migration 12 deliberately did not
			// have. Its reasoning -- "content columns never mutate in
			// production" -- was true of payload_summary and payload_body and
			// is NOT true of memory_key: ApplyMigration (internal/memory/
			// migrate.go) rewrites namespace, memory_key and tags in place
			// when a namespace is renamed. Indexing memory_key without this
			// would ship a field that goes stale the first time anyone renames
			// a key, and a stale index entry is worse than a missing one --
			// it answers.
			//
			// tags had the same exposure already and was silently desyncing on
			// that path; the same trigger fixes it, which is why it is here
			// rather than in a ticket of its own.
			//
			// The WHEN clause is what keeps this cheap. memory_revisions is
			// UPDATEd on two hot paths that touch no indexed column --
			// status transitions (write.go) and embedding writes (embed.go) --
			// and without the guard each one would delete and re-tokenize the
			// full body. `IS NOT` rather than `!=` because these columns are
			// nullable and `NULL != NULL` is NULL, which would skip the
			// reindex exactly when a column is set or cleared.
			if _, err = tx.ExecContext(ctx, `
CREATE TRIGGER memory_revisions_fts_au
AFTER UPDATE ON memory_revisions
WHEN old.memory_key IS NOT new.memory_key
  OR old.payload_summary IS NOT new.payload_summary
  OR old.payload_body IS NOT new.payload_body
  OR old.tags IS NOT new.tags
BEGIN
    INSERT INTO memory_revisions_fts(memory_revisions_fts, rowid, memory_key, payload_summary, payload_body, tags)
    VALUES ('delete', old.rowid, old.memory_key, old.payload_summary, old.payload_body, old.tags);
    INSERT INTO memory_revisions_fts(rowid, memory_key, payload_summary, payload_body, tags)
    VALUES (new.rowid, new.memory_key, new.payload_summary, new.payload_body, new.tags);
END`); err != nil {
				return err
			}
		case 16:
			// Terminal-deprecation lookup (CW-20260903-0061).
			//
			// revision_scope=current normally joins through
			// memory_state.current_revision. When a caller explicitly requests
			// deprecated status, recall also admits a deprecated revision only
			// when no later revision points at it through supersedes. That
			// anti-lookup runs once per candidate on metadata, dense, and BM25
			// paths, so leaving supersedes unindexed turns it into a correlated
			// full-table scan. NULL rows cannot match an equality lookup and are
			// omitted from the index to keep it proportional to real lineage
			// edges rather than to all revisions.
			if _, err = tx.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_memory_revisions_supersedes ON memory_revisions(supersedes) WHERE supersedes IS NOT NULL`); err != nil {
				return err
			}
			// verify-pointers --apply validates newly committed observations with
			// a checked_at range count. Keep that postcondition proportional to
			// the new batch rather than to the append-only verification history.
			if _, err = tx.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_pointer_verifications_checked_at ON pointer_verifications(checked_at)`); err != nil {
				return err
			}
			// Memory timestamps used RFC3339Nano, whose omitted trailing
			// fractional zeroes break chronological TEXT ordering inside one
			// second (.092342Z sorts before .09234Z). Normalize every
			// memory-owned timestamp atomically to fixed-width UTC nanoseconds.
			// This preserves the existing created_at/expires_at indexes and the
			// direct SQL ORDER BY/range/MAX paths that rely on them.
			if err = normalizeMemoryTimestamps(ctx, tx); err != nil {
				return err
			}
		}

		version++
		if _, err = tx.ExecContext(ctx, `INSERT INTO schema_version (version, applied_at) VALUES (?, ?)`, version, time.Now().UTC().Format(time.RFC3339)); err != nil {
			return err
		}
	}

	return tx.Commit()
}

// AppendInput defines a new write.
type AppendInput struct {
	Namespace string
	Key       string
	Actor     string
	Payload   json.RawMessage
	// Metadata is optional structured metadata stored alongside the record.
	// Tags are extracted from metadata.tags ([]string) for filtering.
	Metadata json.RawMessage
	// RecordType is the context type (e.g. "task/spec", "decision/adr").
	RecordType string
	// Status is the lifecycle state (draft|reviewed|canonical|deprecated).
	Status string
	// TTL is an optional RFC3339 expiration timestamp.
	TTL string
	// ContentVersion is a monotonic version number.
	ContentVersion int64
	// Pointers is a list of references (repo paths, commit SHAs, URLs).
	Pointers []string
	// Provenance is structured provenance metadata.
	Provenance json.RawMessage
}

// AppendRecord appends a new immutable revision and advances heads atomically in SQLite.
func (s *Store) AppendRecord(ctx context.Context, in AppendInput) (_ Record, err error) {
	ns, err := sanitizePathPart(in.Namespace)
	if err != nil {
		return Record{}, fmt.Errorf("namespace: %w", err)
	}
	key, err := sanitizePathPart(in.Key)
	if err != nil {
		return Record{}, fmt.Errorf("key: %w", err)
	}
	if strings.TrimSpace(in.Actor) == "" {
		return Record{}, errors.New("actor required")
	}
	if len(in.Payload) == 0 {
		return Record{}, errors.New("payload required")
	}
	if !json.Valid(in.Payload) {
		return Record{}, errors.New("payload must be valid JSON")
	}

	// Make sure the namespace is in the policy registry before we persist
	// data for it. Idempotent. See CW-20260428-0005.
	if _, err := s.ensureNamespaceRegistered(ctx, ns, "inferred"); err != nil {
		return Record{}, fmt.Errorf("ensure namespace registered: %w", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	recordID := fmt.Sprintf("rec_%d", time.Now().UTC().UnixNano())

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Record{}, err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	var rev int64
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(revision), 0) + 1 FROM records WHERE namespace = ? AND key_name = ?`, ns, key).Scan(&rev); err != nil {
		return Record{}, err
	}

	var compactBuf bytes.Buffer
	if err = json.Compact(&compactBuf, in.Payload); err != nil {
		return Record{}, fmt.Errorf("compact payload: %w", err)
	}
	compactPayload := compactBuf.Bytes()

	relPath := filepath.Join(ns, key, fmt.Sprintf("%d.json", rev))
	absPath := filepath.Join(s.recordsDir, relPath)
	if err = os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		return Record{}, err
	}
	if err = os.WriteFile(absPath, compactPayload, 0o644); err != nil {
		return Record{}, err
	}

	h := sha256.Sum256(compactPayload)
	checksum := hex.EncodeToString(h[:])

	var metaStr string
	if len(in.Metadata) > 0 && json.Valid(in.Metadata) {
		metaStr = string(in.Metadata)
	}

	recordType := strings.TrimSpace(in.RecordType)
	status := strings.TrimSpace(in.Status)
	ttl := strings.TrimSpace(in.TTL)
	contentVersion := in.ContentVersion
	pointersJSON := "[]"
	if len(in.Pointers) > 0 {
		pj, _ := json.Marshal(in.Pointers)
		pointersJSON = string(pj)
	}
	provenanceStr := ""
	if len(in.Provenance) > 0 && json.Valid(in.Provenance) {
		provenanceStr = string(in.Provenance)
	}

	_, err = tx.ExecContext(ctx, `
INSERT INTO records (record_id, namespace, key_name, revision, actor, created_at, checksum, file_path, metadata_json,
	record_type, status, ttl, content_version, pointers_json, provenance_json)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
`, recordID, ns, key, rev, in.Actor, now, checksum, relPath, metaStr,
		recordType, status, ttl, contentVersion, pointersJSON, provenanceStr)
	if err != nil {
		_ = os.Remove(absPath)
		return Record{}, err
	}

	for _, tag := range extractTags(in.Metadata) {
		if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO record_tags (record_id, tag) VALUES (?, ?)`, recordID, strings.TrimSpace(tag)); err != nil {
			_ = os.Remove(absPath)
			return Record{}, err
		}
	}

	_, err = tx.ExecContext(ctx, `
INSERT INTO heads (namespace, key_name, head_revision, head_record_id, updated_at, record_type, status)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(namespace, key_name) DO UPDATE SET
	head_revision=excluded.head_revision,
	head_record_id=excluded.head_record_id,
	updated_at=excluded.updated_at,
	record_type=excluded.record_type,
	status=excluded.status
`, ns, key, rev, recordID, now, recordType, status)
	if err != nil {
		_ = os.Remove(absPath)
		return Record{}, err
	}

	if err = tx.Commit(); err != nil {
		_ = os.Remove(absPath)
		return Record{}, err
	}

	var pointers []string
	if len(in.Pointers) > 0 {
		pointers = in.Pointers
	}

	return Record{
		RecordID:       recordID,
		Namespace:      ns,
		Key:            key,
		Revision:       rev,
		Actor:          in.Actor,
		CreatedAt:      now,
		Checksum:       checksum,
		Payload:        append(json.RawMessage(nil), compactPayload...),
		RecordType:     recordType,
		Status:         status,
		TTL:            ttl,
		ContentVersion: contentVersion,
		Pointers:       pointers,
		Provenance:     in.Provenance,
	}, nil
}

// Head returns the current head record for namespace/key.
func (s *Store) Head(ctx context.Context, namespace, key string) (Record, error) {
	ns, err := sanitizePathPart(namespace)
	if err != nil {
		return Record{}, fmt.Errorf("namespace: %w", err)
	}
	k, err := sanitizePathPart(key)
	if err != nil {
		return Record{}, fmt.Errorf("key: %w", err)
	}

	var rec Record
	var relPath string
	var pointersJSON, provenanceStr string
	err = s.db.QueryRowContext(ctx, `
SELECT r.record_id, r.namespace, r.key_name, r.revision, r.actor, r.created_at, r.checksum, r.file_path,
       COALESCE(r.record_type, ''), COALESCE(r.status, ''), COALESCE(r.ttl, ''),
       COALESCE(r.content_version, 0), COALESCE(r.pointers_json, '[]'), COALESCE(r.provenance_json, '')
FROM heads h
JOIN records r ON r.record_id = h.head_record_id
WHERE h.namespace = ? AND h.key_name = ?
`, ns, k).Scan(&rec.RecordID, &rec.Namespace, &rec.Key, &rec.Revision, &rec.Actor, &rec.CreatedAt, &rec.Checksum, &relPath,
		&rec.RecordType, &rec.Status, &rec.TTL,
		&rec.ContentVersion, &pointersJSON, &provenanceStr)
	if err != nil {
		return Record{}, err
	}

	if pointersJSON != "" && pointersJSON != "[]" {
		_ = json.Unmarshal([]byte(pointersJSON), &rec.Pointers)
	}
	if provenanceStr != "" {
		rec.Provenance = json.RawMessage(provenanceStr)
	}

	payload, err := os.ReadFile(filepath.Join(s.recordsDir, relPath))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Record{}, fmt.Errorf("%w: payload missing for %s", ErrConsistencyFault, rec.RecordID)
		}
		return Record{}, err
	}
	rec.Payload = payload
	return rec, nil
}

// History lists revisions in ascending revision order.
func (s *Store) History(ctx context.Context, namespace, key string, limit int) ([]Record, error) {
	ns, err := sanitizePathPart(namespace)
	if err != nil {
		return nil, fmt.Errorf("namespace: %w", err)
	}
	k, err := sanitizePathPart(key)
	if err != nil {
		return nil, fmt.Errorf("key: %w", err)
	}

	query := `
SELECT record_id, namespace, key_name, revision, actor, created_at, checksum, file_path,
       COALESCE(record_type, ''), COALESCE(status, ''), COALESCE(ttl, ''),
       COALESCE(content_version, 0), COALESCE(pointers_json, '[]'), COALESCE(provenance_json, '')
FROM records
WHERE namespace = ? AND key_name = ?
ORDER BY revision ASC`
	args := []any{ns, k}
	if limit > 0 {
		query += " LIMIT ?"
		args = append(args, limit)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Record
	for rows.Next() {
		var rec Record
		var relPath, pointersJSON, provenanceStr string
		if err := rows.Scan(&rec.RecordID, &rec.Namespace, &rec.Key, &rec.Revision, &rec.Actor, &rec.CreatedAt, &rec.Checksum, &relPath,
			&rec.RecordType, &rec.Status, &rec.TTL,
			&rec.ContentVersion, &pointersJSON, &provenanceStr); err != nil {
			return nil, err
		}
		if pointersJSON != "" && pointersJSON != "[]" {
			_ = json.Unmarshal([]byte(pointersJSON), &rec.Pointers)
		}
		if provenanceStr != "" {
			rec.Provenance = json.RawMessage(provenanceStr)
		}
		payload, err := os.ReadFile(filepath.Join(s.recordsDir, relPath))
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil, fmt.Errorf("%w: payload missing for %s", ErrConsistencyFault, rec.RecordID)
			}
			return nil, err
		}
		rec.Payload = payload
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return out, nil
}

// Select evaluates a deterministic selector over indexed records.
func (s *Store) Select(ctx context.Context, sel Selector) ([]Record, error) {
	if err := validateSelector(&sel); err != nil {
		return nil, err
	}
	scope := NormalizedScope(sel.RevisionScope)

	query := `
SELECT DISTINCT r.record_id, r.namespace, r.key_name, r.revision, r.actor, r.created_at, r.checksum, r.file_path,
       COALESCE(r.record_type, ''), COALESCE(r.status, ''), COALESCE(r.ttl, ''),
       COALESCE(r.content_version, 0), COALESCE(r.pointers_json, '[]'), COALESCE(r.provenance_json, '')
FROM records r`
	if scope == "head" {
		query += `
JOIN heads h ON h.head_record_id = r.record_id`
	}
	if len(sel.TagsAny) > 0 {
		query += `
JOIN record_tags rt ON rt.record_id = r.record_id AND rt.tag IN (` + placeholders(len(sel.TagsAny)) + `)`
	}
	query += "\nWHERE 1=1"

	var args []any
	if len(sel.TagsAny) > 0 {
		for _, tag := range sel.TagsAny {
			args = append(args, strings.TrimSpace(tag))
		}
	}
	if len(sel.Keys) > 0 {
		query += " AND r.key_name IN (" + placeholders(len(sel.Keys)) + ")"
		for _, key := range sel.Keys {
			args = append(args, strings.TrimSpace(key))
		}
	}
	if len(sel.Types) > 0 {
		query += " AND r.record_type IN (" + placeholders(len(sel.Types)) + ")"
		for _, t := range sel.Types {
			args = append(args, strings.TrimSpace(t))
		}
	}
	if len(sel.Statuses) > 0 {
		query += " AND r.status IN (" + placeholders(len(sel.Statuses)) + ")"
		for _, st := range sel.Statuses {
			args = append(args, strings.TrimSpace(st))
		}
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Record
	for rows.Next() {
		var rec Record
		var relPath, pointersJSON, provenanceStr string
		if err := rows.Scan(&rec.RecordID, &rec.Namespace, &rec.Key, &rec.Revision, &rec.Actor, &rec.CreatedAt, &rec.Checksum, &relPath,
			&rec.RecordType, &rec.Status, &rec.TTL,
			&rec.ContentVersion, &pointersJSON, &provenanceStr); err != nil {
			return nil, err
		}
		if !matchNamespace(sel.Namespaces, rec.Namespace) {
			continue
		}
		if pointersJSON != "" && pointersJSON != "[]" {
			_ = json.Unmarshal([]byte(pointersJSON), &rec.Pointers)
		}
		if provenanceStr != "" {
			rec.Provenance = json.RawMessage(provenanceStr)
		}
		payload, err := os.ReadFile(filepath.Join(s.recordsDir, relPath))
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil, fmt.Errorf("%w: payload missing for %s", ErrConsistencyFault, rec.RecordID)
			}
			return nil, err
		}
		rec.Payload = payload
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	sort.SliceStable(out, func(i, j int) bool {
		a := out[i]
		b := out[j]
		for _, key := range sel.Order {
			switch key {
			case "namespace":
				if a.Namespace != b.Namespace {
					return a.Namespace < b.Namespace
				}
			case "key":
				if a.Key != b.Key {
					return a.Key < b.Key
				}
			case "revision":
				if a.Revision != b.Revision {
					return a.Revision < b.Revision
				}
			case "created_asc":
				if a.CreatedAt != b.CreatedAt {
					return a.CreatedAt < b.CreatedAt
				}
			case "created_desc":
				if a.CreatedAt != b.CreatedAt {
					return a.CreatedAt > b.CreatedAt
				}
			}
		}
		return a.RecordID < b.RecordID
	})

	if sel.Limit > 0 && len(out) > sel.Limit {
		return out[:sel.Limit], nil
	}
	return out, nil
}

// ScanConsistency reports deterministic consistency findings between index rows and payload files.
func (s *Store) ScanConsistency(ctx context.Context) ([]ConsistencyIssue, error) {
	type recRow struct {
		namespace string
		key       string
		revision  int64
		recordID  string
		filePath  string
		checksum  string
	}

	rows, err := s.db.QueryContext(ctx, `
SELECT namespace, key_name, revision, record_id, file_path, COALESCE(checksum, '')
FROM records
ORDER BY namespace, key_name, revision ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	issues := make([]ConsistencyIssue, 0)
	maxHeads := map[string]recRow{}
	recordByID := map[string]recRow{}

	for rows.Next() {
		var rr recRow
		if err := rows.Scan(&rr.namespace, &rr.key, &rr.revision, &rr.recordID, &rr.filePath, &rr.checksum); err != nil {
			return nil, err
		}
		recordByID[rr.recordID] = rr

		compoundKey := rr.namespace + "\x00" + rr.key
		prev, ok := maxHeads[compoundKey]
		if !ok || rr.revision > prev.revision {
			maxHeads[compoundKey] = rr
		}

		abs := filepath.Join(s.recordsDir, rr.filePath)
		payload, err := os.ReadFile(abs)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				issues = append(issues, ConsistencyIssue{
					Type:      "missing_payload",
					Namespace: rr.namespace,
					Key:       rr.key,
					RecordID:  rr.recordID,
					Revision:  rr.revision,
					FilePath:  rr.filePath,
					Detail:    "record index points to missing payload file",
				})
				continue
			}
			return nil, err
		}
		if !json.Valid(payload) {
			issues = append(issues, ConsistencyIssue{
				Type:      "invalid_payload_json",
				Namespace: rr.namespace,
				Key:       rr.key,
				RecordID:  rr.recordID,
				Revision:  rr.revision,
				FilePath:  rr.filePath,
				Detail:    "payload file is not valid JSON",
			})
		}
		if rr.checksum == "" {
			issues = append(issues, ConsistencyIssue{
				Type:      "unverified_checksum",
				Namespace: rr.namespace,
				Key:       rr.key,
				RecordID:  rr.recordID,
				Revision:  rr.revision,
				FilePath:  rr.filePath,
				Detail:    "record has no stored checksum; written before checksum support was added",
			})
		} else {
			h := sha256.Sum256(payload)
			if hex.EncodeToString(h[:]) != rr.checksum {
				issues = append(issues, ConsistencyIssue{
					Type:      "corrupted",
					Namespace: rr.namespace,
					Key:       rr.key,
					RecordID:  rr.recordID,
					Revision:  rr.revision,
					FilePath:  rr.filePath,
					Detail:    "payload sha256 does not match stored checksum",
				})
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	headRows, err := s.db.QueryContext(ctx, `
SELECT namespace, key_name, head_revision, head_record_id
FROM heads
ORDER BY namespace, key_name ASC`)
	if err != nil {
		return nil, err
	}
	defer headRows.Close()

	seenHeads := map[string]bool{}
	for headRows.Next() {
		var ns, key, headRecordID string
		var headRev int64
		if err := headRows.Scan(&ns, &key, &headRev, &headRecordID); err != nil {
			return nil, err
		}
		compoundKey := ns + "\x00" + key
		seenHeads[compoundKey] = true

		maxRec, ok := maxHeads[compoundKey]
		if !ok {
			issues = append(issues, ConsistencyIssue{
				Type:      "orphan_head",
				Namespace: ns,
				Key:       key,
				Revision:  headRev,
				RecordID:  headRecordID,
				Detail:    "head exists but no records exist for namespace/key",
			})
			continue
		}
		if maxRec.recordID != headRecordID || maxRec.revision != headRev {
			issues = append(issues, ConsistencyIssue{
				Type:      "head_mismatch",
				Namespace: ns,
				Key:       key,
				Revision:  headRev,
				RecordID:  headRecordID,
				Detail:    "head does not point to latest indexed revision",
			})
		}
		if _, ok := recordByID[headRecordID]; !ok {
			issues = append(issues, ConsistencyIssue{
				Type:      "head_missing_record",
				Namespace: ns,
				Key:       key,
				Revision:  headRev,
				RecordID:  headRecordID,
				Detail:    "head references a non-existent record_id",
			})
		}
	}
	if err := headRows.Err(); err != nil {
		return nil, err
	}

	for compoundKey, rec := range maxHeads {
		if !seenHeads[compoundKey] {
			issues = append(issues, ConsistencyIssue{
				Type:      "missing_head",
				Namespace: rec.namespace,
				Key:       rec.key,
				Revision:  rec.revision,
				RecordID:  rec.recordID,
				Detail:    "records exist but no head entry found",
			})
		}
	}

	sort.SliceStable(issues, func(i, j int) bool {
		a, b := issues[i], issues[j]
		if a.Type != b.Type {
			return a.Type < b.Type
		}
		if a.Namespace != b.Namespace {
			return a.Namespace < b.Namespace
		}
		if a.Key != b.Key {
			return a.Key < b.Key
		}
		if a.Revision != b.Revision {
			return a.Revision < b.Revision
		}
		return a.RecordID < b.RecordID
	})

	return issues, nil
}

// RebuildHeads rebuilds heads from latest records for each namespace/key pair.
func (s *Store) RebuildHeads(ctx context.Context) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	if _, err = tx.ExecContext(ctx, `DELETE FROM heads`); err != nil {
		return 0, err
	}
	res, err := tx.ExecContext(ctx, `
INSERT INTO heads (namespace, key_name, head_revision, head_record_id, updated_at)
SELECT r.namespace, r.key_name, r.revision, r.record_id, ?
FROM records r
JOIN (
	SELECT namespace, key_name, MAX(revision) AS max_revision
	FROM records
	GROUP BY namespace, key_name
) latest
ON latest.namespace = r.namespace AND latest.key_name = r.key_name AND latest.max_revision = r.revision
`, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return 0, err
	}
	if err = tx.Commit(); err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, nil
	}
	return n, nil
}

// recordAuditEvent stores structured metadata for operational audit queries.
// This is the sole write path into the audit_events table. All external
// callers go through the Emit* helpers in audit_emit.go.
func (s *Store) recordAuditEvent(ctx context.Context, event AuditEvent) error {
	if strings.TrimSpace(event.EventType) == "" {
		return errors.New("event_type required")
	}
	if strings.TrimSpace(event.Actor) == "" {
		return errors.New("actor required")
	}
	if strings.TrimSpace(event.Namespace) == "" {
		return errors.New("namespace required")
	}
	if strings.TrimSpace(event.Key) == "" {
		return errors.New("key required")
	}
	if event.Revision <= 0 {
		return errors.New("revision must be > 0")
	}
	if strings.TrimSpace(event.CreatedAt) == "" {
		event.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	metadata := event.Metadata
	if len(metadata) > 0 && !json.Valid(metadata) {
		return errors.New("metadata must be valid JSON")
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO audit_events (event_type, actor, namespace, key_name, revision, record_id, created_at, metadata_json)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		event.EventType,
		event.Actor,
		event.Namespace,
		event.Key,
		event.Revision,
		event.RecordID,
		event.CreatedAt,
		string(metadata),
	)
	return err
}

// ListAuditEvents returns newest-first audit events with deterministic ordering.
func (s *Store) ListAuditEvents(ctx context.Context, limit int) ([]AuditEvent, error) {
	events, _, err := s.QueryAuditEvents(ctx, AuditQuery{Limit: limit})
	return events, err
}

// QueryAuditEvents returns deterministic newest-first audit events with optional filters and cursor paging.
func (s *Store) QueryAuditEvents(ctx context.Context, q AuditQuery) ([]AuditEvent, *int64, error) {
	if q.Limit <= 0 {
		q.Limit = 50
	}
	if q.Limit > 200 {
		q.Limit = 200
	}
	query := `
SELECT id, event_type, actor, namespace, key_name, revision, record_id, created_at, metadata_json
FROM audit_events
WHERE 1=1`
	args := make([]any, 0, 4)
	if q.Cursor > 0 {
		query += "\n  AND id < ?"
		args = append(args, q.Cursor)
	}
	if strings.TrimSpace(q.Namespace) != "" {
		query += "\n  AND namespace = ?"
		args = append(args, strings.TrimSpace(q.Namespace))
	}
	if strings.TrimSpace(q.EventType) != "" {
		query += "\n  AND event_type = ?"
		args = append(args, strings.TrimSpace(q.EventType))
	}
	if a := strings.TrimSpace(q.Actor); a != "" {
		query += "\n  AND actor LIKE ?"
		args = append(args, "%"+a+"%")
	}
	if s := strings.TrimSpace(q.Since); s != "" {
		query += "\n  AND created_at >= ?"
		args = append(args, s)
	}
	if u := strings.TrimSpace(q.Until); u != "" {
		query += "\n  AND created_at <= ?"
		args = append(args, u)
	}
	query += "\nORDER BY id DESC\nLIMIT ?"
	args = append(args, q.Limit+1)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	out := make([]AuditEvent, 0, q.Limit+1)
	for rows.Next() {
		var event AuditEvent
		var metadata string
		if err := rows.Scan(&event.ID, &event.EventType, &event.Actor, &event.Namespace, &event.Key, &event.Revision, &event.RecordID, &event.CreatedAt, &metadata); err != nil {
			return nil, nil, err
		}
		if strings.TrimSpace(metadata) != "" {
			event.Metadata = json.RawMessage(metadata)
		}
		out = append(out, event)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	var nextCursor *int64
	if len(out) > q.Limit {
		out = out[:q.Limit]
		last := out[len(out)-1].ID
		nextCursor = &last
	}
	return out, nextCursor, nil
}

// CreateAuthToken creates a new scoped token and returns the plaintext token plus stored metadata.
// Scopes and NamespaceGlobs default to full access when nil or empty.
func (s *Store) CreateAuthToken(ctx context.Context, in TokenCreateInput) (string, AuthToken, error) {
	label := strings.TrimSpace(in.Label)
	if label == "" {
		label = "default"
	}
	now := time.Now().UTC()
	expiresAt := ""
	if in.TTL > 0 {
		expiresAt = now.Add(in.TTL).Format(time.RFC3339)
	}

	scopes := in.Scopes
	if len(scopes) == 0 {
		scopes = mustParseStringSlice(defaultTokenScopes)
	}
	namespacGlobs := in.NamespaceGlobs
	if len(namespacGlobs) == 0 {
		namespacGlobs = mustParseStringSlice(defaultTokenNamespaceGlobs)
	}
	scopesJSON, _ := json.Marshal(scopes)
	globsJSON, _ := json.Marshal(namespacGlobs)

	token, err := generateToken()
	if err != nil {
		return "", AuthToken{}, err
	}
	tokenID, err := generateTokenID()
	if err != nil {
		return "", AuthToken{}, err
	}

	meta := AuthToken{
		TokenID:        tokenID,
		Label:          label,
		ClientID:       in.ClientID,
		Scopes:         scopes,
		NamespaceGlobs: namespacGlobs,
		CreatedAt:      now.Format(time.RFC3339),
		ExpiresAt:      expiresAt,
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO auth_tokens (token_id, token_hash, label, client_id, scopes, namespace_globs, created_at, expires_at, revoked_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, NULL)`,
		meta.TokenID,
		hashToken(token),
		meta.Label,
		meta.ClientID,
		string(scopesJSON),
		string(globsJSON),
		meta.CreatedAt,
		meta.ExpiresAt,
	)
	if err != nil {
		return "", AuthToken{}, err
	}
	return token, meta, nil
}

// IssueAuthToken creates a new token with full-access defaults. Preserved for backward compatibility.
func (s *Store) IssueAuthToken(ctx context.Context, label string, ttl time.Duration) (string, AuthToken, error) {
	return s.CreateAuthToken(ctx, TokenCreateInput{Label: label, TTL: ttl})
}

// mustParseStringSlice parses a JSON array string into []string, panicking on invalid JSON.
// Used only for built-in constant arrays that are always valid.
func mustParseStringSlice(s string) []string {
	var out []string
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		panic("invalid default token JSON: " + err.Error())
	}
	return out
}

// RevokeAuthTokenByID marks a token revoked by its token_id.
func (s *Store) RevokeAuthTokenByID(ctx context.Context, tokenID string) error {
	tokenID = strings.TrimSpace(tokenID)
	if tokenID == "" {
		return ErrAuthTokenInvalid
	}
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.db.ExecContext(ctx, `
UPDATE auth_tokens
SET revoked_at = ?
WHERE token_id = ? AND revoked_at IS NULL`, now, tokenID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return nil
	}
	if n == 0 {
		return ErrAuthTokenInvalid
	}
	return nil
}

// RotateAuthToken revokes the old token and issues a replacement token.
func (s *Store) RotateAuthToken(ctx context.Context, oldToken, label string, ttl time.Duration) (string, AuthToken, error) {
	if err := s.ValidateAuthToken(ctx, oldToken); err != nil {
		return "", AuthToken{}, err
	}
	if err := s.RevokeAuthToken(ctx, oldToken); err != nil {
		return "", AuthToken{}, err
	}
	return s.IssueAuthToken(ctx, label, ttl)
}

// RevokeAuthToken marks a token revoked.
func (s *Store) RevokeAuthToken(ctx context.Context, token string) error {
	hash := hashToken(strings.TrimSpace(token))
	if hash == "" {
		return ErrAuthTokenInvalid
	}
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.db.ExecContext(ctx, `
UPDATE auth_tokens
SET revoked_at = ?
WHERE token_hash = ? AND revoked_at IS NULL`, now, hash)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return nil
	}
	if n == 0 {
		return ErrAuthTokenInvalid
	}
	return nil
}

// ValidateAuthToken verifies token presence and lifecycle state.
func (s *Store) ValidateAuthToken(ctx context.Context, token string) error {
	_, err := s.ValidateAuthTokenWithClaims(ctx, token)
	return err
}

// ValidateAuthTokenWithClaims validates a raw token value and returns its full metadata on success.
func (s *Store) ValidateAuthTokenWithClaims(ctx context.Context, token string) (AuthToken, error) {
	hash := hashToken(strings.TrimSpace(token))
	if hash == "" {
		return AuthToken{}, ErrAuthTokenInvalid
	}
	var meta AuthToken
	var scopesJSON, globsJSON string
	err := s.db.QueryRowContext(ctx, `
SELECT token_id, label, COALESCE(client_id,''), COALESCE(scopes,''), COALESCE(namespace_globs,''),
       created_at, COALESCE(expires_at, ''), COALESCE(revoked_at, '')
FROM auth_tokens
WHERE token_hash = ?
LIMIT 1`, hash).Scan(
		&meta.TokenID, &meta.Label, &meta.ClientID, &scopesJSON, &globsJSON,
		&meta.CreatedAt, &meta.ExpiresAt, &meta.RevokedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AuthToken{}, ErrAuthTokenInvalid
		}
		return AuthToken{}, err
	}
	if strings.TrimSpace(meta.RevokedAt) != "" {
		return AuthToken{}, ErrAuthTokenRevoked
	}
	if strings.TrimSpace(meta.ExpiresAt) != "" {
		t, err := time.Parse(time.RFC3339, meta.ExpiresAt)
		if err != nil {
			return AuthToken{}, err
		}
		if time.Now().UTC().After(t) {
			return AuthToken{}, ErrAuthTokenExpired
		}
	}
	meta.Scopes = mustParseStringSlice(scopesJSON)
	meta.NamespaceGlobs = mustParseStringSlice(globsJSON)
	return meta, nil
}

// ListAuthTokens returns token metadata newest-first.
func (s *Store) ListAuthTokens(ctx context.Context, limit int) ([]AuthToken, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT token_id, label, COALESCE(client_id,''), COALESCE(scopes,''), COALESCE(namespace_globs,''),
       created_at, COALESCE(expires_at, ''), COALESCE(revoked_at, '')
FROM auth_tokens
ORDER BY created_at DESC, token_id DESC
LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuthToken
	for rows.Next() {
		var token AuthToken
		var scopesJSON, globsJSON string
		if err := rows.Scan(&token.TokenID, &token.Label, &token.ClientID, &scopesJSON, &globsJSON,
			&token.CreatedAt, &token.ExpiresAt, &token.RevokedAt); err != nil {
			return nil, err
		}
		token.Scopes = mustParseStringSlice(scopesJSON)
		token.NamespaceGlobs = mustParseStringSlice(globsJSON)
		out = append(out, token)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// GetAuthToken looks up a single auth token by token_id and returns its metadata.
func (s *Store) GetAuthToken(ctx context.Context, tokenID string) (AuthToken, error) {
	var token AuthToken
	var scopesJSON, globsJSON string
	err := s.db.QueryRowContext(ctx, `
SELECT token_id, label, COALESCE(client_id,''), COALESCE(scopes,''), COALESCE(namespace_globs,''),
       created_at, COALESCE(expires_at, ''), COALESCE(revoked_at, '')
FROM auth_tokens
WHERE token_id = ?`, tokenID).Scan(
		&token.TokenID, &token.Label, &token.ClientID, &scopesJSON, &globsJSON,
		&token.CreatedAt, &token.ExpiresAt, &token.RevokedAt)
	if err != nil {
		return AuthToken{}, err
	}
	token.Scopes = mustParseStringSlice(scopesJSON)
	token.NamespaceGlobs = mustParseStringSlice(globsJSON)
	return token, nil
}

// HasActiveAuthTokens reports whether at least one non-revoked, non-expired token exists.
func (s *Store) HasActiveAuthTokens(ctx context.Context) (bool, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	var n int
	if err := s.db.QueryRowContext(ctx, `
SELECT COUNT(1)
FROM auth_tokens
WHERE revoked_at IS NULL
  AND (expires_at IS NULL OR expires_at = '' OR expires_at > ?)`, now).Scan(&n); err != nil {
		return false, err
	}
	return n > 0, nil
}

// Readiness returns deterministic operational readiness status.
func (s *Store) Readiness(ctx context.Context) (ReadinessReport, error) {
	report := ReadinessReport{
		DBPath:      s.dbPath,
		RecordsDir:  s.recordsDir,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
	}
	if _, err := os.Stat(s.recordsDir); err == nil {
		report.RecordsDirExists = true
	} else if errors.Is(err, os.ErrNotExist) {
		report.RecordsDirExists = false
	} else {
		return ReadinessReport{}, err
	}

	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_version`).Scan(&report.SchemaVersion); err != nil {
		return ReadinessReport{}, err
	}

	issues, err := s.ScanConsistency(ctx)
	if err != nil {
		return ReadinessReport{}, err
	}
	report.ConsistencyIssues = len(issues)
	switch {
	case !report.RecordsDirExists:
		report.Status = "failing"
	case report.ConsistencyIssues > 0:
		report.Status = "degraded"
	default:
		report.Status = "healthy"
	}
	report.Healthy = report.Status == "healthy"
	return report, nil
}

// CompactRevisions keeps newest N revisions per namespace/key and removes older revisions.
func (s *Store) CompactRevisions(ctx context.Context, keepPerKey int) (int64, error) {
	if keepPerKey <= 0 {
		return 0, errors.New("keepPerKey must be > 0")
	}

	rows, err := s.db.QueryContext(ctx, `
SELECT record_id, file_path
FROM (
	SELECT record_id, file_path,
		ROW_NUMBER() OVER (PARTITION BY namespace, key_name ORDER BY revision DESC) AS rn
	FROM records
)
WHERE rn > ?
ORDER BY file_path ASC`, keepPerKey)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	type doomed struct {
		recordID string
		filePath string
	}
	var doomedRows []doomed
	for rows.Next() {
		var d doomed
		if err := rows.Scan(&d.recordID, &d.filePath); err != nil {
			return 0, err
		}
		doomedRows = append(doomedRows, d)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if len(doomedRows) == 0 {
		return 0, nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	var deleted int64
	for _, d := range doomedRows {
		if _, err = tx.ExecContext(ctx, `DELETE FROM records WHERE record_id = ?`, d.recordID); err != nil {
			return 0, err
		}
		_ = os.Remove(filepath.Join(s.recordsDir, d.filePath))
		deleted++
	}
	if err = tx.Commit(); err != nil {
		return 0, err
	}
	if _, err := s.RebuildHeads(ctx); err != nil {
		return deleted, err
	}
	return deleted, nil
}

// TrimAuditEvents keeps newest N audit events and drops older rows.
func (s *Store) TrimAuditEvents(ctx context.Context, keep int) (int64, error) {
	if keep < 0 {
		return 0, errors.New("keep must be >= 0")
	}
	res, err := s.db.ExecContext(ctx, `
DELETE FROM audit_events
WHERE id IN (
	SELECT id FROM audit_events
	ORDER BY id DESC
	LIMIT -1 OFFSET ?
)`, keep)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, nil
	}
	return n, nil
}

// TrimRecords deletes records in namespaces matching namespacePattern whose
// created_at is older than cutoffISO (an RFC3339 timestamp string).
// namespacePattern uses SQL LIKE syntax (% for wildcard).
// Returns the number of records trimmed and a list of deleted file paths.
func (s *Store) TrimRecords(ctx context.Context, namespacePattern, cutoffISO string, dryRun bool) (int64, error) {
	if strings.TrimSpace(namespacePattern) == "" {
		return 0, errors.New("namespace_pattern required")
	}
	if strings.TrimSpace(cutoffISO) == "" {
		return 0, errors.New("cutoff required")
	}

	rows, err := s.db.QueryContext(ctx, `
SELECT record_id, file_path FROM records
WHERE namespace LIKE ? AND created_at < ?
ORDER BY created_at ASC`, namespacePattern, cutoffISO)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	type toDelete struct {
		id   string
		path string
	}
	var targets []toDelete
	for rows.Next() {
		var td toDelete
		if err := rows.Scan(&td.id, &td.path); err != nil {
			return 0, err
		}
		targets = append(targets, td)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if dryRun || len(targets) == 0 {
		return int64(len(targets)), nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	var deleted int64
	for _, td := range targets {
		if _, err = tx.ExecContext(ctx, `DELETE FROM heads WHERE head_record_id = ?`, td.id); err != nil {
			return 0, err
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM records WHERE record_id = ?`, td.id); err != nil {
			return 0, err
		}
		_ = os.Remove(filepath.Join(s.recordsDir, td.path))
		deleted++
	}
	if err = tx.Commit(); err != nil {
		return 0, err
	}
	if _, err = s.RebuildHeads(ctx); err != nil {
		return deleted, err
	}
	return deleted, nil
}

// CompactNamespace keeps only the most recent maxRevisions revisions per
// (namespace, key) for namespaces matching namespacePattern (SQL LIKE syntax).
// maxRevisions must be >= 1.
func (s *Store) CompactNamespace(ctx context.Context, namespacePattern string, maxRevisions int, dryRun bool) (int64, error) {
	if strings.TrimSpace(namespacePattern) == "" {
		return 0, errors.New("namespace_pattern required")
	}
	if maxRevisions < 1 {
		return 0, errors.New("max_revisions must be >= 1")
	}

	rows, err := s.db.QueryContext(ctx, `
SELECT record_id, file_path FROM (
	SELECT record_id, file_path,
		ROW_NUMBER() OVER (PARTITION BY namespace, key_name ORDER BY revision DESC) AS rn
	FROM records
	WHERE namespace LIKE ?
)
WHERE rn > ?`, namespacePattern, maxRevisions)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	type toDelete struct {
		id   string
		path string
	}
	var targets []toDelete
	for rows.Next() {
		var td toDelete
		if err := rows.Scan(&td.id, &td.path); err != nil {
			return 0, err
		}
		targets = append(targets, td)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if dryRun || len(targets) == 0 {
		return int64(len(targets)), nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	var deleted int64
	for _, td := range targets {
		if _, err = tx.ExecContext(ctx, `DELETE FROM records WHERE record_id = ?`, td.id); err != nil {
			return 0, err
		}
		_ = os.Remove(filepath.Join(s.recordsDir, td.path))
		deleted++
	}
	if err = tx.Commit(); err != nil {
		return 0, err
	}
	if _, err = s.RebuildHeads(ctx); err != nil {
		return deleted, err
	}
	return deleted, nil
}

// UpsertNamespacePolicy persists namespace owner/policy metadata.
func (s *Store) UpsertNamespacePolicy(ctx context.Context, entry NamespacePolicyEntry) error {
	if strings.TrimSpace(entry.Namespace) == "" {
		return errors.New("namespace required")
	}
	if strings.TrimSpace(entry.OwnerType) == "" {
		return errors.New("owner_type required")
	}
	if strings.TrimSpace(entry.OwnerID) == "" {
		return errors.New("owner_id required")
	}
	policyJSON := ""
	if entry.Policy != nil {
		b, err := json.Marshal(entry.Policy)
		if err != nil {
			return err
		}
		policyJSON = string(b)
	}
	if strings.TrimSpace(entry.UpdatedAt) == "" {
		entry.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO namespace_policies (namespace, owner_type, owner_id, policy_json, updated_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(namespace) DO UPDATE SET
	owner_type=excluded.owner_type,
	owner_id=excluded.owner_id,
	policy_json=excluded.policy_json,
	updated_at=excluded.updated_at`,
		entry.Namespace, entry.OwnerType, entry.OwnerID, policyJSON, entry.UpdatedAt)
	return err
}

// GetNamespacePolicy returns persisted namespace metadata.
func (s *Store) GetNamespacePolicy(ctx context.Context, namespace string) (NamespacePolicyEntry, error) {
	namespace = strings.TrimSpace(namespace)
	if namespace == "" {
		return NamespacePolicyEntry{}, errors.New("namespace required")
	}
	var entry NamespacePolicyEntry
	var policyJSON string
	err := s.db.QueryRowContext(ctx, `
SELECT namespace, owner_type, owner_id, COALESCE(policy_json, ''), updated_at
FROM namespace_policies
WHERE namespace = ?
LIMIT 1`, namespace).Scan(&entry.Namespace, &entry.OwnerType, &entry.OwnerID, &policyJSON, &entry.UpdatedAt)
	if err != nil {
		return NamespacePolicyEntry{}, err
	}
	if strings.TrimSpace(policyJSON) != "" {
		var policy map[string]any
		if err := json.Unmarshal([]byte(policyJSON), &policy); err != nil {
			return NamespacePolicyEntry{}, err
		}
		entry.Policy = policy
	}
	return entry, nil
}

// EnsureNamespaceRegistered makes sure namespace_policies has a row for the
// given namespace, deriving owner metadata from the namespace shape. It is
// idempotent — if the row already exists, it returns immediately without
// overwriting (preserving any explicit registration). Otherwise it inserts a
// row with policy_json={"source":"inferred"} and emits a namespace.register
// audit event with actor="system".
//
// Owner derivation: <segment_0>/<segment_1>/...  →  (segment_0, segment_1)
// when segment_0 is "user" or "app". Other shapes (single-segment namespaces,
// non-tier prefixes, missing owner_id) register with the sentinel owner
// (system, <namespace_itself>) — registry stays authoritative without breaking
// permissive write paths. Validation tightening is a separate concern
// (CW-20260428-0005).
func (s *Store) EnsureNamespaceRegistered(ctx context.Context, namespace string) error {
	_, err := s.ensureNamespaceRegistered(ctx, namespace, "inferred")
	return err
}

// ensureNamespaceRegistered inserts a row in namespace_policies if and only if
// one isn't already there. Returns (inserted, err): inserted is true exactly
// when this call wrote a new row (and therefore emitted the audit event),
// false when an existing row was preserved or a concurrent writer beat us
// to the INSERT. INSERT OR IGNORE + RowsAffected() is the sole idempotency
// gate — no preliminary SELECT.
func (s *Store) ensureNamespaceRegistered(ctx context.Context, namespace, source string) (bool, error) {
	ns := strings.TrimSpace(namespace)
	if ns == "" {
		return false, errors.New("namespace required")
	}

	ownerType, ownerID := DeriveNamespaceOwner(ns)
	policyJSON, err := json.Marshal(map[string]any{"source": source})
	if err != nil {
		return false, err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.db.ExecContext(ctx, `
INSERT OR IGNORE INTO namespace_policies (namespace, owner_type, owner_id, policy_json, updated_at)
VALUES (?, ?, ?, ?, ?)`,
		ns, ownerType, ownerID, string(policyJSON), now)
	if err != nil {
		return false, err
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return false, nil
	}

	meta, err := json.Marshal(map[string]any{
		"source":     source,
		"owner_type": ownerType,
		"owner_id":   ownerID,
	})
	if err == nil {
		_ = s.EmitNamespaceRegister(ctx, "system", ns, meta)
	}
	return true, nil
}

// DeriveNamespaceOwner returns the (owner_type, owner_id) pair derived from a
// namespace string. Well-formed user/* and app/* namespaces yield their tier
// segment plus owner_id; everything else yields the sentinel (system, ns).
// Exported for tests.
func DeriveNamespaceOwner(namespace string) (string, string) {
	ns := strings.TrimSpace(namespace)
	parts := strings.Split(ns, "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "system", ns
	}
	tier := parts[0]
	if tier != "user" && tier != "app" {
		return "system", ns
	}
	return tier, parts[1]
}

// ReconcileNamespaceRegistry scans every distinct namespace appearing in the
// memory_revisions and records (heads) tables and ensures a namespace_policies
// row exists for each. Idempotent and safe to call on every startup; only
// produces work the first time the registry diverges. Returns the number of
// rows inserted.
func (s *Store) ReconcileNamespaceRegistry(ctx context.Context) (int, error) {
	seen := map[string]struct{}{}
	collect := func(query string) error {
		rows, err := s.db.QueryContext(ctx, query)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var ns string
			if err := rows.Scan(&ns); err != nil {
				return err
			}
			ns = strings.TrimSpace(ns)
			if ns == "" {
				continue
			}
			seen[ns] = struct{}{}
		}
		return rows.Err()
	}
	// memory_revisions covers memory + knowledge domains; records covers context.
	if err := collect(`SELECT DISTINCT namespace FROM memory_revisions`); err != nil {
		return 0, err
	}
	if err := collect(`SELECT DISTINCT namespace FROM records`); err != nil {
		return 0, err
	}

	registered := 0
	for ns := range seen {
		inserted, err := s.ensureNamespaceRegistered(ctx, ns, "inferred-backfill")
		if err != nil {
			return registered, err
		}
		if inserted {
			registered++
		}
	}
	return registered, nil
}

// ListNamespacePolicies returns all persisted policies ordered by namespace.
func (s *Store) ListNamespacePolicies(ctx context.Context) ([]NamespacePolicyEntry, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT namespace, owner_type, owner_id, COALESCE(policy_json, ''), updated_at
FROM namespace_policies
ORDER BY namespace ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []NamespacePolicyEntry
	for rows.Next() {
		var entry NamespacePolicyEntry
		var policyJSON string
		if err := rows.Scan(&entry.Namespace, &entry.OwnerType, &entry.OwnerID, &policyJSON, &entry.UpdatedAt); err != nil {
			return nil, err
		}
		if strings.TrimSpace(policyJSON) != "" {
			var policy map[string]any
			if err := json.Unmarshal([]byte(policyJSON), &policy); err != nil {
				return nil, err
			}
			entry.Policy = policy
		}
		out = append(out, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func generateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func generateTokenID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "tok_" + hex.EncodeToString(b), nil
}

func hashToken(token string) string {
	token = strings.TrimSpace(token)
	if token == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func sanitizePathPart(in string) (string, error) {
	trimmed := strings.TrimSpace(in)
	if trimmed == "" {
		return "", errors.New("value required")
	}
	clean := filepath.Clean(trimmed)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("invalid path segment: %q", in)
	}
	for _, part := range strings.Split(clean, string(filepath.Separator)) {
		if part == ".." {
			return "", fmt.Errorf("invalid path segment: %q", in)
		}
	}
	if filepath.IsAbs(clean) {
		return "", fmt.Errorf("absolute path not allowed: %q", in)
	}
	return clean, nil
}

func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	items := make([]string, n)
	for i := range items {
		items[i] = "?"
	}
	return strings.Join(items, ",")
}

func validateSelector(sel *Selector) error {
	if sel == nil {
		return errors.New("selector required")
	}
	if len(sel.Namespaces) > maxSelectorNamespaces {
		return fmt.Errorf("too many namespace patterns: max %d", maxSelectorNamespaces)
	}
	for i, pattern := range sel.Namespaces {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			return fmt.Errorf("namespaces[%d] cannot be empty", i)
		}
		if _, err := filepath.Match(pattern, "x"); err != nil {
			return fmt.Errorf("invalid namespaces[%d] pattern: %w", i, err)
		}
		sel.Namespaces[i] = pattern
	}

	if len(sel.Keys) > maxSelectorKeys {
		return fmt.Errorf("too many keys: max %d", maxSelectorKeys)
	}
	for i, key := range sel.Keys {
		key = strings.TrimSpace(key)
		if key == "" {
			return fmt.Errorf("keys[%d] cannot be empty", i)
		}
		sel.Keys[i] = key
	}

	if sel.Limit < 0 {
		return errors.New("limit must be >= 0")
	}
	if sel.Limit == 0 {
		sel.Limit = DefaultSelectLimit
	}
	if sel.Limit > MaxSelectLimit {
		return fmt.Errorf("limit exceeds max %d", MaxSelectLimit)
	}

	if len(sel.Order) == 0 {
		sel.Order = []string{"namespace", "key", "revision"}
	}
	seen := map[string]bool{}
	for _, key := range sel.Order {
		switch key {
		case "namespace", "key", "revision", "created_asc", "created_desc":
			if seen[key] {
				return fmt.Errorf("duplicate order key %q", key)
			}
			seen[key] = true
		default:
			return fmt.Errorf("unsupported order key %q", key)
		}
	}
	return nil
}

func matchNamespace(patterns []string, namespace string) bool {
	if len(patterns) == 0 {
		return true
	}
	for _, p := range patterns {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		// A bare "*" means match all namespaces, including hierarchical ones
		// with slashes (e.g. "app/mentat"). filepath.Match treats "/" as a
		// path separator that "*" cannot cross, so we handle this explicitly.
		if p == "*" {
			return true
		}
		ok, err := filepath.Match(p, namespace)
		if err == nil && ok {
			return true
		}
	}
	return false
}

// EstimateTokens returns a deterministic byte-to-token estimate using a simple
// 4-bytes-per-token heuristic. Never calls a model.
func EstimateTokens(payload []byte) int {
	return (len(payload) + 3) / 4
}

// extractTags parses a JSON metadata blob and returns the "tags" string array.
// Returns nil if metadata is absent, invalid, or has no tags field.
func extractTags(metadata json.RawMessage) []string {
	if len(metadata) == 0 {
		return nil
	}
	var m struct {
		Tags []string `json:"tags"`
	}
	if err := json.Unmarshal(metadata, &m); err != nil {
		return nil
	}
	var out []string
	for _, t := range m.Tags {
		if t = strings.TrimSpace(t); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// PromoteRequest is the structured payload for a promote.request record.
type PromoteRequest struct {
	Type             string `json:"type"`
	RequestID        string `json:"request_id"`
	SourceNamespace  string `json:"source_namespace"`
	SourceKey        string `json:"source_key"`
	SourceRevisionID string `json:"source_revision_id"`
	SourceChecksum   string `json:"source_checksum,omitempty"`
	TargetNamespace  string `json:"target_namespace"`
	TargetKey        string `json:"target_key"`
	Reason           string `json:"reason,omitempty"`
	ProposedSummary  string `json:"proposed_summary,omitempty"`
	Status           string `json:"status"` // pending|approved|applied
	RequestedAt      string `json:"requested_at"`
	RequestedBy      string `json:"requested_by"`
	ApprovalID       string `json:"approval_id,omitempty"`
	ApprovedBy       string `json:"approved_by,omitempty"`
}

// PromoteApproval is the structured payload for a promote.approve record.
type PromoteApproval struct {
	Type             string `json:"type"`
	ApprovalID       string `json:"approval_id"`
	RequestID        string `json:"request_id"`
	RequestNamespace string `json:"request_namespace"`
	ApprovedAt       string `json:"approved_at"`
	ApprovedBy       string `json:"approved_by"`
	Notes            string `json:"notes,omitempty"`
}

// GetByRecordID fetches a single record by its record_id.
func (s *Store) GetByRecordID(ctx context.Context, recordID string) (Record, error) {
	var rec Record
	var relPath, pointersJSON, provenanceStr string
	err := s.db.QueryRowContext(ctx, `
SELECT record_id, namespace, key_name, revision, actor, created_at, checksum, file_path,
       COALESCE(record_type, ''), COALESCE(status, ''), COALESCE(ttl, ''),
       COALESCE(content_version, 0), COALESCE(pointers_json, '[]'), COALESCE(provenance_json, '')
FROM records WHERE record_id = ?`, recordID).Scan(
		&rec.RecordID, &rec.Namespace, &rec.Key, &rec.Revision, &rec.Actor, &rec.CreatedAt, &rec.Checksum, &relPath,
		&rec.RecordType, &rec.Status, &rec.TTL,
		&rec.ContentVersion, &pointersJSON, &provenanceStr)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Record{}, fmt.Errorf("record %s not found", recordID)
		}
		return Record{}, err
	}
	if pointersJSON != "" && pointersJSON != "[]" {
		_ = json.Unmarshal([]byte(pointersJSON), &rec.Pointers)
	}
	if provenanceStr != "" {
		rec.Provenance = json.RawMessage(provenanceStr)
	}
	payload, err := os.ReadFile(filepath.Join(s.recordsDir, relPath))
	if err != nil {
		return Record{}, err
	}
	rec.Payload = payload
	return rec, nil
}

// GetPromoteRequest looks up the head promote.request record by request_id
// across all app/*/promotions namespaces.
func (s *Store) GetPromoteRequest(ctx context.Context, requestID string) (PromoteRequest, string, error) {
	// request_id is stored as the key in app/*/promotions namespace.
	recs, err := s.Select(ctx, Selector{
		Namespaces:    []string{"app/*/promotions"},
		Keys:          []string{requestID},
		RevisionScope: "head",
	})
	if err != nil {
		return PromoteRequest{}, "", err
	}
	for _, rec := range recs {
		var pr PromoteRequest
		if err := json.Unmarshal(rec.Payload, &pr); err != nil {
			continue
		}
		if pr.Type == "promote.request" {
			return pr, rec.Namespace, nil
		}
	}
	return PromoteRequest{}, "", fmt.Errorf("promote request %s not found", requestID)
}

// GetPromoteApproval looks up the head promote.approve record by request_id.
func (s *Store) GetPromoteApproval(ctx context.Context, requestID string) (PromoteApproval, error) {
	recs, err := s.Select(ctx, Selector{
		Namespaces:    []string{"user/promotions"},
		RevisionScope: "head",
	})
	if err != nil {
		return PromoteApproval{}, err
	}
	for _, rec := range recs {
		var pa PromoteApproval
		if err := json.Unmarshal(rec.Payload, &pa); err != nil {
			continue
		}
		if pa.Type == "promote.approve" && pa.RequestID == requestID {
			return pa, nil
		}
	}
	return PromoteApproval{}, fmt.Errorf("approval for request %s not found", requestID)
}

// CleanupExpiredTTL removes records whose TTL has expired.
// Returns the count of cleaned-up records.
func (s *Store) CleanupExpiredTTL(ctx context.Context) (int64, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	rows, err := s.db.QueryContext(ctx, `
SELECT record_id, file_path FROM records
WHERE ttl != '' AND ttl < ?
ORDER BY created_at ASC`, now)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	type toDelete struct {
		id   string
		path string
	}
	var targets []toDelete
	for rows.Next() {
		var td toDelete
		if err := rows.Scan(&td.id, &td.path); err != nil {
			return 0, err
		}
		targets = append(targets, td)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if len(targets) == 0 {
		return 0, nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	var deleted int64
	for _, td := range targets {
		if _, err = tx.ExecContext(ctx, `DELETE FROM heads WHERE head_record_id = ?`, td.id); err != nil {
			return 0, err
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM record_tags WHERE record_id = ?`, td.id); err != nil {
			return 0, err
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM records WHERE record_id = ?`, td.id); err != nil {
			return 0, err
		}
		_ = os.Remove(filepath.Join(s.recordsDir, td.path))
		deleted++
	}
	if err = tx.Commit(); err != nil {
		return 0, err
	}
	if _, err = s.RebuildHeads(ctx); err != nil {
		return deleted, err
	}
	return deleted, nil
}

// UpdateRecordStatus updates the status of the head record for a namespace/key.
// This appends a new revision with the updated status.
func (s *Store) UpdateRecordStatus(ctx context.Context, namespace, key, actor, newStatus string) (Record, error) {
	head, err := s.Head(ctx, namespace, key)
	if err != nil {
		return Record{}, err
	}
	return s.AppendRecord(ctx, AppendInput{
		Namespace:      namespace,
		Key:            key,
		Actor:          actor,
		Payload:        head.Payload,
		RecordType:     head.RecordType,
		Status:         newStatus,
		TTL:            head.TTL,
		ContentVersion: head.ContentVersion + 1,
		Pointers:       head.Pointers,
		Provenance:     head.Provenance,
	})
}

// EmbeddingRow represents a stored embedding vector.
type EmbeddingRow struct {
	RecordID   string
	Model      string
	Dimensions int
	Vector     []float32
	CreatedAt  string
}

// UpsertEmbedding stores or replaces an embedding for a (record_id, model) pair.
func (s *Store) UpsertEmbedding(ctx context.Context, row EmbeddingRow) error {
	blob := float32ToBlob(row.Vector)
	_, err := s.db.ExecContext(ctx, `
INSERT INTO embeddings (record_id, model, dimensions, vector, created_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(record_id, model) DO UPDATE SET
	dimensions = excluded.dimensions,
	vector = excluded.vector,
	created_at = excluded.created_at`,
		row.RecordID, row.Model, row.Dimensions, blob, time.Now().UTC().Format(time.RFC3339))
	return err
}

// EmbeddingFilter optionally narrows which embeddings are loaded.
type EmbeddingFilter struct {
	Namespaces []string // namespace prefix filters
	Types      []string // record_type filters
	Tags       []string // tag filters (any match)
	Model      string   // required: which model's embeddings to search
}

// ListEmbeddings returns all embeddings matching the filter, joining records for metadata.
func (s *Store) ListEmbeddings(ctx context.Context, f EmbeddingFilter) ([]EmbeddingRow, []Record, error) {
	var args []any
	query := `
SELECT e.record_id, e.model, e.dimensions, e.vector, e.created_at,
       r.namespace, r.key_name, r.revision, r.actor, r.created_at,
       r.checksum, r.file_path, r.record_type, r.status
FROM embeddings e
JOIN records r ON e.record_id = r.record_id
JOIN heads h ON r.namespace = h.namespace AND r.key_name = h.key_name AND r.record_id = h.head_record_id`

	var conditions []string

	if f.Model != "" {
		conditions = append(conditions, "e.model = ?")
		args = append(args, f.Model)
	}

	for _, ns := range f.Namespaces {
		conditions = append(conditions, "r.namespace LIKE ?")
		args = append(args, ns+"%")
	}
	for _, t := range f.Types {
		conditions = append(conditions, "r.record_type = ?")
		args = append(args, t)
	}
	if len(f.Tags) > 0 {
		placeholders := make([]string, len(f.Tags))
		for i, tag := range f.Tags {
			placeholders[i] = "?"
			args = append(args, tag)
		}
		conditions = append(conditions, "e.record_id IN (SELECT record_id FROM record_tags WHERE tag IN ("+strings.Join(placeholders, ",")+"))")
	}

	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	var embeddings []EmbeddingRow
	var records []Record
	for rows.Next() {
		var e EmbeddingRow
		var r Record
		var blob []byte
		var filePath string
		if err := rows.Scan(
			&e.RecordID, &e.Model, &e.Dimensions, &blob, &e.CreatedAt,
			&r.Namespace, &r.Key, &r.Revision, &r.Actor, &r.CreatedAt,
			&r.Checksum, &filePath, &r.RecordType, &r.Status,
		); err != nil {
			return nil, nil, err
		}
		e.Vector = blobToFloat32(blob)
		r.RecordID = e.RecordID
		embeddings = append(embeddings, e)
		records = append(records, r)
	}
	return embeddings, records, rows.Err()
}

// DeleteEmbedding removes an embedding for a (record_id, model) pair.
func (s *Store) DeleteEmbedding(ctx context.Context, recordID, model string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM embeddings WHERE record_id = ? AND model = ?`, recordID, model)
	return err
}

// UpsertEmbeddingRaw implements the embedding.StoreBackend interface.
func (s *Store) UpsertEmbeddingRaw(ctx context.Context, recordID, model string, dimensions int, vector []float32) error {
	return s.UpsertEmbedding(ctx, EmbeddingRow{
		RecordID:   recordID,
		Model:      model,
		Dimensions: dimensions,
		Vector:     vector,
	})
}

// UpsertEmbeddingVec implements embedding.EmbeddingStore.
func (s *Store) UpsertEmbeddingVec(ctx context.Context, recordID, model string, dimensions int, vector []float32) error {
	return s.UpsertEmbedding(ctx, EmbeddingRow{
		RecordID:   recordID,
		Model:      model,
		Dimensions: dimensions,
		Vector:     vector,
	})
}

// EmbeddingCandidate is a flat embedding row used by the vector index.
type EmbeddingCandidate struct {
	RecordID  string
	Namespace string
	Key       string
	Vector    []float32
}

// SearchEmbeddings implements embedding.EmbeddingStore.
func (s *Store) SearchEmbeddings(ctx context.Context, model string, namespaces, types []string) ([]EmbeddingCandidate, error) {
	rows, records, err := s.ListEmbeddings(ctx, EmbeddingFilter{
		Model:      model,
		Namespaces: namespaces,
		Types:      types,
	})
	if err != nil {
		return nil, err
	}
	out := make([]EmbeddingCandidate, len(rows))
	for i, e := range rows {
		out[i] = EmbeddingCandidate{
			RecordID:  e.RecordID,
			Namespace: records[i].Namespace,
			Key:       records[i].Key,
			Vector:    e.Vector,
		}
	}
	return out, nil
}

// DeleteEmbeddingVec implements embedding.EmbeddingStore.
func (s *Store) DeleteEmbeddingVec(ctx context.Context, recordID, model string) error {
	return s.DeleteEmbedding(ctx, recordID, model)
}

// DeleteEmbeddingRaw implements the embedding.StoreBackend interface.
func (s *Store) DeleteEmbeddingRaw(ctx context.Context, recordID, model string) error {
	return s.DeleteEmbedding(ctx, recordID, model)
}

func float32ToBlob(v []float32) []byte {
	buf := make([]byte, len(v)*4)
	for i, f := range v {
		bits := math.Float32bits(f)
		buf[i*4] = byte(bits)
		buf[i*4+1] = byte(bits >> 8)
		buf[i*4+2] = byte(bits >> 16)
		buf[i*4+3] = byte(bits >> 24)
	}
	return buf
}

func blobToFloat32(b []byte) []float32 {
	n := len(b) / 4
	v := make([]float32, n)
	for i := 0; i < n; i++ {
		bits := uint32(b[i*4]) | uint32(b[i*4+1])<<8 | uint32(b[i*4+2])<<16 | uint32(b[i*4+3])<<24
		v[i] = math.Float32frombits(bits)
	}
	return v
}
