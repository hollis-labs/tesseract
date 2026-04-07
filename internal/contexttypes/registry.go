package contexttypes

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// Status represents a context item lifecycle state.
type Status string

const (
	StatusDraft      Status = "draft"
	StatusReviewed   Status = "reviewed"
	StatusCanonical  Status = "canonical"
	StatusDeprecated Status = "deprecated"
)

// ValidStatuses is the canonical set of allowed status values.
var ValidStatuses = []Status{StatusDraft, StatusReviewed, StatusCanonical, StatusDeprecated}

// IsValidStatus reports whether s is a recognized status value.
func IsValidStatus(s string) bool {
	for _, v := range ValidStatuses {
		if string(v) == s {
			return true
		}
	}
	return false
}

// ContextType defines metadata and policy for a context type.
type ContextType struct {
	TypeID            string   `json:"type_id" yaml:"type_id"`
	DefaultTTL        string   `json:"default_ttl,omitempty" yaml:"default_ttl,omitempty"`
	AllowedStatuses   []string `json:"allowed_statuses,omitempty" yaml:"allowed_statuses,omitempty"`
	RequiredFields    []string `json:"required_fields,omitempty" yaml:"required_fields,omitempty"`
	MaxSummaryBytes   int      `json:"max_summary_bytes,omitempty" yaml:"max_summary_bytes,omitempty"`
	PromotionRules    []string `json:"promotion_rules,omitempty" yaml:"promotion_rules,omitempty"`
	RetrievalRankBias float64  `json:"retrieval_rank_bias,omitempty" yaml:"retrieval_rank_bias,omitempty"`
}

// ParseDefaultTTL returns the parsed default TTL duration, or zero if not set.
func (ct ContextType) ParseDefaultTTL() time.Duration {
	if ct.DefaultTTL == "" {
		return 0
	}
	d, err := time.ParseDuration(ct.DefaultTTL)
	if err != nil {
		return 0
	}
	return d
}

// HasAllowedStatus reports whether the given status is permitted for this type.
// If AllowedStatuses is empty, all valid statuses are permitted.
func (ct ContextType) HasAllowedStatus(s string) bool {
	if len(ct.AllowedStatuses) == 0 {
		return IsValidStatus(s)
	}
	for _, a := range ct.AllowedStatuses {
		if a == s {
			return true
		}
	}
	return false
}

// ViewDef defines a purpose-driven bounded retrieval preset.
type ViewDef struct {
	ViewID      string             `json:"view_id" yaml:"view_id"`
	Types       []string           `json:"types" yaml:"types"`
	MaxItems    int                `json:"max_items,omitempty" yaml:"max_items,omitempty"`
	MaxBytes    int                `json:"max_bytes,omitempty" yaml:"max_bytes,omitempty"`
	RankWeights map[string]float64 `json:"rank_weights,omitempty" yaml:"rank_weights,omitempty"`
}

// RegistryConfig is the on-disk format for context type configuration.
type RegistryConfig struct {
	Types []ContextType `json:"types" yaml:"types"`
	Views []ViewDef     `json:"views" yaml:"views"`
}

// Registry manages context types and views.
type Registry struct {
	mu    sync.RWMutex
	types map[string]ContextType
	views map[string]ViewDef
}

// NewRegistry creates a registry with the default core types and views.
func NewRegistry() *Registry {
	r := &Registry{
		types: make(map[string]ContextType),
		views: make(map[string]ViewDef),
	}
	for _, ct := range DefaultTypes() {
		r.types[ct.TypeID] = ct
	}
	for _, v := range DefaultViews() {
		r.views[v.ViewID] = v
	}
	return r
}

// LoadFromFile loads types and views from a YAML or JSON config file,
// merging with (and overriding) defaults.
func (r *Registry) LoadFromFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return r.LoadFromBytes(data)
}

