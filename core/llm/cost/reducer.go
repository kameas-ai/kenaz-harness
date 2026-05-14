// Package cost implements the connector's usage→cost reducer (FR-011 /
// WP11). The reducer reads a starter price table embedded into the
// binary, applies operator overrides if present, and derives a Cost
// for a given (kind, model, usage) triple.
package cost

import (
	_ "embed"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"

	llm "github.com/sigil-tech/kaneaz-harness/core/llm"
	"github.com/sigil-tech/kaneaz-harness/core/llm/pricing"
)

// Cost source constants used in llm.Cost.Source and the
// token-cost-telemetry mission's session_usage_turns table.
const (
	// SourceProvider means the cost figure was reported directly by the
	// upstream provider (e.g. OpenRouter's usage.cost field). Exact.
	SourceProvider = "provider"
	// SourceDerived means the cost was estimated from token counts
	// using the curated pricing.yaml table. Approximate; rendered with
	// a tilde prefix in the UI (~$X.XX).
	SourceDerived = "derived"
	// SourceUnknown means no pricing entry was found; cost is nil.
	SourceUnknown = "unknown"
)

// KindCompaction is the cost-tagging kind compaction-driven LLM calls
// carry so the per-session cost view (and any downstream telemetry)
// can break out compaction overhead from regular chat costs (mission
// compaction-strategy-ui-01KQ8TDI WP08 / plan §2.11).
//
// The constant is consumed by the compaction wiring layer
// (core/compaction/wiring) when it tags compaction LLM calls; the
// cost reducer itself does NOT consume this kind for price lookup
// (price lookup keys on the provider Kind: "anthropic", "openai", …).
// The tag is propagated through the audit emitter so dashboards can
// filter "where did this token spend land" without re-deriving by
// model / session.
const KindCompaction = "compaction"

// KindAutoTitle is the cost-tagging kind the auto-titling wiring adapter
// attaches to every LLM call it issues, mirroring the KindCompaction
// convention (session-auto-titling-01KQ8TDS WP01 / plan §2.8).
// Dashboards can use this tag to break out auto-title overhead from
// regular chat and compaction costs.
const KindAutoTitle = "auto_title"

//go:embed starter_table.yaml
var starterTable []byte

// Table is the in-memory price table.
type Table struct {
	Currency   string  `yaml:"currency"`
	BestEffort bool    `yaml:"best_effort"`
	Entries    []Entry `yaml:"entries"`
}

// Entry is a per-(kind, model_glob) price record.
type Entry struct {
	Kind             string             `yaml:"kind"`
	Model            string             `yaml:"model"`
	PerMillionTokens map[string]float64 `yaml:"per_million_tokens"`
	// PerImageUSD holds per-image pricing keyed by "<quality>/<size>" strings
	// (e.g. "standard/1024x1024", "hd/1024x1024"). An empty key "" is the
	// fallback when quality or size are not specified. When the key does not
	// match, Derive sets Indeterminate=true for the image cost component.
	// (multimodal-io-extended-01KQ8TD2 WP05)
	PerImageUSD map[string]float64 `yaml:"per_image_usd,omitempty"`
}

// LoadDefault returns the embedded starter table.
func LoadDefault() (*Table, error) {
	return Parse(starterTable)
}

// Parse decodes a YAML cost table.
func Parse(raw []byte) (*Table, error) {
	var t Table
	if err := yaml.Unmarshal(raw, &t); err != nil {
		return nil, fmt.Errorf("cost: parse: %w", err)
	}
	if t.Currency == "" {
		t.Currency = "USD"
	}
	return &t, nil
}

// Merge produces a new Table where override entries replace base
// entries by exact (kind, model) match.
func Merge(base, override *Table) *Table {
	if base == nil {
		return override
	}
	if override == nil {
		return base
	}
	out := &Table{Currency: override.Currency, BestEffort: override.BestEffort}
	if out.Currency == "" {
		out.Currency = base.Currency
	}
	keyed := map[string]int{}
	for i, e := range base.Entries {
		keyed[e.Kind+"|"+e.Model] = i
	}
	out.Entries = append(out.Entries, base.Entries...)
	for _, e := range override.Entries {
		if idx, ok := keyed[e.Kind+"|"+e.Model]; ok {
			out.Entries[idx] = e
			continue
		}
		out.Entries = append(out.Entries, e)
	}
	return out
}

// Reducer derives Cost from a Usage snapshot.
type Reducer struct {
	tab *Table
}

// New returns a Reducer backed by tab.
func New(tab *Table) *Reducer { return &Reducer{tab: tab} }

