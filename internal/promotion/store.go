package promotion

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hollis-labs/tesseract/domains"
	"github.com/hollis-labs/tesseract/internal/contextstore"
	"github.com/hollis-labs/tesseract/internal/memory"
	"github.com/hollis-labs/tesseract/internal/memorytime"
	"github.com/hollis-labs/tesseract/internal/workspace"
	"modernc.org/sqlite"
)

type Store struct {
	db       *sql.DB
	mem      *memory.Store
	ws       *workspace.Store
	registry *contextstore.Store
	now      func() time.Time
}

func NewStore(registry *contextstore.Store, mem *memory.Store) *Store {
	var db *sql.DB
	if registry != nil {
		db = registry.DB()
	} else if mem != nil {
		db = mem.DB()
	}
	return &Store{
		db:       db,
		mem:      mem,
		ws:       workspace.NewStore(db),
		registry: registry,
		now:      time.Now,
	}
}

func NewStoreWithDB(db *sql.DB, mem *memory.Store, ws *workspace.Store) *Store {
	if ws == nil && db != nil {
		ws = workspace.NewStore(db)
	}
	return &Store{
		db:  db,
		mem: mem,
		ws:  ws,
		now: time.Now,
	}
}

type sourceSnapshot struct {
	Domain                                                                            domains.Domain
	ItemID, VersionToken, Namespace, Key, Summary, Body, DataSchemaHash, WorkstreamID string
	Data, ConsumerState                                                               json.RawMessage
	Tags                                                                              []string
}

type requestRow struct {
	Metadata
	SourceItemID, SourceVersionToken, SourceDigest string
	Target                                         Target
	ApprovalID                                     string
	ApprovedAt                                     *time.Time
	AppliedAt                                      *time.Time
	TargetItemID, TargetRevisionID                 string
}

