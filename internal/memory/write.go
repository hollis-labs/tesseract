package memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/hollis-labs/tesseract/domains"
)

// Sentinels for the memory write path.
var (
	ErrInvalidInput = errors.New("invalid memory input")
	ErrNotFound     = errors.New("memory not found")
)

// WriteInput carries all fields for a new revision write.
type WriteInput struct {
	// Domain selects the revision's policy bucket. REQUIRED — there is no
	// default, and an empty value is a validation error carrying
	// errDomainRequired. See that message for why the default was removed.
	Domain    domains.Domain
	Namespace string
	MemoryKey string
	// WorkstreamID is presence-capable: nil preserves the current association,
	// a nonempty value sets it, and a pointer to empty clears it.
	WorkstreamID   *string
	Supersedes     string
	Status         Status
	Author         Author
	Trigger        Trigger
	SessionID      string
	DerivedFrom    DerivedFrom
	Confidence     float64
	Tags           []string
	TTL            time.Duration
	Summary        string
	Body           string
	Data           json.RawMessage
	DataSchemaHash string
	Facets         Facets

	// ConsumerState is the consumer's operational JSON bag for this revision
	// (CW-20260909-0036). Empty writes SQL NULL, which is what every revision
	// predating migration 19 carries and what any writer that has nothing to
	// say about lifecycle should keep carrying.
	//
	// Validated for well-formed JSON, object-ness, and the declaring type's
	// required_fields. Never read for its values — see consumerstate.go.
	ConsumerState  json.RawMessage
	Dedup          string  // "none" (default), "semantic"
	DedupThreshold float64 // optional per-call override; 0 = use store default
}

