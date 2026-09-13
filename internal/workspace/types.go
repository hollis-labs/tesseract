// Package workspace stores Tesseract's mutable working artifacts.
//
// Unlike memory, knowledge and event, workspace has one live value and no
// retained content revisions. Deletes move only identity and authorization
// metadata to an indefinite tombstone.
package workspace

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/hollis-labs/tesseract/internal/memory"
)

const Domain = "workspace"

var (
	ErrInvalidInput    = errors.New("invalid workspace input")
	ErrNotFound        = errors.New("workspace item not found")
	ErrDeleted         = errors.New("workspace item deleted")
	ErrVersionConflict = errors.New("workspace version conflict")
	ErrKeyConflict     = errors.New("workspace key conflict")
)

// Item is the one current authored state of a live workspace object.
type Item struct {
	ItemID         string          `json:"item_id"`
	Domain         string          `json:"domain"`
	VersionToken   string          `json:"version_token"`
	Namespace      string          `json:"namespace"`
	Key            string          `json:"key,omitempty"`
	Summary        string          `json:"summary"`
	Body           string          `json:"body,omitempty"`
	Data           json.RawMessage `json:"data,omitempty"`
	DataSchemaHash string          `json:"data_schema_hash,omitempty"`
	Tags           []string        `json:"tags,omitempty"`
	ConsumerState  json.RawMessage `json:"consumer_state,omitempty"`
	Author         memory.Author   `json:"author"`
	SessionID      string          `json:"session_id"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
	Activation     float64         `json:"activation"`
	AccessCount    int64           `json:"access_count"`
	LastUsedAt     time.Time       `json:"last_used_at"`
	LastDecayedAt  time.Time       `json:"last_decayed_at"`
}

// Metadata is safe to resolve before namespace authorization. Tombstones carry
// exactly these stored facts: item identity, domain, namespace and deletion
// time. Live rows answer the same seam with Deleted=false.
type Metadata struct {
	ItemID    string     `json:"item_id"`
	Domain    string     `json:"domain"`
	Namespace string     `json:"namespace"`
	Deleted   bool       `json:"deleted"`
	DeletedAt *time.Time `json:"deleted_at,omitempty"`
}

type CreateInput struct {
	Namespace      string
	Key            string
	Summary        string
	Body           string
	Data           json.RawMessage
	DataSchemaHash string
	Tags           []string
	ConsumerState  json.RawMessage
	Author         memory.Author
	SessionID      string
}

// ClearField names an optional field that an edit removes. Summary, namespace,
// item identity and version identity are deliberately absent.
type ClearField string

const (
	ClearKey           ClearField = "key"
	ClearBody          ClearField = "body"
	ClearData          ClearField = "data"
	ClearTags          ClearField = "tags"
	ClearConsumerState ClearField = "consumer_state"
)

// EditInput is a conditional partial mutation. A nil pointer means omitted;
// ClearFields is the only way to remove an optional value.
type EditInput struct {
	ItemID         string
	VersionToken   string
	Key            *string
	Summary        *string
	Body           *string
	Data           *json.RawMessage
	DataSchemaHash *string
	Tags           *[]string
	ConsumerState  *json.RawMessage
	ClearFields    []ClearField
	Author         memory.Author
	SessionID      string
}

type DeleteInput struct {
	ItemID       string
	VersionToken string
}

// SearchResult is metadata returned from the workspace-only lexical index.
// Search does not count as deliberate use.
type SearchResult struct {
	ItemID    string    `json:"item_id"`
	Namespace string    `json:"namespace"`
	Key       string    `json:"key,omitempty"`
	Summary   string    `json:"summary"`
	UpdatedAt time.Time `json:"updated_at"`
	Score     float64   `json:"score"`
}
