// Package contextapi — synthesis handler.
//
// POST /v1/synthesis/ask is the LLM-backed answer surface for the
// Search & Research page (v2). It runs the same memory.Recall-style
// curation the existing /v1/tesseract/lookup endpoint does, then fans
// the curated results into a go-providers Complete() call with a
// fixed-shape system prompt. The response carries the synthesised
// answer, the cited sources (so the frontend can resolve [n] markers),
// and per-call token / cost telemetry resolved through go-modelsdev.
//
// Returns 503 service_unavailable when SynthesisProvider is nil
// (no provider configured / no API key).
package contextapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/hollis-labs/go-modelsdev/modelsdev"
	"github.com/hollis-labs/tesseract/internal/config"
	"github.com/hollis-labs/tesseract/internal/memory"
)

// synthesisAskRequest is the wire-shape for /v1/synthesis/ask.
type synthesisAskRequest struct {
	Question      string          `json:"question"`
	Namespaces    []string        `json:"namespaces,omitempty"`
	Tags          []string        `json:"tags,omitempty"`
	Limit         int             `json:"limit,omitempty"`
	Domains       []string        `json:"domains,omitempty"` // ["memory","knowledge"]
	Statuses      []memory.Status `json:"statuses,omitempty"`
	ConfidenceMin float64         `json:"confidence_min,omitempty"`
	// ModelOverride lets a caller pin a specific model for one call. Empty
	// uses the server's configured default.
	ModelOverride string `json:"model,omitempty"`
}

// synthesisSource is a numbered source carried back so the frontend can
// resolve [n] citation markers in the answer.
type synthesisSource struct {
	N          int     `json:"n"`
	RevisionID string  `json:"revision_id"`
	MemoryID   string  `json:"memory_id"`
	Domain     string  `json:"domain"`
	Namespace  string  `json:"namespace"`
	MemoryKey  string  `json:"memory_key,omitempty"`
	Summary    string  `json:"summary"`
	Confidence float64 `json:"confidence"`
	// Score mirrors memory.RecallResult.Score — ranking-relative, and
	// absent when the ranking mode produces none.
	Score *float64 `json:"score,omitempty"`
}

// synthesisCost reports per-call cost. Zero when ModelsDev lookup fails.
type synthesisCost struct {
	InputUSD  float64 `json:"input_usd"`
	OutputUSD float64 `json:"output_usd"`
	TotalUSD  float64 `json:"total_usd"`
}

// synthesisUsage carries token + cost + latency telemetry alongside the
// resolved provider/model so the UI can show what backed the answer.
type synthesisUsage struct {
	Provider     string         `json:"provider"`
	Model        string         `json:"model"`
	InputTokens  int            `json:"input_tokens"`
	OutputTokens int            `json:"output_tokens"`
	LatencyMS    int64          `json:"latency_ms"`
	Cost         *synthesisCost `json:"cost,omitempty"`
}

type synthesisAskResponse struct {
	Answer  string            `json:"answer"`
	Sources []synthesisSource `json:"sources"`
	Usage   synthesisUsage    `json:"usage"`
}

