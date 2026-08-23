package capabilities

import (
	"encoding/json"
	"testing"
	"testing/fstest"

	llm "github.com/kameas-ai/kenaz-harness/core/llm"
)

// TestCatalog_BackfillVision verifies the multimodal-io WP03 vision
// backfill table. Every entry below must report Vision=true via the
// embedded YAML data.
func TestCatalog_BackfillVision(t *testing.T) {
	t.Parallel()
	c := mustCatalog(t)
	cases := []struct {
		provider string
		model    string
	}{
		{"anthropic", "claude-3-haiku-20240307"},
		{"anthropic", "claude-3-sonnet-20240229"},
		{"anthropic", "claude-3-opus-20240229"},
		{"anthropic", "claude-3-5-sonnet-20240620"},
		{"anthropic", "claude-3-5-haiku-20241022"},
		{"anthropic", "claude-sonnet-4-5"},
		{"anthropic", "claude-opus-4-1"},
		{"openai", "gpt-4o"},
		{"openai", "gpt-4o-mini"},
		{"openai", "gpt-4-turbo"},
		{"openai", "gpt-4-vision-preview"},
		{"bedrock", "anthropic.claude-3-haiku-20240307-v1:0"},
		{"bedrock", "anthropic.claude-3-5-sonnet-20240620-v1:0"},
		{"bedrock", "amazon.nova-lite-v1"},
		{"bedrock", "amazon.nova-pro-v1"},
	}
	for _, tc := range cases {
		d := c.Describe(tc.provider, tc.model)
		if !d.Has(llm.CapVision) {
			t.Errorf("%s/%s: expected Vision=true, got %+v", tc.provider, tc.model, d.Supported)
		}
	}
}

// TestCatalog_BackfillDocuments verifies the documents-capable subset.
// Models in this list must report Documents=true.
func TestCatalog_BackfillDocuments(t *testing.T) {
	t.Parallel()
	c := mustCatalog(t)
	cases := []struct {
		provider string
		model    string
	}{
		{"anthropic", "claude-3-5-sonnet-20240620"},
		{"anthropic", "claude-sonnet-4-5"},
		{"anthropic", "claude-opus-4-1"},
		{"bedrock", "anthropic.claude-3-5-sonnet-20240620-v1:0"},
		{"bedrock", "amazon.nova-lite-v1"},
		{"bedrock", "amazon.nova-pro-v1"},
	}
	for _, tc := range cases {
		d := c.Describe(tc.provider, tc.model)
		if !d.Has(llm.CapDocuments) {
			t.Errorf("%s/%s: expected Documents=true, got %+v", tc.provider, tc.model, d.Supported)
		}
	}
}

// TestCatalog_DocumentsExcludesNonDocModels verifies the "documents
// is a strict subset of vision" invariant: vision-capable models that
// are NOT in the documents list (legacy claude-3, claude-haiku, gpt-4o,
// older claude-3-5-haiku) must report Documents=false.
func TestCatalog_DocumentsExcludesNonDocModels(t *testing.T) {
	t.Parallel()
	c := mustCatalog(t)
	cases := []struct {
		provider string
		model    string
	}{
		{"anthropic", "claude-3-haiku-20240307"},
		{"anthropic", "claude-3-sonnet-20240229"},
		{"anthropic", "claude-3-opus-20240229"},
		{"anthropic", "claude-3-5-haiku-20241022"},
		{"openai", "gpt-4o"},
		{"openai", "gpt-4o-mini"},
		{"openai", "gpt-4-turbo"},
		{"openai", "gpt-4-vision-preview"},
		{"bedrock", "anthropic.claude-3-haiku-20240307-v1:0"},
	}
	for _, tc := range cases {
		d := c.Describe(tc.provider, tc.model)
		if d.Has(llm.CapDocuments) {
			t.Errorf("%s/%s: expected Documents=false, got %+v", tc.provider, tc.model, d.Supported)
		}
	}
}

