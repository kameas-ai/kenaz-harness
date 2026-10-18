package chat

import (
	"context"
	"strings"
	"sync"
	"testing"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/llm/registry"
)

// recordingSentinelAdapter is a minimal corellm.ProviderAdapter that
// records the GenerationRequest it's called with and returns a trivial
// closed stream. Used (instead of a hand-rolled corellm.Registry fake
// that reimplements Stream — every other fixture in this package does
// that, which bypasses the registry's authorisation check this bug
// lives in entirely) so the test drives the REAL
// core/llm/registry.Registry.Stream authorisation logic.
type recordingSentinelAdapter struct {
	kind string

	mu        sync.Mutex
	calls     int
	lastReq   corellm.GenerationRequest
	effective string // model actually used, mirroring every real adapter's own req.Model-over-prof.Model fallback (see core/llm/openrouter/openrouter.go:533-535)
}

func (a *recordingSentinelAdapter) Kind() string { return a.kind }
func (a *recordingSentinelAdapter) Capabilities(_ string) corellm.CapabilityDescriptor {
	return corellm.CapabilityDescriptor{}
}
func (a *recordingSentinelAdapter) Stream(_ context.Context, req corellm.GenerationRequest, prof corellm.ProviderProfile, _ []byte) (corellm.Stream, error) {
	a.mu.Lock()
	a.calls++
	a.lastReq = req
	// Real adapters fall back to the profile's own default model when
	// req.Model is empty (openrouter.go: "model := prof.Model; if
	// req.Model != "" { model = req.Model }"). Replicating that here is
	// what makes gotModel below the actual bytes a live provider call
	// would carry, not just whatever happened to land on req.Model.
	model := prof.Model
	if req.Model != "" {
		model = req.Model
	}
	a.effective = model
	a.mu.Unlock()
	return &cannedStream{}, nil
}

func (a *recordingSentinelAdapter) snapshot() (int, corellm.GenerationRequest, string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.calls, a.lastReq, a.effective
}

// newRealDefaultSentinelFixture builds a REAL corellm/registry.Registry
// (not a fake that reimplements Stream) loaded with a profile shaped
// like the live failure: a concrete OpenRouter-style allowlist that
// structurally cannot contain the "default" sentinel, because it's a
// real deployed model catalog. Returns the registry, the adapter (to
// inspect what request actually reached "the provider"), and the
// profile's declared default model (the value a correct fix must
// resolve "default" down to).
func newRealDefaultSentinelFixture(t *testing.T) (*registry.Registry, *recordingSentinelAdapter, string) {
	t.Helper()
	reg, err := registry.New(registry.Options{})
	if err != nil {
		t.Fatalf("registry.New: %v", err)
	}
	adapter := &recordingSentinelAdapter{kind: "openrouter"}
	reg.RegisterAdapter(adapter)

	allowed := []string{
		"aion-labs/aion-2.0",
		"anthropic/claude-sonnet-4-5",
		"z-ai/glm-latest",
	}
	prof := corellm.ProviderProfile{
		ID:     "openrouter-4-models",
		Kind:   "openrouter",
		Model:  allowed[0],
		Models: allowed,
		Cred:   corellm.CredentialReference{Kind: "env", Locator: "OPENROUTER_API_KEY"},
	}
	if err := reg.LoadProfiles([]corellm.ProviderProfile{prof}); err != nil {
		t.Fatalf("LoadProfiles: %v", err)
	}
	return reg, adapter, allowed[0]
}

// TestGenerate_DefaultSentinel_RealRegistry_ReproducesLiveFailure is a
// production-path repro of the live `wails dev` failure:
//
//	loop: node "agent_loop": body assistant_turn: model: node "assistant_turn":
//	chat: registry stream: llm: model "default" not authorised for profile
//	"openrouter-4-models" (allowed: [...])
//
// It drives the REAL corellm/registry.Registry.Stream authorisation
// check via LLMProviderAdapter.Generate, exactly the path a live
// assistant_turn node takes: modelOverride empty (an assistant_turn
// node relying on the session's chosen model, not a per-node pin) and
// req.Model == "default" (what exec_compute.go's modelExecutor sends
// verbatim from ModelAttrs.Model — see
// core/agentgraph/graphs/chat_default_classic.yaml:137,
// core/rpc/views/agentgraph/library/{toolloop_default,chat_default}.yaml
// — and six further producers: reflectExecutor, reviewExecutor,
// plannerExecutor, routerAskModel, and both
// exec_escalation_ladder.go rungs).
//
// This asserts the FIXED behaviour: no authorisation error, and the
// adapter that stands in for "the provider" sees the profile's real
// default model, never the literal string "default". Before the fix in
// Generate() (core/rpc/views/agentgraph/chat/llm_provider_adapter.go),
// this same assertion failed because "default" was forwarded verbatim
// past the override seam and rejected by the registry's authorisation
// check, which only ever knows concrete provider model ids.
func TestGenerate_DefaultSentinel_RealRegistry_ReproducesLiveFailure(t *testing.T) {
	reg, adapter, wantModel := newRealDefaultSentinelFixture(t)

	// modelOverride empty: the assistant_turn shape.
	llmAdapter := NewLLMProviderAdapter(reg, "openrouter-4-models", "", nil, nil)

	_, err := llmAdapter.Generate(context.Background(), coreag.LLMRequest{
		SystemPrompt: "base",
		Model:        "default",
	})
	if err != nil {
		if strings.Contains(err.Error(), "not authorised for profile") {
			t.Fatalf("the \"default\" sentinel leaked past Generate() and was rejected by the registry's authorisation check (the live bug): %v", err)
		}
		t.Fatalf("Generate: unexpected error: %v", err)
	}

	calls, gotReq, effective := adapter.snapshot()
	if calls != 1 {
		t.Fatalf("expected exactly 1 adapter.Stream call, got %d", calls)
	}
	if gotReq.Model == "default" {
		t.Fatalf("adapter.Stream saw the literal sentinel \"default\" as a model id — it must be resolved before reaching the provider")
	}
	if effective != wantModel {
		t.Fatalf("effective model = %q, want the profile default %q", effective, wantModel)
	}
}

// TestGenerate_DefaultSentinel_WithModelOverride_PrefersOverride pins
// that when a session/turn DOES have a concrete modelOverride pinned,
// an authored "default" attr on the node still defers to it (the
// sentinel means "no opinion", not "explicitly request the profile
// default over whatever the session already picked").
func TestGenerate_DefaultSentinel_WithModelOverride_PrefersOverride(t *testing.T) {
	reg, adapter, _ := newRealDefaultSentinelFixture(t)

	llmAdapter := NewLLMProviderAdapter(reg, "openrouter-4-models", "anthropic/claude-sonnet-4-5", nil, nil)

	_, err := llmAdapter.Generate(context.Background(), coreag.LLMRequest{
		SystemPrompt: "base",
		Model:        "default",
	})
	if err != nil {
		t.Fatalf("Generate: unexpected error: %v", err)
	}

	_, _, effective := adapter.snapshot()
	if effective != "anthropic/claude-sonnet-4-5" {
		t.Fatalf("effective model = %q, want the pinned override %q", effective, "anthropic/claude-sonnet-4-5")
	}
}