func (s *Store) ensureSchema(ctx context.Context) error {
	if s == nil || s.db == nil {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS promotion_requests (
	request_id                    TEXT PRIMARY KEY,
	status                        TEXT NOT NULL CHECK (status IN ('pending','approved','applied')),
	source_domain                 TEXT NOT NULL,
	source_item_id                TEXT NOT NULL,
	source_version_token          TEXT NOT NULL,
	source_namespace              TEXT NOT NULL,
	source_digest                 TEXT NOT NULL,
	target_domain                 TEXT NOT NULL,
	target_namespace              TEXT NOT NULL,
	target_key                    TEXT NULL,
	target_item_id                TEXT NULL,
	expected_target_revision_id   TEXT NULL,
	target_spec_json              TEXT NOT NULL,
	requested_by                  TEXT NOT NULL,
	reason                        TEXT NOT NULL DEFAULT '',
	requested_at                  TEXT NOT NULL,
	approval_id                   TEXT NULL UNIQUE,
	approved_by                   TEXT NULL,
	approval_notes                TEXT NULL,
	approved_at                   TEXT NULL,
	applied_by                    TEXT NULL,
	applied_at                    TEXT NULL,
	result_item_id                TEXT NULL,
	result_revision_id            TEXT NULL UNIQUE
);
CREATE INDEX IF NOT EXISTS idx_promotion_source ON promotion_requests(source_item_id);
CREATE INDEX IF NOT EXISTS idx_promotion_target ON promotion_requests(target_item_id);
`)
	return err
}

func (s *Store) Request(ctx context.Context, in RequestInput) (RequestReceipt, error) {
	if s == nil || s.db == nil || s.mem == nil {
		return RequestReceipt{}, fmt.Errorf("promotion unavailable")
	}
	if in.SourceRevisionID != "" && in.SourceVersionToken == "" {
		in.SourceVersionToken = in.SourceRevisionID
	}
	if strings.TrimSpace(in.SourceItemID) == "" || strings.TrimSpace(in.SourceVersionToken) == "" {
		return RequestReceipt{}, fmt.Errorf("%w: source_item_id and source_version_token are required", ErrInvalidInput)
	}
	if strings.TrimSpace(in.Actor) == "" {
		return RequestReceipt{}, fmt.Errorf("%w: actor is required", ErrInvalidInput)
	}

	if err := s.ensureSchema(ctx); err != nil {
		return RequestReceipt{}, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RequestReceipt{}, err
	}
	defer func() { _ = tx.Rollback() }()

	source, err := s.loadSource(ctx, tx, in.SourceDomain, in.SourceItemID)
	if err != nil {
		return RequestReceipt{}, err
	}
	if source.VersionToken != in.SourceVersionToken {
		return RequestReceipt{}, fmt.Errorf("%w: source_version_token does not match current source content", ErrSourceStale)
	}
	target, err := s.normalizeTarget(ctx, tx, in.Target, source)
	if err != nil {
		return RequestReceipt{}, err
	}
	if target.Domain == domains.Domain("workspace") {
		if strings.TrimSpace(source.Summary) == "" {
			return RequestReceipt{}, fmt.Errorf("%w: summary is required for workspace target", ErrInvalidInput)
		}
	} else {
		if validationErr := memory.ValidateWriteInput(writeInput(target, source)); validationErr != nil {
			return RequestReceipt{}, fmt.Errorf("%w: %w", ErrInvalidInput, validationErr)
		}
	}
	digest, err := sourceDigest(source)
	if err != nil {
		return RequestReceipt{}, err
	}
	targetJSON, err := json.Marshal(target)
	if err != nil {
		return RequestReceipt{}, err
	}
	requestID := memory.NewULID()
	now := s.now().UTC()

	expectedRevision := target.ExpectedRevisionID
	if expectedRevision == "" && target.ExpectedVersionToken != "" {
		expectedRevision = target.ExpectedVersionToken
	}

	_, err = tx.ExecContext(ctx, `INSERT INTO promotion_requests (
		request_id,status,source_domain,source_item_id,source_version_token,source_namespace,source_digest,
		target_domain,target_namespace,target_key,target_item_id,expected_target_revision_id,target_spec_json,
		requested_by,reason,requested_at
	) VALUES (?, 'pending', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		requestID, string(source.Domain), source.ItemID, source.VersionToken, source.Namespace, digest,
		string(target.Domain), target.Namespace, nullText(target.Key), nullText(target.ItemID),
		nullText(expectedRevision), string(targetJSON), in.Actor, in.Reason,
		memorytime.Format(now))
	if err != nil {
		return RequestReceipt{}, fmt.Errorf("create promotion request: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return RequestReceipt{}, err
	}
	return requestReceipt(requestID, "pending", source, target), nil
}

func (s *Store) Approve(ctx context.Context, in ApproveInput) (ApprovalReceipt, error) {
	if strings.TrimSpace(in.RequestID) == "" || strings.TrimSpace(in.Actor) == "" {
		return ApprovalReceipt{}, fmt.Errorf("%w: request_id and actor are required", ErrInvalidInput)
	}
	if err := s.ensureSchema(ctx); err != nil {
		return ApprovalReceipt{}, err
	}
	return retry(ctx, "approve", func() (ApprovalReceipt, error) {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return ApprovalReceipt{}, err
		}
		defer func() { _ = tx.Rollback() }()
		row, err := loadRequest(ctx, tx, in.RequestID)
		if err != nil {
			return ApprovalReceipt{}, err
		}
		if row.ApprovalID != "" {
			return ApprovalReceipt{RequestID: row.RequestID, ApprovalID: row.ApprovalID, Status: "approved", ApprovedAt: *row.ApprovedAt}, nil
		}
		if row.Status != "pending" {
			return ApprovalReceipt{}, fmt.Errorf("%w: request status is %s", ErrInvalidInput, row.Status)
		}
		approvalID := memory.NewULID()
		now := s.now().UTC()
		res, err := tx.ExecContext(ctx, `UPDATE promotion_requests SET status='approved', approval_id=?, approved_by=?, approval_notes=?, approved_at=? WHERE request_id=? AND status='pending'`,
			approvalID, in.Actor, in.Notes, memorytime.Format(now), in.RequestID)
		if err != nil {
			return ApprovalReceipt{}, err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return ApprovalReceipt{}, errRetry
		}
		if err := tx.Commit(); err != nil {
			return ApprovalReceipt{}, err
		}
		return ApprovalReceipt{RequestID: in.RequestID, ApprovalID: approvalID, Status: "approved", ApprovedAt: now}, nil
	})
}

func (s *Store) Apply(ctx context.Context, in ApplyInput) (ApplyReceipt, error) {
	if strings.TrimSpace(in.RequestID) == "" || strings.TrimSpace(in.Actor) == "" {
		return ApplyReceipt{}, fmt.Errorf("%w: request_id and actor are required", ErrInvalidInput)
	}
	if err := s.ensureSchema(ctx); err != nil {
		return ApplyReceipt{}, err
	}

	type result struct {
		receipt             ApplyReceipt
		rev                 memory.Revision
		write               memory.WriteInput
		namespaceRegistered bool
		committed           bool
	}
	out, applyErr := retry(ctx, "apply", func() (result, error) {
		tx, beginErr := s.db.BeginTx(ctx, nil)
		if beginErr != nil {
			return result{}, beginErr
		}
		defer func() { _ = tx.Rollback() }()
		row, err := loadRequest(ctx, tx, in.RequestID)
		if err != nil {
			return result{}, err
		}
		if row.Status == "applied" {
			return result{receipt: applyReceipt(row)}, nil
		}
		if row.Status != "approved" || row.ApprovalID == "" {
			return result{}, fmt.Errorf("%w: request status is %s", ErrNotApproved, row.Status)
		}
		source, err := s.loadSource(ctx, tx, row.SourceDomain, row.SourceItemID)
		if err != nil {
			return result{}, err
		}
		if source.VersionToken != row.SourceVersionToken {
			return result{}, fmt.Errorf("%w: source item changed after review", ErrSourceStale)
		}
		digest, err := sourceDigest(source)
		if err != nil {
			return result{}, err
		}
		if digest != row.SourceDigest {
			return result{}, fmt.Errorf("%w: source content changed after review", ErrSourceStale)
		}

		var resultItemID, resultRevisionID string
		var rev memory.Revision
		var write memory.WriteInput
		var registered bool

		if row.Target.Domain == domains.Domain("workspace") {
			// Write to workspace target.
			if row.Target.ItemID != "" {
				// Updating existing workspace item.
				expectedToken := row.Target.ExpectedRevisionID
				if expectedToken == "" {
					expectedToken = row.Target.ExpectedVersionToken
				}
				var currentToken, currentNamespace, currentKey string
				err = tx.QueryRowContext(ctx, `SELECT version_token, namespace, COALESCE(key_name, '') FROM workspace_items WHERE item_id=?`, row.Target.ItemID).
					Scan(&currentToken, &currentNamespace, &currentKey)
				if errors.Is(err, sql.ErrNoRows) {
					return result{}, fmt.Errorf("%w: target workspace item no longer exists", ErrTargetStale)
				}
				if err != nil {
					return result{}, err
				}
				if currentToken != expectedToken {
					return result{}, fmt.Errorf("%w: target workspace item changed after review", ErrTargetStale)
				}
				editIn := workspace.EditInput{
					ItemID:       row.Target.ItemID,
					VersionToken: currentToken,
					Author:       row.Target.Author,
					SessionID:    row.Target.SessionID,
					Summary:      &source.Summary,
				}
				if source.Body != "" {
					editIn.Body = &source.Body
				}
				if len(source.Data) > 0 {
					dCopy := append(json.RawMessage(nil), source.Data...)
					editIn.Data = &dCopy
				}
				if source.DataSchemaHash != "" {
					editIn.DataSchemaHash = &source.DataSchemaHash
				}
				if len(row.Target.Tags) > 0 {
					tCopy := append([]string(nil), row.Target.Tags...)
					editIn.Tags = &tCopy
				} else if len(source.Tags) > 0 {
					tCopy := append([]string(nil), source.Tags...)
					editIn.Tags = &tCopy
				}
				if len(row.Target.ConsumerState) > 0 {
					csCopy := append(json.RawMessage(nil), row.Target.ConsumerState...)
					editIn.ConsumerState = &csCopy
				} else if len(source.ConsumerState) > 0 {
					csCopy := append(json.RawMessage(nil), source.ConsumerState...)
					editIn.ConsumerState = &csCopy
				}
				if row.Target.WorkstreamID != nil {
					editIn.WorkstreamID = row.Target.WorkstreamID
				}
				edited, editErr := s.ws.EditInTx(ctx, tx, editIn)
				if editErr != nil {
					return result{}, fmt.Errorf("update target workspace item: %w", editErr)
				}
				resultItemID = edited.ItemID
				resultRevisionID = edited.VersionToken
			} else {
				// Creating new workspace item.
				if row.Target.Key != "" {
					var occupied int
					if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM workspace_items WHERE namespace=? AND key_name=?)`, row.Target.Namespace, row.Target.Key).Scan(&occupied); err != nil {
						return result{}, err
					}
					if occupied != 0 {
						return result{}, fmt.Errorf("%w", ErrTargetKeyOccupied)
					}
				}
				tags := append([]string(nil), source.Tags...)
				if len(row.Target.Tags) > 0 {
					tags = append([]string(nil), row.Target.Tags...)
				}
				var cs json.RawMessage
				if len(row.Target.ConsumerState) > 0 {
					cs = append(json.RawMessage(nil), row.Target.ConsumerState...)
				} else if len(source.ConsumerState) > 0 {
					cs = append(json.RawMessage(nil), source.ConsumerState...)
				}
				schemaHash := source.DataSchemaHash
				if row.Target.DataSchemaHash != "" {
					schemaHash = row.Target.DataSchemaHash
				}
				createIn := workspace.CreateInput{
					Namespace:      row.Target.Namespace,
					Key:            row.Target.Key,
					Summary:        source.Summary,
					Body:           source.Body,
					Data:           append(json.RawMessage(nil), source.Data...),
					DataSchemaHash: schemaHash,
					Tags:           tags,
					ConsumerState:  cs,
					Author:         row.Target.Author,
					SessionID:      row.Target.SessionID,
					WorkstreamID:   row.Target.WorkstreamID,
				}
				created, createErr := s.ws.CreateInTx(ctx, tx, createIn)
				if createErr != nil {
					return result{}, fmt.Errorf("create target workspace item: %w", createErr)
				}
				resultItemID = created.ItemID
				resultRevisionID = created.VersionToken
			}
		} else {
			// Writing to revisioned domain (memory, knowledge, event).
			if s.registry != nil {
				registered, err = s.registry.EnsureNamespaceRegisteredTx(ctx, tx, row.Target.Namespace, "promotion")
				if err != nil {
					return result{}, fmt.Errorf("register target namespace: %w", err)
				}
			} else {
				ownerType, ownerID := contextstore.DeriveNamespaceOwner(row.Target.Namespace)
				policyJSON, _ := json.Marshal(map[string]any{"source": "promotion"})
				now := s.now().UTC().Format(time.RFC3339)
				res, _ := tx.ExecContext(ctx, `INSERT OR IGNORE INTO namespace_policies (namespace, owner_type, owner_id, policy_json, updated_at) VALUES (?, ?, ?, ?, ?)`,
					row.Target.Namespace, ownerType, ownerID, string(policyJSON), now)
				if res != nil {
					rows, _ := res.RowsAffected()
					registered = rows != 0
				}
			}

			write = writeInput(row.Target, source)
			if row.Target.ItemID != "" {
				var domain, namespace, key, current string
				err = tx.QueryRowContext(ctx, `SELECT domain, namespace, COALESCE(memory_key,''), COALESCE(current_revision,'') FROM memory_state WHERE memory_id=?`, row.Target.ItemID).
					Scan(&domain, &namespace, &key, &current)
				if errors.Is(err, sql.ErrNoRows) {
					return result{}, fmt.Errorf("%w: target item no longer exists", ErrTargetStale)
				}
				if err != nil {
					return result{}, err
				}
				if domain != string(row.Target.Domain) || namespace != row.Target.Namespace || key != row.Target.Key || current != row.Target.ExpectedRevisionID {
					return result{}, fmt.Errorf("%w: target item changed after review", ErrTargetStale)
				}
				write.Supersedes = current
				rev, err = s.mem.WriteRevisionToItemInTx(ctx, tx, write, row.Target.ItemID)
			} else {
				if row.Target.Key != "" {
					var occupied int
					if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM memory_state WHERE namespace=? AND memory_key=?)`, row.Target.Namespace, row.Target.Key).Scan(&occupied); err != nil {
						return result{}, err
					}
					if occupied != 0 {
						return result{}, fmt.Errorf("%w", ErrTargetKeyOccupied)
					}
				}
				rev, err = s.mem.WriteRevisionInTx(ctx, tx, write)
			}
			if err != nil {
				return result{}, err
			}
			resultItemID = rev.ItemID
			resultRevisionID = rev.RevisionID
		}

		// If source is a revisioned domain (memory, knowledge, event), deprecate the source revision.
		if row.SourceDomain != domains.Domain("workspace") {
			if depErr := s.mem.DeprecateInTx(ctx, tx, row.SourceVersionToken); depErr != nil {
				return result{}, fmt.Errorf("deprecate source revision: %w", depErr)
			}
		}

		now := s.now().UTC()
		res, err := tx.ExecContext(ctx, `UPDATE promotion_requests SET status='applied', applied_by=?, applied_at=?, result_item_id=?, result_revision_id=? WHERE request_id=? AND status='approved'`,
			in.Actor, memorytime.Format(now), resultItemID, resultRevisionID, in.RequestID)
		if err != nil {
			return result{}, err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return result{}, errRetry
		}
		if err := tx.Commit(); err != nil {
			return result{}, err
		}
		row.Status, row.TargetItemID, row.TargetRevisionID, row.AppliedAt = "applied", resultItemID, resultRevisionID, &now
		return result{receipt: applyReceipt(row), rev: rev, write: write, namespaceRegistered: registered, committed: true}, nil
	})
	if applyErr != nil {
		return ApplyReceipt{}, applyErr
	}
	if out.committed && rowIsRevisioned(out.receipt.TargetDomain) {
		if out.namespaceRegistered && s.registry != nil {
			s.registry.FinishNamespaceRegistration(ctx, out.write.Namespace, "promotion")
		}
		s.mem.FinishRevisionWrite(ctx, out.rev, out.write)
	}
	return out.receipt, nil
}

