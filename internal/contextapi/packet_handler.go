package contextapi

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/hollis-labs/vanta-conduit/internal/contextstore"
)

// PacketTimeWindow bounds records by creation time.
type PacketTimeWindow struct {
	Since string `json:"since,omitempty"` // RFC3339
	Until string `json:"until,omitempty"` // RFC3339
}

// PacketBudget limits result size.
type PacketBudget struct {
	MaxItems          int `json:"max_items,omitempty"`
	MaxBytes          int `json:"max_bytes,omitempty"`
	MaxTokensEstimate int `json:"max_tokens_estimate,omitempty"`
}

// PacketShape controls payload inclusion.
type PacketShape struct {
	IncludePayload bool   `json:"include_payload"`
	PayloadMode    string `json:"payload_mode,omitempty"` // full|head_only
}

// PacketAssembly configures packet assembly beyond basic selection.
type PacketAssembly struct {
	IncludePins   bool             `json:"include_pins"`
	TimeWindow    PacketTimeWindow `json:"time_window,omitempty"`
	Budget        PacketBudget     `json:"budget,omitempty"`
	Shape         PacketShape      `json:"shape,omitempty"`
	ManifestLevel string           `json:"manifest_level,omitempty"` // summary|full
}

// PacketRequest is the body for POST /v1/context/packet.
type PacketRequest struct {
	Selector contextstore.Selector `json:"selector"`
	Assembly PacketAssembly        `json:"assembly"`
}

// PacketItem is one record in the packet response.
type PacketItem struct {
	RecordID  string          `json:"record_id"`
	Namespace string          `json:"namespace"`
	Key       string          `json:"key"`
	Revision  int64           `json:"revision"`
	Actor     string          `json:"actor"`
	CreatedAt string          `json:"created_at"`
	Payload   json.RawMessage `json:"payload,omitempty"`
}

// PacketManifest describes what was assembled and why.
type PacketManifest struct {
	RequestID        string         `json:"request_id"`
	PinsIncluded     int            `json:"pins_included"`
	ItemsTotal       int            `json:"items_total"`
	ItemsReturned    int            `json:"items_returned"`
	BytesReturned    int            `json:"bytes_returned"`
	TokensEstimate   int            `json:"tokens_estimate"`
	Truncated        bool           `json:"truncated"`
	TruncationReason string         `json:"truncation_reason,omitempty"`
	Sources          map[string]int `json:"sources"`
}

// PacketResponse is the response for POST /v1/context/packet.
type PacketResponse struct {
	Items    []PacketItem   `json:"items"`
	Manifest PacketManifest `json:"manifest"`
}

func newRequestID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "req-" + hex.EncodeToString(b)
}

