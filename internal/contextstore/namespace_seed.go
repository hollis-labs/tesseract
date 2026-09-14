package contextstore

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// SeededCerberusProjects is the canonical set of 18 Cerberus-backed projects
// seeded in the namespace registry (ADR adr_namespace_architecture; CW-20260912-0081).
var SeededCerberusProjects = []string{
	"cerberus",
	"chrispian-dev",
	"frag",
	"fragments-engine",
	"glyph",
	"hadron",
	"infrastructure",
	"loom",
	"nanite",
	"nil",
	"sigil",
	"stack-explorer",
	"sysop-ui",
	"tachyon",
	"tangent",
	"tesseract",
	"tether",
	"torque",
}

// IsCerberusProject reports whether slug is one of the 18 seeded Cerberus projects.
func IsCerberusProject(slug string) bool {
	s := strings.TrimSpace(slug)
	for _, p := range SeededCerberusProjects {
		if p == s {
			return true
		}
	}
	return false
}

// SeedCerberusProjects registers the 18 Cerberus project scope roots in
// namespace_policies if not already present. It is idempotent — existing
// declarations are preserved without overwrite. Returns the count of newly
// inserted rows.
func (s *Store) SeedCerberusProjects(ctx context.Context) (int, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	policyJSON, err := json.Marshal(map[string]any{
		"source": "seed",
		"scope":  "project",
	})
	if err != nil {
		return 0, fmt.Errorf("marshal seed policy: %w", err)
	}

	insertedCount := 0
	for _, slug := range SeededCerberusProjects {
		ns := "project/" + slug
		res, err := s.db.ExecContext(ctx, `
INSERT OR IGNORE INTO namespace_policies (namespace, owner_type, owner_id, policy_json, updated_at)
VALUES (?, ?, ?, ?, ?)`,
			ns, "project", slug, string(policyJSON), now)
		if err != nil {
			return insertedCount, fmt.Errorf("seed project %s: %w", slug, err)
		}
		rows, _ := res.RowsAffected()
		if rows > 0 {
			insertedCount++
			meta, _ := json.Marshal(map[string]any{
				"source":     "seed",
				"owner_type": "project",
				"owner_id":   slug,
			})
			_ = s.EmitNamespaceRegister(context.WithoutCancel(ctx), "system", ns, meta)
		}
	}
	return insertedCount, nil
}
