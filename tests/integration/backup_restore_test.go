package integration

// End-to-end backup/restore coverage.
//
// The suite this replaces asserted one thing: that two plain records and their
// audit events survived a restore into a *fresh, empty* store. Every gap the v1
// format had was invisible to it — the typed-record columns were never set, the
// memory and knowledge tables were never written, embeddings and tags and
// namespace policies were never checked, and restoring into a store that
// already held data was never tried. See CW-20260904-0079 / CW-20260904-0080.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/hollis-labs/tesseract/domains"
	"github.com/hollis-labs/tesseract/internal/contextstore"
	"github.com/hollis-labs/tesseract/internal/memory"
)

const (
	backupNS       = "app/editor/session"
	backupKey      = "summary"
	backupMemNS    = "user/chrispian/memory/notes"
	backupMemKey   = "prefs.output_style"
	backupKnowNS   = "user/chrispian/knowledge/framework"
	backupKnowKey  = "framework.go-providers"
	backupModel    = "test-embed-model"
	backupTypedKey = "typed"
)

// seedSourceStore fills a store with at least one row in every domain a backup
// is supposed to carry, so a missing table shows up as a failed assertion
// rather than as an empty result nobody looks at.
func seedSourceStore(t *testing.T, s *contextstore.Store) (typedRecordID string, token string) {
	t.Helper()
	ctx := context.Background()

	for i := 1; i <= 2; i++ {
		rec, err := s.AppendRecord(ctx, contextstore.AppendInput{
			Namespace: backupNS,
			Key:       backupKey,
			Actor:     "app:editor",
			Payload:   json.RawMessage([]byte(`{"n":` + string(rune('0'+i)) + `}`)),
		})
		if err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
		if err := s.EmitWrite(ctx, rec.Actor, rec.Namespace, rec.Key, rec.Revision, rec.RecordID, nil); err != nil {
			t.Fatalf("record audit: %v", err)
		}
	}

	// Every typed-record column at once: v1 captured none of them.
	typed, err := s.AppendRecord(ctx, contextstore.AppendInput{
		Namespace:      backupNS,
		Key:            backupTypedKey,
		Actor:          "app:editor",
		Payload:        json.RawMessage([]byte(`{"spec":"typed"}`)),
		Metadata:       json.RawMessage([]byte(`{"tags":["alpha","beta"],"origin":"seed"}`)),
		RecordType:     "task/spec",
		Status:         "canonical",
		TTL:            "2099-01-01T00:00:00Z",
		ContentVersion: 7,
		Pointers:       []string{"repo://tesseract/internal", "sha:deadbeef"},
		Provenance:     json.RawMessage([]byte(`{"agent":"seed","run":"1"}`)),
	})
	if err != nil {
		t.Fatalf("append typed: %v", err)
	}

	if err := s.UpsertNamespacePolicy(ctx, contextstore.NamespacePolicyEntry{
		Namespace: backupNS,
		OwnerType: "app",
		OwnerID:   "editor",
		Policy:    map[string]any{"retention": "long"},
	}); err != nil {
		t.Fatalf("upsert namespace policy: %v", err)
	}

	if err := s.UpsertEmbeddingRaw(ctx, typed.RecordID, backupModel, 3, []float32{0.25, 0.5, 0.75}); err != nil {
		t.Fatalf("upsert embedding: %v", err)
	}

	ms := memory.NewStore(s.DB(), nil, "", 0, memory.NoopQueue{})
	if _, err := ms.WriteRevision(ctx, memory.WriteInput{
		Namespace:  backupMemNS,
		MemoryKey:  backupMemKey,
		Author:     memory.Author{AgentID: "seed", AgentVersion: "1.0"},
		Trigger:    memory.TriggerExplicit,
		SessionID:  "manual:backup",
		Origin:     memory.OriginUser,
		Confidence: 0.9,
		Status:     memory.StatusDraft,
		Tags:       []string{"prefs"},
		Payload:    memory.Payload{Summary: "User prefers terse output", Body: "No trailing summaries."},
	}); err != nil {
		t.Fatalf("write memory revision: %v", err)
	}
	if _, err := ms.WriteRevision(ctx, memory.WriteInput{
		Domain:     domains.Knowledge,
		Namespace:  backupKnowNS,
		MemoryKey:  backupKnowKey,
		Author:     memory.Author{AgentID: "indexer", AgentVersion: "1.0"},
		Trigger:    memory.TriggerExplicit,
		SessionID:  "indexer:backup",
		Origin:     memory.OriginUser,
		Confidence: 0.8,
		Payload:    memory.Payload{Summary: "go-providers: multi-provider AI adapter"},
		Facets: memory.Facets{
			Kind:    "package",
			Source:  "filesystem",
			Pointer: &memory.Pointer{Scheme: "file", Locator: "/abs/path"},
		},
	}); err != nil {
		t.Fatalf("write knowledge revision: %v", err)
	}

	tok, _, err := s.IssueAuthToken(ctx, "admin", time.Hour)
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}
	return typed.RecordID, tok
}