func (s *Server) handlePacket(w http.ResponseWriter, r *http.Request) {
	// Apply defaults before decoding so the caller can omit fields.
	req := PacketRequest{
		Assembly: PacketAssembly{
			IncludePins:   true,
			ManifestLevel: "summary",
			Shape: PacketShape{
				IncludePayload: true,
				PayloadMode:    "full",
			},
		},
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", err.Error(), nil)
		return
	}

	ctx := r.Context()
	reqID := newRequestID()
	sources := map[string]int{}
	var items []PacketItem
	pinsIncluded := 0
	bytesSoFar := 0
	tokensSoFar := 0
	itemsTotal := 0
	truncated := false
	truncationReason := ""

	budget := req.Assembly.Budget
	shape := req.Assembly.Shape

	budgetExceeded := func() bool {
		if budget.MaxItems > 0 && len(items) >= budget.MaxItems {
			truncationReason = "budget.max_items"
			return true
		}
		if budget.MaxBytes > 0 && bytesSoFar >= budget.MaxBytes {
			truncationReason = "budget.max_bytes"
			return true
		}
		if budget.MaxTokensEstimate > 0 && tokensSoFar >= budget.MaxTokensEstimate {
			truncationReason = "budget.max_tokens_estimate"
			return true
		}
		return false
	}

	addRecord := func(rec contextstore.Record) bool {
		if budgetExceeded() {
			return false
		}
		payload := rec.Payload
		if shape.PayloadMode == "head_only" && len(payload) > 512 {
			payload = payload[:512]
		}
		item := PacketItem{
			RecordID:  rec.RecordID,
			Namespace: rec.Namespace,
			Key:       rec.Key,
			Revision:  rec.Revision,
			Actor:     rec.Actor,
			CreatedAt: rec.CreatedAt,
		}
		if shape.IncludePayload {
			item.Payload = payload
		}
		items = append(items, item)
		bytesSoFar += len(payload)
		tokensSoFar += contextstore.EstimateTokens(payload)
		// Aggregate sources by top two namespace segments.
		parts := strings.SplitN(rec.Namespace, "/", 3)
		nsKey := rec.Namespace
		if len(parts) >= 2 {
			nsKey = parts[0] + "/" + parts[1]
		}
		sources[nsKey]++
		return true
	}

	inTimeWindow := func(rec contextstore.Record) bool {
		tw := req.Assembly.TimeWindow
		if tw.Since != "" && rec.CreatedAt < tw.Since {
			return false
		}
		if tw.Until != "" && rec.CreatedAt > tw.Until {
			return false
		}
		return true
	}

	// Step 1: prepend user/pins/* when include_pins is true.
	if req.Assembly.IncludePins {
		pinSel := contextstore.Selector{
			Namespaces:    []string{"user/pins/*"},
			RevisionScope: "head",
		}
		if pins, err := s.Store.Select(ctx, pinSel); err == nil {
			for _, rec := range pins {
				if inTimeWindow(rec) && addRecord(rec) {
					pinsIncluded++
				}
			}
		}
	}

	// Step 2: run main selector.
	candidates, err := s.Store.Select(ctx, req.Selector)
	if err != nil {
		writeError(w, http.StatusBadRequest, "selector_error", err.Error(), nil)
		return
	}

	// Step 3: count total qualifying candidates, then walk with budget enforcement.
	for _, rec := range candidates {
		if inTimeWindow(rec) {
			itemsTotal++
		}
	}
	for _, rec := range candidates {
		if !inTimeWindow(rec) {
			continue
		}
		if !addRecord(rec) {
			truncated = true
			break
		}
	}

	manifest := PacketManifest{
		RequestID:        reqID,
		PinsIncluded:     pinsIncluded,
		ItemsTotal:       itemsTotal,
		ItemsReturned:    len(items) - pinsIncluded,
		BytesReturned:    bytesSoFar,
		TokensEstimate:   tokensSoFar,
		Truncated:        truncated,
		TruncationReason: truncationReason,
		Sources:          sources,
	}

	_ = s.Store.EmitPacket(ctx, packetActor(r), strings.Join(req.Selector.Namespaces, ","), reqID,
		json.RawMessage(fmt.Sprintf(`{"request_id":%q,"items_returned":%d}`, reqID, manifest.ItemsReturned)))

	if items == nil {
		items = []PacketItem{}
	}
	writeJSON(w, http.StatusOK, PacketResponse{Items: items, Manifest: manifest})
}

// EstimateRequest is the body for POST /v1/context/estimate.
type EstimateRequest struct {
	Selector contextstore.Selector `json:"selector"`
}

// EstimateResponse is the response for POST /v1/context/estimate.
type EstimateResponse struct {
	RecordCount         int `json:"record_count"`
	TotalBytes          int `json:"total_bytes"`
	TotalTokensEstimate int `json:"total_tokens_estimate"`
}

func (s *Server) handleEstimate(w http.ResponseWriter, r *http.Request) {
	var req EstimateRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", err.Error(), nil)
		return
	}
	result, err := s.Store.Estimate(r.Context(), req.Selector)
	if err != nil {
		writeError(w, http.StatusBadRequest, "selector_error", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, EstimateResponse(result))
}

// packetActor extracts an actor identifier from the request or returns "anonymous".
func packetActor(r *http.Request) string {
	if v := r.Header.Get("X-Actor"); v != "" {
		return v
	}
	return "anonymous"
}