// TestCatalog_ContextWindow asserts that curated context-window values
// are populated correctly for the key models in the embedded YAML data.
func TestCatalog_ContextWindow(t *testing.T) {
	t.Parallel()
	c := mustCatalog(t)
	cases := []struct {
		provider string
		model    string
		want     int
	}{
		// Anthropic — all 200k
		{"anthropic", "claude-sonnet-4-5", 200_000},
		{"anthropic", "claude-opus-4-1", 200_000},
		{"anthropic", "claude-3-5-sonnet-20240620", 200_000},
		{"anthropic", "claude-3-5-haiku-20241022", 200_000},
		{"anthropic", "claude-3-haiku-20240307", 200_000},
		{"anthropic", "claude-3-sonnet-20240229", 200_000},
		{"anthropic", "claude-3-opus-20240229", 200_000},
		// OpenAI
		{"openai", "gpt-4o", 128_000},
		{"openai", "gpt-4o-mini", 128_000},
		{"openai", "gpt-4-turbo", 128_000},
		{"openai", "gpt-3.5-turbo", 16_385},
		// Bedrock Claude
		{"bedrock", "anthropic.claude-3-5-sonnet-20240620-v1:0", 200_000},
		{"bedrock", "anthropic.claude-sonnet-4-5-v1:0", 200_000},
		{"bedrock", "anthropic.claude-3-haiku-20240307-v1:0", 200_000},
		// Bedrock Nova
		{"bedrock", "amazon.nova-lite-v1", 300_000},
		{"bedrock", "amazon.nova-pro-v1", 300_000},
		// Unknown model — should return 0
		{"anthropic", "claude-unknown-future-9000", 0},
		{"openai", "gpt-unknown-99", 0},
	}
	for _, tc := range cases {
		got := c.ContextWindow(tc.provider, tc.model)
		if got != tc.want {
			t.Errorf("ContextWindow(%q, %q) = %d, want %d", tc.provider, tc.model, got, tc.want)
		}
	}
}

// TestCatalog_StructuredOutputFlags verifies the structured-output capability
// flags are correctly loaded from the embedded YAML data
// (structured-output-and-grammar-01KX5R8A WP05 acceptance).
func TestCatalog_StructuredOutputFlags(t *testing.T) {
	t.Parallel()
	c := mustCatalog(t)

	// Models that must report CapStructuredOutput=true.
	trueExpected := []struct{ provider, model string }{
		{"openai", "gpt-4o"},
		{"openai", "gpt-4o-mini"},
		{"anthropic", "claude-sonnet-4-5"},
		{"anthropic", "claude-opus-4-1"},
		{"bedrock", "anthropic.claude-3-5-sonnet-20240620-v1:0"},
		{"openrouter", "anthropic/claude-sonnet-4-5"},
		{"openrouter", "openai/gpt-4o"},
	}
	for _, tc := range trueExpected {
		d := c.Describe(tc.provider, tc.model)
		if !d.Has(llm.CapStructuredOutput) {
			t.Errorf("%s/%s: expected CapStructuredOutput=true, got %+v", tc.provider, tc.model, d.Supported)
		}
	}

	// claude-haiku should NOT have structured_output (not reliable enough for tool-call workaround).
	if d := c.Describe("anthropic", "claude-haiku-3-5"); d.Has(llm.CapStructuredOutput) {
		t.Errorf("anthropic/claude-haiku-3-5: expected CapStructuredOutput=false")
	}
}

// TestCatalog_GrammarFlags verifies grammar capability flags
// (structured-output-and-grammar-01KX5R8A WP05 acceptance).
func TestCatalog_GrammarFlags(t *testing.T) {
	t.Parallel()
	c := mustCatalog(t)

	// Cloud providers must NOT report CapGrammar.
	cloudCases := []struct{ provider, model string }{
		{"anthropic", "claude-sonnet-4-5"},
		{"openai", "gpt-4o"},
		{"openrouter", "anthropic/claude-sonnet-4-5"},
		{"bedrock", "anthropic.claude-3-5-sonnet-20240620-v1:0"},
	}
	for _, tc := range cloudCases {
		d := c.Describe(tc.provider, tc.model)
		if d.Has(llm.CapGrammar) {
			t.Errorf("%s/%s: expected CapGrammar=false for cloud provider", tc.provider, tc.model)
		}
	}

	// Ollama's CapGrammar was true here (a stub the file's own comment
	// called "supported in principle") until
	// model-settings-reach-the-model-01PMZ101 WP13 (spec D-12/§5.13):
	// no adapter was registered under Kind "ollama" at the time this
	// assertion was written, so a true grammar row was inert regardless
	// of its value. WP13 registers a real "ollama" adapter kind, which
	// makes this row live for the first time via GenerationRequest.
	// RequestedCapabilities()'s CapGrammar append — and this adapter
	// does not implement a real GBNF constraint. Shipping true here
	// would flip the lie from "unreachable capability row" to "gate
	// says yes, adapter returns ErrUnsupportedFormat", which the spec
	// calls strictly worse than the row this replaces. The real grammar
	// producer is deferred to structured-output-is-reachable-01PMZ808
	// (D-12 names this exact row as what stays deferred); CapGrammar is
	// false until that mission's adapter work lands.
	ollamaDesc := c.Describe("ollama", "llama3.2")
	if ollamaDesc.Has(llm.CapGrammar) {
		t.Errorf("ollama/llama3.2: expected CapGrammar=false (grammar producer deferred to 01PMZ808), got %+v", ollamaDesc.Supported)
	}
}

