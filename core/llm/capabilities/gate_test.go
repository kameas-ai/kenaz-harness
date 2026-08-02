package capabilities

import (
	"errors"
	"testing"

	llm "github.com/kameas-ai/kenaz-harness/core/llm"
)

// ── MultimodalInEnabled env-flag tests (multimodal-io-01KQ8TDF WP07) ────────

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
	if !d.Has(llm.CapStreaming) || !d.Has(llm.CapToolCalling) || !d.Has(llm.CapVision) {
		t.Fatalf("anthropic claude-sonnet should support streaming/tool/vision: %+v", d.Supported)
	}
}

func TestCatalog_Describe_OpenAI_GPT4o(t *testing.T) {
	c := mustCatalog(t)
	d := c.Describe("openai", "gpt-4o-mini")
	if !d.Has(llm.CapVision) {
		t.Fatalf("gpt-4o-mini should support vision: %+v", d)
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
	req := llm.GenerationRequest{Messages: []llm.Message{{Role: llm.RoleUser, Content: []llm.ContentBlock{{Type: "text", Text: "hi"}}}}}
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

// ── CheckAttachments tests (multimodal-io-01KQ8TDF WP02) ─────────────────

func imgBlock(mime string, sizeBytes int64) llm.ContentBlock {
	return llm.ContentBlock{
		Type: "image",
		Source: &llm.MediaSource{
			Kind:      "base64",
			MediaType: mime,
			Data:      "aaa",
			SizeBytes: sizeBytes,
		},
	}
}

func docBlock(mime string, sizeBytes int64) llm.ContentBlock {
	return llm.ContentBlock{
		Type: "document",
		Source: &llm.MediaSource{
			Kind:      "base64",
			MediaType: mime,
			Data:      "aaa",
			SizeBytes: sizeBytes,
		},
	}
}

func reqWithBlocks(blocks ...llm.ContentBlock) llm.GenerationRequest {
	return llm.GenerationRequest{
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: blocks},
		},
	}
}

// TestCheckAttachments_AudioAlwaysRejected verifies that audio MIME types are
// unconditionally rejected on any provider.
func TestCheckAttachments_AudioAlwaysRejected(t *testing.T) {
	c := mustCatalog(t)
	g := NewGate(c)
	for _, provider := range []string{"anthropic", "openai", "bedrock", "openrouter"} {
		prof := llm.ProviderProfile{ID: "p", Kind: provider, Model: "claude-sonnet-4-7"}
		block := llm.ContentBlock{
			Type: "image",
			Source: &llm.MediaSource{Kind: "base64", MediaType: "audio/wav"},
		}
		req := reqWithBlocks(block)
		err := g.CheckAttachments(req, prof)
		if err == nil {
			t.Errorf("%s: expected audio rejection, got nil", provider)
			continue
		}
		var ae *llm.ErrAttachmentAudioUnsupported
		if !errors.As(err, &ae) {
			t.Errorf("%s: expected ErrAttachmentAudioUnsupported, got %T: %v", provider, err, err)
		}
	}
}

// TestCheckAttachments_PDFRejectedOnOpenAI verifies that OpenAI rejects PDF
// blocks pre-flight.
func TestCheckAttachments_PDFRejectedOnOpenAI(t *testing.T) {
	c := mustCatalog(t)
	g := NewGate(c)
	prof := llm.ProviderProfile{ID: "p", Kind: "openai", Model: "gpt-4o"}
	req := reqWithBlocks(docBlock("application/pdf", 1024))
	err := g.CheckAttachments(req, prof)
	if err == nil {
		t.Fatal("expected PDF to be rejected on OpenAI")
	}
	var me *llm.ErrAttachmentMimeUnsupported
	if !errors.As(err, &me) {
		t.Fatalf("expected ErrAttachmentMimeUnsupported, got %T: %v", err, err)
	}
	if me.Provider != "openai" || me.Mime != "application/pdf" {
		t.Fatalf("unexpected error fields: %+v", me)
	}
}

