package capabilities

import (
	"errors"
	"testing"

	llm "github.com/sigil-tech/kaneaz-harness/core/llm"
)

func mustCatalog(t *testing.T) *Catalog {
	t.Helper()
	c, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestCatalog_Describe_Anthropic(t *testing.T) {
	c := mustCatalog(t)
	d := c.Describe("anthropic", "claude-sonnet-4-7")
	if !d.Has(llm.CapStreaming) || !d.Has(llm.CapToolCalling) || !d.Has(llm.CapVision) || !d.Has(llm.CapPromptCaching) {
		t.Fatalf("anthropic claude-sonnet should support streaming/tool/vision/cache: %+v", d.Supported)
	}
}

func TestCatalog_Describe_OpenAI_GPT4o(t *testing.T) {
	c := mustCatalog(t)
	d := c.Describe("openai", "gpt-4o-mini")
	if !d.Has(llm.CapVision) {
		t.Fatalf("gpt-4o-mini should support vision: %+v", d)
	}
	if d.Has(llm.CapPromptCaching) {
		t.Fatalf("openai prompt caching not modeled: %+v", d)
	}
}

func TestCatalog_Describe_UnknownProviderFallback(t *testing.T) {
	c := mustCatalog(t)
	d := c.Describe("totally-new-provider", "some-model")
	if !d.Has(llm.CapStreaming) {
		t.Fatalf("unknown provider must default to streaming-only safe baseline: %+v", d)
	}
	if d.Has(llm.CapVision) || d.Has(llm.CapToolCalling) {
		t.Fatalf("unknown provider must not assume advanced caps: %+v", d)
	}
}

func TestCatalog_Describe_UnknownModelFallback(t *testing.T) {
	c := mustCatalog(t)
	d := c.Describe("anthropic", "claude-future-9000")
	// Falls back to provider defaults; defaults advertise streaming.
	if !d.Has(llm.CapStreaming) {
		t.Fatalf("unknown anthropic model should fall back to defaults: %+v", d)
	}
	if d.Notes[llm.CapStreaming] != "unknown_model_default" {
		t.Fatalf("expected unknown_model_default note, got %+v", d.Notes)
	}
}

func TestGate_RejectsUnsupportedVision(t *testing.T) {
	c := mustCatalog(t)
	g := NewGate(c)
	prof := llm.ProviderProfile{ID: "p", Kind: "ollama", Model: "llama3.1"}
	req := llm.GenerationRequest{
		Attachments: []llm.Attachment{{Kind: "image", MIME: "image/png"}},
	}
	_, err := g.Check(req, prof)
	if err == nil {
		t.Fatal("expected vision rejection")
	}
	var e *llm.ErrCapabilityUnsupported
	if !errors.As(err, &e) {
		t.Fatalf("wrong err type: %T %v", err, err)
	}
	if len(e.Capabilities) != 1 || e.Capabilities[0] != llm.CapVision {
		t.Fatalf("unexpected missing list: %v", e.Capabilities)
	}
}

func TestGate_AcceptsStreamingByDefault(t *testing.T) {
	c := mustCatalog(t)
	g := NewGate(c)
	prof := llm.ProviderProfile{ID: "p", Kind: "anthropic", Model: "claude-sonnet-4-7"}
	req := llm.GenerationRequest{Messages: []llm.Message{{Role: llm.RoleUser, Content: []llm.ContentPart{{Type: "text", Text: "hi"}}}}}
	desc, err := g.Check(req, prof)
	if err != nil {
		t.Fatal(err)
	}
	if !desc.Has(llm.CapStreaming) {
		t.Fatalf("expected streaming descriptor")
	}
}

func TestGate_RejectsToolCallingOnNonToolModel(t *testing.T) {
	c := mustCatalog(t)
	g := NewGate(c)
	prof := llm.ProviderProfile{ID: "p", Kind: "ollama", Model: "tiny-text-only"}
	req := llm.GenerationRequest{Tools: []llm.ToolSpec{{Name: "x"}}}
	if _, err := g.Check(req, prof); err == nil {
		t.Fatal("expected tool-calling rejection on non-tool ollama default")
	}
}

func TestGate_AcceptsCachingForAnthropic(t *testing.T) {
	c := mustCatalog(t)
	g := NewGate(c)
	prof := llm.ProviderProfile{ID: "p", Kind: "anthropic", Model: "claude-sonnet-4-7"}
	req := llm.GenerationRequest{Caching: &llm.CachingSpec{Enabled: true}}
	if _, err := g.Check(req, prof); err != nil {
		t.Fatalf("anthropic should support caching: %v", err)
	}
}

func TestMatchGlob_Suffix(t *testing.T) {
	if !matchGlob("claude-sonnet-*", "claude-sonnet-4-7-20260420") {
		t.Fatal("expected match")
	}
	if matchGlob("claude-sonnet-*", "gpt-4o-mini") {
		t.Fatal("unexpected match")
	}
	if !matchGlob("*", "anything") {
		t.Fatal("wildcard should match")
	}
}