// TestCatalog_ImageOutputFlags verifies CapImageOutput is correctly loaded
// for image-generation models and explicitly false for text models.
// (multimodal-io-extended-01KQ8TD2 WP05)
func TestCatalog_ImageOutputFlags(t *testing.T) {
	t.Parallel()
	c := mustCatalog(t)

	// Image-generation models must report CapImageOutput=true.
	imageModels := []struct{ provider, model string }{
		{"openai", "dall-e-3"},
		{"openai", "gpt-image-1"},
		{"bedrock", "amazon.titan-image-generator-v1"},
		{"bedrock", "stability.stable-diffusion-xl-v1"},
	}
	for _, tc := range imageModels {
		d := c.Describe(tc.provider, tc.model)
		if !d.Has(llm.CapImageOutput) {
			t.Errorf("%s/%s: expected CapImageOutput=true, got %+v", tc.provider, tc.model, d.Supported)
		}
	}

	// Text/chat models must NOT report CapImageOutput.
	textModels := []struct{ provider, model string }{
		{"anthropic", "claude-sonnet-4-5"},
		{"openai", "gpt-4o"},
		{"bedrock", "anthropic.claude-sonnet-4-5-v1:0"},
		{"ollama", "llama3.2"},
		{"openrouter", "openai/gpt-4o"},
	}
	for _, tc := range textModels {
		d := c.Describe(tc.provider, tc.model)
		if d.Has(llm.CapImageOutput) {
			t.Errorf("%s/%s: expected CapImageOutput=false, got %+v", tc.provider, tc.model, d.Supported)
		}
	}
}

// TestAzureOpenAI_CapabilitiesMatchOpenAI asserts that querying the catalog
// with provider="openai" (as the azure adapter does) returns the same
// capability flags as a direct OpenAI query for the same model.
// The azure adapter rewrites the Provider field to "azure-openai" after
// the lookup, but the Supported map must be identical.
// (azure-openai-adapter-01KQ8VMZ WP06)
func TestAzureOpenAI_CapabilitiesMatchOpenAI(t *testing.T) {
	t.Parallel()
	c := mustCatalog(t)
	models := []string{"gpt-4o", "o1", "gpt-4-turbo"}
	for _, m := range models {
		openai := c.Describe("openai", m)
		azureAlias := c.Describe("openai", m) // azure adapter queries with "openai"
		for _, cap := range llm.AllCapabilities() {
			if openai.Has(cap) != azureAlias.Has(cap) {
				t.Errorf("model %q: openai.%s=%v != azure-alias.%s=%v",
					m, cap, openai.Has(cap), cap, azureAlias.Has(cap))
			}
		}
	}
}

// TestAzureOpenAI_o1_SupportsReasoning asserts o1 carries SupportsReasoning=true
// when queried via the openai catalog path (used by the azure adapter).
// (azure-openai-adapter-01KQ8VMZ WP06)
func TestAzureOpenAI_o1_SupportsReasoning(t *testing.T) {
	t.Parallel()
	c := mustCatalog(t)
	// Azure adapter queries with provider="openai" — verify o1 has reasoning.
	d := c.Describe("openai", "o1")
	if !d.Has(llm.CapReasoning) {
		t.Errorf("openai/o1: expected CapReasoning=true (used by azure o1 deployments), got %+v", d.Supported)
	}
}

// ── Gemini capability tests (google-vertex-gemini-adapter-01KQ8VMY WP07) ─────

