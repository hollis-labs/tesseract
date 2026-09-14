package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/hollis-labs/tesseract/domains"
	"github.com/hollis-labs/tesseract/internal/corpusmigration"
	"github.com/hollis-labs/tesseract/internal/memory"
)

func TestClassifyBacklogCLIDbRequired(t *testing.T) {
	outR, outW, _ := os.Pipe()
	errR, errW, _ := os.Pipe()
	defer outR.Close()
	defer errR.Close()

	code := runClassifyBacklog(context.Background(), []string{}, outW, errW)
	_ = outW.Close()
	_ = errW.Close()

	if code != 1 {
		t.Fatalf("expected exit code 1 when --db omitted, got %d", code)
	}
}

func TestClassifyBacklogCLIExecution(t *testing.T) {
	cs, dbPath := setupTestDB(t)
	defer cs.Close()

	memStore := memory.NewStore(cs.DB(), nil, "", 0, memory.NoopQueue{})

	// Seed records
	_, err := memStore.WriteRevision(context.Background(), memory.WriteInput{
		Domain:      domains.Memory,
		Namespace:   "user/chrispian/memory/decisions",
		Actor:       "user",
		MemoryKey:   "test_decision",
		Summary:     "Decided to use nanite",
		Tags:        []string{"decision", "project:nanite"},
		Author:      memory.Author{AgentID: "test-agent"},
		SessionID:   "session-1",
		Status:      memory.StatusCanonical,
		Trigger:     memory.TriggerManual,
		DerivedFrom: memory.DerivedFromProject,
	})
	if err != nil {
		t.Fatalf("seed decision: %v", err)
	}

	_, err = memStore.WriteRevision(context.Background(), memory.WriteInput{
		Domain:      domains.Memory,
		Namespace:   "user/chrispian/memory/notes",
		Actor:       "user",
		MemoryKey:   "destructive_action_rule",
		Summary:     "Never delete without confirmation",
		Tags:        []string{"safety", "notes"},
		Author:      memory.Author{AgentID: "test-agent"},
		SessionID:   "session-1",
		Status:      memory.StatusCanonical,
		Trigger:     memory.TriggerManual,
		DerivedFrom: memory.DerivedFromProject,
	})
	if err != nil {
		t.Fatalf("seed note: %v", err)
	}

	// 1. Text mode run
	outR, outW, _ := os.Pipe()
	errR, errW, _ := os.Pipe()
	defer errR.Close()

	code := runClassifyBacklog(context.Background(), []string{"-db", dbPath}, outW, errW)
	_ = outW.Close()
	_ = errW.Close()

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(outR)
	_ = outR.Close()

	var errBuf bytes.Buffer
	_, _ = errBuf.ReadFrom(errR)

	if code != 0 {
		t.Fatalf("expected exit code 0, got %d (stderr: %s)", code, errBuf.String())
	}
	outText := buf.String()
	if !bytes.Contains([]byte(outText), []byte("Backlog Memory & Event Classification Report")) {
		t.Errorf("output missing header: %s", outText)
	}
	if !bytes.Contains([]byte(outText), []byte("nanite")) {
		t.Errorf("output missing nanite project count: %s", outText)
	}

	// 2. JSON mode run
	outR2, outW2, _ := os.Pipe()
	errR2, errW2, _ := os.Pipe()
	defer errR2.Close()

	code2 := runClassifyBacklog(context.Background(), []string{"-db", dbPath, "-json"}, outW2, errW2)
	_ = outW2.Close()
	_ = errW2.Close()

	var buf2 bytes.Buffer
	_, _ = buf2.ReadFrom(outR2)
	_ = outR2.Close()

	if code2 != 0 {
		t.Fatalf("expected exit code 0 on json mode, got %d", code2)
	}

	var report corpusmigration.ClassificationReport
	if err := json.Unmarshal(buf2.Bytes(), &report); err != nil {
		t.Fatalf("unmarshal json report: %v", err)
	}
	if report.TotalRecords != 2 {
		t.Errorf("expected 2 records, got %d", report.TotalRecords)
	}
	if report.PerProjectCounts["nanite"] != 1 {
		t.Errorf("expected 1 nanite record, got %d", report.PerProjectCounts["nanite"])
	}
	if report.PerProjectCounts["system"] != 1 {
		t.Errorf("expected 1 system record, got %d", report.PerProjectCounts["system"])
	}
}
