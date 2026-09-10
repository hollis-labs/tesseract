// Package typeregistry is the one place Tesseract declares its type
// vocabularies, for every domain.
//
// It is the promotion of internal/contexttypes (CW-20260909-0034, per
// [[tesseract_type_registry_promoted]]). That package held the only genuinely
// config-driven type system in the tree and pointed it at the least-used
// store; three more vocabularies were spelled three other ways. This package
// is the same loader aimed at all of them.
//
// # The seam
//
// Domain and type are different axes and this package owns only the second.
// A DOMAIN selects storage policy — what a write means, whether its rows decay,
// which namespace shapes are legal — and lives in Go, in memory.DomainPolicy.
// A TYPE classifies within a domain, and lives here, in a declaration.
// CW-20260909-0033 built the first; this is the second, and conflating them is
// the mistake both seams exist to prevent.
//
// # Vocabularies, not one flat list
//
// Three axes fold in here, per [[tesseract_vocabularies_fold_into_registry]]:
//
//	context.record_type      the 15 context record types (was DefaultTypes)
//	memory.type              the 8 memory namespace {type} segments
//	                         (was memory.DefaultTypeAllowlist, a Go slice)
//	knowledge.facet_kind     the 12 knowledge kinds
//	                         (was a closed Go map in internal/memory/kinds.go)
//
// Before this, four mechanisms defined what a type could be. Now there are
// two: domain in Go, everything else declared here.
//
// # This moves enforcement authority from code to config
//
// Deliberately, and it was the point rather than a side effect. Under
// [[config_is_policy_code_is_engine]] code is an engine and domain logic
// belongs in a declarative artifact: "we provide the enforcement tools, users
// provide the policy." Enforcement does not weaken in kind — the write
// boundary still rejects what the vocabulary forbids, and memory.WriteRevision
// is still the place it happens. What moved is WHICH VALUES ARE ALLOWED, out
// of our binary and into a file an operator can read and revise.
//
// The cost, stated plainly because it was accepted rather than overlooked: a
// bad edit to types.yaml can open a vocabulary governance says is closed, where
// before it took a compile. cmd/tesseract refuses to boot on a malformed
// registry file rather than falling back to defaults, which is the mitigation
// that fits — see loadTypeRegistry there for why that differs from config.yaml.
//
// # The loader has no DDL path
//
// A Type may DECLARE hot_fields. This package must never turn that into
// schema. Two config files producing two schemas from one binary would break
// the reproducibility the ordered migration list in internal/contextstore
// exists to give; indexes materialize there, where a human reviews the
// statement, exactly as Seer did for the mechanism it invented.
//
// That is [[tesseract_registry_index_ddl_constrained]], operator gate G2, and
// it is asserted rather than trusted: no_ddl_test.go proves this package
// imports no database and spells no schema statement.
package typeregistry

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"go.yaml.in/yaml/v3"
)

// Vocabulary IDs. These name the three axes a value can be classified on and
// are the key every lookup takes, so a caller cannot ask "is this a known
// type" without saying known to what.
const (
	// VocabContextRecordType is the context store's record_type vocabulary.
	VocabContextRecordType = "context.record_type"

	// VocabMemoryType is the {type} segment of a memory namespace.
	VocabMemoryType = "memory.type"

	// VocabKnowledgeFacetKind is the knowledge domain's facet_kind.
	VocabKnowledgeFacetKind = "knowledge.facet_kind"
)

// SchemaRef pins a type's payload schema to a file on disk.
//
// The schema is NOT inlined, following Seer: the document lives as a reviewable
// file and the registry carries a path plus a digest. The digest is what keeps
// a stored record validatable after the catalog moves on — it detects drift
// instead of silently validating against a schema that changed underneath.
//
// Nothing consumes this yet. Structured objects (CW-20260909-0036) are the
// consumer; the field is declared here because the field set was settled as a
// whole in [[tesseract_type_declaration_field_set]], and adding it later would
// mean revisiting every declaration.
type SchemaRef struct {
	SourcePath string `json:"source_path" yaml:"source_path"`
	SchemaHash string `json:"schema_hash" yaml:"schema_hash"`
}

