package capabilities

import (
	"encoding/json"
	"testing"

	llm "github.com/sigil-tech/kaneaz-harness/core/llm"
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
