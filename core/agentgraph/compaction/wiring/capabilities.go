package wiring

import (
	"strings"
	"sync"

	"github.com/kameas-ai/kenaz-harness/core/agentgraph/compaction"
	llmcapabilities "github.com/kameas-ai/kenaz-harness/core/llm/capabilities"
)

// CapabilityLookup maps a (providerID, modelID) pair to its
// MaxContextTokens budget. The compaction engine uses the answer for
// its pre-flight cap check (session_engine.go step 6 / session_rolling.go step 7); a
// missing budget (ok=false) makes the engine skip the check and let
// the provider call surface its own context_length_exceeded error.
//
// compaction-convergence-01PMDL05: this used to carry its own
// hardcoded `builtinContextWindows` table, duplicating context-window
// numbers that also live in core/llm/capabilities/data/*.yaml
// (core/llm/capabilities.Catalog.ContextWindow). That duplication was
// closed by adding the (previously missing) o3 / o3-mini / gpt-4.1 /
// gpt-4.1-mini / o1-mini rows to the YAML catalog and making it the
// sole source of context-window numbers: NewCapabilityLookup no longer
// seeds `table` with any builtins, and MaxContextTokens falls back to
// the shared Catalog (loaded once via llmcapabilities.LoadDefault)
// when the operator-supplied `table` has no entry.
//
// CK-15 justify(blocker: "no RPC, Settings field, or config file
// anywhere in the tree reaches WithCustomTable or SetTable — the only
// production construction path is NewCapabilityLookup() (api.go),
// which starts with an empty table and nothing ever populates it after
// boot; building the escape hatch for real means adding a settings
// surface + RPC + frontend panel, which is new product scope, not a
// missing wire", owner: alec, date: 2026-08-29; chat-turn-integrity-
// 01PMZ606 WP13): `table` / WithCustomTable / SetTable used to be
// described as "the override layer for a model the YAML catalog
// doesn't know about yet — that is a legitimate escape hatch". It is
// legitimate DESIGN, but not a reachable one: WithCustomTable has zero
// callers anywhere including tests that aren't pinning the table
// mechanism itself, and SetTable's only callers are tests. An operator
// hitting an unknown model gets (0, false) from MaxContextTokens today
// — the engine skips its pre-flight cap check and the problem surfaces
// downstream as a provider context_length_exceeded mid-compaction,
// exactly as if this override layer did not exist.
type CapabilityLookup struct {
	mu    sync.RWMutex
	table map[string]int

	// cat is the shared YAML-backed capability catalog. Falls back to
	// nil (fallback disabled, not a crash) if the embedded data
	// fails to parse — see NewCapabilityLookup.
	cat *llmcapabilities.Catalog
}

// NewCapabilityLookup constructs a CapabilityLookup with an empty
// override table, backed by the default YAML capability catalog for
// context-window lookups. A caller wanting the pre-01PMDL05 curated-table
// behaviour verbatim (e.g. a test pinning specific numbers) should use
// WithCustomTable instead.
func NewCapabilityLookup() *CapabilityLookup {
	cat, _ := llmcapabilities.LoadDefault()
	// A load error here means the embedded catalog itself is broken
	// (a build-time invariant, not a runtime condition) — cat stays
	// nil and MaxContextTokens simply reports (0, false) for anything
	// not in an explicitly-supplied table, exactly like an unknown
	// model does today.
	return &CapabilityLookup{table: make(map[string]int), cat: cat}
}

// WithCustomTable returns a CapabilityLookup backed only by the
// caller-supplied table (the supplied table is copied so later
// mutations to the input map don't affect the lookup) — no YAML
// catalog fallback. Pass an empty map to start from a clean slate
// with no context-window answers at all.
func WithCustomTable(table map[string]int) *CapabilityLookup {
	c := &CapabilityLookup{table: make(map[string]int, len(table))}
	for k, v := range table {
		c.table[k] = v
	}
	return c
}

// SetTable adds or replaces a single entry. Useful for tests and for
// operators wiring in a custom model the shared YAML catalog doesn't
// carry yet.
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
// (max, true) on an exact table match; (max, true) on a known table
// prefix match (so a fresh model in a known family inherits its family
// head's budget); otherwise falls back to the shared YAML capability
// catalog's Catalog.ContextWindow; (0, false) when nothing matches
// anywhere — the engine skips its pre-flight check on a false reply.
func (c *CapabilityLookup) MaxContextTokens(model compaction.ProviderProfileRef) (int, bool) {
	if c == nil {
		return 0, false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()

	wantProv := strings.ToLower(strings.TrimSpace(model.ProviderID))
	wantModel := strings.ToLower(strings.TrimSpace(model.ModelID))

	if c.table != nil {
		exact := normalizeKey(model.ProviderID, model.ModelID)
		if v, ok := c.table[exact]; ok {
			return v, true
		}
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
	}

	if c.cat != nil {
		if v := c.cat.ContextWindow(wantProv, wantModel); v > 0 {
			return v, true
		}
	}

	return 0, false
}

// normalizeKey returns the lowercased "<provider>:<model>" key shape.
func normalizeKey(providerID, modelID string) string {
	return strings.ToLower(strings.TrimSpace(providerID)) + ":" + strings.ToLower(strings.TrimSpace(modelID))
}