// LoadFromBytes loads types and views from YAML or JSON bytes.
func (r *Registry) LoadFromBytes(data []byte) error {
	var cfg RegistryConfig
	// Try YAML first (superset of JSON).
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		// Fallback to JSON.
		if err2 := json.Unmarshal(data, &cfg); err2 != nil {
			return fmt.Errorf("config parse failed (yaml: %v, json: %v)", err, err2)
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, ct := range cfg.Types {
		if ct.TypeID == "" {
			return errors.New("type_id is required for every type entry")
		}
		r.types[ct.TypeID] = ct
	}
	for _, v := range cfg.Views {
		if v.ViewID == "" {
			return errors.New("view_id is required for every view entry")
		}
		r.views[v.ViewID] = v
	}
	return nil
}

// GetType returns the type definition for the given type_id.
func (r *Registry) GetType(typeID string) (ContextType, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ct, ok := r.types[typeID]
	return ct, ok
}

// GetView returns the view definition for the given view_id.
func (r *Registry) GetView(viewID string) (ViewDef, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	v, ok := r.views[viewID]
	return v, ok
}

// ListTypes returns all registered types sorted by type_id.
func (r *Registry) ListTypes() []ContextType {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]ContextType, 0, len(r.types))
	for _, ct := range r.types {
		out = append(out, ct)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TypeID < out[j].TypeID })
	return out
}

// ListViews returns all registered views sorted by view_id.
func (r *Registry) ListViews() []ViewDef {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]ViewDef, 0, len(r.views))
	for _, v := range r.views {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ViewID < out[j].ViewID })
	return out
}

