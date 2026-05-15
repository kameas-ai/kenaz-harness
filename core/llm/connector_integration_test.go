package llm_test

// connector_integration_test.go — end-to-end scenario tests for the
// model-fallback-routing mission (model-fallback-routing-01NDFSEX04 WP06).
//
// These tests exercise the full Runner → Streamer loop using synthetic
// provider errors, without requiring a real network connection. They
// complement the unit tests in core/llm/fallback/ by exercising the
// interplay between the typed error taxonomy (ErrTransient, ErrAuth, …)
// and the trigger-matching logic in the fallback evaluator.

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/sigil-tech/kaneaz-harness/core/llm"
	"github.com/sigil-tech/kaneaz-harness/core/llm/fallback"
)

// ── Helpers ────────────────────────────────────────────────────────────────

// syntheticStreamer simulates a sequence of Stream call results. Each
// call pops the next item from the queue; if the queue is empty the call
// returns errExhausted.
type syntheticStreamer struct {
	results []syntheticResult
	cursor  int
}

type syntheticResult struct {
	stream llm.Stream
	err    error
}

var errExhausted = errors.New("synthetic: result queue exhausted")

func (s *syntheticStreamer) Stream(_ context.Context, _ llm.GenerationRequest) (llm.Stream, error) {
	if s.cursor >= len(s.results) {
		return nil, errExhausted
	}
	r := s.results[s.cursor]
	s.cursor++
	return r.stream, r.err
}

// nopStream is a no-op llm.Stream returned on the happy path.
type nopStream struct{}

func (nopStream) Events() <-chan llm.StreamEvent { ch := make(chan llm.StreamEvent); close(ch); return ch }
func (nopStream) Cancel() error                  { return nil }
func (nopStream) Final() (llm.Response, error)   { return llm.Response{Attempts: 1}, nil }

// fixedResolver always resolves to the same Chain.
type fixedResolver struct{ chain *fallback.Chain }

func (r *fixedResolver) Resolve(_ context.Context, _ string) (*fallback.Chain, error) {
	return r.chain, nil
}

// errAttempts collects FallbackAttemptedEvent records from the AttemptHook.
type errAttempts struct {
	events []fallback.FallbackAttemptedEvent
}

func (ea *errAttempts) onAttempt(_ context.Context, e fallback.FallbackAttemptedEvent) {
	ea.events = append(ea.events, e)
}

// ── Tests ──────────────────────────────────────────────────────────────────

// TestConnector_FallbackOnTransient — primary call returns a 529
// (transient / rate-limit), runner hops to the fallback entry, which
// succeeds.
func TestConnector_FallbackOnTransient(t *testing.T) {
	t.Parallel()

	chain := &fallback.Chain{
		ID:   "test-fallback",
		Name: "test",
		Entries: []fallback.ChainEntry{
			{
				ProviderID:  "fallback-provider",
				Model:       "fallback-model",
				Triggers:    []fallback.TriggerCondition{fallback.TriggerError5xx},
				MaxAttempts: 1,
			},
		},
	}
	if err := chain.Validate(); err != nil {
		t.Fatalf("chain.Validate: %v", err)
	}

	transient529 := &llm.ErrTransient{Status: 529, Message: "server overloaded"}
	streamer := &syntheticStreamer{
		results: []syntheticResult{
			{err: transient529},  // primary fails
			{stream: nopStream{}}, // fallback succeeds
		},
	}

	attempts := &errAttempts{}
	runner := fallback.NewRunner(
		streamer,
		&fixedResolver{chain: chain},
		fallback.WithAttemptHook(attempts.onAttempt),
	)

	req := llm.GenerationRequest{
		ProfileID: "primary-provider",
		SessionID: "sess-integration-01",
	}

	stream, err := runner.Stream(context.Background(), req)
	if err != nil {
		t.Fatalf("runner.Stream() error = %v; want nil (fallback should succeed)", err)
	}
	if stream == nil {
		t.Fatal("runner.Stream() stream = nil; want non-nil")
	}

	// Exactly one fallback attempt should have been recorded.
	if len(attempts.events) != 1 {
		t.Fatalf("attempt events len=%d; want 1", len(attempts.events))
	}
	e := attempts.events[0]
	if e.FromProfile != "primary-provider" {
		t.Errorf("event.FromProfile=%q; want %q", e.FromProfile, "primary-provider")
	}
	if e.ToProfile != "fallback-provider" {
		t.Errorf("event.ToProfile=%q; want %q", e.ToProfile, "fallback-provider")
	}
	if e.ToModel != "fallback-model" {
		t.Errorf("event.ToModel=%q; want %q", e.ToModel, "fallback-model")
	}
	if e.Attempt != 1 {
		t.Errorf("event.Attempt=%d; want 1", e.Attempt)
	}
}