func (s *Server) handleSynthesisAsk(w http.ResponseWriter, r *http.Request) {
	if s.SynthesisProvider == nil {
		writeError(w, http.StatusServiceUnavailable, "synthesis_unavailable",
			"synthesis provider is not configured (set synthesis.provider in config.yaml + the matching API key env var)", nil)
		return
	}
	if s.MemoryStore == nil {
		writeError(w, http.StatusServiceUnavailable, "memory_unavailable",
			"memory subsystem not wired into this server", nil)
		return
	}

	var req synthesisAskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", "malformed JSON: "+err.Error(), nil)
		return
	}
	if strings.TrimSpace(req.Question) == "" {
		writeError(w, http.StatusBadRequest, "validation_error", "question is required", nil)
		return
	}
	if len(req.Namespaces) == 0 {
		writeError(w, http.StatusBadRequest, "validation_error", "at least one namespace is required", nil)
		return
	}

	limit := req.Limit
	if limit <= 0 {
		limit = 10
	}
	if limit > 50 {
		limit = 50
	}

	// 1. Recall — same shape memory + knowledge results tesseract_recall uses.
	recallIn := memory.RecallInput{
		Namespaces:    req.Namespaces,
		Query:         req.Question,
		Ranking:       memory.RankingRelevance,
		Limit:         limit,
		RevisionScope: memory.RevisionScopeCurrent,
		Filters: memory.RecallFilters{
			Tags:          req.Tags,
			ConfidenceMin: req.ConfidenceMin,
		},
	}
	if len(req.Statuses) > 0 {
		recallIn.Filters.Statuses = req.Statuses
	}
	results, err := s.MemoryStore.Recall(r.Context(), recallIn)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "recall_failed", err.Error(), nil)
		return
	}

	if len(results) == 0 {
		// Don't burn tokens on empty contexts — return a deterministic
		// "no sources" answer so the UI still renders the Synthesis tab.
		writeJSON(w, http.StatusOK, synthesisAskResponse{
			Answer:  "No sources matched the question for the given namespaces / filters.",
			Sources: []synthesisSource{},
			Usage: synthesisUsage{
				Provider: s.SynthesisConfig.Provider,
				Model:    pickModel(s.SynthesisConfig, req.ModelOverride),
			},
		})
		return
	}

	// 2. Build numbered sources + the prompt body.
	sources := make([]synthesisSource, 0, len(results))
	var promptB strings.Builder
	for i, rr := range results {
		n := i + 1
		sources = append(sources, synthesisSource{
			N:          n,
			RevisionID: rr.Revision.RevisionID,
			MemoryID:   rr.Revision.MemoryID,
			Domain:     string(rr.Revision.Domain),
			Namespace:  rr.Revision.Namespace,
			MemoryKey:  rr.Revision.MemoryKey,
			Summary:    rr.Revision.Payload.Summary,
			Confidence: rr.Revision.Confidence,
			Score:      rr.Score,
		})
		fmt.Fprintf(&promptB, "[%d] domain=%s namespace=%s key=%s\n",
			n, rr.Revision.Domain, rr.Revision.Namespace, rr.Revision.MemoryKey)
		if rr.Revision.Payload.Summary != "" {
			fmt.Fprintf(&promptB, "  Summary: %s\n", rr.Revision.Payload.Summary)
		}
		if rr.Revision.Payload.Body != "" {
			fmt.Fprintf(&promptB, "  Body: %s\n", rr.Revision.Payload.Body)
		}
		promptB.WriteString("\n")
	}

	model := pickModel(s.SynthesisConfig, req.ModelOverride)
	chatReq := llmtypes.ChatRequest{
		Model:        model,
		SystemPrompt: s.SynthesisConfig.SystemPrompt,
		MaxTokens:    s.SynthesisConfig.MaxTokens,
		Messages: []llmtypes.ChatMessage{
			{
				Role: "user",
				Content: fmt.Sprintf(
					"Question:\n%s\n\nSources:\n%s",
					strings.TrimSpace(req.Question),
					promptB.String(),
				),
			},
		},
	}

	// 3. Call the provider. Complete is non-streaming — synthesis answers are
	// short and the v1 UI shows them in a single card; streaming is a v2.1
	// concern.
	start := time.Now()
	answer, err := s.SynthesisProvider.Complete(r.Context(), chatReq)
	latencyMS := time.Since(start).Milliseconds()
	if err != nil {
		writeError(w, http.StatusBadGateway, "synthesis_failed", err.Error(), nil)
		return
	}

	// 4. Resolve token usage + cost. go-providers' Complete() doesn't return
	// usage today (only StreamChat exposes a "usage" event), so v1 reports
	// only latency + provider/model. When upstream surfaces a Complete-side
	// usage signal, this block fills in tokens + cost via go-modelsdev.
	usage := synthesisUsage{
		Provider:  s.SynthesisConfig.Provider,
		Model:     model,
		LatencyMS: latencyMS,
	}
	// (Tokens currently unavailable from non-streaming Complete; cost stays nil.)

	writeJSON(w, http.StatusOK, synthesisAskResponse{
		Answer:  strings.TrimSpace(answer),
		Sources: sources,
		Usage:   usage,
	})
}

func pickModel(cfg config.SynthesisConfig, override string) string {
	if strings.TrimSpace(override) != "" {
		return strings.TrimSpace(override)
	}
	return cfg.Model
}

// computeCost is wired through go-modelsdev when token usage is available.
// Kept here so the Pricing field reference exists in the binary — the
// catalog refresh runs in main even if computeCost goes unused under v1.
func computeCost(client *modelsdev.Client, providerID, modelID string, inTokens, outTokens int) *synthesisCost {
	if client == nil {
		return nil
	}
	m, ok := client.Get(providerID, modelID)
	if !ok {
		return nil
	}
	in := float64(inTokens) / 1_000_000.0 * m.Cost.Input
	out := float64(outTokens) / 1_000_000.0 * m.Cost.Output
	return &synthesisCost{InputUSD: in, OutputUSD: out, TotalUSD: in + out}
}