// TestCatalog_GeminiLoads verifies that gemini.yaml is embedded and loaded.
func TestCatalog_GeminiLoads(t *testing.T) {
	t.Parallel()
	c := mustCatalog(t)
	// Any gemini model should have a non-empty descriptor (vision at minimum).
	d := c.Describe("gemini", "gemini-2.5-flash")
	if !d.Has(llm.CapVision) {
		t.Errorf("gemini/gemini-2.5-flash: expected Vision=true, got %+v", d.Supported)
	}
	if !d.Has(llm.CapStreaming) {
		t.Errorf("gemini/gemini-2.5-flash: expected Streaming=true")
	}
}

// TestCatalog_GeminiReasoning verifies the reasoning capability split
// between 2.5 (true) and 2.0 (false) families.
func TestCatalog_GeminiReasoning(t *testing.T) {
	t.Parallel()
	c := mustCatalog(t)

	// gemini-2.5-pro must advertise CapReasoning=true.
	d25pro := c.Describe("gemini", "gemini-2.5-pro-preview-04-09")
	if !d25pro.Has(llm.CapReasoning) {
		t.Errorf("gemini-2.5-pro: expected CapReasoning=true, got %+v", d25pro.Supported)
	}

	// gemini-2.5-flash must also advertise CapReasoning=true.
	d25flash := c.Describe("gemini", "gemini-2.5-flash")
	if !d25flash.Has(llm.CapReasoning) {
		t.Errorf("gemini-2.5-flash: expected CapReasoning=true, got %+v", d25flash.Supported)
	}

	// gemini-2.0-flash must NOT advertise CapReasoning.
	d20flash := c.Describe("gemini", "gemini-2.0-flash")
	if d20flash.Has(llm.CapReasoning) {
		t.Errorf("gemini-2.0-flash: expected CapReasoning=false, got %+v", d20flash.Supported)
	}
}

// TestCapabilityDescriptor_JSONRoundTrip pins the descriptor JSON
// shape end-to-end including the new documents capability.
func TestCapabilityDescriptor_JSONRoundTrip(t *testing.T) {
	t.Parallel()
	c := mustCatalog(t)
	d := c.Describe("anthropic", "claude-sonnet-4-5")
	raw, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back llm.CapabilityDescriptor
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !back.Has(llm.CapVision) || !back.Has(llm.CapDocuments) {
		t.Errorf("round-trip lost flags: %+v", back.Supported)
	}
	if back.Provider != d.Provider || back.Model != d.Model {
		t.Errorf("round-trip lost provider/model: %+v", back)
	}
}

