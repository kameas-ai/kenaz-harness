package chat

// usage_pipeline_test.go — end-to-end trace for the "0 tok · $0.0000"
// footer bug (owner report: OpenRouter model z-ai/glm-5.2, footer stuck at
// zero during and after streaming).
//
// The investigation walked the pipeline in three stages:
//
//  1. core/llm/openrouter: does the SSE parser handle the terminal
//     usage-bearing chunk, which carries an EMPTY choices array? Confirmed
//     correct — handleSSEData reads `env.Usage` independently of the
//     `choices` loop (core/llm/openrouter/openrouter.go), and
//     TestAdapter_Stream_HappyPath in that package already exercises this
//     exact shape end-to-end through Stream().Final().
//  2. Does that parsed usage reach the LLMProviderAdapter's LastResponse(),
//     which buildChatRunner's UsageHook (HookPostLLM) reads? This file
//     confirms that too: Generate() stores whatever corellm.Stream.Final()
//     returns verbatim into a.lastResp, with no field dropped in between.
//  3. Does the frontend footer read it? NO — this was the actual bug.
//     frontend/src/views/sessions/SessionsView.vue passed the ChatInput
//     `estimate` prop a hardcoded `{ tokens: 0, usd: 0 }` literal instead
//     of the session's live usage snapshot (`session.lastUsage`, populated
//     by the SAME `session.usage.updated` broker event the context-window
//     meter already consumed correctly — which is why the owner observed
//     the context meter working while the footer stayed at zero). Fixed
//     in SessionsView.vue; see the frontend test in
//     frontend/src/views/sessions/__tests__/SessionsView.test.ts
//     ("composer footer reflects live session usage instead of a static
//     zero").
//
// This Go test locks stage (2) of the trace: a stream whose Final()
// response carries usage + provider cost (as produced by a real
// empty-choices OpenRouter terminal frame) must survive Generate()
// unmodified into LastResponse(), which is exactly what the usage hook
// consumes.

import (
	"context"
	"testing"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
)

// usageStubStream is a corellm.Stream whose Final() returns a canned
// Response carrying Usage + Cost, simulating what openrouter.chatStream
// produces after receiving a terminal SSE frame with an empty `choices`
// array and a populated `usage` object (see openrouter.go handleSSEData).
type usageStubStream struct {
	final corellm.Response
}

func (s *usageStubStream) Events() <-chan corellm.StreamEvent {
	ch := make(chan corellm.StreamEvent)
	close(ch)
	return ch
}
func (s *usageStubStream) Cancel() error                    { return nil }
func (s *usageStubStream) Final() (corellm.Response, error) { return s.final, nil }

// usageStubRegistry is a minimal corellm.Registry that always returns the
// configured stream, independent of the request. Only the methods
// LLMProviderAdapter.Generate actually calls are implemented meaningfully.
type usageStubRegistry struct {
	stream corellm.Stream
}

func (r *usageStubRegistry) RegisterAdapter(_ corellm.ProviderAdapter)      {}
func (r *usageStubRegistry) LoadProfiles(_ []corellm.ProviderProfile) error { return nil }
func (r *usageStubRegistry) Evict(_ string) error                           { return nil }
func (r *usageStubRegistry) Profile(_ string) (corellm.ProviderProfile, error) {
	return corellm.ProviderProfile{ID: "p-openrouter", Kind: "openrouter", Model: "z-ai/glm-5.2"}, nil
}
func (r *usageStubRegistry) PreflightAll(_ context.Context) []corellm.PreflightResult { return nil }
func (r *usageStubRegistry) Stream(_ context.Context, _ corellm.GenerationRequest) (corellm.Stream, error) {
	return r.stream, nil
}

// TestGenerate_UsageAndCostSurviveToLastResponse pins stage (2) of the
// token/cost footer trace: a stream whose Final() carries provider-reported
// usage + cost (the shape openrouter.go produces from a terminal SSE frame
// with empty choices) must come out of LastResponse() unchanged — the exact
// value buildChatRunner's UsageHook reads via HookPostLLM to publish
// session.usage.updated.
func TestGenerate_UsageAndCostSurviveToLastResponse(t *testing.T) {
	want := corellm.Response{
		Content:      []corellm.ContentBlock{{Type: "text", Text: "hello"}},
		FinishReason: "stop",
		Usage: corellm.Usage{
			InputTokens:  1234,
			OutputTokens: 567,
		},
		Cost: corellm.Cost{
			Currency: "USD",
			Total:    0.0456,
			Source:   "provider",
		},
	}
	reg := &usageStubRegistry{stream: &usageStubStream{final: want}}
	adapter := NewLLMProviderAdapter(reg, "p-openrouter", "z-ai/glm-5.2", nil, nil)

	out, err := adapter.Generate(context.Background(), coreag.LLMRequest{
		SystemPrompt: "base",
		Messages:     []coreag.Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	// The kernel-facing LLMResponse must carry the totals too (this is what
	// the kernel's own usage/cost bookkeeping reads).
	if out.TokensUsed != want.Usage.InputTokens+want.Usage.OutputTokens {
		t.Errorf("LLMResponse.TokensUsed = %d, want %d", out.TokensUsed, want.Usage.InputTokens+want.Usage.OutputTokens)
	}
	if out.CostUSD != want.Cost.Total {
		t.Errorf("LLMResponse.CostUSD = %v, want %v", out.CostUSD, want.Cost.Total)
	}

	// LastResponse() is what the UsageHook (HookPostLLM, registered in
	// buildChatRunner) reads to build the UsageTurn it persists and the
	// SessionUsagePayload it publishes on session.usage.updated. If this
	// ever comes back zeroed, the frontend footer regresses exactly the
	// way it did here — even though the frontend fix (reading
	// session.lastUsage instead of a hardcoded stub) is what actually
	// resolves the reported bug.
	got := adapter.LastResponse()
	if got.Usage.InputTokens != want.Usage.InputTokens || got.Usage.OutputTokens != want.Usage.OutputTokens {
		t.Errorf("LastResponse().Usage = %+v, want %+v", got.Usage, want.Usage)
	}
	if got.Cost.Total != want.Cost.Total || got.Cost.Source != want.Cost.Source {
		t.Errorf("LastResponse().Cost = %+v, want %+v", got.Cost, want.Cost)
	}
}
