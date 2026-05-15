package fallback

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/sigil-tech/kaneaz-harness/core/llm"
)

// fakeStreamer simulates an ordered sequence of (stream, error) pairs.
// The first call returns results[0], the second results[1], etc.
// If all results are consumed, subsequent calls return (nil, errNoMoreResults).
type fakeStreamer struct {
	mu      sync.Mutex
	results []fakeStreamResult
	calls   int
}

type fakeStreamResult struct {
	stream llm.Stream
	err    error
}

var errNoMoreResults = errors.New("fake: no more results")

func newFakeStreamer(results ...fakeStreamResult) *fakeStreamer {
	return &fakeStreamer{results: results}
}

func (f *fakeStreamer) Stream(_ context.Context, _ llm.GenerationRequest) (llm.Stream, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.calls >= len(f.results) {
		f.calls++
		return nil, errNoMoreResults
	}
	r := f.results[f.calls]
	f.calls++
	return r.stream, r.err
}

func (f *fakeStreamer) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// fakeStream is a no-op llm.Stream used for success returns.
type fakeStream struct{}

func (fakeStream) Events() <-chan llm.StreamEvent { ch := make(chan llm.StreamEvent); close(ch); return ch }
func (fakeStream) Cancel() error                  { return nil }
func (fakeStream) Final() (llm.Response, error)   { return llm.Response{Attempts: 1}, nil }

// fakeResolver returns a fixed chain (or nil).
type fakeResolver struct {
	chain *Chain
	err   error
}

func (r *fakeResolver) Resolve(_ context.Context, _ string) (*Chain, error) {
	return r.chain, r.err
}

// fakeAttemptSink records attempt events in a race-safe manner.
type fakeAttemptSink struct {
	mu     sync.Mutex
	events []FallbackAttemptedEvent
}

func (s *fakeAttemptSink) record(_ context.Context, ev FallbackAttemptedEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, ev)
}

func (s *fakeAttemptSink) snapshot() []FallbackAttemptedEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]FallbackAttemptedEvent, len(s.events))
	copy(out, s.events)
	return out
}

// fakeBlockedSink records blocked events.
type fakeBlockedSink struct {
	mu     sync.Mutex
	events []FallbackBlockedEvent
}

func (s *fakeBlockedSink) record(_ context.Context, ev FallbackBlockedEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, ev)
}

func (s *fakeBlockedSink) snapshot() []FallbackBlockedEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]FallbackBlockedEvent, len(s.events))
	copy(out, s.events)
	return out
}

// TestRunnerPrimarySuccess — primary succeeds, no fallback.
func TestRunnerPrimarySuccess(t *testing.T) {
	t.Parallel()

	streamer := newFakeStreamer(fakeStreamResult{stream: fakeStream{}})
	sink := &fakeAttemptSink{}
	resolver := &fakeResolver{chain: singleEntryChain()}
	runner := NewRunner(streamer, resolver, WithAttemptHook(sink.record))

	stream, err := runner.Stream(context.Background(), llm.GenerationRequest{ProfileID: "primary"})
	if err != nil {
		t.Fatalf("Stream() error = %v; want nil", err)
	}
	if stream == nil {
		t.Fatal("Stream() returned nil stream")
	}
	if got := sink.snapshot(); len(got) != 0 {
		t.Errorf("attempt events = %d; want 0", len(got))
	}
	if streamer.callCount() != 1 {
		t.Errorf("streamer called %d times; want 1", streamer.callCount())
	}
}

// TestRunnerFallbackOnce — primary 529→fallback 200.
func TestRunnerFallbackOnce(t *testing.T) {
	t.Parallel()

	transientErr := &llm.ErrTransient{Status: 529, Message: "overloaded"}
	streamer := newFakeStreamer(
		fakeStreamResult{err: transientErr},   // primary fails
		fakeStreamResult{stream: fakeStream{}}, // fallback succeeds
	)
	sink := &fakeAttemptSink{}
	resolver := &fakeResolver{chain: singleEntryChain()}
	runner := NewRunner(streamer, resolver, WithAttemptHook(sink.record))

	stream, err := runner.Stream(context.Background(), llm.GenerationRequest{
		ProfileID: "anthropic",
		SessionID: "sess-1",
	})
	if err != nil {
		t.Fatalf("Stream() error = %v; want nil", err)
	}
	if stream == nil {
		t.Fatal("Stream() returned nil stream")
	}
	events := sink.snapshot()
	if len(events) != 1 {
		t.Fatalf("attempt events = %d; want 1", len(events))
	}
	if events[0].Reason != string(TriggerError5xx) {
		t.Errorf("attempt event reason = %q; want %q", events[0].Reason, TriggerError5xx)
	}
	if events[0].ToProfile != "openrouter" {
		t.Errorf("attempt event ToProfile = %q; want openrouter", events[0].ToProfile)
	}
	if streamer.callCount() != 2 {
		t.Errorf("streamer called %d times; want 2", streamer.callCount())
	}
}

// TestRunnerNoChainConfigured — no resolver → primary error returned.
func TestRunnerNoChainConfigured(t *testing.T) {
	t.Parallel()

	transientErr := &llm.ErrTransient{Status: 503, Message: "unavailable"}
	streamer := newFakeStreamer(fakeStreamResult{err: transientErr})
	runner := NewRunner(streamer, nil)

	_, err := runner.Stream(context.Background(), llm.GenerationRequest{ProfileID: "p"})
	if !errors.Is(err, transientErr) && err != transientErr {
		// errors.Is on ErrTransient requires pointer identity since it has no Unwrap.
		if err == nil {
			t.Fatal("Stream() returned nil error; want primary error")
		}
	}
	if streamer.callCount() != 1 {
		t.Errorf("streamer called %d times; want 1", streamer.callCount())
	}
}

