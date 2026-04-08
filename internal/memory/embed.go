package memory

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
)

// ErrEmbedderUnavailable is returned when an embedding operation is attempted
// but the store was constructed without an Embedder.
var ErrEmbedderUnavailable = errors.New("embedder unavailable")

// EmbedRevision loads the revision identified by revisionID, extracts
// embeddable text from its payload, calls the configured embedder, and writes
// the resulting vector back to the revision's embedding columns.
func (s *Store) EmbedRevision(ctx context.Context, revisionID, model string) error {
	if s.embedder == nil {
		return ErrEmbedderUnavailable
	}

	rev, err := s.GetRevisionByID(ctx, revisionID)
	if err != nil {
		return err
	}

	text := revisionEmbedText(rev)
	if text == "" {
		return fmt.Errorf("revision %s has no embeddable text", revisionID)
	}

	result, err := s.embedder.Embed(ctx, text, model)
	if err != nil {
		return err
	}

	blob := float32ToBlob(result.Embedding)
	_, err = s.db.ExecContext(ctx,
		`UPDATE memory_revisions SET embedding_model = ?, embedding_vector = ? WHERE revision_id = ?`,
		model, blob, revisionID,
	)
	return err
}

// revisionEmbedText concatenates the revision's summary and body into a single
// string suitable for embedding. If only summary is present the trailing
// newline is omitted.
func revisionEmbedText(rev Revision) string {
	parts := make([]string, 0, 2)
	if rev.Payload.Summary != "" {
		parts = append(parts, rev.Payload.Summary)
	}
	if rev.Payload.Body != "" {
		parts = append(parts, rev.Payload.Body)
	}
	return strings.Join(parts, "\n")
}

// float32ToBlob encodes a float32 slice as a little-endian byte blob.
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

// blobToFloat32 decodes a little-endian byte blob into a float32 slice.
// Returns nil for nil or empty input.
func blobToFloat32(b []byte) []float32 {
	if len(b) == 0 {
		return nil
	}
	n := len(b) / 4
	v := make([]float32, n)
	for i := 0; i < n; i++ {
		bits := uint32(b[i*4]) | uint32(b[i*4+1])<<8 | uint32(b[i*4+2])<<16 | uint32(b[i*4+3])<<24
		v[i] = math.Float32frombits(bits)
	}
	return v
}