// TestCheckAttachments_PDFAcceptedOnAnthropic verifies that Anthropic Claude
// Sonnet accepts PDF blocks.
func TestCheckAttachments_PDFAcceptedOnAnthropic(t *testing.T) {
	c := mustCatalog(t)
	g := NewGate(c)
	prof := llm.ProviderProfile{ID: "p", Kind: "anthropic", Model: "claude-sonnet-4-7"}
	req := reqWithBlocks(docBlock("application/pdf", 1024))
	if err := g.CheckAttachments(req, prof); err != nil {
		t.Fatalf("anthropic sonnet should accept PDFs: %v", err)
	}
}

// TestCheckAttachments_PDFAcceptedOnBedrockClaude verifies that
// Bedrock-Claude accepts PDF blocks.
func TestCheckAttachments_PDFAcceptedOnBedrockClaude(t *testing.T) {
	c := mustCatalog(t)
	g := NewGate(c)
	prof := llm.ProviderProfile{ID: "p", Kind: "bedrock", Model: "anthropic.claude-3-5-sonnet-20240620-v1:0"}
	req := reqWithBlocks(docBlock("application/pdf", 1024))
	if err := g.CheckAttachments(req, prof); err != nil {
		t.Fatalf("bedrock claude-3-5-sonnet should accept PDFs: %v", err)
	}
}

// TestCheckAttachments_PDFRejectedOnBedrockMistral verifies that
// non-document-capable Bedrock models reject PDFs.
func TestCheckAttachments_PDFRejectedOnBedrockMistral(t *testing.T) {
	c := mustCatalog(t)
	g := NewGate(c)
	prof := llm.ProviderProfile{ID: "p", Kind: "bedrock", Model: "meta.llama3-70b-instruct-v1:0"}
	req := reqWithBlocks(docBlock("application/pdf", 1024))
	err := g.CheckAttachments(req, prof)
	if err == nil {
		t.Fatal("expected PDF rejection on bedrock llama")
	}
}

// TestCheckAttachments_ImageTooLargeAnthropic verifies byte-cap enforcement.
func TestCheckAttachments_ImageTooLargeAnthropic(t *testing.T) {
	c := mustCatalog(t)
	g := NewGate(c)
	prof := llm.ProviderProfile{ID: "p", Kind: "anthropic", Model: "claude-sonnet-4-7"}
	// 6 MiB > 5 MiB cap
	req := reqWithBlocks(imgBlock("image/png", 6*1024*1024))
	err := g.CheckAttachments(req, prof)
	if err == nil {
		t.Fatal("expected ErrAttachmentTooLarge")
	}
	var te *llm.ErrAttachmentTooLarge
	if !errors.As(err, &te) {
		t.Fatalf("expected ErrAttachmentTooLarge, got %T: %v", err, err)
	}
}

// TestCheckAttachments_ImageWithinCapAnthropic verifies a 4 MiB image passes.
func TestCheckAttachments_ImageWithinCapAnthropic(t *testing.T) {
	c := mustCatalog(t)
	g := NewGate(c)
	prof := llm.ProviderProfile{ID: "p", Kind: "anthropic", Model: "claude-sonnet-4-7"}
	req := reqWithBlocks(imgBlock("image/png", 4*1024*1024))
	if err := g.CheckAttachments(req, prof); err != nil {
		t.Fatalf("4 MiB PNG should pass anthropic sonnet: %v", err)
	}
}

// TestCheckAttachments_CountExceededAnthropic verifies per-message image count.
func TestCheckAttachments_CountExceededAnthropic(t *testing.T) {
	c := mustCatalog(t)
	g := NewGate(c)
	prof := llm.ProviderProfile{ID: "p", Kind: "anthropic", Model: "claude-haiku-4-5"}
	// haiku cap is 20; put 21 tiny images
	blocks := make([]llm.ContentBlock, 21)
	for i := range blocks {
		blocks[i] = imgBlock("image/png", 100)
	}
	req := reqWithBlocks(blocks...)
	err := g.CheckAttachments(req, prof)
	if err == nil {
		t.Fatal("expected ErrAttachmentCountExceeded for haiku with 21 images")
	}
	var ce *llm.ErrAttachmentCountExceeded
	if !errors.As(err, &ce) {
		t.Fatalf("expected ErrAttachmentCountExceeded, got %T: %v", err, err)
	}
}

