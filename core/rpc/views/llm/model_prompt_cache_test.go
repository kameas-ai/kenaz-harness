package llm

// tool-context-budget-01TCBUD01 integration: ModelInfo.SupportsPromptCache
// is the reader of WP05's llm.ModelInfo.SupportsPromptCache flag — the
// model picker's and session MODEL row's "caches prompts" badge (WP06).
// It must match what the adapters actually put on the wire.

import (
	"context"
	"testing"

	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
)

// cacheFlagAdapter is a provider adapter with a model list. Only the
// optional capabilities the view probes for are implemented.
type cacheFlagAdapter struct {
	corellm.ProviderAdapter
	infos map[string]corellm.ModelInfo
}

func (a cacheFlagAdapter) LookupModelInfo(id string) (corellm.ModelInfo, bool) {
	mi, ok := a.infos[id]
	return mi, ok
}

func (a cacheFlagAdapter) ListModels(context.Context, []byte) ([]corellm.ModelInfo, error) {
	out := make([]corellm.ModelInfo, 0, len(a.infos))
	for _, mi := range a.infos {
		out = append(out, mi)
	}
	return out, nil
}

type cacheFlagRegistry struct {
	*fakeRegistry
	adapters map[string]corellm.ProviderAdapter
}

func (r cacheFlagRegistry) Adapter(kind string) corellm.ProviderAdapter { return r.adapters[kind] }

func TestModelCachesPrompts_MirrorsAdapterDecision(t *testing.T) {
	yes := &corellm.ModelInfo{SupportsPromptCache: true}
	no := &corellm.ModelInfo{}
	cases := []struct {
		name, kind, model string
		listed            *corellm.ModelInfo
		want              bool
	}{
		{"anthropic direct, no flag written: curated table", "anthropic", "claude-sonnet-4-5", nil, true},
		{"anthropic direct, listed without flag: still the table", "anthropic", "claude-sonnet-4-5", no, true},
		{"anthropic direct, non-claude id", "anthropic", "other-model", nil, false},
		{"openrouter anthropic-family, unlisted: table", "openrouter", "anthropic/claude-sonnet-4.5", nil, true},
		{"openrouter anthropic-family, listed with flag", "openrouter", "anthropic/claude-sonnet-4.5", yes, true},
		{"openrouter anthropic-family, listed without flag: vetoed", "openrouter", "anthropic/claude-sonnet-4.5", no, false},
		{"openrouter non-anthropic", "openrouter", "openai/gpt-4o", nil, false},
		{"openai: never marked", "openai", "gpt-4o", nil, false},
	}
	for _, c := range cases {
		if got := modelCachesPrompts(c.kind, c.model, c.listed); got != c.want {
			t.Errorf("%s: modelCachesPrompts(%q, %q) = %v, want %v", c.name, c.kind, c.model, got, c.want)
		}
	}
}

// Both wire paths carry the flag: LLM_ListProviders' modelInfos (the
// session MODEL row / switcher) and ListModels (the add-provider picker).
func TestModelInfo_SupportsPromptCacheOnBothPaths(t *testing.T) {
	or := cacheFlagAdapter{infos: map[string]corellm.ModelInfo{
		"anthropic/claude-sonnet-4.5": {ID: "anthropic/claude-sonnet-4.5", SupportsPromptCache: true},
		"anthropic/claude-zero-cache": {ID: "anthropic/claude-zero-cache"},
		"openai/gpt-4o":               {ID: "openai/gpt-4o"},
	}}
	reg := cacheFlagRegistry{fakeRegistry: &fakeRegistry{}, adapters: map[string]corellm.ProviderAdapter{"openrouter": or}}
	api := New(Config{
		Registry: reg,
		Bundles: &fakeBundles{profiles: []corellm.ProviderProfile{
			{ID: "b-or", Kind: "openrouter", Model: "openai/gpt-4o", Models: []string{"anthropic/claude-sonnet-4.5", "anthropic/claude-zero-cache", "openai/gpt-4o"}},
			{ID: "b-ant", Kind: "anthropic", Model: "claude-sonnet-4-5", Models: []string{"claude-sonnet-4-5"}},
		}},
	})

	provs, err := api.ListProviders(context.Background())
	if err != nil {
		t.Fatalf("ListProviders: %v", err)
	}
	got := map[string]bool{}
	for _, p := range provs {
		for _, mi := range p.ModelInfos {
			got[p.Kind+"|"+mi.ID] = mi.SupportsPromptCache
		}
	}
	want := map[string]bool{
		"openrouter|anthropic/claude-sonnet-4.5": true,
		"openrouter|anthropic/claude-zero-cache": false,
		"openrouter|openai/gpt-4o":               false,
		"anthropic|claude-sonnet-4-5":            true, // Anthropic direct: table fallback
	}
	for k, w := range want {
		if v, ok := got[k]; !ok || v != w {
			t.Errorf("ListProviders %s supportsPromptCache = %v (present %v), want %v", k, v, ok, w)
		}
	}

	models, err := api.ListModels(context.Background(), "openrouter", "k")
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	listed := map[string]bool{}
	for _, m := range models {
		listed[m.ID] = m.SupportsPromptCache
	}
	if !listed["anthropic/claude-sonnet-4.5"] || listed["anthropic/claude-zero-cache"] || listed["openai/gpt-4o"] {
		t.Errorf("ListModels supportsPromptCache = %v", listed)
	}
}