// IsKnownType reports whether the given type_id is registered (core or custom).
// Custom types with prefix "custom/" are always accepted.
func (r *Registry) IsKnownType(typeID string) bool {
	if strings.HasPrefix(typeID, "custom/") {
		return true
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.types[typeID]
	return ok
}

// ValidateType validates that a type_id is known and returns its definition.
func (r *Registry) ValidateType(typeID string) error {
	if typeID == "" {
		return nil // type is optional
	}
	if !r.IsKnownType(typeID) {
		return fmt.Errorf("unknown context type: %q", typeID)
	}
	return nil
}

// ValidateStatus checks that the given status is valid for the type.
func (r *Registry) ValidateStatus(typeID, status string) error {
	if status == "" {
		return nil // status is optional (defaults to draft)
	}
	if !IsValidStatus(status) {
		return fmt.Errorf("invalid status: %q (valid: draft, reviewed, canonical, deprecated)", status)
	}
	if typeID == "" {
		return nil
	}
	ct, ok := r.GetType(typeID)
	if !ok {
		return nil // custom types accept all valid statuses
	}
	if !ct.HasAllowedStatus(status) {
		return fmt.Errorf("status %q not allowed for type %q (allowed: %s)",
			status, typeID, strings.Join(ct.AllowedStatuses, ", "))
	}
	return nil
}

// ValidateRequiredFields checks that all required fields for a registered type
// are present as top-level keys in the payload. Returns nil if the type has no
// required fields, is empty, or is a custom/ type.
func (r *Registry) ValidateRequiredFields(typeID string, payload map[string]any) error {
	if typeID == "" || strings.HasPrefix(typeID, "custom/") {
		return nil
	}
	ct, ok := r.GetType(typeID)
	if !ok || len(ct.RequiredFields) == 0 {
		return nil
	}
	var missing []string
	for _, f := range ct.RequiredFields {
		v, exists := payload[f]
		if !exists {
			missing = append(missing, f)
			continue
		}
		// Treat empty strings as missing.
		if s, ok := v.(string); ok && s == "" {
			missing = append(missing, f)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("type %q requires fields: %s", typeID, strings.Join(missing, ", "))
	}
	return nil
}

// DefaultTypes returns the MVP core types.
func DefaultTypes() []ContextType {
	return []ContextType{
		// Strategy
		{
			TypeID:            "strategy/goal",
			AllowedStatuses:   []string{"draft", "reviewed", "canonical", "deprecated"},
			RetrievalRankBias: 1.2,
		},
		{
			TypeID:            "strategy/constraints",
			AllowedStatuses:   []string{"draft", "reviewed", "canonical", "deprecated"},
			RetrievalRankBias: 1.1,
		},
		{
			TypeID:            "strategy/roadmap",
			AllowedStatuses:   []string{"draft", "reviewed", "canonical", "deprecated"},
			RetrievalRankBias: 1.0,
		},
		{
			TypeID:            "system/map",
			AllowedStatuses:   []string{"draft", "reviewed", "canonical", "deprecated"},
			RetrievalRankBias: 1.3,
		},
		// Execution
		{
			TypeID:            "task/spec",
			AllowedStatuses:   []string{"draft", "reviewed", "canonical", "deprecated"},
			RequiredFields:    []string{"title"},
			RetrievalRankBias: 1.0,
		},
		{
			TypeID:            "runbook",
			AllowedStatuses:   []string{"draft", "reviewed", "canonical", "deprecated"},
			RetrievalRankBias: 0.9,
		},
		{
			TypeID:            "contract/api",
			AllowedStatuses:   []string{"draft", "reviewed", "canonical", "deprecated"},
			RetrievalRankBias: 1.1,
		},
		{
			TypeID:            "contract/data",
			AllowedStatuses:   []string{"draft", "reviewed", "canonical", "deprecated"},
			RetrievalRankBias: 1.1,
		},
		// Knowledge
		{
			TypeID:            "decision/adr",
			AllowedStatuses:   []string{"draft", "reviewed", "canonical", "deprecated"},
			PromotionRules:    []string{"draft->reviewed:requires_human_approval"},
			RetrievalRankBias: 1.4,
		},
		{
			TypeID:            "brief/summary",
			DefaultTTL:        "2160h", // 90 days
			AllowedStatuses:   []string{"draft", "reviewed", "canonical", "deprecated"},
			RetrievalRankBias: 0.8,
		},
		{
			TypeID:            "note/volatile",
			DefaultTTL:        "336h", // 14 days
			AllowedStatuses:   []string{"draft"},
			RetrievalRankBias: 0.5,
		},
		// Governance
		{
			TypeID:            "principles",
			AllowedStatuses:   []string{"draft", "reviewed", "canonical", "deprecated"},
			RetrievalRankBias: 1.5,
		},
		// Session
		{
			TypeID:            "session/snapshot",
			DefaultTTL:        "720h", // 30 days
			AllowedStatuses:   []string{"draft", "reviewed", "canonical", "deprecated"},
			RequiredFields:    []string{"summary"},
			RetrievalRankBias: 0.7,
		},
		// Configuration
		{
			TypeID:            "config/service",
			AllowedStatuses:   []string{"draft", "reviewed", "canonical", "deprecated"},
			RetrievalRankBias: 1.0,
		},
		// Identity
		{
			TypeID:            "project/identity",
			AllowedStatuses:   []string{"draft", "reviewed", "canonical", "deprecated"},
			RequiredFields:    []string{"name"},
			RetrievalRankBias: 1.2,
		},
	}
}

// DefaultViews returns the MVP view presets.
func DefaultViews() []ViewDef {
	return []ViewDef{
		{
			ViewID:   "task_exec",
			Types:    []string{"task/spec", "contract/api", "contract/data", "decision/adr", "runbook", "system/map"},
			MaxItems: 50,
			RankWeights: map[string]float64{
				"canonical":  1.0,
				"reviewed":   0.8,
				"draft":      0.5,
				"deprecated": 0.1,
			},
		},
		{
			ViewID:   "strategy",
			Types:    []string{"strategy/goal", "strategy/constraints", "strategy/roadmap", "decision/adr", "system/map"},
			MaxItems: 30,
			RankWeights: map[string]float64{
				"canonical":  1.0,
				"reviewed":   0.8,
				"draft":      0.5,
				"deprecated": 0.1,
			},
		},
		{
			ViewID:   "agent_boot",
			Types:    []string{"system/map", "principles", "strategy/constraints", "contract/api", "contract/data"},
			MaxItems: 20,
			RankWeights: map[string]float64{
				"canonical":  1.0,
				"reviewed":   0.9,
				"draft":      0.4,
				"deprecated": 0.0,
			},
		},
		{
			ViewID:   "briefing",
			Types:    []string{"brief/summary", "decision/adr", "system/map", "strategy/goal"},
			MaxItems: 25,
			RankWeights: map[string]float64{
				"canonical":  1.0,
				"reviewed":   0.8,
				"draft":      0.5,
				"deprecated": 0.1,
			},
		},
	}
}