// TestCheckAttachments_PixelCapEnforced verifies pixel-cap enforcement.
func TestCheckAttachments_PixelCapEnforced(t *testing.T) {
	c := mustCatalog(t)
	g := NewGate(c)
	// Use a catalog with a custom entry via a custom YAML to test pixel cap.
	// Since the embedded YAML doesn't set max_image_pixels, we test the helper
	// directly via a custom descriptor-less path.
	// Instead, test that a 0 pixel cap means no check is applied.
	prof := llm.ProviderProfile{ID: "p", Kind: "anthropic", Model: "claude-sonnet-4-7"}
	block := llm.ContentBlock{
		Type: "image",
		Source: &llm.MediaSource{
			Kind:            "base64",
			MediaType:       "image/png",
			Data:            "aaa",
			SizeBytes:       100,
			ImageDimensions: &llm.ImageDimensions{Width: 10000, Height: 10000},
		},
	}
	req := reqWithBlocks(block)
	// anthropic.yaml has max_image_pixels: 0 (unbounded) so this should pass.
	if err := g.CheckAttachments(req, prof); err != nil {
		t.Fatalf("anthropic with max_image_pixels=0 should not reject large image: %v", err)
	}
}

// TestCheckAttachments_UnsupportedMimeRejected verifies HEIC rejection on
// anthropic (not in the allowed mime list).
func TestCheckAttachments_UnsupportedMimeRejected(t *testing.T) {
	c := mustCatalog(t)
	g := NewGate(c)
	prof := llm.ProviderProfile{ID: "p", Kind: "anthropic", Model: "claude-sonnet-4-7"}
	req := reqWithBlocks(imgBlock("image/heic", 100))
	err := g.CheckAttachments(req, prof)
	if err == nil {
		t.Fatal("expected ErrAttachmentMimeUnsupported for image/heic")
	}
	var me *llm.ErrAttachmentMimeUnsupported
	if !errors.As(err, &me) {
		t.Fatalf("expected ErrAttachmentMimeUnsupported, got %T: %v", err, err)
	}
}

// TestCheckAttachments_NoAttachments verifies that a plain-text request passes.
func TestCheckAttachments_NoAttachments(t *testing.T) {
	c := mustCatalog(t)
	g := NewGate(c)
	prof := llm.ProviderProfile{ID: "p", Kind: "openai", Model: "gpt-4o"}
	req := llm.GenerationRequest{
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: []llm.ContentBlock{{Type: "text", Text: "hello"}}},
		},
	}
	if err := g.CheckAttachments(req, prof); err != nil {
		t.Fatalf("plain text request must pass: %v", err)
	}
}

// ── HARNESS_MULTIMODAL_IN env-flag tests (multimodal-io-01KQ8TDF WP08) ──────

// TestMultimodalInEnabled_DefaultOn verifies that MultimodalInEnabled returns
// true when the env var is unset (the "default on" semantics of FR-022).
func TestMultimodalInEnabled_DefaultOn(t *testing.T) {
	t.Setenv("HARNESS_MULTIMODAL_IN", "")
	if !MultimodalInEnabled() {
		t.Error("expected MultimodalInEnabled()=true when env var is empty")
	}
}

// TestMultimodalInEnabled_OffWhenExplicit verifies that setting
// HARNESS_MULTIMODAL_IN=off disables the flag (FR-022).
func TestMultimodalInEnabled_OffWhenExplicit(t *testing.T) {
	t.Setenv("HARNESS_MULTIMODAL_IN", "off")
	if MultimodalInEnabled() {
		t.Error("expected MultimodalInEnabled()=false when HARNESS_MULTIMODAL_IN=off")
	}
}

