package contextcli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/hollis-labs/tesseract/internal/contextstore"
)

// errTokenSink is the only sink failure the CLI reports. It names no path
// component and no OS error, so a refusal never discloses what exists.
var errTokenSink = errors.New("private output file must be new, owner-only and in an owner-only parent directory")

// Sink states reported in metadata. Only "delivered" means the token is in the
// file. "emptied" is claimed only after the file was truncated, synced and read
// back as size zero. Anything else keeps the file for owner reconciliation.
const (
	sinkDelivered = "delivered"
	sinkEmptied   = "emptied"
	sinkMayHold   = "may_contain_credential"
)

// File operations are variables so tests can inject each failure the contract
// distinguishes. Production values are the real operations.
var (
	tokenSinkEUID = os.Geteuid

	tokenSinkWrite = func(f *os.File, data string) error {
		n, err := io.WriteString(f, data)
		if err == nil && n != len(data) {
			err = io.ErrShortWrite
		}
		return err
	}
	tokenSinkSync     = func(f *os.File) error { return f.Sync() }
	tokenSinkDirSync  = func(dir *os.File) error { return dir.Sync() }
	tokenSinkTruncate = func(f *os.File) error { return f.Truncate(0) }
	tokenSinkClose    = func(f *os.File) error { return f.Close() }

	tokenOutputIsTerminal = writerIsTerminal
)

// tokenSink is the exclusive private file a newly created token is delivered
// to. Validation and creation are bound to ONE opened directory (an os.Root,
// which is dirfd-relative): the parent is opened once, its identity and
// ownership are checked on that open handle, and the file is created and later
// re-opened relative to it, so a replaced parent or ancestor symlink cannot
// redirect any later step. It is opened BEFORE the store commit, so every
// create, permission, existing-path and symlink failure is a refusal that
// mints nothing. This does not isolate from other processes of the same user,
// who can already read the store and the sink.
type tokenSink struct {
	file *os.File
	root *os.Root
	base string
	path string
	info os.FileInfo
}

// release closes the sink without touching the file. It is used when the store
// outcome is unknown: the empty file stays so a repeat run on the same path
// refuses before minting anything.
func (s *tokenSink) release() {
	if s.file != nil {
		_ = s.file.Close()
		s.file = nil
	}
	if s.root != nil {
		_ = s.root.Close()
		s.root = nil
	}
}

// deliver writes the token and makes it durable: write, file sync, directory
// sync, close, in that order, so the descriptor is still open when a failure
// needs cleanup. It returns the sink state; the error is non-nil unless the
// state is sinkDelivered.
func (s *tokenSink) deliver(token string) (string, error) {
	defer func() {
		if s.root != nil {
			_ = s.root.Close()
			s.root = nil
		}
	}()
	f := s.file
	err := tokenSinkWrite(f, token+"\n")
	if err == nil {
		err = tokenSinkSync(f)
	}
	if err == nil {
		err = s.syncDirectory()
	}
	if err != nil {
		return s.cleanup(f), errTokenSink
	}
	closeErr := tokenSinkClose(f)
	s.file = nil
	if closeErr != nil {
		// The descriptor is gone; clean up through the same directory handle.
		return s.cleanup(nil), errTokenSink
	}
	return sinkDelivered, nil
}

func (s *tokenSink) syncDirectory() error {
	dir, err := s.root.Open(".")
	if err != nil {
		return err
	}
	err = tokenSinkDirSync(dir)
	if closeErr := dir.Close(); err == nil {
		err = closeErr
	}
	return err
}

// cleanup tries to leave an empty file and reports what it can prove. With a
// live descriptor it uses it; otherwise it re-opens the name relative to the
// pinned directory and truncates only if that is still the inode it created.
func (s *tokenSink) cleanup(f *os.File) string {
	if f == nil {
		var err error
		f, err = s.root.OpenFile(s.base, os.O_WRONLY, 0)
		if err != nil {
			return sinkMayHold
		}
		if info, err := f.Stat(); err != nil || !os.SameFile(info, s.info) {
			_ = f.Close()
			return sinkMayHold
		}
	}
	state := sinkEmptied
	if tokenSinkTruncate(f) != nil || tokenSinkSync(f) != nil {
		state = sinkMayHold
	} else if info, err := f.Stat(); err != nil || info.Size() != 0 {
		state = sinkMayHold
	}
	_ = f.Close()
	s.file = nil
	return state
}

func containsAdminScope(scopes []string) bool {
	for _, s := range scopes {
		if strings.EqualFold(strings.TrimSpace(s), "admin") {
			return true
		}
	}
	return false
}

