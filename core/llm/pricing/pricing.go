// Package pricing provides a curated per-(provider_kind, model_glob) price
// table embedded into the binary. It is the authoritative source for
// cost derivation when the upstream provider does not report a cost
// figure directly (e.g. Anthropic, OpenAI direct, Bedrock).
//
// Cost sources (in priority order):
//  1. Provider-reported cost (e.g. OpenRouter usage.cost) → source "provider"
//  2. Table-derived estimate from this package         → source "derived"
//  3. No matching entry                                → source "unknown", nil cost
//
// The last_updated header in pricing.yaml is surfaced in the UI tooltip
// so users can assess the data age.
package pricing

import (
	_ "embed"
	"fmt"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

//go:embed pricing.yaml
var tableBytes []byte

// Entry is one row in the price table.
type Entry struct {
	ProviderKind              string  `yaml:"provider_kind"`
	ModelPattern              string  `yaml:"model_pattern"`
	InputPer1MUSD             float64 `yaml:"input_per_1m_usd"`
	OutputPer1MUSD            float64 `yaml:"output_per_1m_usd"`
	CachedInputPer1MUSD       float64 `yaml:"cached_input_per_1m_usd,omitempty"`
	CachedInputWritePer1MUSD  float64 `yaml:"cached_input_write_per_1m_usd,omitempty"`
	ReasoningPer1MUSD         float64 `yaml:"reasoning_per_1m_usd,omitempty"`
}

// table is the parsed in-memory price table (populated at init time).
type table struct {
	LastUpdated string  `yaml:"last_updated"`
	Entries     []Entry `yaml:"entries"`
}

var defaultTable *table

func init() {
	var t table
	if err := yaml.Unmarshal(tableBytes, &t); err != nil {
		// Fail loudly at init time: a corrupt embed means the binary
		// is broken, not just indeterminate costs.
		panic(fmt.Sprintf("pricing: parse pricing.yaml: %v", err))
	}
	defaultTable = &t
}

// Lookup returns the best-matching Entry for (kind, modelID), along
// with a boolean indicating whether a match was found. Pattern matching
// uses the same prefix/suffix/exact glob semantics as core/llm/cost.
func Lookup(kind, modelID string) (*Entry, bool) {
	if defaultTable == nil {
		return nil, false
	}
	for i := range defaultTable.Entries {
		e := &defaultTable.Entries[i]
		if e.ProviderKind != kind {
			continue
		}
		if matchGlob(e.ModelPattern, modelID) {
			return e, true
		}
	}
	return nil, false
}

// LastUpdatedDate returns the parsed last_updated date from pricing.yaml.
// Returns zero time.Time when the field is missing or unparseable.
func LastUpdatedDate() time.Time {
	if defaultTable == nil || defaultTable.LastUpdated == "" {
		return time.Time{}
	}
	t, err := time.Parse("2006-01-02", defaultTable.LastUpdated)
	if err != nil {
		return time.Time{}
	}
	return t
}

// LastUpdatedString returns the raw last_updated string from pricing.yaml.
func LastUpdatedString() string {
	if defaultTable == nil {
		return ""
	}
	return defaultTable.LastUpdated
}

// matchGlob implements the simple single-wildcard glob used in the cost
// package: "*" matches all, "prefix*" matches HasPrefix, "*suffix"
// matches HasSuffix, otherwise exact match.
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
