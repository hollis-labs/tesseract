package storetemplate

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/hollis-labs/tesseract/internal/contextstore"
)

func TestCopiesKeepIndependentPersistentRecords(t *testing.T) {
	ctx := context.Background()
	template, err := New(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Run("copies", func(t *testing.T) {
		for _, value := range []string{"left", "right"} {
			t.Run(value, func(t *testing.T) {
				t.Parallel()
				cfg := contextstore.Config{RootDir: t.TempDir()}
				store, err := template.Open(ctx, cfg)
				if err != nil {
					t.Fatal(err)
				}
				defer store.Close()
				payload, _ := json.Marshal(value)
				if _, err := store.AppendRecord(ctx, contextstore.AppendInput{
					Namespace: "app/fixture/session", Key: "same-key", Actor: "app:fixture", Payload: payload,
				}); err != nil {
					t.Fatal(err)
				}
				if err := store.Close(); err != nil {
					t.Fatal(err)
				}
				if _, err := template.Open(ctx, cfg); !errors.Is(err, os.ErrExist) {
					t.Fatalf("copying over an existing fixture must be refused: %v", err)
				}
				reopened, err := contextstore.Open(ctx, cfg)
				if err != nil {
					t.Fatal(err)
				}
				defer reopened.Close()
				head, err := reopened.Head(ctx, "app/fixture/session", "same-key")
				if err != nil || string(head.Payload) != string(payload) || head.Revision != 1 {
					t.Fatalf("fixture must retain its own record after reopen: head=%+v err=%v", head, err)
				}
			})
		}
	})
	// Earlier writes must not contaminate the template used by later tests.
	clean, err := template.Open(ctx, contextstore.Config{RootDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer clean.Close()
	if _, err := clean.Head(ctx, "app/fixture/session", "same-key"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("new copy inherited another fixture's record: %v", err)
	}
}
