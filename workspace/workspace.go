// Package workspace exposes Tesseract's mutable workspace types to external Go
// consumers. Storage implementation remains in internal/workspace.
package workspace

import (
	"github.com/hollis-labs/tesseract/domains"
	internal "github.com/hollis-labs/tesseract/internal/workspace"
)

type Store = internal.Store
type Item = internal.Item
type Metadata = internal.Metadata
type CreateInput = internal.CreateInput
type CreateRequest = internal.CreateRequest
type EditInput = internal.EditInput
type DeleteInput = internal.DeleteInput
type MutationReceipt = internal.MutationReceipt
type DeleteReceipt = internal.DeleteReceipt
type ClearField = internal.ClearField
type SearchResult = internal.SearchResult
type RecallInput = internal.RecallInput
type RecallResult = internal.RecallResult

const (
	Domain domains.Domain = internal.Domain

	ClearKey           = internal.ClearKey
	ClearBody          = internal.ClearBody
	ClearData          = internal.ClearData
	ClearTags          = internal.ClearTags
	ClearConsumerState = internal.ClearConsumerState
)

var (
	NewStore               = internal.NewStore
	ErrInvalidInput        = internal.ErrInvalidInput
	ErrNotFound            = internal.ErrNotFound
	ErrDeleted             = internal.ErrDeleted
	ErrVersionConflict     = internal.ErrVersionConflict
	ErrKeyConflict         = internal.ErrKeyConflict
	ErrIdempotencyConflict = internal.ErrIdempotencyConflict
)