// adminNamespaceGlobsOK reports whether every glob is an explicit, finite,
// literal-rooted shape: at least two path segments, the first of which carries
// no wildcard or escape. It refuses "*", an empty glob, and any glob whose root
// segment is itself a pattern. It does not prove that no namespace matches; the
// owner chooses a tree that does not exist, and the admin gate ignores globs.
func adminNamespaceGlobsOK(globs []string) bool {
	if len(globs) == 0 {
		return false
	}
	for _, g := range globs {
		g = strings.TrimSpace(g)
		root, rest, ok := strings.Cut(g, "/")
		if !ok || root == "" || rest == "" || strings.ContainsAny(root, `*?[]\`) {
			return false
		}
	}
	return true
}

// tokenCreateSinkMetadata is everything a sink-mode create may print. It has no
// token field by construction. SecretDelivered is true only when the token was
// written, synced and closed; false means "not confirmed", never "revoked".
type tokenCreateSinkMetadata struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	ClientID        string   `json:"client_id"`
	Scopes          []string `json:"scopes"`
	NamespaceGlobs  []string `json:"namespace_globs"`
	CreatedAt       string   `json:"created_at"`
	ExpiresAt       string   `json:"expires_at"`
	SecretFile      string   `json:"secret_file"`
	SecretDelivered bool     `json:"secret_delivered"`
	SinkState       string   `json:"sink_state"`
}

func (c *CLI) writeTokenCreateSinkMetadata(m tokenCreateSinkMetadata, output string) error {
	if output == "json" {
		return json.NewEncoder(c.Stdout).Encode(m)
	}
	w := tabwriter.NewWriter(c.Stdout, 2, 4, 2, ' ', 0)
	_, _ = fmt.Fprintf(w, "  ID:\t%s\n", m.ID)
	_, _ = fmt.Fprintf(w, "  Name:\t%s\n", m.Name)
	_, _ = fmt.Fprintf(w, "  Client:\t%s\n", m.ClientID)
	_, _ = fmt.Fprintf(w, "  Scopes:\t%s\n", strings.Join(m.Scopes, ", "))
	_, _ = fmt.Fprintf(w, "  Namespaces:\t%s\n", strings.Join(m.NamespaceGlobs, ", "))
	_, _ = fmt.Fprintf(w, "  Created:\t%s\n", m.CreatedAt)
	_, _ = fmt.Fprintf(w, "  Expires:\t%s\n", m.ExpiresAt)
	_, _ = fmt.Fprintf(w, "  Secret file:\t%s\n", m.SecretFile)
	_, _ = fmt.Fprintf(w, "  Delivered:\t%t\n", m.SecretDelivered)
	_, _ = fmt.Fprintf(w, "  Sink state:\t%s\n", m.SinkState)
	return w.Flush()
}

// finishTokenCreateToSink completes a create whose sink was opened before the
// store call. The token value is passed only to deliver(); it never reaches
// stdout or stderr, on success or failure, and no failure path falls back to
// stdout. Every failure asks for owner reconciliation by exact issued ID and
// none authorizes a retry.
func (c *CLI) finishTokenCreateToSink(sink *tokenSink, token string, meta contextstore.AuthToken, createErr error, output string) int {
	if createErr != nil {
		// The error does not prove whether the row was committed (begin and
		// commit failures share one sentinel). Keep the empty file so a repeat
		// run on this path refuses, and do not guess.
		sink.release()
		return c.fail("token create did not complete and its commit outcome is UNKNOWN; an empty private file remains at the sink path; reconcile by listing token metadata; do not retry or revoke on a guess")
	}
	state, deliverErr := sink.deliver(token)
	m := tokenCreateSinkMetadata{
		ID: meta.TokenID, Name: meta.Label, ClientID: meta.ClientID,
		Scopes: meta.Scopes, NamespaceGlobs: meta.NamespaceGlobs,
		CreatedAt: meta.CreatedAt, ExpiresAt: meta.ExpiresAt,
		SecretFile: sink.path, SecretDelivered: deliverErr == nil, SinkState: state,
	}
	if deliverErr != nil {
		metaErr := c.writeTokenCreateSinkMetadata(m, output)
		fileText := "the private file was emptied"
		if state != sinkEmptied {
			fileText = "the private file MAY STILL CONTAIN SOME OR ALL OF A VALID CREDENTIAL and is retained for owner reconciliation"
		}
		msg := "private token delivery was not confirmed after the token was created; credential " + meta.TokenID + " remains active and revocable by that ID; " + fileText + "; owner reconciliation by that exact ID is required; do not retry, re-mint or assume it was revoked"
		if metaErr != nil {
			msg += "; the metadata report itself failed to reach stdout, so rely on this message and a token listing"
		}
		return c.fail(msg)
	}
	if c.writeTokenCreateSinkMetadata(m, output) != nil {
		return c.fail("the token was written to the private file but the metadata report failed to reach stdout; credential " + meta.TokenID + " is committed; owner reconciliation by that exact ID is required before any retry; this is not a usable receipt")
	}
	return 0
}
