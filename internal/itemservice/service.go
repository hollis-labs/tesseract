// Package itemservice joins Tesseract's revisioned and mutable item stores at
// the public object boundary. Storage remains separate; this package supplies
// typed alternatives without fabricating revisions for workspace items.
package itemservice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/hollis-labs/tesseract/domains"
	"github.com/hollis-labs/tesseract/internal/memory"
	"github.com/hollis-labs/tesseract/internal/workspace"
)

var ErrHistoryUnavailable = errors.New("item history unavailable")

type Service struct {
	Revisions *memory.Store
	Workspace *workspace.Store
}

type Metadata struct {
	ItemID    string     `json:"item_id"`
	Domain    string     `json:"domain"`
	Namespace string     `json:"namespace"`
	Deleted   bool       `json:"deleted"`
	DeletedAt *time.Time `json:"deleted_at,omitempty"`
}

type WorkspacePayload struct {
	Summary        string          `json:"summary"`
	Body           string          `json:"body,omitempty"`
	Data           json.RawMessage `json:"data,omitempty"`
	DataSchemaHash string          `json:"data_schema_hash,omitempty"`
}

// WorkspaceItem is the public current-read shape. Content remains nested under
// payload, matching existing revision reads.
type WorkspaceItem struct {
	ItemID        string             `json:"item_id"`
	Domain        string             `json:"domain"`
	VersionToken  string             `json:"version_token"`
	Namespace     string             `json:"namespace"`
	Key           string             `json:"key,omitempty"`
	WorkstreamID  string             `json:"workstream_id,omitempty"`
	Provenance    *memory.Provenance `json:"provenance,omitempty"`
	Payload       WorkspacePayload   `json:"payload"`
	Tags          []string           `json:"tags,omitempty"`
	ConsumerState json.RawMessage    `json:"consumer_state,omitempty"`
	Author        memory.Author      `json:"author"`
	SessionID     string             `json:"session_id"`
	CreatedAt     time.Time          `json:"created_at"`
	UpdatedAt     time.Time          `json:"updated_at"`
	Activation    float64            `json:"activation"`
	AccessCount   int64              `json:"access_count"`
	LastUsedAt    time.Time          `json:"last_used_at"`
	LastDecayedAt time.Time          `json:"last_decayed_at"`
}

type ReadResult struct {
	Revision *memory.Revision `json:"revision,omitempty"`
	Item     *WorkspaceItem   `json:"item,omitempty"`
}

func PublicWorkspaceItem(item workspace.Item) WorkspaceItem {
	return WorkspaceItem{
		ItemID: item.ItemID, Domain: workspace.Domain, VersionToken: item.VersionToken,
		Namespace: item.Namespace, Key: item.Key, WorkstreamID: item.WorkstreamID, Provenance: item.Provenance,
		Payload: WorkspacePayload{Summary: item.Summary, Body: item.Body, Data: item.Data, DataSchemaHash: item.DataSchemaHash},
		Tags:    item.Tags, ConsumerState: item.ConsumerState, Author: item.Author, SessionID: item.SessionID,
		CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt, Activation: item.Activation,
		AccessCount: item.AccessCount, LastUsedAt: item.LastUsedAt, LastDecayedAt: item.LastDecayedAt,
	}
}

// LookupMetadata resolves identity and authorization scope without loading
// content or recording use.
func (s *Service) LookupMetadata(ctx context.Context, itemID string) (Metadata, error) {
	if s.Revisions != nil {
		state, err := s.Revisions.GetState(ctx, itemID)
		if err == nil {
			return Metadata{ItemID: itemID, Domain: string(state.Domain), Namespace: state.Namespace}, nil
		}
		if !errors.Is(err, memory.ErrNotFound) {
			return Metadata{}, err
		}
	}
	if s.Workspace != nil {
		meta, err := s.Workspace.LookupMetadata(ctx, itemID)
		if err == nil {
			return Metadata{ItemID: meta.ItemID, Domain: meta.Domain, Namespace: meta.Namespace, Deleted: meta.Deleted, DeletedAt: meta.DeletedAt}, nil
		}
		if !errors.Is(err, workspace.ErrNotFound) {
			return Metadata{}, err
		}
	}
	return Metadata{}, fmt.Errorf("%w: item_id %s", memory.ErrNotFound, itemID)
}

