package workspacepromotion

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/hollis-labs/tesseract/domains"
	"github.com/hollis-labs/tesseract/internal/memory"
)

var (
	ErrInvalidInput      = errors.New("invalid workspace promotion input")
	ErrNotFound          = errors.New("workspace promotion not found")
	ErrSourceNotFound    = errors.New("workspace promotion source not found")
	ErrSourceDeleted     = errors.New("workspace promotion source deleted")
	ErrSourceStale       = errors.New("workspace promotion source is stale")
	ErrTargetStale       = errors.New("workspace promotion target is stale")
	ErrTargetKeyOccupied = errors.New("workspace promotion target key is occupied")
	ErrNotApproved       = errors.New("workspace promotion is not approved")
)

// Target is the normalized destination metadata. WorkstreamID is always
// non-nil after Request: nil at the public boundary means apply the frozen
// inheritance rule, while a pointer to empty explicitly clears association.
type Target struct {
	Domain             domains.Domain     `json:"domain"`
	Namespace          string             `json:"namespace"`
	Key                string             `json:"key,omitempty"`
	ItemID             string             `json:"item_id,omitempty"`
	ExpectedRevisionID string             `json:"expected_revision_id,omitempty"`
	Author             memory.Author      `json:"author"`
	SessionID          string             `json:"session_id"`
	Tags               []string           `json:"tags,omitempty"`
	ConsumerState      json.RawMessage    `json:"consumer_state,omitempty"`
	Confidence         float64            `json:"confidence,omitempty"`
	TTLSeconds         int64              `json:"ttl_seconds,omitempty"`
	DataSchemaHash     string             `json:"data_schema_hash,omitempty"`
	WorkstreamID       *string            `json:"workstream_id"`
	Status             memory.Status      `json:"status,omitempty"`
	Trigger            memory.Trigger     `json:"trigger,omitempty"`
	DerivedFrom        memory.DerivedFrom `json:"derived_from,omitempty"`
	Kind               string             `json:"kind,omitempty"`
	Source             string             `json:"source,omitempty"`
	Pointer            *memory.Pointer    `json:"pointer,omitempty"`
}

type RequestInput struct {
	SourceItemID       string `json:"source_item_id"`
	SourceVersionToken string `json:"source_version_token"`
	Actor              string `json:"actor"`
	Reason             string `json:"reason,omitempty"`
	Target             Target `json:"target"`
}

type ApproveInput struct {
	RequestID string `json:"request_id"`
	Actor     string `json:"actor"`
	Notes     string `json:"notes,omitempty"`
}

type ApplyInput struct {
	RequestID string `json:"request_id"`
	Actor     string `json:"actor"`
}

type RequestReceipt struct {
	RequestID          string          `json:"request_id"`
	Status             string          `json:"status"`
	SourceItemID       string          `json:"source_item_id"`
	SourceVersionToken string          `json:"source_version_token"`
	Target             TargetReference `json:"target"`
}

type TargetReference struct {
	Domain    domains.Domain `json:"domain"`
	Namespace string         `json:"namespace"`
	Key       string         `json:"key,omitempty"`
	ItemID    string         `json:"item_id,omitempty"`
}

type ApprovalReceipt struct {
	RequestID  string    `json:"request_id"`
	ApprovalID string    `json:"approval_id"`
	Status     string    `json:"status"`
	ApprovedAt time.Time `json:"approved_at"`
}

type ApplyReceipt struct {
	RequestID          string         `json:"request_id"`
	ApprovalID         string         `json:"approval_id"`
	Status             string         `json:"status"`
	SourceItemID       string         `json:"source_item_id"`
	SourceVersionToken string         `json:"source_version_token"`
	TargetItemID       string         `json:"target_item_id"`
	TargetRevisionID   string         `json:"target_revision_id"`
	TargetDomain       domains.Domain `json:"target_domain"`
	TargetNamespace    string         `json:"target_namespace"`
	TargetKey          string         `json:"target_key,omitempty"`
	AppliedAt          time.Time      `json:"applied_at"`
}

// Metadata contains only retained identity and authorization fields.
type Metadata struct {
	RequestID       string
	Status          string
	SourceNamespace string
	TargetDomain    domains.Domain
	TargetNamespace string
}