// TestBackupRestoreFullDomainParity is the round trip the format exists for:
// export a store carrying context records (with every typed column), tags,
// namespace policies, embeddings, memory and knowledge revisions and auth
// tokens, restore it over a destination that already holds unrelated data of
// its own, and require the destination to end up as an exact copy of the source
// with none of its own rows surviving.
func TestBackupRestoreFullDomainParity(t *testing.T) {
	ctx := context.Background()

	src, err := contextstore.Open(ctx, contextstore.Config{RootDir: t.TempDir()})
	if err != nil {
		t.Fatalf("open src: %v", err)
	}
	defer src.Close()
	typedRecordID, srcToken := seedSourceStore(t, src)

	backupPath := filepath.Join(t.TempDir(), "backup")
	if err := src.ExportBackup(ctx, backupPath); err != nil {
		t.Fatalf("export backup: %v", err)
	}

	info, err := contextstore.InspectBackup(backupPath)
	if err != nil {
		t.Fatalf("verify backup: %v", err)
	}
	if info.FormatVersion != contextstore.BackupFormatVersion {
		t.Fatalf("format_version = %d, want %d", info.FormatVersion, contextstore.BackupFormatVersion)
	}
	if info.SchemaVersion == 0 {
		t.Fatal("backup did not record a schema version")
	}
	assertBackupLayout(t, backupPath)

	// The destination is deliberately not empty. Restoring into a populated
	// store used to leave its memory, tags, policies and pointer rows behind,
	// producing something that was half backup and half destination.
	dst, err := contextstore.Open(ctx, contextstore.Config{RootDir: t.TempDir()})
	if err != nil {
		t.Fatalf("open dst: %v", err)
	}
	defer dst.Close()
	if _, err := dst.AppendRecord(ctx, contextstore.AppendInput{
		Namespace: "app/other/stale",
		Key:       "leftover",
		Actor:     "app:other",
		Payload:   json.RawMessage([]byte(`{"stale":true}`)),
		Metadata:  json.RawMessage([]byte(`{"tags":["stale"]}`)),
	}); err != nil {
		t.Fatalf("seed dst record: %v", err)
	}
	dstToken, _, err := dst.IssueAuthToken(ctx, "dst-admin", time.Hour)
	if err != nil {
		t.Fatalf("issue dst token: %v", err)
	}
	dstMem := memory.NewStore(dst.DB(), nil, "", 0, memory.NoopQueue{})
	if _, err := dstMem.WriteRevision(ctx, memory.WriteInput{
		Namespace:  "user/other/memory/notes",
		MemoryKey:  "stale.key",
		Author:     memory.Author{AgentID: "dst", AgentVersion: "1.0"},
		Trigger:    memory.TriggerExplicit,
		SessionID:  "manual:dst",
		Origin:     memory.OriginUser,
		Confidence: 0.5,
		Payload:    memory.Payload{Summary: "destination-only memory"},
	}); err != nil {
		t.Fatalf("seed dst memory: %v", err)
	}

	if err := dst.RestoreBackup(ctx, backupPath); err != nil {
		t.Fatalf("restore backup: %v", err)
	}

	// ── context records, including every typed column ──────────────────────
	srcHist, err := src.History(ctx, backupNS, backupKey, 0)
	if err != nil {
		t.Fatalf("src history: %v", err)
	}
	dstHist, err := dst.History(ctx, backupNS, backupKey, 0)
	if err != nil {
		t.Fatalf("dst history: %v", err)
	}
	if !reflect.DeepEqual(canonicalizeRecords(t, srcHist), canonicalizeRecords(t, dstHist)) {
		t.Fatalf("history parity mismatch")
	}

	srcTyped, err := src.Head(ctx, backupNS, backupTypedKey)
	if err != nil {
		t.Fatalf("src typed head: %v", err)
	}
	dstTyped, err := dst.Head(ctx, backupNS, backupTypedKey)
	if err != nil {
		t.Fatalf("dst typed head: %v", err)
	}
	if !reflect.DeepEqual(canonicalizeRecords(t, []contextstore.Record{srcTyped}), canonicalizeRecords(t, []contextstore.Record{dstTyped})) {
		t.Fatalf("typed record mismatch:\nsrc=%#v\ndst=%#v", srcTyped, dstTyped)
	}
	if dstTyped.RecordType != "task/spec" || dstTyped.Status != "canonical" || dstTyped.TTL != "2099-01-01T00:00:00Z" ||
		dstTyped.ContentVersion != 7 || len(dstTyped.Pointers) != 2 || len(dstTyped.Provenance) == 0 {
		t.Fatalf("typed columns did not survive the restore: %#v", dstTyped)
	}

	// metadata_json has no accessor on Record, so read the column directly.
	var srcMeta, dstMeta string
	if err := src.DB().QueryRowContext(ctx, `SELECT COALESCE(metadata_json,'') FROM records WHERE record_id = ?`, typedRecordID).Scan(&srcMeta); err != nil {
		t.Fatalf("src metadata: %v", err)
	}
	if err := dst.DB().QueryRowContext(ctx, `SELECT COALESCE(metadata_json,'') FROM records WHERE record_id = ?`, typedRecordID).Scan(&dstMeta); err != nil {
		t.Fatalf("dst metadata: %v", err)
	}
	if srcMeta == "" || srcMeta != dstMeta {
		t.Fatalf("metadata_json mismatch: src=%q dst=%q", srcMeta, dstMeta)
	}

	// ── record_tags ────────────────────────────────────────────────────────
	tagged, err := dst.Select(ctx, contextstore.Selector{Namespaces: []string{backupNS}, TagsAny: []string{"alpha"}, RevisionScope: "all"})
	if err != nil {
		t.Fatalf("dst tag select: %v", err)
	}
	if len(tagged) != 1 || tagged[0].RecordID != typedRecordID {
		t.Fatalf("record_tags did not survive the restore: %#v", tagged)
	}

	// ── namespace_policies ─────────────────────────────────────────────────
	srcPolicies, err := src.ListNamespacePolicies(ctx)
	if err != nil {
		t.Fatalf("src policies: %v", err)
	}
	dstPolicies, err := dst.ListNamespacePolicies(ctx)
	if err != nil {
		t.Fatalf("dst policies: %v", err)
	}
	if !reflect.DeepEqual(srcPolicies, dstPolicies) {
		t.Fatalf("namespace policy mismatch:\nsrc=%#v\ndst=%#v", srcPolicies, dstPolicies)
	}

	// ── embeddings ─────────────────────────────────────────────────────────
	// The old restore deleted every record with foreign keys on, so ON DELETE
	// CASCADE silently took the embeddings with them and nothing put them back.
	srcEmb, _, err := src.ListEmbeddings(ctx, contextstore.EmbeddingFilter{Model: backupModel})
	if err != nil {
		t.Fatalf("src embeddings: %v", err)
	}
	dstEmb, _, err := dst.ListEmbeddings(ctx, contextstore.EmbeddingFilter{Model: backupModel})
	if err != nil {
		t.Fatalf("dst embeddings: %v", err)
	}
	if len(srcEmb) != 1 || len(dstEmb) != 1 {
		t.Fatalf("embedding count: src=%d dst=%d, want 1 each", len(srcEmb), len(dstEmb))
	}
	if !reflect.DeepEqual(srcEmb[0], dstEmb[0]) {
		t.Fatalf("embedding mismatch:\nsrc=%#v\ndst=%#v", srcEmb[0], dstEmb[0])
	}

	// ── memory and knowledge ───────────────────────────────────────────────
	// RestoreBackup replaces the *sql.DB behind Store.DB(), so a memory store
	// built before the restore is bound to a closed handle. Re-fetch.
	dstMem = memory.NewStore(dst.DB(), nil, "", 0, memory.NoopQueue{})
	srcMem := memory.NewStore(src.DB(), nil, "", 0, memory.NoopQueue{})

	srcRev, err := srcMem.GetCurrent(ctx, backupMemNS, backupMemKey)
	if err != nil {
		t.Fatalf("src memory read: %v", err)
	}
	dstRev, err := dstMem.GetCurrent(ctx, backupMemNS, backupMemKey)
	if err != nil {
		t.Fatalf("memory revision did not survive the restore: %v", err)
	}
	if dstRev.RevisionID != srcRev.RevisionID || dstRev.Payload.Summary != srcRev.Payload.Summary {
		t.Fatalf("memory revision mismatch:\nsrc=%#v\ndst=%#v", srcRev, dstRev)
	}

	srcKnow, err := srcMem.GetCurrentInDomain(ctx, domains.Knowledge, backupKnowNS, backupKnowKey)
	if err != nil {
		t.Fatalf("src knowledge read: %v", err)
	}
	dstKnow, err := dstMem.GetCurrentInDomain(ctx, domains.Knowledge, backupKnowNS, backupKnowKey)
	if err != nil {
		t.Fatalf("knowledge revision did not survive the restore: %v", err)
	}
	if dstKnow.RevisionID != srcKnow.RevisionID {
		t.Fatalf("knowledge revision mismatch: src=%s dst=%s", srcKnow.RevisionID, dstKnow.RevisionID)
	}

	// The FTS index travels with the snapshot rather than being rebuilt, so
	// recall must work immediately after a restore.
	hits, err := dstMem.Recall(ctx, memory.RecallInput{
		Namespaces:    []string{backupMemNS},
		RevisionScope: memory.RevisionScopeCurrent,
		Ranking:       memory.RankingRelevance,
		Query:         "terse output",
		Limit:         10,
	})
	if err != nil {
		t.Fatalf("recall after restore: %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("recall returned nothing after restore: the FTS index did not survive")
	}

	// ── audit + tokens ─────────────────────────────────────────────────────
	srcAudit, err := src.ListAuditEvents(ctx, 100)
	if err != nil {
		t.Fatalf("src audit: %v", err)
	}
	dstAudit, err := dst.ListAuditEvents(ctx, 100)
	if err != nil {
		t.Fatalf("dst audit: %v", err)
	}
	if !reflect.DeepEqual(canonicalizeAudit(t, srcAudit), canonicalizeAudit(t, dstAudit)) {
		t.Fatalf("audit parity mismatch:\nsrc=%#v\ndst=%#v", srcAudit, dstAudit)
	}
	if err := dst.ValidateAuthToken(ctx, srcToken); err != nil {
		t.Fatalf("restored token should validate: %v", err)
	}

	// ── nothing of the destination's own survives ──────────────────────────
	if _, err := dst.Head(ctx, "app/other/stale", "leftover"); err == nil {
		t.Fatal("destination's own record survived the restore")
	}
	if err := dst.ValidateAuthToken(ctx, dstToken); err == nil {
		t.Fatal("destination's own auth token survived the restore")
	}
	if _, err := dstMem.GetCurrent(ctx, "user/other/memory/notes", "stale.key"); !errors.Is(err, memory.ErrNotFound) {
		t.Fatalf("destination's own memory survived the restore: %v", err)
	}
	staleTagged, err := dst.Select(ctx, contextstore.Selector{Namespaces: []string{"app/*"}, TagsAny: []string{"stale"}, RevisionScope: "all"})
	if err != nil {
		t.Fatalf("dst stale tag select: %v", err)
	}
	if len(staleTagged) != 0 {
		t.Fatalf("destination's own record_tags survived the restore: %#v", staleTagged)
	}

	// A restored store must be internally consistent without any repair step.
	issues, err := dst.ScanConsistency(ctx)
	if err != nil {
		t.Fatalf("dst consistency scan: %v", err)
	}
	if len(issues) != 0 {
		t.Fatalf("restored store reports consistency issues: %#v", issues)
	}
}

// assertBackupLayout pins the v2 on-disk shape and its permissions: the backup
// embeds auth_tokens.token_hash, which v1 wrote world-readable.
func assertBackupLayout(t *testing.T, dir string) {
	t.Helper()

	di, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat backup dir: %v", err)
	}
	if perm := di.Mode().Perm(); perm != 0o700 {
		t.Errorf("backup directory mode = %o, want 700", perm)
	}

	raw, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var manifest contextstore.BackupManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("parse manifest: %v", err)
	}
	if len(manifest.Omitted) != 0 {
		t.Errorf("manifest declares omissions: %#v", manifest.Omitted)
	}
	if manifest.Checksum == "" {
		t.Error("manifest has no checksum")
	}

	// The manifest's table list is the readable form of the coverage guarantee.
	// These are the tables v1 dropped on the floor.
	tables := map[string]bool{}
	for _, name := range manifest.Tables {
		tables[name] = true
	}
	for _, want := range []string{
		"records", "heads", "audit_events", "auth_tokens", "namespace_policies",
		"record_tags", "embeddings", "memory_state", "memory_revisions",
		"memory_revisions_fts", "pointer_verifications", "schema_version",
	} {
		if !tables[want] {
			t.Errorf("backup manifest does not list table %q", want)
		}
	}

	var sawDB, sawRecord bool
	for _, entry := range manifest.Contents {
		if entry.SHA256 == "" {
			t.Errorf("manifest entry %q has no checksum", entry.Path)
		}
		switch entry.Kind {
		case "db":
			sawDB = true
		case "record":
			sawRecord = true
		}
		fi, err := os.Stat(filepath.Join(dir, filepath.FromSlash(entry.Path)))
		if err != nil {
			t.Fatalf("stat %s: %v", entry.Path, err)
		}
		if perm := fi.Mode().Perm(); perm != 0o600 {
			t.Errorf("backup file %s mode = %o, want 600", entry.Path, perm)
		}
	}
	if !sawDB || !sawRecord {
		t.Errorf("manifest is missing db (%v) or record (%v) entries", sawDB, sawRecord)
	}
}

