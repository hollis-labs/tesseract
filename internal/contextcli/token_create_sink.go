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

// Test seams. Production values are the real process identity, a plain write
// and a char-device terminal check.
var (
	tokenSinkEUID  = os.Geteuid
	tokenSinkWrite = func(f *os.File, data string) error {
		n, err := io.WriteString(f, data)
		if err == nil && n != len(data) {
			err = io.ErrShortWrite
		}
		return err
	}
	tokenOutputIsTerminal = writerIsTerminal
)

// tokenSink is the exclusive private file a newly created token is delivered
// to. It is opened BEFORE the store commit, so every create, permission,
// existing-path and symlink failure is a refusal that mints nothing. After the
// commit the only remaining failures are write, sync, close and directory
// sync, which deliver() reports as lost delivery.
type tokenSink struct {
	file   *os.File
	path   string
	parent string
	info   os.FileInfo
}

// writerIsTerminal reports whether w is a terminal-like character device.
// /dev/null is a character device but cannot retain output, so it is allowed.
func writerIsTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	if err != nil || info.Mode()&os.ModeCharDevice == 0 {
		return false
	}
	if null, err := os.Stat(os.DevNull); err == nil && os.SameFile(info, null) {
		return false
	}
	return true
}

func containsAdminScope(scopes []string) bool {
	for _, s := range scopes {
		if strings.EqualFold(strings.TrimSpace(s), "admin") {
			return true
		}
	}
	return false
}

// release closes the sink without touching the file. It is used when the store
// outcome is unknown: the empty file stays so a repeat run on the same path
// refuses before minting anything.
func (s *tokenSink) release() {
	if s != nil && s.file != nil {
		_ = s.file.Close()
		s.file = nil
	}
}

// deliver writes the token and makes it durable. On any failure it empties the
// file (best effort) so no partial token is left claiming to be one, and the
// caller must report lost delivery.
func (s *tokenSink) deliver(token string) error {
	f := s.file
	err := tokenSinkWrite(f, token+"\n")
	if err == nil {
		err = f.Sync()
	}
	if err != nil {
		_ = f.Truncate(0)
		_ = f.Close()
		s.file = nil
		return errTokenSink
	}
	closeErr := f.Close()
	s.file = nil
	if closeErr == nil {
		closeErr = syncParentDir(s.parent)
	}
	if closeErr != nil {
		s.emptyByName()
		return errTokenSink
	}
	return nil
}

// emptyByName truncates the sink only if the path still names the file we
// created; it never follows a replaced path.
func (s *tokenSink) emptyByName() {
	cur, err := os.Lstat(s.path)
	if err != nil || !cur.Mode().IsRegular() || !os.SameFile(cur, s.info) {
		return
	}
	_ = os.Truncate(s.path, 0)
}

// tokenCreateSinkMetadata is everything a sink-mode create may print. It has no
// token field by construction.
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
	return w.Flush()
}

// finishTokenCreateToSink completes a create whose sink was opened before the
// store call. The token value is passed only to deliver(); it never reaches
// stdout or stderr, on success or failure.
func (c *CLI) finishTokenCreateToSink(sink *tokenSink, token string, meta contextstore.AuthToken, createErr error, output string) int {
	if createErr != nil {
		// The error does not prove whether the row was committed (begin and
		// commit failures share one sentinel). Keep the empty file so a repeat
		// run on this path refuses, and do not guess.
		sink.release()
		return c.fail("token create did not complete and its commit outcome is not established; an empty private file remains at the sink path; list token metadata and reconcile before retrying")
	}
	deliverErr := sink.deliver(token)
	m := tokenCreateSinkMetadata{
		ID: meta.TokenID, Name: meta.Label, ClientID: meta.ClientID,
		Scopes: meta.Scopes, NamespaceGlobs: meta.NamespaceGlobs,
		CreatedAt: meta.CreatedAt, ExpiresAt: meta.ExpiresAt,
		SecretFile: sink.path, SecretDelivered: deliverErr == nil,
	}
	if deliverErr != nil {
		_ = c.writeTokenCreateSinkMetadata(m, output)
		return c.fail("private token delivery failed after the token was created; credential " + meta.TokenID + " remains active and revocable by that ID; treat the private file as unusable (emptying it was best effort); do not re-mint or revoke until that ID is confirmed")
	}
	if c.writeTokenCreateSinkMetadata(m, output) != nil {
		return c.fail("token written to the private file; metadata output failed; credential " + meta.TokenID + " remains committed")
	}
	return 0
}
