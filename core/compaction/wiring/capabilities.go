package wiring

import (
	"strings"
	"sync"

	"github.com/kameas-ai/kenaz-harness/core/compaction"
)

// CapabilityLookup maps (providerID, modelID) → MaxContextTokens budget.
// The compaction engine uses the answer for its pre-flight cap check
// (engine.go step 6 / rolling.go step 7); a missing budget (ok=false)
// makes the engine skip the check and let the provider call surface
// its own context_length_exceeded error.
//
// The current core/llm/capabilities.CapabilityDescriptor does not carry
// MaxContextTokens (per-(provider, model) descriptors track per-feature
// support flags but not numeric budgets). Until that surface lands —
// follow-up mission likely on capability-data — this adapter ships a
// curated table of "well-known" model context windows so the harness
// can still drive the threshold-mode pre-flight check on real models
// today. Unknown models return (0, false), which tells the engine to
// skip the pre-flight gracefully (the provider's own gate will catch
// any over-cap span).
//
// New entries land here when a model is added to the harness's known
// providers; the table itself is intentionally small to avoid drift
// against upstream provider docs. Operators with a custom model can
// supply their own table via WithCustomTable or override an entry via
// SetTable.
type CapabilityLookup struct {
	mu    sync.RWMutex
	table map[string]int
}

// builtinContextWindows is the curated default table. Keys are
// "<providerID>:<modelID>" lowercased; values are documented context
// windows in tokens. Multiple variants of a model that share a window
// are listed by their canonical id; the lookup tries the exact id
// first and then a prefix match against family heads.
//
// Numbers come from public provider documentation as of the mission
// merge date. Do NOT edit without checking the upstream source.
var builtinContextWindows = map[string]int{
	// Anthropic Claude family.
	"anthropic:claude-sonnet-4-5":     200000,
	"anthropic:claude-sonnet-4":       200000,
	"anthropic:claude-opus-4":         200000,
	"anthropic:claude-haiku-4":        200000,
	"anthropic:claude-3-5-sonnet":     200000,
	"anthropic:claude-3-5-haiku":      200000,
	"anthropic:claude-3-opus":         200000,
	"anthropic:claude-3-sonnet":       200000,
	"anthropic:claude-3-haiku":        200000,

	// OpenAI GPT-4o + o1 + o3 families.
	"openai:gpt-4o":                  128000,
	"openai:gpt-4o-mini":             128000,
	"openai:gpt-4-turbo":             128000,
	"openai:o1":                      200000,
	"openai:o1-mini":                 128000,
	"openai:o3":                      200000,
	"openai:o3-mini":                 200000,
	"openai:gpt-4.1":                 1000000,
	"openai:gpt-4.1-mini":            1000000,

	// Bedrock surfaces Anthropic models behind anthropic.* ids.
	"bedrock:anthropic.claude-sonnet-4-5":  200000,
	"bedrock:anthropic.claude-3-5-sonnet":  200000,

	// OpenRouter is a passthrough — model ids reuse the upstream
	// vendor's id with an "<vendor>/<model>" prefix. Common
	// OpenRouter-hosted models are listed; users on a custom model
	// can supply their own entry via SetTable.
	"openrouter:anthropic/claude-sonnet-4-5": 200000,
	"openrouter:openai/gpt-4o":               128000,
}

// NewCapabilityLookup constructs the default lookup populated with the
// curated builtin table.
func NewCapabilityLookup() *CapabilityLookup {
	c := &CapabilityLookup{table: make(map[string]int, len(builtinContextWindows))}
	for k, v := range builtinContextWindows {
		c.table[k] = v
	}
	return c
}

// WithCustomTable returns a CapabilityLookup backed by a caller-
// supplied table. The supplied table is copied so later mutations of
// the input map don't affect the lookup. Pass an empty map to start
// from a clean slate (no builtins).
func WithCustomTable(table map[string]int) *CapabilityLookup {
	c := &CapabilityLookup{table: make(map[string]int, len(table))}
	for k, v := range table {
		c.table[k] = v
	}
	return c
}

// SetTable replaces or adds an entry. Useful for tests and for
// operators wiring a custom model.
func (c *CapabilityLookup) SetTable(providerID, modelID string, maxTokens int) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.table == nil {
		c.table = make(map[string]int)
	}
	c.table[normalizeKey(providerID, modelID)] = maxTokens
}

// MaxContextTokens implements compaction.CapabilityLookup. Returns
// (max, true) on an exact match; (max, true) on a known prefix match
// (so a fresh model in a known family inherits its family head's
// budget); (0, false) when nothing matches — the engine skips its
// pre-flight check on a false reply.
func (c *CapabilityLookup) MaxContextTokens(model compaction.ProviderProfileRef) (int, bool) {
	if c == nil {
		return 0, false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.table == nil {
		return 0, false
	}
	exact := normalizeKey(model.ProviderID, model.ModelID)
	if v, ok := c.table[exact]; ok {
		return v, true
	}
	// Prefix match: walk every entry whose provider matches and whose
	// model prefix matches the requested id. The table is small enough
	// (≤ 30 entries) that a linear scan stays cheap.
	wantProv := strings.ToLower(strings.TrimSpace(model.ProviderID))
	wantModel := strings.ToLower(strings.TrimSpace(model.ModelID))
	for key, v := range c.table {
		colon := strings.IndexByte(key, ':')
		if colon < 0 {
			continue
		}
		prov := key[:colon]
		mod := key[colon+1:]
		if prov != wantProv {
			continue
		}
		if wantModel != "" && strings.HasPrefix(wantModel, mod) {
			return v, true
		}
	}
	return 0, false
}

// normalizeKey returns the lowercased "<provider>:<model>" key shape.
func normalizeKey(providerID, modelID string) string {
	return strings.ToLower(strings.TrimSpace(providerID)) + ":" + strings.ToLower(strings.TrimSpace(modelID))
}
