// Package storetemplate provides isolated test stores without repeating the
// full schema migration for every fixture. It is only imported by tests.
package storetemplate

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/hollis-labs/tesseract/internal/contextstore"
)

// Template holds an immutable, fully migrated empty database. Each Open writes
// a separate copy; tests never share a database connection or mutable state.
type Template struct {
	db []byte
}

// New runs the real migrations once. Call it from TestMain, before tests run.
func New(ctx context.Context) (*Template, error) {
	root, err := os.MkdirTemp("", "tesseract-test-template-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(root)
	path := filepath.Join(root, "template.db")
	store, err := contextstore.Open(ctx, contextstore.Config{RootDir: root, DBPath: path})
	if err != nil {
		return nil, err
	}
	// Closing the last SQLite connection checkpoints WAL data into the file.
	if err := store.Close(); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return &Template{db: data}, nil
}

// Open seeds a new fixture, then uses the normal store open path. The caller
// owns Close. Existing databases are refused: tests exercising reopen or
// schema migration behavior should continue to call contextstore.Open directly.
func (t *Template) Open(ctx context.Context, cfg contextstore.Config) (*contextstore.Store, error) {
	if cfg.RootDir == "" {
		return nil, fmt.Errorf("storetemplate: fixture RootDir is required")
	}
	if cfg.DBPath == "" {
		cfg.DBPath = filepath.Join(cfg.RootDir, "data", "index", "context.db")
	}
	if err := os.MkdirAll(filepath.Dir(cfg.DBPath), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(cfg.DBPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, err
	}
	_, writeErr := f.Write(t.db)
	closeErr := f.Close()
	if writeErr != nil {
		return nil, writeErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	return contextstore.Open(ctx, cfg)
}