// WriteRevision creates a new revision in the memory store, handling keyed,
// keyless, and supersedes cases within a single transaction.
func (s *Store) WriteRevision(ctx context.Context, in WriteInput) (Revision, error) {
	// The input is flat; stored revisions and every read projection retain Payload.
	payload := Payload{Summary: in.Summary, Body: in.Body, Data: in.Data, DataSchemaHash: in.DataSchemaHash}
	if err := ValidateWriteInput(in); err != nil {
		return Revision{}, err
	}

	// Make sure the namespace is in the policy registry before we write data
	// for it. Idempotent — only inserts the first time the namespace is seen.
	// See CW-20260428-0005 for the bug this closes (registry-vs-data drift).
	if err := s.EnsureNamespaceRegistered(ctx, in.Namespace); err != nil {
		return Revision{}, err
	}

	// Semantic dedup: if requested, search for similar existing revisions.
	// NOTE: This runs outside the transaction (before BeginTx). There is a
	// TOCTOU window where a concurrent write could create a matching revision
	// between the dedup check and the INSERT. This is acceptable for single-
	// writer SQLite but should be revisited for multi-writer backends.
	var dedupMatch string
	if in.Dedup == "semantic" {
		if s.embedder == nil {
			return Revision{}, ErrEmbedderUnavailable
		}
		threshold := in.DedupThreshold
		if threshold == 0 {
			threshold = s.dedupThreshold
		}
		if threshold == 0 {
			threshold = 0.85
		}
		text := revisionEmbedText(Revision{Payload: payload})
		matchID, sameKey, matchErr := s.findSemanticMatch(ctx, in.Domain, in.Namespace, in.MemoryKey, text, threshold)
		if matchErr != nil {
			return Revision{}, fmt.Errorf("semantic dedup: %w", matchErr)
		}
		if matchID != "" {
			dedupMatch = matchID
			if sameKey && in.Supersedes == "" {
				in.Supersedes = matchID
			}
		}
		// Dedup has been resolved before the transaction. The transaction-aware
		// writer deliberately accepts only concrete revision writes.
		in.Dedup = "none"
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Revision{}, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	rev, err := s.WriteRevisionInTx(ctx, tx, in)
	if err != nil {
		return Revision{}, err
	}

	if err := tx.Commit(); err != nil {
		return Revision{}, fmt.Errorf("commit: %w", err)
	}

	rev.DedupMatch = dedupMatch
	s.FinishRevisionWrite(ctx, rev, in)
	return rev, nil
}

func optionalWorkstream(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func resolveRevisionWorkstream(ctx context.Context, tx *sql.Tx, memoryID string, explicit *string) (string, error) {
	if explicit != nil {
		return *explicit, nil
	}
	var currentRevision sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT current_revision FROM memory_state WHERE memory_id = ?`, memoryID).Scan(&currentRevision); err != nil {
		return "", fmt.Errorf("read current workstream association: %w", err)
	}
	if currentRevision.Valid && currentRevision.String != "" {
		var current sql.NullString
		if err := tx.QueryRowContext(ctx, `SELECT workstream_id FROM memory_revisions WHERE revision_id = ?`, currentRevision.String).Scan(&current); err != nil {
			return "", fmt.Errorf("read current workstream association: %w", err)
		}
		return current.String, nil
	}
	if received := WriteContextFromContext(ctx); received != nil {
		return received.WorkstreamID, nil
	}
	return "", nil
}

// resolveOrCreateMemory finds an existing memory_state by (namespace, key)
// or creates a new one. For keyless writes (key == ""), always creates new.
// Domain is stamped on creation and never changes for a given memory_id.
func resolveOrCreateMemory(ctx context.Context, tx *sql.Tx, domain domains.Domain, namespace, key string) (string, error) {
	if key != "" {
		var existing, existingDomain string
		row := tx.QueryRowContext(ctx,
			`SELECT memory_id, domain FROM memory_state WHERE namespace = ? AND memory_key = ?`,
			namespace, key,
		)
		err := row.Scan(&existing, &existingDomain)
		if err == nil {
			if existingDomain != string(domain) {
				return "", fmt.Errorf("%w: memory %s/%s exists under domain %q, cannot write as %q",
					ErrInvalidInput, namespace, key, existingDomain, domain)
			}
			return existing, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return "", err
		}
		// Fall through to create.
	}

	memoryID := NewULID()
	var keyVal sql.NullString
	if key != "" {
		keyVal = sql.NullString{String: key, Valid: true}
	}
	_, err := tx.ExecContext(ctx, `
INSERT INTO memory_state (memory_id, domain, namespace, memory_key, activation, access_count, created_at)
VALUES (?, ?, ?, ?, 1.0, 0, ?)`,
		memoryID, string(domain), namespace, keyVal, time.Now().UTC().Format(memoryTimeFormat),
	)
	if err != nil {
		return "", fmt.Errorf("insert memory_state: %w", err)
	}
	return memoryID, nil
}

// deprecateRevisionTx is the ONE authorized mutation of
// memory_revisions.status. Guard test enforces this constraint.
func deprecateRevisionTx(ctx context.Context, tx *sql.Tx, revisionID string) error {
	_, err := tx.ExecContext(ctx,
		`UPDATE memory_revisions SET status = ? WHERE revision_id = ? AND status != ?`,
		string(StatusDeprecated), revisionID, string(StatusDeprecated),
	)
	// RowsAffected == 0 is OK (idempotent — already deprecated or does not exist).
	return err
}

// validateWriteInput checks all required fields before a revision is written.
// errDomainRequired is what a caller sees when Domain is empty.
//
// It is long deliberately. Its recipient is usually an autonomous agent that
// has one shot at fixing the call, and `domain is required` would tell it the
// field name it already has while withholding every fact it needs: what the
// field selects, why it stopped having a default, and which value is the one it
// almost certainly wants. A denial that costs the caller another turn to
// interpret is a denial that was not finished.
const errDomainRequired = `domain is required and has no default.

domain selects the revision's POLICY BUCKET: which namespace shapes and key ` +
	`rules apply, which facets are allowed, and which domain name the audit log ` +
	`stamps on every event this revision produces. Valid values are "memory", ` +
	`"knowledge" and "event".

If you are writing agent memory, set domain="memory" — that is exactly what an ` +
	`empty value used to mean, so this is a one-field change. If you are writing ` +
	`knowledge or event revisions, prefer their own entry points ` +
	`(knowledge.Store.Write, event.Store.Write): they set the domain for you and ` +
	`apply the rest of their domain's shape, which this path does not.

It is required rather than defaulted because a default is indistinguishable ` +
	`from a deliberate choice at the point where the difference matters — the ` +
	`audit log. Deprecate stamped every knowledge and event revision as "memory" ` +
	`until CW-20260910-0069, and promote's audit line was accurate only by ` +
	`inference through this default rather than by construction.`

func validateWriteInput(in WriteInput) error {
	if in.WorkstreamID != nil {
		if err := ValidateWorkstreamID(*in.WorkstreamID); err != nil {
			return fmt.Errorf("%w: %w", ErrInvalidInput, err)
		}
	}
	if in.Domain == "" {
		return fmt.Errorf("%w: %s", ErrInvalidInput, errDomainRequired)
	}
	if !in.Domain.Valid() {
		return fmt.Errorf("%w: invalid domain %q (valid: memory, knowledge, event)", ErrInvalidInput, in.Domain)
	}
	policy, err := policyFor(in.Domain)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidInput, err)
	}
	if err := policy.ValidateFacets(in.Facets); err != nil {
		return err
	}
	if in.Namespace == "" {
		return fmt.Errorf("%w: namespace is required", ErrInvalidInput)
	}
	// Namespace shape and key vocabulary are both the domain's own rules now.
	// The legacy user/{id}[/project|session/{id}]/memory parser moved into
	// memoryPolicy.ValidateNamespace, and the key vocabulary into its
	// ValidateKey; knowledge accepts externally-sourced keys by returning nil.
	if err := policy.ValidateNamespace(in.Namespace); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidInput, err)
	}
	if in.MemoryKey != "" {
		if err := policy.ValidateKey(in.MemoryKey); err != nil {
			return fmt.Errorf("%w: %w", ErrInvalidInput, err)
		}
	}
	if in.SessionID == "" {
		return fmt.Errorf("%w: session_id is required", ErrInvalidInput)
	}
	if in.Author.AgentID == "" {
		return fmt.Errorf("%w: author.agent_id is required", ErrInvalidInput)
	}
	if in.DerivedFrom == "" {
		return fmt.Errorf("%w: derived_from is required", ErrInvalidInput)
	}
	if !in.DerivedFrom.Valid() {
		return fmt.Errorf("%w: invalid derived_from %q", ErrInvalidInput, in.DerivedFrom)
	}
	if in.Trigger == "" {
		return fmt.Errorf("%w: trigger is required", ErrInvalidInput)
	}
	if !in.Trigger.Valid() {
		return fmt.Errorf("%w: invalid trigger %q", ErrInvalidInput, in.Trigger)
	}
	if in.Confidence < 0 || in.Confidence > 1.0 {
		return fmt.Errorf("%w: confidence must be in [0, 1.0], got %f", ErrInvalidInput, in.Confidence)
	}
	if in.Status != "" && !in.Status.Valid() {
		return fmt.Errorf("%w: invalid status %q", ErrInvalidInput, in.Status)
	}
	if in.Summary == "" {
		return fmt.Errorf("%w: summary is required", ErrInvalidInput)
	}
	if in.Dedup != "" && in.Dedup != "none" && in.Dedup != "semantic" {
		return fmt.Errorf("%w: invalid dedup mode %q (must be none or semantic)", ErrInvalidInput, in.Dedup)
	}
	if in.DedupThreshold < 0 || in.DedupThreshold > 1.0 {
		return fmt.Errorf("%w: dedup_threshold must be in [0, 1.0], got %f", ErrInvalidInput, in.DedupThreshold)
	}
	return nil
}

// DomainPolicy.ValidateFacets is the authoritative persistence-boundary check
// for the domain/facet contract; see domainpolicy.go. Every production writer
// ultimately reaches WriteRevision, including the root facade, knowledge
// wrapper, HTTP, MCP, and memory promotion paths, so no outer adapter can
// bypass these invariants.