// TestBackupExportIncludesConfig covers the optional config.yaml the caller can
// hand the exporter; the store cannot find it on its own.
func TestBackupExportIncludesConfig(t *testing.T) {
	ctx := context.Background()
	s, err := contextstore.Open(ctx, contextstore.Config{RootDir: t.TempDir()})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(cfgPath, []byte("embedding:\n  model: test\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	backupPath := filepath.Join(t.TempDir(), "backup")
	if err := s.ExportBackupWithOptions(ctx, backupPath, contextstore.ExportBackupOptions{ConfigPath: cfgPath}); err != nil {
		t.Fatalf("export: %v", err)
	}
	if err := s.VerifyBackup(backupPath); err != nil {
		t.Fatalf("verify: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(backupPath, "config.yaml"))
	if err != nil {
		t.Fatalf("read backed-up config: %v", err)
	}
	if string(got) != "embedding:\n  model: test\n" {
		t.Fatalf("config content = %q", got)
	}
}

// TestBackupVerifyRejectsTampering covers the per-file checksums: mutating any
// byte of the snapshot or of a payload must fail verification, and a tampered
// backup must not be restorable.
func TestBackupVerifyRejectsTampering(t *testing.T) {
	ctx := context.Background()
	src, err := contextstore.Open(ctx, contextstore.Config{RootDir: t.TempDir()})
	if err != nil {
		t.Fatalf("open src: %v", err)
	}
	defer src.Close()
	seedSourceStore(t, src)

	base := filepath.Join(t.TempDir(), "backup")
	if err := src.ExportBackup(ctx, base); err != nil {
		t.Fatalf("export: %v", err)
	}

	cases := []struct {
		name   string
		mutate func(t *testing.T, dir string)
	}{
		{
			name: "payload byte flipped",
			mutate: func(t *testing.T, dir string) {
				path := firstBackupRecordFile(t, dir)
				if err := os.WriteFile(path, []byte(`{"n":9}`), 0o600); err != nil {
					t.Fatalf("tamper payload: %v", err)
				}
			},
		},
		{
			name: "snapshot truncated",
			mutate: func(t *testing.T, dir string) {
				if err := os.Truncate(filepath.Join(dir, "main.db"), 4096); err != nil {
					t.Fatalf("truncate snapshot: %v", err)
				}
			},
		},
		{
			name: "manifest entry removed",
			mutate: func(t *testing.T, dir string) {
				path := filepath.Join(dir, "manifest.json")
				raw, err := os.ReadFile(path)
				if err != nil {
					t.Fatalf("read manifest: %v", err)
				}
				var m map[string]any
				if err := json.Unmarshal(raw, &m); err != nil {
					t.Fatalf("parse manifest: %v", err)
				}
				contents, _ := m["contents"].([]any)
				m["contents"] = contents[:len(contents)-1]
				out, err := json.Marshal(m)
				if err != nil {
					t.Fatalf("marshal manifest: %v", err)
				}
				if err := os.WriteFile(path, out, 0o600); err != nil {
					t.Fatalf("write manifest: %v", err)
				}
			},
		},
		{
			name: "unlisted file added",
			mutate: func(t *testing.T, dir string) {
				if err := os.WriteFile(filepath.Join(dir, "records", "smuggled.json"), []byte(`{}`), 0o600); err != nil {
					t.Fatalf("add file: %v", err)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "tampered")
			copyDirForTest(t, base, dir)
			tc.mutate(t, dir)

			if err := src.VerifyBackup(dir); err == nil {
				t.Fatal("expected verification to reject the tampered backup")
			}

			dst, err := contextstore.Open(ctx, contextstore.Config{RootDir: t.TempDir()})
			if err != nil {
				t.Fatalf("open dst: %v", err)
			}
			defer dst.Close()
			if err := dst.RestoreBackup(ctx, dir); err == nil {
				t.Fatal("expected restore to reject the tampered backup")
			}
		})
	}
}

// TestRestoreLegacyV1Snapshot proves backups taken before format v2 are still
// restorable. The snapshot is built by hand because the exporter no longer
// writes v1.
func TestRestoreLegacyV1Snapshot(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "legacy.json")
	writeLegacyV1Snapshot(t, path)

	dst, err := contextstore.Open(ctx, contextstore.Config{RootDir: t.TempDir()})
	if err != nil {
		t.Fatalf("open dst: %v", err)
	}
	defer dst.Close()

	if err := dst.VerifyBackup(path); err != nil {
		t.Fatalf("verify legacy snapshot: %v", err)
	}
	if err := dst.RestoreBackup(ctx, path); err != nil {
		t.Fatalf("restore legacy snapshot: %v", err)
	}

	// Heads are rebuilt inside the staged database, so a head read works with
	// no repair step. The old restore rebuilt them after the commit, in a
	// separate transaction that a failure could skip entirely.
	head, err := dst.Head(ctx, "app/legacy/session", "summary")
	if err != nil {
		t.Fatalf("head after legacy restore: %v", err)
	}
	if head.Revision != 2 {
		t.Fatalf("head revision = %d, want 2", head.Revision)
	}
	hist, err := dst.History(ctx, "app/legacy/session", "summary", 0)
	if err != nil {
		t.Fatalf("history after legacy restore: %v", err)
	}
	if len(hist) != 2 {
		t.Fatalf("history length = %d, want 2", len(hist))
	}
	events, err := dst.ListAuditEvents(ctx, 10)
	if err != nil {
		t.Fatalf("audit after legacy restore: %v", err)
	}
	if len(events) != 1 || events[0].EventType != "context.write" {
		t.Fatalf("audit events = %#v", events)
	}
	issues, err := dst.ScanConsistency(ctx)
	if err != nil {
		t.Fatalf("consistency scan: %v", err)
	}
	if len(issues) != 0 {
		t.Fatalf("legacy restore left consistency issues: %#v", issues)
	}
}

// TestRestoreLegacyV1RejectsPathTraversal covers the hostile-input half of the
// legacy reader: file_path came straight out of the snapshot and was joined
// onto recordsDir without sanitizing, so `../` wrote outside the store.
func TestRestoreLegacyV1RejectsPathTraversal(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "hostile.json")
	writeLegacyV1SnapshotWithPath(t, path, "../../../escaped.json")

	root := t.TempDir()
	dst, err := contextstore.Open(ctx, contextstore.Config{RootDir: root})
	if err != nil {
		t.Fatalf("open dst: %v", err)
	}
	defer dst.Close()

	if err := dst.VerifyBackup(path); err == nil {
		t.Fatal("expected verification to reject a traversing payload path")
	}
	if err := dst.RestoreBackup(ctx, path); err == nil {
		t.Fatal("expected restore to reject a traversing payload path")
	}
	if _, err := os.Stat(filepath.Join(root, "escaped.json")); err == nil {
		t.Fatal("restore wrote outside the records directory")
	}
}

// ── helpers ─────────────────────────────────────────────────────────────────

func writeLegacyV1Snapshot(t *testing.T, path string) {
	t.Helper()
	writeLegacyV1SnapshotWithPath(t, path, "")
}

// writeLegacyV1SnapshotWithPath emits a v1 snapshot, optionally forcing one
// record's file_path so the traversal guard can be exercised.
func writeLegacyV1SnapshotWithPath(t *testing.T, path, forceFilePath string) {
	t.Helper()

	type v1Record struct {
		RecordID  string          `json:"record_id"`
		Namespace string          `json:"namespace"`
		Key       string          `json:"key"`
		Revision  int64           `json:"revision"`
		Actor     string          `json:"actor"`
		CreatedAt string          `json:"created_at"`
		FilePath  string          `json:"file_path"`
		Payload   json.RawMessage `json:"payload"`
	}
	type v1AuthToken struct {
		TokenID        string `json:"token_id"`
		TokenHash      string `json:"token_hash"`
		Label          string `json:"label"`
		ClientID       string `json:"client_id,omitempty"`
		Scopes         string `json:"scopes,omitempty"`
		NamespaceGlobs string `json:"namespace_globs,omitempty"`
		CreatedAt      string `json:"created_at"`
		ExpiresAt      string `json:"expires_at,omitempty"`
		RevokedAt      string `json:"revoked_at,omitempty"`
	}
	type v1Snapshot struct {
		Version     int                       `json:"version"`
		ExportedAt  string                    `json:"exported_at"`
		Records     []v1Record                `json:"records"`
		AuditEvents []contextstore.AuditEvent `json:"audit_events"`
		AuthTokens  []v1AuthToken             `json:"auth_tokens"`
		Checksum    string                    `json:"checksum"`
	}

	const ns = "app/legacy/session"
	const key = "summary"
	now := time.Now().UTC().Format(time.RFC3339)
	snap := v1Snapshot{
		Version:    1,
		ExportedAt: now,
		Records: []v1Record{
			{RecordID: "rec_1", Namespace: ns, Key: key, Revision: 1, Actor: "app:legacy", CreatedAt: now,
				FilePath: filepath.Join(ns, key, "1.json"), Payload: json.RawMessage(`{"n":1}`)},
			{RecordID: "rec_2", Namespace: ns, Key: key, Revision: 2, Actor: "app:legacy", CreatedAt: now,
				FilePath: filepath.Join(ns, key, "2.json"), Payload: json.RawMessage(`{"n":2}`)},
		},
		AuditEvents: []contextstore.AuditEvent{
			{ID: 1, EventType: "context.write", Actor: "app:legacy", Namespace: ns, Key: key, Revision: 2, RecordID: "rec_2", CreatedAt: now},
		},
		AuthTokens: []v1AuthToken{
			{TokenID: "tok_legacy", TokenHash: "0000000000000000000000000000000000000000000000000000000000000000",
				Label: "legacy", CreatedAt: now},
		},
	}
	if forceFilePath != "" {
		snap.Records[0].FilePath = forceFilePath
	}

	// v1's checksum is sha256 over the snapshot marshalled with an empty
	// Checksum field, which is reproduced here rather than exported.
	raw, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	snap.Checksum = sha256Hex(raw)
	out, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	if err := os.WriteFile(path, out, 0o600); err != nil {
		t.Fatalf("write snapshot: %v", err)
	}
}

func firstBackupRecordFile(t *testing.T, dir string) string {
	t.Helper()
	var found []string
	err := filepath.Walk(filepath.Join(dir, "records"), func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			found = append(found, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk records: %v", err)
	}
	if len(found) == 0 {
		t.Fatal("backup contains no payload files")
	}
	sort.Strings(found)
	return found[0]
}

func copyDirForTest(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o600)
	})
	if err != nil {
		t.Fatalf("copy backup: %v", err)
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

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
