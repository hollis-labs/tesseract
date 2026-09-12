package memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/hollis-labs/tesseract/domains"
	"github.com/hollis-labs/tesseract/internal/memorylinks"
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
	Domain     domains.Domain
	Namespace  string
	MemoryKey  string
	Supersedes string
	Status     Status
	Author     Author
	Trigger    Trigger
	SessionID  string
	Origin     Origin
	Confidence float64
	Tags       []string
	TTL        time.Duration
	Payload    Payload
	Facets     Facets

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
	if err := validateWriteInput(in); err != nil {
		return Revision{}, err
	}
	// Consumer state is validated after validateWriteInput rather than inside
	// it, because the required-fields half needs a type declaration and
	// resolving one needs a namespace that has already been proven well-formed.
	// Handing a malformed namespace to the registry lookup would report a
	// missing required field for a type nobody named.
	if err := validateConsumerStateFor(in); err != nil {
		return Revision{}, err
	}

	// Make sure the namespace is in the policy registry before we write data
	// for it. Idempotent — only inserts the first time the namespace is seen.
	// See CW-20260428-0005 for the bug this closes (registry-vs-data drift).
	if s.namespaceRegistrar != nil {
		if err := s.namespaceRegistrar.EnsureNamespaceRegistered(ctx, in.Namespace); err != nil {
			return Revision{}, fmt.Errorf("ensure namespace registered: %w", err)
		}
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
		text := revisionEmbedText(Revision{Payload: in.Payload})
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
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Revision{}, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	memoryID, err := resolveOrCreateMemory(ctx, tx, in.Domain, in.Namespace, in.MemoryKey)
	if err != nil {
		return Revision{}, fmt.Errorf("resolve memory: %w", err)
	}

	status := in.Status
	if status == "" {
		status = StatusDraft
	}

	revisionID := NewULID()
	now := time.Now().UTC()

	var expiresAt *time.Time
	var ttlSeconds int64
	if in.TTL > 0 {
		ttlSeconds = int64(in.TTL.Seconds())
		exp := now.Add(in.TTL)
		expiresAt = &exp
	}

	tags := in.Tags
	if tags == nil {
		tags = []string{}
	}
	tagsJSON, err := json.Marshal(tags)
	if err != nil {
		return Revision{}, fmt.Errorf("marshal tags: %w", err)
	}

	// Nullable string helpers for the INSERT.
	nullStr := func(s string) sql.NullString {
		if s == "" {
			return sql.NullString{}
		}
		return sql.NullString{String: s, Valid: true}
	}
	nullTime := func(t *time.Time) sql.NullString {
		if t == nil {
			return sql.NullString{}
		}
		return sql.NullString{String: t.UTC().Format(memoryTimeFormat), Valid: true}
	}
	nullInt := func(v int64) sql.NullInt64 {
		if v == 0 {
			return sql.NullInt64{}
		}
		return sql.NullInt64{Int64: v, Valid: true}
	}

	var pointerScheme, pointerLocator string
	var pointerResolvedAt *time.Time
	if in.Facets.Pointer != nil {
		pointerScheme = in.Facets.Pointer.Scheme
		pointerLocator = in.Facets.Pointer.Locator
		pointerResolvedAt = in.Facets.Pointer.ResolvedAt
	}

	_, err = tx.ExecContext(ctx, `
INSERT INTO memory_revisions (
    revision_id, memory_id, domain, namespace, memory_key, status, supersedes,
    created_at, author_agent_id, author_version, trigger, session_id, origin,
    confidence, tags, ttl_seconds, expires_at, payload_summary, payload_body,
    facet_kind, facet_source, facet_pointer_scheme, facet_pointer_locator, facet_pointer_resolved_at,
    consumer_state
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		revisionID,
		memoryID,
		string(in.Domain),
		in.Namespace,
		nullStr(in.MemoryKey),
		string(status),
		nullStr(in.Supersedes),
		now.Format(memoryTimeFormat),
		in.Author.AgentID,
		in.Author.AgentVersion,
		string(in.Trigger),
		in.SessionID,
		string(in.Origin),
		in.Confidence,
		string(tagsJSON),
		nullInt(ttlSeconds),
		nullTime(expiresAt),
		nullStr(in.Payload.Summary),
		nullStr(in.Payload.Body),
		nullStr(in.Facets.Kind),
		nullStr(in.Facets.Source),
		nullStr(pointerScheme),
		nullStr(pointerLocator),
		nullTime(pointerResolvedAt),
		nullStr(string(in.ConsumerState)),
	)
	if err != nil {
		return Revision{}, fmt.Errorf("insert revision: %w", err)
	}

	// If supersedes is set, verify it belongs to the same logical memory
	// and auto-deprecate it. Cross-memory deprecation is rejected to prevent
	// corrupting unrelated memory_state.current_revision pointers.
	if in.Supersedes != "" {
		var supersededMemID string
		supErr := tx.QueryRowContext(ctx,
			`SELECT memory_id FROM memory_revisions WHERE revision_id = ?`,
			in.Supersedes,
		).Scan(&supersededMemID)
		if supErr != nil {
			if errors.Is(supErr, sql.ErrNoRows) {
				return Revision{}, fmt.Errorf("%w: supersedes revision %s not found", ErrInvalidInput, in.Supersedes)
			}
			return Revision{}, fmt.Errorf("verify supersedes: %w", supErr)
		}
		if supersededMemID != memoryID {
			return Revision{}, fmt.Errorf("%w: supersedes revision %s belongs to a different memory", ErrInvalidInput, in.Supersedes)
		}
		if depErr := deprecateRevisionTx(ctx, tx, in.Supersedes); depErr != nil {
			return Revision{}, fmt.Errorf("deprecate superseded: %w", depErr)
		}
	}

	// Parse this revision's `[[links]]` into the edge table (CW-20260825-0017).
	//
	// Inside the same transaction as the revision, so a committed revision
	// always has its edges and a rolled-back one leaves none behind. It runs
	// AFTER the insert above because the edge rows carry from_revision_id as a
	// foreign key, and after resolveOrCreateMemory because a revision that
	// cites its own key resolves against the memory_state row that call
	// created.
	//
	// A link whose target names nothing is stored unresolved rather than
	// dropped, and that is not a degraded case: 15% of the corpus's links
	// point at keys renamed away years of revisions ago, and the graph is
	// more useful knowing they were written than pretending they were not.
	if err = memorylinks.WriteReferences(ctx, tx, memorylinks.TxResolver{Tx: tx},
		memorylinks.RevisionRef{
			RevisionID: revisionID,
			MemoryID:   memoryID,
			Namespace:  in.Namespace,
			CreatedAt:  now.Format(memoryTimeFormat),
		},
		memorylinks.LinkText(in.Payload.Summary, in.Payload.Body),
	); err != nil {
		return Revision{}, fmt.Errorf("index links: %w", err)
	}

	// Bind edges that were written pointing at this key before anything
	// carried it. Resolution is otherwise forward-only, and the ordinary
	// authoring order writes the citing record first.
	if err = memorylinks.ResolvePending(ctx, tx, memorylinks.TxResolver{Tx: tx}, in.MemoryKey); err != nil {
		return Revision{}, fmt.Errorf("resolve pending links: %w", err)
	}

	// Point the memory_state current_revision to this new revision.
	_, err = tx.ExecContext(ctx,
		`UPDATE memory_state SET current_revision = ? WHERE memory_id = ?`,
		revisionID, memoryID,
	)
	if err != nil {
		return Revision{}, fmt.Errorf("update state: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return Revision{}, fmt.Errorf("commit: %w", err)
	}

	// Everything below this line is post-commit work: the revision is durable
	// and the caller is going to be told so no matter what happens next. It
	// therefore runs on a context detached from the caller's cancellation. An
	// HTTP client that disconnects — or an MCP call that is canceled —
	// between the commit above and the enqueue below would otherwise drop the
	// embed job permanently, leaving a committed revision that no worker will
	// ever see. WithoutCancel keeps trace state and request values and drops
	// only the cancellation. CW-20260826-0018.
	postCommitCtx := context.WithoutCancel(ctx)

	// The enqueue stays best-effort — a queue outage must not roll back a
	// durable write — but it is no longer silent. A failure increments the
	// store's enqueue-failure counter (read back via DeferredEmbeddingStatus)
	// and logs the identity needed to recover, mirroring the shape
	// contextstore uses for audit emit. Recovery is POST
	// /v1/admin/queue/backfill, which only helps if someone knows to run it.
	if err := s.queue.Enqueue(postCommitCtx, Job{
		Kind:    EmbedJobKind,
		Payload: []byte(fmt.Sprintf(`{"revision_id":%q}`, revisionID)),
	}); err != nil {
		s.enqueueFailures.Add(1)
		// Identity only. This line may name the memory that failed to embed;
		// it must never carry the memory's contents.
		s.log().WarnContext(postCommitCtx, "embed enqueue failed",
			"job_kind", EmbedJobKind,
			"revision_id", revisionID,
			"memory_id", memoryID,
			"namespace", in.Namespace,
			"domain", string(in.Domain),
			"queue", fmt.Sprintf("%T", s.queue),
			"err", err,
		)
	}

	if s.auditSink != nil {
		actor := in.Author.AgentID
		key := in.MemoryKey
		if key == "" {
			key = memoryID // logical memory identity for keyless writes
		}
		// Same post-commit reasoning as the enqueue above: the audit record of
		// a committed write must not be lost because the caller hung up. The
		// errors stay discarded here because contextstore's emit path
		// structured-logs every failure itself.
		//
		// The domain rides along as an argument rather than selecting a
		// method name. The four-arm switch this replaced ended in a default
		// that emitted memory.write, so a domain without an arm produced an
		// audit row naming the wrong domain — a log that lies is worse than
		// one that is missing, and it was the last silent default in the
		// domain-dispatch set (CW-20260909-0033).
		op := auditOpWrite
		if in.Supersedes != "" {
			op = auditOpSupersede
		}
		_ = s.auditSink.EmitRevision(postCommitCtx, string(in.Domain), op, actor, in.Namespace, key, revisionID, nil)
	}

	rev := Revision{
		RevisionID:    revisionID,
		MemoryID:      memoryID,
		Domain:        in.Domain,
		Namespace:     in.Namespace,
		MemoryKey:     in.MemoryKey,
		Status:        status,
		Supersedes:    in.Supersedes,
		CreatedAt:     now,
		Author:        in.Author,
		Trigger:       in.Trigger,
		SessionID:     in.SessionID,
		Origin:        in.Origin,
		Confidence:    in.Confidence,
		Tags:          tags,
		TTLSeconds:    ttlSeconds,
		ExpiresAt:     expiresAt,
		Payload:       in.Payload,
		Facets:        in.Facets,
		ConsumerState: in.ConsumerState,
		DedupMatch:    dedupMatch,
	}
	return rev, nil
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
	if in.Origin == "" {
		return fmt.Errorf("%w: origin is required", ErrInvalidInput)
	}
	if !in.Origin.Valid() {
		return fmt.Errorf("%w: invalid origin %q", ErrInvalidInput, in.Origin)
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
	if in.Payload.Summary == "" {
		return fmt.Errorf("%w: payload.summary is required", ErrInvalidInput)
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
