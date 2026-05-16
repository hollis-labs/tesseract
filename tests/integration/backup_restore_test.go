package integration

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/hollis-labs/tesseract/internal/contextstore"
)

func TestBackupRestoreParity(t *testing.T) {
	srcRoot := t.TempDir()
	src, err := contextstore.Open(context.Background(), contextstore.Config{RootDir: srcRoot})
	if err != nil {
		t.Fatalf("open src: %v", err)
	}
	defer src.Close()

	for i := 1; i <= 2; i++ {
		rec, err := src.AppendRecord(context.Background(), contextstore.AppendInput{
			Namespace: "app/editor/session",
			Key:       "summary",
			Actor:     "app:editor",
			Payload:   json.RawMessage([]byte(`{"n":` + string(rune('0'+i)) + `}`)),
		})
		if err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
		if err := src.EmitWrite(context.Background(), rec.Actor, rec.Namespace, rec.Key, rec.Revision, rec.RecordID, nil); err != nil {
			t.Fatalf("record audit: %v", err)
		}
	}
	token, _, err := src.IssueAuthToken(context.Background(), "admin", time.Hour)
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}

	backupPath := filepath.Join(t.TempDir(), "backup.json")
	if err := src.ExportBackup(context.Background(), backupPath); err != nil {
		t.Fatalf("export backup: %v", err)
	}
	if err := src.VerifyBackup(backupPath); err != nil {
		t.Fatalf("verify backup: %v", err)
	}

	dstRoot := t.TempDir()
	dst, err := contextstore.Open(context.Background(), contextstore.Config{RootDir: dstRoot})
	if err != nil {
		t.Fatalf("open dst: %v", err)
	}
	defer dst.Close()
	if err := dst.RestoreBackup(context.Background(), backupPath); err != nil {
		t.Fatalf("restore backup: %v", err)
	}

	srcHist, err := src.History(context.Background(), "app/editor/session", "summary", 0)
	if err != nil {
		t.Fatalf("src history: %v", err)
	}
	dstHist, err := dst.History(context.Background(), "app/editor/session", "summary", 0)
	if err != nil {
		t.Fatalf("dst history: %v", err)
	}
	if !reflect.DeepEqual(canonicalizeRecords(t, srcHist), canonicalizeRecords(t, dstHist)) {
		t.Fatalf("history parity mismatch")
	}

	srcAudit, err := src.ListAuditEvents(context.Background(), 100)
	if err != nil {
		t.Fatalf("src audit: %v", err)
	}
	dstAudit, err := dst.ListAuditEvents(context.Background(), 100)
	if err != nil {
		t.Fatalf("dst audit: %v", err)
	}
	// Backup uses json.MarshalIndent which re-pretty-prints embedded
	// RawMessage metadata, so byte-level DeepEqual would flag whitespace
	// differences only. Compare semantic JSON values via canonicalization.
	if !reflect.DeepEqual(canonicalizeAudit(t, srcAudit), canonicalizeAudit(t, dstAudit)) {
		t.Fatalf("audit parity mismatch:\nsrc=%#v\ndst=%#v", srcAudit, dstAudit)
	}

	if err := dst.ValidateAuthToken(context.Background(), token); err != nil {
		t.Fatalf("restored token should validate: %v", err)
	}

	// Tamper with backup and ensure verification fails.
	tamperedPath := filepath.Join(t.TempDir(), "tampered.json")
	raw, err := os.ReadFile(backupPath)
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	var snap map[string]any
	if err := json.Unmarshal(raw, &snap); err != nil {
		t.Fatalf("unmarshal backup: %v", err)
	}
	snap["version"] = float64(999)
	rawTampered, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("marshal tampered: %v", err)
	}
	if err := os.WriteFile(tamperedPath, rawTampered, 0o644); err != nil {
		t.Fatalf("write tampered: %v", err)
	}
	if err := dst.VerifyBackup(tamperedPath); err == nil {
		t.Fatalf("expected tampered backup verification failure")
	}
}

func canonicalizeAudit(t *testing.T, in []contextstore.AuditEvent) []contextstore.AuditEvent {
	t.Helper()
	out := make([]contextstore.AuditEvent, 0, len(in))
	for _, ev := range in {
		cloned := ev
		if len(ev.Metadata) > 0 {
			var v any
			if err := json.Unmarshal(ev.Metadata, &v); err != nil {
				t.Fatalf("unmarshal metadata: %v", err)
			}
			b, err := json.Marshal(v)
			if err != nil {
				t.Fatalf("marshal metadata: %v", err)
			}
			cloned.Metadata = b
		}
		out = append(out, cloned)
	}
	return out
}

func canonicalizeRecords(t *testing.T, in []contextstore.Record) []contextstore.Record {
	t.Helper()
	out := make([]contextstore.Record, 0, len(in))
	for _, rec := range in {
		cloned := rec
		var payload any
		if err := json.Unmarshal(rec.Payload, &payload); err != nil {
			t.Fatalf("unmarshal payload: %v", err)
		}
		b, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal payload: %v", err)
		}
		cloned.Payload = b
		out = append(out, cloned)
	}
	return out
}
