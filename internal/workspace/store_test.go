package workspace_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/tesseract/domains"
	"github.com/hollis-labs/tesseract/internal/contextstore"
	"github.com/hollis-labs/tesseract/internal/memory"
	"github.com/hollis-labs/tesseract/internal/workspace"
)

const testNamespace = "project/tesseract/workspace/drafts"

func newWorkspaceStore(t *testing.T) (*contextstore.Store, *workspace.Store, *time.Time) {
	t.Helper()
	cs, err := contextstore.Open(context.Background(), contextstore.Config{RootDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	now := time.Date(2026, 9, 13, 1, 30, 0, 0, time.UTC)
	return cs, workspace.NewStore(cs.DB(), workspace.WithClock(func() time.Time { return now })), &now
}

func createInput(key string) workspace.CreateInput {
	return workspace.CreateInput{
		Namespace: testNamespace,
		Key:       key,
		Summary:   "workspace draft",
		Body:      "initial body",
		Author:    memory.Author{AgentID: "writer-a", AgentVersion: "1"},
		SessionID: "session-a",
	}
}

func ptr[T any](value T) *T { return &value }

func TestConditionalEditsRejectStaleWritersAndRollbackOnCollision(t *testing.T) {
	cs, first, now := newWorkspaceStore(t)
	second := workspace.NewStore(cs.DB(), workspace.WithClock(func() time.Time { return *now }))
	ctx := context.Background()

	item, err := first.Create(ctx, createInput("draft/one"))
	if err != nil {
		t.Fatal(err)
	}
	originalToken := item.VersionToken
	*now = now.Add(time.Minute)
	winner, err := first.Edit(ctx, workspace.EditInput{
		ItemID: item.ItemID, VersionToken: originalToken, Body: ptr("winner body"),
		Author: memory.Author{AgentID: "writer-a", AgentVersion: "2"}, SessionID: "session-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	if winner.VersionToken == originalToken {
		t.Fatal("accepted edit did not rotate version_token")
	}
	if winner.AccessCount != item.AccessCount+1 || !winner.LastUsedAt.Equal(*now) {
		t.Fatalf("accepted edit did not count as use: before=%#v after=%#v", item, winner)
	}

	_, err = second.Edit(ctx, workspace.EditInput{
		ItemID: item.ItemID, VersionToken: originalToken, Summary: ptr("stale summary"),
		Author: memory.Author{AgentID: "writer-b"}, SessionID: "session-b",
	})
	if !errors.Is(err, workspace.ErrVersionConflict) {
		t.Fatalf("stale edit error = %v, want version conflict", err)
	}
	if _, deleteErr := second.Delete(ctx, workspace.DeleteInput{ItemID: item.ItemID, VersionToken: originalToken}); !errors.Is(deleteErr, workspace.ErrVersionConflict) {
		t.Fatalf("stale delete error = %v, want version conflict", deleteErr)
	}

	if _, createErr := first.Create(ctx, createInput("occupied")); createErr != nil {
		t.Fatal(createErr)
	}
	losingBody := "must roll back"
	occupied := "occupied"
	_, err = first.Edit(ctx, workspace.EditInput{
		ItemID: item.ItemID, VersionToken: winner.VersionToken, Key: &occupied, Body: &losingBody,
		Author: memory.Author{AgentID: "writer-a"}, SessionID: "session-a",
	})
	if !errors.Is(err, workspace.ErrKeyConflict) {
		t.Fatalf("colliding rename error = %v, want key conflict", err)
	}
	after, err := first.GetCurrent(ctx, item.ItemID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Body != winner.Body || after.Key != winner.Key || after.VersionToken != winner.VersionToken {
		t.Fatalf("failed multi-field edit partially applied: before=%#v after=%#v", winner, after)
	}

	equal, err := first.Edit(ctx, workspace.EditInput{
		ItemID: item.ItemID, VersionToken: after.VersionToken, Summary: ptr(after.Summary),
		Author: memory.Author{AgentID: "writer-a"}, SessionID: "session-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	if equal.VersionToken == after.VersionToken {
		t.Fatal("accepted equal-value edit did not rotate version_token")
	}
}

func TestConcurrentWritersWithOneTokenCommitOnce(t *testing.T) {
	cs, first, now := newWorkspaceStore(t)
	second := workspace.NewStore(cs.DB(), workspace.WithClock(func() time.Time { return *now }))
	ctx := context.Background()
	item, err := first.Create(ctx, createInput("concurrent"))
	if err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	results := make(chan error, 2)
	for i, store := range []*workspace.Store{first, second} {
		go func(writer int, store *workspace.Store) {
			<-start
			body := fmt.Sprintf("writer-%d", writer)
			_, editErr := store.Edit(ctx, workspace.EditInput{
				ItemID: item.ItemID, VersionToken: item.VersionToken, Body: &body,
				Author: memory.Author{AgentID: body}, SessionID: body,
			})
			results <- editErr
		}(i, store)
	}
	close(start)
	var succeeded, conflicted int
	for range 2 {
		err := <-results
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, workspace.ErrVersionConflict):
			conflicted++
		default:
			t.Fatalf("concurrent edit returned unexpected error: %v", err)
		}
	}
	if succeeded != 1 || conflicted != 1 {
		t.Fatalf("concurrent results: succeeded=%d conflicted=%d, want one each", succeeded, conflicted)
	}
}

func TestConcurrentDeleteRetriesReturnTheSameTombstoneReceipt(t *testing.T) {
	cs, store, _ := newWorkspaceStore(t)
	ctx := context.Background()
	item, err := store.Create(ctx, createInput("delete-race"))
	if err != nil {
		t.Fatal(err)
	}

	entered := make(chan struct{})
	release := make(chan struct{})
	var blockOnce sync.Once
	blockedStore := workspace.NewStore(cs.DB(), workspace.WithClock(func() time.Time {
		blockOnce.Do(func() {
			close(entered)
			<-release
		})
		return time.Date(2026, 9, 13, 2, 0, 0, 0, time.UTC)
	}))
	type result struct {
		receipt workspace.DeleteReceipt
		err     error
	}
	firstDone := make(chan result, 1)
	go func() {
		receipt, deleteErr := blockedStore.DeleteWithReceipt(ctx, workspace.DeleteInput{
			ItemID: item.ItemID, VersionToken: item.VersionToken,
		})
		firstDone <- result{receipt: receipt, err: deleteErr}
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("first delete did not reach its transaction")
	}
	second, err := store.DeleteWithReceipt(ctx, workspace.DeleteInput{
		ItemID: item.ItemID, VersionToken: item.VersionToken,
	})
	close(release)
	if err != nil {
		t.Fatal(err)
	}
	first := <-firstDone
	if first.err != nil {
		t.Fatalf("concurrent retry: %v", first.err)
	}
	if first.receipt.ItemID != second.ItemID || first.receipt.DeletedAt == nil || second.DeletedAt == nil || !first.receipt.DeletedAt.Equal(*second.DeletedAt) {
		t.Fatalf("receipts differ: first=%+v second=%+v", first.receipt, second)
	}
	staleItem, err := store.Create(ctx, createInput("stale-delete"))
	if err != nil {
		t.Fatal(err)
	}
	updatedSummary := "new token"
	if _, err := store.Edit(ctx, workspace.EditInput{
		ItemID: staleItem.ItemID, VersionToken: staleItem.VersionToken, Summary: &updatedSummary,
		Author: memory.Author{AgentID: "writer-a"}, SessionID: "session-a",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DeleteWithReceipt(ctx, workspace.DeleteInput{ItemID: staleItem.ItemID, VersionToken: staleItem.VersionToken}); !errors.Is(err, workspace.ErrVersionConflict) {
		t.Fatalf("stale live delete error=%v, want version conflict", err)
	}
	if _, err := store.DeleteWithReceipt(ctx, workspace.DeleteInput{ItemID: "01MISSING", VersionToken: item.VersionToken}); !errors.Is(err, workspace.ErrNotFound) {
		t.Fatalf("unknown delete error=%v, want not found", err)
	}
}

func TestExactFreeFormKeysAndKeylessItems(t *testing.T) {
	_, store, _ := newWorkspaceStore(t)
	ctx := context.Background()
	keys := []string{" padded/key ", "padded/key", " ", "folder/with spaces/name"}
	items := make(map[string]workspace.Item)
	for _, key := range keys {
		item, err := store.Create(ctx, createInput(key))
		if err != nil {
			t.Fatalf("create key %q: %v", key, err)
		}
		items[key] = item
	}
	for _, key := range keys {
		got, err := store.GetCurrentByKey(ctx, testNamespace, key)
		if err != nil {
			t.Fatalf("get key %q: %v", key, err)
		}
		if got.ItemID != items[key].ItemID || got.Key != key {
			t.Fatalf("key %q resolved %#v, want item %q", key, got, items[key].ItemID)
		}
	}
	if _, err := store.Create(ctx, createInput(" padded/key ")); !errors.Is(err, workspace.ErrKeyConflict) {
		t.Fatalf("duplicate exact key error = %v, want key conflict", err)
	}

	firstKeyless, err := store.Create(ctx, createInput(""))
	if err != nil {
		t.Fatal(err)
	}
	secondKeyless, err := store.Create(ctx, createInput(""))
	if err != nil {
		t.Fatal(err)
	}
	if firstKeyless.ItemID == secondKeyless.ItemID {
		t.Fatal("two keyless creates reused item identity")
	}
	if _, err := store.GetCurrentByKey(ctx, testNamespace, ""); !errors.Is(err, workspace.ErrInvalidInput) {
		t.Fatalf("empty key lookup error = %v, want invalid input", err)
	}
}

func TestEditOmissionClearingAndDataHashCoupling(t *testing.T) {
	_, store, _ := newWorkspaceStore(t)
	ctx := context.Background()
	hash := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	data := json.RawMessage(`{"v":1}`)
	state := json.RawMessage(`{"phase":"draft"}`)
	in := createInput("clear-me")
	in.Data, in.DataSchemaHash = data, hash
	in.Tags = []string{"one", "two"}
	in.ConsumerState = state
	item, err := store.Create(ctx, in)
	if err != nil {
		t.Fatal(err)
	}

	changedSummary := "new summary"
	omission, err := store.Edit(ctx, workspace.EditInput{
		ItemID: item.ItemID, VersionToken: item.VersionToken, Summary: &changedSummary,
		Author: memory.Author{AgentID: "editor"}, SessionID: "session-edit",
	})
	if err != nil {
		t.Fatal(err)
	}
	if omission.Body != item.Body || string(omission.Data) != string(item.Data) ||
		omission.DataSchemaHash != hash || string(omission.ConsumerState) != string(state) || len(omission.Tags) != 2 {
		t.Fatalf("omitted fields were not preserved: %#v", omission)
	}

	replacement := json.RawMessage(`{"v":2}`)
	replaced, err := store.Edit(ctx, workspace.EditInput{
		ItemID: item.ItemID, VersionToken: omission.VersionToken, Data: &replacement,
		Author: memory.Author{AgentID: "editor"}, SessionID: "session-edit",
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(replaced.Data) != string(replacement) || replaced.DataSchemaHash != "" {
		t.Fatalf("data replacement retained stale schema claim: %#v", replaced)
	}
	if _, hashErr := store.Edit(ctx, workspace.EditInput{
		ItemID: item.ItemID, VersionToken: replaced.VersionToken, DataSchemaHash: &hash,
		Author: memory.Author{AgentID: "editor"}, SessionID: "session-edit",
	}); !errors.Is(hashErr, workspace.ErrInvalidInput) {
		t.Fatalf("hash-only edit error = %v, want invalid input", hashErr)
	}

	cleared, err := store.Edit(ctx, workspace.EditInput{
		ItemID: item.ItemID, VersionToken: replaced.VersionToken,
		ClearFields: []workspace.ClearField{
			workspace.ClearKey, workspace.ClearBody, workspace.ClearData,
			workspace.ClearTags, workspace.ClearConsumerState,
		},
		Author: memory.Author{AgentID: "editor"}, SessionID: "session-edit",
	})
	if err != nil {
		t.Fatal(err)
	}
	if cleared.Key != "" || cleared.Body != "" || len(cleared.Data) != 0 ||
		cleared.DataSchemaHash != "" || cleared.Tags != nil || len(cleared.ConsumerState) != 0 {
		t.Fatalf("explicit clears did not remove optional fields: %#v", cleared)
	}
	if cleared.Summary != changedSummary {
		t.Fatalf("clear changed required summary: %q", cleared.Summary)
	}
	if _, err := store.Edit(ctx, workspace.EditInput{
		ItemID: item.ItemID, VersionToken: cleared.VersionToken,
		Author: memory.Author{AgentID: "editor"}, SessionID: "session-edit",
	}); !errors.Is(err, workspace.ErrInvalidInput) {
		t.Fatalf("no-op edit error = %v, want invalid input", err)
	}
}

func TestDeletePurgesContentAndIndexAndRecreateMintsIdentity(t *testing.T) {
	cs, store, now := newWorkspaceStore(t)
	ctx := context.Background()
	in := createInput("reusable/key")
	in.Summary = "ephemeral vanishedtoken"
	in.Body = "body vanishedbody"
	item, err := store.Create(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if hits, searchErr := store.SearchLexical(ctx, testNamespace, "vanishedtoken", 0); searchErr != nil || len(hits) != 1 {
		t.Fatalf("pre-delete lexical hits=%#v err=%v", hits, searchErr)
	}
	replacementData := json.RawMessage(`{"state":"replacement"}`)
	edited, err := store.Edit(ctx, workspace.EditInput{
		ItemID: item.ItemID, VersionToken: item.VersionToken,
		Summary: ptr("replacementtoken"), Body: ptr("replacementbody"), Data: &replacementData,
		Author: memory.Author{AgentID: "editor"}, SessionID: "session-edit",
	})
	if err != nil {
		t.Fatal(err)
	}
	if hits, searchErr := store.SearchLexical(ctx, testNamespace, "vanishedtoken", 0); searchErr != nil || len(hits) != 0 {
		t.Fatalf("overwritten content remains searchable: hits=%#v err=%v", hits, searchErr)
	}
	if hits, searchErr := store.SearchLexical(ctx, testNamespace, "replacementtoken", 0); searchErr != nil || len(hits) != 1 {
		t.Fatalf("replacement content is not searchable: hits=%#v err=%v", hits, searchErr)
	}
	var liveVersions int
	if queryErr := cs.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM workspace_items WHERE item_id = ?`, item.ItemID).Scan(&liveVersions); queryErr != nil {
		t.Fatal(queryErr)
	}
	if liveVersions != 1 {
		t.Fatalf("edit retained %d live versions, want one current row", liveVersions)
	}

	*now = now.Add(time.Hour)
	tombstone, err := store.Delete(ctx, workspace.DeleteInput{ItemID: item.ItemID, VersionToken: edited.VersionToken})
	if err != nil {
		t.Fatal(err)
	}
	if !tombstone.Deleted || tombstone.ItemID != item.ItemID || tombstone.Namespace != testNamespace || tombstone.DeletedAt == nil {
		t.Fatalf("tombstone metadata = %#v", tombstone)
	}
	if _, readErr := store.GetCurrent(ctx, item.ItemID); !errors.Is(readErr, workspace.ErrDeleted) {
		t.Fatalf("deleted current error = %v, want deleted", readErr)
	}
	if _, deleteErr := store.Delete(ctx, workspace.DeleteInput{ItemID: item.ItemID, VersionToken: "anything"}); !errors.Is(deleteErr, workspace.ErrDeleted) {
		t.Fatalf("repeated delete error = %v, want deleted", deleteErr)
	}
	if _, deleteErr := store.Delete(ctx, workspace.DeleteInput{ItemID: "never-existed", VersionToken: "anything"}); !errors.Is(deleteErr, workspace.ErrNotFound) {
		t.Fatalf("unknown delete error = %v, want not found", deleteErr)
	}
	if hits, searchErr := store.SearchLexical(ctx, testNamespace, "replacementtoken", 0); searchErr != nil || len(hits) != 0 {
		t.Fatalf("deleted content remains searchable: hits=%#v err=%v", hits, searchErr)
	}
	var liveRows int
	if queryErr := cs.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM workspace_items WHERE item_id = ?`, item.ItemID).Scan(&liveRows); queryErr != nil {
		t.Fatal(queryErr)
	}
	if liveRows != 0 {
		t.Fatal("deleted content row remains live")
	}

	recreated, err := store.Create(ctx, createInput("reusable/key"))
	if err != nil {
		t.Fatal(err)
	}
	if recreated.ItemID == item.ItemID {
		t.Fatal("recreated key revived tombstoned identity")
	}
	metadata, err := store.LookupMetadata(ctx, item.ItemID)
	if err != nil || !metadata.Deleted {
		t.Fatalf("old identity did not remain tombstoned: %#v err=%v", metadata, err)
	}
}

func TestPlainReadsAndSearchDoNotCountUse(t *testing.T) {
	_, store, now := newWorkspaceStore(t)
	ctx := context.Background()
	item, err := store.Create(ctx, createInput("use-test"))
	if err != nil {
		t.Fatal(err)
	}
	*now = now.Add(time.Hour)
	if _, readErr := store.GetCurrent(ctx, item.ItemID); readErr != nil {
		t.Fatal(readErr)
	}
	if _, readErr := store.GetCurrentByKey(ctx, testNamespace, "use-test"); readErr != nil {
		t.Fatal(readErr)
	}
	if _, searchErr := store.SearchLexical(ctx, testNamespace, "workspace", 0); searchErr != nil {
		t.Fatal(searchErr)
	}
	plain, err := store.GetCurrent(ctx, item.ItemID)
	if err != nil {
		t.Fatal(err)
	}
	if plain.AccessCount != 0 || !plain.LastUsedAt.Equal(item.LastUsedAt) || plain.VersionToken != item.VersionToken {
		t.Fatalf("plain reads changed use or token: before=%#v after=%#v", item, plain)
	}

	used, err := store.GetCurrentAndUse(ctx, item.ItemID)
	if err != nil {
		t.Fatal(err)
	}
	if used.AccessCount != 1 || !used.LastUsedAt.Equal(*now) || used.VersionToken != item.VersionToken {
		t.Fatalf("deliberate read bookkeeping = %#v", used)
	}
	*now = now.Add(time.Hour)
	if useErr := store.MarkUsed(ctx, item.ItemID); useErr != nil {
		t.Fatal(useErr)
	}
	after, err := store.GetCurrent(ctx, item.ItemID)
	if err != nil {
		t.Fatal(err)
	}
	if after.AccessCount != 2 || !after.LastUsedAt.Equal(*now) || after.VersionToken != item.VersionToken {
		t.Fatalf("explicit use bookkeeping = %#v", after)
	}
}

type recordingQueue struct {
	mu   sync.Mutex
	jobs []memory.Job
}

func (q *recordingQueue) Enqueue(_ context.Context, job memory.Job) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.jobs = append(q.jobs, job)
	return nil
}

func (q *recordingQueue) count() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.jobs)
}

func TestWorkspaceMutationsDoNotTouchRevisionStoreOrEmbeddingQueue(t *testing.T) {
	cs, store, _ := newWorkspaceStore(t)
	ctx := context.Background()
	queue := &recordingQueue{}
	ms := memory.NewStore(cs.DB(), nil, "", 0, queue)
	revision, err := ms.WriteRevision(ctx, memory.WriteInput{
		Domain: domains.Memory, Namespace: "project/tesseract/memory/notes", MemoryKey: "neighbor.item",
		Summary: "neighbor", Author: memory.Author{AgentID: "test"}, SessionID: "session-neighbor",
		Trigger: memory.TriggerManual, DerivedFrom: memory.DerivedFromProject,
	})
	if err != nil {
		t.Fatal(err)
	}
	jobsBefore := queue.count()
	var revisionsBefore int
	if queryErr := cs.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM memory_revisions`).Scan(&revisionsBefore); queryErr != nil {
		t.Fatal(queryErr)
	}

	item, err := store.Create(ctx, createInput("isolated"))
	if err != nil {
		t.Fatal(err)
	}
	edited, err := store.Edit(ctx, workspace.EditInput{
		ItemID: item.ItemID, VersionToken: item.VersionToken, Body: ptr("changed"),
		Author: memory.Author{AgentID: "editor"}, SessionID: "session-edit",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Delete(ctx, workspace.DeleteInput{ItemID: item.ItemID, VersionToken: edited.VersionToken}); err != nil {
		t.Fatal(err)
	}

	var revisionsAfter, workspaceRevisions int
	if err := cs.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM memory_revisions`).Scan(&revisionsAfter); err != nil {
		t.Fatal(err)
	}
	if err := cs.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM memory_revisions WHERE domain = 'workspace'`).Scan(&workspaceRevisions); err != nil {
		t.Fatal(err)
	}
	if revisionsAfter != revisionsBefore || workspaceRevisions != 0 || queue.count() != jobsBefore {
		t.Fatalf("workspace escaped its store: revisions %d->%d workspace=%d jobs %d->%d",
			revisionsBefore, revisionsAfter, workspaceRevisions, jobsBefore, queue.count())
	}
	if got, err := ms.GetRevisionByID(ctx, revision.RevisionID); err != nil || got.RevisionID != revision.RevisionID {
		t.Fatalf("neighbor revision changed or disappeared: %#v err=%v", got, err)
	}
}

func TestWorkspaceItemIDSkipsRevisionedAndTombstonedIdentities(t *testing.T) {
	cs, _, _ := newWorkspaceStore(t)
	ctx := context.Background()
	ms := memory.NewStore(cs.DB(), nil, "", 0, memory.NoopQueue{})
	revision, err := ms.WriteRevision(ctx, memory.WriteInput{
		Domain: domains.Memory, Namespace: "project/tesseract/memory/notes", MemoryKey: "identity.neighbor",
		Summary: "neighbor", Author: memory.Author{AgentID: "test"}, SessionID: "session-neighbor",
		Trigger: memory.TriggerManual, DerivedFrom: memory.DerivedFromProject,
	})
	if err != nil {
		t.Fatal(err)
	}
	ids := sequenceIDs(revision.ItemID, revision.RevisionID, "workspace-item", "workspace-token", "new-token")
	store := workspace.NewStore(cs.DB(), workspace.WithIDGenerator(ids))
	item, err := store.Create(ctx, createInput("identity"))
	if err != nil {
		t.Fatal(err)
	}
	if item.ItemID != "workspace-item" {
		t.Fatalf("item_id = %q, did not skip revisioned identities", item.ItemID)
	}
	if _, deleteErr := store.Delete(ctx, workspace.DeleteInput{ItemID: item.ItemID, VersionToken: item.VersionToken}); deleteErr != nil {
		t.Fatal(deleteErr)
	}
	recreate := workspace.NewStore(cs.DB(), workspace.WithIDGenerator(sequenceIDs(item.ItemID, "replacement-item", "replacement-token")))
	replacement, err := recreate.Create(ctx, createInput("identity"))
	if err != nil {
		t.Fatal(err)
	}
	if replacement.ItemID != "replacement-item" {
		t.Fatalf("replacement item_id = %q, did not skip tombstone", replacement.ItemID)
	}
	if _, err := cs.DB().ExecContext(ctx, `
		INSERT INTO memory_state (memory_id, namespace, memory_key, domain)
		VALUES (?, 'project/tesseract/memory/notes', 'identity.reverse_collision', 'memory')`, replacement.ItemID); err == nil {
		t.Fatal("revisioned state reused a live workspace item_id")
	}
	if _, err := cs.DB().ExecContext(ctx, `
		INSERT INTO memory_state (memory_id, namespace, memory_key, domain)
		VALUES (?, 'project/tesseract/memory/notes', 'identity.tombstone_collision', 'memory')`, item.ItemID); err == nil {
		t.Fatal("revisioned state reused a tombstoned workspace item_id")
	}
}

func sequenceIDs(values ...string) func() string {
	index := 0
	return func() string {
		if index < len(values) {
			value := values[index]
			index++
			return value
		}
		value := fmt.Sprintf("generated-%d", index)
		index++
		return value
	}
}