// TestConnector_NoFallbackWhenPrimarySucceeds — when the primary call
// succeeds on the first attempt, no fallback is attempted.
func TestConnector_NoFallbackWhenPrimarySucceeds(t *testing.T) {
	t.Parallel()

	chain := &fallback.Chain{
		ID:   "test-fallback",
		Name: "test",
		Entries: []fallback.ChainEntry{
			{
				ProviderID:  "fallback-provider",
				Triggers:    []fallback.TriggerCondition{fallback.TriggerError5xx},
				MaxAttempts: 1,
			},
		},
	}

	streamer := &syntheticStreamer{
		results: []syntheticResult{
			{stream: nopStream{}}, // primary succeeds immediately
		},
	}

	attempts := &errAttempts{}
	runner := fallback.NewRunner(
		streamer,
		&fixedResolver{chain: chain},
		fallback.WithAttemptHook(attempts.onAttempt),
	)

	stream, err := runner.Stream(context.Background(), llm.GenerationRequest{})
	if err != nil {
		t.Fatalf("runner.Stream() error = %v; want nil", err)
	}
	if stream == nil {
		t.Fatal("stream = nil; want non-nil")
	}

	// No fallback events when primary succeeds.
	if len(attempts.events) != 0 {
		t.Errorf("attempt events = %d; want 0", len(attempts.events))
	}
	// Only one Stream call (the primary).
	if streamer.cursor != 1 {
		t.Errorf("streamer calls = %d; want 1", streamer.cursor)
	}
}

// TestConnector_NoFallbackWhenTriggerMismatches — a 401 auth error does
// NOT trigger a chain configured for error_5xx.
func TestConnector_NoFallbackWhenTriggerMismatches(t *testing.T) {
	t.Parallel()

	chain := &fallback.Chain{
		ID:   "test-5xx-only",
		Name: "5xx only",
		Entries: []fallback.ChainEntry{
			{
				ProviderID:  "fallback-provider",
				Triggers:    []fallback.TriggerCondition{fallback.TriggerError5xx},
				MaxAttempts: 1,
			},
		},
	}

	authErr := &llm.ErrAuth{Status: 401, Message: "invalid api key"}
	streamer := &syntheticStreamer{
		results: []syntheticResult{
			{err: authErr}, // primary fails with auth error
		},
	}

	runner := fallback.NewRunner(
		streamer,
		&fixedResolver{chain: chain},
	)

	_, err := runner.Stream(context.Background(), llm.GenerationRequest{})
	if err == nil {
		t.Fatal("runner.Stream() = nil; want auth error (trigger mismatch, no fallback)")
	}

	// Should surface the original auth error, not ErrFallbackExhausted.
	var authE *llm.ErrAuth
	if !errors.As(err, &authE) {
		t.Errorf("err = %T (%v); want *llm.ErrAuth", err, err)
	}
}