// Type is one declaration in one vocabulary.
//
// The field set is closed by [[tesseract_type_declaration_field_set]] and the
// two omissions are as load-bearing as the members:
//
//   - No retrieval_rank_bias. A per-type thumb on the scale inside recall is
//     unfixable from a bad result, because nobody debugging a ranking
//     complaint thinks to check a type declaration. The ranking concern sits
//     on the DOMAIN axis, where [[tesseract_event_is_a_domain]] put it.
//
//   - No promotion_rules. Workflow authority does not belong to a type.
//
//   - No presentation metadata — display_name, icon, color, list_columns,
//     filters, actions, default_sort. Seer's registry accreted exactly that
//     second wave, so the pull is real and will recur. Under the service /
//     product seam it belongs to the product, per
//     [[tesseract_stores_wiki_page_content]]: "we might project specific
//     taxonomy views in GUI/UI stuff but that's not Tesseract's concern."
type Type struct {
	TypeID          string   `json:"type_id" yaml:"type_id"`
	DefaultTTL      string   `json:"default_ttl,omitempty" yaml:"default_ttl,omitempty"`
	AllowedStatuses []string `json:"allowed_statuses,omitempty" yaml:"allowed_statuses,omitempty"`
	RequiredFields  []string `json:"required_fields,omitempty" yaml:"required_fields,omitempty"`
	MaxSummaryBytes int      `json:"max_summary_bytes,omitempty" yaml:"max_summary_bytes,omitempty"`

	// HotFields names payload fields worth indexing. DECLARATIVE ONLY — see
	// the package comment. Materializing one is a migration in
	// internal/contextstore, written and reviewed by a human.
	HotFields []string `json:"hot_fields,omitempty" yaml:"hot_fields,omitempty"`

	SchemaRef *SchemaRef `json:"schema_ref,omitempty" yaml:"schema_ref,omitempty"`
}

// ParseDefaultTTL returns the parsed default TTL duration, or zero if unset.
//
// The unparseable case cannot arrive from a loaded config — validateConfig
// refuses a default_ttl that is not a Go duration, so a typo is a load error
// rather than a silent zero. It stays lenient here for a Type built in Go,
// where the value is a literal a compiler and TestShippedDefaultsParse both
// see.
func (t Type) ParseDefaultTTL() time.Duration {
	if t.DefaultTTL == "" {
		return 0
	}
	d, err := time.ParseDuration(t.DefaultTTL)
	if err != nil {
		return 0
	}
	return d
}

// HasAllowedStatus reports whether s is permitted for this type. An empty
// AllowedStatuses means every valid status is permitted.
func (t Type) HasAllowedStatus(s string) bool {
	if len(t.AllowedStatuses) == 0 {
		return IsValidStatus(s)
	}
	for _, a := range t.AllowedStatuses {
		if a == s {
			return true
		}
	}
	return false
}

// Vocabulary is one axis of classification and the governance around it.
//
// Closed is the first-class governance property this promotion adds. Closure
// used to be an accident of implementation — knowledge kinds were closed
// because someone reached for a map, memory types were closed because the
// parser rejected what was not in a slice, context types were neither because
// IsKnownType had an escape hatch. Now a vocabulary DECLARES whether it is
// closed, and the difference is visible in one place rather than inferred from
// three.
//
// OpenPrefixes is the faithful record of a shape that is neither open nor
// flatly closed: context.record_type rejects an unregistered type but has
// always accepted anything under `custom/`, so an app can carry its own types
// without a release. Modeling that as Closed=false would open the vocabulary
// entirely, which is a behavior change hiding inside a move — the one thing
// this slice was told not to do. Memory and knowledge declare no prefixes.
type Vocabulary struct {
	VocabularyID string   `json:"vocabulary_id" yaml:"vocabulary_id"`
	Closed       bool     `json:"closed" yaml:"closed"`
	OpenPrefixes []string `json:"open_prefixes,omitempty" yaml:"open_prefixes,omitempty"`
	Types        []Type   `json:"types" yaml:"types"`
}

