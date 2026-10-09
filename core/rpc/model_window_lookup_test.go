package rpc

import (
	"context"
	"testing"

	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
)

// liveAdapter is a provider adapter with a live model list.
type liveAdapter struct {
	kind   string
	models map[string]int
}

func (a liveAdapter) Kind() string { return a.kind }
func (a liveAdapter) Capabilities(string) corellm.CapabilityDescriptor {
	return corellm.CapabilityDescriptor{}
}
func (a liveAdapter) Stream(context.Context, corellm.GenerationRequest, corellm.ProviderProfile, []byte) (corellm.Stream, error) {
	return nil, nil
}
func (a liveAdapter) LookupModelInfo(id string) (corellm.ModelInfo, bool) {
	w, ok := a.models[id]
	return corellm.ModelInfo{ID: id, ContextWindow: w}, ok
}

// adapterRegistry answers Adapter(kind); every other Registry method is
// unused here.
type adapterRegistry struct {
	corellm.Registry
	ad corellm.ProviderAdapter
}

func (r adapterRegistry) Adapter(kind string) corellm.ProviderAdapter {
	if r.ad != nil && r.ad.Kind() == kind {
		return r.ad
	}
	return nil
}

type mapCatalog map[string]int

func (m mapCatalog) ContextWindow(kind, model string) int { return m[kind+"/"+model] }

// TestModelWindows_ResolutionOrder: the user's per-kind override wins,
// then the adapter's live model list, then the curated catalog; a model
// none of them knows is 0 (unknown), which leaves the schema budget at
// its setting.
func TestModelWindows_ResolutionOrder(t *testing.T) {
	overrides := map[string]int{}
	w := modelWindows{
		reg: adapterRegistry{ad: liveAdapter{kind: "openrouter", models: map[string]int{"live-model": 131072}}},
		cat: mapCatalog{
			"openrouter/live-model": 8000,
			"openrouter/cat-model":  64000,
			"anthropic/claude":      200000,
		},
		overrides: func() map[string]int { return overrides },
	}
	cases := []struct {
		kind, model string
		want        int
	}{
		{"openrouter", "live-model", 131072}, // live beats the catalog's 8000
		{"openrouter", "cat-model", 64000},   // live miss falls to the catalog
		{"anthropic", "claude", 200000},      // no adapter: catalog
		{"openrouter", "nobody-knows", 0},
		{"gemini", "x", 0},
	}
	for _, c := range cases {
		if got := w.ContextWindow(c.kind, c.model); got != c.want {
			t.Errorf("%s/%s = %d, want %d", c.kind, c.model, got, c.want)
		}
	}
	overrides["openrouter"] = 32000
	if got := w.ContextWindow("openrouter", "live-model"); got != 32000 {
		t.Errorf("with an openrouter override: %d, want 32000", got)
	}
	overrides["openrouter"] = 0
	if got := w.ContextWindow("openrouter", "live-model"); got != 131072 {
		t.Errorf("a zero override is ignored: %d, want 131072", got)
	}
	if got := (modelWindows{}).ContextWindow("openrouter", "live-model"); got != 0 {
		t.Errorf("empty lookup = %d, want 0", got)
	}
}
