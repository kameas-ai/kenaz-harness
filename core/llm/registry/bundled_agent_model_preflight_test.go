// Tests for bundled-profile-model-preflight (WP18 /
// model-settings-reach-the-model-01PMZ101 UNIT-12, 2026-09-09).
//
// The finding: core/agents/bundled/code-reviewer.yaml declared
// "anthropic/claude-sonnet-4-5" (hyphen, vendor-prefixed) — a string that
// was never valid for EITHER provider, not a right-string-wrong-provider
// trade. OpenRouter's real, live model catalog namespaces by vendor and
// carries "anthropic/claude-sonnet-4.5" (dot); Anthropic's own direct API
// takes a BARE id with no vendor prefix at all, hyphenated throughout
// ("claude-sonnet-4-5" — see core/llm/anthropic/anthropic_test.go's
// fixture; nothing in the codebase strips a vendor prefix before dispatch).
// So the vendor-prefixed hyphen form matched neither convention — an
// adversarial test dispatching all five bundled profiles against a
// direct-Anthropic profile confirmed identical rejection before and after
// this fix, only the spelling in the error differs. Every bundled agent
// profile that named a "4-5" generation model had the same typo;
// core/agents/bundled/*.yaml now use the dot form throughout, which is
// correct for the OpenRouter-shaped dispatch these profiles actually use.
//
// The reporter's sharpest point: existing coverage of the model-selection
// path (llm_provider_adapter_test.go and friends) drives fake
// corellm.Registry test doubles whose Stream() methods accept ANY model
// unconditionally — they never exercise registry.Registry.Stream's own
// authorization logic, so a typo like this ships invisibly. These tests
// close that gap by driving bundled profiles through THIS package's real
// Registry.Stream — the actual production authorization code — with only
// the wire-level adapter faked (fakeAdapter, the same test double every
// other pipeline test in this file already uses for exactly that reason).
package registry

import (
	"context"
	"strings"
	"testing"

	coreagents "github.com/kameas-ai/kenaz-harness/core/agents"
	llm "github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/llm/envprovider"
)

// liveOpenRouterCatalogModels mirrors the models a real OpenRouter
// multi-model profile ("openrouter-4-models" in the reporter's session)
// carries for the Claude family bundled profiles ask for. Confirmed
// against OpenRouter's live catalog on 2026-09-09 (openrouter.ai/anthropic/
// claude-sonnet-4.5 and .../claude-haiku-4.5): OpenRouter uses a dot for
// point releases across every vendor it routes to (mirrors the existing
// "anthropic/claude-3.5-sonnet" convention already documented in
// core/llm/capabilities/data/openrouter.yaml), which is NOT the same
// string Anthropic's own direct API uses for the identical model.
var liveOpenRouterCatalogModels = []string{
	"anthropic/claude-sonnet-4.5",
	"anthropic/claude-haiku-4.5",
	"anthropic/claude-opus-4.1",
	"openai/gpt-4o",
}

// newOpenRouterFourModelsProfile builds a real *Registry loaded with a
// profile shaped like the reporter's "openrouter-4-models" — an
// OpenRouter-kind profile whose Models list is the live catalog above.
// The adapter is faked (no network); the profile, the registry, and the
// authorization logic that runs before the adapter is ever called are all
// production code.
func newOpenRouterFourModelsProfile(t *testing.T) (*Registry, *fakeAdapter) {
	t.Helper()
	r, _ := newReg(t)
	adapter := &fakeAdapter{
		kind: "openrouter",
		final: llm.Response{
			Content:      []llm.ContentBlock{{Type: "text", Text: "ok"}},
			FinishReason: "stop",
		},
	}
	r.RegisterAdapter(adapter)
	prof := llm.ProviderProfile{
		ID:     "openrouter-4-models",
		Kind:   "openrouter",
		Model:  liveOpenRouterCatalogModels[0],
		Models: liveOpenRouterCatalogModels,
		Cred:   llm.CredentialReference{Kind: "env", Locator: "TEST_REG_OPENROUTER_KEY"},
	}
	if err := r.LoadProfiles([]llm.ProviderProfile{prof}); err != nil {
		t.Fatalf("LoadProfiles: %v", err)
	}
	return r, adapter
}

// TestBundledProfiles_ModelsAreAuthorized is AC-18a: every bundled agent
// profile's declared model resolves to an authorized model on a
// representative live-shaped provider profile, driven through the real
// Registry.Stream authorization path. This is the assertion that would
// have caught the shipped hyphen/dot defect, and it keeps catching it if a
// future bundled profile names a model the active catalog doesn't carry.
func TestBundledProfiles_ModelsAreAuthorized(t *testing.T) {
	profiles, err := coreagents.LoadAll("")
	if err != nil {
		t.Fatalf("agents.LoadAll: %v", err)
	}
	if len(profiles) == 0 {
		t.Fatal("expected at least one bundled agent profile")
	}

	r, adapter := newOpenRouterFourModelsProfile(t)

	for _, p := range profiles {
		p := p
		t.Run(p.ID, func(t *testing.T) {
			if p.Model == "" {
				t.Skip("profile declares no model override; inherits the parent session's model")
			}
			stream, err := r.Stream(context.Background(), llm.GenerationRequest{
				ProfileID: "openrouter-4-models",
				Model:     p.Model,
				SessionID: "s-" + p.ID,
			})
			if err != nil {
				t.Fatalf("bundled profile %q declares model %q, which registry.Stream rejected "+
					"against profile \"openrouter-4-models\" (models: %v): %v",
					p.ID, p.Model, liveOpenRouterCatalogModels, err)
			}
			for range stream.Events() {
			}
			if _, err := stream.Final(); err != nil {
				t.Fatalf("bundled profile %q: stream.Final: %v", p.ID, err)
			}
		})
	}
	if adapter.calls == 0 {
		t.Fatal("expected the fake adapter to have been reached at least once (authorization passed for every profile)")
	}
}