// ViewDef is a purpose-driven bounded retrieval preset over context records.
//
// Views ride along with the registry rather than being promoted to a general
// concept: they are a context-surface affordance and retire with that store
// (CW-20260909-0037). RankWeights is a ranking knob and survives the cull that
// dropped retrieval_rank_bias because it is not the same thing — a view is a
// preset the CALLER selects by name, not an ambient per-type bias applied to
// every recall whether or not anyone asked.
type ViewDef struct {
	ViewID      string             `json:"view_id" yaml:"view_id"`
	Types       []string           `json:"types" yaml:"types"`
	MaxItems    int                `json:"max_items,omitempty" yaml:"max_items,omitempty"`
	MaxBytes    int                `json:"max_bytes,omitempty" yaml:"max_bytes,omitempty"`
	RankWeights map[string]float64 `json:"rank_weights,omitempty" yaml:"rank_weights,omitempty"`
}

// RegistryConfig is the on-disk format, shipped as types.yaml alongside
// config.yaml. A file need only carry what it changes; everything absent keeps
// its built-in default.
type RegistryConfig struct {
	Vocabularies []Vocabulary `json:"vocabularies" yaml:"vocabularies"`
	Views        []ViewDef    `json:"views" yaml:"views"`
}

// vocabEntry is a Vocabulary with an index over its types for O(1) lookup.
type vocabEntry struct {
	closed       bool
	openPrefixes []string
	order        []string
	types        map[string]Type
}

// Registry holds the loaded vocabularies and views. Safe for concurrent use.
type Registry struct {
	mu     sync.RWMutex
	vocabs map[string]*vocabEntry
	views  map[string]ViewDef
}

// NewRegistry returns a registry carrying the built-in defaults.
//
// The defaults are in Go rather than in a file that must exist because a fresh
// install has to work with no configuration at all, and because the shipped
// vocabulary is what governance signed off on. types.yaml revises that; it does
// not have to restate it.
func NewRegistry() *Registry {
	r := &Registry{
		vocabs: make(map[string]*vocabEntry),
		views:  make(map[string]ViewDef),
	}
	for _, v := range DefaultVocabularies() {
		r.putVocabulary(v)
	}
	for _, v := range DefaultViews() {
		r.views[v.ViewID] = v
	}
	return r
}

// putVocabulary installs v, replacing any vocabulary already under that ID.
// Caller holds the lock, or holds the registry exclusively (construction).
//
// Replace rather than merge is deliberate: a merge would make it impossible to
// REMOVE a type by editing the file, which is half of what owning the policy
// means. An operator narrowing a vocabulary is the case that has to work.
func (r *Registry) putVocabulary(v Vocabulary) {
	e := &vocabEntry{
		closed:       v.Closed,
		openPrefixes: append([]string(nil), v.OpenPrefixes...),
		order:        make([]string, 0, len(v.Types)),
		types:        make(map[string]Type, len(v.Types)),
	}
	for _, t := range v.Types {
		if _, dup := e.types[t.TypeID]; !dup {
			e.order = append(e.order, t.TypeID)
		}
		e.types[t.TypeID] = t
	}
	sort.Strings(e.order)
	r.vocabs[v.VocabularyID] = e
}

// LoadVocabulary installs a vocabulary built in process, replacing whatever
// is registered under that ID. Validation matches the file loader — an
// in-process caller gets the same refusals a types.yaml would.
func (r *Registry) LoadVocabulary(v Vocabulary) error {
	if err := validateConfig(RegistryConfig{Vocabularies: []Vocabulary{v}}); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.putVocabulary(v)
	return nil
}