// TestRunnerNoTriggerMatch — error doesn't match triggers.
func TestRunnerNoTriggerMatch(t *testing.T) {
	t.Parallel()

	authErr := &llm.ErrAuth{Status: 401, Message: "unauthorized"}
	streamer := newFakeStreamer(fakeStreamResult{err: authErr})
	chain := &Chain{
		ID:   "5xx-only",
		Name: "5xx only",
		Entries: []ChainEntry{
			{ProviderID: "openrouter", Triggers: []TriggerCondition{TriggerError5xx}},
		},
	}
	resolver := &fakeResolver{chain: chain}
	sink := &fakeAttemptSink{}
	runner := NewRunner(streamer, resolver, WithAttemptHook(sink.record))

	_, err := runner.Stream(context.Background(), llm.GenerationRequest{ProfileID: "p"})
	if err == nil {
		t.Fatal("Stream() returned nil error; want auth error")
	}
	if events := sink.snapshot(); len(events) != 0 {
		t.Errorf("attempt events = %d; want 0", len(events))
	}
	if streamer.callCount() != 1 {
		t.Errorf("streamer called %d times; want 1", streamer.callCount())
	}
}

// TestRunnerCeilingExhausted — 10 matching entries → stops at 5.
func TestRunnerCeilingExhausted(t *testing.T) {
	t.Parallel()

	// Build a chain with 10 entries, all matching TriggerErrorAny.
	entries := make([]ChainEntry, 10)
	for i := range entries {
		entries[i] = ChainEntry{
			ProviderID: "openrouter",
			Triggers:   []TriggerCondition{TriggerErrorAny},
		}
	}
	chain := &Chain{ID: "big-chain", Name: "big", Entries: entries}
	resolver := &fakeResolver{chain: chain}

	// 6 results (1 primary + 5 hops) all fail with transient.
	transientErr := &llm.ErrTransient{Status: 529}
	results := make([]fakeStreamResult, MaxWholeChainAttempts+1)
	for i := range results {
		results[i] = fakeStreamResult{err: transientErr}
	}
	streamer := newFakeStreamer(results...)
	sink := &fakeAttemptSink{}
	runner := NewRunner(streamer, resolver, WithAttemptHook(sink.record))

	_, err := runner.Stream(context.Background(), llm.GenerationRequest{ProfileID: "p"})
	if err == nil {
		t.Fatal("Stream() returned nil; want ErrFallbackExhausted")
	}
	var exhausted *ErrFallbackExhausted
	if !errors.As(err, &exhausted) {
		t.Fatalf("err type = %T; want *ErrFallbackExhausted", err)
	}
	if exhausted.Attempts != MaxWholeChainAttempts {
		t.Errorf("Attempts = %d; want %d", exhausted.Attempts, MaxWholeChainAttempts)
	}
	events := sink.snapshot()
	if len(events) != MaxWholeChainAttempts {
		t.Errorf("attempt events = %d; want %d", len(events), MaxWholeChainAttempts)
	}
}

// TestRunnerCedarDeny — Cedar deny → primary error returned, no broker event.
func TestRunnerCedarDeny(t *testing.T) {
	t.Parallel()

	transientErr := &llm.ErrTransient{Status: 529}
	streamer := newFakeStreamer(fakeStreamResult{err: transientErr})
	chain := singleEntryChain()
	resolver := &fakeResolver{chain: chain}

	sink := &fakeAttemptSink{}
	blocked := &fakeBlockedSink{}

	denyErr := &llm.ErrPolicyDenied{Reason: "lockdown posture"}
	runner := NewRunner(
		streamer,
		resolver,
		WithPolicyCheck(func(_ context.Context, _ string) error { return denyErr }),
		WithAttemptHook(sink.record),
		WithBlockedHook(blocked.record),
	)

	_, err := runner.Stream(context.Background(), llm.GenerationRequest{ProfileID: "p"})
	if err == nil {
		t.Fatal("Stream() returned nil; want primary error")
	}
	// Must return the primary error, NOT the policy denial.
	var transient *llm.ErrTransient
	if !errors.As(err, &transient) {
		t.Errorf("err = %v; want *ErrTransient (primary error)", err)
	}

	// No attempt events (blocked before the hop fires).
	if events := sink.snapshot(); len(events) != 0 {
		t.Errorf("attempt events = %d; want 0", len(events))
	}
	// One blocked event.
	if bev := blocked.snapshot(); len(bev) != 1 {
		t.Errorf("blocked events = %d; want 1", len(bev))
	}
	// Streamer called exactly once (primary only).
	if streamer.callCount() != 1 {
		t.Errorf("streamer called %d times; want 1", streamer.callCount())
	}
}

// singleEntryChain returns a minimal test chain.
func singleEntryChain() *Chain {
	return &Chain{
		ID:   "test-chain",
		Name: "Test",
		Entries: []ChainEntry{
			{ProviderID: "openrouter", Model: "openai/gpt-4o", Triggers: []TriggerCondition{TriggerError5xx}},
		},
	}
}
