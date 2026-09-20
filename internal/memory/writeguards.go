package memory

import (
	"errors"
	"fmt"
)

// Optimistic-write guards (CW-20260919-0019).
//
// A revisioned write is unconditional by default. A second write to a
// (namespace, key) that already exists becomes the new head without a word, and
// `supersedes` is checked for existence and lineage but never against the
// current head, so two writers working from one base both succeed and the later
// one wins. That is the right default for the writers that exist: a session
// appending to its own notes has nobody to conflict with. It is the wrong one
// for a writer that owns records other writers can also reach, which needs to
// say "only if this key is new" or "only if nobody has written since I read".
//
// Those are the two guards, WriteInput.CreateOnly and
// WriteInput.ExpectedRevisionID. Both are opt-in and default off, so a caller
// that does not ask for one is unchanged — including promotion and corpus
// migration, which never set them. They are checked inside the write's own
// transaction, against the head that transaction sees, so a guard is a
// compare-and-set rather than a read followed by a write.
//
// They mirror what the other two mutation paths already do: workspace refuses a
// create onto a live key (ErrKeyConflict) and a write against a stale
// version_token (ErrVersionConflict), and promotion refuses a target whose
// ExpectedRevisionID is no longer current.

var (
	// ErrKeyConflict is returned when WriteInput.CreateOnly is set and the key
	// already has an item.
	ErrKeyConflict = errors.New("memory key conflict")

	// ErrRevisionConflict is returned when WriteInput.ExpectedRevisionID is set
	// and is not the item's current revision. That includes a key with no item at
	// all: an expectation about a head cannot hold where there is no head.
	ErrRevisionConflict = errors.New("memory revision conflict")
)

// WriteConflict is what a failed guard returns. It wraps ErrKeyConflict or
// ErrRevisionConflict, so errors.Is selects on the kind, and carries what the
// caller collided with, so recovering does not take a second read that could
// itself race another writer.
type WriteConflict struct {
	// Err is ErrKeyConflict or ErrRevisionConflict.
	Err error

	Namespace string
	Key       string

	// ItemID and CurrentRevisionID name the item the write collided with and its
	// head. CurrentRevisionID is what to send as ExpectedRevisionID once the
	// caller has re-read that revision and re-applied its change to it. Both are
	// empty when ExpectedRevisionID named a key that has no item.
	ItemID            string
	CurrentRevisionID string

	// ExpectedRevisionID echoes what the caller sent. Empty for a key conflict.
	ExpectedRevisionID string
}

func (c *WriteConflict) Unwrap() error { return c.Err }

// Error is written for a caller that has one turn to recover from it, so it
// names the guard that fired, the head it lost to, and the way out.
func (c *WriteConflict) Error() string {
	subject := fmt.Sprintf("namespace %q key %q", c.Namespace, c.Key)
	switch {
	case errors.Is(c.Err, ErrKeyConflict):
		return fmt.Sprintf("%s: create_only was set, but %s already has item %s (current revision %s). "+
			"Drop create_only to write a new revision of it, or pass expected_revision_id=%s to write against that head.",
			c.Err, subject, c.ItemID, c.CurrentRevisionID, c.CurrentRevisionID)
	case c.ItemID == "":
		return fmt.Sprintf("%s: expected_revision_id %s was given, but %s has no item to write against. "+
			"Drop expected_revision_id to create it, or set create_only to make sure you are creating it.",
			c.Err, c.ExpectedRevisionID, subject)
	default:
		return fmt.Sprintf("%s: expected_revision_id %s is not the current revision of %s (item %s); the current revision is %s. "+
			"Re-read it, apply your change to it, and retry with expected_revision_id=%s.",
			c.Err, c.ExpectedRevisionID, subject, c.ItemID, c.CurrentRevisionID, c.CurrentRevisionID)
	}
}

// Details is the structured form of the conflict, for surfaces whose error
// envelope has somewhere to put it. Keys are omitted when there is nothing to
// say, so a caller can tell "no item" from "an item whose head moved".
func (c *WriteConflict) Details() map[string]any {
	d := map[string]any{"namespace": c.Namespace, "key": c.Key}
	if c.ItemID != "" {
		d["item_id"] = c.ItemID
		d["current_revision_id"] = c.CurrentRevisionID
	}
	if c.ExpectedRevisionID != "" {
		d["expected_revision_id"] = c.ExpectedRevisionID
	}
	return d
}

// resolvedItem is what the write path learned about the item a write targets,
// read in the transaction that will write it.
type resolvedItem struct {
	ID string
	// Created reports that this write minted the item. Its memory_state row then
	// exists only inside the still-open transaction, which a returned guard error
	// rolls back with everything else.
	Created bool
	// CurrentRevisionID is the item's head before this write. Empty when Created.
	CurrentRevisionID string
}

// checkWriteGuards applies the opt-in guards to the item a write resolved to.
// CreateOnly and ExpectedRevisionID are mutually exclusive, which
// validateWriteInput has already enforced, so at most one of them can fire.
func checkWriteGuards(in WriteInput, item resolvedItem) error {
	if in.CreateOnly && !item.Created {
		return &WriteConflict{
			Err: ErrKeyConflict, Namespace: in.Namespace, Key: in.MemoryKey,
			ItemID: item.ID, CurrentRevisionID: item.CurrentRevisionID,
		}
	}
	if in.ExpectedRevisionID != "" && in.ExpectedRevisionID != item.CurrentRevisionID {
		conflict := &WriteConflict{
			Err: ErrRevisionConflict, Namespace: in.Namespace, Key: in.MemoryKey,
			ExpectedRevisionID: in.ExpectedRevisionID,
		}
		if !item.Created {
			conflict.ItemID, conflict.CurrentRevisionID = item.ID, item.CurrentRevisionID
		}
		return conflict
	}
	return nil
}