// LoadFromFile loads a registry config from a YAML or JSON file, replacing any
// vocabulary the file names and leaving the rest at their defaults.
func (r *Registry) LoadFromFile(path string) error {
	// #nosec G304 -- path is the operator's own types.yaml, resolved by
	// cmd/tesseract from the go-apppaths config dir. Reading a file the
	// operator names is the feature.
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := r.LoadFromBytes(data); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

// LoadFromBytes loads a registry config from YAML or JSON bytes. YAML is a
// superset of JSON, so one decoder reads both.
//
// UNKNOWN FIELDS ARE AN ERROR. This is a governance artifact, and a silently
// ignored key is the failure mode that matters most here: `close: true` for
// `closed: true` would leave a vocabulary open while reading, to whoever wrote
// it, exactly like closing it. The same strictness is what stops a dropped
// knob from looking alive — a file declaring `retrieval_rank_bias` is refused
// rather than loaded and ignored.
//
// The load is atomic: a file with one bad entry changes nothing. A partially
// applied vocabulary would leave the process enforcing a list that exists in
// neither the defaults nor the file, which is the state nobody can reason
// about.
func (r *Registry) LoadFromBytes(data []byte) error {
	cfg, err := parseRegistryConfig(data)
	if err != nil {
		return err
	}
	if err := validateConfig(cfg); err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	for _, v := range cfg.Vocabularies {
		r.putVocabulary(v)
	}
	for _, v := range cfg.Views {
		r.views[v.ViewID] = v
	}
	return nil
}

// parseRegistryConfig decodes YAML/JSON with unknown fields refused. An empty
// document is not an error: it declares no change, which is a legitimate thing
// for a file to say.
func parseRegistryConfig(data []byte) (RegistryConfig, error) {
	var cfg RegistryConfig
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		if errors.Is(err, io.EOF) {
			return RegistryConfig{}, nil
		}
		return RegistryConfig{}, fmt.Errorf("config parse failed: %w", err)
	}
	return cfg, nil
}

// hotFieldRE constrains a declared hot field to a bare identifier.
//
// Nothing in this package can emit DDL, so this is not what stops a SQL
// injection today — TestRegistryLoaderHasNoDDLPath is. It is what keeps the
// constraint true if a future migration generator reads these names: a value
// that cannot be anything but an identifier cannot become a statement, whoever
// picks it up.
var hotFieldRE = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// schemaHashRE matches a hex sha256 digest.
var schemaHashRE = regexp.MustCompile(`^[0-9a-f]{64}$`)

func validateConfig(cfg RegistryConfig) error {
	seenVocab := make(map[string]struct{}, len(cfg.Vocabularies))
	for _, v := range cfg.Vocabularies {
		if v.VocabularyID == "" {
			return errors.New("vocabulary_id is required for every vocabulary entry")
		}
		if _, dup := seenVocab[v.VocabularyID]; dup {
			return fmt.Errorf("vocabulary %q declared twice", v.VocabularyID)
		}
		seenVocab[v.VocabularyID] = struct{}{}
		for _, t := range v.Types {
			if err := validateType(v.VocabularyID, t); err != nil {
				return err
			}
		}
	}
	seenView := make(map[string]struct{}, len(cfg.Views))
	for _, v := range cfg.Views {
		if v.ViewID == "" {
			return errors.New("view_id is required for every view entry")
		}
		// Refused for the same reason a duplicate vocabulary is: last-one-wins
		// on a file that carries policy means the effective config is not the
		// one an operator reads top to bottom.
		if _, dup := seenView[v.ViewID]; dup {
			return fmt.Errorf("view %q declared twice", v.ViewID)
		}
		seenView[v.ViewID] = struct{}{}
	}
	return nil
}

func validateType(vocabID string, t Type) error {
	if t.TypeID == "" {
		return fmt.Errorf("vocabulary %q: type_id is required for every type entry", vocabID)
	}
	for _, f := range t.HotFields {
		if !hotFieldRE.MatchString(f) {
			return fmt.Errorf("vocabulary %q type %q: hot field %q must be a lowercase identifier",
				vocabID, t.TypeID, f)
		}
	}
	if t.SchemaRef != nil {
		if t.SchemaRef.SourcePath == "" || t.SchemaRef.SchemaHash == "" {
			return fmt.Errorf("vocabulary %q type %q: schema_ref needs both source_path and schema_hash",
				vocabID, t.TypeID)
		}
		if !schemaHashRE.MatchString(t.SchemaRef.SchemaHash) {
			return fmt.Errorf("vocabulary %q type %q: schema_hash %q is not a hex sha256 digest",
				vocabID, t.TypeID, t.SchemaRef.SchemaHash)
		}
	}
	// A default_ttl that does not parse is refused rather than accepted and
	// silently read as zero. ParseDefaultTTL swallows the error and answers 0,
	// so "24hr" for "24h" would load clean and quietly mean NO EXPIRY — a
	// retention change from a typo, on a file where unknown keys are already
	// fatal. Validate once, here, where the operator can be told.
	if t.DefaultTTL != "" {
		d, err := time.ParseDuration(t.DefaultTTL)
		if err != nil {
			return fmt.Errorf("vocabulary %q type %q: default_ttl %q is not a Go duration (e.g. \"336h\"): %w",
				vocabID, t.TypeID, t.DefaultTTL, err)
		}
		if d < 0 {
			return fmt.Errorf("vocabulary %q type %q: default_ttl %q must not be negative",
				vocabID, t.TypeID, t.DefaultTTL)
		}
	}
	if t.MaxSummaryBytes < 0 {
		return fmt.Errorf("vocabulary %q type %q: max_summary_bytes must not be negative",
			vocabID, t.TypeID)
	}
	return nil
}

