package memory_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/hollis-labs/tesseract/domains"
	"github.com/hollis-labs/tesseract/internal/memory"
)

const hyphenKey = "user-preferences"

// TestReadAndWriteAgreeOnAnInvalidMemoryKey is the asymmetry CW-20260514-0022
// names: the write path rejected a hyphenated key with a validation error
// while every read answered the same key with a bare "not found", so one
// mistake produced two diagnoses and only one of them said what was wrong.
func TestReadAndWriteAgreeOnAnInvalidMemoryKey(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	const ns = "user/chrispian/memory/notes"

	writeErr := func() error {
		_, err := ms.WriteRevision(ctx, sampleInput(hyphenKey))
		return err
	}()
	if !errors.Is(writeErr, memory.ErrInvalidKey) {
		t.Fatalf("write of %q: err = %v, want ErrInvalidKey", hyphenKey, writeErr)
	}

	reads := map[string]func() error{
		"GetCurrent": func() error {
			_, err := ms.GetCurrent(ctx, ns, hyphenKey)
			return err
		},
		"GetHistory": func() error {
			_, err := ms.GetHistory(ctx, ns, hyphenKey)
			return err
		},
		"GetCurrentInDomain": func() error {
			_, err := ms.GetCurrentInDomain(ctx, domains.Memory, ns, hyphenKey)
			return err
		},
		"GetHistoryInDomain": func() error {
			_, err := ms.GetHistoryInDomain(ctx, domains.Memory, ns, hyphenKey)
			return err
		},
	}
	for name, read := range reads {
		t.Run(name, func(t *testing.T) {
			err := read()
			if err == nil {
				t.Fatal("expected an error")
			}
			// Same diagnosis as the write path...
			if !errors.Is(err, memory.ErrInvalidKey) {
				t.Errorf("read did not report the key policy: %v", err)
			}
			// ...without changing the answer to the read question, which
			// transports map to 404.
			if !errors.Is(err, memory.ErrNotFound) {
				t.Errorf("read stopped reporting ErrNotFound: %v", err)
			}
			if !strings.Contains(err.Error(), `did you mean "user_preferences"?`) {
				t.Errorf("read error does not suggest the valid form: %v", err)
			}
		})
	}
}

// TestValidKeyMissKeepsAPlainNotFound: the diagnosis is for keys that could
// never have been written, not for memories that simply do not exist.
func TestValidKeyMissKeepsAPlainNotFound(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()

	_, err := ms.GetCurrent(context.Background(), "user/chrispian/memory/notes", "nothing.here")
	if !errors.Is(err, memory.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if errors.Is(err, memory.ErrInvalidKey) {
		t.Errorf("a valid key that is simply absent was reported as invalid: %v", err)
	}
}

// TestKnowledgeReadsAreNotHeldToTheMemoryKeyRule guards the reason the
// diagnosis is attached on the miss rather than before the lookup: knowledge
// keys legitimately carry hyphens (write.go applies ValidateKey only for
// domains.Memory), so a knowledge row at a hyphenated key must still resolve,
// and a knowledge miss must not be explained with a memory-key rule that does
// not apply to it.
func TestKnowledgeReadsAreNotHeldToTheMemoryKeyRule(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	const ns = "user/chrispian/knowledge/framework"
	const knowledgeKey = "framework.go-providers"

	written, err := ms.WriteRevision(ctx, memory.WriteInput{
		Domain:     domains.Knowledge,
		Namespace:  ns,
		MemoryKey:  knowledgeKey,
		Author:     memory.Author{AgentID: "indexer", AgentVersion: "1.0"},
		Trigger:    memory.TriggerExplicit,
		SessionID:  "indexer:01HX",
		Origin:     memory.OriginUser,
		Confidence: 0.9,
		Payload:    memory.Payload{Summary: "go-providers: multi-provider AI adapter"},
		Facets: memory.Facets{
			Kind:    "package",
			Source:  "filesystem",
			Pointer: &memory.Pointer{Scheme: "file", Locator: "/abs/path"},
		},
	})
	if err != nil {
		t.Fatalf("knowledge write with a hyphenated key: %v", err)
	}

	got, err := ms.GetCurrentInDomain(ctx, domains.Knowledge, ns, knowledgeKey)
	if err != nil {
		t.Fatalf("knowledge read of a hyphenated key: %v", err)
	}
	if got.RevisionID != written.RevisionID {
		t.Errorf("RevisionID = %q, want %q", got.RevisionID, written.RevisionID)
	}

	_, err = ms.GetCurrentInDomain(ctx, domains.Knowledge, ns, "no-such-key")
	if !errors.Is(err, memory.ErrNotFound) {
		t.Fatalf("knowledge miss: err = %v, want ErrNotFound", err)
	}
	if errors.Is(err, memory.ErrInvalidKey) {
		t.Errorf("knowledge miss was explained with the memory-key rule: %v", err)
	}
}
