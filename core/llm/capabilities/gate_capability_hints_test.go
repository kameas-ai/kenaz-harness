package capabilities

// gate_capability_hints_test.go — model-settings-reach-the-model-01PMZ101
// WP14 AC-017(b): "fails if only Gate.Check is asserted. That is R-5,
// and it is the exact half-fix D-2 exists to prevent. CheckAttachments
// must be in the assertion." Both Check and CheckAttachments are
// covered here, in both directions (a hint turning a capability ON
// that the static baseline says off, and OFF that the baseline says
// on), plus the MIME-list-population edge case R-5 names explicitly.

import (
	"testing"

	llm "github.com/kameas-ai/kenaz-harness/core/llm"
)

// TestGate_Check_CapabilityHintOverridesBaseline_On uses custom-openai
// (whose static baseline in custom-openai.yaml sets tool_calling but
// NOT reasoning — see the WP02 baseline) with a CapReasoning hint set
// to true, and asserts a reasoning-requesting request now passes.
func TestGate_Check_CapabilityHintOverridesBaseline_On(t *testing.T) {
	c := mustCatalog(t)
	g := NewGate(c)
	prof := llm.ProviderProfile{ID: "p", Kind: "custom-openai", Model: "any-model"}
	req := llm.GenerationRequest{Reasoning: &llm.ReasoningSpec{Enabled: true, BudgetTokens: 1024}}

	// Baseline: no hint, must refuse (custom-openai.yaml sets reasoning: false).
	if _, err := g.Check(req, prof); err == nil {
		t.Fatal("baseline: expected reasoning rejection on plain custom-openai with no hint")
	}

	// With a hint, the SAME request against the SAME static baseline
	// must now pass — proving the hint overlays rather than being
	// ignored or requiring the baseline itself to change.
	prof.CapabilityHints = map[llm.Capability]bool{llm.CapReasoning: true}
	if _, err := g.Check(req, prof); err != nil {
		t.Fatalf("Check with CapReasoning hint=true = %v, want nil", err)
	}
}

// TestGate_Check_CapabilityHintOverridesBaseline_Off asserts the
// reverse direction: a hint can also turn OFF a capability the static
// baseline reports as true, e.g. a probe that discovered an endpoint
// does NOT actually support tool calling despite the conservative
// baseline assuming it does.
func TestGate_Check_CapabilityHintOverridesBaseline_Off(t *testing.T) {
	c := mustCatalog(t)
	g := NewGate(c)
	prof := llm.ProviderProfile{
		ID: "p", Kind: "custom-openai", Model: "any-model",
		CapabilityHints: map[llm.Capability]bool{llm.CapToolCalling: false},
	}
	req := llm.GenerationRequest{Tools: []llm.ToolSpec{{Name: "x"}}}
	if _, err := g.Check(req, prof); err == nil {
		t.Fatal("expected tool-calling rejection when CapToolCalling hint=false overrides the custom-openai.yaml baseline's true")
	}
}

// TestGate_Check_NoHintsLeavesBaselineUntouched is the "absent hint
// changes nothing" half of the contract — the common case, since most
// profiles have never been probed.
func TestGate_Check_NoHintsLeavesBaselineUntouched(t *testing.T) {
	c := mustCatalog(t)
	g := NewGate(c)
	prof := llm.ProviderProfile{ID: "p", Kind: "openai", Model: "gpt-4o"}
	req := llm.GenerationRequest{Tools: []llm.ToolSpec{{Name: "x"}}}
	if _, err := g.Check(req, prof); err != nil {
		t.Fatalf("nil CapabilityHints must not change the baseline result: %v", err)
	}
}

// TestGate_CheckAttachments_VisionHintEnablesImages is R-5's own
// scenario verbatim: "a user pointing custom-openai at a vision-
// capable endpoint currently has images refused." custom-openai.yaml's
// static baseline sets image_input: false (WP02, spec D-1 — a
// conservative default since a local llama build usually cannot take
// images). A CapVision=true hint must make CheckAttachments accept an
// image block it would otherwise refuse.
func TestGate_CheckAttachments_VisionHintEnablesImages(t *testing.T) {
	c := mustCatalog(t)
	g := NewGate(c)
	imgReq := llm.GenerationRequest{Messages: []llm.Message{
		{Role: llm.RoleUser, Content: []llm.ContentBlock{
			{Type: "image", Source: &llm.MediaSource{Kind: "base64", MediaType: "image/png", Data: "aaa"}},
		}},
	}}

	// Baseline: no hint, custom-openai refuses images.
	baseline := llm.ProviderProfile{ID: "p", Kind: "custom-openai", Model: "llava-endpoint"}
	if err := g.CheckAttachments(imgReq, baseline); err == nil {
		t.Fatal("baseline: expected image refusal on plain custom-openai with no vision hint")
	}

	// With a CapVision hint, the same request against the same static
	// baseline must now be accepted.
	hinted := llm.ProviderProfile{
		ID: "p", Kind: "custom-openai", Model: "llava-endpoint",
		CapabilityHints: map[llm.Capability]bool{llm.CapVision: true},
	}
	if err := g.CheckAttachments(imgReq, hinted); err != nil {
		t.Fatalf("CheckAttachments with CapVision hint=true = %v, want nil", err)
	}
}