// ---- Vocabulary-generic lookups ------------------------------------------

// Lookup returns the declaration for value in the named vocabulary.
func (r *Registry) Lookup(vocabID, value string) (Type, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.vocabs[vocabID]
	if !ok {
		return Type{}, false
	}
	t, ok := e.types[value]
	return t, ok
}

// Values returns every declared value in the vocabulary, sorted. Returns nil
// for an unknown vocabulary.
func (r *Registry) Values(vocabID string) []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.vocabs[vocabID]
	if !ok {
		return nil
	}
	return append([]string(nil), e.order...)
}

// Types returns every declaration in the vocabulary, sorted by type_id.
func (r *Registry) Types(vocabID string) []Type {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.vocabs[vocabID]
	if !ok {
		return nil
	}
	out := make([]Type, 0, len(e.order))
	for _, id := range e.order {
		out = append(out, e.types[id])
	}
	return out
}

// IsClosed reports whether the vocabulary refuses undeclared values. An
// unknown vocabulary reports false — there is nothing to close.
func (r *Registry) IsClosed(vocabID string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.vocabs[vocabID]
	return ok && e.closed
}

// VocabularyIDs returns the registered vocabulary IDs, sorted.
func (r *Registry) VocabularyIDs() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.vocabs))
	for id := range r.vocabs {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// Allows reports whether value is permitted in the vocabulary.
//
// A closed vocabulary permits a declared value, or one under a declared open
// prefix. An open vocabulary permits anything non-empty. An UNKNOWN vocabulary
// permits nothing: a typo'd vocabulary ID must not read as "no rules here."
func (r *Registry) Allows(vocabID, value string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.vocabs[vocabID]
	if !ok {
		return false
	}
	if _, declared := e.types[value]; declared {
		return true
	}
	for _, p := range e.openPrefixes {
		if p != "" && strings.HasPrefix(value, p) {
			return true
		}
	}
	return !e.closed && value != ""
}

// List renders a vocabulary for an error message, so a rejection can name the
// allowed set rather than only refusing.
func (r *Registry) List(vocabID string) string {
	return strings.Join(r.Values(vocabID), ", ")
}

// ---- The process registry -------------------------------------------------

var (
	defaultMu       sync.RWMutex
	defaultRegistry = NewRegistry()
)

// Default returns the process-wide registry.
//
// A process-level registry rather than a handle threaded through every call
// site, because the callers are package-level predicates —
// memory.ParseNamespace validating a {type} segment, the knowledge write
// boundary validating a facet_kind — reached from the CLI, the daemon, the MCP
// adapter and the library facade alike. Threading a registry to all of them
// would be a larger change than this slice, for no gain: it REPLACES two
// package-level vars (memory's allowedTypes and canonicalKnowledgeKinds) with
// one, which is the direction of travel.
func Default() *Registry {
	defaultMu.RLock()
	defer defaultMu.RUnlock()
	return defaultRegistry
}

// Install replaces the process registry and returns a restore function.
//
// Called once at boot by cmd/tesseract after reading types.yaml, and by tests
// that need a different vocabulary. The restore function is what makes it safe
// in a test — it is the same shape memory.SetTypeAllowlist had, kept because
// the existing tests are written against it.
func Install(r *Registry) (restore func()) {
	defaultMu.Lock()
	defer defaultMu.Unlock()
	prev := defaultRegistry
	defaultRegistry = r
	return func() {
		defaultMu.Lock()
		defer defaultMu.Unlock()
		defaultRegistry = prev
	}
}