// TestBundledProfile_KnownBadModelID_ActionablePreflightError is AC-18b: a
// known-bad model id (the exact hyphenated typo that shipped) must be
// rejected with an actionable error naming the requested model, the
// profile, and a remedy — BEFORE the adapter (and therefore before any
// paid request) is ever reached. It must not look like, or come from, a
// provider auth/payment failure.
func TestBundledProfile_KnownBadModelID_ActionablePreflightError(t *testing.T) {
	r, adapter := newOpenRouterFourModelsProfile(t)

	const shippedTypo = "anthropic/claude-sonnet-4-5" // hyphen; the real catalog has a dot
	_, err := r.Stream(context.Background(), llm.GenerationRequest{
		ProfileID: "openrouter-4-models",
		Model:     shippedTypo,
		SessionID: "s",
	})
	if err == nil {
		t.Fatal("expected the known-bad model id to be rejected")
	}
	msg := err.Error()
	for _, want := range []string{shippedTypo, "openrouter-4-models", "not authorised"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error message %q does not mention %q", msg, want)
		}
	}
	// "What to do": the message must point at a fixable source, not just
	// state the failure.
	if !strings.Contains(msg, "agent profile") && !strings.Contains(msg, "Settings") {
		t.Errorf("error message %q is not actionable (no remedy named)", msg)
	}
	if adapter.calls != 0 {
		t.Fatalf("adapter was called (%d times) — the bad model reached the wire instead of "+
			"being stopped before a paid request", adapter.calls)
	}
}

// TestRegistry_StreamAuthorizesDefaultModelEvenWithoutOverride guards the
// gap the override-only check used to leave open: when no per-call
// override is given, the profile's OWN default Model must still be a
// member of its AvailableModels() list. Before WP18 this path only ran the
// authorization loop when req.Model differed from prof.Model, so a
// profile whose stored default itself was invalid (e.g. seeded from a
// bundled profile's typoed model id) sailed straight through to the
// adapter — which is how the reporter's session produced the payment
// failure in the same incident.
func TestRegistry_StreamAuthorizesDefaultModelEvenWithoutOverride(t *testing.T) {
	r, adapter := newOpenRouterFourModelsProfile(t)
	// Corrupt the profile's OWN default to the shipped typo, out of band,
	// the way a misconfigured / stale provider profile would.
	prof, err := r.Profile("openrouter-4-models")
	if err != nil {
		t.Fatalf("Profile: %v", err)
	}
	prof.Model = "anthropic/claude-sonnet-4-5" // not in Models
	if err := r.Evict("openrouter-4-models"); err != nil {
		t.Fatalf("Evict: %v", err)
	}
	if err := r.LoadProfiles([]llm.ProviderProfile{prof}); err != nil {
		t.Fatalf("LoadProfiles: %v", err)
	}

	_, err = r.Stream(context.Background(), llm.GenerationRequest{
		ProfileID: "openrouter-4-models",
		// No Model override — the caller trusts the profile's own default.
		SessionID: "s",
	})
	if err == nil {
		t.Fatal("expected the profile's own invalid default model to be rejected")
	}
	if !strings.Contains(err.Error(), "anthropic/claude-sonnet-4-5") {
		t.Errorf("error message %q does not name the bad default model", err.Error())
	}
	if adapter.calls != 0 {
		t.Fatalf("adapter was called (%d times) with an unauthorized default model", adapter.calls)
	}
}

// TestEnvProviderDefaultModels_OpenRouterIsAuthorized closes the gap an
// independent review found in this PR: core/llm/envprovider.DefaultModels
// carried the identical hyphen typo for its "openrouter" entry — a second,
// separate live site for the same defect class, on the cmd/harness-vm and
// served-mode default-model resolution path that core/agents/bundled/*.yaml
// coverage above does not reach (envprovider.go's own doc comment calls this
// table the single source of truth for both callers). Bare "anthropic" is
// intentionally NOT checked here: it is a direct-API kind with no vendor
// prefix and is authorized by construction against any profile whose Models
// list is empty (llm.ProviderProfile.AvailableModels' single-model
// fallback) — the OpenRouter entry is the one that must match a live,
// vendor-namespaced catalog.
func TestEnvProviderDefaultModels_OpenRouterIsAuthorized(t *testing.T) {
	r, adapter := newOpenRouterFourModelsProfile(t)

	model := envprovider.DefaultModels["openrouter"]
	if model == "" {
		t.Fatal("envprovider.DefaultModels has no \"openrouter\" entry")
	}

	stream, err := r.Stream(context.Background(), llm.GenerationRequest{
		ProfileID: "openrouter-4-models",
		Model:     model,
		SessionID: "s",
	})
	if err != nil {
		t.Fatalf("envprovider.DefaultModels[%q] = %q, which registry.Stream rejected "+
			"against profile \"openrouter-4-models\" (models: %v): %v",
			"openrouter", model, liveOpenRouterCatalogModels, err)
	}
	for range stream.Events() {
	}
	if _, err := stream.Final(); err != nil {
		t.Fatalf("stream.Final: %v", err)
	}
	if adapter.calls == 0 {
		t.Fatal("expected the fake adapter to have been reached (authorization passed)")
	}
}
