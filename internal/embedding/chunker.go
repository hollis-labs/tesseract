package embedding

import (
	"strings"
	"unicode"
)

// ChunkStrategy defines how documents are split into chunks.
type ChunkStrategy string

const (
	// ChunkFixed splits text into fixed-size chunks with configurable overlap.
	ChunkFixed ChunkStrategy = "fixed"
	// ChunkSentence splits on sentence boundaries, grouping into chunks up to max size.
	ChunkSentence ChunkStrategy = "sentence"
	// ChunkParagraph splits on paragraph boundaries (double newlines).
	ChunkParagraph ChunkStrategy = "paragraph"
)

// ChunkerConfig controls chunking behavior.
type ChunkerConfig struct {
	Strategy   ChunkStrategy // default: "fixed"
	MaxChars   int           // max characters per chunk (default: 1000)
	OverlapPct int           // overlap percentage for fixed strategy (default: 10, range 0-50)
}

// Chunk represents a piece of a larger document.
type Chunk struct {
	Text       string // the chunk text
	Index      int    // zero-based chunk index
	StartChar  int    // start offset in original text
	EndChar    int    // end offset in original text
	TotalCount int    // total number of chunks
}

// ChunkText splits text into chunks using the configured strategy.
func ChunkText(text string, cfg ChunkerConfig) []Chunk {
	if cfg.MaxChars <= 0 {
		cfg.MaxChars = 1000
	}
	if cfg.Strategy == "" {
		cfg.Strategy = ChunkFixed
	}
	if cfg.OverlapPct < 0 {
		cfg.OverlapPct = 0
	}
	if cfg.OverlapPct > 50 {
		cfg.OverlapPct = 50
	}

	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	if len(text) <= cfg.MaxChars {
		return []Chunk{{Text: text, Index: 0, StartChar: 0, EndChar: len(text), TotalCount: 1}}
	}

	switch cfg.Strategy {
	case ChunkSentence:
		return chunkBySentence(text, cfg.MaxChars)
	case ChunkParagraph:
		return chunkByParagraph(text, cfg.MaxChars)
	default:
		return chunkFixed(text, cfg.MaxChars, cfg.OverlapPct)
	}
}

// chunkFixed splits text into fixed-size chunks with overlap.
func chunkFixed(text string, maxChars, overlapPct int) []Chunk {
	overlap := maxChars * overlapPct / 100
	step := maxChars - overlap
	if step <= 0 {
		step = 1
	}

	var chunks []Chunk
	for start := 0; start < len(text); start += step {
		end := start + maxChars
		if end > len(text) {
			end = len(text)
		}
		// Try to break at a word boundary.
		if end < len(text) {
			breakAt := strings.LastIndexFunc(text[start:end], unicode.IsSpace)
			if breakAt > 0 {
				end = start + breakAt
			}
		}
		chunk := strings.TrimSpace(text[start:end])
		if chunk != "" {
			chunks = append(chunks, Chunk{
				Text:      chunk,
				Index:     len(chunks),
				StartChar: start,
				EndChar:   end,
			})
		}
		if end >= len(text) {
			break
		}
	}
	setTotalCount(chunks)
	return chunks
}

// chunkBySentence groups sentences into chunks up to maxChars.
func chunkBySentence(text string, maxChars int) []Chunk {
	sentences := splitSentences(text)
	var chunks []Chunk
	var current strings.Builder
	startChar := 0
	pos := 0

	for _, sent := range sentences {
		sentLen := len(sent)
		if current.Len() > 0 && current.Len()+sentLen > maxChars {
			chunk := strings.TrimSpace(current.String())
			if chunk != "" {
				chunks = append(chunks, Chunk{
					Text:      chunk,
					Index:     len(chunks),
					StartChar: startChar,
					EndChar:   pos,
				})
			}
			current.Reset()
			startChar = pos
		}
		current.WriteString(sent)
		pos += sentLen
	}
	if current.Len() > 0 {
		chunk := strings.TrimSpace(current.String())
		if chunk != "" {
			chunks = append(chunks, Chunk{
				Text:      chunk,
				Index:     len(chunks),
				StartChar: startChar,
				EndChar:   pos,
			})
		}
	}
	setTotalCount(chunks)
	return chunks
}

// chunkByParagraph splits on double newlines, grouping short paragraphs together.
func chunkByParagraph(text string, maxChars int) []Chunk {
	paragraphs := strings.Split(text, "\n\n")
	var chunks []Chunk
	var current strings.Builder
	startChar := 0
	pos := 0

	for i, para := range paragraphs {
		para = strings.TrimSpace(para)
		if para == "" {
			if i < len(paragraphs)-1 {
				pos += len(paragraphs[i]) + 2 // account for \n\n
			}
			continue
		}
		paraLen := len(para)
		if current.Len() > 0 && current.Len()+paraLen+2 > maxChars {
			chunk := strings.TrimSpace(current.String())
			if chunk != "" {
				chunks = append(chunks, Chunk{
					Text:      chunk,
					Index:     len(chunks),
					StartChar: startChar,
					EndChar:   pos,
				})
			}
			current.Reset()
			startChar = pos
		}
		if current.Len() > 0 {
			current.WriteString("\n\n")
		}
		current.WriteString(para)
		pos += len(paragraphs[i])
		if i < len(paragraphs)-1 {
			pos += 2
		}
	}
	if current.Len() > 0 {
		chunk := strings.TrimSpace(current.String())
		if chunk != "" {
			chunks = append(chunks, Chunk{
				Text:      chunk,
				Index:     len(chunks),
				StartChar: startChar,
				EndChar:   pos,
			})
		}
	}
	setTotalCount(chunks)
	return chunks
}

// splitSentences does basic sentence splitting on '.', '!', '?'.
func splitSentences(text string) []string {
	var sentences []string
	var current strings.Builder
	runes := []rune(text)
	for i, r := range runes {
		current.WriteRune(r)
		if (r == '.' || r == '!' || r == '?') && i+1 < len(runes) && unicode.IsSpace(runes[i+1]) {
			sentences = append(sentences, current.String())
			current.Reset()
		}
	}
	if current.Len() > 0 {
		sentences = append(sentences, current.String())
	}
	return sentences
}

func setTotalCount(chunks []Chunk) {
	for i := range chunks {
		chunks[i].TotalCount = len(chunks)
	}
}