func rowIsRevisioned(d domains.Domain) bool {
	return d != domains.Domain("workspace")
}

func (s *Store) LookupMetadata(ctx context.Context, requestID string) (Metadata, error) {
	if err := s.ensureSchema(ctx); err != nil {
		return Metadata{}, err
	}
	var meta Metadata
	var srcDomain, tgtDomain string
	err := s.db.QueryRowContext(ctx, `SELECT request_id,status,source_domain,source_namespace,target_domain,target_namespace FROM promotion_requests WHERE request_id=?`, requestID).
		Scan(&meta.RequestID, &meta.Status, &srcDomain, &meta.SourceNamespace, &tgtDomain, &meta.TargetNamespace)
	if errors.Is(err, sql.ErrNoRows) {
		return Metadata{}, fmt.Errorf("%w: request_id %s", ErrNotFound, requestID)
	}
	if err != nil {
		return Metadata{}, err
	}
	meta.SourceDomain = domains.Domain(srcDomain)
	meta.TargetDomain = domains.Domain(tgtDomain)
	return meta, nil
}

func (s *Store) normalizeTarget(ctx context.Context, tx *sql.Tx, target Target, source sourceSnapshot) (Target, error) {
	if target.Author.AgentID == "" || target.SessionID == "" {
		return Target{}, fmt.Errorf("%w: target author.agent_id and session_id are required", ErrInvalidInput)
	}
	if target.TTLSeconds < 0 {
		return Target{}, fmt.Errorf("%w: target ttl_seconds cannot be negative", ErrInvalidInput)
	}

	if target.Domain == domains.Domain("workspace") {
		if target.ItemID != "" {
			expectedToken := target.ExpectedRevisionID
			if expectedToken == "" {
				expectedToken = target.ExpectedVersionToken
			}
			if expectedToken == "" {
				return Target{}, fmt.Errorf("%w: expected_target_revision_id or expected_version_token is required with target item_id", ErrInvalidInput)
			}
			var namespace, key, versionToken, workstream string
			err := tx.QueryRowContext(ctx, `SELECT namespace, COALESCE(key_name,''), version_token, COALESCE(workstream_id,'') FROM workspace_items WHERE item_id=?`, target.ItemID).
				Scan(&namespace, &key, &versionToken, &workstream)
			if errors.Is(err, sql.ErrNoRows) {
				return Target{}, fmt.Errorf("%w: target item_id does not exist", ErrTargetStale)
			}
			if err != nil {
				return Target{}, err
			}
			if versionToken != expectedToken {
				return Target{}, fmt.Errorf("%w: expected_target_revision_id is not current", ErrTargetStale)
			}
			target.Namespace, target.Key = namespace, key
			if target.WorkstreamID == nil {
				target.WorkstreamID = stringPointer(workstream)
			}
		} else {
			if target.Namespace == "" {
				return Target{}, fmt.Errorf("%w: target namespace is required", ErrInvalidInput)
			}
			if target.Key != "" {
				var occupied int
				if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM workspace_items WHERE namespace=? AND key_name=?)`, target.Namespace, target.Key).Scan(&occupied); err != nil {
					return Target{}, err
				}
				if occupied != 0 {
					return Target{}, fmt.Errorf("%w", ErrTargetKeyOccupied)
				}
			}
			if target.WorkstreamID == nil {
				target.WorkstreamID = stringPointer(source.WorkstreamID)
			}
		}
		return target, nil
	}

	if target.ItemID != "" {
		if target.Domain != "" || target.Namespace != "" || target.Key != "" {
			return Target{}, fmt.Errorf("%w: existing target item_id cannot be combined with domain, namespace, or key", ErrInvalidInput)
		}
		if target.ExpectedRevisionID == "" {
			return Target{}, fmt.Errorf("%w: expected_target_revision_id is required with target item_id", ErrInvalidInput)
		}
		var domain, key, current, workstream string
		err := tx.QueryRowContext(ctx, `SELECT s.domain,s.namespace,COALESCE(s.memory_key,''),COALESCE(s.current_revision,''),COALESCE(r.workstream_id,'') FROM memory_state s LEFT JOIN memory_revisions r ON r.revision_id=s.current_revision WHERE s.memory_id=?`, target.ItemID).
			Scan(&domain, &target.Namespace, &key, &current, &workstream)
		if errors.Is(err, sql.ErrNoRows) {
			return Target{}, fmt.Errorf("%w: target item_id does not exist", ErrTargetStale)
		}
		if err != nil {
			return Target{}, err
		}
		if current != target.ExpectedRevisionID {
			return Target{}, fmt.Errorf("%w: expected_target_revision_id is not current", ErrTargetStale)
		}
		target.Domain, target.Key = domains.Domain(domain), key
		if target.WorkstreamID == nil {
			target.WorkstreamID = stringPointer(workstream)
		}
	} else {
		if target.ExpectedRevisionID != "" {
			return Target{}, fmt.Errorf("%w: expected_target_revision_id requires target item_id", ErrInvalidInput)
		}
		if !target.Domain.Valid() || target.Namespace == "" {
			return Target{}, fmt.Errorf("%w: target domain and namespace are required", ErrInvalidInput)
		}
		if target.Key != "" {
			var occupied int
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM memory_state WHERE namespace=? AND memory_key=?)`, target.Namespace, target.Key).Scan(&occupied); err != nil {
				return Target{}, err
			}
			if occupied != 0 {
				return Target{}, fmt.Errorf("%w", ErrTargetKeyOccupied)
			}
		}
		if target.WorkstreamID == nil {
			target.WorkstreamID = stringPointer(source.WorkstreamID)
		}
	}

	switch target.Domain {
	case domains.Memory:
		if target.Trigger == "" || target.DerivedFrom == "" {
			return Target{}, fmt.Errorf("%w: memory target trigger and derived_from are required", ErrInvalidInput)
		}
		if target.Status == "" {
			target.Status = memory.StatusDraft
		}
		if target.Kind != "" || target.Source != "" || target.Pointer != nil {
			return Target{}, fmt.Errorf("%w: knowledge fields are invalid for memory targets", ErrInvalidInput)
		}
	case domains.Knowledge:
		if target.Status != "" && target.Status != memory.StatusCanonical {
			return Target{}, fmt.Errorf("%w: knowledge target status must be canonical", ErrInvalidInput)
		}
		if target.Trigger != "" && target.Trigger != memory.TriggerManual {
			return Target{}, fmt.Errorf("%w: knowledge target trigger must be manual", ErrInvalidInput)
		}
		if target.DerivedFrom != "" && target.DerivedFrom != memory.DerivedFromReference {
			return Target{}, fmt.Errorf("%w: knowledge target derived_from must be reference", ErrInvalidInput)
		}
		target.Status = memory.StatusCanonical
		target.Trigger = memory.TriggerManual
		target.DerivedFrom = memory.DerivedFromReference
		if target.Confidence == 0 {
			target.Confidence = .9
		}
		if target.Pointer == nil {
			return Target{}, fmt.Errorf("%w: knowledge target pointer is required", ErrInvalidInput)
		}
		if target.Pointer.ResolvedAt == nil {
			now := s.now().UTC()
			target.Pointer.ResolvedAt = &now
		}
	case domains.Event:
		if target.Status != "" && target.Status != memory.StatusCanonical {
			return Target{}, fmt.Errorf("%w: event target status must be canonical", ErrInvalidInput)
		}
		target.Status = memory.StatusCanonical
		if target.Trigger == "" {
			target.Trigger = memory.TriggerManual
		}
		if target.DerivedFrom == "" {
			target.DerivedFrom = memory.DerivedFromObservation
		}
		if target.Confidence == 0 {
			target.Confidence = .9
		}
		if target.Kind != "" || target.Source != "" || target.Pointer != nil {
			return Target{}, fmt.Errorf("%w: knowledge fields are invalid for event targets", ErrInvalidInput)
		}
	default:
		return Target{}, fmt.Errorf("%w: target domain must be workspace, memory, knowledge, or event", ErrInvalidInput)
	}
	return target, nil
}

