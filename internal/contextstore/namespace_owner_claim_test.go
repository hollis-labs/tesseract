package contextstore

import (
	"context"
	"fmt"
	"testing"
)

// Separate Store instances ensure correctness comes from the database, rather
// than an in-process lock. The loser must read the winner in the same transaction.
func TestNamespaceOwnerIfUnsetConcurrent(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	first, err := Open(ctx, Config{RootDir: root})
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := Open(ctx, Config{RootDir: root})
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	for i := 0; i < 20; i++ {
		ns := fmt.Sprintf("project/claim-%d", i)
		if i%2 == 1 {
			if err := first.EnsureNamespaceRegistered(ctx, ns); err != nil {
				t.Fatal(err)
			}
		}
		type outcome struct {
			entry   NamespacePolicyEntry
			changed bool
			err     error
		}
		results := make(chan outcome, 2)
		start := make(chan struct{})
		for j, store := range []*Store{first, second} {
			go func(s *Store, owner string) {
				<-start
				entry, changed, err := s.SetNamespaceOwnerIfUnset(ctx, NamespacePolicyEntry{Namespace: ns, OwnerType: "project", OwnerID: owner})
				results <- outcome{entry, changed, err}
			}(store, fmt.Sprintf("owner-%d", j))
		}
		close(start)
		a, b := <-results, <-results
		if a.err != nil || b.err != nil {
			t.Fatalf("claim errors: %v, %v", a.err, b.err)
		}
		if a.changed == b.changed {
			t.Fatalf("expected one winner: %+v %+v", a, b)
		}
		if a.entry.OwnerID != b.entry.OwnerID || !a.entry.OwnerExplicit() || !b.entry.OwnerExplicit() {
			t.Fatalf("owners disagree: %+v %+v", a, b)
		}
		actual, err := first.GetNamespacePolicy(ctx, ns)
		if err != nil || actual.OwnerID != a.entry.OwnerID {
			t.Fatalf("persisted owner: %+v, %v", actual, err)
		}
	}
}