// TestConnector_WholeChainceilingHonoured — the runner stops after
// MaxWholeChainAttempts hops even when the chain has more entries.
func TestConnector_WholeChainceilingHonoured(t *testing.T) {
	t.Parallel()

	// Build a chain with MaxWholeChainAttempts+2 entries so the ceiling
	// fires before the chain is exhausted.
	ceiling := fallback.MaxWholeChainAttempts
	entries := make([]fallback.ChainEntry, ceiling+2)
	for i := range entries {
		entries[i] = fallback.ChainEntry{
			ProviderID:  fmt.Sprintf("hop-%d", i+1),
			Triggers:    []fallback.TriggerCondition{fallback.TriggerErrorAny},
			MaxAttempts: 1,
		}
	}
	chain := &fallback.Chain{
		ID:      "big-chain",
		Name:    "big",
		Entries: entries,
	}

	// Build a streamer that always fails with a transient error.
	const numCalls = 20 // more than enough
	results := make([]syntheticResult, numCalls)
	for i := range results {
		results[i].err = &llm.ErrTransient{Status: 500, Message: "down"}
	}
	streamer := &syntheticStreamer{results: results}

	attempts := &errAttempts{}
	runner := fallback.NewRunner(
		streamer,
		&fixedResolver{chain: chain},
		fallback.WithAttemptHook(attempts.onAttempt),
	)

	_, err := runner.Stream(context.Background(), llm.GenerationRequest{})
	if err == nil {
		t.Fatal("runner.Stream() = nil; want exhaustion error")
	}

	var exhausted *fallback.ErrFallbackExhausted
	if !errors.As(err, &exhausted) {
		t.Fatalf("err type = %T (%v); want *fallback.ErrFallbackExhausted", err, err)
	}

	// The runner must not have fired more than the whole-chain ceiling.
	if exhausted.Attempts != ceiling {
		t.Errorf("Attempts=%d; want exactly %d (whole-chain ceiling)",
			exhausted.Attempts, ceiling)
	}
	// Attempt hook must have been called exactly `ceiling` times.
	if len(attempts.events) != ceiling {
		t.Errorf("attempt events = %d; want %d", len(attempts.events), ceiling)
	}
}

// TestConnector_PolicyDenyBlocks — when the checkPolicy func returns an
// error the runner returns the primary error (fail-closed).
func TestConnector_PolicyDenyBlocks(t *testing.T) {
	t.Parallel()

	chain := &fallback.Chain{
		ID:   "policy-guarded",
		Name: "guarded",
		Entries: []fallback.ChainEntry{
			{
				ProviderID:  "fallback-provider",
				Triggers:    []fallback.TriggerCondition{fallback.TriggerError5xx},
				MaxAttempts: 1,
			},
		},
	}

	primaryErr := &llm.ErrTransient{Status: 503, Message: "service unavailable"}
	streamer := &syntheticStreamer{
		results: []syntheticResult{
			{err: primaryErr}, // primary fails
		},
	}

	policyDenyErr := errors.New("cedar: access denied")
	runner := fallback.NewRunner(
		streamer,
		&fixedResolver{chain: chain},
		fallback.WithPolicyCheck(func(_ context.Context, _ string) error {
			return policyDenyErr
		}),
	)

	_, err := runner.Stream(context.Background(), llm.GenerationRequest{})
	if err == nil {
		t.Fatal("runner.Stream() = nil; want error (policy deny blocks fallback)")
	}

	// Must surface the primary error (fail-closed behaviour), not the
	// policy denial, and not a fallback-exhausted error.
	var transientE *llm.ErrTransient
	if !errors.As(err, &transientE) {
		t.Errorf("err = %T (%v); want *llm.ErrTransient (primary err)", err, err)
	}
}

// TestConnector_FallbackOn429 — 429 rate-limit triggers a chain
// configured with error_429.
func TestConnector_FallbackOn429(t *testing.T) {
	t.Parallel()

	chain := &fallback.Chain{
		ID:   "ratelimit-fallback",
		Name: "rate-limit fallback",
		Entries: []fallback.ChainEntry{
			{
				ProviderID:  "secondary-provider",
				Triggers:    []fallback.TriggerCondition{fallback.TriggerError429},
				MaxAttempts: 1,
			},
		},
	}

	streamer := &syntheticStreamer{
		results: []syntheticResult{
			{err: &llm.ErrTransient{Status: 429, Message: "rate limited"}},
			{stream: nopStream{}},
		},
	}

	runner := fallback.NewRunner(streamer, &fixedResolver{chain: chain})

	stream, err := runner.Stream(context.Background(), llm.GenerationRequest{
		ProfileID: "primary",
		SessionID: "sess-429-test",
	})
	if err != nil {
		t.Fatalf("runner.Stream() error = %v; want nil", err)
	}
	if stream == nil {
		t.Fatal("stream = nil; want non-nil from fallback")
	}

	// Two calls: primary (429) and fallback (success).
	if streamer.cursor != 2 {
		t.Errorf("streamer calls = %d; want 2", streamer.cursor)
	}
}
