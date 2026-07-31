package chat

import (
	"context"
	"sync"
	"testing"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
)

// retryCountingRegistry is a race-safe corellm.Registry fake that returns
// a transient error for the first failN calls to Stream (failN < 0 means
// "always fail") and a canned success afterwards. Used to observe how
// many times Generate's retry wrapper actually calls through to
// registry.Stream for a given profile-resolved retry.Policy
// (model-request-path-live-01PMDL01 WP02).
type retryCountingRegistry struct {
	mu      sync.Mutex
	calls   int
	profile corellm.ProviderProfile
	failN   int
}

func (r *retryCountingRegistry) RegisterAdapter(_ corellm.ProviderAdapter)      {}
func (r *retryCountingRegistry) LoadProfiles(_ []corellm.ProviderProfile) error { return nil }
func (r *retryCountingRegistry) Evict(_ string) error                           { return nil }
func (r *retryCountingRegistry) Profile(_ string) (corellm.ProviderProfile, error) {
	return r.profile, nil
}
func (r *retryCountingRegistry) PreflightAll(_ context.Context) []corellm.PreflightResult {
	return nil
}
func (r *retryCountingRegistry) Stream(_ context.Context, _ corellm.GenerationRequest) (corellm.Stream, error) {
	r.mu.Lock()
	r.calls++
	n := r.calls
	r.mu.Unlock()
	if r.failN < 0 || n <= r.failN {
		return nil, &corellm.ErrTransient{Status: 503, Message: "boom"}
	}
	return &cannedStream{}, nil
}

func (r *retryCountingRegistry) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

// TestGenerate_RetryDrivenByProfilePolicy is WP02 of
// model-request-path-live-01PMDL01: Generate() previously wrapped
// registry.Stream with a hardcoded retry.StreamPolicy{MaxAttempts:3,...}
// for every profile. It must instead resolve the policy from the active
// profile's Retry field (via retry.StreamPolicyFromLLM), so a profile
// that disables retries stops after one attempt, and a profile that
// leaves Retry unset still gets a sane default (matching
// retry.DefaultRetryPolicy) that permits recovery from one transient
// error.
func TestGenerate_RetryDrivenByProfilePolicy(t *testing.T) {
	t.Run("profile MaxAttempts=1 stops after the first failure", func(t *testing.T) {
		reg := &retryCountingRegistry{
			profile: corellm.ProviderProfile{
				Retry: &corellm.RetryPolicy{MaxAttempts: 1, BaseMS: 1, Jitter: "none"},
			},
			failN: -1, // always fails
		}
		adapter := NewLLMProviderAdapter(reg, "profile-1", "openai/gpt-4o", nil, nil)

		_, err := adapter.Generate(context.Background(), coreag.LLMRequest{SystemPrompt: "base"})
		if err == nil {
			t.Fatal("expected an error from an always-failing stream")
		}
		if got := reg.callCount(); got != 1 {
			t.Fatalf("expected exactly 1 Stream() call (MaxAttempts=1, no retry), got %d", got)
		}
	})

	t.Run("unset profile retry falls back to a default that allows recovery", func(t *testing.T) {
		reg := &retryCountingRegistry{
			profile: corellm.ProviderProfile{}, // Retry left nil
			failN:   1,                         // one transient failure, then success
		}
		adapter := NewLLMProviderAdapter(reg, "profile-1", "openai/gpt-4o", nil, nil)

		_, err := adapter.Generate(context.Background(), coreag.LLMRequest{SystemPrompt: "base"})
		if err != nil {
			t.Fatalf("expected the default retry policy to recover from one transient error: %v", err)
		}
		if got := reg.callCount(); got != 2 {
			t.Fatalf("expected exactly 2 Stream() calls (1 failure + 1 retry), got %d", got)
		}
	})
}
