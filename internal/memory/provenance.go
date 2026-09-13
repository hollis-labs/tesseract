package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

const (
	// WorkstreamIDMaxBytes bounds the opaque correlation identifier while
	// preserving its exact bytes. Tesseract does not interpret its shape.
	WorkstreamIDMaxBytes = 256
	// ProvenanceSessionIDMaxBytes bounds the receiver-supplied session value.
	ProvenanceSessionIDMaxBytes = 256
)

// WriteContext is the normalized, bounded receipt captured from a transport.
// VerificationUnverified is deliberate: the receiver records the envelope it
// was handed and makes no authentication claim about its contents.
type WriteContext struct {
	Issuer       string `json:"issuer"`
	Verification string `json:"verification"`
	SessionID    string `json:"session_id"`
	WorkstreamID string `json:"workstream_id,omitempty"`
}

// Provenance groups receiver-owned facts separately from item association.
type Provenance struct {
	WriteContext *WriteContext `json:"write_context,omitempty"`
}

type writeContextKey struct{}

// ContextWithWriteContext is an internal integration seam for a receiver. It
// is intentionally not re-exported from Tesseract's public memory package.
func ContextWithWriteContext(ctx context.Context, value WriteContext) context.Context {
	return context.WithValue(ctx, writeContextKey{}, value)
}

func WriteContextFromContext(ctx context.Context) *WriteContext {
	value, ok := ctx.Value(writeContextKey{}).(WriteContext)
	if !ok {
		return nil
	}
	cloned := value
	return &cloned
}

func EncodeWriteContext(ctx context.Context) (any, *Provenance, error) {
	value := WriteContextFromContext(ctx)
	if value == nil {
		return nil, nil, nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, nil, fmt.Errorf("encode write context: %w", err)
	}
	return string(raw), &Provenance{WriteContext: value}, nil
}

func DecodeWriteContext(raw string) (*Provenance, error) {
	if raw == "" {
		return nil, nil
	}
	var value WriteContext
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return nil, fmt.Errorf("decode write context: %w", err)
	}
	return &Provenance{WriteContext: &value}, nil
}

// ValidateWorkstreamID accepts an opaque nonempty value without normalizing
// it. Empty is reserved for an explicit clear and is handled by the caller.
func ValidateWorkstreamID(value string) error {
	if value == "" {
		return nil
	}
	if len(value) > WorkstreamIDMaxBytes {
		return fmt.Errorf("workstream_id is %d bytes, over the %d byte ceiling", len(value), WorkstreamIDMaxBytes)
	}
	if strings.TrimSpace(value) != value {
		return fmt.Errorf("workstream_id must not have leading or trailing whitespace")
	}
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("workstream_id must not be whitespace-only")
	}
	return nil
}

// ValidateWorkstreamFilter validates an explicitly supplied association
// selector. Unlike a write, an empty string cannot mean "clear": accepting it
// would silently turn a supplied filter into an unfiltered read.
func ValidateWorkstreamFilter(value string) error {
	if value == "" {
		return fmt.Errorf("workstream_id filter must be a non-empty string")
	}
	return ValidateWorkstreamID(value)
}
