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
