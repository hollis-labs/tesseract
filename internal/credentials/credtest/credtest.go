// Package credtest gives other packages' tests a real go-secrets resolver
// backed by a throwaway file store, so they never import go-secrets themselves
// (internal/credentials stays Tesseract's only seam to it).
package credtest

import (
	"context"
	"strings"
	"testing"

	secrets "github.com/hollis-labs/go-secrets"
	"github.com/hollis-labs/tesseract/internal/credentials"
)

// Store is a resolver over a temporary file:// store plus a way to seed it.
type Store struct {
	credentials.Resolver
	t *testing.T
	r *secrets.Resolver
}

// New returns an empty temporary store.
func New(t *testing.T) *Store {
	t.Helper()
	r := secrets.New(secrets.WithStateDir(t.TempDir()))
	return &Store{Resolver: r, t: t, r: r}
}

// Set writes value behind ref (a file://<ns>/<key> reference).
func (s *Store) Set(ref, value string) {
	s.t.Helper()
	if err := s.r.Set(context.Background(), ref, strings.NewReader(value)); err != nil {
		s.t.Fatalf("seed %s: %v", ref, err)
	}
}