// Derive computes the cost for usage under (kind, model). If no entry
// matches, the returned Cost has Indeterminate=true (US3 Acceptance 1
// must still pass without a cost number).
func (r *Reducer) Derive(usage llm.Usage, kind, model string) llm.Cost {
	if r == nil || r.tab == nil {
		return llm.Cost{Indeterminate: true}
	}
	entry, ok := r.lookup(kind, model)
	if !ok {
		return llm.Cost{Indeterminate: true, Currency: r.tab.Currency}
	}
	rates := entry.PerMillionTokens
	const million = 1_000_000.0
	inputCost := float64(usage.InputTokens) / million * rates["input"]
	outputCost := float64(usage.OutputTokens) / million * rates["output"]
	cachedCost := float64(usage.CachedInputRead)/million*rates["cached_input_read"] +
		float64(usage.CachedInputWrite)/million*rates["cached_input_write"]
	reasoningCost := float64(usage.ReasoningTokens) / million * rates["reasoning"]
	total := inputCost + outputCost + cachedCost + reasoningCost
	return llm.Cost{
		Currency:      r.tab.Currency,
		Total:         total,
		InputCost:     inputCost,
		OutputCost:    outputCost,
		CachedCost:    cachedCost,
		ReasoningCost: reasoningCost,
		Source:        "starter",
	}
}

// DeriveImage computes the cost for image generation usage under (kind, model).
// It looks up per_image_usd keyed by "<quality>/<size>" (e.g. "hd/1024x1024").
// Falls back to the "" key when quality or size are empty. Returns
// Indeterminate=true when no pricing entry matches the quality/size combination.
// (multimodal-io-extended-01KQ8TD2 WP05)
func (r *Reducer) DeriveImage(usage llm.Usage, kind, model, quality, size string) llm.Cost {
	if r == nil || r.tab == nil {
		return llm.Cost{Indeterminate: true}
	}
	if usage.ImagesGenerated == 0 {
		return llm.Cost{Currency: r.tab.Currency, Total: 0, Source: "starter"}
	}
	entry, ok := r.lookup(kind, model)
	if !ok {
		return llm.Cost{Indeterminate: true, Currency: r.tab.Currency}
	}
	if len(entry.PerImageUSD) == 0 {
		return llm.Cost{Indeterminate: true, Currency: r.tab.Currency}
	}
	// Try exact quality/size key first.
	key := quality + "/" + size
	pricePerImage, found := entry.PerImageUSD[key]
	if !found && quality != "" {
		// Try quality-only key (no size qualifier).
		pricePerImage, found = entry.PerImageUSD[quality]
	}
	if !found && quality == "" && size == "" {
		// Try "" fallback only when neither quality nor size is specified.
		pricePerImage, found = entry.PerImageUSD[""]
	}
	if !found {
		return llm.Cost{Indeterminate: true, Currency: r.tab.Currency}
	}
	imageCost := pricePerImage * float64(usage.ImagesGenerated)
	return llm.Cost{
		Currency:  r.tab.Currency,
		Total:     imageCost,
		ImageCost: imageCost,
		Source:    "starter",
	}
}

// DeriveWithSource applies the three-branch cost derivation policy for
// the token-cost-telemetry mission:
//
//  1. If providerCostUSD is non-nil and > 0, return it as-is with
//     source="provider" (OpenRouter direct reports, future Anthropic
//     billing extension).
//  2. Otherwise, look up the (kind, model) pair in the curated
//     pricing.yaml table. If found, derive from token counts and
//     return source="derived".
//  3. If no entry exists, return source="unknown" with a nil CostUSD.
//
// The returned *float64 is nil in case 3; callers must handle nil and
// render tokens-only with no dollar figure (FR-005b "we don't lie").
func DeriveWithSource(usage llm.Usage, kind, model string, providerCostUSD *float64) (costUSD *float64, source string) {
	// Branch 1: provider-reported cost.
	if providerCostUSD != nil && *providerCostUSD > 0 {
		v := *providerCostUSD
		return &v, SourceProvider
	}

	// Branch 2: pricing-table derivation.
	entry, ok := pricing.Lookup(kind, model)
	if ok {
		const million = 1_000_000.0
		total := float64(usage.InputTokens)/million*entry.InputPer1MUSD +
			float64(usage.OutputTokens)/million*entry.OutputPer1MUSD +
			float64(usage.CachedInputRead)/million*entry.CachedInputPer1MUSD +
			float64(usage.CachedInputWrite)/million*entry.CachedInputWritePer1MUSD +
			float64(usage.ReasoningTokens)/million*entry.ReasoningPer1MUSD
		return &total, SourceDerived
	}

	// Branch 3: unknown.
	return nil, SourceUnknown
}

func (r *Reducer) lookup(kind, model string) (Entry, bool) {
	for _, e := range r.tab.Entries {
		if e.Kind != kind {
			continue
		}
		if matchGlob(e.Model, model) {
			return e, true
		}
	}
	return Entry{}, false
}

func matchGlob(pattern, s string) bool {
	if pattern == "" {
		return false
	}
	if pattern == "*" {
		return true
	}
	if strings.HasSuffix(pattern, "*") {
		return strings.HasPrefix(s, strings.TrimSuffix(pattern, "*"))
	}
	if strings.HasPrefix(pattern, "*") {
		return strings.HasSuffix(s, strings.TrimPrefix(pattern, "*"))
	}
	return pattern == s
}
