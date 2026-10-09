package contextapi

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"reflect"
	"strings"

	"github.com/hollis-labs/tesseract/internal/contextstore"
)

// No anonymous loopback or static credential becomes a managed administrator.
// The store revalidates this opaque proof within the issuer transaction.
func (s *Server) credentialAdmin(w http.ResponseWriter, r *http.Request) (contextstore.CredentialAdmin, bool) {
	if !s.ManagedAuth {
		writeError(w, http.StatusForbidden, "admin_auth_required", "managed administrator credential required", nil)
		return contextstore.CredentialAdmin{}, false
	}
	raw := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	admin, err := s.Store.AuthorizeCredentialAdmin(r.Context(), raw)
	if err != nil {
		credentialAPIError(w, contextstore.ErrCredentialForbidden)
		return contextstore.CredentialAdmin{}, false
	}
	return admin, true
}
func credentialAPIError(w http.ResponseWriter, err error) {
	code := http.StatusInternalServerError
	kind := "credential_unavailable"
	message := "credential operation unavailable"
	switch {
	case errors.Is(err, contextstore.ErrCredentialForbidden):
		code = http.StatusForbidden
		kind = "admin_auth_required"
		message = "managed administrator credential required"
	case errors.Is(err, contextstore.ErrCredentialConflict):
		code = http.StatusConflict
		kind = "credential_conflict"
		message = "credential generation or idempotency conflict"
	case errors.Is(err, contextstore.ErrCredentialRequest), errors.Is(err, contextstore.ErrAuthTokenInvalid), errors.Is(err, contextstore.ErrAuthTokenRevoked):
		code = http.StatusBadRequest
		kind = "credential_request_invalid"
		message = "invalid credential request"
	}
	writeError(w, code, kind, message, nil)
}

// Strict closed flat schema: encoding/json alone accepts duplicate/case-alias
// keys and trailing objects. Never echo a supplied key/value into errors.
func decodeCredentialRequest(r *http.Request, dst any) error {
	defer r.Body.Close()
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 8192))
	t, err := dec.Token()
	if err != nil || t != json.Delim('{') {
		return contextstore.ErrCredentialRequest
	}
	typ := reflect.TypeOf(dst).Elem()
	allowed := map[string]bool{}
	for i := 0; i < typ.NumField(); i++ {
		allowed[strings.Split(typ.Field(i).Tag.Get("json"), ",")[0]] = true
	}
	fields := map[string]json.RawMessage{}
	for dec.More() {
		key, e := dec.Token()
		if e != nil {
			return contextstore.ErrCredentialRequest
		}
		name, ok := key.(string)
		if !ok || !allowed[name] || fields[name] != nil {
			return contextstore.ErrCredentialRequest
		}
		var value json.RawMessage
		if dec.Decode(&value) != nil || string(value) == "null" {
			return contextstore.ErrCredentialRequest
		}
		fields[name] = value
	}
	if _, err = dec.Token(); err != nil {
		return contextstore.ErrCredentialRequest
	}
	if _, err = dec.Token(); err != io.EOF {
		return contextstore.ErrCredentialRequest
	}
	b, err := json.Marshal(fields)
	if err != nil || json.Unmarshal(b, dst) != nil {
		return contextstore.ErrCredentialRequest
	}
	return nil
}
func credentialPrivateTransport(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
func (s *Server) handleCredentialIssue(w http.ResponseWriter, r *http.Request) {
	admin, ok := s.credentialAdmin(w, r)
	if !ok {
		return
	}
	if !credentialPrivateTransport(r) {
		writeError(w, http.StatusForbidden, "private_transport_required", "credential issuance requires confidential local or TLS transport", nil)
		return
	}
	var in contextstore.CredentialIssueInput
	if err := decodeCredentialRequest(r, &in); err != nil {
		credentialAPIError(w, err)
		return
	}
	secret, result, err := s.Store.IssueServiceCredential(r.Context(), admin, in, "http")
	if err != nil {
		credentialAPIError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	// A failed/partial response leaves a committed, listable obligation. Retry
	// returns metadata alone; never log or persist this response to recover it.
	response := struct {
		contextstore.CredentialResult
		Token string `json:"token,omitempty"`
	}{result, secret}
	writeJSON(w, http.StatusOK, response)
}
func (s *Server) handleCredentialRevoke(w http.ResponseWriter, r *http.Request) {
	admin, ok := s.credentialAdmin(w, r)
	if !ok {
		return
	}
	var in contextstore.CredentialRevokeInput
	if err := decodeCredentialRequest(r, &in); err != nil {
		credentialAPIError(w, err)
		return
	}
	result, err := s.Store.RevokeServiceCredential(r.Context(), admin, in, "http")
	if err != nil {
		if errors.Is(err, contextstore.ErrAuthTokenRevoked) && result.CredentialID != "" {
			writeError(w, http.StatusBadRequest, "credential_revoked", "credential is already revoked", map[string]any{"current_credential": result})
			return
		}
		credentialAPIError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, result)
}
