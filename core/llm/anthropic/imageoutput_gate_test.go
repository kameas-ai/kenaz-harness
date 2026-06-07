package anthropic

// Wire-shape coverage for GenerationRequest.ImageOutput on the Anthropic
// adapter.
//
// Anthropic models do not generate images natively
// (core/llm/capabilities/data/anthropic.yaml sets image_output: false on
// every model row). The capability gate is therefore the contract:
// requests opting into ImageOutput must be rejected before any wire call,
// and the Anthropic adapter must never see a request where the field is
// set.
//
// This test asserts that the gate rejects ImageOutput-bearing requests
// for every active Anthropic model, with ErrCapabilityUnsupported naming
// CapImageOutput. It is registered against the
// (GenerationRequest, ImageOutput, anthropic) coverage triple in
// core/llm/coverage_registry.yaml. (multimodal-io-extended-01KQ8TD2
// fallout — replaces the deferred-coverage `unsupported:` entry.)

import (
	"errors"
	"testing"

	llm "github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/llm/capabilities"
)

// TestAnthropicAdapter_ImageOutput_RejectedByGate verifies that a
// request with ImageOutput set is rejected by the capability gate
// for every Anthropic model. Anthropic doesn't generate images
// natively, so this is the load-bearing contract: the Anthropic
// adapter never receives a request where ImageOutput != nil.
func TestAnthropicAdapter_ImageOutput_RejectedByGate(t *testing.T) {
	t.Parallel()

	cat, err := capabilities.LoadDefault()
	if err != nil {
		t.Fatalf("capabilities.LoadDefault: %v", err)
	}
	gate := capabilities.NewGate(cat)

	models := []string{
		"claude-sonnet-4-5",
		"claude-haiku-4-5",
		"claude-opus-4-1",
	}

	for _, model := range models {
		t.Run(model, func(t *testing.T) {
			req := minReq()
			req.ImageOutput = &llm.ImageOutputSpec{}

			_, err := gate.Check(req, stdProf(model))
			if err == nil {
				t.Fatalf("expected gate rejection for ImageOutput on %s", model)
			}

			var capErr *llm.ErrCapabilityUnsupported
			if !errors.As(err, &capErr) {
				t.Fatalf("expected ErrCapabilityUnsupported, got %T: %v", err, err)
			}

			found := false
			for _, c := range capErr.Capabilities {
				if c == llm.CapImageOutput {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("expected CapImageOutput in missing list, got %v", capErr.Capabilities)
			}
		})
	}
}