// TestGate_CheckAttachments_VisionHintWithCatalogMimeList exercises the
// realistic path through the real Catalog: custom-openai.yaml already
// populates ImageInputMimeTypes even though ImageInput defaults to
// false (loader.go's AttachmentLimits seeds a restrictive default MIME
// list unconditionally), so a vision hint here only needs to flip
// ImageInput — the MIME allow-list was never empty. The genuinely
// empty-list case (a provider with NO catalog entry at all) is
// covered directly below, since no catalog data in this tree actually
// produces an empty ImageInputMimeTypes to drive through Gate.
func TestGate_CheckAttachments_VisionHintWithCatalogMimeList(t *testing.T) {
	c := mustCatalog(t)
	g := NewGate(c)
	prof := llm.ProviderProfile{
		ID: "p", Kind: "custom-openai", Model: "llava-endpoint",
		CapabilityHints: map[llm.Capability]bool{llm.CapVision: true},
	}
	for _, mime := range []string{"image/png", "image/jpeg", "image/gif", "image/webp"} {
		req := llm.GenerationRequest{Messages: []llm.Message{
			{Role: llm.RoleUser, Content: []llm.ContentBlock{
				{Type: "image", Source: &llm.MediaSource{Kind: "base64", MediaType: mime, Data: "aaa"}},
			}},
		}}
		if err := g.CheckAttachments(req, prof); err != nil {
			t.Errorf("CheckAttachments(%s) = %v, want nil — the vision hint must flip ImageInput without losing the catalog's MIME allow-list", mime, err)
		}
	}
}

// TestApplyVisionHintToAttachments_PopulatesMimeListWhenEmpty is a
// direct, white-box test of R-5's exact stated hazard: "mimeAllowed
// returns false on an empty list. A hint that sets ImageInput=true
// without populating ImageInputMimeTypes refuses every image and
// looks wired." No catalog data in this tree currently produces an
// empty ImageInputMimeTypes (loader.go's AttachmentLimits seeds a
// restrictive default list unconditionally), so this test constructs
// the empty-list precondition directly to prove the guard exists
// independent of today's YAML contents — a future provider whose
// catalog omits image_input_mime_types entirely must not silently
// regress into the hazard.
func TestApplyVisionHintToAttachments_PopulatesMimeListWhenEmpty(t *testing.T) {
	desc := AttachmentDescriptor{ImageInput: false, ImageInputMimeTypes: nil}
	applyVisionHintToAttachments(&desc, map[llm.Capability]bool{llm.CapVision: true})
	if !desc.ImageInput {
		t.Fatal("want ImageInput=true after a CapVision=true hint")
	}
	if len(desc.ImageInputMimeTypes) == 0 {
		t.Fatal("want a non-empty ImageInputMimeTypes after a CapVision=true hint on an empty baseline — " +
			"an empty list means mimeAllowed refuses every image regardless of ImageInput (R-5)")
	}
}

// TestApplyVisionHintToAttachments_OffDoesNotTouchMimeList asserts a
// CapVision=false hint doesn't spuriously populate a MIME list — there
// is nothing to gain from it and it would mask a genuinely-empty
// catalog list in a debugging session.
func TestApplyVisionHintToAttachments_OffDoesNotTouchMimeList(t *testing.T) {
	desc := AttachmentDescriptor{ImageInput: true, ImageInputMimeTypes: nil}
	applyVisionHintToAttachments(&desc, map[llm.Capability]bool{llm.CapVision: false})
	if desc.ImageInput {
		t.Fatal("want ImageInput=false after a CapVision=false hint")
	}
	if len(desc.ImageInputMimeTypes) != 0 {
		t.Fatal("a CapVision=false hint should not populate a MIME list")
	}
}

// TestGate_CheckAttachments_VisionHintOff asserts the reverse
// direction on the attachment side too: a hint can turn image support
// OFF for a profile whose static baseline says true.
func TestGate_CheckAttachments_VisionHintOff(t *testing.T) {
	c := mustCatalog(t)
	g := NewGate(c)
	prof := llm.ProviderProfile{
		ID: "p", Kind: "openai", Model: "gpt-4o", // openai.yaml: vision-capable by default
		CapabilityHints: map[llm.Capability]bool{llm.CapVision: false},
	}
	req := llm.GenerationRequest{Messages: []llm.Message{
		{Role: llm.RoleUser, Content: []llm.ContentBlock{
			{Type: "image", Source: &llm.MediaSource{Kind: "base64", MediaType: "image/png", Data: "aaa"}},
		}},
	}}
	if err := g.CheckAttachments(req, prof); err == nil {
		t.Fatal("expected image refusal when CapVision hint=false overrides openai.yaml's true baseline")
	}
}
