// Package skills embeds agent-facing markdown skills that document
// Tesseract primitives and features. Served over MCP via the
// tesseract_skills tool.
package skills

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

//go:embed *.md
var skillsFS embed.FS

// SkillMeta is the index entry returned from List.
type SkillMeta struct {
	Name        string   `json:"name" yaml:"name"`
	Description string   `json:"description" yaml:"description"`
	ScopeHint   string   `json:"scope_hint" yaml:"scope_hint"`
	Related     []string `json:"related,omitempty" yaml:"related,omitempty"`
}

// ErrSkillNotFound is returned by Get when name is not an embedded skill.
var ErrSkillNotFound = errors.New("skill not found")

// List returns metadata for every embedded skill. Result is sorted with
// "start-here" first, then alphabetical by Name.
func List() ([]SkillMeta, error) {
	entries, err := fs.ReadDir(skillsFS, ".")
	if err != nil {
		return nil, fmt.Errorf("read skills fs: %w", err)
	}
	metas := make([]SkillMeta, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		m, err := parseMeta(e.Name())
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", e.Name(), err)
		}
		metas = append(metas, m)
	}
	sort.SliceStable(metas, func(i, j int) bool {
		if metas[i].Name == "start-here" {
			return true
		}
		if metas[j].Name == "start-here" {
			return false
		}
		return metas[i].Name < metas[j].Name
	})
	return metas, nil
}

// Get returns the full markdown body (including frontmatter) for the named
// skill. Returns ErrSkillNotFound wrapped with a list of valid names when
// name is unknown.
func Get(name string) (string, error) {
	data, err := fs.ReadFile(skillsFS, name+".md")
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			avail, listErr := listNames()
			if listErr != nil {
				return "", fmt.Errorf("%w: %q (list available skills: %w)", ErrSkillNotFound, name, listErr)
			}
			return "", fmt.Errorf("%w: %q. Available: [%s]", ErrSkillNotFound, name, strings.Join(avail, ", "))
		}
		return "", fmt.Errorf("read skill %q: %w", name, err)
	}
	return string(data), nil
}

func listNames() ([]string, error) {
	metas, err := List()
	if err != nil {
		return nil, err
	}
	names := make([]string, len(metas))
	for i, m := range metas {
		names[i] = m.Name
	}
	return names, nil
}

// parseMeta reads a single skill file and returns its frontmatter.
// Expected layout: "---\n<yaml>\n---\n<body>".
func parseMeta(filename string) (SkillMeta, error) {
	var m SkillMeta
	data, err := fs.ReadFile(skillsFS, filename)
	if err != nil {
		return m, err
	}
	s := string(data)
	if !strings.HasPrefix(s, "---\n") {
		return m, fmt.Errorf("missing frontmatter opener")
	}
	rest := s[4:]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return m, fmt.Errorf("missing frontmatter closer")
	}
	if err := yaml.Unmarshal([]byte(rest[:end]), &m); err != nil {
		return m, fmt.Errorf("yaml: %w", err)
	}
	expectedName := strings.TrimSuffix(filename, ".md")
	if m.Name != "" && m.Name != expectedName {
		return m, fmt.Errorf("frontmatter name %q does not match filename %q", m.Name, expectedName)
	}
	if m.Name == "" {
		return m, fmt.Errorf("frontmatter missing name")
	}
	if m.Description == "" {
		return m, fmt.Errorf("frontmatter missing description")
	}
	return m, nil
}