func writeInput(target Target, source sourceSnapshot) memory.WriteInput {
	return memory.WriteInput{
		Domain: target.Domain, Namespace: target.Namespace, Actor: "user", MemoryKey: target.Key, WorkstreamID: target.WorkstreamID,
		Status: target.Status, Author: target.Author, Trigger: target.Trigger, SessionID: target.SessionID,
		DerivedFrom: target.DerivedFrom, Confidence: target.Confidence, Tags: target.Tags,
		TTL: time.Duration(target.TTLSeconds) * time.Second, Summary: source.Summary, Body: source.Body,
		Data: append(json.RawMessage(nil), source.Data...), DataSchemaHash: target.DataSchemaHash,
		Facets:        memory.Facets{Kind: target.Kind, Source: target.Source, Pointer: target.Pointer},
		ConsumerState: append(json.RawMessage(nil), target.ConsumerState...),
	}
}

func (s *Store) loadSource(ctx context.Context, tx *sql.Tx, sourceDomain domains.Domain, itemID string) (sourceSnapshot, error) {
	if sourceDomain == domains.Domain("workspace") {
		return loadWorkspaceSource(ctx, tx, itemID)
	}
	if sourceDomain == domains.Memory || sourceDomain == domains.Knowledge || sourceDomain == domains.Event {
		return loadRevisionedSource(ctx, tx, sourceDomain, itemID)
	}

	// Auto-detect domain if omitted.
	snap, err := loadWorkspaceSource(ctx, tx, itemID)
	if err == nil {
		return snap, nil
	}
	if errors.Is(err, ErrSourceDeleted) {
		return snap, err
	}

	snap, err = loadRevisionedSource(ctx, tx, "", itemID)
	if err == nil {
		return snap, nil
	}
	return sourceSnapshot{}, fmt.Errorf("%w: item_id %s", ErrSourceNotFound, itemID)
}

