package index

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenCreatesSchema(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "volon.db")

	ctx := context.Background()
	store, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()

	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("db not created: %v", err)
	}
}

func TestUpsertAndList(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(context.Background(), filepath.Join(dir, "volon.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()

	row := TaskRow{
		ID:        "TASK-1",
		Title:     "hello",
		Status:    "todo",
		Priority:  "A",
		Tags:      "cli,go",
		Project:   "volon",
		Context:   "dev",
		CreatedAt: "2026-02-24",
		UpdatedAt: "2026-02-24",
		FilePath:  ".agentrc/tasks/TASK-1.md",
	}
	if err := store.UpsertTask(context.Background(), row); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	list, err := store.ListTasks(context.Background(), Filters{Statuses: []string{"todo"}})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 || list[0].ID != "TASK-1" {
		t.Fatalf("unexpected list result: %+v", list)
	}
}

func TestReplaceAll(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(context.Background(), filepath.Join(dir, "volon.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()

	rows := []TaskRow{
		{
			ID:        "TASK-1",
			Title:     "one",
			Status:    "todo",
			CreatedAt: "2026-02-24",
			UpdatedAt: "2026-02-24",
			FilePath:  ".agentrc/tasks/TASK-1.md",
		},
		{
			ID:        "TASK-2",
			Title:     "two",
			Status:    "doing",
			CreatedAt: "2026-02-24",
			UpdatedAt: "2026-02-24",
			FilePath:  ".agentrc/tasks/TASK-2.md",
		},
	}

	if err := store.ReplaceAll(context.Background(), rows); err != nil {
		t.Fatalf("replace: %v", err)
	}

	list, err := store.ListTasks(context.Background(), Filters{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(list))
	}
}