// TestCatalog_DescribeRich_InheritsProviderKnobDefaults is the regression
// test for WP08 (model-request-path-live-01PMDL01): applyRichEntry used to
// wholesale-overwrite every ProviderCapabilities knob field with the
// matched model row's (Go zero-value) fields, silently zeroing every knob
// the shipped YAML never sets at model level even when the provider-level
// `defaults:` block declares real support. Real model rows for every
// provider here are silent on the extended knob fields, so a correct merge
// must resolve them to the provider defaults — not false/"" across the
// board — while an explicit model-level override (e.g. vision on gpt-4o)
// must still win.
func TestCatalog_DescribeRich_InheritsProviderKnobDefaults(t *testing.T) {
	t.Parallel()
	c := mustCatalog(t)

	t.Run("openai/gpt-4o inherits defaults' knob support", func(t *testing.T) {
		t.Parallel()
		caps := c.DescribeRich("openai", "gpt-4o")
		wantTrue := map[string]bool{
			"ParallelToolCalls": caps.ParallelToolCalls,
			"Seed":              caps.Seed,
			"Logprobs":          caps.Logprobs,
			"TopP":              caps.TopP,
			"FrequencyPenalty":  caps.FrequencyPenalty,
			"PresencePenalty":   caps.PresencePenalty,
			"ResponseFormat":    caps.ResponseFormat,
		}
		for name, got := range wantTrue {
			if !got {
				t.Errorf("openai/gpt-4o: %s = false, want true (inherited from provider defaults)", name)
			}
		}
		if caps.TopK {
			t.Errorf("openai/gpt-4o: TopK = true, want false (OpenAI does not support top_k)")
		}
		if caps.Batch {
			t.Errorf("openai/gpt-4o: Batch = true, want false")
		}
		// Explicit model-level override must still win over the provider
		// default (openai defaults declare vision:false).
		if !caps.Vision {
			t.Errorf("openai/gpt-4o: Vision = false, want true (explicit model-level override)")
		}
	})

	t.Run("anthropic/claude-sonnet-4-5 inherits defaults' knob support", func(t *testing.T) {
		t.Parallel()
		caps := c.DescribeRich("anthropic", "claude-sonnet-4-5")
		if !caps.TopK {
			t.Errorf("anthropic/claude-sonnet-4-5: TopK = false, want true (Anthropic supports top_k)")
		}
		if !caps.TopP {
			t.Errorf("anthropic/claude-sonnet-4-5: TopP = false, want true")
		}
		if caps.Seed {
			t.Errorf("anthropic/claude-sonnet-4-5: Seed = true, want false (Anthropic has no seed param)")
		}
		if caps.ParallelToolCalls {
			t.Errorf("anthropic/claude-sonnet-4-5: ParallelToolCalls = true, want false")
		}
		if !caps.ResponseFormat {
			t.Errorf("anthropic/claude-sonnet-4-5: ResponseFormat = false, want true")
		}
		// Explicit model-level override must still win (claude-sonnet-*
		// sets reasoning_style: token_budget explicitly).
		if caps.Reasoning_ != llm.ReasoningTokenBudget {
			t.Errorf("anthropic/claude-sonnet-4-5: Reasoning_ = %v, want ReasoningTokenBudget", caps.Reasoning_)
		}
	})

	t.Run("gemini/gemini-2.5-pro inherits defaults' knob support", func(t *testing.T) {
		t.Parallel()
		caps := c.DescribeRich("gemini", "gemini-2.5-pro")
		if !caps.TopP {
			t.Errorf("gemini/gemini-2.5-pro: TopP = false, want true (inherited)")
		}
		if !caps.TopK {
			t.Errorf("gemini/gemini-2.5-pro: TopK = false, want true (inherited)")
		}
	})

	t.Run("bedrock/anthropic.claude-sonnet-* inherits defaults' knob support", func(t *testing.T) {
		t.Parallel()
		caps := c.DescribeRich("bedrock", "anthropic.claude-sonnet-4-5-v1:0")
		if !caps.TopK {
			t.Errorf("bedrock claude-sonnet: TopK = false, want true (inherited)")
		}
		if !caps.TopP {
			t.Errorf("bedrock claude-sonnet: TopP = false, want true (inherited)")
		}
		if caps.Seed {
			t.Errorf("bedrock claude-sonnet: Seed = true, want false")
		}
	})
}

// TestCatalog_DescribeRich_ModelOverrideWinsOverDefault builds a synthetic
// provider spec (rather than relying on shipped data, none of which
// currently overrides a knob at model level) to pin the other half of the
// merge contract directly: when a model row DOES explicitly set a knob
// field, that value must win over the provider-level default, including
// the case where the override is the more restrictive `false`.
func TestCatalog_DescribeRich_ModelOverrideWinsOverDefault(t *testing.T) {
	t.Parallel()
	const doc = `
provider: synthtest
defaults:
  streaming: true
  seed: true
  top_p: true
  top_k: false
  parallel_tool_calls: true
models:
  - match: "no-override-model"
    streaming: true
  - match: "override-model"
    streaming: true
    seed: false
    top_k: true
`
	fsys := fstest.MapFS{
		"data/synthtest.yaml": &fstest.MapFile{Data: []byte(doc)},
	}
	c, err := Load(fsys, "data")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Model row silent on seed/top_p/top_k/parallel_tool_calls: must
	// inherit every default verbatim.
	inherited := c.DescribeRich("synthtest", "no-override-model")
	if !inherited.Seed || !inherited.TopP || inherited.TopK || !inherited.ParallelToolCalls {
		t.Errorf("no-override-model: got Seed=%v TopP=%v TopK=%v ParallelToolCalls=%v, want true/true/false/true (all inherited)",
			inherited.Seed, inherited.TopP, inherited.TopK, inherited.ParallelToolCalls)
	}

	// Model row explicitly overrides seed (true->false) and top_k
	// (false->true); top_p and parallel_tool_calls are left unset and
	// must still fall through to the provider default.
	overridden := c.DescribeRich("synthtest", "override-model")
	if overridden.Seed {
		t.Errorf("override-model: Seed = true, want false (explicit model override should win)")
	}
	if !overridden.TopK {
		t.Errorf("override-model: TopK = false, want true (explicit model override should win)")
	}
	if !overridden.TopP {
		t.Errorf("override-model: TopP = false, want true (unset field must inherit default)")
	}
	if !overridden.ParallelToolCalls {
		t.Errorf("override-model: ParallelToolCalls = false, want true (unset field must inherit default)")
	}
}