func loadWorkspaceSource(ctx context.Context, tx *sql.Tx, itemID string) (sourceSnapshot, error) {
	var out sourceSnapshot
	var key, body, data, dataHash, tags, consumer, workstream sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT item_id,version_token,namespace,COALESCE(key_name,''),summary,body,data,data_schema_hash,tags,consumer_state,COALESCE(workstream_id,'') FROM workspace_items WHERE item_id=?`, itemID).
		Scan(&out.ItemID, &out.VersionToken, &out.Namespace, &key, &out.Summary, &body, &data, &dataHash, &tags, &consumer, &workstream)
	if errors.Is(err, sql.ErrNoRows) {
		var deleted int
		if lookupErr := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM workspace_tombstones WHERE item_id=?)`, itemID).Scan(&deleted); lookupErr != nil {
			return out, lookupErr
		}
		if deleted != 0 {
			return out, fmt.Errorf("%w: item_id %s", ErrSourceDeleted, itemID)
		}
		return out, fmt.Errorf("%w: item_id %s", ErrSourceNotFound, itemID)
	}
	if err != nil {
		return out, err
	}
	out.Domain = domains.Domain("workspace")
	out.Key, out.Body, out.DataSchemaHash, out.WorkstreamID = key.String, body.String, dataHash.String, workstream.String
	if data.Valid {
		out.Data = append(json.RawMessage(nil), data.String...)
	}
	if consumer.Valid {
		out.ConsumerState = append(json.RawMessage(nil), consumer.String...)
	}
	if tags.Valid {
		if err := json.Unmarshal([]byte(tags.String), &out.Tags); err != nil {
			return out, err
		}
	}
	return out, nil
}

