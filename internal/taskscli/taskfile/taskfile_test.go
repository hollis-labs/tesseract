package taskfile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sampleTask = `---
id: TASK-00000000-001
title: "Sample task"
status: todo
priority: A
project: forge
tags: [alpha]
context: dev
created_at: 2026-02-21
updated_at: 2026-02-21
---

## Description

Body text.

## Acceptance

- a

## Updates
- seed entry
`

const sampleInlineDelimiter = `---
id: TASK-00000000-002
title: Inline delimiter
status: todo
project: forge
updated_at: 2026-02-21---

## Description

## Updates
- seed
`

func TestLoadModifyAndSave(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "TASK-00000000-001.md")
	if err := os.WriteFile(path, []byte(sampleTask), 0o644); err != nil {
		t.Fatalf("write sample: %v", err)
	}

	tf, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	tf.SetString("status", "doing")
	tf.SetString("updated_at", "2026-02-22")
	tf.SetTags([]string{"alpha", "beta"})
	if err := tf.AppendUpdate("2026-02-22 started task"); err != nil {
		t.Fatalf("append update: %v", err)
	}
	if err := tf.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("re-read: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, "status: doing") {
		t.Fatalf("status not updated:\n%s", content)
	}
	if !strings.Contains(content, "beta") {
		t.Fatalf("tags not updated:\n%s", content)
	}
	if !strings.Contains(content, "- 2026-02-22 started task") {
		t.Fatalf("update not appended:\n%s", content)
	}

	meta := tf.Metadata()
	if meta.Status != "doing" || meta.UpdatedAt != "2026-02-22" {
		t.Fatalf("metadata mismatch: %+v", meta)
	}
}

func TestDefaultBodySections(t *testing.T) {
	body := DefaultBody()
	for _, heading := range []string{"## Description", "## Acceptance", "## Updates"} {
		if !strings.Contains(body, heading) {
			t.Fatalf("default body missing %s", heading)
		}
	}
}

func TestSplitFrontmatterInlineDelimiter(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "TASK-inline.md")
	if err := os.WriteFile(path, []byte(sampleInlineDelimiter), 0o644); err != nil {
		t.Fatalf("write sample: %v", err)
	}

	if _, err := Load(path); err != nil {
		t.Fatalf("load inline delimiter: %v", err)
	}
}
