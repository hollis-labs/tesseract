package embedding

import (
	"strings"
	"testing"
)

func TestChunkText_ShortText(t *testing.T) {
	chunks := ChunkText("Hello world", ChunkerConfig{MaxChars: 100})
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk for short text, got %d", len(chunks))
	}
	if chunks[0].Text != "Hello world" {
		t.Errorf("unexpected text: %q", chunks[0].Text)
	}
	if chunks[0].TotalCount != 1 {
		t.Errorf("expected TotalCount=1, got %d", chunks[0].TotalCount)
	}
}

func TestChunkText_Empty(t *testing.T) {
	chunks := ChunkText("", ChunkerConfig{MaxChars: 100})
	if len(chunks) != 0 {
		t.Fatalf("expected 0 chunks for empty text, got %d", len(chunks))
	}
}

func TestChunkFixed(t *testing.T) {
	text := strings.Repeat("word ", 100) // 500 chars
	chunks := ChunkText(text, ChunkerConfig{
		Strategy:   ChunkFixed,
		MaxChars:   100,
		OverlapPct: 10,
	})
	if len(chunks) < 4 {
		t.Fatalf("expected at least 4 chunks, got %d", len(chunks))
	}
	for i, c := range chunks {
		if c.Index != i {
			t.Errorf("chunk %d has index %d", i, c.Index)
		}
		if c.TotalCount != len(chunks) {
			t.Errorf("chunk %d TotalCount=%d, expected %d", i, c.TotalCount, len(chunks))
		}
		if len(c.Text) > 100 {
			t.Errorf("chunk %d exceeds max: %d chars", i, len(c.Text))
		}
	}
}

func TestChunkSentence(t *testing.T) {
	text := "First sentence. Second sentence. Third sentence. Fourth sentence. Fifth sentence."
	chunks := ChunkText(text, ChunkerConfig{
		Strategy: ChunkSentence,
		MaxChars: 40,
	})
	if len(chunks) < 2 {
		t.Fatalf("expected at least 2 chunks, got %d", len(chunks))
	}
	// Verify all text is covered.
	var all strings.Builder
	for _, c := range chunks {
		all.WriteString(c.Text)
	}
	// All sentences should be present.
	if !strings.Contains(all.String(), "First") || !strings.Contains(all.String(), "Fifth") {
		t.Errorf("missing content in chunks")
	}
}

func TestChunkParagraph(t *testing.T) {
	text := "Paragraph one content here.\n\nParagraph two content here.\n\nParagraph three content here."
	chunks := ChunkText(text, ChunkerConfig{
		Strategy: ChunkParagraph,
		MaxChars: 50,
	})
	if len(chunks) < 2 {
		t.Fatalf("expected at least 2 chunks, got %d", len(chunks))
	}
	for _, c := range chunks {
		if c.Text == "" {
			t.Error("empty chunk")
		}
	}
}

func TestChunkFixed_NoOverlap(t *testing.T) {
	text := strings.Repeat("a ", 200) // 400 chars
	chunks := ChunkText(text, ChunkerConfig{
		Strategy:   ChunkFixed,
		MaxChars:   100,
		OverlapPct: 0,
	})
	if len(chunks) < 3 {
		t.Fatalf("expected at least 3 chunks with no overlap, got %d", len(chunks))
	}
}

func TestChunkDefaults(t *testing.T) {
	text := strings.Repeat("word ", 300) // 1500 chars
	chunks := ChunkText(text, ChunkerConfig{})
	if len(chunks) < 2 {
		t.Fatalf("expected chunking with defaults, got %d chunks", len(chunks))
	}
}
