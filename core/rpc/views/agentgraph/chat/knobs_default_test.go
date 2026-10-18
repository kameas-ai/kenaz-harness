package chat

// knobs_default_test.go — model-settings-reach-the-model-01PMZ101
// UNIT-6 / WP10, AC-009's send-path half: the store round-trip alone is
// not consumption (CLAUDE.md sweep pass 4) — this pins that the session
// default actually reaches GenerationRequest.Knobs, and that a resolver
// error or nil result degrades to no override rather than failing the
// turn (mirroring buildAttachmentsBlock's fail-open posture for the same
// class of optional per-session read).
//
// A pure seam-boundary unit test (fake resolver, fake registry) — no SQL
// to bypass here (spec §8 rule 3); the real-sqlite half of AC-009 lives
// in core/storage/sqlite/upgrade_knobs_default_test.go.

import (
	"context"
	"errors"
	"testing"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
)

// fakeKnobsDefaultResolver is a race-safe (single-goroutine-use in these
// tests, but guarded per CLAUDE.md's race-safe-fakes convention anyway)
// stand-in for *session.Manager.
type fakeKnobsDefaultResolver struct {
	knobs *corellm.RequestKnobs
	err   error
}

func (f *fakeKnobsDefaultResolver) GetKnobsDefault(_ context.Context, _ string) (*corellm.RequestKnobs, error) {
	return f.knobs, f.err
}

// TestGenerate_MergesSessionKnobsDefault is the "reaches the model" half
// of WP10: a session-level RequestKnobs override, once wired via
// WithKnobsDefault, must land on GenerationRequest.Knobs — not merely
// round-trip through the store (spec FR-009).
func TestGenerate_MergesSessionKnobsDefault(t *testing.T) {
	t.Parallel()

	want := &corellm.RequestKnobs{
		Reasoning: &corellm.ReasoningConfig{OpenAIEffort: "high"},
	}

	reg := &capturingRegistry{}
	adapter := NewLLMProviderAdapter(reg, "profile-1", "openai/gpt-4o", nil, nil).
		WithSessionID("session-1").
		WithKnobsDefault(&fakeKnobsDefaultResolver{knobs: want})

	if _, err := adapter.Generate(context.Background(), coreag.LLMRequest{SystemPrompt: "base"}); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	gen := reg.snapshot()
	if gen.Knobs == nil {
		t.Fatal("GenerationRequest.Knobs is nil; want the session default merged onto the wire request — " +
			"this is the WP10 defect: the column round-trips through the store but nothing downstream reads it")
	}
	if gen.Knobs.Reasoning == nil || gen.Knobs.Reasoning.OpenAIEffort != "high" {
		t.Errorf("GenerationRequest.Knobs = %+v, want Reasoning.OpenAIEffort=\"high\"", gen.Knobs)
	}
}

// TestGenerate_NoKnobsDefaultResolverLeavesKnobsNil pins the pre-existing
// behaviour when the resolver is not wired (nil) — every request's Knobs
// stays nil, byte-identical to every chat turn before this field
// existed. Without this assertion, a bug that always set some default
// Knobs value could pass the positive test above and still be wrong.
func TestGenerate_NoKnobsDefaultResolverLeavesKnobsNil(t *testing.T) {
	t.Parallel()

	reg := &capturingRegistry{}
	adapter := NewLLMProviderAdapter(reg, "profile-1", "openai/gpt-4o", nil, nil).
		WithSessionID("session-1")
	// Deliberately no WithKnobsDefault call.

	if _, err := adapter.Generate(context.Background(), coreag.LLMRequest{SystemPrompt: "base"}); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if gen := reg.snapshot(); gen.Knobs != nil {
		t.Errorf("GenerationRequest.Knobs = %+v, want nil when no resolver is wired", gen.Knobs)
	}
}

// TestGenerate_KnobsDefaultNilOverrideLeavesKnobsNil pins the "no
// override set for this session" case — a wired resolver returning
// (nil, nil) (the store's own contract for a session that never opened
// the tune panel) must not be turned into an empty *RequestKnobs{}. An
// empty-but-non-nil Knobs would still take encoder precedence over
// Params for every openaiwire-based adapter (body.go: "Apply Knobs
// (highest precedence)"), silently discarding graph-node-authored
// sampling knobs for a session that never asked for an override.
func TestGenerate_KnobsDefaultNilOverrideLeavesKnobsNil(t *testing.T) {
	t.Parallel()

	reg := &capturingRegistry{}
	adapter := NewLLMProviderAdapter(reg, "profile-1", "openai/gpt-4o", nil, nil).
		WithSessionID("session-1").
		WithKnobsDefault(&fakeKnobsDefaultResolver{knobs: nil})

	if _, err := adapter.Generate(context.Background(), coreag.LLMRequest{SystemPrompt: "base"}); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if gen := reg.snapshot(); gen.Knobs != nil {
		t.Errorf("GenerationRequest.Knobs = %+v, want nil when the resolver reports no override", gen.Knobs)
	}
}

// TestGenerate_KnobsDefaultResolverErrorDegradesToNoOverride mirrors
// buildAttachmentsBlock's fail-open posture: a resolver error must not
// fail the turn, and must not be mistaken for "no override" being
// silently promoted to a zero-value struct either — the request simply
// proceeds with Knobs unset, same as if the resolver were not wired.
func TestGenerate_KnobsDefaultResolverErrorDegradesToNoOverride(t *testing.T) {
	t.Parallel()

	reg := &capturingRegistry{}
	adapter := NewLLMProviderAdapter(reg, "profile-1", "openai/gpt-4o", nil, nil).
		WithSessionID("session-1").
		WithKnobsDefault(&fakeKnobsDefaultResolver{err: errors.New("store unavailable")})

	if _, err := adapter.Generate(context.Background(), coreag.LLMRequest{SystemPrompt: "base"}); err != nil {
		t.Fatalf("Generate: %v (a resolver error must degrade, not fail the turn)", err)
	}

	if gen := reg.snapshot(); gen.Knobs != nil {
		t.Errorf("GenerationRequest.Knobs = %+v, want nil when the resolver errors", gen.Knobs)
	}
}
