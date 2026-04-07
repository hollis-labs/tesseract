package embedding

import "math"

// SearchResult holds a single ranked search match.
type SearchResult struct {
	RecordID  string  `json:"record_id"`
	Namespace string  `json:"namespace"`
	Key       string  `json:"key"`
	Score     float64 `json:"score"`
}

// CosineSimilarity computes the cosine similarity between two vectors.
// Returns 0 if either vector has zero magnitude.
func CosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		ai, bi := float64(a[i]), float64(b[i])
		dot += ai * bi
		normA += ai * ai
		normB += bi * bi
	}
	denom := math.Sqrt(normA) * math.Sqrt(normB)
	if denom == 0 {
		return 0
	}
	return dot / denom
}

// RankByCosineSimilarity scores candidates against a query vector and returns
// the top-limit results sorted by descending score, filtered by threshold.
func RankByCosineSimilarity(query []float32, vectors [][]float32, recordIDs []string, namespaces []string, keys []string, limit int, threshold float64) []SearchResult {
	type scored struct {
		idx   int
		score float64
	}

	var candidates []scored
	for i, vec := range vectors {
		s := CosineSimilarity(query, vec)
		if s >= threshold {
			candidates = append(candidates, scored{idx: i, score: s})
		}
	}

	// Sort descending by score.
	for i := 0; i < len(candidates); i++ {
		for j := i + 1; j < len(candidates); j++ {
			if candidates[j].score > candidates[i].score {
				candidates[i], candidates[j] = candidates[j], candidates[i]
			}
		}
	}

	if limit > 0 && len(candidates) > limit {
		candidates = candidates[:limit]
	}

	results := make([]SearchResult, len(candidates))
	for i, c := range candidates {
		results[i] = SearchResult{
			RecordID:  recordIDs[c.idx],
			Namespace: namespaces[c.idx],
			Key:       keys[c.idx],
			Score:     c.score,
		}
	}
	return results
}