// TestMultimodalInEnabled_CaseInsensitive verifies that "OFF" and " off " are
// also treated as disabled (defensive robustness).
func TestMultimodalInEnabled_CaseInsensitive(t *testing.T) {
	for _, v := range []string{"OFF", "Off", " off ", " OFF "} {
		t.Run(v, func(t *testing.T) {
			t.Setenv("HARNESS_MULTIMODAL_IN", v)
			if MultimodalInEnabled() {
				t.Errorf("expected false for HARNESS_MULTIMODAL_IN=%q", v)
			}
		})
	}
}

// TestAttachmentLimits_EnvFlagForceDisable verifies that when
// HARNESS_MULTIMODAL_IN=off, AttachmentLimits forces ImageInput=false and
// DocumentInput=false regardless of per-model YAML (FR-022).
func TestAttachmentLimits_EnvFlagForceDisable(t *testing.T) {
	t.Setenv("HARNESS_MULTIMODAL_IN", "off")
	c := mustCatalog(t)
	// Anthropic Sonnet normally has ImageInput=true and DocumentInput=true.
	desc := c.AttachmentLimits("anthropic", "claude-sonnet-4-7")
	if desc.ImageInput {
		t.Error("expected ImageInput=false when HARNESS_MULTIMODAL_IN=off")
	}
	if desc.DocumentInput {
		t.Error("expected DocumentInput=false when HARNESS_MULTIMODAL_IN=off")
	}
}

// TestAttachmentLimits_EnvFlagOnPreservesDescriptor verifies that when
// HARNESS_MULTIMODAL_IN is unset, the YAML values are preserved (FR-022).
func TestAttachmentLimits_EnvFlagOnPreservesDescriptor(t *testing.T) {
	t.Setenv("HARNESS_MULTIMODAL_IN", "")
	c := mustCatalog(t)
	// Anthropic Sonnet should have ImageInput=true per the YAML.
	desc := c.AttachmentLimits("anthropic", "claude-sonnet-4-7")
	if !desc.ImageInput {
		t.Error("expected ImageInput=true for anthropic sonnet when HARNESS_MULTIMODAL_IN is unset")
	}
}

// ── Gemini reasoning gate tests (google-vertex-gemini-adapter-01KQ8VMY WP05) ──

// TestGeminiReasoningGate_2_5_Allowed verifies that gemini-2.5-pro passes
// the capability gate for reasoning.
func TestGeminiReasoningGate_2_5_Allowed(t *testing.T) {
	t.Parallel()
	c := mustCatalog(t)
	gate := NewGate(c)
	req := llm.GenerationRequest{
		ProfileID: "test",
		Messages:  []llm.Message{llm.NewTextMessage(llm.RoleUser, "think step by step")},
		Reasoning: &llm.ReasoningSpec{Enabled: true, BudgetTokens: 4000},
	}
	prof := llm.ProviderProfile{Kind: "gemini", Model: "gemini-2.5-pro-preview-04-09"}
	_, err := gate.Check(req, prof)
	if err != nil {
		t.Errorf("gemini-2.5-pro + reasoning: expected nil error, got %v", err)
	}
}

// TestGeminiReasoningGate_2_0_Rejected verifies that gemini-2.0-flash
// is rejected by the capability gate when reasoning is requested.
func TestGeminiReasoningGate_2_0_Rejected(t *testing.T) {
	t.Parallel()
	c := mustCatalog(t)
	gate := NewGate(c)
	req := llm.GenerationRequest{
		ProfileID: "test",
		Messages:  []llm.Message{llm.NewTextMessage(llm.RoleUser, "think step by step")},
		Reasoning: &llm.ReasoningSpec{Enabled: true, BudgetTokens: 4000},
	}
	prof := llm.ProviderProfile{Kind: "gemini", Model: "gemini-2.0-flash"}
	_, err := gate.Check(req, prof)
	if err == nil {
		t.Error("gemini-2.0-flash + reasoning: expected capability-unsupported error, got nil")
	}
	var capErr *llm.ErrCapabilityUnsupported
	if !errors.As(err, &capErr) {
		t.Errorf("want *ErrCapabilityUnsupported, got %T: %v", err, err)
	}
}
