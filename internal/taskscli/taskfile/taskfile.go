package taskfile

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// File represents a single task markdown document.
type File struct {
	Path string
	doc  *yaml.Node
	body string
}

// Metadata is a typed projection of common frontmatter fields.
type Metadata struct {
	ID          string
	Title       string
	Status      string
	Priority    string
	Type        string
	Project     string
	Context     string
	Tags        []string
	ParentID    string
	SprintID    string
	CreatedAt   string
	UpdatedAt   string
	IterationID string
	FilePath    string
}

const (
	updatesHeading = "## Updates"
)

// Load reads a task file from disk.
func Load(path string) (*File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	front, body, err := splitFrontmatter(string(data))
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(front), &doc); err != nil {
		return nil, fmt.Errorf("parse YAML frontmatter in %s: %w", path, err)
	}
	if len(doc.Content) == 0 {
		doc.Content = []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}
	}

	return &File{
		Path: path,
		doc:  &doc,
		body: body,
	}, nil
}

// New returns an in-memory task file with default sections.
func New(path string) *File {
	return &File{
		Path: path,
		doc: &yaml.Node{
			Kind:    yaml.DocumentNode,
			Content: []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}},
		},
		body: DefaultBody(),
	}
}

// DefaultBody produces the standard section layout for new tasks.
func DefaultBody() string {
	return "\n## Description\n\n\n## Acceptance\n\n\n## Updates\n"
}

// Body returns the markdown body (after the frontmatter).
func (f *File) Body() string {
	return f.body
}

// SetBody replaces the markdown body.
func (f *File) SetBody(body string) {
	f.body = body
}

// Metadata extracts typed metadata for index population.
func (f *File) Metadata() Metadata {
	return Metadata{
		ID:          f.getString("id"),
		Title:       f.getString("title"),
		Status:      f.getString("status"),
		Priority:    f.getString("priority"),
		Type:        f.getString("type"),
		Project:     f.getString("project"),
		Context:     f.getString("context"),
		Tags:        f.getStringSlice("tags"),
		ParentID:    f.getString("parent_id"),
		SprintID:    f.getString("sprint_id"),
		CreatedAt:   f.getString("created_at"),
		UpdatedAt:   f.getString("updated_at"),
		IterationID: f.getString("iteration_id"),
		FilePath:    f.Path,
	}
}

// SetString sets/overwrites a scalar frontmatter value.
func (f *File) SetString(key, value string) {
	if value == "" {
		f.deleteKey(key)
		return
	}
	node := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
	f.setMapValue(key, node)
}

// SetTags stores tags as a YAML sequence.
func (f *File) SetTags(tags []string) {
	if len(tags) == 0 {
		f.deleteKey("tags")
		return
	}
	seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	for _, tag := range tags {
		if strings.TrimSpace(tag) == "" {
			continue
		}
		seq.Content = append(seq.Content, &yaml.Node{
			Kind:  yaml.ScalarNode,
			Tag:   "!!str",
			Value: strings.TrimSpace(tag),
		})
	}
	if len(seq.Content) == 0 {
		f.deleteKey("tags")
		return
	}
	f.setMapValue("tags", seq)
}

// AppendUpdate adds a new bullet to the ## Updates section.
func (f *File) AppendUpdate(entry string) error {
	if strings.TrimSpace(entry) == "" {
		return errors.New("update entry cannot be empty")
	}
	idx := strings.Index(f.body, updatesHeading)
	if idx == -1 {
		return fmt.Errorf("%s missing %s section", f.Path, updatesHeading)
	}

	sectionStart := idx + len(updatesHeading)
	rest := f.body[sectionStart:]
	nextHeading := strings.Index(rest, "\n## ")
	var updatesContent string
	var after string
	if nextHeading == -1 {
		updatesContent = rest
		after = ""
	} else {
		updatesContent = rest[:nextHeading]
		after = rest[nextHeading:]
	}

	if strings.TrimSpace(updatesContent) == "" {
		updatesContent = "\n- " + entry + "\n"
	} else {
		if !strings.HasSuffix(updatesContent, "\n") {
			updatesContent += "\n"
		}
		updatesContent += "- " + entry + "\n"
	}

	f.body = f.body[:sectionStart] + updatesContent + after
	return nil
}

// Save writes the task file to disk using an atomic rename.
func (f *File) Save() error {
	mapping := f.ensureMapping()

	var frontBuf bytes.Buffer
	enc := yaml.NewEncoder(&frontBuf)
	enc.SetIndent(2)
	if err := enc.Encode(mapping); err != nil {
		return fmt.Errorf("encode frontmatter: %w", err)
	}
	if err := enc.Close(); err != nil {
		return err
	}

	var buf bytes.Buffer
	buf.WriteString("---\n")
	buf.Write(frontBuf.Bytes())
	if !bytes.HasSuffix(frontBuf.Bytes(), []byte("\n")) {
		buf.WriteByte('\n')
	}
	buf.WriteString("---\n")
	buf.WriteString(f.body)
	if !strings.HasSuffix(f.body, "\n") {
		buf.WriteByte('\n')
	}

	if err := os.MkdirAll(filepath.Dir(f.Path), 0o755); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(filepath.Dir(f.Path), "task-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())

	if _, err := tmp.Write(buf.Bytes()); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	return os.Rename(tmp.Name(), f.Path)
}

func (f *File) ensureMapping() *yaml.Node {
	if len(f.doc.Content) == 0 {
		f.doc.Content = []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}
	}
	return f.doc.Content[0]
}

func (f *File) getString(key string) string {
	if node := f.getMapValue(key); node != nil {
		return node.Value
	}
	return ""
}

func (f *File) getStringSlice(key string) []string {
	node := f.getMapValue(key)
	if node == nil || node.Kind != yaml.SequenceNode {
		return nil
	}
	var out []string
	for _, child := range node.Content {
		out = append(out, child.Value)
	}
	return out
}

func (f *File) setMapValue(key string, value *yaml.Node) {
	m := f.ensureMapping()
	for i := 0; i < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content[i+1] = value
			return
		}
	}
	keyNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}
	m.Content = append(m.Content, keyNode, value)
}

func (f *File) getMapValue(key string) *yaml.Node {
	m := f.ensureMapping()
	for i := 0; i < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

func (f *File) deleteKey(key string) {
	m := f.ensureMapping()
	for i := 0; i < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content = append(m.Content[:i], m.Content[i+2:]...)
			return
		}
	}
}

func splitFrontmatter(content string) (string, string, error) {
	if !strings.HasPrefix(content, "---") {
		return "", "", errors.New("missing frontmatter delimiter")
	}
	trimmed := content[len("---"):]
	switch {
	case strings.HasPrefix(trimmed, "\r\n"):
		trimmed = trimmed[len("\r\n"):]
	case strings.HasPrefix(trimmed, "\n"):
		trimmed = trimmed[len("\n"):]
	default:
		return "", "", errors.New("invalid frontmatter start")
	}

	for _, marker := range []string{"\n---\n", "\r\n---\r\n", "\n---\r\n", "\r\n---\n", "---\n", "---\r\n"} {
		if idx := strings.Index(trimmed, marker); idx != -1 {
			front := trimmed[:idx]
			body := trimmed[idx+len(marker):]
			return front, body, nil
		}
	}

	return "", "", errors.New("missing closing frontmatter delimiter")
}
