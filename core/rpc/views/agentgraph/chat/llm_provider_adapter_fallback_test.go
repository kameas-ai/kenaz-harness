package chat

import (
	"context"
	"sync"
	"testing"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
)

// fallbackRecordingRegistry is a race-safe corellm.Registry fake that fails
// the first call with a transient 5xx error (matching the bundled
// "anthropic-with-openrouter-fallback" chain's error_5xx trigger) and
// succeeds on every subsequent call, recording each GenerationRequest so
// the test can assert the hop's (ProfileID, Model) landed on the seam
// (model-request-path-live-01PMDL01 WP07).
type fallbackRecordingRegistry struct {
	mu       sync.Mutex
	requests []corellm.GenerationRequest
	profile  corellm.ProviderProfile
}

func (r *fallbackRecordingRegistry) RegisterAdapter(_ corellm.ProviderAdapter)      {}
func (r *fallbackRecordingRegistry) LoadProfiles(_ []corellm.ProviderProfile) error { return nil }
func (r *fallbackRecordingRegistry) Evict(_ string) error                           { return nil }
func (r *fallbackRecordingRegistry) Profile(_ string) (corellm.ProviderProfile, error) {
	return r.profile, nil
}
func (r *fallbackRecordingRegistry) PreflightAll(_ context.Context) []corellm.PreflightResult {
	return nil
}
func (r *fallbackRecordingRegistry) Stream(_ context.Context, req corellm.GenerationRequest) (corellm.Stream, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requests = append(r.requests, req)
	if len(r.requests) == 1 {
		return nil, &corellm.ErrTransient{Status: 503, Message: "overloaded"}
	}
	return &cannedStream{}, nil
}

func (r *fallbackRecordingRegistry) snapshot() []corellm.GenerationRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]corellm.GenerationRequest, len(r.requests))
	copy(out, r.requests)
	return out
}

// TestGenerate_FallbackChainId_RoutesThroughRunnerOnPrimaryFailure is the
// WP07 last-mile test for model-request-path-live-01PMDL01: before this
// change Generate() called a.reg.Stream directly regardless of
// req.FallbackChainId, so a failing primary call never consulted the
// fallback chain even when the firing node authored one. Asserts that a
// node-level FallbackChainId routes the call through a fallback.Runner
// (StoreResolver falling through to the bundled default chain), and that
// a failing primary hops to the chain's single entry
// (provider_id: openrouter, model: openai/gpt-4o) which then succeeds.
func TestGenerate_FallbackChainId_RoutesThroughRunnerOnPrimaryFailure(t *testing.T) {
	reg := &fallbackRecordingRegistry{}
	adapter := NewLLMProviderAdapter(reg, "anthropic", "claude-3", nil, nil)

	if _, err := adapter.Generate(context.Background(), coreag.LLMRequest{
		SystemPrompt:    "base",
		FallbackChainId: "anthropic-with-openrouter-fallback",
	}); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	reqs := reg.snapshot()
	if len(reqs) != 2 {
		t.Fatalf("expected 2 Stream() calls (primary fail + fallback hop), got %d", len(reqs))
	}
	hop := reqs[1]
	if hop.ProfileID != "openrouter" {
		t.Errorf("hop ProfileID = %q, want %q", hop.ProfileID, "openrouter")
	}
	if hop.Model != "openai/gpt-4o" {
		t.Errorf("hop Model = %q, want %q", hop.Model, "openai/gpt-4o")
	}
}

// TestGenerate_EmptyFallbackChainId_LeavesPathUnchanged asserts that
// omitting FallbackChainId (the pre-WP07 default) never constructs a
// fallback.Runner — the registry sees exactly one Stream() call even when
// that call fails, matching pre-WP07 behaviour (the retry policy alone
// governs re-attempts, not the fallback chain).
func TestGenerate_EmptyFallbackChainId_LeavesPathUnchanged(t *testing.T) {
	reg := &retryCountingRegistry{
		profile: corellm.ProviderProfile{
			Retry: &corellm.RetryPolicy{MaxAttempts: 1, BaseMS: 1, Jitter: "none"},
		},
		failN: -1, // always fails
	}
	adapter := NewLLMProviderAdapter(reg, "profile-1", "openai/gpt-4o", nil, nil)

	_, err := adapter.Generate(context.Background(), coreag.LLMRequest{SystemPrompt: "base"})
	if err == nil {
		t.Fatal("expected an error from an always-failing stream with no fallback chain")
	}
	if got := reg.callCount(); got != 1 {
		t.Fatalf("expected exactly 1 Stream() call (no FallbackChainId, MaxAttempts=1), got %d", got)
	}
}
