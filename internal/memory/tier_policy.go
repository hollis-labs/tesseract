package memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/hollis-labs/tesseract/domains"
	"github.com/hollis-labs/tesseract/internal/contextpolicy"
)

type queryRower interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// validateTierPolicy checks declared tier policy (allowed_ops, max_bytes_per_key,
// required_schema_keys) for the incoming write. It checks the exact namespace
// first, falling back to the scope head (e.g. project/{slug}). If no declared
// policy is found, it returns nil (default passthrough).
func (s *Store) validateTierPolicy(ctx context.Context, q queryRower, in WriteInput) error {
	ns := strings.TrimSpace(in.Namespace)
	if ns == "" {
		return nil
	}

	var policyJSON sql.NullString
	err := q.QueryRowContext(ctx, `SELECT policy_json FROM namespace_policies WHERE namespace = ?`, ns).Scan(&policyJSON)
	var activePolicy string
	if err == nil && isDeclaredPolicyJSON(policyJSON.String) {
		activePolicy = policyJSON.String
	} else if err != nil && !errors.Is(err, sql.ErrNoRows) {
		// If table doesn't exist (e.g. non-migrated mock), pass through
		if strings.Contains(err.Error(), "no such table") {
			return nil
		}
		return err
	}

	if activePolicy == "" {
		head := contextpolicy.ExtractScopeHead(ns)
		if head != "" && head != ns {
			var hPolicy sql.NullString
			hErr := q.QueryRowContext(ctx, `SELECT policy_json FROM namespace_policies WHERE namespace = ?`, head).Scan(&hPolicy)
			if hErr == nil && isDeclaredPolicyJSON(hPolicy.String) {
				activePolicy = hPolicy.String
			} else if hErr != nil && !errors.Is(hErr, sql.ErrNoRows) {
				if !strings.Contains(hErr.Error(), "no such table") {
					return hErr
				}
			}
		}
	}

	if activePolicy == "" {
		return nil
	}

	var policyMap map[string]any
	if err := json.Unmarshal([]byte(activePolicy), &policyMap); err != nil {
		return fmt.Errorf("unmarshal namespace policy: %w", err)
	}

	tp := contextpolicy.ParseTierPolicy(policyMap)
	payloadLen := len(in.Summary) + len(in.Body) + len(in.Data)
	return tp.Validate(ns, "write", payloadLen, in.Data)
}

// validateCanWrite enforces namespace write authorization rules via
// contextpolicy.CanWrite. It consults namespace_policies for declared
// registration on the target namespace and its scope head, registers them in a
// policy engine, and checks write permission. An empty actor defaults to
// "agent", ensuring unasserted writes to protected user namespaces fail closed.
func (s *Store) validateCanWrite(ctx context.Context, q queryRower, in WriteInput) error {
	// CanWrite enforcement applies to memory and knowledge domains (CW-20260912-0082).
	// The event domain is an append-only narrative log that opts out of memory scope fencing.
	if in.Domain == domains.Event {
		return nil
	}
	ns := strings.TrimSpace(in.Namespace)
	if ns == "" {
		return nil
	}
	actor := strings.TrimSpace(in.Actor)
	if actor == "" {
		actor = "agent"
	}
	clientID := strings.TrimSpace(in.ClientID)

	engine := contextpolicy.New()

	// 1. Exact lookup
	var ownerType, ownerID, policyJSON sql.NullString
	err := q.QueryRowContext(ctx, `SELECT owner_type, owner_id, policy_json FROM namespace_policies WHERE namespace = ?`, ns).
		Scan(&ownerType, &ownerID, &policyJSON)
	if err == nil && ownerType.Valid && ownerID.Valid && ownerType.String != "" && ownerID.String != "" {
		var policyMap map[string]any
		if policyJSON.Valid && policyJSON.String != "" {
			_ = json.Unmarshal([]byte(policyJSON.String), &policyMap)
		}
		_ = engine.RegisterNamespace(ns, ownerType.String, ownerID.String, policyMap)
	} else if err != nil && !errors.Is(err, sql.ErrNoRows) {
		if !strings.Contains(err.Error(), "no such table") {
			return err
		}
	}

	// 2. Scope head fallback
	head := contextpolicy.ExtractScopeHead(ns)
	if head != "" && head != ns {
		var hType, hID, hPolicy sql.NullString
		hErr := q.QueryRowContext(ctx, `SELECT owner_type, owner_id, policy_json FROM namespace_policies WHERE namespace = ?`, head).
			Scan(&hType, &hID, &hPolicy)
		if hErr == nil && hType.Valid && hID.Valid && hType.String != "" && hID.String != "" {
			var hPolicyMap map[string]any
			if hPolicy.Valid && hPolicy.String != "" {
				_ = json.Unmarshal([]byte(hPolicy.String), &hPolicyMap)
			}
			_ = engine.RegisterNamespace(head, hType.String, hID.String, hPolicyMap)
		} else if hErr != nil && !errors.Is(hErr, sql.ErrNoRows) {
			if !strings.Contains(hErr.Error(), "no such table") {
				return hErr
			}
		}
	}

	return engine.CanWrite(clientID, actor, ns)
}