func loadRevisionedSource(ctx context.Context, tx *sql.Tx, expectedDomain domains.Domain, itemID string) (sourceSnapshot, error) {
	var out sourceSnapshot
	var domain, currentRevision, key string
	err := tx.QueryRowContext(ctx, `SELECT domain, namespace, COALESCE(memory_key, ''), COALESCE(current_revision, '') FROM memory_state WHERE memory_id=?`, itemID).
		Scan(&domain, &out.Namespace, &key, &currentRevision)
	if errors.Is(err, sql.ErrNoRows) {
		return out, fmt.Errorf("%w: item_id %s", ErrSourceNotFound, itemID)
	}
	if err != nil {
		return out, err
	}
	if expectedDomain != "" && domain != string(expectedDomain) {
		return out, fmt.Errorf("%w: item_id %s belongs to domain %s, not %s", ErrSourceNotFound, itemID, domain, expectedDomain)
	}
	if currentRevision == "" {
		return out, fmt.Errorf("%w: item_id %s has no active revision", ErrSourceDeleted, itemID)
	}

	var summary, body, data, dataHash, tags, consumer, workstream sql.NullString
	var status string
	err = tx.QueryRowContext(ctx, `SELECT payload_summary, payload_body, payload_data, payload_data_schema_hash, tags, consumer_state, COALESCE(workstream_id, ''), status FROM memory_revisions WHERE revision_id=?`, currentRevision).
		Scan(&summary, &body, &data, &dataHash, &tags, &consumer, &workstream, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return out, fmt.Errorf("%w: current revision %s", ErrSourceNotFound, currentRevision)
	}
	if err != nil {
		return out, err
	}
	if status == string(memory.StatusDeprecated) {
		return out, fmt.Errorf("%w: item_id %s current revision is deprecated", ErrSourceDeleted, itemID)
	}

	out.Domain = domains.Domain(domain)
	out.ItemID = itemID
	out.VersionToken = currentRevision
	out.Key = key
	out.Summary = summary.String
	out.Body = body.String
	out.DataSchemaHash = dataHash.String
	out.WorkstreamID = workstream.String
	if data.Valid {
		out.Data = append(json.RawMessage(nil), data.String...)
	}
	if consumer.Valid {
		out.ConsumerState = append(json.RawMessage(nil), consumer.String...)
	}
	if tags.Valid {
		if err := json.Unmarshal([]byte(tags.String), &out.Tags); err != nil {
			return out, err
		}
	}
	return out, nil
}

