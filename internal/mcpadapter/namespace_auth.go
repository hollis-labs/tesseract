package mcpadapter

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/hollis-labs/tesseract/internal/contextstore"
	"github.com/hollis-labs/tesseract/internal/memory"
	"github.com/hollis-labs/tesseract/internal/nsglob"
)

// namespaceDecision applies the runtime's mode to a fine-grained decision.
// Legacy adapters enforce; the runtime records would-deny in permissive mode.
func (a *Adapter) namespaceDecision(ctx context.Context, allowed bool) bool {
	if decision, ok := a.Auth.(interface {
		NamespaceDecision(context.Context, bool) bool
	}); ok {
		return decision.NamespaceDecision(ctx, allowed)
	}
	return allowed
}

func (a *Adapter) namespaceAllowed(ctx context.Context, globs []string, namespace string) bool {
	return a.namespaceDecision(ctx, nsglob.Permitted(globs, namespace))
}

func (a *Adapter) namespaceVisible(ctx context.Context, namespace string) bool {
	claims, ok := a.loadClaims(ctx)
	if !ok {
		// The legacy surface permits anonymous reads. An authenticated surface
		// must never interpret lost claims as an unrestricted namespace grant.
		return a.Auth == nil || a.namespaceDecision(ctx, false)
	}
	return a.namespaceAllowed(ctx, claims.NamespaceGlobs, namespace)
}

func (a *Adapter) requireNamespace(ctx context.Context, namespace string) any {
	if namespace != "" && !a.namespaceVisible(ctx, namespace) {
		return toolError(codeNamespaceNotPermitted, "token namespace globs do not permit this namespace")
	}
	return nil
}

// Metadata resolves an ID's namespace before loading/reinforcing its content.
// Lookups return the same not-found result for missing and denied IDs. Touch
// retains its existing unknown-ID batching behavior through ignoreMissing.
func (a *Adapter) requireRevisionNamespace(ctx context.Context, id string, ignoreMissing bool) any {
	store := a.revisionStore()
	if store == nil {
		return revisionStoreUnavailable()
	}
	meta, err := store.LookupRevisionMetadata(ctx, id)
	if errors.Is(err, memory.ErrNotFound) && ignoreMissing {
		return nil
	}
	if errors.Is(err, memory.ErrNotFound) || err == nil && !a.namespaceVisible(ctx, meta.Namespace) {
		return toolErrorWithDetails(codeNotFound, fmt.Sprintf("%v: revision_id %s", memory.ErrNotFound, id), memory.NotFoundDetails("revision_id", id, time.Now()))
	}
	if err != nil {
		return toolError(codeInternalError, "cannot authorize revision namespace")
	}
	return nil
}

func (a *Adapter) requireRecordNamespace(ctx context.Context, id string) any {
	var namespace string
	err := a.Store.DB().QueryRowContext(ctx, "SELECT namespace FROM records WHERE record_id = ?", id).Scan(&namespace)
	if errors.Is(err, sql.ErrNoRows) || err == nil && !a.namespaceVisible(ctx, namespace) {
		return toolError(codeNotFound, "record not found")
	}
	if err != nil {
		return toolError(codeInternalError, "cannot authorize record namespace")
	}
	return nil
}

func (a *Adapter) filterEmbeddings(ctx context.Context, embeddings []contextstore.EmbeddingRow, records []contextstore.Record) ([]contextstore.EmbeddingRow, []contextstore.Record) {
	permit := a.namespacePermit(ctx)
	outE := make([]contextstore.EmbeddingRow, 0, len(embeddings))
	outR := make([]contextstore.Record, 0, len(records))
	for i, rec := range records {
		if permit == nil || permit(rec.Namespace) {
			outE = append(outE, embeddings[i])
			outR = append(outR, rec)
		}
	}
	return outE, outR
}

// Resolve claims before opening a store cursor. The predicate must not query
// the token store while the same SQLite connection is streaming results.
func (a *Adapter) namespacePermit(ctx context.Context) func(string) bool {
	claims, ok := a.loadClaims(ctx)
	if !ok {
		if a.Auth == nil {
			return nil
		}
		return func(string) bool { return a.namespaceDecision(ctx, false) }
	}
	return func(ns string) bool { return a.namespaceAllowed(ctx, claims.NamespaceGlobs, ns) }
}

func (a *Adapter) selectRecords(ctx context.Context, sel contextstore.Selector) ([]contextstore.Record, error) {
	sel.Permit = a.namespacePermit(ctx)
	return a.Store.Select(ctx, sel)
}
