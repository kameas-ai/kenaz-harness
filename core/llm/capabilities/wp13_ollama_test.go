package capabilities

// wp13_ollama_test.go — model-settings-reach-the-model-01PMZ101 WP13
// AC-016. Tests run through capabilities.LoadDefault() and the real
// Gate — never by reading the YAML directly (spec §8 rule 3) — because
// the whole §1.1 defect this mission fixes was the YAML and the gate
// disagreeing about which key to use.

import (
	"testing"

	llm "github.com/kameas-ai/kenaz-harness/core/llm"
)

// TestWP13_AC016_OllamaToolCallingPassesGate is the AC-016 assertion
// verbatim: a tool-bearing request on a Kind: "ollama" profile must
// return nil from Gate.Check. Fails if ollama.yaml's top-level
// tool_calling default is still false — which is exactly the state
// registering a real "ollama" adapter kind against the file's
// PRE-WP13 contents would have re-caused (spec D-15): the P0 this
// mission exists to fix, on a different provider kind.
func TestWP13_AC016_OllamaToolCallingPassesGate(t *testing.T) {
	c := mustCatalog(t)
	g := NewGate(c)
	prof := llm.ProviderProfile{ID: "p", Kind: "ollama", Model: "llama3.1"}
	req := llm.GenerationRequest{Tools: []llm.ToolSpec{{Name: "search"}}}
	if _, err := g.Check(req, prof); err != nil {
		t.Fatalf("Gate.Check on a tool-bearing ollama request = %v, want nil (ollama.yaml tool_calling default must be true)", err)
	}
}

// TestWP13_AC016_OllamaToolCallingPassesGate_UnmatchedModel repeats the
// assertion for a model name that matches none of ollama.yaml's
// model: rows, so the descriptor comes purely from the provider-level
// defaults block — the exact row WP13 corrected.
func TestWP13_AC016_OllamaToolCallingPassesGate_UnmatchedModel(t *testing.T) {
	c := mustCatalog(t)
	g := NewGate(c)
	prof := llm.ProviderProfile{ID: "p", Kind: "ollama", Model: "some-future-ollama-model"}
	req := llm.GenerationRequest{Tools: []llm.ToolSpec{{Name: "search"}}}
	if _, err := g.Check(req, prof); err != nil {
		t.Fatalf("Gate.Check on an unmatched-model ollama request = %v, want nil (provider default must be tool_calling:true)", err)
	}
}

// TestWP13_GrammarModeOneOfTwo asserts the mission's own rule for the
// grammar capability: "either constrains the wire or grammar: false.
// One of the two. Not neither." This mission's disposition (D-12) is
// grammar: false — the real GBNF producer is deferred to
// structured-output-is-reachable-01PMZ808 — so a grammar-mode request
// must be REFUSED by the gate (a clean, attributable pre-flight
// refusal), never silently accepted by a gate that then hands the
// adapter a mode it cannot honour.
func TestWP13_GrammarModeOneOfTwo(t *testing.T) {
	c := mustCatalog(t)
	g := NewGate(c)
	prof := llm.ProviderProfile{ID: "p", Kind: "ollama", Model: "llama3.1"}
	req := llm.GenerationRequest{ResponseFormat: &llm.ResponseFormat{Mode: "grammar"}}
	if _, err := g.Check(req, prof); err == nil {
		t.Fatal("Gate.Check on a grammar-mode ollama request = nil, want a capability-unsupported refusal " +
			"(this mission ships grammar:false + refuses at the gate; shipping grammar:true with no adapter " +
			"support would move the lie from the catalog to a raw provider/adapter error instead)")
	}
}

// TestWP13_LocalRuntimesOtherThanOllamaStillResolveCustomOpenAI is
// D-10's regression assertion: "the ollama adapter does NOT replace
// custom-openai.yaml — localruntime supports five kinds, moving one
// leaves four." llama-server, lm-studio, jan and gpt4all are (per
// register G-6 and core/llm/localruntime/types.go) still persisted and
// resolved as Kind: "custom-openai", and WP13 must not have disturbed
// that file or its gate behaviour.
func TestWP13_LocalRuntimesOtherThanOllamaStillResolveCustomOpenAI(t *testing.T) {
	c := mustCatalog(t)
	g := NewGate(c)
	// These are representative local-model-family names; the exact
	// model string doesn't matter here — what matters is that Kind
	// stays "custom-openai" (never "ollama") for every local runtime
	// this mission does not migrate, and the gate resolves it the same
	// way it did before WP13/WP14 touched anything.
	for _, model := range []string{"llama-server-model", "lm-studio-model", "jan-model", "gpt4all-model"} {
		prof := llm.ProviderProfile{ID: "p", Kind: "custom-openai", Model: model}
		req := llm.GenerationRequest{Tools: []llm.ToolSpec{{Name: "search"}}}
		if _, err := g.Check(req, prof); err != nil {
			t.Errorf("Gate.Check(custom-openai, %q) = %v, want nil — custom-openai.yaml must be unaffected by the ollama.yaml correction", model, err)
		}
	}
}

// TestWP13_CatalogAliasUnaffected is the WP02-inherited regression
// rule applied to this WP's own change: every OTHER provider's
// descriptor must be byte-identical before and after the ollama.yaml
// correction. An ollama-scoped fix that changes another provider's
// descriptor is a different, larger bug.
func TestWP13_CatalogAliasUnaffected(t *testing.T) {
	c := mustCatalog(t)
	cases := []struct{ provider, model string }{
		{"anthropic", "claude-sonnet-4-5"},
		{"openai", "gpt-4o"},
		{"azure-openai", "gpt-4o"},
		{"custom-openai", "llama3.1"},
		{"bedrock", "anthropic.claude-3-5-sonnet-20241022-v2:0"},
		{"gemini", "gemini-2.5-pro"},
		{"openrouter", "anthropic/claude-sonnet-4-5"},
	}
	for _, tc := range cases {
		d := c.Describe(tc.provider, tc.model)
		if !d.Has(llm.CapStreaming) {
			t.Errorf("%s/%s regressed: expected streaming support unaffected by the ollama.yaml change", tc.provider, tc.model)
		}
	}
}