func sourceDigest(source sourceSnapshot) (string, error) {
	b, err := json.Marshal(struct {
		Domain                                                                            string
		ItemID, VersionToken, Namespace, Key, Summary, Body, DataSchemaHash, WorkstreamID string
		Data, ConsumerState                                                               json.RawMessage
		Tags                                                                              []string
	}{
		Domain:         string(source.Domain),
		ItemID:         source.ItemID,
		VersionToken:   source.VersionToken,
		Namespace:      source.Namespace,
		Key:            source.Key,
		Summary:        source.Summary,
		Body:           source.Body,
		DataSchemaHash: source.DataSchemaHash,
		WorkstreamID:   source.WorkstreamID,
		Data:           source.Data,
		ConsumerState:  source.ConsumerState,
		Tags:           source.Tags,
	})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return fmt.Sprintf("%x", sum[:]), nil
}

type rowQuery interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func loadRequest(ctx context.Context, q rowQuery, requestID string) (requestRow, error) {
	var out requestRow
	var srcDomain, tgtDomain, targetJSON string
	var approvalID, approvedAt, appliedAt, targetItem, targetRevision sql.NullString
	err := q.QueryRowContext(ctx, `SELECT request_id,status,source_domain,source_namespace,target_domain,target_namespace,source_item_id,source_version_token,source_digest,target_spec_json,approval_id,approved_at,applied_at,result_item_id,result_revision_id FROM promotion_requests WHERE request_id=?`, requestID).
		Scan(&out.RequestID, &out.Status, &srcDomain, &out.SourceNamespace, &tgtDomain, &out.TargetNamespace, &out.SourceItemID, &out.SourceVersionToken, &out.SourceDigest, &targetJSON, &approvalID, &approvedAt, &appliedAt, &targetItem, &targetRevision)
	if errors.Is(err, sql.ErrNoRows) {
		return out, fmt.Errorf("%w: request_id %s", ErrNotFound, requestID)
	}
	if err != nil {
		return out, err
	}
	out.SourceDomain = domains.Domain(srcDomain)
	out.TargetDomain = domains.Domain(tgtDomain)
	if err := json.Unmarshal([]byte(targetJSON), &out.Target); err != nil {
		return out, err
	}
	out.ApprovalID, out.TargetItemID, out.TargetRevisionID = approvalID.String, targetItem.String, targetRevision.String
	if approvedAt.Valid {
		t, err := memorytime.Parse(approvedAt.String)
		if err != nil {
			return out, err
		}
		out.ApprovedAt = &t
	}
	if appliedAt.Valid {
		t, err := memorytime.Parse(appliedAt.String)
		if err != nil {
			return out, err
		}
		out.AppliedAt = &t
	}
	return out, nil
}

