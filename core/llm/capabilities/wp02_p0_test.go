package capabilities

import (
	"testing"

	llm "github.com/kameas-ai/kenaz-harness/core/llm"
)

// TestGate_AzureOpenAI_And_CustomOpenAI_ResolveRealCapabilities is AC-001
// (model-settings-reach-the-model-01PMZ101 WP02, mutation-checked): before
// this WP, capabilities.Load had no entry for "azure-openai" or
// "custom-openai" (core/llm/capabilities/data/ shipped six files, neither
// of these two among them), so Catalog.Describe fell into the
// unknown-provider branch — every tool-bearing GenerationRequest was
// refused before any wire call. This is the P0 recorded end to end by
// WP01's research/capability-matrix.md Run 1 (a real registry.Stream).
func TestGate_AzureOpenAI_And_CustomOpenAI_ResolveRealCapabilities(t *testing.T) {
	c := mustCatalog(t)
	g := NewGate(c)

	toolReq := llm.GenerationRequest{
		Tools: []llm.ToolSpec{{Name: "get_weather", Description: "get the weather"}},
	}
	imageReq := llm.GenerationRequest{
		Messages: []llm.Message{{
			Role: llm.RoleUser,
			Content: []llm.ContentBlock{{
				Type:   "image",
				Source: &llm.MediaSource{Kind: "base64", MediaType: "image/png", Data: "AA", SizeBytes: 10},
			}},
		}},
	}

	t.Run("azure-openai", func(t *testing.T) {
		prof := llm.ProviderProfile{Kind: "azure-openai", Model: "gpt-4o"}
		if _, err := g.Check(toolReq, prof); err != nil {
			t.Fatalf("Gate.Check(tool-bearing) = %v; want nil (the P0)", err)
		}
		// gpt-4o supports vision under the openai catalog azure-openai now
		// aliases to (loader.go providerAlias), so the image attachment
		// should be accepted, not refused.
		if err := g.CheckAttachments(imageReq, prof); err != nil {
			t.Fatalf("CheckAttachments(image/png) = %v; want nil (azure-openai gpt-4o resolves vision via the openai alias)", err)
		}
	})

	t.Run("custom-openai", func(t *testing.T) {
		prof := llm.ProviderProfile{Kind: "custom-openai", Model: "llama3.1"}
		if _, err := g.Check(toolReq, prof); err != nil {
			t.Fatalf("Gate.Check(tool-bearing) = %v; want nil (the P0)", err)
		}
		// custom-openai.yaml sets image_input: false as a deliberate
		// baseline decision (WP02 §2 — "a local llama build usually cannot
		// take images"), not an accident. Assert the refusal explicitly.
		if err := g.CheckAttachments(imageReq, prof); err == nil {
			t.Fatal("CheckAttachments(image/png) = nil; want a refusal — custom-openai.yaml's image_input baseline is false")
		}
	})
}

// TestCatalog_AliasTable_DoesNotChangeExistingProviders is WP02's
// regression test (tasks.md WP02 "Tests" bullet 2): the azure-openai
// alias must not perturb the descriptors of the providers that already
// had a YAML file. Values are pinned from WP01's
// research/capability-matrix.md Run 2 (a real capabilities.LoadDefault()
// + Gate.Check probe against this checkout, 2026-08-19) — an alias table
// that changes an existing provider's descriptor is a different bug from
// the one this WP fixes.
func TestCatalog_AliasTable_DoesNotChangeExistingProviders(t *testing.T) {
	c := mustCatalog(t)
	cases := []struct {
		provider, model                                 string
		toolCalling, grammar, structured, reasoning, ok bool // ok = image accepted
	}{
		{"anthropic", "claude-sonnet-4-5", true, false, true, true, true},
		{"openai", "gpt-4o", true, false, true, false, true},
		{"openrouter", "openai/gpt-4o", true, false, true, false, true},
		{"bedrock", "anthropic.claude-3-5-sonnet-20241022-v2:0", true, false, true, false, true},
		// structured=true since structured-output-is-reachable-01PMZE14
		// WP04/WP05: gemini's adapter now sets GenerationConfig.ResponseSchema
		// (core/llm/gemini/wire.go:379,398), so the capability row saying
		// "true" is the row telling the truth about the adapter. This is NOT
		// the alias table changing a provider — the property this test
		// guards — it is a deliberate capability gain landed by another
		// mission, verified here against the adapter rather than taken on
		// report.
		{"gemini", "gemini-2.5-pro", true, false, true, true, true},
	}
	imageReq := llm.GenerationRequest{
		Messages: []llm.Message{{
			Role: llm.RoleUser,
			Content: []llm.ContentBlock{{
				Type:   "image",
				Source: &llm.MediaSource{Kind: "base64", MediaType: "image/png", Data: "AA", SizeBytes: 10},
			}},
		}},
	}
	g := NewGate(c)
	for _, tc := range cases {
		d := c.Describe(tc.provider, tc.model)
		if got := d.Has(llm.CapToolCalling); got != tc.toolCalling {
			t.Errorf("%s/%s: tool_calling = %v; want %v", tc.provider, tc.model, got, tc.toolCalling)
		}
		if got := d.Has(llm.CapGrammar); got != tc.grammar {
			t.Errorf("%s/%s: grammar = %v; want %v", tc.provider, tc.model, got, tc.grammar)
		}
		if got := d.Has(llm.CapStructuredOutput); got != tc.structured {
			t.Errorf("%s/%s: structured_output = %v; want %v", tc.provider, tc.model, got, tc.structured)
		}
		if got := d.Has(llm.CapReasoning); got != tc.reasoning {
			t.Errorf("%s/%s: reasoning = %v; want %v", tc.provider, tc.model, got, tc.reasoning)
		}
		prof := llm.ProviderProfile{Kind: tc.provider, Model: tc.model}
		err := g.CheckAttachments(imageReq, prof)
		if (err == nil) != tc.ok {
			t.Errorf("%s/%s: CheckAttachments err=%v; want ok=%v", tc.provider, tc.model, err, tc.ok)
		}
	}
}