// ReadCurrent deliberately reads current content and records use in domains
// that participate in activation.
func (s *Service) ReadCurrent(ctx context.Context, meta Metadata) (ReadResult, error) {
	if meta.Deleted {
		return ReadResult{}, fmt.Errorf("%w: item_id %s", workspace.ErrDeleted, meta.ItemID)
	}
	if meta.Domain == workspace.Domain {
		if s.Workspace == nil {
			return ReadResult{}, fmt.Errorf("workspace store unavailable")
		}
		item, err := s.Workspace.GetCurrentAndUse(ctx, meta.ItemID)
		if err != nil {
			return ReadResult{}, err
		}
		public := PublicWorkspaceItem(item)
		return ReadResult{Item: &public}, nil
	}
	if s.Revisions == nil {
		return ReadResult{}, fmt.Errorf("revision store unavailable")
	}
	var rev memory.Revision
	var err error
	if meta.Domain == string(domains.Event) {
		rev, err = s.Revisions.GetCurrentByItemID(ctx, meta.ItemID)
	} else {
		rev, err = s.Revisions.GetCurrentByItemIDReinforced(ctx, meta.ItemID)
	}
	if err != nil {
		return ReadResult{}, err
	}
	return ReadResult{Revision: &rev}, nil
}

func (s *Service) ReadWorkspaceByKey(ctx context.Context, namespace, key string) (WorkspaceItem, error) {
	if s.Workspace == nil {
		return WorkspaceItem{}, fmt.Errorf("workspace store unavailable")
	}
	item, err := s.Workspace.GetCurrentByKey(ctx, namespace, key)
	if err != nil {
		return WorkspaceItem{}, err
	}
	if markErr := s.Workspace.MarkUsed(ctx, item.ItemID); markErr != nil {
		return WorkspaceItem{}, markErr
	}
	item, err = s.Workspace.GetCurrent(ctx, item.ItemID)
	if err != nil {
		return WorkspaceItem{}, err
	}
	return PublicWorkspaceItem(item), nil
}

func (s *Service) History(ctx context.Context, meta Metadata) ([]memory.Revision, error) {
	if meta.Domain == workspace.Domain {
		return nil, fmt.Errorf("%w: workspace items do not retain revisions", ErrHistoryUnavailable)
	}
	if s.Revisions == nil {
		return nil, fmt.Errorf("revision store unavailable")
	}
	return s.Revisions.GetHistoryByItemID(ctx, meta.ItemID)
}

type TouchResult struct {
	Touched       int      `json:"touched"`
	NotFound      []string `json:"not_found"`
	NotReinforced []string `json:"not_reinforced"`
	Deleted       []string `json:"deleted"`
}

func (s *Service) TouchItems(ctx context.Context, metas []Metadata) (TouchResult, error) {
	result := TouchResult{NotFound: []string{}, NotReinforced: []string{}, Deleted: []string{}}
	if len(metas) == 0 || len(metas) > memory.MaxTouchRevisions {
		return result, fmt.Errorf("%w: item_ids must contain between 1 and %d values", memory.ErrInvalidInput, memory.MaxTouchRevisions)
	}
	seen := map[string]bool{}
	var revisionIDs []string
	for _, meta := range metas {
		if seen[meta.ItemID] {
			continue
		}
		seen[meta.ItemID] = true
		switch {
		case meta.Deleted:
			result.Deleted = append(result.Deleted, meta.ItemID)
		case meta.Domain == workspace.Domain:
			if s.Workspace == nil {
				return result, fmt.Errorf("workspace store unavailable")
			}
			if err := s.Workspace.MarkUsed(ctx, meta.ItemID); err != nil {
				return result, err
			}
			result.Touched++
		case meta.Domain == string(domains.Event):
			result.NotReinforced = append(result.NotReinforced, meta.ItemID)
		default:
			revisionIDs = append(revisionIDs, meta.ItemID)
		}
	}
	if len(revisionIDs) > 0 {
		if s.Revisions == nil {
			return result, fmt.Errorf("revision store unavailable")
		}
		moved, err := s.Revisions.TouchItems(ctx, revisionIDs)
		if err != nil {
			return result, err
		}
		result.Touched += moved.Touched
		result.NotFound = append(result.NotFound, moved.NotFound...)
		result.NotReinforced = append(result.NotReinforced, moved.NotReinforced...)
	}
	return result, nil
}