func requestReceipt(id, status string, source sourceSnapshot, target Target) RequestReceipt {
	return RequestReceipt{
		RequestID:          id,
		Status:             status,
		SourceDomain:       source.Domain,
		SourceItemID:       source.ItemID,
		SourceVersionToken: source.VersionToken,
		Target: TargetReference{
			Domain:    target.Domain,
			Namespace: target.Namespace,
			Key:       target.Key,
			ItemID:    target.ItemID,
		},
	}
}

func applyReceipt(row requestRow) ApplyReceipt {
	r := ApplyReceipt{
		RequestID:          row.RequestID,
		ApprovalID:         row.ApprovalID,
		Status:             "applied",
		SourceDomain:       row.SourceDomain,
		SourceItemID:       row.SourceItemID,
		SourceVersionToken: row.SourceVersionToken,
		TargetItemID:       row.TargetItemID,
		TargetDomain:       row.Target.Domain,
		TargetNamespace:    row.Target.Namespace,
		TargetKey:          row.Target.Key,
		AppliedAt:          *row.AppliedAt,
	}
	if row.Target.Domain == domains.Domain("workspace") {
		r.TargetVersionToken = row.TargetRevisionID
		r.TargetRevisionID = row.TargetRevisionID
	} else {
		r.TargetRevisionID = row.TargetRevisionID
	}
	return r
}

func stringPointer(value string) *string   { return &value }
func nullText(value string) sql.NullString { return sql.NullString{String: value, Valid: value != ""} }

var errRetry = errors.New("retry promotion transaction")

func retry[T any](ctx context.Context, operation string, fn func() (T, error)) (T, error) {
	var zero T
	var last error
	for attempt := range 8 {
		value, err := fn()
		if !isBusy(err) && !errors.Is(err, errRetry) {
			return value, err
		}
		last = err
		select {
		case <-ctx.Done():
			return zero, ctx.Err()
		case <-time.After(time.Duration(1<<attempt) * time.Millisecond):
		}
	}
	return zero, fmt.Errorf("promotion %s exhausted SQLite retries: %w", operation, last)
}

func isBusy(err error) bool {
	var sqliteErr *sqlite.Error
	return errors.As(err, &sqliteErr) && sqliteErr.Code()&0xff == 5
}