// NamespaceOwner returns the registered owner of namespace. It consults
// namespace_policies for an exact match, falls back to the scope head in
// namespace_policies, and finally falls back to deriveNamespaceOwner.
func (s *Store) NamespaceOwner(ctx context.Context, namespace string) (string, string, error) {
	ns := strings.TrimSpace(namespace)
	if ns == "" {
		return "", "", errors.New("namespace required")
	}

	// 1. Exact lookup
	var ownerType, ownerID, policyJSON sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT owner_type, owner_id, policy_json FROM namespace_policies WHERE namespace = ?`, ns).
		Scan(&ownerType, &ownerID, &policyJSON)
	if err == nil {
		if isDeclaredPolicyJSON(policyJSON.String) && ownerType.Valid && ownerID.Valid && ownerType.String != "" && ownerID.String != "" {
			return ownerType.String, ownerID.String, nil
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		if !strings.Contains(err.Error(), "no such table") {
			return "", "", err
		}
	}

	// 2. Scope head fallback
	head := contextpolicy.ExtractScopeHead(ns)
	if head != "" && head != ns {
		var hType, hID, hPolicy sql.NullString
		hErr := s.db.QueryRowContext(ctx, `SELECT owner_type, owner_id, policy_json FROM namespace_policies WHERE namespace = ?`, head).
			Scan(&hType, &hID, &hPolicy)
		if hErr == nil {
			if (isDeclaredPolicyJSON(hPolicy.String) || err != nil) && hType.Valid && hID.Valid && hType.String != "" && hID.String != "" {
				return hType.String, hID.String, nil
			}
		} else if !errors.Is(hErr, sql.ErrNoRows) {
			if !strings.Contains(hErr.Error(), "no such table") {
				return "", "", hErr
			}
		}
	}

	// If exact match existed (even inferred), return its owner
	if err == nil && ownerType.Valid && ownerID.Valid && ownerType.String != "" && ownerID.String != "" {
		return ownerType.String, ownerID.String, nil
	}

	// 3. Path derivation fallback
	return deriveNamespaceOwner(ns)
}

func deriveNamespaceOwner(namespace string) (string, string, error) {
	ns := strings.TrimSpace(namespace)
	parts := strings.Split(ns, "/")
	if len(parts) >= 2 && parts[1] != "" {
		switch parts[0] {
		case "user", "project", "app", "org", "session":
			return parts[0], parts[1], nil
		}
	}
	return "system", ns, nil
}

func isDeclaredPolicyJSON(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "{}" || raw == "null" {
		return false
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return false
	}
	src, _ := m["source"].(string)
	if src == "inferred" || src == "inferred-backfill" {
		return false
	}
	return len(m) > 0
}
