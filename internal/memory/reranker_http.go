package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// HTTPRerankerConfig parameterizes a cross-encoder reranker that speaks
// the Cohere/Voyage JSON shape: POST {endpoint} with
// {"model", "query", "documents", "top_n"} and Authorization bearer key;
// response {"results":[{"index":int,"relevance_score":float}]}. Several
// cross-encoder providers (Cohere Rerank v2/v3, Voyage rerank-2,
// self-hosted bge-reranker wrapped in a compatible gateway) accept the
// same shape, so a single adapter covers them.
type HTTPRerankerConfig struct {
	Endpoint   string        // full URL to POST to
	APIKey     string        // bearer token (empty sends no Authorization header)
	Model      string        // model name passed in the payload
	HTTPClient *http.Client  // optional; defaults to http.Client with 30s timeout
	Timeout    time.Duration // defaults to 30s when HTTPClient is nil
	// DocumentText extracts the indexable text from a Revision. Defaults
	// to concatenating payload_summary + " " + payload_body.
	DocumentText func(Revision) string
}

// HTTPReranker implements Reranker via a Cohere/Voyage-compatible HTTP
// endpoint. Use NewHTTPReranker to construct.
type HTTPReranker struct {
	cfg HTTPRerankerConfig
}

// NewHTTPReranker returns a Reranker that calls the configured HTTP
// endpoint. No validation is performed here — missing endpoints show
// up as HTTP errors at Rerank time.
func NewHTTPReranker(cfg HTTPRerankerConfig) *HTTPReranker {
	if cfg.HTTPClient == nil {
		timeout := cfg.Timeout
		if timeout == 0 {
			timeout = 30 * time.Second
		}
		cfg.HTTPClient = &http.Client{Timeout: timeout}
	}
	if cfg.DocumentText == nil {
		cfg.DocumentText = defaultDocumentText
	}
	return &HTTPReranker{cfg: cfg}
}

func defaultDocumentText(r Revision) string {
	return strings.TrimSpace(r.Payload.Summary + " " + r.Payload.Body)
}

type rerankRequest struct {
	Model     string   `json:"model"`
	Query     string   `json:"query"`
	Documents []string `json:"documents"`
	TopN      int      `json:"top_n,omitempty"`
}

type rerankResult struct {
	Index          int     `json:"index"`
	RelevanceScore float64 `json:"relevance_score"`
}

type rerankResponse struct {
	Results []rerankResult `json:"results"`
}

// Rerank issues an HTTP POST to the configured endpoint and returns the
// candidates in the reranker's preferred order, truncated to topK when
// topK > 0.
func (h *HTTPReranker) Rerank(ctx context.Context, query string, candidates []Revision, topK int) ([]Revision, error) {
	if len(candidates) == 0 {
		return candidates, nil
	}

	docs := make([]string, len(candidates))
	for i, c := range candidates {
		docs[i] = h.cfg.DocumentText(c)
	}

	payload := rerankRequest{
		Model:     h.cfg.Model,
		Query:     query,
		Documents: docs,
	}
	// Always send top_n when the caller asked for a specific top-K, even
	// when it matches the document count. Some Cohere/Voyage-compatible
	// endpoints default top_n to a small constant when the field is
	// absent, which would silently truncate results. Clamp to the
	// document count so we never ask the server for more than we sent.
	if topK > 0 {
		payload.TopN = topK
		if payload.TopN > len(candidates) {
			payload.TopN = len(candidates)
		}
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode rerank request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.cfg.Endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build rerank request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if h.cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+h.cfg.APIKey)
	}

	resp, err := h.cfg.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("rerank http: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("rerank http status %d: %s", resp.StatusCode, strings.TrimSpace(string(snippet)))
	}

	var parsed rerankResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("decode rerank response: %w", err)
	}

	sort.SliceStable(parsed.Results, func(i, j int) bool {
		return parsed.Results[i].RelevanceScore > parsed.Results[j].RelevanceScore
	})

	out := make([]Revision, 0, len(parsed.Results))
	for _, r := range parsed.Results {
		if r.Index < 0 || r.Index >= len(candidates) {
			continue
		}
		out = append(out, candidates[r.Index])
		if topK > 0 && len(out) >= topK {
			break
		}
	}
	return out, nil
}
